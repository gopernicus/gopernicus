// Package stdouttrace provides a stdout exporter adapter for telemetry.
// This prints spans to stdout for local development and debugging.
package stdouttrace

import (
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	otelstdout "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/gopernicus/gopernicus/telemetry"
)

// Options configures the console exporter.
type Options struct {
	ServiceName string
	PrettyPrint bool
	Timestamps  bool
}

// New creates a new telemetry Provider that outputs to stdout.
// Useful for local development where you want to see spans in terminal.
func New(opts Options) (*telemetry.Provider, error) {
	exporterOpts := []otelstdout.Option{
		otelstdout.WithWriter(os.Stdout),
	}
	if opts.PrettyPrint {
		exporterOpts = append(exporterOpts, otelstdout.WithPrettyPrint())
	}
	if !opts.Timestamps {
		exporterOpts = append(exporterOpts, otelstdout.WithoutTimestamps())
	}

	exporter, err := otelstdout.New(exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating console exporter: %w", err)
	}

	// Use NewSchemaless so resource.Merge never conflicts with whatever
	// schema version resource.Default() picks up from detectors.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", opts.ServiceName),
			attribute.String("deployment.environment.name", "development"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	// Synchronous exporter so spans appear immediately in dev.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(res),
	)

	return telemetry.NewProvider(tp, opts.ServiceName), nil
}

// NewSimple creates a console provider with default options.
func NewSimple(serviceName string) (*telemetry.Provider, error) {
	return New(Options{
		ServiceName: serviceName,
		PrettyPrint: true,
		Timestamps:  true,
	})
}
