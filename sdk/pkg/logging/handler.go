package logging

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	traceIDKey   = "trace_id"
	spanIDKey    = "span_id"
	requestIDKey = "request_id"
)

// ContextHandler adds trace_id, span_id, and request_id from sdk context
// helpers before passing each record to an inner handler. Missing or empty
// IDs are skipped, as is an ID the record already carries (web.Logger adds
// request_id itself); an ID bound earlier with WithAttrs is not seen, so it
// repeats. IDs follow any group set with WithGroup.
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
	var hasTrace, hasSpan, hasRequest bool
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case traceIDKey:
			hasTrace = true
		case spanIDKey:
			hasSpan = true
		case requestIDKey:
			hasRequest = true
		}
		return true
	})
	r = r.Clone()
	if id, ok := sdk.TraceIDFromContext(ctx); ok && !hasTrace {
		r.AddAttrs(slog.String(traceIDKey, id))
	}
	if id, ok := sdk.SpanIDFromContext(ctx); ok && !hasSpan {
		r.AddAttrs(slog.String(spanIDKey, id))
	}
	if id, ok := sdk.RequestIDFromContext(ctx); ok && !hasRequest {
		r.AddAttrs(slog.String(requestIDKey, id))
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
