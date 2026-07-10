package tracing

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Middleware returns HTTP middleware that wraps each request in a span on t. A
// nil tracer is treated as Noop, so the middleware can be wired unconditionally.
//
// This is a capability×foundation composition (a tracing capability producing a
// web.Middleware), so it lives with the tracing semantics rather than in web:
// web stays agnostic of tracing, tracing legally depends on web (capability →
// foundation) and reads/writes the trace_id/span_id via the kernel's
// request-identity vocabulary (sdk.WithTraceID/WithSpanID), so it never imports
// logging.
//
// Place this middleware OUTER of web.Logger. Two consequences hang on that
// order:
//   - The span's context — and, when t returns a SpanFinisher implementing
//     SpanIdentity, the trace_id/span_id stashed on it — is already on the
//     request when Logger emits its access line, so those IDs appear on the
//     request log via logging.TracingHandler.
//   - web.RecordError type-asserts the response writer directly with no Unwrap
//     walk. This middleware records a 5xx onto the span with a synthesized error
//     and never touches the writer's recorded error, so a handler's RecordError
//     keeps landing on Logger's inner StatusRecorder — the access line's error
//     field does not silently regress.
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
			name := r.Pattern
			if name == "" {
				name = "http.request"
			}

			ctx, span := t.StartSpan(r.Context(), name)
			defer span.Finish()

			attrs := []Attribute{
				StringAttribute("http.method", r.Method),
				StringAttribute("http.host", r.Host),
				StringAttribute("user_agent", r.UserAgent()),
				StringAttribute("net.peer.ip", peerHost(r.RemoteAddr)),
			}
			if r.Pattern != "" {
				attrs = append(attrs, StringAttribute("http.route", r.Pattern))
			}
			span.SetAttributes(attrs...)

			if id, ok := span.(SpanIdentity); ok {
				if traceID := id.TraceID(); traceID != "" {
					ctx = sdk.WithTraceID(ctx, traceID)
				}
				if spanID := id.SpanID(); spanID != "" {
					ctx = sdk.WithSpanID(ctx, spanID)
				}
			}

			sw := web.NewStatusRecorder(w)
			next.ServeHTTP(sw, r.WithContext(ctx))

			span.SetAttributes(StringAttribute("http.status_code", strconv.Itoa(sw.Status())))
			if sw.Status() >= 500 {
				span.RecordError(fmt.Errorf("server error: %d", sw.Status()))
			}
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
