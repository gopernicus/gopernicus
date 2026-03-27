package workers

import "context"

// Tracer creates spans for worker operations. Infrastructure packages
// (e.g., OpenTelemetry) implement this interface to add tracing to job
// processing without the SDK importing external tracing dependencies.
type Tracer interface {
	// StartSpan creates a new span as a child of any existing span in the context.
	// Returns a context with the span and a SpanFinisher to complete it.
	StartSpan(ctx context.Context, operationName string) (context.Context, SpanFinisher)
}

// SpanFinisher completes a span after the operation finishes.
type SpanFinisher interface {
	// SetAttributes adds key-value metadata to the span.
	SetAttributes(attrs ...Attribute)

	// RecordError records an error on the span and marks it as failed.
	RecordError(err error)

	// Finish completes the span. Must be called exactly once.
	Finish()
}

// Attribute is a key-value pair for span metadata.
type Attribute struct {
	Key   string
	Value string
}

// StringAttribute creates an Attribute with a string value.
func StringAttribute(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

// noopSpanFinisher is a no-op implementation of SpanFinisher used when no
// Tracer is configured. It allows call sites to call span methods unconditionally.
type noopSpanFinisher struct{}

func (noopSpanFinisher) SetAttributes(...Attribute) {}
func (noopSpanFinisher) RecordError(error)           {}
func (noopSpanFinisher) Finish()                     {}
