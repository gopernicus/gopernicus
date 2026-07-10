package sdk

import "context"

// The request-identity vocabulary below is the kernel's second deliberate
// promotion (2026-07-10, sdk-layering P2). It is the cross-cutting trace/span/
// request-id context vocabulary every tier touches: web stamps a request id,
// the tracing capability stashes trace/span ids off a span's identity, and
// logging reads all three onto its log lines. Homing the keys and their
// accessors in the kernel is what lets web and tracing carry these ids without
// importing logging — the foundation->foundation edge the layering forbids.
// The first promotion was the sentinel errors; the kernel contract (stdlib
// only, promotion is a visible act) lives in errors.go.

// contextKey is the kernel's unexported context-key type, so no other package
// can collide with these keys.
type contextKey string

const (
	traceIDKey   contextKey = "trace_id"
	spanIDKey    contextKey = "span_id"
	requestIDKey contextKey = "request_id"
)

// WithTraceID returns a context carrying the given trace ID.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// WithSpanID returns a context carrying the given span ID.
func WithSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, spanIDKey, id)
}

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// TraceIDFromContext returns the trace ID attached to ctx and whether a
// non-empty one was present.
func TraceIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(traceIDKey).(string)
	return id, ok && id != ""
}

// SpanIDFromContext returns the span ID attached to ctx and whether a non-empty
// one was present.
func SpanIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(spanIDKey).(string)
	return id, ok && id != ""
}

// RequestIDFromContext returns the request ID attached to ctx and whether a
// non-empty one was present.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok && id != ""
}
