package email

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Emailer optionally renders and sends mail through a host-selected Sender.
// Logging, retry and notification fan-out remain with the caller.
type Emailer struct {
	renderer    *Renderer
	sender      Sender
	defaultFrom string
}

// New prepares templated sending. Render-only callers use NewRenderer.
func New(sender Sender, defaultFrom string, opts ...Option) (*Emailer, error) {
	if sender == nil {
		return nil, fmt.Errorf("email: sender is required: %w", sdk.ErrInvalidInput)
	}
	if err := validateMailbox(defaultFrom); err != nil {
		return nil, fmt.Errorf("email: invalid default From mailbox: %w", sdk.ErrInvalidInput)
	}
	renderer, err := NewRenderer(opts...)
	if err != nil {
		return nil, err
	}
	return &Emailer{renderer: renderer, sender: sender, defaultFrom: defaultFrom}, nil
}

// SendRequest selects one recipient and the rendered email content.
type SendRequest struct {
	To       string
	Subject  string
	Template string
	Data     any
	Layout   LayoutType
}

// RenderAndSend renders both alternatives and performs one transport attempt.
func (e *Emailer) RenderAndSend(ctx context.Context, req SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	html, text, err := e.renderer.Render(RenderRequest{Template: req.Template, Subject: req.Subject, Data: req.Data, Layout: req.Layout})
	if err != nil {
		return err
	}
	message := Message{From: e.defaultFrom, To: []string{req.To}, Subject: req.Subject, Text: text, HTML: html}
	if err := NewDelivery(e.sender, message).Send(ctx); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// Render exposes the same render-only operation as Renderer.
func (e *Emailer) Render(req RenderRequest) (html, text string, err error) {
	return e.renderer.Render(req)
}
