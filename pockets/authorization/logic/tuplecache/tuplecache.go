// Package tuplecache maintains a reconstructible mirror of raw relationships.
// It stores neither permission decisions nor expanded group memberships.
package tuplecache

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	ErrUnavailable    = fmt.Errorf("tuple cache unavailable: %w", sdk.ErrUnavailable)
	ErrCapacity       = fmt.Errorf("tuple cache physical capacity exceeded: %w", ErrUnavailable)
	ErrConflict       = fmt.Errorf("tuple cache publication changed: %w", sdk.ErrConflict)
	ErrBinding        = fmt.Errorf("tuple cache store or freshness policy binding mismatch: %w", sdk.ErrInvalidInput)
	ErrSnapshotClosed = fmt.Errorf("tuple cache snapshot closed: %w", sdk.ErrUnavailable)
)

// State is a delivery receipt, never a cache-key namespace. An empty State
// means no complete mirror exists. A receipt identifies one atomic publication.
type State struct{ Binding, Receipt string }

// SetKey selects a raw index. Reverse selects tuples whose exact subject is Ref;
// otherwise Ref identifies the resource type, ID and relation.
type SetKey struct {
	Reverse bool
	Ref     relationships.SubjectRef
}

// Change retains the complete mutation. Nil Before/After denotes create/delete.
// Both nil is valid only in a Full snapshot: it may identify a reset or an
// ID-only acknowledgement entry whose payload is already reflected in Tuples.
// ID is an opaque durable outbox identity, not a commit-order revision.
type Change struct {
	ID     string
	Before *relationships.CreateRelationship
	After  *relationships.CreateRelationship
}

// Snapshot contains all committed pending changes visible in one source snapshot.
// Full also includes all current tuples and may contain ID-only Changes.
// Receipt is the source's acknowledged
// delivery receipt. A full rebuild must not reapply Changes to Tuples.
type Snapshot struct {
	Receipt string
	Full    bool
	Tuples  []relationships.CreateRelationship
	Changes []Change
}

// CheckReads is borrowed only for the duration of a source snapshot callback.
type CheckReads interface {
	relationships.CheckReadSource
	HasExactRole(context.Context, string, string, string, string, string) (bool, error)
}

// Source owns authoritative facts and atomic mutation capture. Implementations
// reject ambient transactions for delivery/snapshot operations. Ordinary guarded
// writes keep their existing transaction-bound readers.
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
	Read(ctx context.Context, expected State, keys []SetKey) ([][]relationships.SubjectRef, error)
	// Publish compares expected atomically. Full replaces all data from Tuples;
	// otherwise apply Changes in order, updating only affected index fields.
	// validFor is the remaining eligibility period, measured on backend time.
	// A no-change publication may retain the receipt and only renew eligibility.
	Publish(ctx context.Context, expected, next State, snapshot Snapshot, validFor time.Duration) error
}
