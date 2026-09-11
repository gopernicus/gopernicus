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
