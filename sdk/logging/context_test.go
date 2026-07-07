package logging

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

func TestTracingHandler_RequestID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithRequestID(context.Background(), "req-789")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["request_id"] != "req-789" {
		t.Errorf("request_id = %v, want req-789", entry["request_id"])
	}
}

func TestTracingHandler_TraceID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithTraceID(context.Background(), "trace-123")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["trace_id"] != "trace-123" {
		t.Errorf("trace_id = %v, want trace-123", entry["trace_id"])
	}
}

func TestTracingHandler_SpanID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithSpanID(context.Background(), "span-456")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["span_id"] != "span-456" {
		t.Errorf("span_id = %v, want span-456", entry["span_id"])
	}
}

func TestTracingHandler_AllIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithTraceID(context.Background(), "trace-1")
	ctx = WithSpanID(ctx, "span-2")
	ctx = WithRequestID(ctx, "req-3")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["trace_id"] != "trace-1" {
		t.Errorf("trace_id = %v, want trace-1", entry["trace_id"])
	}
	if entry["span_id"] != "span-2" {
		t.Errorf("span_id = %v, want span-2", entry["span_id"])
	}
	if entry["request_id"] != "req-3" {
		t.Errorf("request_id = %v, want req-3", entry["request_id"])
	}
}

func TestTracingHandler_TraceSpanEmptyStringSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithTraceID(context.Background(), "")
	ctx = WithSpanID(ctx, "")
	log.InfoContext(ctx, "empty")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["trace_id"]; ok {
		t.Error("empty trace_id should not be present")
	}
	if _, ok := entry["span_id"]; ok {
		t.Error("empty span_id should not be present")
	}
}

func TestTracingHandler_NoID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.InfoContext(context.Background(), "no id")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["request_id"]; ok {
		t.Error("request_id should not be present")
	}
}

func TestTracingHandler_EmptyStringSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := WithRequestID(context.Background(), "")
	log.InfoContext(ctx, "empty")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["request_id"]; ok {
		t.Error("empty request_id should not be present")
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

func TestRequestIDFromContext(t *testing.T) {
	if _, ok := RequestIDFromContext(context.Background()); ok {
		t.Error("empty context should report no request id")
	}
	ctx := WithRequestID(context.Background(), "r1")
	if v, ok := RequestIDFromContext(ctx); !ok || v != "r1" {
		t.Errorf("RequestIDFromContext = %q,%v, want r1,true", v, ok)
	}
}
