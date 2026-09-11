// Package sendgrid implements notify/email.Sender using SendGrid's typed mail
// request builder and an adapter-owned HTTP client.
package sendgrid

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go/helpers/mail"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

const sendPath = "/v3/mail/send"

// Config is host-owned provider configuration.
type Config struct {
	APIKey string
	// FromName is the display name paired with the message's bare From mailbox.
	FromName string
	// Host is an HTTP(S) origin. Empty selects https://api.sendgrid.com.
	// Plain HTTP is supported for local testing and is development-only.
	Host string
	// HTTPClient supplies a transport, timeout and other HTTP settings. New copies
	// the client and disables redirects so bearer credentials and message bodies
	// never follow a redirect. The transport remains shared and host-owned.
	// Nil uses the standard transport with a 30-second timeout.
	HTTPClient *http.Client
}

// Sender holds immutable configuration; every send builds its own request.
type Sender struct {
	client   *http.Client
	apiKey   string
	fromName string
	host     string
}

var _ email.Sender = (*Sender)(nil)

func (s *Sender) Capabilities() notify.Capabilities {
	if strings.HasPrefix(s.host, "https://") {
		return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
	}
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityNone, DevelopmentOnly: true}
}

// New validates configuration and prepares a sender without I/O. Credentials
// are checked by the provider when sending; construction only validates inputs.
func New(cfg Config) (*Sender, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("sendgrid: API key is required: %w", sdk.ErrInvalidInput)
	}
	if strings.ContainsAny(cfg.APIKey, "\r\n") || strings.ContainsAny(cfg.FromName, "\r\n") {
		return nil, fmt.Errorf("sendgrid: configuration contains line breaks: %w", sdk.ErrInvalidInput)
	}
	host := cfg.Host
	if host == "" {
		host = "https://api.sendgrid.com"
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("sendgrid: Host must be an HTTP(S) origin: %w", sdk.ErrInvalidInput)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Sender{client: &client, apiKey: cfg.APIKey, fromName: cfg.FromName, host: u.Scheme + "://" + u.Host}, nil
}

// Send performs one request. It preserves both email representations and stable
// SDK error causes, without returning provider response bodies in diagnostics.
func (s *Sender) Send(ctx context.Context, msg email.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	m := mail.NewV3Mail()
	m.SetFrom(mail.NewEmail(s.fromName, msg.From))
	m.Subject = msg.Subject
	m.AddContent(mail.NewContent("text/plain", msg.Text))
	if msg.HTML != "" {
		m.AddContent(mail.NewContent("text/html", msg.HTML))
	}
	p := mail.NewPersonalization()
	for _, address := range msg.To {
		p.AddTos(mail.NewEmail("", address))
	}
	m.AddPersonalizations(p)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+sendPath, bytes.NewReader(mail.GetRequestBody(m)))
	if err != nil {
		return fmt.Errorf("sendgrid: request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("sendgrid: send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &ResponseError{StatusCode: response.StatusCode}
	}
	return nil
}

// ResponseError reports a rejected provider response without its potentially
// sensitive body. Authentication errors concern provider credentials, not the
// application's end user. Retry policy belongs to the host.
type ResponseError struct{ StatusCode int }

func (e *ResponseError) Error() string {
	return fmt.Sprintf("sendgrid: status %d (%s)", e.StatusCode, http.StatusText(e.StatusCode))
}

func (e *ResponseError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusBadRequest:
		return sdk.ErrInvalidInput
	case http.StatusUnauthorized:
		return sdk.ErrUnauthorized
	case http.StatusForbidden:
		return sdk.ErrForbidden
	case http.StatusNotFound:
		return sdk.ErrNotFound
	default:
		return nil
	}
}
