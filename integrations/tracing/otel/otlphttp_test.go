package otel

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// otlpRequest is one export request an otlpReceiver accepted.
type otlpRequest struct {
	path     string
	header   http.Header
	services []string
	spans    []string
}

// otlpReceiver is a minimal OTLP/HTTP traces endpoint that records each request.
type otlpReceiver struct {
	mu   sync.Mutex
	reqs []otlpRequest
}

func (rcv *otlpReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		body = zr
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		http.Error(w, "bad proto", http.StatusBadRequest)
		return
	}
	got := otlpRequest{path: r.URL.Path, header: r.Header.Clone()}
	for _, rs := range req.GetResourceSpans() {
		for _, kv := range rs.GetResource().GetAttributes() {
			if kv.GetKey() == "service.name" {
				got.services = append(got.services, kv.GetValue().GetStringValue())
			}
		}
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				got.spans = append(got.spans, sp.GetName())
			}
		}
	}
	rcv.mu.Lock()
	rcv.reqs = append(rcv.reqs, got)
	rcv.mu.Unlock()
	out, _ := proto.Marshal(&coltracepb.ExportTraceServiceResponse{})
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}

func (rcv *otlpReceiver) only(t *testing.T) otlpRequest {
	t.Helper()
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.reqs) != 1 {
		t.Fatalf("receiver got %d requests, want 1", len(rcv.reqs))
	}
	return rcv.reqs[0]
}

// exportOne opens cfg, exports one span named name and flushes.
func exportOne(t *testing.T, cfg Config, name string) {
	t.Helper()
	tracer, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, span := tracer.StartSpan(context.Background(), name)
	span.Finish()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracer.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if err := tracer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func trustServer(srv *httptest.Server) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// TestOTLPHTTPExportsWithHeaders: over plain loopback HTTP and over TLS, one
// span reaches <base>/v1/traces gzip-compressed, carrying the configured
// headers and the service name.
func TestOTLPHTTPExportsWithHeaders(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", useTLS), func(t *testing.T) {
			rcv := &otlpReceiver{}
			var srv *httptest.Server
			if useTLS {
				srv = httptest.NewTLSServer(rcv)
			} else {
				srv = httptest.NewServer(rcv)
			}
			defer srv.Close()
			cfg := Config{
				Exporter:    ExporterOTLPHTTP,
				Endpoint:    srv.URL + "/otlp/",
				Headers:     sdk.Secret("Authorization=Basic%20dGVzdDpzZWNyZXQ=, X-Scope-OrgID=acme"),
				ServiceName: "otlphttp-test",
				SampleRate:  1,
				RuntimeMode: environment.ModeProduction,
			}
			if useTLS {
				cfg.tlsConfig = trustServer(srv)
			}
			exportOne(t, cfg, "export.span")
			got := rcv.only(t)
			if got.path != "/otlp/v1/traces" {
				t.Errorf("path = %q, want /otlp/v1/traces", got.path)
			}
			if a := got.header.Get("Authorization"); a != "Basic dGVzdDpzZWNyZXQ=" {
				t.Errorf("Authorization = %q", a)
			}
			if o := got.header.Get("X-Scope-OrgID"); o != "acme" {
				t.Errorf("X-Scope-OrgID = %q", o)
			}
			if e := got.header.Get("Content-Encoding"); e != "gzip" {
				t.Errorf("Content-Encoding = %q, want gzip", e)
			}
			if len(got.spans) != 1 || got.spans[0] != "export.span" || len(got.services) != 1 || got.services[0] != "otlphttp-test" {
				t.Errorf("spans = %v services = %v", got.spans, got.services)
			}
		})
	}
}

