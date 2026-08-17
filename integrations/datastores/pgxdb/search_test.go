package pgxdb

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// crud-search-upstream T2 — the pgx search clause.

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
		args := pgx.NamedArgs{}

		if err := AddSearchClause(&buf, args, fields, "gear"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		got := buf.String()
		want := `SELECT id FROM widgets WHERE (("name" COLLATE "C") ILIKE @list_search ESCAPE '\' OR ("description" COLLATE "C") ILIKE @list_search ESCAPE '\')`
		if got != want {
			t.Errorf("SQL =\n%s\nwant\n%s", got, want)
		}
		if args[searchArg] != "%gear%" {
			t.Errorf("bound pattern = %v, want %q", args[searchArg], "%gear%")
		}
	})

	t.Run("writes AND when the base already filters", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets WHERE tenant_id = @tenant")
		args := pgx.NamedArgs{"tenant": "t1"}

		if err := AddSearchClause(&buf, args, fields, "gear"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if !strings.Contains(buf.String(), "WHERE tenant_id = @tenant AND ((") {
			t.Errorf("expected an AND-joined predicate:\n%s", buf.String())
		}
		if args["tenant"] != "t1" {
			t.Error("the caller's own argument was disturbed")
		}
	})

	t.Run("escapes wildcards into the bound pattern", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		args := pgx.NamedArgs{}

		if err := AddSearchClause(&buf, args, fields, "100%"); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if args[searchArg] != `%100\%%` {
			t.Errorf("bound pattern = %v, want %q", args[searchArg], `%100\%%`)
		}
	})

	t.Run("trims the term", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		args := pgx.NamedArgs{}

		if err := AddSearchClause(&buf, args, fields, "  gear  "); err != nil {
			t.Fatalf("AddSearchClause: %v", err)
		}
		if args[searchArg] != "%gear%" {
			t.Errorf("bound pattern = %v, want the trimmed %q", args[searchArg], "%gear%")
		}
	})

	t.Run("blank term is a no-op", func(t *testing.T) {
		for _, term := range []string{"", "   "} {
			var buf strings.Builder
			buf.WriteString("SELECT id FROM widgets")
			args := pgx.NamedArgs{}

			if err := AddSearchClause(&buf, args, fields, term); err != nil {
				t.Fatalf("AddSearchClause(%q): %v", term, err)
			}
			if buf.String() != "SELECT id FROM widgets" {
				t.Errorf("a blank term appended SQL: %s", buf.String())
			}
			if _, bound := args[searchArg]; bound {
				t.Error("a blank term bound the reserved argument")
			}
		}
	})

	t.Run("non-blank term with no fields fails loudly", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		args := pgx.NamedArgs{}

		err := AddSearchClause(&buf, args, nil, "gear")
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("err = %v, want sdk.ErrInvalidInput", err)
		}
		if buf.String() != "SELECT id FROM widgets" {
			t.Errorf("a rejected search still appended SQL: %s", buf.String())
		}
	})

	t.Run("an invalid column is rejected, not interpolated", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets")
		args := pgx.NamedArgs{}

		err := AddSearchClause(&buf, args, []crud.SearchField{{Column: `name"; DROP TABLE widgets; --`}}, "gear")
		if err == nil {
			t.Fatal("an invalid identifier was accepted")
		}
		if strings.Contains(buf.String(), "DROP TABLE") {
			t.Errorf("the invalid identifier reached the SQL: %s", buf.String())
		}
	})

	t.Run("a pre-existing reserved argument fails rather than being overwritten", func(t *testing.T) {
		var buf strings.Builder
		buf.WriteString("SELECT id FROM widgets WHERE note = @" + searchArg)
		args := pgx.NamedArgs{searchArg: "caller's own value"}

		err := AddSearchClause(&buf, args, fields, "gear")
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("err = %v, want sdk.ErrInvalidInput", err)
		}
		if args[searchArg] != "caller's own value" {
			t.Errorf("the caller's argument was overwritten: %v", args[searchArg])
		}
	})
}

// TestWithSearchDoesNotMutateCaller is the fan-out trap's other half: ListQuery is
// copied by value but its NamedArgs map is shared, so the derivation must clone.
func TestWithSearchDoesNotMutateCaller(t *testing.T) {
	original := ListQuery[struct{}]{
		BaseSQL:      "SELECT id FROM widgets",
		Args:         pgx.NamedArgs{"tenant": "t1"},
		SearchFields: []crud.SearchField{{Column: "name"}},
	}

	derived, err := original.withSearch("gear")
	if err != nil {
		t.Fatalf("withSearch: %v", err)
	}

	if original.BaseSQL != "SELECT id FROM widgets" {
		t.Errorf("the caller's BaseSQL was mutated: %s", original.BaseSQL)
	}
	if _, leaked := original.Args[searchArg]; leaked {
		t.Error("the reserved argument leaked into the caller's args map")
	}
	if !strings.Contains(derived.BaseSQL, "ILIKE @"+searchArg) {
		t.Errorf("the derived query carries no search predicate: %s", derived.BaseSQL)
	}
	if derived.Args[searchArg] != "%gear%" {
		t.Errorf("derived pattern = %v", derived.Args[searchArg])
	}
	if derived.Args["tenant"] != "t1" {
		t.Error("the derived query lost the caller's own argument")
	}
}

// TestWithSearchBlankIsUnchanged pins that a list with no search behaves exactly
// as before — the additive-compatibility promise.
func TestWithSearchBlankIsUnchanged(t *testing.T) {
	original := ListQuery[struct{}]{
		BaseSQL: "SELECT id FROM widgets",
		Args:    pgx.NamedArgs{"tenant": "t1"},
	}
	derived, err := original.withSearch("   ")
	if err != nil {
		t.Fatalf("withSearch(blank): %v", err)
	}
	if derived.BaseSQL != original.BaseSQL {
		t.Errorf("a blank search changed BaseSQL: %s", derived.BaseSQL)
	}
}

// TestWithSearchReachesEveryQueryPath is the fan-out assertion: the predicate
// must be present in the base every downstream path builds from, including the
// COUNT wrap.
func TestWithSearchReachesEveryQueryPath(t *testing.T) {
	q := ListQuery[struct{}]{
		BaseSQL:      "SELECT id FROM widgets",
		SearchFields: []crud.SearchField{{Column: "name"}},
	}
	searched, err := q.withSearch("gear")
	if err != nil {
		t.Fatalf("withSearch: %v", err)
	}

	// The count query wraps BaseSQL, so a searched base is a searched count.
	countSQL := "SELECT COUNT(*) FROM (" + searched.BaseSQL + ") AS list_count"
	if !strings.Contains(countSQL, "ILIKE @"+searchArg) {
		t.Errorf("the COUNT wrap does not carry the search predicate: %s", countSQL)
	}

	// The cursor predicate must append with AND, not a second WHERE.
	var buf strings.Builder
	buf.WriteString(searched.BaseSQL)
	args := cloneArgs(searched.Args)
	if err := ApplyCursorPagination(&buf, args, "created_at", "id", "2026-01-01T00:00:00Z", "abc", crud.DESC, false, false); err != nil {
		t.Fatalf("ApplyCursorPagination: %v", err)
	}
	if strings.Count(strings.ToUpper(buf.String()), " WHERE ") != 1 {
		t.Errorf("search and cursor produced more than one WHERE:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "AND (") {
		t.Errorf("the cursor predicate did not append with AND:\n%s", buf.String())
	}
}
