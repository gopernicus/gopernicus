package otel

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/gopernicus/gopernicus/sdk/capabilities/metrics"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// HTTPConfig selects the host's inbound propagation policy.
type HTTPConfig struct {
	// TrustTraceContext accepts W3C traceparent/tracestate from callers. With
	// parent-based sampling, an accepted caller can also select the sampling flag.
	// False ignores these headers and retains any existing local context parent.
	// Baggage is never extracted by this middleware.
	TrustTraceContext bool

	// Meter, when set, records http.server.request.duration (seconds) for
	// every request as its span finishes. Its attributes come from a fixed
	// allowlist: http.request.method (bounded; _OTHER otherwise), http.route
	// (the matched template from RouteNames, "unmatched" for a 404/405 the mux
	// matched nothing for, absent when no route is known) and
	// http.response.status_code as a class ("2xx" … "5xx"). Never the path,
	// query, user agent or peer address. Nil records no metrics.
	Meter metrics.Meter
}

// requestDurationSpec is the HTTP server duration histogram's spec.
var requestDurationSpec = metrics.Spec{
	Name:        "http.server.request.duration",
	Description: "Duration of HTTP server requests.",
	Unit:        "s",
}

// Middleware starts server spans with current typed HTTP attributes and reuses
// SDK response completion. Mount it as a global web.WebHandler.Use, outside
// web.Logger and web.Panics, and add RouteNames() as the LAST Use so spans are
// named by the matched route; without it a global span is named by its method
// alone. Mounted per route instead, it sees r.Pattern directly and needs no
// RouteNames. No raw URL, query, body or arbitrary headers are recorded.
func Middleware(t *Tracer, cfg HTTPConfig) web.Middleware {
	if t == nil {
		return tracing.Middleware(nil)
	}
	st := serverTracer{Tracer: t, config: cfg}
	if cfg.Meter != nil {
		st.duration = cfg.Meter.Histogram(requestDurationSpec)
	}
	return tracing.Middleware(st)
}

// RouteNames records the route the mux matched so Middleware can name its span
// by it. Install it as the LAST web.WebHandler.Use, directly on the mux: the
// mux writes r.Pattern only onto the request it receives, so any layer that
// clones the request (r.WithContext) never sees it.
//
// A matched request's span is named "<METHOD> <route>" with http.route set. A
// request the mux matched nothing for (404, 405) is named "<METHOD> unmatched",
// never by its path, which can carry ids and tokens. Installed anywhere else it
// sees no pattern, and spans keep their method-only names. Without an outer
// Middleware it does nothing.
func RouteNames() web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if slot, ok := r.Context().Value(routeSlotKey{}).(*routeSlot); ok {
					slot.seen, slot.pattern = true, r.Pattern
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// routeSlotKey keys the per-request *routeSlot Middleware stashes for
// RouteNames.
type routeSlotKey struct{}

// routeSlot carries the matched pattern from RouteNames, innermost, back to the
// span Middleware finishes. One request goroutine writes it before Finish
// reads it, so it needs no lock.
type routeSlot struct {
	seen    bool
	pattern string
}

// routeFromPattern returns a ServeMux pattern's path part: the method prefix
// and any host are dropped. It never uses the request path.
func routeFromPattern(pattern string) string {
	route := pattern
	if _, rest, found := strings.Cut(route, " "); found {
		route = rest
	}
	if index := strings.IndexByte(route, '/'); index >= 0 {
		route = route[index:]
	}
	return route
}

type serverTracer struct {
	*Tracer
	config HTTPConfig
	// duration is the request-duration histogram when config.Meter is set.
	duration metrics.Histogram
}

var _ tracing.HTTPTracer = serverTracer{}

func (t serverTracer) StartHTTPSpan(r *http.Request) (context.Context, tracing.SpanFinisher) {
	ctx := r.Context()
	if t.config.TrustTraceContext {
		ctx = (propagation.TraceContext{}).Extract(ctx, propagation.HeaderCarrier(r.Header))
	}
	method := r.Method
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE", "QUERY":
	default:
		method = "_OTHER"
	}
	// Go patterns can include a host before the route. Never use the request path.
	route := routeFromPattern(r.Pattern)
	name := method
	if method == "_OTHER" {
		name = "HTTP"
	}
	prefix := name
	attrs := []attribute.KeyValue{semconv.HTTPRequestMethodKey.String(method)}
	if route != "" {
		name += " " + route
		attrs = append(attrs, semconv.HTTPRoute(route))
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	attrs = append(attrs, semconv.URLScheme(scheme))
	if host, err := url.Parse("//" + r.Host); err == nil && host.Hostname() != "" {
		attrs = append(attrs, semconv.ServerAddress(host.Hostname()))
		if port, err := strconv.Atoi(host.Port()); err == nil && port > 0 && port <= 65535 {
			attrs = append(attrs, semconv.ServerPort(port))
		}
	}
	if host, portText, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		attrs = append(attrs, semconv.NetworkPeerAddress(host))
		if port, err := strconv.Atoi(portText); err == nil && port > 0 && port <= 65535 {
			attrs = append(attrs, semconv.NetworkPeerPort(port))
		}
	}
	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKindServer), oteltrace.WithAttributes(attrs...))
	slot := &routeSlot{}
	ctx = context.WithValue(ctx, routeSlotKey{}, slot)
	return ctx, &httpSpanFinisher{spanFinisher: &spanFinisher{span: span, recordErrorText: t.recordErrorText}, prefix: prefix, slot: slot,
		method: method, startRoute: route, duration: t.duration, ctx: ctx, started: time.Now()}
}

