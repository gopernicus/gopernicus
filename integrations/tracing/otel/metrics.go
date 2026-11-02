package otel

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/gopernicus/gopernicus/sdk/capabilities/metrics"
)

const (
	// defaultMetricInterval is the periodic reader's export interval when
	// Config.MetricInterval is zero.
	defaultMetricInterval = 60 * time.Second
	// otlpHTTPMetricsPath is the OTLP/HTTP metrics signal path.
	otlpHTTPMetricsPath = "/v1/metrics"
)

var _ metrics.Meter = (*Metrics)(nil)

// Metrics implements sdk/capabilities/metrics.Meter over OpenTelemetry and
// exports on a periodic reader. Build one with OpenMetrics.
type Metrics struct {
	meter otelmetric.Meter
	// owned is the provider this module built; nil in ExporterProvider mode.
	owned *sdkmetric.MeterProvider
}

// OpenMetrics builds a *Metrics from the same Config as Open and OpenLogs:
// Exporter, Endpoint, Headers, Insecure, the service resource and RuntimeMode,
// with the same validation, posture rules and OTEL_EXPORTER_OTLP_* isolation.
// Over OTLP/HTTP metrics go to <Endpoint>/v1/metrics. A periodic reader exports
// every Config.MetricInterval (60s when zero). ExporterProvider adapts
// Config.MeterProvider and never shuts it down.
func OpenMetrics(ctx context.Context, cfg Config) (*Metrics, error) {
	var httpTarget otlpHTTPTarget
	if cfg.Exporter == ExporterOTLPHTTP {
		target, err := parseOTLPHTTP(cfg)
		if err != nil {
			return nil, err
		}
		httpTarget = target
	}
	if err := cfg.checkPosture(httpTarget); err != nil {
		return nil, err
	}
	if cfg.Exporter == "" {
		cfg.Exporter = defaultExporter
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = defaultServiceName
	}
	if cfg.MetricInterval < 0 {
		return nil, fmt.Errorf("otel: Config.MetricInterval must not be negative")
	}
	interval := cfg.MetricInterval
	if interval == 0 {
		interval = defaultMetricInterval
	}
	if cfg.Exporter == ExporterProvider {
		if cfg.MeterProvider == nil {
			return nil, fmt.Errorf("otel: exporter %q requires Config.MeterProvider for metrics", cfg.Exporter)
		}
		return &Metrics{meter: cfg.MeterProvider.Meter(cfg.ServiceName)}, nil
	}

	var exporter sdkmetric.Exporter
	switch cfg.Exporter {
	case ExporterStdout:
		w := cfg.Stdout
		if w == nil {
			w = os.Stdout
		}
		opts := []stdoutmetric.Option{stdoutmetric.WithWriter(w)}
		if cfg.PrettyPrint {
			opts = append(opts, stdoutmetric.WithPrettyPrint())
		}
		e, err := stdoutmetric.New(opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating stdout metric exporter: %w", err)
		}
		exporter = e
	case ExporterOTLPGRPC:
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithHeaders(map[string]string{}), otlpmetricgrpc.WithTimeout(otlpHTTPTimeout)}
		if cfg.Endpoint != "" {
			opts = append(opts, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		e, err := otlpmetricgrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating OTLP/gRPC metric exporter: %w", err)
		}
		exporter = e
	case ExporterOTLPHTTP:
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(httpTarget.signalURL(otlpHTTPMetricsPath)),
			otlpmetrichttp.WithHeaders(httpTarget.headers),
			otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
			otlpmetrichttp.WithTimeout(otlpHTTPTimeout),
		}
		if httpTarget.insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		} else {
			tlsCfg := cfg.tlsConfig
			if tlsCfg == nil {
				tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(tlsCfg))
		}
		e, err := otlpmetrichttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating OTLP/HTTP metric exporter: %w", err)
		}
		exporter = e
	default:
		return nil, fmt.Errorf("otel: unknown exporter %q", cfg.Exporter)
	}

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))),
		sdkmetric.WithResource(res),
	)
	return &Metrics{meter: provider.Meter(cfg.ServiceName), owned: provider}, nil
}

