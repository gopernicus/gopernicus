package turso

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// searchEscape is the ESCAPE character the generated LIKE predicate declares.
const searchEscape = `\`

// AddSearchClause wraps the complete SELECT in buf with an outer literal,
// case-insensitive substring predicate. Fields must name projected, unqualified
// text columns. Call before final ORDER BY / LIMIT / OFFSET; existing arguments
// remain in place. Blank terms are a no-op; a nonblank term without searchable
// fields returns sdk.ErrInvalidInput.
func AddSearchClause(buf *strings.Builder, args *[]any, fields []list.SearchField, term string) error {
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

	beginListPredicate(buf)
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