// TestOTLPHTTPIgnoresOTELEnvironment poisons every OTEL_EXPORTER_OTLP_*
// variable the exporter reads; none reaches the request or redirects it.
func TestOTLPHTTPIgnoresOTELEnvironment(t *testing.T) {
	rcv := &otlpReceiver{}
	srv := httptest.NewTLSServer(rcv)
	defer srv.Close()
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_TRACES_"} {
		t.Setenv(prefix+"ENDPOINT", "http://poison.invalid:1/poison")
		t.Setenv(prefix+"HEADERS", "X-Poison=1,Authorization=poison")
		t.Setenv(prefix+"TIMEOUT", "1")
		t.Setenv(prefix+"COMPRESSION", "none")
		t.Setenv(prefix+"INSECURE", "true")
		t.Setenv(prefix+"CERTIFICATE", "/nonexistent/poison.pem")
	}
	cfg := Config{
		Exporter: ExporterOTLPHTTP, Endpoint: srv.URL, Headers: sdk.Secret("Authorization=Bearer%20real"),
		SampleRate: 1, tlsConfig: trustServer(srv),
	}
	exportOne(t, cfg, "env.span")
	got := rcv.only(t)
	if got.path != "/v1/traces" {
		t.Errorf("path = %q, want /v1/traces", got.path)
	}
	if got.header.Get("X-Poison") != "" || got.header.Get("Authorization") != "Bearer real" {
		t.Errorf("headers = %v; the env headers leaked", got.header)
	}
	if got.header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q; the env compression applied", got.header.Get("Content-Encoding"))
	}
}

// TestOTLPHTTPEmptyHeadersStillOverrideEnvironment: with no Config.Headers a
// stray OTEL_EXPORTER_OTLP_HEADERS is still not sent.
func TestOTLPHTTPEmptyHeadersStillOverrideEnvironment(t *testing.T) {
	rcv := &otlpReceiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "X-Poison=1")
	exportOne(t, Config{Exporter: ExporterOTLPHTTP, Endpoint: srv.URL, SampleRate: 1}, "noheaders.span")
	if got := rcv.only(t); got.header.Get("X-Poison") != "" {
		t.Fatalf("env header sent: %v", got.header)
	}
}

// TestOTLPHTTPValidation refuses malformed endpoints and header lists in every
// mode, before any exporter is built, without echoing a header value or a
// userinfo credential.
func TestOTLPHTTPValidation(t *testing.T) {
	const secretValue = "s3cr3t-value"
	for _, tc := range []struct {
		name     string
		endpoint string
		insecure bool
		headers  string
	}{
		{"empty endpoint", "", false, ""},
		{"host:port, not a URL", "collector.example.com:4318", false, ""},
		{"grpc scheme", "grpc://collector.example.com:4317", false, ""},
		{"no host", "https:///otlp", false, ""},
		{"userinfo", "https://user:" + secretValue + "@collector.example.com/otlp", false, ""},
		{"query", "https://collector.example.com/otlp?x=1", false, ""},
		{"fragment", "https://collector.example.com/otlp#frag", false, ""},
		{"signal path included", "https://collector.example.com/otlp/v1/traces", false, ""},
		{"insecure with https", "https://collector.example.com/otlp", true, ""},
		{"header without =", "https://collector.example.com/otlp", false, "Authorization " + secretValue},
		{"empty header name", "https://collector.example.com/otlp", false, "=" + secretValue},
		{"invalid header name", "https://collector.example.com/otlp", false, "Bad Name=" + secretValue},
		{"bad percent-encoding", "https://collector.example.com/otlp", false, "Authorization=%zz" + secretValue},
		{"control character", "https://collector.example.com/otlp", false, "Authorization=" + secretValue + "%0d%0aX-Injected:1"},
		{"repeated header", "https://collector.example.com/otlp", false, "Authorization=a,authorization=" + secretValue},
		{"empty entry", "https://collector.example.com/otlp", false, "A=1,,B=" + secretValue},
	} {
		for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
			tracer, err := Open(context.Background(), Config{
				Exporter: ExporterOTLPHTTP, Endpoint: tc.endpoint, Insecure: tc.insecure,
				Headers: sdk.Secret(tc.headers), RuntimeMode: mode,
			})
			if tracer != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("%s (mode %q): Open = %v, %v; want ErrInvalidInput", tc.name, mode, tracer, err)
				continue
			}
			if strings.Contains(err.Error(), secretValue) {
				t.Errorf("%s: the error echoes a secret: %v", tc.name, err)
			}
		}
	}
}

