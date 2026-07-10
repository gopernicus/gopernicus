package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

func TestConsole_Kind(t *testing.T) {
	c := NewConsole(identity.KindPhone, slog.Default())
	if got := c.Kind(); got != identity.KindPhone {
		t.Errorf("Kind() = %q, want %q", got, identity.KindPhone)
	}
}

func TestConsole_Notify_LogsKindAddressSubjectBody(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	c := NewConsole(identity.KindPhone, log)

	to := identity.Address{Kind: identity.KindPhone, Value: "+15551234567"}
	msg := Message{Subject: "Your code", Body: "123456"}
	if err := c.Notify(context.Background(), to, msg); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	// A non-empty buffer proves the logger we passed is the one used.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v (raw: %s)", err, buf.String())
	}
	if entry["kind"] != identity.KindPhone {
		t.Errorf("logged kind = %v, want %q", entry["kind"], identity.KindPhone)
	}
	if entry["to"] != to.Value {
		t.Errorf("logged to = %v, want %q", entry["to"], to.Value)
	}
	if entry["subject"] != msg.Subject {
		t.Errorf("logged subject = %v, want %q", entry["subject"], msg.Subject)
	}
	if entry["body"] != msg.Body {
		t.Errorf("logged body = %v, want %q", entry["body"], msg.Body)
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

	c := NewConsole(identity.KindEmail, nil)
	to := identity.Address{Kind: identity.KindEmail, Value: "a@example.com"}
	if err := c.Notify(context.Background(), to, Message{Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("nil logger should fall back to slog.Default(); got no output")
	}
}
