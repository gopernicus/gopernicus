package otel

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPServerSpansAndExplicitPropagation(t *testing.T) {
	remote := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled, Remote: true})
	for _, test := range []struct {
		name, header, method string
		trust, parent        bool
	}{
		{name: "ignored by default", header: "00-" + remote.TraceID().String() + "-" + remote.SpanID().String() + "-01", method: "GET"},
		{name: "trusted", header: "00-" + remote.TraceID().String() + "-" + remote.SpanID().String() + "-01", method: "GET", trust: true, parent: true},
		{name: "malformed", header: "invalid", method: "GET", trust: true},
		{name: "unknown method", method: "SECRET-METHOD"},
		{name: "query method", method: "QUERY"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer provider.Shutdown(context.Background())
			tracer, err := Open(context.Background(), Config{Exporter: ExporterProvider, Provider: provider})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(test.method, "https://app.example:8443/private?secret=VALUE", nil)
			req.Pattern = test.method + " /items/{id}"
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("traceparent", test.header)
			req.Header.Set("baggage", "secret=VALUE")
			Middleware(tracer, HTTPConfig{TrustTraceContext: test.trust})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if baggage.FromContext(r.Context()).Len() != 0 {
					t.Error("baggage extracted")
				}
				w.WriteHeader(201)
			})).ServeHTTP(httptest.NewRecorder(), req)
			span := recorder.Ended()[0]
			if span.SpanKind() != trace.SpanKindServer || span.Parent().IsValid() != test.parent {
				t.Fatalf("kind/parent: %v %v", span.SpanKind(), span.Parent())
			}
			if test.parent && !span.Parent().Equal(remote) {
				t.Fatal("remote parent changed")
			}
			values := map[attribute.Key]attribute.Value{}
			for _, a := range span.Attributes() {
				values[a.Key] = a.Value
			}
			for key, want := range map[attribute.Key]int64{"http.response.status_code": 201, "server.port": 8443, "network.peer.port": 12345} {
				if value := values[key]; value.Type() != attribute.INT64 || value.AsInt64() != want {
					t.Fatalf("%s=%v", key, value)
				}
			}
			if values["http.route"].AsString() != "/items/{id}" || values["url.query"].Type() != attribute.INVALID {
				t.Fatal("route or privacy metadata incorrect")
			}
			wantName := test.method + " /items/{id}"
			if test.method == "SECRET-METHOD" {
				wantName = "HTTP /items/{id}"
				if values["http.request.method"].AsString() != "_OTHER" {
					t.Fatal("unbounded method")
				}
			}
			if span.Name() != wantName {
				t.Fatalf("name=%q want=%q", span.Name(), wantName)
			}
		})
	}
}

func TestRootSamplingAndParentDecisions(t *testing.T) {
	for _, rate := range []float64{0, 1} {
		sampler := rootSampler(rate)
		for _, parent := range []string{"root", "sampled local", "unsampled local", "sampled remote", "unsampled remote"} {
			ctx := context.Background()
			want := rate == 1
			if parent != "root" {
				sampled := parent == "sampled local" || parent == "sampled remote"
				flags := trace.TraceFlags(0)
				if sampled {
					flags = trace.FlagsSampled
				}
				sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: flags, Remote: parent == "sampled remote" || parent == "unsampled remote"})
				ctx = trace.ContextWithSpanContext(ctx, sc)
				want = sampled
			}
			result := sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: ctx, TraceID: trace.TraceID{3}})
			if (result.Decision == sdktrace.RecordAndSample) != want {
				t.Fatalf("rate=%v parent=%s decision=%v", rate, parent, result.Decision)
			}
		}
	}
	for _, rate := range []float64{-1, 2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Open(context.Background(), Config{Exporter: ExporterOTLPGRPC, SampleRate: rate}); err == nil {
			t.Fatalf("invalid rate accepted: %v", rate)
		}
	}
	var output bytes.Buffer
	tracer, err := Open(context.Background(), Config{Stdout: &output})
	if err != nil {
		t.Fatal(err)
	}
	defer tracer.Shutdown(context.Background())
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}}))
	_, span := tracer.StartSpan(ctx, "not sampled")
	span.Finish()
	if output.Len() != 0 {
		t.Fatal("stdout ignored unsampled parent")
	}
}

func TestHTTPKeepsLocalParentAndBoundsUnknownRoute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	tracer, _ := Open(context.Background(), Config{Exporter: ExporterProvider, Provider: provider})
	ctx, parent := provider.Tracer("host").Start(context.Background(), "parent")
	req := httptest.NewRequest("GET", "https://app.example/private/123?secret=VALUE", nil).WithContext(ctx)
	Middleware(tracer, HTTPConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })).ServeHTTP(httptest.NewRecorder(), req)
	span := recorder.Ended()[0]
	parent.End()
	if !span.Parent().Equal(parent.SpanContext()) || span.Name() != "GET" {
		t.Fatalf("local parent or bounded name wrong: %v %s", span.Parent(), span.Name())
	}
	var failure bool
	for _, a := range span.Attributes() {
		if a.Key == "error.type" && a.Value.AsString() == "http.request_failed" {
			failure = true
		}
	}
	if !failure {
		t.Fatal("failure category missing")
	}
}
