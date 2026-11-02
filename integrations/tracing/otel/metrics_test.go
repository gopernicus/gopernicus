package otel

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/metrics"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// manualMetrics is a *Metrics over a ManualReader, for exact assertions.
func manualMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	m, err := OpenMetrics(context.Background(), Config{Exporter: ExporterProvider, MeterProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	return m, reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}

// TestMetricsInstruments: counter, histogram and gauge values arrive with
// their attributes; the gauge callback runs per collect and unregister stops it.
func TestMetricsInstruments(t *testing.T) {
	m, reader := manualMetrics(t)
	ctx := context.Background()
	m.Counter(metrics.Spec{Name: "jobs.completed", Unit: "{job}"}).Add(ctx, 3, metrics.StringAttribute("job.kind", "mail"))
	m.Histogram(metrics.Spec{Name: "work.duration", Unit: "s"}).Record(ctx, 0.5)
	calls := 0
	unregister, err := m.Gauge(metrics.Spec{Name: "queue.depth"}, func(context.Context) []metrics.Observation {
		calls++
		return []metrics.Observation{{Value: 7, Attrs: []metrics.Attribute{metrics.StringAttribute("queue", "a")}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, reader)
	sum, ok := got["jobs.completed"].(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 3 || !sum.IsMonotonic {
		t.Fatalf("counter = %#v", got["jobs.completed"])
	}
	if v, _ := sum.DataPoints[0].Attributes.Value("job.kind"); v.AsString() != "mail" {
		t.Fatalf("counter attrs = %v", sum.DataPoints[0].Attributes)
	}
	if h, ok := got["work.duration"].(metricdata.Histogram[float64]); !ok || h.DataPoints[0].Count != 1 || h.DataPoints[0].Sum != 0.5 {
		t.Fatalf("histogram = %#v", got["work.duration"])
	}
	if g, ok := got["queue.depth"].(metricdata.Gauge[float64]); !ok || g.DataPoints[0].Value != 7 {
		t.Fatalf("gauge = %#v", got["queue.depth"])
	}
	unregister()
	unregister()
	collect(t, reader)
	if calls != 1 {
		t.Fatalf("gauge callback ran %d times; unregister did not stop it", calls)
	}
}

func TestMetricsRefusals(t *testing.T) {
	m, _ := manualMetrics(t)
	m.Counter(metrics.Spec{}).Add(context.Background(), 1)           // no-op, no panic
	m.Histogram(metrics.Spec{}).Record(context.Background(), 1)      // no-op, no panic
	if _, err := m.Gauge(metrics.Spec{Name: "g"}, nil); err == nil { // a gauge needs a callback
		t.Error("nil observe admitted")
	}
	var nilMetrics *Metrics
	nilMetrics.Counter(metrics.Spec{Name: "c"}).Add(context.Background(), 1)
	if _, err := OpenMetrics(context.Background(), Config{Exporter: ExporterProvider}); err == nil {
		t.Error("provider mode without MeterProvider admitted")
	}
	if _, err := OpenMetrics(context.Background(), Config{MetricInterval: -time.Second}); err == nil {
		t.Error("negative interval admitted")
	}
	if _, err := OpenMetrics(context.Background(), Config{Exporter: ExporterOTLPHTTP, Endpoint: "http://collector.example.com:4318", RuntimeMode: environment.ModeProduction}); !errors.Is(err, ErrInsecureExporter) {
		t.Errorf("production plain HTTP = %v", err)
	}
	if _, err := OpenMetrics(context.Background(), Config{Exporter: ExporterOTLPHTTP}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty endpoint = %v", err)
	}
}

// TestHTTPRequestMetrics: through a real WebHandler with RouteNames, the
// duration histogram carries only method, route (or unmatched) and status
// class; no path, id or token reaches any attribute.
func TestHTTPRequestMetrics(t *testing.T) {
	m, reader := manualMetrics(t)
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	tracer, err := Open(context.Background(), Config{Exporter: ExporterProvider, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	h.Use(Middleware(tracer, HTTPConfig{Meter: m}), cloneRequest, RouteNames())
	h.Handle("GET", "/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			http.NotFound(w, r)
		}
	})
	for _, r := range []*http.Request{
		httptest.NewRequest("GET", "/users/42", nil),
		httptest.NewRequest("GET", "/users/42", nil),
		httptest.NewRequest("GET", "/users/missing", nil),
		httptest.NewRequest("GET", "/reset/tok_SECRET", nil),
		httptest.NewRequest("POST", "/users/42", nil),
	} {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	hist, ok := collect(t, reader)["http.server.request.duration"].(metricdata.Histogram[float64])
	if !ok {
		t.Fatal("no http.server.request.duration histogram")
	}
	series := map[string]uint64{}
	for _, dp := range hist.DataPoints {
		var parts []string
		for _, kv := range dp.Attributes.ToSlice() {
			parts = append(parts, string(kv.Key)+"="+kv.Value.AsString())
		}
		key := strings.Join(parts, ",")
		series[key] = dp.Count
		for _, leak := range []string{"42", "missing", "tok_SECRET", "reset"} {
			if strings.Contains(key, leak) {
				t.Errorf("series %q leaks %q", key, leak)
			}
		}
	}
	want := map[string]uint64{
		"http.request.method=GET,http.response.status_code=2xx,http.route=/users/{id}": 2,
		"http.request.method=GET,http.response.status_code=4xx,http.route=/users/{id}": 1,
		"http.request.method=GET,http.response.status_code=4xx,http.route=unmatched":   1,
		"http.request.method=POST,http.response.status_code=4xx,http.route=unmatched":  1,
	}
	for k, n := range want {
		if series[k] != n {
			t.Errorf("series %q count = %d, want %d (all: %v)", k, series[k], n, series)
		}
	}
	if len(series) != len(want) {
		t.Errorf("series = %v", series)
	}
}

// otlpMetricsReceiver records OTLP/HTTP metric requests.
type otlpMetricsReceiver struct {
	mu     sync.Mutex
	paths  []string
	header []http.Header
	names  []string
}

func (rcv *otlpMetricsReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var req colmetricpb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		http.Error(w, "proto", http.StatusBadRequest)
		return
	}
	rcv.mu.Lock()
	rcv.paths = append(rcv.paths, r.URL.Path)
	rcv.header = append(rcv.header, r.Header.Clone())
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				rcv.names = append(rcv.names, m.GetName())
			}
		}
	}
	rcv.mu.Unlock()
	out, _ := proto.Marshal(&colmetricpb.ExportMetricsServiceResponse{})
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}

// TestMetricsOTLPHTTP: metrics reach <base>/v1/metrics with the configured
// headers and gzip, over TLS, with every OTEL_EXPORTER_OTLP_* variable poisoned.
func TestMetricsOTLPHTTP(t *testing.T) {
	rcv := &otlpMetricsReceiver{}
	srv := httptest.NewTLSServer(rcv)
	defer srv.Close()
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_METRICS_"} {
		t.Setenv(prefix+"ENDPOINT", "http://poison.invalid:1/poison")
		t.Setenv(prefix+"HEADERS", "X-Poison=1,Authorization=poison")
		t.Setenv(prefix+"TIMEOUT", "1")
		t.Setenv(prefix+"COMPRESSION", "none")
		t.Setenv(prefix+"INSECURE", "true")
		t.Setenv(prefix+"CERTIFICATE", "/nonexistent/poison.pem")
	}
	m, err := OpenMetrics(context.Background(), Config{
		Exporter: ExporterOTLPHTTP, Endpoint: srv.URL + "/otlp", Headers: sdk.Secret("Authorization=Basic%20bWV0cmljczp0ZXN0"),
		RuntimeMode: environment.ModeProduction, tlsConfig: trustServer(srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Counter(metrics.Spec{Name: "shipped.total"}).Add(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	_ = m.Shutdown(ctx)
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.paths) == 0 || rcv.paths[0] != "/otlp/v1/metrics" {
		t.Fatalf("paths = %v", rcv.paths)
	}
	h := rcv.header[0]
	if h.Get("Authorization") != "Basic bWV0cmljczp0ZXN0" || h.Get("X-Poison") != "" || h.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers = %v", h)
	}
	if !strings.Contains(strings.Join(rcv.names, ","), "shipped.total") {
		t.Fatalf("metric names = %v", rcv.names)
	}
}

// TestMetricsStdout: the stdout exporter writes the instruments on flush.
func TestMetricsStdout(t *testing.T) {
	var out bytes.Buffer
	m, err := OpenMetrics(context.Background(), Config{Exporter: ExporterStdout, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	m.Counter(metrics.Spec{Name: "stdout.total"}).Add(context.Background(), 2)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stdout.total") {
		t.Fatalf("stdout export lacks the counter:\n%s", out.String())
	}
}
