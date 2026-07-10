package sendgrid

// Every test here is hermetic: the Sender is pointed at an httptest.Server via
// Config.Host (SendGrid's overridable request host), so no request ever leaves
// the process and no real email is sent. There is deliberately no live leg.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
)

// sendPayload mirrors the subset of SendGrid's v3 Mail Send JSON body this
// integration produces, so tests can assert the constructed wire request.
type sendPayload struct {
	From struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"from"`
	Subject          string `json:"subject"`
	Personalizations []struct {
		To []struct {
			Email string `json:"email"`
		} `json:"to"`
	} `json:"personalizations"`
	Content []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"content"`
}

// capturedRequest records what the fake SendGrid endpoint received.
type capturedRequest struct {
	called bool
	method string
	path   string
	auth   string
	body   sendPayload
}

// newFakeSendGrid starts an httptest server that captures the incoming request
// and replies with respStatus/respBody, and returns a Sender wired to it.
func newFakeSendGrid(t *testing.T, cfg Config, respStatus int, respBody string) (*Sender, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.called = true
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.WriteHeader(respStatus)
		if respBody != "" {
			_, _ = io.WriteString(w, respBody)
		}
	}))
	t.Cleanup(srv.Close)
	cfg.Host = srv.URL
	return New(cfg), captured
}

func TestSend_ConstructsRequest(t *testing.T) {
	sender, captured := newFakeSendGrid(t, Config{APIKey: "sg-test-key", FromName: "Gopernicus"}, http.StatusAccepted, "")

	msg := email.Message{
		From:    "noreply@example.com",
		To:      []string{"a@example.com", "b@example.com"},
		Subject: "Welcome",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	}
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if !captured.called {
		t.Fatal("fake SendGrid endpoint was never called")
	}
	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != sendPath {
		t.Errorf("path = %q, want %q", captured.path, sendPath)
	}
	if captured.auth != "Bearer sg-test-key" {
		t.Errorf("Authorization = %q, want %q", captured.auth, "Bearer sg-test-key")
	}

	if captured.body.From.Email != "noreply@example.com" {
		t.Errorf("from email = %q, want %q", captured.body.From.Email, "noreply@example.com")
	}
	if captured.body.From.Name != "Gopernicus" {
		t.Errorf("from name = %q, want %q", captured.body.From.Name, "Gopernicus")
	}
	if captured.body.Subject != "Welcome" {
		t.Errorf("subject = %q, want %q", captured.body.Subject, "Welcome")
	}

	if len(captured.body.Personalizations) != 1 {
		t.Fatalf("personalizations = %d, want 1", len(captured.body.Personalizations))
	}
	tos := captured.body.Personalizations[0].To
	if len(tos) != 2 || tos[0].Email != "a@example.com" || tos[1].Email != "b@example.com" {
		t.Errorf("recipients = %+v, want [a@example.com b@example.com]", tos)
	}

	if len(captured.body.Content) != 2 {
		t.Fatalf("content parts = %d, want 2", len(captured.body.Content))
	}
	if captured.body.Content[0].Type != "text/plain" || captured.body.Content[0].Value != "plain body" {
		t.Errorf("content[0] = %+v, want text/plain 'plain body'", captured.body.Content[0])
	}
	if captured.body.Content[1].Type != "text/html" || captured.body.Content[1].Value != "<p>html body</p>" {
		t.Errorf("content[1] = %+v, want text/html '<p>html body</p>'", captured.body.Content[1])
	}
}

func TestSend_TextOnlyOmitsHTMLContent(t *testing.T) {
	sender, captured := newFakeSendGrid(t, Config{APIKey: "k"}, http.StatusAccepted, "")

	msg := email.Message{
		From:    "noreply@example.com",
		To:      []string{"a@example.com"},
		Subject: "Text only",
		Text:    "just text",
	}
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(captured.body.Content) != 1 {
		t.Fatalf("content parts = %d, want 1", len(captured.body.Content))
	}
	if captured.body.Content[0].Type != "text/plain" {
		t.Errorf("content[0] type = %q, want text/plain", captured.body.Content[0].Type)
	}
}

func TestSend_InvalidMessageNeverCallsAPI(t *testing.T) {
	sender, captured := newFakeSendGrid(t, Config{APIKey: "k"}, http.StatusAccepted, "")

	err := sender.Send(context.Background(), email.Message{}) // missing everything
	if err == nil {
		t.Fatal("expected validation error for empty message, got nil")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("error = %v, want wraps ErrInvalidInput", err)
	}
	if captured.called {
		t.Error("SendGrid endpoint must not be called for an invalid message")
	}
}

func TestSend_StatusErrorsMapToKinds(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error // nil means "non-nil error, no specific sentinel"
	}{
		{"bad request", http.StatusBadRequest, sdk.ErrInvalidInput},
		{"unauthorized", http.StatusUnauthorized, sdk.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, sdk.ErrForbidden},
		{"not found", http.StatusNotFound, sdk.ErrNotFound},
		{"server error", http.StatusInternalServerError, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, _ := newFakeSendGrid(t, Config{APIKey: "k"}, tt.status, `{"errors":[{"message":"boom"}]}`)

			err := sender.Send(context.Background(), email.Message{
				From:    "noreply@example.com",
				To:      []string{"a@example.com"},
				Subject: "s",
				Text:    "t",
			})
			if err == nil {
				t.Fatalf("status %d: expected error, got nil", tt.status)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("status %d: error = %v, want wraps %v", tt.status, err, tt.want)
			}
		})
	}
}
