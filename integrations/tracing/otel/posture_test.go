package otel_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/tracing/otel"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// captureDefaultLog redirects slog.Default, where Open writes its development
// posture warnings, for the duration of the test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func shutdown(t *testing.T, tracer *otel.Tracer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestPosture_RuntimeModeHasNoEnvTag pins the mode as programmatic:
// ParseEnvTags leaves it zero with same-named variables set.
func TestPosture_RuntimeModeHasNoEnvTag(t *testing.T) {
	field, ok := reflect.TypeFor[otel.Config]().FieldByName("RuntimeMode")
	if !ok {
		t.Fatal("Config.RuntimeMode is missing")
	}
	if tag := field.Tag.Get("env"); tag != "" {
		t.Fatalf("Config.RuntimeMode carries env tag %q", tag)
	}
	for _, key := range []string{"RuntimeMode", "RUNTIME_MODE", "TRACING_RUNTIME_MODE", "APP_RUNTIME_MODE"} {
		t.Setenv(key, "production")
	}
	var cfg otel.Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.RuntimeMode != "" {
		t.Fatalf("ParseEnvTags populated RuntimeMode = %q", cfg.RuntimeMode)
	}
}

// TestPosture_ProductionRequiresExporter: with the env-tag default gone, an
// unset TRACING_EXPORTER reaches Open empty and production refuses it; an
// explicit stdout is admitted.
func TestPosture_ProductionRequiresExporter(t *testing.T) {
	t.Setenv("TRACING_EXPORTER", "")
	if err := os.Unsetenv("TRACING_EXPORTER"); err != nil {
		t.Fatal(err)
	}
	var cfg otel.Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Exporter != "" {
		t.Fatalf("unset TRACING_EXPORTER parsed as %q, want empty", cfg.Exporter)
	}
	cfg.RuntimeMode = environment.ModeProduction
	if tracer, err := otel.Open(context.Background(), cfg); tracer != nil || !errors.Is(err, otel.ErrExporterRequired) {
		t.Fatalf("Open = %v, %v; want ErrExporterRequired", tracer, err)
	}

	t.Setenv("TRACING_EXPORTER", "stdout")
	cfg = otel.Config{}
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	var buf bytes.Buffer
	cfg.RuntimeMode = environment.ModeProduction
	cfg.Stdout = &buf
	tracer, err := otel.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("explicit stdout in production = %v, want admitted", err)
	}
	shutdown(t, tracer)
}

// TestPosture_DevelopmentKeepsStdoutDefault: an empty exporter is still the
// stdout dev tracer; development adds one WARN, the zero mode logs nothing.
func TestPosture_DevelopmentKeepsStdoutDefault(t *testing.T) {
	logs := captureDefaultLog(t)
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment} {
		logs.Reset()
		var buf bytes.Buffer
		tracer, err := otel.Open(context.Background(), otel.Config{RuntimeMode: mode, Stdout: &buf})
		if err != nil {
			t.Fatalf("mode %q: Open = %v", mode, err)
		}
		_, span := tracer.StartSpan(context.Background(), "default.span")
		span.Finish()
		shutdown(t, tracer)
		if !strings.Contains(buf.String(), "default.span") {
			t.Fatalf("mode %q: the stdout default wrote no span", mode)
		}
		wantWarnings := 0
		if mode == environment.ModeDevelopment {
			wantWarnings = 1
		}
		if got := strings.Count(logs.String(), "level=WARN"); got != wantWarnings {
			t.Fatalf("mode %q: %d WARN lines, want %d:\n%s", mode, got, wantWarnings, logs)
		}
	}
}

func TestPosture_InsecureOTLP(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		insecure bool
		refused  bool
	}{
		{"localhost:4317", true, false},
		{"LOCALHOST:4317", true, false},
		{"127.0.0.1:4317", true, false},
		{"[::1]:4317", true, false},
		{"localhost", true, false},
		{"collector.example.com:4317", true, true},
		{"10.0.0.5:4317", true, true},
		{"127.0.0.2:4317", true, true},
		{"", true, true},
		{"collector.example.com:4317", false, false},
	} {
		tracer, err := otel.Open(context.Background(), otel.Config{
			RuntimeMode: environment.ModeProduction,
			Exporter:    otel.ExporterOTLPGRPC,
			Endpoint:    tc.endpoint,
			Insecure:    tc.insecure,
		})
		if tc.refused {
			if tracer != nil || !errors.Is(err, otel.ErrInsecureExporter) {
				t.Fatalf("endpoint %q insecure=%v: Open = %v, %v; want ErrInsecureExporter", tc.endpoint, tc.insecure, tracer, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("endpoint %q insecure=%v: Open = %v, want admitted", tc.endpoint, tc.insecure, err)
		}
		shutdown(t, tracer)
	}

	logs := captureDefaultLog(t)
	tracer, err := otel.Open(context.Background(), otel.Config{
		RuntimeMode: environment.ModeDevelopment,
		Exporter:    otel.ExporterOTLPGRPC,
		Endpoint:    "collector.example.com:4317",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("development Open = %v, want admitted", err)
	}
	shutdown(t, tracer)
	if got := strings.Count(logs.String(), "level=WARN"); got != 1 {
		t.Fatalf("development logged %d WARN lines, want 1:\n%s", got, logs)
	}
}

func TestPosture_InvalidModeRefused(t *testing.T) {
	_, err := otel.Open(context.Background(), otel.Config{RuntimeMode: "prod", Exporter: otel.ExporterStdout})
	if !errors.Is(err, environment.ErrModeInvalid) {
		t.Fatalf("Open = %v, want environment.ErrModeInvalid", err)
	}
}
