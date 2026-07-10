package notify

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Console is a development Notifier that logs deliveries instead of sending them
// — the dev default for any kind, mirroring email.Console. It never drops
// silently: every Notify is logged.
type Console struct {
	kind string
	log  *slog.Logger
}

var _ Notifier = (*Console)(nil)

// NewConsole returns a console Notifier for the given kind. A nil logger falls
// back to slog.Default().
func NewConsole(kind string, log *slog.Logger) *Console {
	if log == nil {
		log = slog.Default()
	}
	return &Console{kind: kind, log: log}
}

// Kind reports the address kind this Notifier declares.
func (c *Console) Kind() string { return c.kind }

// Notify logs the delivery at INFO.
func (c *Console) Notify(ctx context.Context, to identity.Address, msg Message) error {
	c.log.InfoContext(ctx, "notify (console notifier)",
		"kind", c.kind,
		"to", to.Value,
		"subject", msg.Subject,
		"body", msg.Body,
	)
	return nil
}
