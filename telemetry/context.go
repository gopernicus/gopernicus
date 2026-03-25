package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// TraceID extracts the trace ID from the context.
// Returns empty string if no valid trace context exists.
func TraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

// SpanID extracts the span ID from the context.
// Returns empty string if no valid span context exists.
func SpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return ""
	}
	return span.SpanContext().SpanID().String()
}

// SpanFromContext returns the current span from context.
// Returns a no-op span if no span exists.
func SpanFromContext(ctx context.Context) Span {
	return trace.SpanFromContext(ctx)
}
