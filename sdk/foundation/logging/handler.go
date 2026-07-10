package logging

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk"
)

// TracingHandler is a slog.Handler that injects trace_id, span_id, and
// request_id into every log record, reading them from the kernel's
// request-identity vocabulary (sdk.TraceIDFromContext and friends). It wraps
// another handler and delegates all actual output to it. Each value is only
// added when present in the context; missing or empty values are skipped.
type TracingHandler struct {
	inner slog.Handler
}

// NewTracingHandler wraps the given handler with trace/span/request-id injection.
func NewTracingHandler(inner slog.Handler) *TracingHandler {
	return &TracingHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *TracingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace, span, and request IDs from context, then delegates to
// the inner handler.
func (h *TracingHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := sdk.TraceIDFromContext(ctx); ok {
		r.AddAttrs(slog.String("trace_id", id))
	}
	if id, ok := sdk.SpanIDFromContext(ctx); ok {
		r.AddAttrs(slog.String("span_id", id))
	}
	if id, ok := sdk.RequestIDFromContext(ctx); ok {
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