// Counter returns a monotonic int64 counter. An empty name or an instrument
// OpenTelemetry refuses yields a no-op counter and one WARN, never a panic.
func (m *Metrics) Counter(spec metrics.Spec) metrics.Counter {
	if m == nil || spec.Name == "" {
		warnInstrument(spec, "empty name or nil Metrics")
		return metrics.Noop{}.Counter(spec)
	}
	c, err := m.meter.Int64Counter(spec.Name, otelmetric.WithDescription(spec.Description), otelmetric.WithUnit(spec.Unit))
	if err != nil {
		warnInstrument(spec, err.Error())
		return metrics.Noop{}.Counter(spec)
	}
	return counter{c}
}

// Histogram returns a float64 histogram, with the same fallback as Counter.
func (m *Metrics) Histogram(spec metrics.Spec) metrics.Histogram {
	if m == nil || spec.Name == "" {
		warnInstrument(spec, "empty name or nil Metrics")
		return metrics.Noop{}.Histogram(spec)
	}
	h, err := m.meter.Float64Histogram(spec.Name, otelmetric.WithDescription(spec.Description), otelmetric.WithUnit(spec.Unit))
	if err != nil {
		warnInstrument(spec, err.Error())
		return metrics.Noop{}.Histogram(spec)
	}
	return histogram{h}
}

// Gauge registers an observable float64 gauge whose observe callback runs on
// each collection. unregister stops it and is safe to call more than once.
func (m *Metrics) Gauge(spec metrics.Spec, observe func(ctx context.Context) []metrics.Observation) (func(), error) {
	if m == nil || spec.Name == "" || observe == nil {
		return func() {}, fmt.Errorf("otel: Gauge needs a name, an observe callback and a non-nil *Metrics")
	}
	g, err := m.meter.Float64ObservableGauge(spec.Name, otelmetric.WithDescription(spec.Description), otelmetric.WithUnit(spec.Unit))
	if err != nil {
		return func() {}, fmt.Errorf("otel: registering gauge %q: %w", spec.Name, err)
	}
	reg, err := m.meter.RegisterCallback(func(ctx context.Context, o otelmetric.Observer) error {
		for _, obs := range observe(ctx) {
			o.ObserveFloat64(g, obs.Value, otelmetric.WithAttributes(otelAttrs(obs.Attrs)...))
		}
		return nil
	}, g)
	if err != nil {
		return func() {}, fmt.Errorf("otel: registering gauge %q callback: %w", spec.Name, err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = reg.Unregister() }) }, nil
}

// ForceFlush collects and exports now. It is a no-op in ExporterProvider mode
// and on a nil *Metrics.
func (m *Metrics) ForceFlush(ctx context.Context) error {
	if m == nil || m.owned == nil {
		return nil
	}
	return m.owned.ForceFlush(ctx)
}

// Shutdown flushes and stops the provider this module built. It is a no-op in
// ExporterProvider mode (the caller owns that provider) and on a nil *Metrics.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.owned == nil {
		return nil
	}
	return m.owned.Shutdown(ctx)
}

type counter struct{ c otelmetric.Int64Counter }

func (c counter) Add(ctx context.Context, n int64, attrs ...metrics.Attribute) {
	c.c.Add(ctx, n, otelmetric.WithAttributes(otelAttrs(attrs)...))
}

type histogram struct{ h otelmetric.Float64Histogram }

func (h histogram) Record(ctx context.Context, v float64, attrs ...metrics.Attribute) {
	h.h.Record(ctx, v, otelmetric.WithAttributes(otelAttrs(attrs)...))
}

func otelAttrs(attrs []metrics.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kvs := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		kvs[i] = attribute.String(a.Key, a.Value)
	}
	return kvs
}

func warnInstrument(spec metrics.Spec, reason string) {
	slog.Default().Warn("otel: metric instrument not created; using a no-op", "name", spec.Name, "reason", reason)
}
