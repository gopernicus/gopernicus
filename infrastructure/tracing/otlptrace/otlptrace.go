// Package otlptrace provides an OTLP exporter adapter for telemetry.
// OTLP (OpenTelemetry Protocol) is the standard protocol for sending
// telemetry data to collectors like Jaeger, Tempo, Grafana, etc.
package otlptrace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/gopernicus/gopernicus/telemetry"
)

// Options configures the OTLP exporter.
type Options struct {
	Endpoint       string
	Insecure       bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	SampleRate     float64
}

// New creates a new telemetry Provider using OTLP gRPC exporter.
func New(ctx context.Context, opts Options) (*telemetry.Provider, error) {
	clientOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(opts.Endpoint),
	}
	if opts.Insecure {
		clientOpts = append(clientOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(opts.ServiceVersion),
			semconv.DeploymentEnvironmentName(opts.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(opts.SampleRate)),
		sdktrace.WithResource(res),
	)

	return telemetry.NewProvider(tp, opts.ServiceName), nil
}
