package messagingsvc

import (
	"context"
	"errors"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

type fakeInquiries struct{ items []messaging.Inquiry }

func (f *fakeInquiries) Create(ctx context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	f.items = append(f.items, in)
	return in, nil
}
func (f *fakeInquiries) List(ctx context.Context) ([]messaging.Inquiry, error) { return f.items, nil }

type recordSender struct {
	sent []email.Message
	fail bool
}

func (r *recordSender) Send(ctx context.Context, msg email.Message) error {
	if r.fail {
		return errors.New("smtp down")
	}
	r.sent = append(r.sent, msg)
	return nil
}

func TestSubmit_PersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	repo := &fakeInquiries{}
	sender := &recordSender{}
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	svc := NewService(repo, sender, "site@example.com", "ops@example.com", func() time.Time { return now })

	inq, err := svc.Submit(ctx, "Alice", "alice@example.com", "Hello there")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(repo.items) != 1 || repo.items[0].ID != inq.ID {
		t.Errorf("inquiry not persisted")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected one email, got %d", len(sender.sent))
	}
	m := sender.sent[0]
	if m.To[0] != "ops@example.com" || m.From != "site@example.com" {
		t.Errorf("wrong addresses: %+v", m)
	}
	if !contains(m.Text, "alice@example.com") || !contains(m.Subject, "Alice") {
		t.Errorf("email content missing inquiry details: %+v", m)
	}
}

func TestSubmit_PersistsEvenWhenSendFails(t *testing.T) {
	ctx := context.Background()
	repo := &fakeInquiries{}
	svc := NewService(repo, &recordSender{fail: true}, "f", "t", func() time.Time { return time.Now() })

	saved, err := svc.Submit(ctx, "Bob", "bob@example.com", "Hi")
	if err == nil {
		t.Errorf("expected send error to surface")
	}
	if len(repo.items) != 1 || saved.ID == "" {
		t.Errorf("inquiry should be persisted even when notification fails")
	}
}

func TestSubmit_Validation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(&fakeInquiries{}, &recordSender{}, "f", "t", func() time.Time { return time.Now() })

	for _, tc := range []struct{ name, email, msg string }{
		{"", "a@b.com", "m"},
		{"A", "not-an-email", "m"},
		{"A", "a@b.com", ""},
	} {
		if _, err := svc.Submit(ctx, tc.name, tc.email, tc.msg); !errors.Is(err, errs.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput for %+v, got %v", tc, err)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
