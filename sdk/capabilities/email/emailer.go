package email

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
)

var _ Renderer = (*Emailer)(nil)

// Emailer is an optional template layer over a Sender. It renders content
// templates wrapped in branded layouts and delivers the result as a Message
// with both HTML and Text set. Logging is opt-in via WithLogger; when set,
// send failures are logged with recipient and subject context.
type Emailer struct {
	log         *slog.Logger
	sender      Sender
	defaultFrom string
	templates   *TemplateRegistry
}

// Option configures an Emailer during construction.
type Option func(*Emailer) error

// WithLogger enables structured logging of send failures.
func WithLogger(log *slog.Logger) Option {
	return func(e *Emailer) error {
		e.log = log
		return nil
	}
}

// WithContentTemplates registers content templates from an embed.FS under a
// namespace at the given layer, making them available as "namespace:name".
func WithContentTemplates(namespace string, fsys embed.FS, layer TemplateLayer) Option {
	return func(e *Emailer) error {
		return e.templates.RegisterTemplates(namespace, fsys, layer)
	}
}

// WithLayouts registers layout templates from an embed.FS at the given layer.
// Layout files should be named transactional.html, transactional.txt, and so on.
func WithLayouts(fsys embed.FS, dir string, layer TemplateLayer) Option {
	return func(e *Emailer) error {
		return e.templates.RegisterLayouts(fsys, dir, layer)
	}
}

// WithBranding sets the branding configuration for layout templates, reachable
// via {{.Brand.Name}}, {{.Brand.LogoURL}}, and so on.
func WithBranding(branding *Branding) Option {
	return func(e *Emailer) error {
		e.templates.SetBranding(branding)
		return nil
	}
}

// New creates an Emailer that renders templates and delivers them through
// sender. defaultFrom is used as the From address of every rendered message.
func New(sender Sender, defaultFrom string, opts ...Option) (*Emailer, error) {
	templates, err := newTemplateRegistry()
	if err != nil {
		return nil, fmt.Errorf("initialize template registry: %w", err)
	}

	e := &Emailer{
		sender:      sender,
		defaultFrom: defaultFrom,
		templates:   templates,
	}

	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, fmt.Errorf("apply option: %w", err)
		}
	}

	return e, nil
}

// RenderAndSend renders req.Template with the configured layout and sends the
// result as a Message with both HTML and Text set. The default From address is
// applied and the message is validated before delivery.
func (e *Emailer) RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error {
	cfg := ApplyOptions(opts...)

	html, text, err := e.templates.RenderWithLayout(req.Template, req.Data, cfg.Layout)
	if err != nil {
		return fmt.Errorf("render template %q: %w", req.Template, err)
	}

	msg := Message{
		From:    e.defaultFrom,
		Subject: req.Subject,
		HTML:    html,
		Text:    text,
	}
	if req.To != "" {
		msg.To = []string{req.To}
	}

	if err := msg.Validate(); err != nil {
		return err
	}

	if err := e.sender.Send(ctx, msg); err != nil {
		if e.log != nil {
			e.log.ErrorContext(ctx, "failed to send email",
				slog.String("error", err.Error()),
				slog.String("to", strings.Join(msg.To, ", ")),
				slog.String("subject", msg.Subject),
			)
		}
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}

// Render renders req.Template with the configured layout and returns both the
// HTML and plain-text versions without sending.
func (e *Emailer) Render(templateName string, data any, opts ...RenderOption) (html, text string, err error) {
	cfg := ApplyOptions(opts...)
	return e.templates.RenderWithLayout(templateName, data, cfg.Layout)
}
