package messagingsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

type inquiryCapture struct{ saved messaging.Inquiry }

func (r *inquiryCapture) Create(_ context.Context, inq messaging.Inquiry) (messaging.Inquiry, error) {
	r.saved = inq
	return inq, nil
}

func (r *inquiryCapture) List(context.Context) ([]messaging.Inquiry, error) {
	return []messaging.Inquiry{r.saved}, nil
}

func TestContactNameCannotInjectSMTPHeaders(t *testing.T) {
	store := &inquiryCapture{}
	// A validation failure must precede dialing this unused endpoint.
	sender := email.NewSMTP(email.SMTPConfig{Host: "127.0.0.1", Port: "0"})
	service := NewService(store, sender, "sender@example.test", "operator@example.test", sdk.IDGenerator{}, nil)
	saved, err := service.Submit(context.Background(), "Alice\r\nX-Audit-Injected: yes", "alice@example.test", "Hello")
	if !errors.Is(err, sdk.ErrInvalidInput) || strings.Contains(err.Error(), "X-Audit-Injected") {
		t.Fatalf("unsafe subject did not return a safe validation error: %v", err)
	}
	if saved.ID == "" || saved.ID != store.saved.ID {
		t.Fatal("notification failure must preserve the existing persisted-inquiry behavior")
	}
}
