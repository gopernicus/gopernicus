package storetest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// TestReference runs the outbox conformance suite against the in-package
// reference implementation. This is what lets features/events self-verify under
// guard G2 (the core cannot import a driver, so without an in-package
// implementation the suite would compile but never execute). newRef returns a
// fresh, empty repository per call — the clean-isolation contract Run documents.
func TestReference(t *testing.T) {
	Run(t, func(t *testing.T) outbox.EntryRepository {
		return newReference()
	})
}

// reference is a stdlib-only, test-scoped in-memory outbox.EntryRepository. It
// exists to give the feature module something to run the suite against, and it
// deliberately HAND-ENFORCES the EventID uniqueness a SQL store gets from a
// primary key — that is exactly the invariant the suite is proving, and a naive
// memory store that silently overwrote (or blindly appended) a duplicate would
// make testEventIDUniqueness vacuously pass. The pre-check below is the honesty
// the phase-2-W7 lesson demands (design §8: memstore-honest).
type reference struct {
	mu      sync.Mutex
	entries map[string]*outbox.Entry // keyed by EventID — the primary/de-dupe key
}

var _ outbox.EntryRepository = (*reference)(nil)

func newReference() *reference {
	return &reference{entries: map[string]*outbox.Entry{}}
}

// Append persists records, rejecting any EventID that already exists (or repeats
// within the batch) with errs.ErrAlreadyExists. The check runs over the whole
// batch before any row is written, so a batch carrying a collision commits
// nothing — the atomicity a SQL store's transaction gives for free.
func (r *reference) Append(ctx context.Context, recs ...sdkevents.Record) error {
	if len(recs) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool, len(recs))
	for _, rc := range recs {
		if _, exists := r.entries[rc.EventID]; exists || seen[rc.EventID] {
			return fmt.Errorf("outbox append %q: %w", rc.EventID, errs.ErrAlreadyExists)
		}
		seen[rc.EventID] = true
	}

	now := time.Now().UTC()
	for _, rc := range recs {
		rc := rc
		r.entries[rc.EventID] = &outbox.Entry{Record: rc, CreatedAt: now}
	}
	return nil
}

// ListUnpublished returns up to limit unpublished entries ordered by CreatedAt
// ascending, EventID breaking ties for determinism.
func (r *reference) ListUnpublished(ctx context.Context, limit int) ([]outbox.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []outbox.Entry
	for _, e := range r.entries {
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
// unknown eventID returns nil.
func (r *reference) MarkPublished(ctx context.Context, eventID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[eventID]
	if !ok || e.PublishedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	e.PublishedAt = &now
	return nil
}

// PurgePublished deletes published entries whose CreatedAt is strictly before
// the cutoff and returns the count removed; unpublished entries are never purged.
func (r *reference) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for id, e := range r.entries {
		if e.PublishedAt != nil && e.CreatedAt.Before(before) {
			delete(r.entries, id)
			n++
		}
	}
	return n, nil
}
