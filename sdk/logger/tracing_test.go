package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewTracingHandler(handler))
}

func parseJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, string(data))
	}
	return m
}

func TestTracingHandler_AllIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := context.Background()
	ctx = WithTraceID(ctx, "trace-123")
	ctx = WithSpanID(ctx, "span-456")
	ctx = WithRequestID(ctx, "req-789")

	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["trace_id"] != "trace-123" {
		t.Errorf("trace_id = %v, want trace-123", entry["trace_id"])
	}
	if entry["span_id"] != "span-456" {
		t.Errorf("span_id = %v, want span-456", entry["span_id"])
	}
	if entry["request_id"] != "req-789" {
		t.Errorf("request_id = %v, want req-789", entry["request_id"])
	}
}

func TestTracingHandler_PartialIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithRequestID(context.Background(), "req-only")

	log.InfoContext(ctx, "partial")

	entry := parseJSON(t, buf.Bytes())
	if entry["request_id"] != "req-only" {
		t.Errorf("request_id = %v, want req-only", entry["request_id"])
	}
	if _, ok := entry["trace_id"]; ok {
		t.Errorf("trace_id should not be present, got %v", entry["trace_id"])
	}
	if _, ok := entry["span_id"]; ok {
		t.Errorf("span_id should not be present, got %v", entry["span_id"])
	}
}

func TestTracingHandler_NoIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.InfoContext(context.Background(), "no ids")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["trace_id"]; ok {
		t.Error("trace_id should not be present")
	}
	if _, ok := entry["span_id"]; ok {
		t.Error("span_id should not be present")
	}
	if _, ok := entry["request_id"]; ok {
		t.Error("request_id should not be present")
	}
}

func TestTracingHandler_EmptyStringsSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithTraceID(context.Background(), "")

	log.InfoContext(ctx, "empty")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["trace_id"]; ok {
		t.Error("empty trace_id should not be present")
	}
}

func TestTracingHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tracing := NewTracingHandler(handler)
	log := slog.New(tracing.WithAttrs([]slog.Attr{slog.String("service", "test")}))

	ctx := WithTraceID(context.Background(), "trace-attr")

	log.InfoContext(ctx, "with attrs")

	entry := parseJSON(t, buf.Bytes())
	if entry["service"] != "test" {
		t.Errorf("service = %v, want test", entry["service"])
	}
	if entry["trace_id"] != "trace-attr" {
		t.Errorf("trace_id = %v, want trace-attr", entry["trace_id"])
	}
}

func TestTracingHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tracing := NewTracingHandler(handler)
	log := slog.New(tracing.WithGroup("app"))

	ctx := WithRequestID(context.Background(), "req-group")

	log.InfoContext(ctx, "grouped", "key", "value")

	entry := parseJSON(t, buf.Bytes())
	// When a group is active, slog nests all record attributes under it.
	// The trace IDs injected via AddAttrs end up inside the group.
	app, ok := entry["app"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'app' group in output, got: %v", entry)
	}
	if app["request_id"] != "req-group" {
		t.Errorf("app.request_id = %v, want req-group", app["request_id"])
	}
	if app["key"] != "value" {
		t.Errorf("app.key = %v, want value", app["key"])
	}
}

func TestTracingHandler_Enabled(t *testing.T) {
	handler := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	tracing := NewTracingHandler(handler)

	if tracing.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("DEBUG should not be enabled at WARN level")
	}
	if !tracing.Enabled(context.Background(), slog.LevelError) {
		t.Error("ERROR should be enabled at WARN level")
	}
}

func TestNew_WithTracing(t *testing.T) {
	// Ensure WithTracing() option works through the New constructor
	log := New(Options{
		Level:  "DEBUG",
		Format: "json",
		Output: "STDERR",
	}, WithTracing())

	if log == nil {
		t.Fatal("New with WithTracing returned nil")
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithTraceID(ctx, "t1")
	ctx = WithSpanID(ctx, "s1")
	ctx = WithRequestID(ctx, "r1")

	if v, ok := ctx.Value(traceIDKey).(string); !ok || v != "t1" {
		t.Errorf("trace_id = %v, want t1", v)
	}
	if v, ok := ctx.Value(spanIDKey).(string); !ok || v != "s1" {
		t.Errorf("span_id = %v, want s1", v)
	}
	if v, ok := ctx.Value(requestIDKey).(string); !ok || v != "r1" {
		t.Errorf("request_id = %v, want r1", v)
	}
}
