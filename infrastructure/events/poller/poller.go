// Package poller reads committed event_outbox rows and publishes them to the
// event bus. It uses repository interfaces — no SQL, no database imports.
//
// Wire it with sdk/workers.WorkerPool for adaptive polling:
//
//	p := poller.New(outboxRepo, bus, poller.WithBatchSize(50), poller.WithLogger(log))
//	pool := workers.NewPool(p.Poll, workers.Options{...})
//	go pool.Start(ctx)
package poller

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// OutboxEntry is the minimal interface the poller needs from an outbox row.
type OutboxEntry interface {
	GetID() string
	GetEventType() string
	GetPayload() json.RawMessage
}

// OutboxReader lists unpublished outbox entries and marks them published.
type OutboxReader interface {
	ListUnpublished(ctx context.Context, limit int) ([]OutboxEntry, error)
	MarkPublished(ctx context.Context, id string) error
}

// Poller reads committed outbox rows and publishes them to the event bus.
type Poller struct {
	reader    OutboxReader
	bus       events.Bus
	batchSize int
	log       *slog.Logger
}

// Option configures a Poller.
type Option func(*Poller)

// WithBatchSize sets how many rows to read per poll cycle.
func WithBatchSize(n int) Option {
	return func(p *Poller) { p.batchSize = n }
}

// WithLogger sets the logger.
func WithLogger(log *slog.Logger) Option {
	return func(p *Poller) { p.log = log }
}

// New creates a Poller.
func New(reader OutboxReader, bus events.Bus, opts ...Option) *Poller {
	p := &Poller{
		reader:    reader,
		bus:       bus,
		batchSize: 50,
		log:       slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Poll reads unpublished outbox rows and publishes them to the event bus.
// Returns workers.ErrNoWork when no rows are available, which triggers the
// WorkerPool's idle interval.
func (p *Poller) Poll(ctx context.Context) error {
	entries, err := p.reader.ListUnpublished(ctx, p.batchSize)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return workers.ErrNoWork
	}

	for _, entry := range entries {
		evt := outboxEvent{
			eventType: entry.GetEventType(),
			payload:   entry.GetPayload(),
		}

		if err := p.bus.Emit(ctx, evt); err != nil {
			p.log.ErrorContext(ctx, "poller: failed to emit event",
				"event_id", entry.GetID(),
				"event_type", entry.GetEventType(),
				"err", err,
			)
			continue
		}

		if err := p.reader.MarkPublished(ctx, entry.GetID()); err != nil {
			p.log.ErrorContext(ctx, "poller: failed to mark published",
				"event_id", entry.GetID(),
				"err", err,
			)
		}
	}

	return nil
}

// outboxEvent reconstructs an event from an outbox row for bus emission.
// Subscribers use events.TypedHandler which calls Unmarshal to deserialize.
type outboxEvent struct {
	eventType string
	payload   json.RawMessage
}

func (e outboxEvent) Type() string          { return e.eventType }
func (e outboxEvent) OccurredAt() time.Time { return time.Time{} }
func (e outboxEvent) CorrelationID() string { return "" }
func (e outboxEvent) Unmarshal(target any) error {
	return json.Unmarshal(e.payload, target)
}
