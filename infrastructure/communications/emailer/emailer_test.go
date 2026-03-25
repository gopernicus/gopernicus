package emailer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/notify"
)

// mockClient is a test implementation of the Client interface.
type mockClient struct {
	sendFunc func(ctx context.Context, email Email) error
}

func (m *mockClient) Send(ctx context.Context, email Email) error {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, email)
	}
	return nil
}

func newTestEmailer(t *testing.T, client Client) *Emailer {
	t.Helper()
	e, err := New(slog.Default(), client, "default@example.com")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return e
}

func TestSend_Success(t *testing.T) {
	clientCalled := false
	var receivedEmail Email

	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			clientCalled = true
			receivedEmail = email
			return nil
		},
	})

	err := e.Send(context.Background(), Email{
		To:      "recipient@example.com",
		Subject: "Test Subject",
		HTML:    "<h1>Test</h1>",
		Text:    "Test",
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !clientCalled {
		t.Error("client was not called")
	}
	if receivedEmail.To != "recipient@example.com" {
		t.Errorf("To = %q, want %q", receivedEmail.To, "recipient@example.com")
	}
	if receivedEmail.Subject != "Test Subject" {
		t.Errorf("Subject = %q, want %q", receivedEmail.Subject, "Test Subject")
	}
}

func TestSend_DefaultFrom(t *testing.T) {
	var receivedEmail Email

	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			receivedEmail = email
			return nil
		},
	})

	err := e.Send(context.Background(), Email{
		To:      "recipient@example.com",
		Subject: "Test",
		HTML:    "<p>Test</p>",
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if receivedEmail.From != "default@example.com" {
		t.Errorf("From = %q, want %q", receivedEmail.From, "default@example.com")
	}
}

func TestSend_CustomFrom(t *testing.T) {
	var receivedEmail Email

	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			receivedEmail = email
			return nil
		},
	})

	err := e.Send(context.Background(), Email{
		To:      "recipient@example.com",
		From:    "custom@example.com",
		Subject: "Test",
		HTML:    "<p>Test</p>",
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if receivedEmail.From != "custom@example.com" {
		t.Errorf("From = %q, want %q", receivedEmail.From, "custom@example.com")
	}
}

func TestSend_MissingTo(t *testing.T) {
	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			t.Error("client should not be called for invalid email")
			return nil
		},
	})

	err := e.Send(context.Background(), Email{
		Subject: "Test",
		HTML:    "<p>Test</p>",
	})

	if err == nil {
		t.Fatal("Send() should return error for missing To")
	}
	if !strings.Contains(err.Error(), "recipient (To) is required") {
		t.Errorf("error = %q, want to contain 'recipient (To) is required'", err.Error())
	}
}

func TestSend_MissingSubject(t *testing.T) {
	e := newTestEmailer(t, &mockClient{})

	err := e.Send(context.Background(), Email{
		To:   "recipient@example.com",
		HTML: "<p>Test</p>",
	})

	if err == nil {
		t.Fatal("Send() should return error for missing Subject")
	}
	if !strings.Contains(err.Error(), "subject is required") {
		t.Errorf("error = %q, want to contain 'subject is required'", err.Error())
	}
}

func TestSend_MissingBody(t *testing.T) {
	e := newTestEmailer(t, &mockClient{})

	err := e.Send(context.Background(), Email{
		To:      "recipient@example.com",
		Subject: "Test",
	})

	if err == nil {
		t.Fatal("Send() should return error for missing body")
	}
	if !strings.Contains(err.Error(), "HTML or Text body") {
		t.Errorf("error = %q, want to contain 'HTML or Text body'", err.Error())
	}
}

func TestSend_ClientError(t *testing.T) {
	expectedErr := errors.New("mock client error")
	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			return expectedErr
		},
	})

	err := e.Send(context.Background(), Email{
		To:      "recipient@example.com",
		Subject: "Test",
		HTML:    "<p>Test</p>",
	})

	if err == nil {
		t.Fatal("Send() should return error when client fails")
	}
	if !strings.Contains(err.Error(), "send email") {
		t.Errorf("error = %q, want to contain 'send email'", err.Error())
	}
}

func TestNotify(t *testing.T) {
	var receivedEmail Email

	e := newTestEmailer(t, &mockClient{
		sendFunc: func(ctx context.Context, email Email) error {
			receivedEmail = email
			return nil
		},
	})

	err := e.Notify(context.Background(), notify.Notification{
		Recipient: "user@example.com",
		Subject:   "Alert",
		Body:      "Something happened",
	})

	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if receivedEmail.To != "user@example.com" {
		t.Errorf("To = %q, want %q", receivedEmail.To, "user@example.com")
	}
	if receivedEmail.Subject != "Alert" {
		t.Errorf("Subject = %q, want %q", receivedEmail.Subject, "Alert")
	}
	if receivedEmail.Text != "Something happened" {
		t.Errorf("Text = %q, want %q", receivedEmail.Text, "Something happened")
	}
	if receivedEmail.HTML != "" {
		t.Errorf("HTML = %q, want empty (Notify uses plain text)", receivedEmail.HTML)
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple tags", "<p>Hello World</p>", "Hello World"},
		{"nested tags", "<div><h1>Title</h1><p>Content</p></div>", "TitleContent"},
		{"self-closing", "Line 1<br/>Line 2", "Line 1Line 2"},
		{"no tags", "Plain text", "Plain text"},
		{"empty", "", ""},
		{"only tags", "<div></div>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripHTMLTags(tt.input)
			if result != tt.expected {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
