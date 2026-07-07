// Package crud is the optional, opinionated CRUD starter for apps built on sdk:
// a small set of generic contracts (Reader/Writer/CRUD), a cursor-paginated
// Page/ListRequest pair, ordering helpers, and a pure cursor codec. It is a
// convenience contract, not a base class — nothing is required to implement
// Reader[T, F]; domains embed, narrow, or ignore it. There is no SQL generation
// here; providers write their own SQL directly (see integrations/datastores/
// turso, features/cms/stores/turso).
//
// # Two limit-validation semantics, deliberately
//
// The package carries two ways to bound a page size, for two different edges:
//
//   - ParseListRequest is the strict transport-edge parser for untrusted user
//     input (JSON query strings). An empty limit means DefaultLimit, but a
//     non-numeric, non-positive, or over-max limit is an error, never silently
//     clamped. JSON transports surface that error as a 400 (via
//     web.ErrValidation once the web JSON kit lands); the caller sees exactly
//     what they asked for or a rejection.
//   - ListRequest.NormalizedLimit is the store-edge clamp: store-side safety
//     that applies DefaultLimit when unset and caps at MaxLimit. Here a
//     programmatic limit of 0 means "default", not an error — SSR handlers and
//     store adapters keep clamping so a mis-set limit can never issue an
//     unbounded scan.
package crud

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// DefaultLimit is the page size applied when a ListRequest sets none.
const DefaultLimit = 25

// MaxLimit caps the page size a ListRequest may ask for.
const MaxLimit = 100

// ErrNotFound is the shared sentinel returned when an entity is absent.
// Aliased from sdk/errs so crud consumers can check one symbol.
var ErrNotFound = errs.ErrNotFound

// Reader is the read side of a repository for entity T, filtered by domain
// filter F. Page params are uniform across aggregates, but the filter — the
// query shape a List call needs beyond those params — varies per aggregate, so
// it rides the type parameter rather than ListRequest.
type Reader[T, F any] interface {
	// Get returns the entity with the given id, or ErrNotFound when absent.
	Get(ctx context.Context, id string) (T, error)
	// List returns a cursor-paginated page of entities matching filter.
	List(ctx context.Context, filter F, req ListRequest) (Page[T], error)
}

// Writer is the write side of a repository for entity T, with create input C
// and update input U.
type Writer[T, C, U any] interface {
	Create(ctx context.Context, in C) (T, error)
	Update(ctx context.Context, id string, in U) (T, error)
	Delete(ctx context.Context, id string) error
}

// CRUD composes the read and write sides.
type CRUD[T, F, C, U any] interface {
	Reader[T, F]
	Writer[T, C, U]
}

// ListRequest is a cursor-paginated list query. It stays non-generic because
// page params are uniform; the per-aggregate filter rides Reader's F type
// parameter instead. Cursor is an opaque token produced by a prior Page; an
// empty Cursor requests the first page. A zero-value Order means the store's
// default order.
type ListRequest struct {
	Limit  int
	Cursor string
	Order  Order
}

// NormalizedLimit returns the effective limit for the store edge, applying
// DefaultLimit when unset and clamping to MaxLimit. A limit of 0 means
// "default" here, not an error — see the package doc's two-semantics rule.
func (r ListRequest) NormalizedLimit() int {
	limit := r.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return limit
}

// Page is one page of a cursor-paginated list. The json tags anticipate the
// JSON-API surface (the web kit); SSR consumers ignore them.
type Page[T any] struct {
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
}
