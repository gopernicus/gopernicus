package otel

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	stdouttrace "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Exporter selects how Open ships spans. The zero value is treated as
// ExporterStdout so a zero Config yields a usable dev tracer.
type Exporter string

const (
	// ExporterStdout writes spans to Config.Stdout (default os.Stdout) via the
	// stdout exporter — for local development and debugging.
	ExporterStdout Exporter = "stdout"
	// ExporterOTLPGRPC sends spans to an OTLP/gRPC collector (Jaeger, Tempo,
	// Grafana Alloy, an OpenTelemetry Collector, …) via otlptracegrpc.
	ExporterOTLPGRPC Exporter = "otlpgrpc"
	// ExporterProvider adapts a caller-supplied TracerProvider (Config.Provider);
	// this module builds no exporter and never shuts the provider down.
	ExporterProvider Exporter = "provider"
)

// Exporter/resource defaults applied to zero-value Config fields by Open.
const (
	defaultExporter    = ExporterStdout
	defaultServiceName = "gopernicus"
)

// Config selects and configures the OpenTelemetry exporter Open builds. Its
// `env:` tags let a host populate the scalar fields with sdk/pkg/environment.ParseEnvTags;
// the Stdout and Provider fields are programmatic-only. A zero Config is filled
// with the documented defaults by Open (stdout exporter, service name
// "gopernicus", parent-based sampling of roots), so struct-literal construction stays
// first-class.
type Config struct {
	// Exporter chooses the span destination. Empty defaults to ExporterStdout.
	Exporter Exporter `env:"TRACING_EXPORTER" default:"stdout"`

	// ServiceName tags every span's resource and names the tracer. Empty
	// defaults to "gopernicus".
	ServiceName string `env:"TRACING_SERVICE_NAME" default:"gopernicus"`
	// ServiceVersion and Environment add optional resource attributes; empty
	// values are omitted.
	ServiceVersion string `env:"TRACING_SERVICE_VERSION" default:""`
	Environment    string `env:"TRACING_ENVIRONMENT" default:""`
	// SampleRate is the OTLP root-sampling ratio in [0,1]. Literal zero samples
	// no roots; environment loading defaults it to 1. Parent decisions take
	// precedence. Stdout samples roots and respects parents; Provider owns its policy.
	SampleRate float64 `env:"TRACING_SAMPLE_RATE" default:"1"`

	// Endpoint is the OTLP/gRPC collector address (host:port). Empty lets the
	// exporter use its own default (localhost:4317).
	Endpoint string `env:"TRACING_OTLP_ENDPOINT" default:""`
	// Insecure disables transport security for the OTLP/gRPC connection.
	Insecure bool `env:"TRACING_OTLP_INSECURE" default:"false"`

	// PrettyPrint indents the stdout exporter's JSON output.
	PrettyPrint bool `env:"TRACING_STDOUT_PRETTY" default:"false"`
	// Stdout is the destination for ExporterStdout. Nil defaults to os.Stdout;
	// tests can redirect it to a buffer.
	Stdout io.Writer

	// Provider is the caller-supplied TracerProvider used by ExporterProvider.
	// Required for that mode; ignored otherwise. Its lifecycle stays the
	// caller's responsibility.
	Provider oteltrace.TracerProvider
}

// Open builds a *Tracer for cfg.Exporter. Zero fields take the documented
// defaults. ExporterStdout and ExporterOTLPGRPC construct an owned
// TracerProvider whose flush/stop runs in Tracer.Shutdown; ExporterProvider
// adapts cfg.Provider and leaves its lifecycle to the caller.
//
// ctx is used only when constructing the OTLP/gRPC exporter. Construction does
// not block on a live collector: an OTLP tracer opens offline and connects
// lazily on first export.
func Open(ctx context.Context, cfg Config) (*Tracer, error) {
	if cfg.Exporter == "" {
		cfg.Exporter = defaultExporter
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = defaultServiceName
	}
	if math.IsNaN(cfg.SampleRate) || math.IsInf(cfg.SampleRate, 0) || cfg.SampleRate < 0 || cfg.SampleRate > 1 {
		return nil, fmt.Errorf("otel: sample rate must be finite and between 0 and 1")
	}

	switch cfg.Exporter {
	case ExporterProvider:
		if cfg.Provider == nil {
			return nil, fmt.Errorf("otel: exporter %q requires Config.Provider", cfg.Exporter)
		}
		return &Tracer{tracer: cfg.Provider.Tracer(cfg.ServiceName)}, nil
	case ExporterStdout:
		return openStdout(cfg)
	case ExporterOTLPGRPC:
		return openOTLPGRPC(ctx, cfg)
	default:
		return nil, fmt.Errorf("otel: unknown exporter %q", cfg.Exporter)
	}
}

// openStdout wires a synchronous stdout exporter so spans appear immediately in
// development.
func openStdout(cfg Config) (*Tracer, error) {
	w := cfg.Stdout
	if w == nil {
		w = os.Stdout
	}

	opts := []stdouttrace.Option{stdouttrace.WithWriter(w)}
	if cfg.PrettyPrint {
		opts = append(opts, stdouttrace.WithPrettyPrint())
	}

	exporter, err := stdouttrace.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("otel: creating stdout exporter: %w", err)
	}

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
	)
	return newOwnedTracer(tp, cfg.ServiceName), nil
}

// openOTLPGRPC wires a batching OTLP/gRPC exporter for a collector.
func openOTLPGRPC(ctx context.Context, cfg Config) (*Tracer, error) {
	opts := []otlptracegrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otel: creating OTLP/gRPC exporter: %w", err)
	}

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(rootSampler(cfg.SampleRate)),
		sdktrace.WithResource(res),
	)
	return newOwnedTracer(tp, cfg.ServiceName), nil
}

// newResource builds the span resource from cfg. NewSchemaless keeps
// resource.Merge from conflicting on whatever schema resource.Default() detects.
func newResource(cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{attribute.String("service.name", cfg.ServiceName)}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment.name", cfg.Environment))
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, fmt.Errorf("otel: building resource: %w", err)
	}
	return res, nil
}

func rootSampler(rate float64) sdktrace.Sampler {
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))
}
