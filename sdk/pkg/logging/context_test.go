package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewContextHandler(handler))
}

func parseJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, string(data))
	}
	return m
}

func TestContextHandler_RequestID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithRequestID(context.Background(), "req-789")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["request_id"] != "req-789" {
		t.Errorf("request_id = %v, want req-789", entry["request_id"])
	}
}

func TestContextHandler_TraceID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithTraceID(context.Background(), "trace-123")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["trace_id"] != "trace-123" {
		t.Errorf("trace_id = %v, want trace-123", entry["trace_id"])
	}
}

func TestContextHandler_SpanID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithSpanID(context.Background(), "span-456")
	log.InfoContext(ctx, "test message")

	entry := parseJSON(t, buf.Bytes())
	if entry["span_id"] != "span-456" {
		t.Errorf("span_id = %v, want span-456", entry["span_id"])
	}
}

func TestContextHandler_AllIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithTraceID(context.Background(), "trace-1")
	ctx = sdk.WithSpanID(ctx, "span-2")
	ctx = sdk.WithRequestID(ctx, "req-3")
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

func TestContextHandler_TraceSpanEmptyStringSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithTraceID(context.Background(), "")
	ctx = sdk.WithSpanID(ctx, "")
	log.InfoContext(ctx, "empty")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["trace_id"]; ok {
		t.Error("empty trace_id should not be present")
	}
	if _, ok := entry["span_id"]; ok {
		t.Error("empty span_id should not be present")
	}
}

func TestContextHandler_NoID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.InfoContext(context.Background(), "no id")

	entry := parseJSON(t, buf.Bytes())
	for _, key := range []string{"trace_id", "span_id", "request_id"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s should not be present", key)
		}
	}
}

func TestContextHandler_EmptyStringSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	ctx := sdk.WithRequestID(context.Background(), "")
	log.InfoContext(ctx, "empty")

	entry := parseJSON(t, buf.Bytes())
	if _, ok := entry["request_id"]; ok {
		t.Error("empty request_id should not be present")
	}
}

func TestContextHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	contextHandler := NewContextHandler(handler)
	log := slog.New(contextHandler).With("service", "test").WithGroup("app")

	ctx := sdk.WithRequestID(context.Background(), "req-group")
	log.InfoContext(ctx, "grouped", "key", "value")

	entry := parseJSON(t, buf.Bytes())
	if entry["service"] != "test" {
		t.Errorf("service = %v, want test", entry["service"])
	}
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

func TestContextHandler_Enabled(t *testing.T) {
	handler := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	contextHandler := NewContextHandler(handler)

	if contextHandler.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("DEBUG should not be enabled at WARN level")
	}
	if !contextHandler.Enabled(context.Background(), slog.LevelError) {
		t.Error("ERROR should be enabled at WARN level")
	}
}

func TestContextHandler_RecordReuse(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			var buf bytes.Buffer
			handler := NewContextHandler(slog.NewJSONHandler(&buf, nil))
			record := slog.NewRecord(time.Time{}, slog.LevelInfo, "reused", 0)
			// Add one at a time to leave spare capacity beyond slog's inline attrs.
			for i := range 8 {
				record.AddAttrs(slog.Int(fmt.Sprintf("key%d", i), i))
			}
			const calls = 32
			var wg sync.WaitGroup
			for i := range calls {
				log := func() {
					ctx := sdk.WithRequestID(context.Background(), fmt.Sprintf("req-%d", i))
					if err := handler.Handle(ctx, record); err != nil {
						t.Errorf("Handle: %v", err)
					}
				}
				if concurrent {
					wg.Go(log)
				} else {
					log()
				}
			}
			wg.Wait()
			if record.NumAttrs() != 8 {
				t.Errorf("source record has %d attributes, want 8", record.NumAttrs())
			}
			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			if len(lines) != calls {
				t.Fatalf("got %d log lines, want %d", len(lines), calls)
			}
			seen := make(map[string]bool)
			for _, line := range lines {
				entry := parseJSON(t, line)
				if _, ok := entry["!BUG"]; ok {
					t.Errorf("record was mutated without ownership: %s", line)
				}
				for i := range 8 {
					key := fmt.Sprintf("key%d", i)
					if entry[key] != float64(i) {
						t.Errorf("%s = %v, want %d", key, entry[key], i)
					}
				}
				id, ok := entry["request_id"].(string)
				if !ok || seen[id] {
					t.Errorf("missing or repeated request ID: %v", entry["request_id"])
				}
				seen[id] = true
			}
			for i := range calls {
				if !seen[fmt.Sprintf("req-%d", i)] {
					t.Errorf("missing request ID req-%d", i)
				}
			}
		})
	}
}
