package otel

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	otelapi "go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
)

func flushLogs(t *testing.T, l *Logs) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if err := l.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestLogsStdoutCarriesTraceContext: a record exports with body, severity and
// attributes, and one logged inside a span carries that span's trace id.
func TestLogsStdoutCarriesTraceContext(t *testing.T) {
	var out bytes.Buffer
	logs, err := OpenLogs(context.Background(), Config{Exporter: ExporterStdout, Stdout: &out, ServiceName: "logs-test"})
	if err != nil {
		t.Fatal(err)
	}
	tracer, err := Open(context.Background(), Config{Exporter: ExporterStdout, Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer tracer.Shutdown(context.Background())
	ctx, span := tracer.StartSpan(context.Background(), "request")
	traceID := span.(interface{ TraceID() string }).TraceID()

	log := slog.New(logs.Handler(slog.LevelInfo))
	log.DebugContext(ctx, "dropped below the level")
	log.InfoContext(ctx, "inside the span", "order_id", "o-1")
	span.Finish()
	flushLogs(t, logs)

	got := out.String()
	if strings.Contains(got, "dropped below the level") {
		t.Fatal("a DEBUG record passed an INFO handler")
	}
	for _, want := range []string{"inside the span", "order_id", "o-1", traceID, "logs-test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout export lacks %q:\n%s", want, got)
		}
	}
}

// TestLogsRedactionRunsFirst: in the documented chain, a redacted key never
// reaches the exported record.
func TestLogsRedactionRunsFirst(t *testing.T) {
	var out bytes.Buffer
	logs, err := OpenLogs(context.Background(), Config{Exporter: ExporterStdout, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	var console bytes.Buffer
	log := slog.New(logging.NewContextHandler(logging.NewRedactHandler(
		slog.NewMultiHandler(slog.NewTextHandler(&console, nil), logs.Handler(slog.LevelInfo)), []string{"password"})))
	log.Info("signed in", "password", "hunter2-secret", "user", "u1")
	flushLogs(t, logs)
	for name, buf := range map[string]string{"export": out.String(), "console": console.String()} {
		if strings.Contains(buf, "hunter2-secret") {
			t.Fatalf("%s carries the redacted value:\n%s", name, buf)
		}
		if !strings.Contains(buf, "signed in") {
			t.Fatalf("%s lacks the record:\n%s", name, buf)
		}
	}
}

// otlpLogsReceiver records OTLP/HTTP log requests.
type otlpLogsReceiver struct {
	mu     sync.Mutex
	paths  []string
	header []http.Header
	bodies []string
}

func (rcv *otlpLogsReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "gzip", http.StatusBadRequest)
			return
		}
		body = zr
	}
	raw, _ := io.ReadAll(body)
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		http.Error(w, "proto", http.StatusBadRequest)
		return
	}
	var bodies []string
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				bodies = append(bodies, lr.GetBody().GetStringValue())
			}
		}
	}
	rcv.mu.Lock()
	rcv.paths = append(rcv.paths, r.URL.Path)
	rcv.header = append(rcv.header, r.Header.Clone())
	rcv.bodies = append(rcv.bodies, bodies...)
	rcv.mu.Unlock()
	out, _ := proto.Marshal(&collogspb.ExportLogsServiceResponse{})
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}

// TestLogsOTLPHTTP: records reach <base>/v1/logs with the configured headers,
// gzip, over TLS, with every OTEL_EXPORTER_OTLP_* variable poisoned.
func TestLogsOTLPHTTP(t *testing.T) {
	rcv := &otlpLogsReceiver{}
	srv := httptest.NewTLSServer(rcv)
	defer srv.Close()
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_LOGS_"} {
		t.Setenv(prefix+"ENDPOINT", "http://poison.invalid:1/poison")
		t.Setenv(prefix+"HEADERS", "X-Poison=1,Authorization=poison")
		t.Setenv(prefix+"TIMEOUT", "1")
		t.Setenv(prefix+"COMPRESSION", "none")
		t.Setenv(prefix+"INSECURE", "true")
		t.Setenv(prefix+"CERTIFICATE", "/nonexistent/poison.pem")
	}
	logs, err := OpenLogs(context.Background(), Config{
		Exporter: ExporterOTLPHTTP, Endpoint: srv.URL + "/otlp", Headers: sdk.Secret("Authorization=Basic%20bG9nczp0ZXN0"),
		RuntimeMode: environment.ModeProduction, tlsConfig: trustServer(srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	slog.New(logs.Handler(nil)).Info("shipped over OTLP/HTTP")
	flushLogs(t, logs)
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.paths) != 1 || rcv.paths[0] != "/otlp/v1/logs" {
		t.Fatalf("paths = %v", rcv.paths)
	}
	h := rcv.header[0]
	if h.Get("Authorization") != "Basic bG9nczp0ZXN0" || h.Get("X-Poison") != "" || h.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers = %v", h)
	}
	if len(rcv.bodies) != 1 || rcv.bodies[0] != "shipped over OTLP/HTTP" {
		t.Fatalf("bodies = %v", rcv.bodies)
	}
}

