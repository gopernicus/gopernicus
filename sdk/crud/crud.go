// Package crud is the optional, opinionated CRUD starter for apps built on sdk:
// a small set of generic contracts (Reader/Writer/CRUD), a cursor-paginated
// Page/ListRequest pair, ordering helpers, and a pure cursor codec. It is a
// convenience contract, not a base class — nothing is required to implement
// Reader[T, F]; domains embed, narrow, or ignore it. There is no SQL generation
// here; providers write their own SQL directly (see integrations/datastores/
// turso, features/cms/stores/turso).
//
// # Two validation semantics, deliberately
//
// The package validates page params at two different edges, with two
// deliberately different postures — strict at the transport edge, clamping or
// mode-checking at the store edge:
//
//   - ParseListRequest is the strict transport-edge parser for untrusted user
//     input (JSON query strings). An empty limit means DefaultLimit, but a
//     non-numeric, non-positive, or over-max limit is an error, never silently
//     clamped. Likewise a non-numeric or negative offset, or a non-bool count,
//     is rejected, and a cursor param combined with an offset param is rejected.
//     JSON transports surface that error as a 400 (via web.ErrValidation once
//     the web JSON kit lands); the caller sees exactly what they asked for or a
//     rejection.
//   - ListRequest.NormalizedLimit is the store-edge clamp: store-side safety
//     that applies the effective default when unset and caps at the effective
//     max. Here a programmatic limit of 0 means "default", not an error — SSR
//     handlers and store adapters keep clamping so a mis-set limit can never
//     issue an unbounded scan.
//
// # Caller intent versus resource policy
//
// ListRequest.Limit is what the caller wants; a Limits value is what the
// resource permits. The two are distinct: a caller asks for a page size, and
// the resource's Limits decides the default and the ceiling that request is
// resolved against. NormalizedLimit and ParseListRequest both take a Limits so
// the same per-resource vocabulary governs both edges. The package constants
// DefaultLimit and MaxLimit are the zero-value fallbacks — a Limits with zero
// fields resolves to them, so sdk stays the opinionated zero-config starter
// while an expensive resource (say, a list of embeddings) can declare a tighter
// Max and a cheap one (bare IDs) a looser one.
//
// The domain-rim convention (documented, not yet exercised): an aggregate whose
// resource needs non-default limits declares a var ListLimits = crud.Limits{…}
// beside its OrderFields/DefaultOrder, and its stores and handlers pass it. No
// current aggregate declares one — every call site passes the zero value.
//   - ListRequest.Validate is the store-edge strategy check, the counterpart to
//     NormalizedLimit's clamp: it rejects a request whose Offset/Cursor
//     contradict its Strategy (a cursor strategy carrying a non-zero Offset, an
//     offset strategy carrying a Cursor, a negative Offset, or an unknown
//     Strategy value) with an error wrapping errs.ErrInvalidInput. Stores call
//     it before touching their backend so a programmatically mis-set request is
//     caught without a transport parse.
//
// # Strategy and count semantics (normative)
//
// A ListRequest runs in one of two strategies, named explicitly by its Strategy
// field — never inferred from Offset:
//
//   - Strategy selection. Strategy names the mode: StrategyCursor (the default;
//     a zero-value Strategy resolves to it) or StrategyOffset. There is no
//     inference from Offset — Offset == 0 under StrategyOffset is a real,
//     expressible first offset page, distinct from cursor mode. A cursor
//     strategy carrying a non-zero Offset, an offset strategy carrying a Cursor,
//     a negative Offset, or an unknown Strategy value is invalid — rejected at
//     both edges (ParseListRequest and Validate).
//   - Cursor strategy (default). The store over-fetches limit+1, calls TrimPage
//     to derive HasMore and NextCursor, and — when Cursor != "" — also runs a
//     reverse probe (flipped comparison and ORDER BY, results reversed back)
//     and applies MarkPrevPage: any probe row ⇒ HasPrev; a full window
//     (len == limit) ⇒ PreviousCursor is the probe's first record; a partial
//     window ⇒ HasPrev with an empty PreviousCursor, meaning "the previous page
//     is the first page". The first page has HasPrev = false.
//   - Offset strategy. Same ORDER BY (order + pk tiebreaker), LIMIT n+1 OFFSET
//     off; HasMore comes from the over-fetch and HasPrev = Offset > 0. NextCursor
//     and PreviousCursor stay empty at every offset — the caller does the offset
//     arithmetic.
//   - WithCount (both strategies). When set, the store computes Page.Total, the
//     full matching row count under the list's filter WHERE — never including the
//     cursor/offset predicates, never capped by limit. Total is nil when a
//     count was not requested.
//   - Stale cursor. When the order field changed between requests, DecodeCursor
//     treats the token as a first page (see cursor.go).
//
// # Query-param vocabulary
//
// Transport edges parse a standard vocabulary into a ListRequest: limit,
// cursor, offset, and count map through ParseListRequest; order=field:direction
// is parsed separately by ParseOrder because the allow-list is per-aggregate.
// Each paginated aggregate declares its allow-list (map[string]OrderField) plus
// a default Order in its feature-core domain package; ParseOrder validates the
// requested field at the edge, and backends validate again (QuoteIdentifier or
// allow-list membership) before use — raw request input never reaches SQL.
package crud

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// DefaultLimit is the fallback page size applied when neither a ListRequest nor
// the resource's Limits sets a default.
const DefaultLimit = 25

