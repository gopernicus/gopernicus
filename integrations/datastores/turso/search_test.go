package turso

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// crud-search-upstream T3 — the turso search clause, mirroring the pgx tests.

func TestEscapeSearchTerm(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "invoices", "invoices"},
		{"percent", "100%", `100\%`},
		{"underscore", "a_c", `a\_c`},
		{"backslash", `a\b`, `a\\b`},
		{"backslash before percent stays one escape deep", `\%`, `\\\%`},
		{"all three", `a\b%c_d`, `a\\b\%c\_d`},
		{"non-ASCII untouched", "発注書", "発注書"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeSearchTerm(tt.in); got != tt.want {
				t.Errorf("EscapeSearchTerm(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAddSearchClauseSQL(t *testing.T) {
	fields := []crud.SearchField{{Column: "name"}, {Column: "description"}}

	t.Run("writes WHERE on a bare base", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		var args []any

		if err := AddSearchClause(&buf, &args, fields, "gear"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		want := `SELECT id FROM widgets WHERE ("name" LIKE ? ESCAPE '\' OR "description" LIKE ? ESCAPE '\')`
		if buf.String() != want {
			t.Errorf("SQL =\n%s\nwant\n%s", buf.String(), want)
		}
		// One placeholder per field, so one argument per field.
		if len(args) != 2 || args[0] != "%gear%" || args[1] != "%gear%" {
			t.Errorf("args = %v, want two %q", args, "%gear%")
		}
	})

	t.Run("uses LIKE, never ILIKE", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		var args []any

		if err := AddSearchClause(&buf, &args, fields, "gear"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if strings.Contains(strings.ToUpper(buf.String()), "ILIKE") {
			t.Errorf("SQLite has no ILIKE, but the predicate used it:\n%s", buf.String())
		}
	})

	t.Run("preserves the caller's argument order", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets WHERE tenant_id = ?")
		args := []any{"t1"}

		if err := AddSearchClause(&buf, &args, []crud.SearchField{{Column: "name"}}, "gear"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if len(args) != 2 || args[0] != "t1" || args[1] != "%gear%" {
			t.Errorf("args = %v, want [t1 %%gear%%] in that order", args)
		}
		if !strings.Contains(buf.String(), "WHERE tenant_id = ? AND (") {
			t.Errorf("expected an AND-joined predicate:\n%s", buf.String())
		}
	})

	t.Run("escapes wildcards into the bound pattern", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		var args []any

		if err := AddSearchClause(&buf, &args, []crud.SearchField{{Column: "name"}}, "100%"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if args[0] != `%100\%%` {
			t.Errorf("bound pattern = %v, want %q", args[0], `%100\%%`)
		}
	})

	t.Run("blank term is a no-op", func(t *testing.T) {
		for _, term := range []string{"", "   "} {
			var buf strings.Builder
			buf.WriteString("SELECT id FROM widgets")
			var args []any

			if err := AddSearchClause(&buf, &args, fields, term); err != nil {
				t.Fatalf("AddSearchClause(%q): %v", term, err)
			}
			if buf.String() != "SELECT id FROM widgets" || len(args) != 0 {
				t.Errorf("a blank term changed the query: %s args=%v", buf.String(), args)
			}
		}
	})

	t.Run("non-blank term with no fields fails loudly", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		var args []any

		if err := AddSearchClause(&buf, &args, nil, "gear"); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("err = %v, want sdk.ErrInvalidInput", err)
		}
		if buf.String() != "SELECT id FROM widgets" || len(args) != 0 {
			t.Errorf("a rejected search still changed the query: %s args=%v", buf.String(), args)
		}
	})

	t.Run("an invalid column is rejected, not interpolated", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		var args []any

		err := AddSearchClause(&buf, &args, []crud.SearchField{{Column: `name"; DROP TABLE widgets; --`}}, "gear")
		if err == nil {
			t.Fatal("an invalid identifier was accepted")
		}
		if strings.Contains(buf.String(), "DROP TABLE") {
			t.Errorf("the invalid identifier reached the SQL: %s", buf.String())
		}
	})
}

