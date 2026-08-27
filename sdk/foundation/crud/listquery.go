package crud

import "net/url"

// The canonical list query keys. A host's OpenAPI or docs name them from here
// rather than repeating literals.
const (
	QueryKeyLimit  = "limit"
	QueryKeyCursor = "cursor"
	QueryKeyOffset = "offset"
	QueryKeyCount  = "count"
	QueryKeySearch = "q"
	QueryKeyOrder  = "order" // read by ParseOrder callers, never by ParseListQuery
)

// ListQueryOptions is the resource-side policy ParseListQuery resolves an
// untrusted query against. The zero value is sdk's defaults (DefaultLimit /
// MaxLimit, StrategyCursor).
type ListQueryOptions struct {
	Limits          Limits
	DefaultStrategy Strategy
}

// ParseListQuery is ParseListRequest over the canonical query keys
// (limit/cursor/offset/count/q). Every rejection wraps sdk.ErrInvalidInput —
// web.ErrFromDomain answers 400; web.ErrValidation carries the sentence. Order
// is a separate concern with a per-aggregate allow-list: parse it beside this
// call with ParseOrder (reject) or fall back to the default order (SSR).
func ParseListQuery(q url.Values, opts ListQueryOptions) (ListRequest, error) {
	return ParseListRequest(ListParams{
		Limit:           q.Get(QueryKeyLimit),
		Cursor:          q.Get(QueryKeyCursor),
		Offset:          q.Get(QueryKeyOffset),
		Count:           q.Get(QueryKeyCount),
		Search:          q.Get(QueryKeySearch),
		Limits:          opts.Limits,
		DefaultStrategy: opts.DefaultStrategy,
	})
}
