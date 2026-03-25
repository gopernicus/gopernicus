package stdoutemailer

import (
	"context"
	"log/slog"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
)

func TestSend_Success(t *testing.T) {
	client := New(slog.Default())

	err := client.Send(context.Background(), emailer.Email{
		To:      "recipient@example.com",
		From:    "sender@example.com",
		Subject: "Test Subject",
		HTML:    "<h1>Hello World</h1><p>This is a test email.</p>",
		Text:    "Hello World\nThis is a test email.",
	})

	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
	}
}

func TestSend_LongHTML(t *testing.T) {
	client := New(slog.Default())

	longHTML := "<html><body>"
	for i := 0; i < 50; i++ {
		longHTML += "<p>This is a test paragraph with some content that will make the email body very long.</p>"
	}
	longHTML += "</body></html>"

	err := client.Send(context.Background(), emailer.Email{
		To:      "recipient@example.com",
		From:    "sender@example.com",
		Subject: "Test Subject",
		HTML:    longHTML,
	})

	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
	}
}

func TestSend_NoTextBody(t *testing.T) {
	client := New(slog.Default())

	err := client.Send(context.Background(), emailer.Email{
		To:      "recipient@example.com",
		From:    "sender@example.com",
		Subject: "Test Subject",
		HTML:    "<h1>Hello World</h1>",
	})

	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
	}
}

func TestSend_EmptyBody(t *testing.T) {
	client := New(slog.Default())

	err := client.Send(context.Background(), emailer.Email{
		To:      "recipient@example.com",
		From:    "sender@example.com",
		Subject: "Test Subject",
	})

	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
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
