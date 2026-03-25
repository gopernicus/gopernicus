package outbox

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// OutboxMarker is the optional interface for events that should be routed to
// the outbox. Events embedding BaseEvent and calling .ToOutbox() satisfy it.
type OutboxMarker interface {
	IsOutbox() bool
}

// Bus decorates an existing events.Bus, intercepting outbox-marked events.
//
// For events where IsOutbox() returns true:
//   - The event is written to the outbox table via OutboxWriter.
//   - The event is NOT sent to the underlying bus (worker handles delivery).
//
// For events where IsOutbox() returns false (or the event does not implement
// OutboxMarker):
//   - The event is passed to the underlying bus unchanged.
type Bus struct {
	underlying events.Bus
	writer     OutboxWriter
	opts       Options
	log        *slog.Logger
}

// NewBus creates a Bus decorating the given inner bus.
func NewBus(underlying events.Bus, writer OutboxWriter, opts Options, log *slog.Logger) *Bus {
	return &Bus{
		underlying: underlying,
		writer:     writer,
		opts:       opts,
		log:        log,
	}
}

// Emit routes the event based on IsOutbox(). Outbox-marked events are written
// to the outbox table; all others are delegated to the underlying bus.
func (b *Bus) Emit(ctx context.Context, event events.Event, opts ...events.EmitOption) error {
	if marker, ok := event.(OutboxMarker); ok && marker.IsOutbox() {
		b.log.DebugContext(ctx, "outbox: writing event to outbox",
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
		)

		if err := WriteEventToOutbox(ctx, b.writer, event, b.opts); err != nil {
			b.log.ErrorContext(ctx, "outbox: failed to write event",
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"err", err,
			)
			return err
		}

		return nil
	}

	return b.underlying.Emit(ctx, event, opts...)
}

// Subscribe delegates to the underlying bus.
// Note: subscribers here receive only non-outbox events. Outbox events are
// processed by the worker, which has its own handler registration.
func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	return b.underlying.Subscribe(topic, handler)
}

// Close closes the underlying bus.
func (b *Bus) Close(ctx context.Context) error {
	return b.underlying.Close(ctx)
}

// Compile-time interface check.
var _ events.Bus = (*Bus)(nil)
