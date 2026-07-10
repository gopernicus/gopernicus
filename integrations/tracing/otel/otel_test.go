package otel_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gopernicus/gopernicus/integrations/tracing/otel"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

// TestProviderExporter drives the whole sdk/capabilities/tracing.Tracer surface against a
// caller-supplied TracerProvider whose SpanRecorder captures ended spans, so the
// assertions read span name, attributes, and status directly off the recorded
// spans without any exporter or network.
func TestProviderExporter(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	tracer, err := otel.Open(context.Background(), otel.Config{
		Exporter:    otel.ExporterProvider,
		ServiceName: "provider-test",
		Provider:    provider,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Provider mode must not take ownership of the caller's provider.
	if err := tracer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown should be a no-op in provider mode: %v", err)
	}

	wantErr := errors.New("boom")
	_, span := tracer.StartSpan(context.Background(), "operation.test")
	span.SetAttributes(tracing.StringAttribute("component", "otel-test"))
	span.RecordError(wantErr)
	span.Finish()

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(ended))
	}
	got := ended[0]

	if got.Name() != "operation.test" {
		t.Errorf("span name = %q, want %q", got.Name(), "operation.test")
	}
	if v, ok := attrValue(got.Attributes(), "component"); !ok || v != "otel-test" {
		t.Errorf("attribute component = %q (present=%v), want %q", v, ok, "otel-test")
	}
	if got.Status().Code != codes.Error {
		t.Errorf("status code = %v, want %v", got.Status().Code, codes.Error)
	}
	if got.Status().Description != wantErr.Error() {
		t.Errorf("status description = %q, want %q", got.Status().Description, wantErr.Error())
	}
	if !hasErrorEvent(got.Events()) {
		t.Errorf("expected a recorded error event on the span")
	}
}

// TestSpanFinisherExposesIDs drives the SpanIdentity path through the tracetest
// SpanRecorder: the finisher's TraceID/SpanID must be valid hex and match the
// recorded span's own SpanContext, so tracing.Middleware can link them onto log lines.
func TestSpanFinisherExposesIDs(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	tracer, err := otel.Open(context.Background(), otel.Config{
		Exporter:    otel.ExporterProvider,
		ServiceName: "identity-test",
		Provider:    provider,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, span := tracer.StartSpan(context.Background(), "identity.span")
	id, ok := span.(tracing.SpanIdentity)
	if !ok {
		t.Fatal("spanFinisher does not satisfy tracing.SpanIdentity")
	}
	traceID, spanID := id.TraceID(), id.SpanID()
	span.Finish()

	if len(traceID) != 32 {
		t.Errorf("TraceID() = %q, want a 32-hex-char trace id", traceID)
	}
	if len(spanID) != 16 {
		t.Errorf("SpanID() = %q, want a 16-hex-char span id", spanID)
	}

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(ended))
	}
	sc := ended[0].SpanContext()
	if got := sc.TraceID().String(); got != traceID {
		t.Errorf("finisher TraceID = %q, recorded span TraceID = %q", traceID, got)
	}
	if got := sc.SpanID().String(); got != spanID {
		t.Errorf("finisher SpanID = %q, recorded span SpanID = %q", spanID, got)
	}
}

// TestStdoutExporter redirects the stdout exporter to a buffer and asserts the
// emitted JSON carries the span name and attribute — the exporter is
// synchronous, so the span is present as soon as Finish returns.
func TestStdoutExporter(t *testing.T) {
	var buf bytes.Buffer
	tracer, err := otel.Open(context.Background(), otel.Config{
		Exporter:    otel.ExporterStdout,
		ServiceName: "stdout-test",
		Stdout:      &buf,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, span := tracer.StartSpan(context.Background(), "stdout.span")
	span.SetAttributes(tracing.StringAttribute("stage", "emit"))
	span.Finish()

	if err := tracer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("stdout exporter wrote nothing")
	}
	for _, want := range []string{"stdout.span", "stage", "emit", "stdout-test"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout output missing %q\n---\n%s", want, out)
		}
	}
}

// TestStdoutIsDefault confirms an empty Exporter falls back to stdout so a zero
// Config (with a redirected writer) is a usable dev tracer.
func TestStdoutIsDefault(t *testing.T) {
	var buf bytes.Buffer
	tracer, err := otel.Open(context.Background(), otel.Config{Stdout: &buf})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, span := tracer.StartSpan(context.Background(), "default.span")
	span.Finish()
	if err := tracer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(buf.String(), "default.span") {
		t.Errorf("default exporter did not write span; got %q", buf.String())
	}
}

// TestOTLPGRPCConstructs verifies the OTLP/gRPC mode builds a working tracer
// offline: the exporter connects lazily, so Open succeeds and Shutdown (with no
// spans buffered) returns without contacting a collector.
func TestOTLPGRPCConstructs(t *testing.T) {
	tracer, err := otel.Open(context.Background(), otel.Config{
		Exporter:    otel.ExporterOTLPGRPC,
		ServiceName: "otlp-test",
		Endpoint:    "localhost:4317",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if tracer == nil {
		t.Fatal("Open returned a nil tracer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with no buffered spans: %v", err)
	}
}

// TestOpenErrors covers the two Config validation failures.
func TestOpenErrors(t *testing.T) {
	if _, err := otel.Open(context.Background(), otel.Config{Exporter: otel.ExporterProvider}); err == nil {
		t.Error("provider mode without a Provider should error")
	}
	if _, err := otel.Open(context.Background(), otel.Config{Exporter: "nope"}); err == nil {
		t.Error("unknown exporter should error")
	}
}

// TestTracerSatisfiesPort ensures *Tracer is usable wherever the sdk port is
// accepted.
func TestTracerSatisfiesPort(t *testing.T) {
	var buf bytes.Buffer
	tracer, err := otel.Open(context.Background(), otel.Config{Stdout: &buf})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var port tracing.Tracer = tracer
	_, span := port.StartSpan(context.Background(), "port.span")
	span.Finish()
	if err := tracer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func attrValue(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

func hasErrorEvent(events []sdktrace.Event) bool {
	for _, e := range events {
		if e.Name == "exception" {
			return true
		}
	}
	return false
}
