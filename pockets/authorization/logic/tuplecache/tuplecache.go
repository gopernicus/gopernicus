// Package tuplecache maintains a reconstructible mirror of canonical authorization tuples.
// It stores neither permission decisions nor expanded group memberships.
package tuplecache

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	ErrUnavailable    = fmt.Errorf("tuple cache unavailable: %w", sdk.ErrUnavailable)
	ErrCapacity       = fmt.Errorf("tuple cache physical capacity exceeded: %w", ErrUnavailable)
	ErrConflict       = fmt.Errorf("tuple cache publication changed: %w", sdk.ErrConflict)
	ErrBinding        = fmt.Errorf("tuple cache store or freshness policy binding mismatch: %w", sdk.ErrInvalidInput)
	ErrSnapshotClosed = tuples.ErrSnapshotClosed
)

// State is a delivery receipt, never a cache-key namespace. An empty State
// means no complete mirror exists. A receipt identifies one atomic publication.
type State struct{ Binding, Receipt string }

// Protocol versions every canonical snapshot and backend namespace.
const Protocol = 2

// SetKey selects a canonical forward or reverse raw index.
type SetKey = tuples.SetKey

// Change retains the complete mutation. Nil Before/After denotes create/delete.
// Both nil is valid only in a Full snapshot: it may identify a reset or an
// ID-only acknowledgement entry whose payload is already reflected in Tuples.
// ID is an opaque durable outbox identity, not a commit-order revision.
type Change struct {
	ID     string
	Before *tuples.Tuple
	After  *tuples.Tuple
}

// Snapshot contains all committed pending changes visible in one source snapshot.
// Full also includes all current tuples and may contain ID-only Changes.
// Receipt is the source's acknowledged
// delivery receipt. A full rebuild must not reapply Changes to Tuples.
type Snapshot struct {
	Receipt string
	Full    bool
	Tuples  []tuples.Tuple
	Changes []Change
}

// CheckReads is borrowed only for the duration of a source snapshot callback.
type CheckReads = tuples.Reader

// Source owns authoritative facts and atomic mutation capture. Delivery Snapshot
// and Acknowledge reject ambient transactions. ReadSnapshot borrows a suitable
// ambient tuple snapshot or rejects insufficient isolation; it never detaches
// reads from pending writes. Guarded mutations retain their serialized readers.
type Source interface {
	Binding() string
	CacheableContext(context.Context) bool
	Snapshot(ctx context.Context, mirroredReceipt string) (Snapshot, error)
	// Acknowledge compares the source receipt, updates it and deletes exactly
	// ids in one transaction. A mismatch returns ErrConflict without changes.
	Acknowledge(ctx context.Context, expectedReceipt, receipt string, ids []string) error
	ReadSnapshot(context.Context, func(context.Context, CheckReads) error) error
}

// Backend owns a complete raw mirror, its atomic publications and readiness.
// No operation may expose a partially applied batch or partially built mirror.
// The host supplies a dedicated namespace and owns the backend's lifecycle.
type Backend interface {
	// State returns even an expired receipt for recovery; it does not prove
	// that cached permission reads are eligible.
	State(context.Context) (State, error)
	// Read returns one complete set per key, in input order. An empty set is
	// known absence only in a complete, unexpired mirror matching expected.
	// Empty keys validate eligibility without reading any tuple fields.
	Read(ctx context.Context, expected State, keys []SetKey) ([][]tuples.Tuple, error)
	// Publish compares expected atomically. Full replaces all data from Tuples;
	// otherwise apply Changes in order, updating only affected index fields.
	// validFor is the remaining eligibility period, measured on backend time.
	// A no-change publication may retain the receipt and only renew eligibility.
	Publish(ctx context.Context, expected, next State, snapshot Snapshot, validFor time.Duration) error
}
