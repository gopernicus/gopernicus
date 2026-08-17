package turso

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// searchEscape is the ESCAPE character the generated LIKE predicate declares.
const searchEscape = `\`

// AddSearchClause appends a case-insensitive LITERAL-substring predicate over
// fields and appends ONE positional argument — the bound pattern — to args. It
// writes WHERE when buf holds no WHERE clause yet and AND otherwise, matching
// appendCursorPredicate so the two compose in either order.
//
// It is the positional-argument twin of the pgx AddSearchClause, with one
// deliberate dialect difference: **SQLite has no ILIKE**. Its `LIKE` is already
// case-insensitive for ASCII by default and leaves non-ASCII code points alone —
// which is exactly the fold crud.MatchesSearch performs and exactly what
// PostgreSQL's `ILIKE ... COLLATE "C"` performs. The three agree; the SQL keyword
// differs, and that difference belongs here in the store rather than in a shared
// abstraction that would have to pretend they are the same word.
//
// The generated predicate is:
//
//	("col_a" LIKE ? ESCAPE '\' OR "col_b" LIKE ? ESCAPE '\')
//
// Note the pattern is appended ONCE PER FIELD, because positional placeholders
// cannot be reused the way a named argument can. Preserving BaseSQL's own
// argument order is the caller's contract and is respected: the search arguments
// go on the end, before any cursor/limit/offset arguments a later step appends.
//
// A blank (or whitespace-only) term is a no-op. A NON-blank term with no fields
// is sdk.ErrInvalidInput — a list that declares nothing searchable must not
// answer a search with an unfiltered page.
func AddSearchClause(buf *strings.Builder, args *[]any, fields []crud.SearchField, term string) error {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil
	}
	if len(fields) == 0 {
		return fmt.Errorf("search is not supported by this list: %w", sdk.ErrInvalidInput)
	}

	predicates := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted, err := QuoteIdentifier(f.Column)
		if err != nil {
			return fmt.Errorf("search field: %w", err)
		}
		predicates = append(predicates, fmt.Sprintf(`%s LIKE ? ESCAPE '%s'`, quoted, searchEscape))
	}

	if strings.Contains(strings.ToUpper(buf.String()), "WHERE") {
		buf.WriteString(" AND ")
	} else {
		buf.WriteString(" WHERE ")
	}
	buf.WriteString("(")
	buf.WriteString(strings.Join(predicates, " OR "))
	buf.WriteString(")")

	pattern := "%" + EscapeSearchTerm(term) + "%"
	for range fields {
		*args = append(*args, pattern)
	}
	return nil
}

// EscapeSearchTerm turns a human-typed term into a LIKE pattern fragment that
// matches literally: `\` → `\\`, `%` → `\%`, `_` → `\_`. It is byte-identical to
// the pgx connector's function of the same name — the two dialects must agree on
// what a term MEANS even where they disagree on the keyword that applies it.
//
// A single-pass Replacer is used deliberately: each input byte is consumed and
// replaced exactly once, so an inserted backslash is never itself re-escaped.
func EscapeSearchTerm(term string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(term)
}
