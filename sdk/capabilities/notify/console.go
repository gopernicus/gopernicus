package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// Console is a development body sender. It logs the destination and plain text;
// a host may use it for a non-email channel without a provider account.
type Console struct{ log *slog.Logger }

// NewConsole creates a development sender. A nil logger uses slog.Default.
func NewConsole(log *slog.Logger) *Console {
	if log == nil {
		log = slog.Default()
	}
	return &Console{log: log}
}

func (c *Console) Capabilities() Capabilities {
	return Capabilities{TransportSecurity: TransportSecurityNone, DevelopmentOnly: true}
}

// Send logs one plain-text body. Use DeliveryFunc to select it in notify.Send.
func (c *Console) Send(ctx context.Context, destination, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" || strings.TrimSpace(body) == "" {
		return fmt.Errorf("notify: destination and body are required: %w", sdk.ErrInvalidInput)
	}
	c.log.InfoContext(ctx, "notification (console sender)", "to", destination, "body", body)
	return nil
}
