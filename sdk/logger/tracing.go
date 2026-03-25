package logger

import (
	"context"
	"log/slog"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const (
	traceIDKey   contextKey = "trace_id"
	spanIDKey    contextKey = "span_id"
	requestIDKey contextKey = "request_id"
)

// WithTraceID returns a context with the given trace ID attached.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// WithSpanID returns a context with the given span ID attached.
func WithSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, spanIDKey, id)
}

// WithRequestID returns a context with the given request ID attached.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// TracingHandler is a slog.Handler that injects trace_id, span_id, and
// request_id from the context into every log record. It wraps another handler
// and delegates all actual output to it.
//
// Values are only added if present in the context. Missing values are skipped.
type TracingHandler struct {
	inner slog.Handler
}

// NewTracingHandler wraps the given handler with trace/request ID injection.
func NewTracingHandler(inner slog.Handler) *TracingHandler {
	return &TracingHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *TracingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace/request IDs from context, then delegates to the inner handler.
func (h *TracingHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(traceIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	if id, ok := ctx.Value(spanIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("span_id", id))
	}
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new TracingHandler wrapping the inner handler's WithAttrs.
func (h *TracingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TracingHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a new TracingHandler wrapping the inner handler's WithGroup.
func (h *TracingHandler) WithGroup(name string) slog.Handler {
	return &TracingHandler{inner: h.inner.WithGroup(name)}
}
