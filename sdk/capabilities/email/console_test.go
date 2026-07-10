package email

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestConsole_Send_LogsToRecipientAndSubject(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	c := NewConsole(log)

	msg := Message{
		From:    "sender@example.com",
		To:      []string{"a@example.com", "b@example.com"},
		Subject: "Weekly digest",
		Text:    "body",
	}
	if err := c.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v (raw: %s)", err, buf.String())
	}
	if entry["subject"] != msg.Subject {
		t.Errorf("logged subject = %v, want %q", entry["subject"], msg.Subject)
	}
	to, ok := entry["to"].([]any)
	if !ok || len(to) != 2 || to[0] != "a@example.com" || to[1] != "b@example.com" {
		t.Errorf("logged to = %v, want %v", entry["to"], msg.To)
	}
	if entry["from"] != msg.From {
		t.Errorf("logged from = %v, want %q", entry["from"], msg.From)
	}
}

func TestConsole_Send_InvalidMessageNotLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := NewConsole(log)

	err := c.Send(context.Background(), Message{}) // missing everything
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output for an invalid message, got %q", buf.String())
	}
}

// TestConsole_NilLogger asserts the doc comment's promise: a nil logger
// discards output — Send succeeds without panicking and without delivering
// anywhere. (Historical note: this originally documented a nil-writer panic,
// fixed 2026-07-02 by defaulting to io.Discard.)
func TestConsole_NilLogger(t *testing.T) {
	c := NewConsole(nil)
	if err := c.Send(context.Background(), validMessage()); err != nil {
		t.Fatalf("Send() with nil logger error = %v, want nil (output discarded)", err)
	}
}
