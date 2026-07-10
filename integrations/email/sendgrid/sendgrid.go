// Package sendgrid implements the sdk/email.Sender port over Twilio SendGrid's
// v3 Mail Send API, wrapping exactly one third-party library —
// github.com/sendgrid/sendgrid-go (with its transport dependency
// github.com/sendgrid/rest). It is an integration rather than an sdk default
// because it speaks one vendor's live API contract, which churns on SendGrid's
// schedule, not sdk's; sdk defaults (Console, SMTP) must stay vendor-neutral.
// It imports sdk/email for the Sender vocabulary and sdk/errs for stable error
// kinds — no feature and no other integration.
package sendgrid

import (
	"context"
	"fmt"
	"net/http"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/email"
)

// sendPath is SendGrid's v3 Mail Send endpoint path, appended to the host to
// form the request BaseURL.
const sendPath = "/v3/mail/send"

// Compile-time assertion that Sender satisfies the sdk email port.
var _ email.Sender = (*Sender)(nil)

// Config holds SendGrid connection settings.
type Config struct {
	// APIKey authenticates requests; sent as a Bearer token by sendgrid-go.
	APIKey string
	// FromName is the optional display name paired with each message's From
	// address (the sdk email.Message carries only a bare address).
	FromName string
	// Host overrides the scheme+host the request is sent to (e.g.
	// "https://api.eu.sendgrid.com"). Empty uses SendGrid's default,
	// https://api.sendgrid.com. Tests point this at an httptest server.
	Host string
}

// Sender delivers email.Message values through SendGrid's Mail Send API.
type Sender struct {
	client   *sendgrid.Client
	fromName string
}

// New constructs a Sender. It builds a POST client for the Mail Send endpoint
// against cfg.Host (or SendGrid's default host when empty); no network I/O
// happens here.
func New(cfg Config) *Sender {
	request := sendgrid.GetRequest(cfg.APIKey, sendPath, cfg.Host)
	request.Method = "POST"
	return &Sender{
		client:   &sendgrid.Client{Request: request},
		fromName: cfg.FromName,
	}
}

// Send validates the message and delivers it. A non-2xx response is mapped to a
// stable sdk/errs kind where one fits (400/401/403/404); other statuses return
// a plain error carrying the status code and response body.
func (s *Sender) Send(ctx context.Context, msg email.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	m := mail.NewV3Mail()
	m.SetFrom(mail.NewEmail(s.fromName, msg.From))
	m.Subject = msg.Subject

	if msg.Text != "" {
		m.AddContent(mail.NewContent("text/plain", msg.Text))
	}
	if msg.HTML != "" {
		m.AddContent(mail.NewContent("text/html", msg.HTML))
	}

	p := mail.NewPersonalization()
	for _, addr := range msg.To {
		p.AddTos(mail.NewEmail("", addr))
	}
	m.AddPersonalizations(p)

	resp, err := s.client.SendWithContext(ctx, m)
	if err != nil {
		return fmt.Errorf("sendgrid: send: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, resp.Body)
	}
	return nil
}

// statusError maps a non-2xx SendGrid response to an error. Auth- and
// input-relevant codes wrap a sdk/errs sentinel so callers can branch on kind;
// every case keeps the status code and body for diagnostics.
func statusError(status int, body string) error {
	switch status {
	case http.StatusBadRequest:
		return fmt.Errorf("sendgrid: status %d: %s: %w", status, body, sdk.ErrInvalidInput)
	case http.StatusUnauthorized:
		return fmt.Errorf("sendgrid: status %d: %s: %w", status, body, sdk.ErrUnauthorized)
	case http.StatusForbidden:
		return fmt.Errorf("sendgrid: status %d: %s: %w", status, body, sdk.ErrForbidden)
	case http.StatusNotFound:
		return fmt.Errorf("sendgrid: status %d: %s: %w", status, body, sdk.ErrNotFound)
	default:
		return fmt.Errorf("sendgrid: status %d: %s", status, body)
	}
}
