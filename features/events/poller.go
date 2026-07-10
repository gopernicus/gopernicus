package events

import (
	"context"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
)

// defaultBatchSize is the number of unpublished entries a Poll drains per
// iteration when WithBatchSize is not set.
const defaultBatchSize = 100

// PollerOption configures a Poller at construction.
type PollerOption func(*pollerConfig)

type pollerConfig struct {
	batchSize int
}

// WithBatchSize sets the number of unpublished entries drained per Poll. A
// non-positive value is ignored and the default (100) applies.
func WithBatchSize(n int) PollerOption {
	return func(c *pollerConfig) { c.batchSize = n }
}

// Poller drains the transactional outbox onto the bus: each Poll reads a batch
// of unpublished entries (oldest first), emits each onto the bus, then marks it
// published — the at-least-once durable rail (design §5). It owns NO goroutines
// and no lifecycle: the host drives it on an sdk/foundation/workers pool (Poll is a
// workers.WorkFunc), mirroring the host-drives-execution philosophy. Single
// poller per outbox is the documented v1 assumption; ListUnpublished does no row
// claiming.
type Poller struct {
	repo      outbox.EntryRepository
	bus       sdkevents.Bus
	batchSize int
}

// NewPoller builds a Poller reading unpublished entries from repo and emitting
// them onto bus. Batch size defaults to 100; override with WithBatchSize.
func NewPoller(repo outbox.EntryRepository, bus sdkevents.Bus, opts ...PollerOption) *Poller {
	cfg := pollerConfig{batchSize: defaultBatchSize}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.batchSize <= 0 {
		cfg.batchSize = defaultBatchSize
	}
	return &Poller{repo: repo, bus: bus, batchSize: cfg.batchSize}
}

// Poll runs one drain iteration and satisfies workers.WorkFunc, so a host feeds
// it directly to an sdk/foundation/workers pool. It reads a batch of unpublished entries
// (CreatedAt ascending), emits each as a rehydrated event, then marks it
// published. Poll returns workers.ErrNoWork when the batch is empty so the pool
// backs the worker off to its idle interval.
//
// Emit discipline (design §5, post-gate P1). Every emit uses WithSync, and on an
// emit error Poll returns WITHOUT marking the entry published — the entry stays
// unpublished and the next poll retries it. Sync is load-bearing: the Memory
// bus's async default returns nil even when its bounded queue drops the event,
// and a remote backend's async emit is fire-and-forget, so an async
// publish-then-mark would silently lose durable events. The sync semantics per
// backend:
//
//   - Memory + WithSync returns the FIRST handler error. A failing subscriber
//     therefore also leaves the entry unpublished, so the entry is redelivered
//     on the next poll — consistent with the idempotent-handler contract
//     (handlers must de-dupe on EventID).
//   - A Redis-streams backend + WithSync returns the XADD error properly, so a
//     failed durable publish likewise leaves the entry unpublished.
//
// Closed-bus edge: BOTH the Memory bus and a Redis-streams backend return nil
// (logging a "dropped" warning) on WithSync against a CLOSED bus. Poll would
// then mark the entry published even though nothing was delivered. This is safe
// ONLY because the documented shutdown order stops the poller before bus.Close;
// a host that closes the bus while the poller still runs violates that order.
//
// Ordering: publish-then-mark is at-least-once. A poller crash (or a
// MarkPublished failure) between a successful emit and the mark redelivers the
// entry on the next poll, so consumers MUST de-dupe on EventID().
func (p *Poller) Poll(ctx context.Context) error {
	entries, err := p.repo.ListUnpublished(ctx, p.batchSize)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return workers.ErrNoWork
	}

	for _, entry := range entries {
		evt := newOutboxEvent(entry.Record)
		if err := p.bus.Emit(ctx, evt, sdkevents.WithSync()); err != nil {
			// Emit failed: leave the entry unpublished so the next poll retries
			// it (P1). Do NOT mark it published.
			return err
		}
		if err := p.repo.MarkPublished(ctx, entry.EventID); err != nil {
			// The emit succeeded but the mark did not: the entry stays
			// unpublished and is re-emitted on the next poll. Consumers de-dupe
			// on EventID(), so the duplicate is harmless.
			return err
		}
	}
	return nil
}

// outboxEvent is the feature-local rehydrated event the poller emits for a
// persisted Record (gate edit 1: sdkevents.RemoteEvent carries no EventID and
// its CorrelationID is not unique per event, and sdk/capabilities/events stays frozen). It
// embeds a RemoteEvent so it satisfies sdkevents.Event, sdkevents.Metadata, and
// sdkevents.Unmarshaler (the TypedHandler slow path decodes the payload into the
// subscriber's concrete type), and adds EventID() — the durable rail's de-dupe
// key and what the SSE hub reads for the `id:` field.
type outboxEvent struct {
	sdkevents.RemoteEvent
	eventID string
}

var (
	_ sdkevents.Event       = outboxEvent{}
	_ sdkevents.Metadata    = outboxEvent{}
	_ sdkevents.Unmarshaler = outboxEvent{}
	_ interface {
		EventID() string
	} = outboxEvent{}
)

// newOutboxEvent rehydrates a Record into an outboxEvent. The embedded
// RemoteEvent reuses sdk/capabilities/events' frozen envelope decoding (Unmarshal, Metadata);
// eventID carries the Record primary key the RemoteEvent envelope cannot.
func newOutboxEvent(rec sdkevents.Record) outboxEvent {
	return outboxEvent{
		RemoteEvent: sdkevents.RemoteEvent{
			EventType:   rec.Type,
			Occurred:    rec.OccurredAt,
			Correlation: rec.CorrelationID,
			Payload:     rec.Payload,
			Tenant:      rec.TenantID,
			AggType:     rec.AggregateType,
			AggID:       rec.AggregateID,
		},
		eventID: rec.EventID,
	}
}

// EventID returns the Record's de-dupe key: the outbox primary key, the
// at-least-once de-dupe key, and the SSE `id:` the hub emits.
func (e outboxEvent) EventID() string { return e.eventID }
