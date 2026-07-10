package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// fakeSender captures the last Message it was asked to send and can be primed to
// fail, so tests can assert field mapping and error propagation.
type fakeSender struct {
	sent   email.Message
	called bool
	err    error
}

func (f *fakeSender) Send(ctx context.Context, msg email.Message) error {
	f.called = true
	f.sent = msg
	return f.err
}

func TestMailerBridge_Kind(t *testing.T) {
	b := NewMailerBridge(&fakeSender{}, "noreply@example.com")
	if got := b.Kind(); got != identity.KindEmail {
		t.Errorf("Kind() = %q, want %q", got, identity.KindEmail)
	}
}

func TestMailerBridge_Notify_MapsFieldsAndInjectsFrom(t *testing.T) {
	fake := &fakeSender{}
	b := NewMailerBridge(fake, "noreply@example.com")

	to := identity.Address{Kind: identity.KindEmail, Value: "invitee@example.com"}
	msg := Message{Subject: "You're invited", Body: "click here"}
	if err := b.Notify(context.Background(), to, msg); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if !fake.called {
		t.Fatal("sender was not called")
	}
	if fake.sent.From != "noreply@example.com" {
		t.Errorf("From = %q, want %q", fake.sent.From, "noreply@example.com")
	}
	if len(fake.sent.To) != 1 || fake.sent.To[0] != to.Value {
		t.Errorf("To = %v, want [%q]", fake.sent.To, to.Value)
	}
	if fake.sent.Subject != msg.Subject {
		t.Errorf("Subject = %q, want %q", fake.sent.Subject, msg.Subject)
	}
	if fake.sent.Text != msg.Body {
		t.Errorf("Text = %q, want %q", fake.sent.Text, msg.Body)
	}
}

func TestMailerBridge_Notify_SendErrorPropagates(t *testing.T) {
	sendErr := errors.New("smtp unavailable")
	fake := &fakeSender{err: sendErr}
	b := NewMailerBridge(fake, "noreply@example.com")

	to := identity.Address{Kind: identity.KindEmail, Value: "invitee@example.com"}
	err := b.Notify(context.Background(), to, Message{Subject: "s", Body: "b"})
	if !errors.Is(err, sendErr) {
		t.Fatalf("Notify() error = %v, want %v", err, sendErr)
	}
}

func TestMailerBridge_Notify_ValidateErrorPropagates(t *testing.T) {
	fake := &fakeSender{}
	b := NewMailerBridge(fake, "") // empty From fails email.Message.Validate

	to := identity.Address{Kind: identity.KindEmail, Value: "invitee@example.com"}
	err := b.Notify(context.Background(), to, Message{Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("expected a validation error for empty From, got nil")
	}
	if fake.called {
		t.Error("sender should not be called when validation fails")
	}
}