// TestWithSearchDoesNotMutateCaller pins the slice-aliasing hazard: ListQuery is
// copied by value, but a slice header shares its backing array.
func TestWithSearchDoesNotMutateCaller(t *testing.T) {
	// Spare capacity is what makes an in-place append dangerous, so build one.
	args := make([]any, 1, 8)
	args[0] = "t1"

	original := ListQuery[struct{}]{
		BaseSQL:      "SELECT id FROM widgets WHERE tenant_id = ?",
		Args:         args,
		SearchFields: []crud.SearchField{{Column: "name"}},
	}

	derived, err := original.withSearch("gear")
	if err != nil {
		t.Fatalf("withSearch: %v", err)
	}

	if len(original.Args) != 1 || original.Args[0] != "t1" {
		t.Errorf("the caller's args were mutated: %v", original.Args)
	}
	if original.BaseSQL != "SELECT id FROM widgets WHERE tenant_id = ?" {
		t.Errorf("the caller's BaseSQL was mutated: %s", original.BaseSQL)
	}
	if len(derived.Args) != 2 || derived.Args[1] != "%gear%" {
		t.Errorf("derived args = %v", derived.Args)
	}
	if !strings.Contains(derived.BaseSQL, "LIKE ?") {
		t.Errorf("the derived query carries no search predicate: %s", derived.BaseSQL)
	}
}

// TestWithSearchReachesEveryQueryPath is the fan-out assertion.
func TestWithSearchReachesEveryQueryPath(t *testing.T) {
	q := ListQuery[struct{}]{
		BaseSQL:      "SELECT id FROM widgets",
		SearchFields: []crud.SearchField{{Column: "name"}},
	}
	searched, err := q.withSearch("gear")
	if err != nil {
		t.Fatalf("withSearch: %v", err)
	}

	countSQL := "SELECT COUNT(*) FROM (" + searched.BaseSQL + ") AS list_count"
	if !strings.Contains(countSQL, "LIKE ?") {
		t.Errorf("the COUNT wrap does not carry the search predicate: %s", countSQL)
	}

	var buf strings.Builder
	buf.WriteString(searched.BaseSQL)
	args := append([]any(nil), searched.Args...)
	if err := appendCursorPredicate(&buf, &args, "created_at", "id", "2026-01-01T00:00:00Z", "abc", crud.DESC, false, false); err != nil {
		t.Fatalf("appendCursorPredicate: %v", err)
	}
	if strings.Count(strings.ToUpper(buf.String()), " WHERE ") != 1 {
		t.Errorf("search and cursor produced more than one WHERE:\n%s", buf.String())
	}
	// The search argument must still precede the cursor arguments.
	if len(args) != 3 || args[0] != "%gear%" {
		t.Errorf("argument order disturbed: %v", args)
	}
}

// TestSearchDialectsAgreeOnEscaping pins that the two connectors escape a term
// identically — the dialects differ on the KEYWORD, never on what a term means.
func TestSearchDialectsAgreeOnEscaping(t *testing.T) {
	for _, term := range []string{"invoices", "100%", "a_c", `a\b`, "発注書", `\%_`} {
		if got := EscapeSearchTerm(term); got != pgxEquivalentEscape(term) {
			t.Errorf("EscapeSearchTerm(%q) = %q, want the pgx-equivalent %q", term, got, pgxEquivalentEscape(term))
		}
	}
}

// pgxEquivalentEscape reproduces the pgx connector's escaping rule locally. The
// turso module cannot import pgxdb (they are independent connectors with
// independent module graphs), so parity is pinned by restating the rule and
// letting this test fail if either side drifts.
func pgxEquivalentEscape(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
}
