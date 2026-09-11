package list

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultLimit is the fallback page size applied when neither a Request nor
// the resource's Limits sets a default.
const DefaultLimit = 25

// MaxLimit is the fallback page-size ceiling applied when the resource's Limits
// sets no Max.
const MaxLimit = 100

// Strategy names a Request's pagination mode. It is explicit — never
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

// Request carries pagination, ordering and search input. Domain filters live
// beside it in domain-owned repository methods. A zero Order uses the store
// default.
//
// Strategy names the mode explicitly (StrategyCursor when empty). Cursor is an
// opaque token produced by a prior Page; an empty Cursor requests the first
// page. A cursor strategy carrying a non-zero Offset, an offset strategy
// carrying a Cursor, a negative Offset, or an unknown Strategy value is invalid
// — rejected by Validate at the store edge and by ParseRequest at the
// transport edge. When WithCount is set the store also computes Page.Total. See
// the package doc's strategy/count matrix.
type Request struct {
	Limit     int
	Cursor    string
	Offset    int
	Order     Order
	WithCount bool
	Strategy  Strategy // "" resolves to StrategyCursor
	// Search is the case-insensitive LITERAL substring a store applies across its
	// declared SearchFields (crud-search-upstream T1). Blank means no search.
	//
	// Stores trim it again rather than trusting the transport parser, because a
	// programmatically constructed Request bypasses ParseRequest entirely.
	// A non-blank Search against a list that declares NO searchable fields is
	// sdk.ErrInvalidInput — never a silently unfiltered page presented as a search
	// result. See MatchesSearch for the exact matching and case-folding contract.
	Search string
}

// ResolvedStrategy returns StrategyCursor when Strategy is empty, else Strategy
// as set. Stores switch on it to pick their cursor or offset flow.
func (r Request) ResolvedStrategy() Strategy {
	if r.Strategy == "" {
		return StrategyCursor
	}
	return r.Strategy
}

// Validate is the store-edge strategy check, called before a List touches its
// backend. It rejects an Offset/Cursor that contradicts the request's Strategy
// (a cursor strategy with a non-zero Offset, an offset strategy with a Cursor,
// a negative Offset) and an unknown Strategy value, each with an error wrapping
// sdk.ErrInvalidInput.
func (r Request) Validate() error {
	switch r.Strategy {
	case "", StrategyCursor:
		if r.Offset != 0 {
			return fmt.Errorf("cursor strategy does not accept an offset: %w", sdk.ErrInvalidInput)
		}
	case StrategyOffset:
		if r.Cursor != "" {
			return fmt.Errorf("offset strategy does not accept a cursor: %w", sdk.ErrInvalidInput)
		}
		if r.Offset < 0 {
			return fmt.Errorf("offset must not be negative: %w", sdk.ErrInvalidInput)
		}
	default:
		return fmt.Errorf("unknown pagination strategy %q: %w", r.Strategy, sdk.ErrInvalidInput)
	}
	return nil
}

// Limits sets a resource's default and maximum page sizes. Nonpositive fields
// use DefaultLimit and MaxLimit. A default above the maximum is clamped.
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
func (r Request) NormalizedLimit(l Limits) int {
	defaultLimit, maxLimit := l.resolved()

	limit := r.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit
}

func (l Limits) resolved() (int, int) {
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

	return defaultLimit, maxLimit
}

// Page is one page of a cursor-paginated list. The json tags anticipate the
// JSON-API surface (the web kit); SSR consumers ignore them.
//
// Every field except Items is omitempty, so a final page serializes as just
// {"items":[…]}. Clients must read an absent has_more/next_cursor as
// false/empty — absence is the normal end-of-list signal, not an error.
type Page[T any] struct {
	// Items is nil-normalized by the SDK constructors and bridges — build a page
	// with Items, MapItems, TrimPage, MapPage, or MapPageErr and an empty page
	// marshals "items":[]. A directly constructed Page[T]{} is caller-owned and
	// still marshals "items":null.
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	Total          *int64 `json:"total,omitempty"` // nil = not requested
}
