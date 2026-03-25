// Package outbox provides a bus decorator that implements the transactional outbox
// pattern for durable event delivery.
//
// Events marked with .ToOutbox() are written to the event_outbox table for
// processing by a worker. All other events are passed through to the underlying
// bus unchanged.
//
//	Server emits event → Bus checks IsOutbox()
//	                           |                    |
//	                   [IsOutbox() true]    [IsOutbox() false]
//	                           |                    |
//	                   INSERT event_outbox    underlying bus only
//	                           |
//	                   Worker stages, processes, marks completed
//
// Outbox events serve a different purpose than bus events:
//   - Bus events: real-time, best-effort (or at-least-once via Redis Streams)
//   - Outbox events: critical business events requiring durable delivery with retry
//
// The two are intentionally separate. Changing the bus provider (e.g. memory →
// redis-streams) does not affect outbox routing.
package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// =============================================================================
// OutboxWriter port
// =============================================================================

// CreateOutboxEvent is the input for writing an event to the outbox table.
type CreateOutboxEvent struct {
	EventID       string
	EventType     string
	CorrelationID string
	TenantID      *string
	AggregateType *string
	AggregateID   *string
	OccurredAt    time.Time
	Payload       json.RawMessage
	Priority      int
	MaxRetries    int
	ScheduledFor  *time.Time // nil defaults to now()
}

// OutboxEvent is the result returned after writing to the outbox.
type OutboxEvent struct {
	EventID   string
	EventType string
	Status    string
}

// OutboxWriter is the port for persisting events to the outbox table.
// Implementations (e.g. an eventoutboxrepo backed by Postgres) satisfy this interface.
type OutboxWriter interface {
	Create(ctx context.Context, input CreateOutboxEvent) (OutboxEvent, error)
}

// =============================================================================
// WriteEventToOutbox
// =============================================================================

// WriteEventToOutbox serializes event and writes it to the outbox via writer.
// Metadata (tenant, aggregate) is extracted from EventWithMetadata if available.
func WriteEventToOutbox(ctx context.Context, writer OutboxWriter, event events.Event, opts Options) error {
	id, err := events.GenerateID()
	if err != nil {
		return err
	}

	payload, err := events.EncodeEvent(event)
	if err != nil {
		return err
	}

	var tenantID, aggregateType, aggregateID *string
	if em, ok := event.(events.EventWithMetadata); ok {
		tenantID = em.TenantID()
		aggregateType = em.AggregateType()
		aggregateID = em.AggregateID()
	}

	_, err = writer.Create(ctx, CreateOutboxEvent{
		EventID:       id,
		EventType:     event.Type(),
		CorrelationID: event.CorrelationID(),
		TenantID:      tenantID,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		OccurredAt:    event.OccurredAt(),
		Payload:       json.RawMessage(payload),
		Priority:      opts.DefaultPriority,
		MaxRetries:    opts.MaxRetries,
	})
	return err
}
