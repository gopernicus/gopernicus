// Package telemetry provides OpenTelemetry-based distributed tracing.
// It wraps OTel's TracerProvider and provides helpers so the rest of the
// codebase doesn't need to import OTel directly.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Provider wraps OTel's TracerProvider and manages telemetry lifecycle.
type Provider struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// NewProvider creates a Provider from an OTel TracerProvider.
// The serviceName is used as the default tracer name.
func NewProvider(tp *sdktrace.TracerProvider, serviceName string) *Provider {
	return &Provider{
		tp:     tp,
		tracer: tp.Tracer(serviceName),
	}
}

// Tracer returns the primary tracer for the service.
func (p *Provider) Tracer() Tracer {
	if p == nil || p.tracer == nil {
		return noop.NewTracerProvider().Tracer("")
	}
	return p.tracer
}

// NamedTracer returns a tracer with a specific name.
// Use this when you want spans grouped under a different name (e.g., a submodule).
func (p *Provider) NamedTracer(name string) Tracer {
	if p == nil || p.tp == nil {
		return noop.NewTracerProvider().Tracer(name)
	}
	return p.tp.Tracer(name)
}

// Shutdown flushes any pending spans and shuts down the provider.
// Should be called on application shutdown.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}

// RegisterGlobal sets this provider as the global OTel tracer provider.
// This allows StartSpan() to work and libraries that use otel.GetTracerProvider()
// to use this provider.
func (p *Provider) RegisterGlobal() {
	if p == nil || p.tp == nil {
		return
	}
	otel.SetTracerProvider(p.tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
}
