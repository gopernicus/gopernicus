// Package email is the facility port for sending mail. Stdlib-only senders
// (SMTP via net/smtp, Console for dev) ship right here as defaults and
// implement Sender; a third-party SaaS sender would live in its own
// integrations/email/<tech> module. sdk/email is stdlib-only and knows
// nothing about any backend beyond the Sender interface.
package email

import (
	"context"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Message is an outbound email. Text is required; HTML is optional.
type Message struct {
	From    string
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Validate checks the message has the minimum required fields. Failures wrap
// errs.ErrInvalidInput.
func (m Message) Validate() error {
	if strings.TrimSpace(m.From) == "" {
		return fmt.Errorf("from is required: %w", errs.ErrInvalidInput)
	}
	if len(m.To) == 0 {
		return fmt.Errorf("at least one recipient is required: %w", errs.ErrInvalidInput)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return fmt.Errorf("subject is required: %w", errs.ErrInvalidInput)
	}
	if strings.TrimSpace(m.Text) == "" {
		return fmt.Errorf("body is required: %w", errs.ErrInvalidInput)
	}
	return nil
}

// Sender delivers a Message. Implemented by remotes/email backends.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}
