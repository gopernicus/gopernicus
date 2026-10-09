package email

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func validMessage() Message {
	return Message{
		From:    "sender@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "hello",
		Text:    "body text",
	}
}

func TestMessage_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(Message) Message
		wantErr bool
	}{
		{"valid message", func(m Message) Message { return m }, false},
		{"blank recipient member", func(m Message) Message { m.To = []string{" "}; return m }, true},
		{"recipient list in one field", func(m Message) Message { m.To = []string{"a@example.test, b@example.test"}; return m }, true},
		{"display name in bare mailbox", func(m Message) Message { m.From = "Name <from@example.test>"; return m }, true},
		{"subject injection", func(m Message) Message { m.Subject = "Hello\r\nX-Injected: value"; return m }, true},
		{"invalid UTF8 subject", func(m Message) Message { m.Subject = string([]byte{255}); return m }, true},
		{"oversized mailbox", func(m Message) Message { m.From = strings.Repeat("x", 1000) + "@example.test"; return m }, true},

		{"missing from", func(m Message) Message { m.From = ""; return m }, true},
		{"blank from (whitespace only)", func(m Message) Message { m.From = "   "; return m }, true},
		{"missing to (nil slice)", func(m Message) Message { m.To = nil; return m }, true},
		{"missing to (empty slice)", func(m Message) Message { m.To = []string{}; return m }, true},
		{"missing subject", func(m Message) Message { m.Subject = ""; return m }, true},
		{"blank subject (whitespace only)", func(m Message) Message { m.Subject = "  "; return m }, true},
		{"missing text body", func(m Message) Message { m.Text = ""; return m }, true},
		{"blank text body (whitespace only)", func(m Message) Message { m.Text = "  "; return m }, true},
		// HTML is documented as optional and Validate never inspects it: a
		// message with HTML set but Text empty is still invalid (Text is the
		// only body field validation checks).
		{"HTML set but Text empty is still invalid", func(m Message) Message { m.Text = ""; m.HTML = "<p>hi</p>"; return m }, true},
		{"HTML optional and absent is fine", func(m Message) Message { m.HTML = ""; return m }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mutate(validMessage()).Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.wantErr && !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("error %v does not wrap sdk.ErrInvalidInput", err)
			}
		})
	}
}

func TestMessage_ValidateUnsubscribe(t *testing.T) {
	const link = "https://example.test/unsubscribe?token=secret-token"
	tests := []struct {
		name    string
		u       Unsubscribe
		to      []string
		wantErr bool
	}{
		{"zero value", Unsubscribe{}, nil, false},
		{"URL only", Unsubscribe{URL: link}, nil, false},
		{"mailbox only", Unsubscribe{Mailto: "unsubscribe@example.test"}, nil, false},
		{"URL and mailbox", Unsubscribe{URL: link, Mailto: "unsubscribe@example.test"}, nil, false},
		{"one-click", Unsubscribe{URL: link, OneClick: true}, nil, false},
		{"URL on shared message", Unsubscribe{URL: link}, []string{"a@example.test", "b@example.test"}, false},
		{"900-byte URL", Unsubscribe{URL: link + "&p=" + strings.Repeat("x", 900-len(link)-3)}, nil, false},

		{"OneClick only", Unsubscribe{OneClick: true}, nil, true},
		{"one-click without URL", Unsubscribe{Mailto: "unsubscribe@example.test", OneClick: true}, nil, true},
		{"one-click on shared message", Unsubscribe{URL: link, OneClick: true}, []string{"a@example.test", "b@example.test"}, true},
		{"http URL", Unsubscribe{URL: "http://example.test/u"}, nil, true},
		{"relative URL", Unsubscribe{URL: "/unsubscribe"}, nil, true},
		{"mailto URL in URL field", Unsubscribe{URL: "mailto:a@example.test"}, nil, true},
		{"URL userinfo", Unsubscribe{URL: "https://user:secret@example.test/u"}, nil, true},
		{"URL fragment", Unsubscribe{URL: link + "#frag"}, nil, true},
		{"URL CRLF injection", Unsubscribe{URL: link + "\r\nBcc: victim@example.test"}, nil, true},
		{"URL closing bracket", Unsubscribe{URL: link + ">, <https://evil.test"}, nil, true},
		{"URL space", Unsubscribe{URL: "https://example.test/a b"}, nil, true},
		{"URL non-ASCII", Unsubscribe{URL: "https://exämple.test/u"}, nil, true},
		{"901-byte URL", Unsubscribe{URL: link + "&p=" + strings.Repeat("x", 901-len(link)-3)}, nil, true},
		{"mailbox CRLF injection", Unsubscribe{Mailto: "u@example.test\r\nBcc: v@example.test"}, nil, true},
		{"mailbox display name", Unsubscribe{Mailto: "Name <u@example.test>"}, nil, true},
		{"mailbox query", Unsubscribe{Mailto: "u?subject=x@example.test"}, nil, true},
		{"mailbox quoted local part", Unsubscribe{Mailto: `"u"@example.test`}, nil, true},
		{"mailbox non-ASCII", Unsubscribe{Mailto: "ü@example.test"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMessage()
			if tt.to != nil {
				m.To = tt.to
			}
			m.Unsubscribe = tt.u
			err := m.Validate()
			if tt.wantErr != (err != nil) {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("error %v does not wrap sdk.ErrInvalidInput", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.test") {
				t.Errorf("diagnostic echoes input: %v", err)
			}
		})
	}
}
