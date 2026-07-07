package email

// SMTP's Send path exercises net/smtp.SendMail against a real network
// connection; that path is intentionally left untested here — no live SMTP
// server or fake SMTP server dependency is introduced (see phase 2 plan,
// W2). Only the constructor and config-driven auth wiring are covered.

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"testing"
)

func TestNewSMTP_AddrAndAuth(t *testing.T) {
	tests := []struct {
		name     string
		cfg      SMTPConfig
		wantAddr string
		wantAuth bool
	}{
		{
			name:     "no username means no auth",
			cfg:      SMTPConfig{Host: "smtp.example.com", Port: "25"},
			wantAddr: "smtp.example.com:25",
			wantAuth: false,
		},
		{
			name:     "username set enables PLAIN auth",
			cfg:      SMTPConfig{Host: "smtp.example.com", Port: "587", Username: "user", Password: "pass"},
			wantAddr: "smtp.example.com:587",
			wantAuth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSMTP(tt.cfg)
			if s.addr != tt.wantAddr {
				t.Errorf("addr = %q, want %q", s.addr, tt.wantAddr)
			}
			if (s.auth != nil) != tt.wantAuth {
				t.Errorf("auth set = %v, want %v", s.auth != nil, tt.wantAuth)
			}
		})
	}
}

func TestSMTP_Send_ValidatesMessageBeforeDialing(t *testing.T) {
	// An invalid message must fail Validate() before SMTP.Send ever attempts
	// to dial out — this keeps the test network-free while still covering
	// the guard clause.
	s := NewSMTP(SMTPConfig{Host: "smtp.invalid.example.", Port: "25"})
	err := s.Send(context.Background(), Message{})
	if err == nil {
		t.Fatal("expected validation error for empty message, got nil")
	}
}

// TestBuildMessage_PlainByteIdentity pins the exact wire bytes of a text-only
// message so the multipart extension cannot silently regress the plain path.
func TestBuildMessage_PlainByteIdentity(t *testing.T) {
	msg := Message{
		From:    "sender@example.com",
		To:      []string{"a@example.com", "b@example.com"},
		Subject: "Hello",
		Text:    "Line one\nLine two",
	}

	got, err := buildMessage(msg)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	want := "From: sender@example.com\r\n" +
		"To: a@example.com, b@example.com\r\n" +
		"Subject: Hello\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Line one\nLine two"

	if string(got) != want {
		t.Errorf("plain message mismatch:\n got %q\nwant %q", got, want)
	}
}

// TestBuildMessage_MultipartAlternative asserts the structure of a message with
// an HTML alternative by parsing it with the stdlib mime readers — the boundary
// is random, so the message is validated by structure, not golden string.
func TestBuildMessage_MultipartAlternative(t *testing.T) {
	msg := Message{
		From:    "sender@example.com",
		To:      []string{"rcpt@example.com"},
		Subject: "Hi",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	}

	raw, err := buildMessage(msg)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got := m.Header.Get("MIME-Version"); got != "1.0" {
		t.Errorf("MIME-Version = %q, want %q", got, "1.0")
	}

	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse top-level Content-Type: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Errorf("top-level media type = %q, want %q", mediaType, "multipart/alternative")
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("top-level Content-Type is missing a boundary parameter")
	}

	mr := multipart.NewReader(m.Body, boundary)

	part1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart (text): %v", err)
	}
	assertPart(t, part1, "text/plain", "utf-8", "plain body")

	part2, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart (html): %v", err)
	}
	assertPart(t, part2, "text/html", "utf-8", "<p>html body</p>")

	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("expected exactly two parts, got extra part (err = %v)", err)
	}
}

func assertPart(t *testing.T, part *multipart.Part, wantType, wantCharset, wantBody string) {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse part Content-Type: %v", err)
	}
	if mediaType != wantType {
		t.Errorf("part media type = %q, want %q", mediaType, wantType)
	}
	if params["charset"] != wantCharset {
		t.Errorf("part charset = %q, want %q", params["charset"], wantCharset)
	}

	body, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read part body: %v", err)
	}
	if string(body) != wantBody {
		t.Errorf("part body = %q, want %q", body, wantBody)
	}
}
