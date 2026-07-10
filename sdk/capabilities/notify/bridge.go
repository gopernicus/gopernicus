package notify

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// MailerBridge is a Notifier for the email kind that adapts an email.Sender, so
// the email kind routes through the same delivery seam as every other kind
// WITHOUT replacing sdk/capabilities/email — the mail facility stays; this bridges to it.
type MailerBridge struct {
	sender email.Sender
	from   string
}

var _ Notifier = (*MailerBridge)(nil)

// NewMailerBridge returns an email-kind Notifier backed by sender. from is the
// sender address: email.Message.Validate requires From and neither notify.Message
// nor identity.Address carries one, so the bridge injects it.
func NewMailerBridge(sender email.Sender, from string) *MailerBridge {
	return &MailerBridge{sender: sender, from: from}
}

// Kind reports identity.KindEmail.
func (b *MailerBridge) Kind() string { return identity.KindEmail }

// Notify maps the message onto an email.Message (to.Value → To[0],
// msg.Subject → Subject, msg.Body → Text, from → From) and sends it. Errors from
// Validate and Send propagate.
func (b *MailerBridge) Notify(ctx context.Context, to identity.Address, msg Message) error {
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