// TestOTLPHTTPPosture: production refuses plain HTTP to a non-loopback host
// and admits loopback and https; development warns once, naming the endpoint
// and never a header.
func TestOTLPHTTPPosture(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		refused  bool
	}{
		{"http://localhost:4318", false},
		{"http://127.0.0.1:4318/otlp", false},
		{"http://[::1]:4318", false},
		{"http://collector.example.com:4318", true},
		{"http://10.0.0.5:4318", true},
		{"https://collector.example.com/otlp", false},
	} {
		tracer, err := Open(context.Background(), Config{
			Exporter: ExporterOTLPHTTP, Endpoint: tc.endpoint, RuntimeMode: environment.ModeProduction,
		})
		if tc.refused {
			if tracer != nil || !errors.Is(err, ErrInsecureExporter) {
				t.Errorf("%s: Open = %v, %v; want ErrInsecureExporter", tc.endpoint, tracer, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Open = %v; want admitted", tc.endpoint, err)
			continue
		}
		_ = tracer.Shutdown(context.Background())
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	tracer, err := Open(context.Background(), Config{
		Exporter: ExporterOTLPHTTP, Endpoint: "http://collector.example.com:4318",
		Headers: sdk.Secret("Authorization=Basic%20warnsecret"), RuntimeMode: environment.ModeDevelopment,
	})
	if err != nil {
		t.Fatalf("development Open = %v", err)
	}
	_ = tracer.Shutdown(context.Background())
	logs := buf.String()
	if strings.Count(logs, "level=WARN") != 1 || !strings.Contains(logs, "collector.example.com") {
		t.Fatalf("development logs = %q, want one WARN naming the endpoint", logs)
	}
	if strings.Contains(logs, "warnsecret") {
		t.Fatalf("development WARN leaked a header: %q", logs)
	}
}

// TestOTLPHTTPHeadersRedacted: a Config printed or logged shows no header value.
func TestOTLPHTTPHeadersRedacted(t *testing.T) {
	cfg := Config{Exporter: ExporterOTLPHTTP, Headers: sdk.Secret("Authorization=Basic%20printsecret")}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "config", cfg, "headers", cfg.Headers)
	for _, out := range []string{fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, cfg.Headers), buf.String()} {
		if strings.Contains(out, "printsecret") {
			t.Fatalf("header value printed: %s", out)
		}
	}
}

// TestOTLPHTTPConfigFromEnvironment: ParseEnvTags fills the OTLP/HTTP fields,
// the sdk.Secret headers included, from the TRACING_* variables.
func TestOTLPHTTPConfigFromEnvironment(t *testing.T) {
	t.Setenv("TRACING_EXPORTER", "otlphttp")
	t.Setenv("TRACING_OTLP_ENDPOINT", "https://collector.example.com/otlp")
	t.Setenv("TRACING_OTLP_HEADERS", "Authorization=Basic%20ZW52OnRlc3Q=")
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Exporter != ExporterOTLPHTTP || cfg.Endpoint != "https://collector.example.com/otlp" || cfg.Headers.Reveal() != "Authorization=Basic%20ZW52OnRlc3Q=" {
		t.Fatalf("cfg = %+v (headers %d bytes)", cfg, len(cfg.Headers.Reveal()))
	}
	target, err := parseOTLPHTTP(cfg)
	if err != nil {
		t.Fatalf("parseOTLPHTTP: %v", err)
	}
	if target.signalURL(otlpHTTPTracesPath) != "https://collector.example.com/otlp/v1/traces" || target.signalURL(otlpHTTPLogsPath) != "https://collector.example.com/otlp/v1/logs" || target.headers["Authorization"] != "Basic ZW52OnRlc3Q=" {
		t.Fatalf("target = %s %v", target.base, len(target.headers))
	}
}
