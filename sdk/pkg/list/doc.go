// Package list provides pagination requests and pages, ordering, search,
// cursor encoding and row mapping. Domains own their repository methods and
// filters; adapters implement these shared listing rules.
//
// # Requests
//
// ParseQuery parses limit, cursor, offset, count and q from url.Values. Order
// has its own per-resource allow-list and is parsed separately with ParseOrder.
// Blank query values count as absent. Invalid explicit values and conflicting
// strategies wrap sdk.ErrInvalidInput. ParseRequest accepts the same values
// through Params when url.Values is not the source.
//
// Request.Validate checks strategy consistency before a store touches its
// backend. NormalizedLimit applies resource defaults and clamps the limit.
// Zero Limits means DefaultLimit (25) and MaxLimit (100).
//
// # Pages
//
// Cursor mode is the default. Fetch limit+1 rows and use TrimPage to detect a
// next page. For previous navigation, fetch up to limit+1 records at or before
// the incoming cursor, restore their normal order, and call MarkPrevPage. Any
// record establishes HasPrev. An extra predecessor supplies PreviousCursor;
// otherwise an empty previous cursor opens the first page.
//
// Offset mode must be explicit, even at offset zero. It uses the same stable
// ordering, overfetches one row, sets HasPrev from Offset > 0 and emits no
// cursors. WithCount requests the full filtered population, excluding cursor,
// offset and limit boundaries. Total is nil when a count was not requested.
//
// Order fields and primary keys must form a stable total order. A well-formed
// cursor for another order field resets to the first page; malformed cursors
// are invalid input. Tokens are unsigned positions, not access credentials.
//
// Search matches literal substrings with ASCII case folding. A blank term
// disables search. A nonblank term on a list without searchable fields is
// invalid input. Adapters own how projected fields map to their datastore.
package list
