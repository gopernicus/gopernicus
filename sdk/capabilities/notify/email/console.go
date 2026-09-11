package email

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// Console is a development Sender that logs messages instead of delivering them
// — useful locally and in tests where no SMTP server exists.
type Console struct {
	log *slog.Logger
}

var (
	_ Sender                    = (*Console)(nil)
	_ notify.CapabilityReporter = (*Console)(nil)
)

// notify.Capabilities marks the console sender development-only: it logs message bodies
// (verification codes, magic links) rather than delivering them, so a production
// host must reject it.
func (s *Console) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityNone, DevelopmentOnly: true}
}

// NewConsole returns a console Sender. A nil logger uses slog.Default().
func NewConsole(log *slog.Logger) *Console {
	if log == nil {
		log = slog.Default()
	}
	return &Console{log: log}
}

// Send logs the message at INFO.
func (s *Console) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "email (console sender)",
		"from", msg.From,
		"to", msg.To,
		"subject", msg.Subject,
		"text", msg.Text,
	)
	return nil
}
