package notify

import (
	"context"
	"testing"
)

// Compile-time check that a mock can satisfy Notifier.
type mockNotifier struct{}

func (m *mockNotifier) Notify(ctx context.Context, n Notification) error { return nil }

var _ Notifier = (*mockNotifier)(nil)

func TestNotification_Fields(t *testing.T) {
	n := Notification{
		Recipient: "user@example.com",
		Subject:   "Hello",
		Body:      "World",
		Metadata:  map[string]string{"key": "value"},
	}

	if n.Recipient != "user@example.com" {
		t.Errorf("Recipient = %q, want %q", n.Recipient, "user@example.com")
	}
	if n.Subject != "Hello" {
		t.Errorf("Subject = %q, want %q", n.Subject, "Hello")
	}
	if n.Body != "World" {
		t.Errorf("Body = %q, want %q", n.Body, "World")
	}
	if n.Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %q, want %q", n.Metadata["key"], "value")
	}
}
