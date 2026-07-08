// Package outboxmem is the example-local, in-memory outbox.EntryRepository the
// auth-cms host wires when EVENTS_OUTBOX=memory selects the durable second
// variant (design §8's zero-infra proof: memory bus + in-memory outbox + poller
// + SSE over `go run`, no datastore driver in the graph). It is the runnable
// twin of the test-scoped reference in features/events/storetest — R3/S6 keeps
// the runnable in-memory store example-local, so features/events ships no
// stores/memory module.
//
// Like that reference, Store deliberately HAND-ENFORCES the EventID uniqueness a
// SQL store gets for free from a primary key: Append pre-checks the whole batch
// against existing rows AND within itself before writing any row, so a batch
// carrying a collision commits nothing. That honesty is what makes the
// storetest EventIDUniqueness case a real proof rather than a vacuous pass
// (outboxmem_test.go runs the full suite against this store).
package outboxmem

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
	"github.com/gopernicus/gopernicus/sdk/errs"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// Store is a mutex-backed in-memory outbox.EntryRepository keyed by EventID (the
// primary/de-dupe key). New returns a fresh, empty Store.
type Store struct {
	mu      sync.Mutex
	entries map[string]*outbox.Entry
}

var _ outbox.EntryRepository = (*Store)(nil)

// New builds an empty in-memory outbox store.
func New() *Store {
	return &Store{entries: map[string]*outbox.Entry{}}
}

// Append persists records in their own "transaction", rejecting any EventID that
// already exists (or repeats within the batch) with errs.ErrAlreadyExists. The
// check runs over the whole batch before any row is written, so a batch carrying
// a collision commits nothing — the atomicity a SQL store's transaction gives
// for free. Appending zero records is a nil no-op.
func (s *Store) Append(ctx context.Context, recs ...sdkevents.Record) error {
	if len(recs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]bool, len(recs))
	for _, rc := range recs {
		if _, exists := s.entries[rc.EventID]; exists || seen[rc.EventID] {
			return fmt.Errorf("outboxmem append %q: %w", rc.EventID, errs.ErrAlreadyExists)
		}
		seen[rc.EventID] = true
	}

	now := time.Now().UTC()
	for _, rc := range recs {
		rc := rc
		s.entries[rc.EventID] = &outbox.Entry{Record: rc, CreatedAt: now}
	}
	return nil
}

// ListUnpublished returns up to limit unpublished entries (PublishedAt nil)
// ordered by CreatedAt ascending, EventID breaking ties for determinism. A
// non-positive limit returns every unpublished entry.
func (s *Store) ListUnpublished(ctx context.Context, limit int) ([]outbox.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []outbox.Entry
	for _, e := range s.entries {
		if e.PublishedAt == nil {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].EventID < out[j].EventID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MarkPublished is idempotent: publishing an already-published entry or an
// unknown eventID returns nil, so the poller can retry a mark without a hard
// failure.
func (s *Store) MarkPublished(ctx context.Context, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[eventID]
	if !ok || e.PublishedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	e.PublishedAt = &now
	return nil
}

// PurgePublished deletes published entries whose CreatedAt is strictly before
// the cutoff and returns the count removed; unpublished entries are never purged
// regardless of age.
func (s *Store) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for id, e := range s.entries {
		if e.PublishedAt != nil && e.CreatedAt.Before(before) {
			delete(s.entries, id)
			n++
		}
	}
	return n, nil
}
