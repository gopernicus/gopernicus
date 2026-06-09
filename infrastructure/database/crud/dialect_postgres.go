package crud

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PostgresDialect renders Postgres SQL: $n placeholders, ILIKE contains,
// = ANY(...) array membership, and tsquery search strategies.
type PostgresDialect struct{}

var _ Dialect = PostgresDialect{}

func (PostgresDialect) Name() string { return "postgres" }

func (PostgresDialect) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

func (PostgresDialect) QuoteIdent(s string) (string, error) { return quoteIdent(s) }

func (PostgresDialect) Contains(col, placeholder string) string {
	return col + " ILIKE " + placeholder
}

func (PostgresDialect) In(col string, vals []any, args *Args) string {
	// = ANY with a single array argument: handles the empty set as a no-match
	// without special-casing, and keeps the statement shape stable for the
	// prepared-statement cache regardless of set size.
	return col + " = ANY(" + args.Add(vals) + ")"
}

func (PostgresDialect) ValidateSearch(s SearchSpec) error {
	switch s.Strategy {
	case SearchContains, SearchWebSearch, SearchTSVector:
		return nil
	default:
		return fmt.Errorf("postgres dialect: unknown search strategy %q", s.Strategy)
	}
}

func (d PostgresDialect) SearchPredicate(s SearchSpec, term string, args *Args) (string, error) {
	switch s.Strategy {
	case SearchContains:
		return renderContainsAcross(d, s.Fields, term, args)
	case SearchWebSearch:
		if len(s.Fields) == 0 {
			return "", fmt.Errorf("web_search strategy: no fields declared")
		}
		parts := make([]string, 0, len(s.Fields))
		for _, f := range s.Fields {
			col, err := d.QuoteIdent(f)
			if err != nil {
				return "", err
			}
			parts = append(parts, "coalesce("+col+", '')")
		}
		// Placeholders are single-use across dialects: bind the config once
		// per occurrence rather than reusing $n.
		return "to_tsvector(" + args.Add(searchConfig(s)) + "::regconfig, " + strings.Join(parts, " || ' ' || ") +
			") @@ websearch_to_tsquery(" + args.Add(searchConfig(s)) + "::regconfig, " + args.Add(term) + ")", nil
	case SearchTSVector:
		col, err := d.QuoteIdent(s.Column)
		if err != nil {
			return "", err
		}
		return col + " @@ websearch_to_tsquery(" + args.Add(searchConfig(s)) + "::regconfig, " + args.Add(term) + ")", nil
	default:
		return "", fmt.Errorf("postgres dialect: unknown search strategy %q", s.Strategy)
	}
}

func (PostgresDialect) TimeArg(t time.Time) any { return t }

func (PostgresDialect) WrapScanDest(dest any) any { return dest }

func searchConfig(s SearchSpec) string {
	if s.Config != "" {
		return s.Config
	}
	return "english"
}
