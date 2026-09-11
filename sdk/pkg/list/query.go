package list

import "net/url"

// The canonical list query keys. A host's OpenAPI or docs name them from here
// rather than repeating literals.
const (
	QueryKeyLimit  = "limit"
	QueryKeyCursor = "cursor"
	QueryKeyOffset = "offset"
	QueryKeyCount  = "count"
	QueryKeySearch = "q"
	QueryKeyOrder  = "order" // read by ParseOrder callers, never by ParseQuery
)

// QueryOptions is the resource-side policy ParseQuery resolves an
// untrusted query against. The zero value is sdk's defaults (DefaultLimit /
// MaxLimit, StrategyCursor).
type QueryOptions struct {
	Limits          Limits
	DefaultStrategy Strategy
}

// ParseQuery is ParseRequest over the canonical query keys
// (limit/cursor/offset/count/q). Every rejection wraps sdk.ErrInvalidInput —
// web.ErrFromDomain answers 400; web.ErrValidation carries the sentence. Order
// is a separate concern with a per-aggregate allow-list: parse it beside this
// call with ParseOrder (reject) or fall back to the default order (SSR).
func ParseQuery(q url.Values, opts QueryOptions) (Request, error) {
	return ParseRequest(Params{
		Limit:           q.Get(QueryKeyLimit),
		Cursor:          q.Get(QueryKeyCursor),
		Offset:          q.Get(QueryKeyOffset),
		Count:           q.Get(QueryKeyCount),
		Search:          q.Get(QueryKeySearch),
		Limits:          opts.Limits,
		DefaultStrategy: opts.DefaultStrategy,
	})
}
