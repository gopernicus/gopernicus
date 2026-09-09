package firestore

import (
	"strings"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// SearchFilter builds the ListQuery.PostFilter that answers crud.ListRequest's
// Search over a Firestore list (ruling R4). Firestore has no substring
// operator, so the predicate the SQL connectors express as
// `LIKE ? ESCAPE '\'` / `ILIKE` is Go code here, page-filled by List — and this
// is the ONE implementation of it, so the two pocket store trains cannot drift
// from each other or from turso.
//
// fields is the store's SearchFields allow-list, exactly as it is declared for
// pgx and turso; valueOf reads a row's value for one of those columns (the
// Firestore analogue of naming the column in the SQL predicate). A row matches
// when ANY declared field matches, which is the SQL `OR` across columns.
// Matching itself is crud.MatchesSearch — a LITERAL substring under ASCII-only
// case folding, so `%`, `_` and `\` are ordinary characters and non-ASCII code
// points compare exactly. All three backends therefore answer the same term the
// same way, which is what the pockets' storetest search group asserts.
//
// It returns NIL — meaning "no filter" — in exactly two cases:
//
//   - a blank (or whitespace-only) term: no search is not a filter, and a nil
//     PostFilter lets the list take its cheap server-side path;
//   - a non-blank term over an EMPTY field list: the list declares nothing
//     searchable. Do not substitute a permissive filter for that nil; hand it
//     to ListQuery.PostFilter as-is and List refuses the request with
//     sdk.ErrInvalidInput, which is the same answer turso's AddSearchClause
//     gives. A list that answers a search with an unfiltered page is the false
//     green R4 exists to prevent.
//
// The cost model is the page-fill cost model: O(documents scanned), not
// O(rows returned). R4 restricts Search to parent-scoped lists for that reason.
func SearchFilter[T any](fields []crud.SearchField, valueOf func(row T, field string) string, term string) func(T) bool {
	if strings.TrimSpace(term) == "" || len(fields) == 0 || valueOf == nil {
		return nil
	}

	columns := make([]string, 0, len(fields))
	for _, f := range fields {
		columns = append(columns, f.Column)
	}

	return func(row T) bool {
		for _, column := range columns {
			if crud.MatchesSearch(valueOf(row, column), term) {
				return true
			}
		}
		return false
	}
}
