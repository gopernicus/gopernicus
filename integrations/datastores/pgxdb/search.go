package pgxdb

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// searchArg is the connector-reserved named argument AddSearchClause binds. It is
// reserved: a caller's BaseSQL must not already use it, and AddSearchClause fails
// rather than overwriting one, because a silently clobbered argument would change
// what the caller's own predicate matched.
const searchArg = "list_search"

// searchEscape is the ESCAPE character the generated LIKE/ILIKE predicate
// declares. Backslash is the SQL default, but declaring it explicitly means the
// predicate does not depend on the server's standard_conforming_strings setting.
const searchEscape = `\`

// AddSearchClause wraps the complete SELECT in buf with an outer literal,
// case-insensitive substring predicate. Fields must name projected, unqualified
// text columns. Call before final ORDER BY / LIMIT / OFFSET; existing arguments
// remain in place. Blank terms are a no-op; a nonblank term without searchable
// fields returns sdk.ErrInvalidInput.
//
// The generated predicate is:
//
//	(("col_a" COLLATE "C") ILIKE @list_search ESCAPE '\' OR ("col_b" COLLATE "C") ILIKE @list_search ESCAPE '\')
//
// with @list_search bound to "%" + escape(term) + "%".
//
// Three details are load-bearing:
//
//   - **EscapeSearchTerm makes the term literal.** Without it, a person typing
//     `100%` matches every row and a person typing `a_c` matches `abc`. The v1
//     generator built `"%" + term + "%"` with no escaping at all; that defect is
//     not restored.
//   - **COLLATE "C" pins the fold.** ILIKE under a non-deterministic collation is
//     an error, and under a locale collation its folding would diverge from
//     SQLite's LIKE and from list.MatchesSearch. The C collation gives the
//     ASCII-only fold all three agree on.
//   - **Columns are quoted through QuoteIdentifier**, so a column name that is not
//     a valid identifier is an error rather than an injection point.
//
// A blank (or whitespace-only) term is a no-op: it appends nothing and binds
// nothing. A NON-blank term with no fields is sdk.ErrInvalidInput — a list that
// declares nothing searchable must not answer a search with an unfiltered page
// that looks like a result.
func AddSearchClause(buf *strings.Builder, args pgx.NamedArgs, fields []list.SearchField, term string) error {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil
	}
	if len(fields) == 0 {
		return fmt.Errorf("search is not supported by this list: %w", sdk.ErrInvalidInput)
	}
	if _, exists := args[searchArg]; exists {
		return fmt.Errorf("named argument %q is reserved by the connector's search clause: %w", searchArg, sdk.ErrInvalidInput)
	}

	predicates := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted, err := QuoteIdentifier(f.Column)
		if err != nil {
			return fmt.Errorf("search field: %w", err)
		}
		predicates = append(predicates,
			fmt.Sprintf(`(%s COLLATE "C") ILIKE @%s ESCAPE '%s'`, quoted, searchArg, searchEscape))
	}

	beginListPredicate(buf)
	buf.WriteString("(" + strings.Join(predicates, " OR ") + ")")

	args[searchArg] = "%" + EscapeSearchTerm(term) + "%"
	return nil
}

// EscapeSearchTerm turns a human-typed term into a LIKE pattern fragment that
// matches literally: `\` → `\\`, `%` → `\%`, `_` → `\_`.
//
// It uses a single-pass Replacer deliberately. Each input byte is consumed and
// replaced exactly once, so an inserted backslash is never itself re-escaped. The
// naive alternative — three sequential strings.ReplaceAll calls — WOULD have that
// bug unless the backslash pass ran first, which is the kind of ordering
// dependency worth designing away rather than commenting on.
func EscapeSearchTerm(term string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(term)
}
