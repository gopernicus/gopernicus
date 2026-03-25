package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// StartSpan creates a new span as a child of the current span in context.
// Uses the global tracer (set via Provider.RegisterGlobal).
func StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return otel.Tracer("").Start(ctx, name)
}

// StartSpanWithTracer creates a new span using a specific tracer.
func StartSpanWithTracer(ctx context.Context, tracer Tracer, name string) (context.Context, Span) {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("")
	}
	return tracer.Start(ctx, name)
}

// AddAttribute adds a string attribute to a span.
func AddAttribute(span Span, key, value string) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.String(key, value))
}

// AddAttributes adds multiple string attributes to a span.
func AddAttributes(span Span, attrs map[string]string) {
	if span == nil || !span.IsRecording() {
		return
	}
	for k, v := range attrs {
		span.SetAttributes(attribute.String(k, v))
	}
}

// RecordError records an error on a span and sets the span status to error.
func RecordError(span Span, err error) {
	if span == nil || !span.IsRecording() || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// AddIntAttribute adds an integer attribute to a span.
func AddIntAttribute(span Span, key string, value int) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.Int(key, value))
}

// AddInt64Attribute adds an int64 attribute to a span.
func AddInt64Attribute(span Span, key string, value int64) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.Int64(key, value))
}

// AddBoolAttribute adds a boolean attribute to a span.
func AddBoolAttribute(span Span, key string, value bool) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.Bool(key, value))
}

// AddEvent adds a timestamped event to a span.
func AddEvent(span Span, name string) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.AddEvent(name)
}

// SetSpanError sets the span status to error with the given message.
func SetSpanError(span Span, message string) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetStatus(codes.Error, message)
}

// ExtractContext extracts trace context from incoming HTTP request headers.
func ExtractContext(ctx context.Context, header http.Header) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
}

// InjectContext injects trace context into outgoing HTTP request headers.
func InjectContext(ctx context.Context, header http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
}

// StartServerSpan creates a new span for an incoming HTTP request.
func StartServerSpan(ctx context.Context, tracer Tracer, name string) (context.Context, Span) {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("")
	}
	return tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
}

// StartClientSpan creates a new span for an outgoing request.
func StartClientSpan(ctx context.Context, tracer Tracer, name string) (context.Context, Span) {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("")
	}
	return tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
}
