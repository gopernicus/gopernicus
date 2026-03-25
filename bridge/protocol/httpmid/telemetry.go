// Package httpmid provides HTTP middleware that bridges sdk/web with
// infrastructure concerns like telemetry.
package httpmid

import (
	"net"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// TelemetryMiddleware returns standard HTTP middleware that creates OpenTelemetry
// server spans for each incoming request.
//
// Example:
//
//	tracer := provider.Tracer()
//	mux := http.NewServeMux()
//	handler := httpmid.TelemetryMiddleware(tracer)(mux)
//	http.ListenAndServe(":8080", handler)
func TelemetryMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Prefer route pattern for low cardinality span names.
			// Go 1.22+ ServeMux sets r.Pattern for matched routes.
			spanName := "HTTP IN " + r.Method + " " + r.URL.Path
			if r.Pattern != "" {
				spanName = "HTTP IN " + r.Pattern
			}

			ctx, span := tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.host", r.Host),
				attribute.String("http.user_agent", r.UserAgent()),
				attribute.String("net.peer.ip", extractIP(r.RemoteAddr)),
			)

			if r.Pattern != "" {
				span.SetAttributes(attribute.String("http.route", r.Pattern))
			}

			if r.ContentLength > 0 {
				span.SetAttributes(attribute.Int64("http.request_content_length", r.ContentLength))
			}

			rc := web.NewResponseCapture(w)
			next.ServeHTTP(rc, r.WithContext(ctx))

			span.SetAttributes(
				attribute.Int("http.status_code", rc.StatusCode),
				attribute.Int64("http.response_content_length", rc.BytesWritten),
			)

			if rc.StatusCode >= 500 {
				span.SetStatus(codes.Error, "server error")
			}
		})
	}
}

func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
