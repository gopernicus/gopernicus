package crud

import (
	"fmt"
	"strings"
	"time"
)

// Dialect renders the per-database SQL delta. The surface is deliberately
// small — placeholders, identifier quoting, the contains/IN/search
// predicates, and time encoding are the entire enumerable difference between
// supported databases for the CRUD verbs.
type Dialect interface {
	Name() string

	// Placeholder renders the n-th (1-based) positional placeholder.
	Placeholder(n int) string

	// QuoteIdent quotes a validated identifier.
	QuoteIdent(s string) (string, error)

	// Contains renders a substring-match predicate for a column against a
	// placeholder bound to "%term%".
	Contains(col, placeholder string) string

	// In renders an IN predicate. An empty value set renders a
	// constant-false predicate — never `IN ()` (a SQLite syntax error) and
	// never an unrestricted match (a cross-principal leak).
	In(col string, vals []any, args *Args) string

	// ValidateSearch reports whether the dialect supports a search strategy.
	// Called at store construction so mismatches fail before serving traffic.
	ValidateSearch(s SearchSpec) error

	// SearchPredicate renders the declared search strategy for a term.
	SearchPredicate(s SearchSpec, term string, args *Args) (string, error)

	// TimeArg encodes a time.Time query argument (driver-native time for
	// Postgres; canonical RFC3339Nano UTC text for SQLite, where lexical
	// comparison must agree with chronological order).
	TimeArg(t time.Time) any

	// WrapScanDest optionally wraps a scan destination (e.g. SQLite TEXT
	// timestamps into *time.Time). Returns dest unchanged when no wrapping
	// is needed.
	WrapScanDest(dest any) any
}

// renderPred renders one predicate through the dialect.
func renderPred(d Dialect, p Pred, args *Args) (string, error) {
	if p.Raw != nil {
		return p.Raw(d, args)
	}

	col, err := d.QuoteIdent(p.Col)
	if err != nil {
		return "", err
	}

	switch p.Op {
	case OpEq:
		return col + " = " + args.Add(timeAware(d, p.Val)), nil
	case OpNotEq:
		return col + " <> " + args.Add(timeAware(d, p.Val)), nil
	case OpLT:
		return col + " < " + args.Add(timeAware(d, p.Val)), nil
	case OpLTE:
		return col + " <= " + args.Add(timeAware(d, p.Val)), nil
	case OpGT:
		return col + " > " + args.Add(timeAware(d, p.Val)), nil
	case OpGTE:
		return col + " >= " + args.Add(timeAware(d, p.Val)), nil
	case OpIn:
		return d.In(col, anySlice(p.Val), args), nil
	case OpContains:
		term, ok := p.Val.(string)
		if !ok {
			return "", fmt.Errorf("contains predicate on %s: value must be string, got %T", p.Col, p.Val)
		}
		return d.Contains(col, args.Add("%"+term+"%")), nil
	case OpIsNull:
		return col + " IS NULL", nil
	case OpNotNull:
		return col + " IS NOT NULL", nil
	default:
		return "", fmt.Errorf("unknown predicate op %d on %s", p.Op, p.Col)
	}
}

// timeAware routes time.Time values through the dialect's time encoding so
// that lexical comparison on TEXT-stored timestamps agrees with
// chronological order. Every time argument is encoded, not just ranges.
func timeAware(d Dialect, v any) any {
	switch t := v.(type) {
	case time.Time:
		return d.TimeArg(t)
	case *time.Time:
		if t != nil {
			return d.TimeArg(*t)
		}
	}
	return v
}

// anySlice normalizes a typed slice into []any.
func anySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out
	case []int64:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out
	case []int:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out
	case nil:
		return nil
	default:
		return []any{v}
	}
}

// renderContainsAcross ORs a contains predicate across fields — the shared
// body of the portable "contains" search strategy.
//
// Placeholders are single-use: the term is bound once PER FIELD. Postgres
// would tolerate reusing $n, but SQLite's ? placeholders are positional —
// each occurrence consumes an argument — so reuse is itself a dialect
// difference and the portable rule is one Add per occurrence.
func renderContainsAcross(d Dialect, fields []string, term string, args *Args) (string, error) {
	if len(fields) == 0 {
		return "", fmt.Errorf("contains search: no fields declared")
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		col, err := d.QuoteIdent(f)
		if err != nil {
			return "", err
		}
		parts = append(parts, d.Contains(col, args.Add("%"+term+"%")))
	}
	return "(" + strings.Join(parts, " OR ") + ")", nil
}
