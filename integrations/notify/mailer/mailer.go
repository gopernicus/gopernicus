// Package mailer bridges sdk/capabilities/email into the sdk/capabilities/notify
// delivery seam: an email-kind Notifier over any email.Sender.
//
// This is a COMPOSING integration (taxonomy amended at sdk-layering P5, 2026-07-10):
// it implements one sdk capability port (notify.Notifier) by composing other sdk
// packages, carries zero external dependencies, and never imports features/,
// examples/, or another integration. It cannot live in sdk itself — a
// capability→capability import (notify→email) is forbidden by the sdk layering
// law; cross-capability composition leaves sdk, and this module is its home.
package mailer

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Bridge is a Notifier for the email kind that adapts an email.Sender, so the
// email kind routes through the same delivery seam as every other kind WITHOUT
// replacing sdk/capabilities/email — the mail facility stays; this bridges to it.
type Bridge struct {
	sender email.Sender
	from   string
}

var _ notify.Notifier = (*Bridge)(nil)

// New returns an email-kind Notifier backed by sender. from is the sender
// address: email.Message.Validate requires From and neither notify.Message nor
// identity.Address carries one, so the bridge injects it.
func New(sender email.Sender, from string) *Bridge {
	return &Bridge{sender: sender, from: from}
}

// Kind reports identity.KindEmail.
func (b *Bridge) Kind() string { return identity.KindEmail }

// Notify maps the message onto an email.Message (to.Value → To[0],
// msg.Subject → Subject, msg.Body → Text, from → From) and sends it. Errors
// from Validate and Send propagate.
func (b *Bridge) Notify(ctx context.Context, to identity.Address, msg notify.Message) error {
	m := email.Message{
		From:    b.from,
		To:      []string{to.Value},
		Subject: msg.Subject,
		Text:    msg.Body,
	}
	if err := m.Validate(); err != nil {
		return err
	}
	return b.sender.Send(ctx, m)
}
