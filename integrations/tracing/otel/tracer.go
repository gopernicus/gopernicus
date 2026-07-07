package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/gopernicus/gopernicus/sdk/tracing"
)

var (
	_ tracing.Tracer       = (*Tracer)(nil)
	_ tracing.SpanFinisher = (*spanFinisher)(nil)
	_ tracing.SpanIdentity = (*spanFinisher)(nil)
)

// Tracer implements sdk/tracing.Tracer over an OpenTelemetry tracer. Build one
// with Open; pass it wherever an sdk/tracing.Tracer is accepted. It is safe for
// concurrent use.
type Tracer struct {
	tracer   oteltrace.Tracer
	provider *sdktrace.TracerProvider // nil in ExporterProvider mode (caller owns it)
}

// newOwnedTracer wraps a provider this module created so Shutdown flushes and
// stops it.
func newOwnedTracer(tp *sdktrace.TracerProvider, name string) *Tracer {
	return &Tracer{tracer: tp.Tracer(name), provider: tp}
}

// StartSpan begins an OpenTelemetry span as a child of any span already in ctx
// and returns a context carrying it plus a finisher.
func (t *Tracer) StartSpan(ctx context.Context, operationName string) (context.Context, tracing.SpanFinisher) {
	ctx, span := t.tracer.Start(ctx, operationName)
	return ctx, &spanFinisher{span: span}
}

// Shutdown flushes any buffered spans and stops the TracerProvider this module
// created. It is a no-op in ExporterProvider mode, where the caller owns the
// provider's lifecycle, and safe to call on a nil *Tracer.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	return t.provider.Shutdown(ctx)
}

// ForceFlush exports any spans the provider has buffered without stopping it. It
// is a no-op in ExporterProvider mode and on a nil *Tracer.
func (t *Tracer) ForceFlush(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	return t.provider.ForceFlush(ctx)
}

// spanFinisher adapts an OpenTelemetry span to sdk/tracing.SpanFinisher.
type spanFinisher struct {
	span oteltrace.Span
}

// SetAttributes attaches the string attributes to the span.
func (f *spanFinisher) SetAttributes(attrs ...tracing.Attribute) {
	if len(attrs) == 0 {
		return
	}
	kvs := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		kvs[i] = attribute.String(a.Key, a.Value)
	}
	f.span.SetAttributes(kvs...)
}

// RecordError records err on the span and marks the span failed.
func (f *spanFinisher) RecordError(err error) {
	if err == nil {
		return
	}
	f.span.RecordError(err)
	f.span.SetStatus(codes.Error, err.Error())
}

// Finish ends the span. Call it exactly once.
func (f *spanFinisher) Finish() {
	f.span.End()
}

// TraceID returns the span's trace ID as a hex string, satisfying
// tracing.SpanIdentity so a caller (e.g. web.Tracing) can stash it via
// logging.WithTraceID. It returns "" when the span context has no valid trace ID.
func (f *spanFinisher) TraceID() string {
	sc := f.span.SpanContext()
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanID returns the span's span ID as a hex string, satisfying
// tracing.SpanIdentity. It returns "" when the span context has no valid span ID.
func (f *spanFinisher) SpanID() string {
	sc := f.span.SpanContext()
	if !sc.HasSpanID() {
		return ""
	}
	return sc.SpanID().String()
}
