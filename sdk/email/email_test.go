package email

import (
	"errors"
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
