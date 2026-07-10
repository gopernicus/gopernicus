// Package outbox is the transactional-outbox domain of the events feature: the
// persisted Entry row and the EntryRepository outbound port a store adapter
// (features/events/stores/turso, features/events/stores/postgres, an in-memory
// store) fills. The poller reads unpublished entries through this port, emits
// them onto the bus, then marks them published — the at-least-once durable
// rail (design §5). The port stays dialect-blind; each store implements it and
// the shared storetest suite runs the doc contract below against every store.
package outbox

import (
	"context"
	"time"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// Entry is one persisted outbox row: the durable event envelope plus delivery
// bookkeeping. The embedded Record's EventID is the primary key and the
// at-least-once de-dupe key. A nil PublishedAt means the entry is unpublished
// (the poller has not yet emitted it); a non-nil PublishedAt records when the
// poller marked it delivered.
type Entry struct {
	sdkevents.Record            // EventID is the primary key / de-dupe key
	CreatedAt        time.Time  // when the row was appended
	PublishedAt      *time.Time // nil = unpublished
}

// EntryRepository is the poller's outbound port for the durable rail
// (constitution rule 3: the port lives with its consumer, the events feature).
// A store adapter or an in-memory store fills it; the feature core stays
// dialect-blind. The doc comments below are the contract the storetest
// conformance suite executes against every implementation.
type EntryRepository interface {
	// Append persists records in their own transaction — the non-transactional
	// convenience path (the transactional appender that shares the emitting
	// feature's commit is a store-level concern, not this port). Appending a
	// record whose EventID already exists returns sdk.ErrAlreadyExists; the
	// EventID is the row's primary key and the de-dupe key. Appending zero
	// records is a no-op that returns nil.
	Append(ctx context.Context, recs ...sdkevents.Record) error

	// ListUnpublished returns up to limit unpublished entries (PublishedAt nil)
	// ordered by CreatedAt ascending — oldest first, so the poller drains in
	// append order. A non-positive limit is implementation-defined; callers
	// pass a positive batch size.
	ListUnpublished(ctx context.Context, limit int) ([]Entry, error)

	// MarkPublished records the entry with the given eventID as published. It is
	// idempotent: marking an already-published entry returns nil, and marking an
	// unknown eventID returns nil (the row may have been purged) — the poller
	// must be able to retry a mark without a hard failure.
	MarkPublished(ctx context.Context, eventID string) error

	// PurgePublished deletes published entries whose CreatedAt is strictly
	// before the given time and returns the number removed — outbox retention.
	// Unpublished entries are never purged regardless of age.
	PurgePublished(ctx context.Context, before time.Time) (int, error)
}
