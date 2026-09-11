package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// SMTPConfig holds host-owned connection settings. STARTTLS is opportunistic:
// this supports explicit private-relay configurations without TLS. PLAIN auth
// follows net/smtp's protection against sending credentials to remote plaintext
// servers. Use a different Sender when mandatory or implicit TLS is required.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	// Timeout bounds a complete attempt, including the greeting and DATA reply.
	// Zero uses 30 seconds; an earlier caller deadline wins.
	Timeout time.Duration
}

// SMTP is a stdlib email transport. Each Send owns its connection.
type SMTP struct {
	addr    string
	host    string
	auth    smtp.Auth
	timeout time.Duration
}

var _ Sender = (*SMTP)(nil)

func (s *SMTP) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityStartTLS}
}

// NewSMTP prepares a sender without opening a connection.
func NewSMTP(cfg SMTPConfig) *SMTP {
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &SMTP{addr: net.JoinHostPort(cfg.Host, cfg.Port), host: cfg.Host, auth: auth, timeout: timeout}
}

// Send validates and delivers one email. Cancellation closes the active
// connection, including while awaiting a greeting, STARTTLS or a DATA reply.
// A successful DATA acknowledgement remains success even if QUIT fails.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	if s.timeout < 0 {
		return fmt.Errorf("email: SMTP timeout must not be negative: %w", sdk.ErrInvalidInput)
	}
	body, err := buildMessage(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("smtp connect: %w", errors.Join(err, ctx.Err()))
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := s.send(conn, msg, body); err != nil {
		return fmt.Errorf("smtp send: %w", errors.Join(err, ctx.Err()))
	}
	return nil
}

func (s *SMTP) send(conn net.Conn, msg Message, body []byte) error {
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
			return err
		}
	}
	if s.auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not support AUTH")
		}
		if err := client.Auth(s.auth); err != nil {
			return err
		}
	}
	if ok, _ := client.Extension("SMTPUTF8"); !ok {
		for _, address := range append([]string{msg.From}, msg.To...) {
			for _, r := range address {
				if r > 127 {
					return fmt.Errorf("SMTP server does not support international mailboxes: %w", sdk.ErrInvalidInput)
				}
			}
		}
	}
	if err := client.Mail(msg.From); err != nil {
		return err
	}
	for _, recipient := range msg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		// Closing DATA after a write error could submit a partial message.
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	// DATA is acknowledged. A failed or canceled session cleanup does not mean
	// the host should retry a message the server already accepted.
	_ = client.Quit()
	return nil
}

// buildMessage uses quoted-printable bodies and folded encoded subjects so UTF-8
// content works with ordinary SMTP servers and long input stays within MIME lines.
func buildMessage(msg Message) ([]byte, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	writeAddressHeaders(&b, msg)
	if msg.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintable(&b, msg.Text); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	var parts bytes.Buffer
	mw := multipart.NewWriter(&parts)
	for _, part := range []struct{ kind, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.kind+"; charset=utf-8")
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		writer, err := mw.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if err := writeQuotedPrintable(writer, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mw.Boundary())
	b.Write(parts.Bytes())
	return b.Bytes(), nil
}

func writeQuotedPrintable(w io.Writer, body string) error {
	writer := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(writer, body); err != nil {
		return err
	}
	return writer.Close()
}

func writeAddressHeaders(b *bytes.Buffer, msg Message) {
	fmt.Fprintf(b, "From: %s\r\n", msg.From)
	fmt.Fprintf(b, "To: %s\r\n", strings.Join(msg.To, ",\r\n "))
	b.WriteString("Subject: ")
	// A 42-byte chunk yields an encoded word no longer than 68 bytes. Break at
	// UTF-8 rune boundaries; whitespace between encoded words is not content.
	subject := msg.Subject
	for len(subject) > 0 {
		n := min(42, len(subject))
		for n < len(subject) && !utf8.RuneStart(subject[n]) {
			n--
		}
		fmt.Fprintf(b, "=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject[:n])))
		subject = subject[n:]
		if subject != "" {
			b.WriteString("\r\n ")
		}
	}
	b.WriteString("\r\n")
	fmt.Fprintf(b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
}
