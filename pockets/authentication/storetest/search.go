package storetest

import (
	"context"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// The list-search conformance cases (crud-search-upstream T4). API-key `name` is
// the pocket's first searchable field, so this group is where a dialect that
// escapes wildcards differently, folds case differently, or forgets to apply the
// predicate to the COUNT is caught.
//
// The oracle rows below are the same ones sdk/foundation/crud's MatchesSearch
// table pins, restated against a real store: if PostgreSQL's ILIKE, SQLite's
// LIKE, and the Go matcher ever disagree, they disagree HERE.

// searchNames are the seeded API-key names. They deliberately include the
// characters a naive `"%" + term + "%"` implementation treats as wildcards.
var searchNames = []string{
	"deploy-bot",
	"Deploy-Admin",
	"ci runner",
	"100% coverage",
	"a_c naming",
	"abc naming",
	"renaming task",
	`back\slash`,
	"発注書",
}

// runSearch registers the search conformance group.
func runSearch(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()

	t.Run("ListSearch", func(t *testing.T) {
		if newRepos(t).APIKeys == nil {
			t.Skip("APIKeys not wired — list-search conformance NOT verified for this Repositories")
		}
		t.Run("LiteralSubstringOracle", func(t *testing.T) { testSearchOracle(t, newRepos(t)) })
		t.Run("BlankTermIsUnfiltered", func(t *testing.T) { testSearchBlankUnfiltered(t, newRepos(t)) })
		t.Run("SearchIsScopedToTheParent", func(t *testing.T) { testSearchScoped(t, newRepos(t)) })
		t.Run("CountReflectsTheSearch", func(t *testing.T) { testSearchCount(t, newRepos(t)) })
		t.Run("SearchWithCursorPaging", func(t *testing.T) { testSearchCursor(t, newRepos(t)) })
	})
}

// seedSearchKeys creates one API key per searchNames entry under sa-search, plus
// a foreign-parent row that must never be matched.
func seedSearchKeys(t *testing.T, repos auth.Repositories) {
	t.Helper()
	for i, name := range searchNames {
		mustCreateAPIKey(t, repos.APIKeys, "sa-search", name, "search-hash-"+name, time.Time{},
			suiteBase.Add(time.Duration(i)*time.Minute))
	}
	// A row under a different parent carrying a name that WOULD match, so a store
	// that forgets its parent predicate fails loudly.
	mustCreateAPIKey(t, repos.APIKeys, "sa-other", "deploy-bot", "search-hash-foreign", time.Time{}, suiteBase)
}

// searchNamesFor runs a search and returns the matched names.
func searchNamesFor(t *testing.T, repos auth.Repositories, term string) []string {
	t.Helper()
	page, err := repos.APIKeys.ListByServiceAccount(context.Background(), "sa-search",
		crud.ListRequest{Limit: 50, Search: term})
	if err != nil {
		t.Fatalf("ListByServiceAccount(search=%q): %v", term, err)
	}
	names := make([]string, 0, len(page.Items))
	for _, k := range page.Items {
		names = append(names, k.Name)
	}
	return names
}

// testSearchOracle is the shared matching table, executed against the store. Each
// row asserts the store agrees with crud.MatchesSearch for EVERY seeded name —
// not just that the expected row came back, but that no unexpected one did.
func testSearchOracle(t *testing.T, repos auth.Repositories) {
	seedSearchKeys(t, repos)

	terms := []string{
		"deploy",       // plain substring, two rows differing only in case
		"DEPLOY",       // ASCII fold
		"Deploy-Admin", // exact
		"runner",       // suffix
		"100%",         // percent is LITERAL
		"%",            // a lone percent must not match everything
		"a_c",          // underscore is LITERAL
		"_",            // a lone underscore must not match everything
		`back\s`,       // backslash is LITERAL
		`\`,            // a lone backslash
		"発注",           // non-Latin substring
		"nothing-here", // no match
		"  deploy  ",   // trimmed
	}

	for _, term := range terms {
		t.Run("term="+term, func(t *testing.T) {
			got := searchNamesFor(t, repos, term)

			// The oracle: exactly the seeded names crud.MatchesSearch accepts.
			var want []string
			for _, name := range searchNames {
				if crud.MatchesSearch(name, term) {
					want = append(want, name)
				}
			}

			if len(got) != len(want) {
				t.Fatalf("search %q returned %v, want %v (the crud.MatchesSearch oracle)", term, got, want)
			}
			set := map[string]bool{}
			for _, n := range got {
				set[n] = true
			}
			for _, n := range want {
				if !set[n] {
					t.Errorf("search %q missed %q; got %v", term, n, got)
				}
			}
		})
	}
}

func testSearchBlankUnfiltered(t *testing.T, repos auth.Repositories) {
	seedSearchKeys(t, repos)

	for _, term := range []string{"", "   "} {
		got := searchNamesFor(t, repos, term)
		if len(got) != len(searchNames) {
			t.Errorf("blank search %q returned %d rows, want all %d", term, len(got), len(searchNames))
		}
	}
}

func testSearchScoped(t *testing.T, repos auth.Repositories) {
	seedSearchKeys(t, repos)

	got := searchNamesFor(t, repos, "deploy-bot")
	if len(got) != 1 {
		t.Fatalf("search returned %v, want exactly the one row under this parent", got)
	}

	// The identically-named foreign row is reachable only under its own parent.
	page, err := repos.APIKeys.ListByServiceAccount(context.Background(), "sa-other",
		crud.ListRequest{Limit: 50, Search: "deploy-bot"})
	if err != nil {
		t.Fatalf("ListByServiceAccount(foreign): %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("the foreign parent returned %d rows, want 1", len(page.Items))
	}
}

// testSearchCount is the query-fan-out trap: WithCount must report the SEARCHED
// total, not the unfiltered one. A store that appends the predicate to only the
// page query returns a page of 2 rows claiming 8 results.
func testSearchCount(t *testing.T, repos auth.Repositories) {
	seedSearchKeys(t, repos)

	page, err := repos.APIKeys.ListByServiceAccount(context.Background(), "sa-search",
		crud.ListRequest{Limit: 50, WithCount: true, Search: "deploy"})
	if err != nil {
		t.Fatalf("ListByServiceAccount: %v", err)
	}
	want := 0
	for _, name := range searchNames {
		if crud.MatchesSearch(name, "deploy") {
			want++
		}
	}
	if len(page.Items) != want {
		t.Fatalf("page has %d items, want %d", len(page.Items), want)
	}
	if page.Total == nil {
		t.Fatal("WithCount produced no Total")
	}
	if int(*page.Total) != want {
		t.Errorf("Total = %d, want the SEARCHED total %d — the count query ignored the search", *page.Total, want)
	}
}

// testSearchCursor proves the search survives cursor paging: the predicate must
// reach the second page and the reverse probe, not just the first query.
func testSearchCursor(t *testing.T, repos auth.Repositories) {
	seedSearchKeys(t, repos)

	ctx := context.Background()

	// Three seeded names contain "naming", paged two at a time. The limit is 2
	// rather than 1 deliberately: HasPrev is derived from rows STRICTLY BEFORE the
	// cursor, and with a limit of 1 the cursor IS the whole first page, so page 2
	// would legitimately report HasPrev=false and the assertion would be testing
	// the pagination semantic rather than the search.
	const term = "naming"
	matching := 0
	for _, name := range searchNames {
		if crud.MatchesSearch(name, term) {
			matching++
		}
	}
	if matching < 3 {
		t.Fatalf("the seed carries %d names matching %q; this case needs at least 3", matching, term)
	}

	first, err := repos.APIKeys.ListByServiceAccount(ctx, "sa-search",
		crud.ListRequest{Limit: 2, Search: term})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("first page has %d items, want 2", len(first.Items))
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page HasMore=%v NextCursor=%q, want more", first.HasMore, first.NextCursor)
	}
	if first.HasPrev {
		t.Error("the first page reports a previous page")
	}

	second, err := repos.APIKeys.ListByServiceAccount(ctx, "sa-search",
		crud.ListRequest{Limit: 2, Cursor: first.NextCursor, Search: term})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != matching-2 {
		t.Fatalf("second page has %d items, want %d", len(second.Items), matching-2)
	}

	// Every paged row matches, and no row repeats: the predicate reached BOTH the
	// forward page query and the reverse probe.
	seen := map[string]bool{}
	for _, k := range append(append([]apikey.APIKey{}, first.Items...), second.Items...) {
		if !crud.MatchesSearch(k.Name, term) {
			t.Errorf("a paged row %q does not match the search; the predicate did not reach this query", k.Name)
		}
		if seen[k.Name] {
			t.Errorf("row %q appeared on both pages", k.Name)
		}
		seen[k.Name] = true
	}
	if !second.HasPrev {
		t.Error("the second page reports no previous page; the reverse probe lost the search predicate")
	}
}
