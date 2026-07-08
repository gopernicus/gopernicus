package securityevent

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// ListFilter narrows a List query. Every field is optional: a zero value means
// "no constraint on this dimension". Since and Until bound CreatedAt (Since is
// inclusive, Until is exclusive); the store composes only the set fields into a
// PARAMETERIZED WHERE — never string concatenation (design §9, plan-cut note).
type ListFilter struct {
	UserID      string
	EventType   string
	EventStatus string
	Since       time.Time // inclusive lower bound on CreatedAt; zero → no lower bound
	Until       time.Time // exclusive upper bound on CreatedAt; zero → no upper bound
}

// SecurityEventRepository persists and reads the append-only audit rail
// (design §5.1). It is APPEND-ONLY BY CONSTRUCTION: there is no Update or Delete
// method, so no adapter can offer a rewrite path. Implemented by feature store
// adapters (features/authentication/stores/turso) or any host-provided implementation
// (see the storetest reference).
//
// Optionality (ratified AV9): the whole port is optional. A host that leaves
// Repositories.SecurityEvents nil keeps no audit trail, and the service's
// recording site becomes a documented no-op.
//
// List is crud-typed (design §9). Ordering is CONTRACTUAL: results are always
// ORDER BY created_at DESC, id DESC — the id tiebreak keeps pages stable when
// several events share a created_at (the storetest collision case asserts
// identical order AND NextCursor across implementations). The Details map
// round-trips uniformly: a nil or empty map stored reads back as a NON-NIL empty
// map (the stores may persist '{}' or NULL, their choice; the read-back contract
// is uniform across backends).
type SecurityEventRepository interface {
	// Create appends an audit row. It returns the stored record. Callers treat
	// it as best-effort (a failure is logged, never surfaced to the auth flow).
	Create(ctx context.Context, evt SecurityEvent) (SecurityEvent, error)
	// List returns a cursor-paginated page of events matching filter, ordered
	// created_at DESC, id DESC. The dynamic filter WHERE is always parameterized.
	List(ctx context.Context, filter ListFilter, req crud.ListRequest) (crud.Page[SecurityEvent], error)
}

// Match reports whether evt satisfies the filter. It is the reference predicate
// the neutral suite pages against and an in-memory adapter reuses; SQL stores
// express the same logic as a parameterized WHERE.
func (f ListFilter) Match(evt SecurityEvent) bool {
	if f.UserID != "" && evt.UserID != f.UserID {
		return false
	}
	if f.EventType != "" && evt.EventType != f.EventType {
		return false
	}
	if f.EventStatus != "" && evt.EventStatus != f.EventStatus {
		return false
	}
	if !f.Since.IsZero() && evt.CreatedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !evt.CreatedAt.Before(f.Until) {
		return false
	}
	return true
}
