package email

import (
	"context"
	"io"
	"log/slog"
)

// Console is a development Sender that logs messages instead of delivering them
// — useful locally and in tests where no SMTP server exists.
type Console struct {
	log *slog.Logger
}

var _ Sender = (*Console)(nil)

// NewConsole returns a console Sender. A nil logger discards output.
func NewConsole(log *slog.Logger) *Console {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Console{log: log}
}

// Send logs the message at INFO.
func (s *Console) Send(ctx context.Context, msg Message) error {
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