// MaxLimit is the fallback page-size ceiling applied when the resource's Limits
// sets no Max.
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

// Strategy names a ListRequest's pagination mode. It is explicit — never
// inferred from Offset — so a programmatic request and a parsed one both name
// their intent, and Offset == 0 under StrategyOffset is a real first offset page
// rather than a silent flip to cursor mode.
type Strategy string

const (
	// StrategyCursor is keyset/cursor pagination — the default. A zero-value
	// Strategy resolves to it (see ResolvedStrategy).
	StrategyCursor Strategy = "cursor"
	// StrategyOffset is LIMIT/OFFSET pagination.
	StrategyOffset Strategy = "offset"
)

// ListRequest is a paginated list query with two strategies. It stays
// non-generic because page params are uniform; the per-aggregate filter rides
// Reader's F type parameter instead. A zero-value Order means the store's
// default order.
//
// Strategy names the mode explicitly (StrategyCursor when empty). Cursor is an
// opaque token produced by a prior Page; an empty Cursor requests the first
// page. A cursor strategy carrying a non-zero Offset, an offset strategy
// carrying a Cursor, a negative Offset, or an unknown Strategy value is invalid
// — rejected by Validate at the store edge and by ParseListRequest at the
// transport edge. When WithCount is set the store also computes Page.Total. See
// the package doc's strategy/count matrix.
type ListRequest struct {
	Limit     int
	Cursor    string
	Offset    int
	Order     Order
	WithCount bool
	Strategy  Strategy // "" resolves to StrategyCursor
}

// ResolvedStrategy returns StrategyCursor when Strategy is empty, else Strategy
// as set. Stores switch on it to pick their cursor or offset flow.
func (r ListRequest) ResolvedStrategy() Strategy {
	if r.Strategy == "" {
		return StrategyCursor
	}
	return r.Strategy
}

// Validate is the store-edge strategy check, called before a List touches its
// backend. It rejects an Offset/Cursor that contradicts the request's Strategy
// (a cursor strategy with a non-zero Offset, an offset strategy with a Cursor,
// a negative Offset) and an unknown Strategy value, each with an error wrapping
// errs.ErrInvalidInput.
func (r ListRequest) Validate() error {
	switch r.Strategy {
	case "", StrategyCursor:
		if r.Offset != 0 {
			return fmt.Errorf("cursor strategy does not accept an offset: %w", errs.ErrInvalidInput)
		}
	case StrategyOffset:
		if r.Cursor != "" {
			return fmt.Errorf("offset strategy does not accept a cursor: %w", errs.ErrInvalidInput)
		}
		if r.Offset < 0 {
			return fmt.Errorf("offset must not be negative: %w", errs.ErrInvalidInput)
		}
	default:
		return fmt.Errorf("unknown pagination strategy %q: %w", r.Strategy, errs.ErrInvalidInput)
	}
	return nil
}

// Limits is the per-resource page-size vocabulary: what the resource permits, as
// distinct from ListRequest.Limit, which is what the caller wants. A zero field
// falls back to the crud constant — Default to DefaultLimit, Max to MaxLimit —
// so the zero value reproduces sdk's opinionated defaults. An aggregate that
// needs non-default limits declares a var ListLimits = crud.Limits{…} in its
// domain rim beside OrderFields; its stores and handlers pass it. See the
// package doc's caller-intent-versus-resource-policy section.
type Limits struct {
	Default int // 0 = DefaultLimit
	Max     int // 0 = MaxLimit
}

// NormalizedLimit returns the effective limit for the store edge, resolving the
// request's Limit against the resource's Limits. The effective default is
// l.Default when positive, else DefaultLimit; the effective max is l.Max when
// positive, else MaxLimit; a declared default greater than the effective max
// clamps to the max (defensive). A limit <= 0 yields the effective default, and
// a limit above the effective max is clamped to it — a limit of 0 means
// "default" here, not an error. See the package doc's two-semantics rule.
func (r ListRequest) NormalizedLimit(l Limits) int {
	defaultLimit := l.Default
	if defaultLimit <= 0 {
		defaultLimit = DefaultLimit
	}
	maxLimit := l.Max
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	if defaultLimit > maxLimit {
		defaultLimit = maxLimit
	}

	limit := r.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
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
	Total          *int64 `json:"total,omitempty"` // nil = not requested
}