// TestLogsValidationAndPosture: OpenLogs applies Open's rules.
func TestLogsValidationAndPosture(t *testing.T) {
	if _, err := OpenLogs(context.Background(), Config{Exporter: ExporterOTLPHTTP}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty OTLP/HTTP endpoint = %v", err)
	}
	if _, err := OpenLogs(context.Background(), Config{Exporter: ExporterOTLPHTTP, Endpoint: "https://u:p@collector.example.com/otlp"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("userinfo endpoint = %v", err)
	}
	if _, err := OpenLogs(context.Background(), Config{Exporter: ExporterOTLPHTTP, Endpoint: "http://collector.example.com:4318", RuntimeMode: environment.ModeProduction}); !errors.Is(err, ErrInsecureExporter) {
		t.Errorf("production plain HTTP = %v", err)
	}
	if _, err := OpenLogs(context.Background(), Config{RuntimeMode: environment.ModeProduction}); !errors.Is(err, ErrExporterRequired) {
		t.Errorf("production empty exporter = %v", err)
	}
	if _, err := OpenLogs(context.Background(), Config{Exporter: ExporterProvider}); err == nil {
		t.Error("provider mode without LoggerProvider admitted")
	}
	grpc, err := OpenLogs(context.Background(), Config{Exporter: ExporterOTLPGRPC, Endpoint: "localhost:4317", Insecure: true})
	if err != nil {
		t.Fatalf("OTLP/gRPC offline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = grpc.Shutdown(ctx)

	provider := sdklog.NewLoggerProvider()
	logs, err := OpenLogs(context.Background(), Config{Exporter: ExporterProvider, LoggerProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("Logs.Shutdown stopped the caller's provider: %v", err)
	}
	var nilLogs *Logs
	slog.New(nilLogs.Handler(nil)).Info("discarded")
}

// TestErrorHandlerRateLimits: a burst logs once; the next line after the
// interval carries the suppressed count.
func TestErrorHandlerRateLimits(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	h := &rateLimitedErrors{console: slog.New(slog.NewTextHandler(&buf, nil)), every: time.Minute, now: func() time.Time { return now }}
	for i := 0; i < 100; i++ {
		h.Handle(errors.New("export failed"))
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 || !strings.Contains(buf.String(), "suppressed=0") {
		t.Fatalf("burst logged %d lines:\n%s", n, buf.String())
	}
	now = now.Add(time.Minute)
	h.Handle(errors.New("export failed"))
	if !strings.Contains(buf.String(), "suppressed=99") {
		t.Fatalf("after the interval:\n%s", buf.String())
	}
}

// TestInstallErrorHandlerRestores: OpenTelemetry's errors reach the console
// while installed; restore puts the previous global handler back.
func TestInstallErrorHandlerRestores(t *testing.T) {
	prev := otelapi.GetErrorHandler()
	var buf bytes.Buffer
	restore := InstallErrorHandler(slog.New(slog.NewTextHandler(&buf, nil)), time.Hour)
	otelapi.Handle(errors.New("exporter unreachable"))
	if !strings.Contains(buf.String(), "exporter unreachable") {
		t.Fatalf("installed handler wrote:\n%s", buf.String())
	}
	restore()
	if otelapi.GetErrorHandler() != prev {
		t.Fatal("restore did not reinstate the previous handler")
	}
}
