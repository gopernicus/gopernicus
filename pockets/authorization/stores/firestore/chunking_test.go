package firestore

import (
	"fmt"
	"testing"
)

// The query-chunking arithmetic, hermetically. Firestore validates NONE of it
// client-side — an over-complex query is an InvalidArgument from the server, on
// a graph shape a small fixture never reaches — so the rules are pinned here
// rather than discovered in production.

// TestMaxChunkHonoursBothVendorCaps pins the two caps a chunk size has to
// satisfy at once: at most thirty disjunctions after DNF expansion, and at most
// one hundred filters PLUS sort orders counted across those disjunctions.
func TestMaxChunkHonoursBothVendorCaps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		filters int
		orders  int
		want    int
	}{
		{name: "one filter is capped by the disjunction limit", filters: 1, orders: 0, want: 30},
		{name: "three filters still fit thirty disjunctions", filters: 3, orders: 0, want: 30},
		{name: "the descendant hop", filters: descendantFiltersPerDisjunct, orders: 0, want: 30},
		{name: "a lookup stream is capped by complexity, not disjunctions", filters: lookupFiltersPerDisjunct, orders: lookupSortOrders, want: 24},
		{name: "a very wide conjunction still yields a legal chunk", filters: 200, orders: 2, want: 1},
		{name: "a zero filter count is treated as one", filters: 0, orders: 0, want: 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maxChunk(tc.filters, tc.orders)
			if got != tc.want {
				t.Fatalf("maxChunk(%d, %d) = %d, want %d", tc.filters, tc.orders, got, tc.want)
			}
			if got*max(tc.filters, 1)+tc.orders > maxQueryComplexity && got != 1 {
				t.Fatalf("maxChunk(%d, %d) = %d exceeds the %d filters+orders cap", tc.filters, tc.orders, got, maxQueryComplexity)
			}
			if got > maxDisjunctions {
				t.Fatalf("maxChunk(%d, %d) = %d exceeds the %d-disjunction cap", tc.filters, tc.orders, got, maxDisjunctions)
			}
		})
	}
}

// TestChunkBudgetsAreTheOnesTheQueriesUse states the resolved budgets as
// literals, so a change to either vendor constant has to be acknowledged here
// before it silently reshapes every lookup.
func TestChunkBudgetsAreTheOnesTheQueriesUse(t *testing.T) {
	if lookupChunkBudget != 24 {
		t.Fatalf("lookupChunkBudget = %d, want 24 ((100 - 2 orders) / 4 filters)", lookupChunkBudget)
	}
	if descendantChunkBudget != 30 {
		t.Fatalf("descendantChunkBudget = %d, want 30 (the disjunction cap binds first)", descendantChunkBudget)
	}
}

// TestChunkProductNeverExceedsItsBudget is the property that keeps a two-`in`
// query legal: the DISJUNCTIONS are the PRODUCT of the two value lists, so
// chunking each list independently is not enough.
func TestChunkProductNeverExceedsItsBudget(t *testing.T) {
	primary := make([]string, 0, 40)
	for i := range 40 {
		primary = append(primary, fmt.Sprintf("p%04d", i))
	}
	secondary := make([]string, 0, 97)
	for i := range 97 {
		secondary = append(secondary, fmt.Sprintf("s%04d", i))
	}

	for _, budget := range []int{1, 2, 7, 24, 30} {
		for _, size := range []int{0, 1, 2, 5, 40} {
			p, s := primary[:size], secondary
			seen := map[[2]string]int{}
			for _, pair := range chunkProduct(p, s, budget) {
				if n := len(pair.primary) * len(pair.secondary); n > budget {
					t.Fatalf("budget %d, %d primaries: a chunk carries %d disjunctions", budget, size, n)
				}
				for _, a := range pair.primary {
					for _, b := range pair.secondary {
						seen[[2]string{a, b}]++
					}
				}
			}
			if len(seen) != size*len(s) {
				t.Fatalf("budget %d, %d primaries: covered %d pairs, want %d", budget, size, len(seen), size*len(s))
			}
			for pair, n := range seen {
				if n != 1 {
					t.Fatalf("budget %d: pair %v queried %d times", budget, pair, n)
				}
			}
		}
	}
}

// TestChunkProductWithNoValuesIssuesNoQuery: an empty frontier or an empty
// relation set must produce no chunks at all, so the caller loops zero times
// rather than issuing a query that matches everything.
func TestChunkProductWithNoValuesIssuesNoQuery(t *testing.T) {
	if got := chunkProduct(nil, []string{"a"}, 30); len(got) != 0 {
		t.Fatalf("no primaries must yield no chunks, got %d", len(got))
	}
	if got := chunkProduct([]string{"a"}, nil, 30); len(got) != 0 {
		t.Fatalf("no secondaries must yield no chunks, got %d", len(got))
	}
}

// TestPageIDsIsTheKeysetWindow pins the descendant closure's in-Go window: after
// is EXCLUSIVE and byte-ordered, a non-positive limit is unbounded, and an
// `after` that is not itself in the closure still cuts at the right place.
func TestPageIDsIsTheKeysetWindow(t *testing.T) {
	ids := []string{"B", "Z", "_x", "a", "~z", "é"}
	for _, tc := range []struct {
		after string
		limit int
		want  []string
	}{
		{after: "", limit: 0, want: ids},
		{after: "", limit: -1, want: ids},
		{after: "", limit: 2, want: []string{"B", "Z"}},
		{after: "B", limit: 0, want: ids[1:]},
		{after: "é", limit: 0, want: nil},
		{after: "C", limit: 0, want: ids[1:]}, // not present: cuts by order
		{after: "_x", limit: 2, want: []string{"a", "~z"}},
		{after: "", limit: 100, want: ids},
	} {
		got := pageIDs(ids, tc.after, tc.limit)
		if len(got) != len(tc.want) {
			t.Fatalf("pageIDs(after %q, limit %d) = %v, want %v", tc.after, tc.limit, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("pageIDs(after %q, limit %d) = %v, want %v", tc.after, tc.limit, got, tc.want)
			}
		}
	}
}
