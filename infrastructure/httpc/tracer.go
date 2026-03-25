package httpc

import (
	"net/http"

	"github.com/gopernicus/gopernicus/telemetry"
)

// NewTracingTransport wraps an http.RoundTripper with OpenTelemetry client span
// tracing. Each outbound request gets a span with method, URL, and status
// attributes. Trace context is propagated via request headers.
//
// Example:
//
//	tracer := provider.Tracer()
//	client := httpc.NewClient(
//	    httpc.WithTransport(httpc.NewTracingTransport(tracer, http.DefaultTransport)),
//	)
func NewTracingTransport(tracer telemetry.Tracer, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{tracer: tracer, base: base}
}

type tracingTransport struct {
	tracer telemetry.Tracer
	base   http.RoundTripper
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	spanName := "HTTP OUT " + req.Method
	if req.URL != nil && req.URL.Host != "" {
		spanName = "HTTP OUT " + req.Method + " " + req.URL.Host
	}

	ctx, span := telemetry.StartClientSpan(req.Context(), t.tracer, spanName)
	defer span.End()

	telemetry.AddAttribute(span, "http.method", req.Method)
	telemetry.AddAttribute(span, "http.url", sanitizeURL(req))

	if req.URL != nil {
		telemetry.AddAttribute(span, "net.peer.name", req.URL.Host)
		telemetry.AddAttribute(span, "http.scheme", req.URL.Scheme)
	}

	telemetry.InjectContext(ctx, req.Header)

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		telemetry.RecordError(span, err)
		return resp, err
	}

	if resp != nil {
		telemetry.AddIntAttribute(span, "http.status_code", resp.StatusCode)
		if resp.StatusCode >= 500 {
			telemetry.SetSpanError(span, "server error")
		}
	}

	return resp, nil
}

// sanitizeURL returns the URL without query parameters to avoid leaking sensitive data.
func sanitizeURL(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	u := *req.URL
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
