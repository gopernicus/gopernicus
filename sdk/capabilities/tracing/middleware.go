package tracing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Middleware returns HTTP middleware that wraps each request in a span on t. A
// nil tracer is treated as Noop, so the middleware can be wired unconditionally.
//
// This is a capability×pkg composition (a tracing capability producing a
// web.Middleware), so it lives with the tracing semantics rather than in web:
// web stays agnostic of tracing, tracing legally depends on web (capability →
// pkg) and reads/writes the trace_id/span_id via the kernel's
// request-identity vocabulary (sdk.WithTraceID/WithSpanID), so it never imports
// logging.
//
// Place this middleware OUTER of web.Logger. Two consequences hang on that
// order:
//   - The span's context — and, when t returns a SpanFinisher implementing
//     SpanIdentity, the trace_id/span_id stashed on it — is already on the
//     request when Logger emits its access line, so those IDs appear on the
//     request log via logging.ContextHandler.
//   - Nested StatusRecorders forward original response errors to Logger. This
//     middleware records a 5xx onto the span with a synthesized error without
//     replacing the original cause in the access log.
//
// The span name is r.Pattern alone (it already embeds the method, e.g.
// "GET /posts/{id}", because Handle wraps middleware inside the mux match, so
// the pattern is populated when this runs). An empty pattern falls back to the
// static "http.request", never r.URL.Path, to keep span-name cardinality bounded.
//
// Cost: the middleware builds a per-request attribute slice and parses
// RemoteAddr even when wired with Noop (parity with Logger's per-request
// attribute build); there is no Noop fast path, so a host that cares about that
// cost simply omits the middleware.
func Middleware(t Tracer) web.Middleware {
	if t == nil {
		t = Noop{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ctx context.Context
			var span SpanFinisher
			var attrs []Attribute
			if httpTracer, ok := t.(HTTPTracer); ok {
				ctx, span = httpTracer.StartHTTPSpan(r)
			} else {
				name := r.Pattern
				if name == "" {
					name = "http.request"
				}
				ctx, span = t.StartSpan(r.Context(), name)
				attrs = []Attribute{
					StringAttribute("http.method", r.Method),
					StringAttribute("http.host", r.Host),
					StringAttribute("user_agent", r.UserAgent()),
					StringAttribute("net.peer.ip", peerHost(r.RemoteAddr)),
				}
				if r.Pattern != "" {
					attrs = append(attrs, StringAttribute("http.route", r.Pattern))
				}
			}

			sw := web.NewStatusRecorder(w)
			defer func() {
				panicValue := recover()
				defer span.Finish()
				// A normal empty handler completes with implicit 200. An escaping
				// panic or a hijack before headers has no known response status.
				status := sw.Status()
				if status != 0 && (panicValue == nil || sw.Committed()) {
					if httpSpan, ok := span.(HTTPSpan); ok {
						httpSpan.SetHTTPStatus(status)
					} else {
						span.SetAttributes(StringAttribute("http.status_code", strconv.Itoa(status)))
					}
				}
				switch {
				case panicValue != nil:
					span.RecordError(errors.New("http handler panic"))
				case sw.Err() != nil:
					span.RecordError(errors.New("http response failed"))
				case status >= 500:
					span.RecordError(fmt.Errorf("server error: %d", status))
				}
				// Do not overwrite the writer's cause; Logger needs the original.
				if panicValue != nil {
					panic(panicValue)
				}
			}()
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}
			if id, ok := span.(SpanIdentity); ok {
				if traceID := id.TraceID(); traceID != "" {
					ctx = sdk.WithTraceID(ctx, traceID)
				}
				if spanID := id.SpanID(); spanID != "" {
					ctx = sdk.WithSpanID(ctx, spanID)
				}
			}

			next.ServeHTTP(sw, r.WithContext(ctx))
		})
	}
}

// peerHost returns the host portion of a RemoteAddr ("host:port"), falling back
// to the raw value when it carries no port.
func peerHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// HTTPTracer optionally creates an HTTP span with protocol metadata available at
// creation time (including to a sampler). It owns the request attributes; the
// middleware still owns completion and request-context identity propagation.
type HTTPTracer interface {
	StartHTTPSpan(*http.Request) (context.Context, SpanFinisher)
}

// HTTPSpan optionally receives native HTTP status metadata instead of the
// generic string attribute. A status is supplied only when it is known.
type HTTPSpan interface {
	SetHTTPStatus(int)
}
