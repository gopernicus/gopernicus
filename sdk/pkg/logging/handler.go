package logging

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk"
)

// ContextHandler adds trace_id, span_id, and request_id from sdk context
// helpers before passing each record to an inner handler. Missing or empty
// IDs are skipped. IDs follow any group set with WithGroup.
type ContextHandler struct {
	inner slog.Handler
}

// NewContextHandler wraps a handler with context ID injection.
func NewContextHandler(inner slog.Handler) *ContextHandler {
	return &ContextHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace, span, and request IDs from context, then delegates to
// the inner handler.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
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

// WithAttrs returns a new ContextHandler wrapping the inner handler's WithAttrs.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a new ContextHandler wrapping the inner handler's WithGroup.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{inner: h.inner.WithGroup(name)}
}
