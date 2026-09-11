package email

import (
	"context"
	"fmt"
	"slices"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

type delivery struct {
	sender  Sender
	message Message
}

// NewDelivery prepares a full email for an explicitly selected notify.Send call.
// It performs no I/O and snapshots the recipient slice. Sender configuration is
// host-owned and must remain safe for concurrent use. Validate at Send time so
// dispatch reports invalid messages at their original delivery positions.
func NewDelivery(sender Sender, message Message) notify.Delivery {
	message.To = slices.Clone(message.To)
	d := &delivery{sender: sender, message: message}
	posture := notify.InspectTransport(sender)
	if posture.Declared {
		return &declaredDelivery{delivery: d, capabilities: posture.Capabilities}
	}
	return d
}

func (d *delivery) Send(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.sender == nil {
		return fmt.Errorf("email: sender is required: %w", sdk.ErrInvalidInput)
	}
	if err := d.message.Validate(); err != nil {
		return err
	}
	message := d.message
	message.To = slices.Clone(message.To)
	return d.sender.Send(ctx, message)
}

// Only a delivery over a declared transport implements CapabilityReporter.
// An unknown sender therefore stays unknown after adapting it.
type declaredDelivery struct {
	*delivery
	capabilities notify.Capabilities
}

func (d *declaredDelivery) Capabilities() notify.Capabilities { return d.capabilities }
