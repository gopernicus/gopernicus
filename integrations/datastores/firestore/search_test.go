package firestore_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// searchRow is the row the SearchFilter cases match over.
type searchRow struct {
	Name  string
	Email string
}

// searchValueOf is the store-supplied field reader: the Firestore analogue of
// naming a column in a SQL predicate.
func searchValueOf(row searchRow, field string) string {
	switch field {
	case "name":
		return row.Name
	case "email":
		return row.Email
	}
	return ""
}

// TestSearchFilterMatchesTheCrudOracle pins the shared postfilter to the ONE
// definition of matching every backend uses (crud.MatchesSearch): a literal
// substring under ASCII-only case folding, so `%`, `_` and `\` are ordinary
// characters. A per-store reimplementation is exactly how the three backends
// would drift apart.
func TestSearchFilterMatchesTheCrudOracle(t *testing.T) {
	fields := []crud.SearchField{{Column: "name"}}
	rows := []searchRow{
		{Name: "deploy-bot"},
		{Name: "Deploy-Admin"},
		{Name: "100% coverage"},
		{Name: "a_c naming"},
		{Name: "abc naming"},
		{Name: `back\slash`},
		{Name: "発注書"},
	}

	for _, term := range []string{"deploy", "DEPLOY", "100%", "%", "a_c", "_", `back\s`, `\`, "発注", "nope"} {
		filter := firestore.SearchFilter(fields, searchValueOf, term)
		if filter == nil {
			t.Fatalf("SearchFilter(%q) = nil for a non-blank term over a declared field", term)
		}
		for _, row := range rows {
			want := crud.MatchesSearch(row.Name, term)
			if got := filter(row); got != want {
				t.Errorf("term %q against %q = %v, want %v (the crud.MatchesSearch oracle)", term, row.Name, got, want)
			}
		}
	}
}

// TestSearchFilterAcrossFields is the SQL `OR` across columns: a row matches
// when ANY declared field matches, and a field the store did NOT declare is not
// searched even though the row carries it.
func TestSearchFilterAcrossFields(t *testing.T) {
	row := searchRow{Name: "deploy-bot", Email: "ops@example.com"}

	both := firestore.SearchFilter([]crud.SearchField{{Column: "name"}, {Column: "email"}}, searchValueOf, "example.com")
	if !both(row) {
		t.Error("a term matching the second declared field did not match")
	}

	nameOnly := firestore.SearchFilter([]crud.SearchField{{Column: "name"}}, searchValueOf, "example.com")
	if nameOnly(row) {
		t.Error("a term matched an UNDECLARED field — SearchFields is the allow-list")
	}
}

// TestSearchFilterReturnsNil pins the two nil answers and, with them, the
// refusal they compose into. A blank term is not a filter; an empty field list
// is a list that declares nothing searchable, and List turns that nil into
// sdk.ErrInvalidInput rather than answering a search with an unfiltered page.
func TestSearchFilterReturnsNil(t *testing.T) {
	fields := []crud.SearchField{{Column: "name"}}

	for _, term := range []string{"", "   ", "\t\n"} {
		if firestore.SearchFilter(fields, searchValueOf, term) != nil {
			t.Errorf("SearchFilter(%q) is non-nil; a blank term is not a filter", term)
		}
	}
	if firestore.SearchFilter(nil, searchValueOf, "deploy") != nil {
		t.Error("SearchFilter with no declared fields is non-nil; the list declares nothing searchable")
	}
	if firestore.SearchFilter[searchRow](fields, nil, "deploy") != nil {
		t.Error("SearchFilter with no field reader is non-nil")
	}
}
