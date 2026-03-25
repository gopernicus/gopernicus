// Package sendgridemailer provides a SendGrid email client adapter.
package sendgridemailer

import (
	"context"
	"fmt"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
)

var _ emailer.Client = (*Client)(nil)

// Client implements emailer.Client using SendGrid's API.
type Client struct {
	apiKey    string
	fromEmail string
	fromName  string
	sgClient  *sendgrid.Client
}

// New creates a new SendGrid email client.
func New(apiKey, fromEmail, fromName string) *Client {
	return &Client{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		fromName:  fromName,
		sgClient:  sendgrid.NewSendClient(apiKey),
	}
}

// Send sends an email via SendGrid.
func (c *Client) Send(ctx context.Context, email emailer.Email) error {
	fromEmail := email.From
	fromName := c.fromName
	if fromEmail == "" {
		fromEmail = c.fromEmail
	}
	from := mail.NewEmail(fromName, fromEmail)

	to := mail.NewEmail("", email.To)

	m := mail.NewV3Mail()
	m.SetFrom(from)
	m.Subject = email.Subject

	if email.Text != "" {
		m.AddContent(mail.NewContent("text/plain", email.Text))
	}
	if email.HTML != "" {
		m.AddContent(mail.NewContent("text/html", email.HTML))
	}

	p := mail.NewPersonalization()
	p.AddTos(to)
	m.AddPersonalizations(p)

	response, err := c.sgClient.SendWithContext(ctx, m)
	if err != nil {
		return fmt.Errorf("failed to send email via SendGrid: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("SendGrid returned status %d: %s", response.StatusCode, response.Body)
	}

	return nil
}
