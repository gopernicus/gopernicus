// Package emailer provides email rendering and sending infrastructure.
// It includes the Renderer interface, template management, and pluggable
// email clients (SendGrid, console, etc.).
//
// Emailer also implements notify.Notifier for simple notification delivery.
package emailer

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
)

// Compile-time interface check.
var _ Renderer = (*Emailer)(nil)

// Email represents an email message to be sent.
type Email struct {
	To      string // Recipient email address
	From    string // Sender email address (optional, uses default if empty)
	Subject string // Email subject line
	HTML    string // HTML email body
	Text    string // Plain text email body (fallback)
}

// Client defines the interface for email sending implementations.
// Different clients (SendGrid, SES, Console) implement this interface.
type Client interface {
	Send(ctx context.Context, email Email) error
}

// Emailer provides email sending functionality with pluggable clients.
// It wraps a Client implementation and adds template rendering and defaults.
// Emailer implements both Renderer (for templated emails) and notify.Notifier (for simple alerts).
//
// Logging is opt-in via WithLogger. When configured, the emailer logs send failures
// with recipient and subject context.
type Emailer struct {
	log         *slog.Logger
	client      Client
	defaultFrom string
	templates   *TemplateRegistry
}

// Option configures an Emailer during construction.
type Option func(*Emailer) error

// WithLogger enables structured logging for email operations.
// When set, the emailer logs send failures with recipient and subject context.
func WithLogger(log *slog.Logger) Option {
	return func(e *Emailer) error {
		e.log = log
		return nil
	}
}

// WithContentTemplates registers content templates from an embed.FS under a namespace with explicit layer.
// For example: WithContentTemplates("authentication", authTemplates, LayerCore)
// makes templates available as "authentication:templatename".
func WithContentTemplates(namespace string, fsys embed.FS, layer TemplateLayer) Option {
	return func(e *Emailer) error {
		if e.templates == nil {
			return nil
		}
		return e.templates.RegisterTemplates(namespace, fsys, layer)
	}
}

// WithLayouts registers layout templates from an embed.FS with explicit layer.
// Layout templates should be named: transactional.html, transactional.txt, etc.
func WithLayouts(fsys embed.FS, dir string, layer TemplateLayer) Option {
	return func(e *Emailer) error {
		if e.templates == nil {
			return nil
		}
		return e.templates.RegisterLayouts(fsys, dir, layer)
	}
}

// WithBranding sets the branding configuration for email templates.
// Templates can access branding via {{.Brand.Name}}, {{.Brand.LogoURL}}, etc.
func WithBranding(branding *Branding) Option {
	return func(e *Emailer) error {
		if e.templates == nil {
			return nil
		}
		e.templates.SetBranding(branding)
		return nil
	}
}

// New creates a new Emailer with the specified client and default from address.
//
// The client parameter determines how emails are actually sent (SendGrid, console logger, etc.).
// The defaultFrom parameter is used when an email's From field is empty.
// Use WithLogger to enable structured logging of send failures.
func New(client Client, defaultFrom string, opts ...Option) (*Emailer, error) {
	templateRegistry, err := newTemplateRegistry()
	if err != nil {
		return nil, fmt.Errorf("initialize template registry: %w", err)
	}

	e := &Emailer{
		client:      client,
		defaultFrom: defaultFrom,
		templates:   templateRegistry,
	}

	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, fmt.Errorf("apply option: %w", err)
		}
	}

	return e, nil
}

// =============================================================================
// Renderer Interface Implementation
// =============================================================================

// RenderAndSend implements Renderer.
// It renders a content template with the specified layout and sends it.
func (e *Emailer) RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error {
	cfg := ApplyOptions(opts...)

	html, text, err := e.templates.RenderWithLayout(req.Template, req.Data, cfg.Layout)
	if err != nil {
		return fmt.Errorf("render template %q: %w", req.Template, err)
	}

	return e.Send(ctx, Email{
		To:      req.To,
		Subject: req.Subject,
		HTML:    html,
		Text:    text,
	})
}

// Render implements Renderer.
// It renders a content template with the specified layout and returns HTML and text versions.
func (e *Emailer) Render(templateName string, data any, opts ...RenderOption) (html, text string, err error) {
	cfg := ApplyOptions(opts...)
	return e.templates.RenderWithLayout(templateName, data, cfg.Layout)
}

// =============================================================================
// notify.Notifier Implementation
// =============================================================================

// =============================================================================
// Direct Sending
// =============================================================================

// Send sends an email using the configured client.
// If the email's From field is empty, the defaultFrom address is used.
func (e *Emailer) Send(ctx context.Context, email Email) error {
	if email.From == "" {
		email.From = e.defaultFrom
	}

	if email.To == "" {
		return fmt.Errorf("email recipient (To) is required")
	}
	if email.Subject == "" {
		return fmt.Errorf("email subject is required")
	}
	if email.HTML == "" && email.Text == "" {
		return fmt.Errorf("email must have either HTML or Text body")
	}

	if err := e.client.Send(ctx, email); err != nil {
		if e.log != nil {
			e.log.ErrorContext(ctx, "failed to send email",
				slog.String("error", err.Error()),
				slog.String("to", email.To),
				slog.String("subject", email.Subject),
			)
		}
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}
