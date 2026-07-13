package email

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
)

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
}

// SMTP is the default Sender over the standard-library net/smtp — no external
// dependency. (SaaS mailers like Resend/SES are separate integration modules.)
type SMTP struct {
	addr string
	auth smtp.Auth
}

var (
	_ Sender             = (*SMTP)(nil)
	_ CapabilityReporter = (*SMTP)(nil)
)

// Capabilities declares SMTP a production-capable transport: it is not
// development-only, and net/smtp negotiates STARTTLS when the server advertises
// it. Declaring metadata lets a production host accept it (an undeclared Sender
// is rejected fail-closed).
func (s *SMTP) Capabilities() Capabilities {
	return Capabilities{TransportSecurity: TransportSecurityStartTLS, DevelopmentOnly: false}
}

// NewSMTP returns an SMTP Sender. When Username is set, PLAIN auth is used.
func NewSMTP(cfg SMTPConfig) *SMTP {
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return &SMTP{addr: net.JoinHostPort(cfg.Host, cfg.Port), auth: auth}
}

// Send delivers the message. The context is honored for cancellation only via
// the underlying dial timeout of net/smtp (best effort).
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	body, err := buildMessage(msg)
	if err != nil {
		return fmt.Errorf("smtp build message: %w", err)
	}
	if err := smtp.SendMail(s.addr, s.auth, msg.From, msg.To, body); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// buildMessage renders an RFC 5322 message body. With no HTML alternative it is
// a single text/plain message (byte-identical to the pre-template behavior);
// with an HTML alternative it is a multipart/alternative message carrying the
// plain-text part first and the HTML part second, so clients that cannot render
// HTML fall back to the text alternative.
func buildMessage(msg Message) ([]byte, error) {
	if msg.HTML == "" {
		return buildPlainMessage(msg), nil
	}
	return buildMultipartMessage(msg)
}

// buildPlainMessage renders a minimal RFC 5322 plain-text message.
func buildPlainMessage(msg Message) []byte {
	var b bytes.Buffer
	writeAddressHeaders(&b, msg)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Text)
	return b.Bytes()
}

// buildMultipartMessage renders a multipart/alternative message with the
// text/plain part first and the text/html part second.
func buildMultipartMessage(msg Message) ([]byte, error) {
	var parts bytes.Buffer
	mw := multipart.NewWriter(&parts)

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, fmt.Errorf("create text part: %w", err)
	}
	if _, err := tw.Write([]byte(msg.Text)); err != nil {
		return nil, fmt.Errorf("write text part: %w", err)
	}

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
	hw, err := mw.CreatePart(htmlHeader)
	if err != nil {
		return nil, fmt.Errorf("create html part: %w", err)
	}
	if _, err := hw.Write([]byte(msg.HTML)); err != nil {
		return nil, fmt.Errorf("write html part: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	var b bytes.Buffer
	writeAddressHeaders(&b, msg)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n", mw.Boundary())
	b.WriteString("\r\n")
	b.Write(parts.Bytes())
	return b.Bytes(), nil
}

// writeAddressHeaders writes the From/To/Subject/MIME-Version headers shared by
// both the plain and multipart renderings.
func writeAddressHeaders(b *bytes.Buffer, msg Message) {
	fmt.Fprintf(b, "From: %s\r\n", msg.From)
	fmt.Fprintf(b, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
}
