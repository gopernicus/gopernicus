// Package tracing is the facility port for distributed tracing. It is
// deliberately minimal: a Tracer starts named spans, and a SpanFinisher carries
// string attributes, records an error, and finishes. That is the whole stdlib
// decoupling boundary — sdk never imports OpenTelemetry. The richer span
// vocabulary (span kind, typed/nested attributes, links, baggage, and any
// exporter) lives in the integrations/tracing/otel module that implements these
// interfaces; this port stays minimal by design (capability-map ruling #4,
// ratified). sdk/tracing itself is stdlib-only.
//
// Noop is the shipped default (named for what it does, like email.Console): it
// starts spans that record nothing, so call sites can start and finish spans
// unconditionally whether or not a real tracer is wired.
//
// SpanIdentity is the optional linkage convention between a tracer's spans and
// sdk/logging's trace_id/span_id fields: a finisher whose span has stable trace
// and span IDs also implements SpanIdentity, so a caller (e.g. tracing.Middleware)
// can stash those IDs via sdk.WithTraceID/WithSpanID and have them appear on log
// lines. An implementer that adds it must keep the method set in sync with this
// interface; integrations/tracing/otel satisfies it, Noop does not.
package tracing

import "context"

// Tracer creates spans for an operation. An integration (e.g. OpenTelemetry)
// implements this to add tracing without sdk importing a tracing library.
type Tracer interface {
	// StartSpan begins a span as a child of any span already in ctx and returns
	// a context carrying the new span plus a SpanFinisher to complete it.
	StartSpan(ctx context.Context, operationName string) (context.Context, SpanFinisher)
}

// SpanFinisher completes a span after its operation returns.
type SpanFinisher interface {
	// SetAttributes attaches key-value metadata to the span.
	SetAttributes(attrs ...Attribute)

	// RecordError records an error on the span and marks it failed.
	RecordError(err error)

	// Finish completes the span. Call it exactly once.
	Finish()
}

// SpanIdentity is an optional interface a SpanFinisher may also implement to
// expose the stable identity of its span. It is the compile-checked home for
// the cross-module method-set contract that links a tracer's spans to
// sdk/logging's trace_id/span_id fields: a caller type-asserts it on the
// returned SpanFinisher and, when it is satisfied with non-empty IDs, stashes
// them via sdk.WithTraceID/WithSpanID so they land on log lines. Optional;
// implementations without stable span identity simply omit it.
type SpanIdentity interface {
	TraceID() string
	SpanID() string
}

// Attribute is a key-value pair of span metadata. The port is intentionally
// string-only; richer attribute value types belong in the otel integration.
type Attribute struct {
	Key   string
	Value string
}

// StringAttribute builds an Attribute with a string value.
func StringAttribute(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

// Noop is the default Tracer: it starts spans that record nothing, letting call
// sites start and finish spans unconditionally when no real tracer is wired.
type Noop struct{}

var _ Tracer = Noop{}

// StartSpan returns ctx unchanged and a no-op finisher.
func (Noop) StartSpan(ctx context.Context, operationName string) (context.Context, SpanFinisher) {
	return ctx, noopSpanFinisher{}
}

// noopSpanFinisher discards everything, so unconditional span calls are safe
// when no Tracer is configured.
type noopSpanFinisher struct{}

var _ SpanFinisher = noopSpanFinisher{}

func (noopSpanFinisher) SetAttributes(...Attribute) {}
func (noopSpanFinisher) RecordError(error)          {}
func (noopSpanFinisher) Finish()                    {}
