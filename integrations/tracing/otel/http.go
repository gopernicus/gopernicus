package otel

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	oteltrace "go.opentelemetry.io/otel/trace"

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
}

// Middleware starts server spans with current typed HTTP attributes and reuses
// SDK response completion. Mount inside routing (so r.Pattern is known), outside
// web.Logger and web.Panics. No raw URL, query, body or arbitrary headers are recorded.
func Middleware(t *Tracer, cfg HTTPConfig) web.Middleware {
	if t == nil {
		return tracing.Middleware(nil)
	}
	return tracing.Middleware(serverTracer{Tracer: t, config: cfg})
}

type serverTracer struct {
	*Tracer
	config HTTPConfig
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
	route := r.Pattern
	if _, rest, found := strings.Cut(route, " "); found {
		route = rest
	}
	// Go patterns can include a host before the route. Never use the request path.
	if index := strings.IndexByte(route, '/'); index >= 0 {
		route = route[index:]
	}
	name := method
	if method == "_OTHER" {
		name = "HTTP"
	}
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
	return ctx, &httpSpanFinisher{spanFinisher: &spanFinisher{span: span}}
}

type httpSpanFinisher struct{ *spanFinisher }

var _ tracing.HTTPSpan = (*httpSpanFinisher)(nil)

func (f *httpSpanFinisher) SetHTTPStatus(status int) {
	f.span.SetAttributes(semconv.HTTPResponseStatusCode(status))
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
