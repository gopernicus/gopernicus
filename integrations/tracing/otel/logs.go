package otel

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Logs exports slog records over OpenTelemetry. Build one with OpenLogs and
// put Handler() last in the host's handler chain, after redaction.
type Logs struct {
	provider otellog.LoggerProvider
	// owned is the provider this module built; nil in ExporterProvider mode.
	owned *sdklog.LoggerProvider
	name  string
}

// OpenLogs builds a *Logs from the same Config as Open: Exporter (stdout,
// otlpgrpc, otlphttp or provider), Endpoint, Headers, Insecure, the service
// resource and RuntimeMode, with the same validation, posture rules and
// OTEL_EXPORTER_OTLP_* isolation. Over OTLP/HTTP records go to
// <Endpoint>/v1/logs. ExporterProvider adapts Config.LoggerProvider and never
// shuts it down. OTLP exporters open offline and batch.
func OpenLogs(ctx context.Context, cfg Config) (*Logs, error) {
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
	if cfg.Exporter == ExporterProvider {
		if cfg.LoggerProvider == nil {
			return nil, fmt.Errorf("otel: exporter %q requires Config.LoggerProvider for logs", cfg.Exporter)
		}
		return &Logs{provider: cfg.LoggerProvider, name: cfg.ServiceName}, nil
	}

	var processor sdklog.Processor
	switch cfg.Exporter {
	case ExporterStdout:
		w := cfg.Stdout
		if w == nil {
			w = os.Stdout
		}
		opts := []stdoutlog.Option{stdoutlog.WithWriter(w)}
		if cfg.PrettyPrint {
			opts = append(opts, stdoutlog.WithPrettyPrint())
		}
		exporter, err := stdoutlog.New(opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating stdout log exporter: %w", err)
		}
		processor = sdklog.NewSimpleProcessor(exporter)
	case ExporterOTLPGRPC:
		opts := []otlploggrpc.Option{otlploggrpc.WithHeaders(map[string]string{}), otlploggrpc.WithTimeout(otlpHTTPTimeout)}
		if cfg.Endpoint != "" {
			opts = append(opts, otlploggrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		exporter, err := otlploggrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating OTLP/gRPC log exporter: %w", err)
		}
		processor = sdklog.NewBatchProcessor(exporter)
	case ExporterOTLPHTTP:
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpointURL(httpTarget.signalURL(otlpHTTPLogsPath)),
			otlploghttp.WithHeaders(httpTarget.headers),
			otlploghttp.WithCompression(otlploghttp.GzipCompression),
			otlploghttp.WithTimeout(otlpHTTPTimeout),
		}
		if httpTarget.insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		} else {
			tlsCfg := cfg.tlsConfig
			if tlsCfg == nil {
				tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			opts = append(opts, otlploghttp.WithTLSClientConfig(tlsCfg))
		}
		exporter, err := otlploghttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otel: creating OTLP/HTTP log exporter: %w", err)
		}
		processor = sdklog.NewBatchProcessor(exporter)
	default:
		return nil, fmt.Errorf("otel: unknown exporter %q", cfg.Exporter)
	}

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor), sdklog.WithResource(res))
	return &Logs{provider: provider, owned: provider, name: cfg.ServiceName}, nil
}

// Handler returns a slog.Handler that emits records at or above level as
// OpenTelemetry log records, through OpenTelemetry's slog bridge. A record
// logged with a context carrying a span gets that span's trace and span ids.
//
// Put it LAST in the chain, after redaction, so nothing a redacting handler
// removes is exported:
//
//	slog.New(logging.NewContextHandler(logging.NewRedactHandler(
//		slog.NewMultiHandler(console, logs.Handler(slog.LevelInfo)), keys)))
//
// A nil level means slog.LevelInfo. A nil *Logs returns a handler that
// discards everything.
func (l *Logs) Handler(level slog.Leveler) slog.Handler {
	if level == nil {
		level = slog.LevelInfo
	}
	if l == nil || l.provider == nil {
		return slog.DiscardHandler
	}
	return &levelHandler{inner: otelslog.NewHandler(l.name, otelslog.WithLoggerProvider(l.provider)), level: level}
}

// ForceFlush exports buffered records without stopping. It is a no-op in
// ExporterProvider mode and on a nil *Logs.
func (l *Logs) ForceFlush(ctx context.Context) error {
	if l == nil || l.owned == nil {
		return nil
	}
	return l.owned.ForceFlush(ctx)
}

// Shutdown flushes and stops the provider this module built. It is a no-op in
// ExporterProvider mode (the caller owns that provider) and on a nil *Logs.
func (l *Logs) Shutdown(ctx context.Context) error {
	if l == nil || l.owned == nil {
		return nil
	}
	return l.owned.Shutdown(ctx)
}

// levelHandler gates an inner handler at a minimum level.
type levelHandler struct {
	inner slog.Handler
	level slog.Leveler
}

func (h *levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level() && h.inner.Enabled(ctx, level)
}

func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{inner: h.inner.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{inner: h.inner.WithGroup(name), level: h.level}
}