type httpSpanFinisher struct {
	*spanFinisher
	// prefix is the span name's method part ("GET", or "HTTP" for _OTHER).
	prefix string
	// slot is filled by RouteNames; status is the recorded response status.
	slot   *routeSlot
	status int

	// method, startRoute, duration, ctx and started feed the request-duration
	// histogram (nil duration: no metrics).
	method     string
	startRoute string
	duration   metrics.Histogram
	ctx        context.Context
	started    time.Time
}

var _ tracing.HTTPSpan = (*httpSpanFinisher)(nil)

func (f *httpSpanFinisher) SetHTTPStatus(status int) {
	f.status = status
	f.span.SetAttributes(semconv.HTTPResponseStatusCode(status))
}

// Finish names the span from the route RouteNames recorded, then ends it. A
// matched pattern names it by route; no pattern with a 404 or 405 names it
// "<prefix> unmatched". No pattern with any other status means RouteNames was
// not directly on the mux, so the start-time name stays.
func (f *httpSpanFinisher) Finish() {
	route := f.startRoute
	if f.slot != nil && f.slot.seen {
		if matched := routeFromPattern(f.slot.pattern); matched != "" {
			route = matched
			f.span.SetName(f.prefix + " " + route)
			f.span.SetAttributes(semconv.HTTPRoute(route))
		} else if f.status == http.StatusNotFound || f.status == http.StatusMethodNotAllowed {
			route = "unmatched"
			f.span.SetName(f.prefix + " unmatched")
		}
	}
	f.recordDuration(route)
	f.spanFinisher.Finish()
}

// recordDuration records the request on the duration histogram with the
// allowlisted attributes only.
func (f *httpSpanFinisher) recordDuration(route string) {
	if f.duration == nil {
		return
	}
	attrs := []metrics.Attribute{metrics.StringAttribute("http.request.method", f.method)}
	if route != "" {
		attrs = append(attrs, metrics.StringAttribute("http.route", route))
	}
	if f.status >= 100 && f.status < 600 {
		attrs = append(attrs, metrics.StringAttribute("http.response.status_code", strconv.Itoa(f.status/100)+"xx"))
	}
	f.duration.Record(f.ctx, time.Since(f.started).Seconds(), attrs...)
}

// Automatic middleware errors use a fixed category; arbitrary error text is not
// promoted to a high-cardinality semantic attribute.
func (f *httpSpanFinisher) RecordError(err error) {
	if err == nil {
		return
	}
	f.span.SetAttributes(attribute.String("error.type", "http.request_failed"))
	f.spanFinisher.RecordError(err)
}
