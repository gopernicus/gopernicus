package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestConsole_SendLogsAddressBody(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	c := NewConsole(log)

	to := "+15551234567"
	body := "123456"
	if err := c.Send(context.Background(), to, body); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// A non-empty buffer proves the logger we passed is the one used.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v (raw: %s)", err, buf.String())
	}
	if entry["to"] != to {
		t.Errorf("logged to = %v, want %q", entry["to"], to)
	}
	if entry["body"] != body {
		t.Errorf("logged body = %v, want %q", entry["body"], body)
	}
}

// TestConsole_NilLogger_UsesSlogDefault asserts the doc comment's promise: a nil
// logger falls back to slog.Default(). Swapping the process default to a
// buffer-backed logger and observing output there proves the fallback.
func TestConsole_NilLogger_UsesSlogDefault(t *testing.T) {
	var buf bytes.Buffer
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	c := NewConsole(nil)
	to := "a@example.com"
	if err := c.Send(context.Background(), to, "b"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("nil logger should fall back to slog.Default(); got no output")
	}
}
