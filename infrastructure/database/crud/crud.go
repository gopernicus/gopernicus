// Package crud is the runtime half of gopernicus data-layer generation: a
// generic, dialect-aware store that owns the mechanical CRUD verbs (List with
// keyset pagination, Get, Create, Update, Delete, and record-state
// transitions) so that generated code shrinks to a declarative Spec per
// entity instead of an imperative store body.
//
// The package speaks ONLY the verbs the generator emits. It is not a query
// builder or an ORM: anything beyond the closed predicate vocabulary is
// hand-written SQL on the same store (see Spec escape hatches), and complex
// stores (recursive CTEs, job-queue claiming) stay fully hand-written.
//
// Drivers are isolated in leaf subpackages (pgxq, sqliteq) so importing crud
// pulls no database dependencies.
package crud

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoFieldsToUpdate is returned by Update when the update struct resolves
// to zero SET clauses.
var ErrNoFieldsToUpdate = errors.New("no fields to update")

// ErrNotFound is returned when a row does not exist. Specs typically map it
// to a domain sentinel via MapError.
var ErrNotFound = errors.New("record not found")

// Rows is the minimal row iterator the store needs. Satisfied by adapters
// over pgx.Rows and *sql.Rows.
type Rows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Querier is the execution surface the store needs; the package defines what
// it consumes rather than importing a driver's interface. Args are
// positional — the Dialect renders matching placeholders. Implementations
// map driver errors to the shared infrastructure sentinels.
type Querier interface {
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	Exec(ctx context.Context, query string, args ...any) (rowsAffected int64, err error)
}

// Args accumulates positional query arguments and hands out the matching
// dialect placeholder for each.
type Args struct {
	dialect Dialect
	vals    []any
}

// NewArgs creates an argument accumulator for the given dialect.
func NewArgs(d Dialect) *Args {
	return &Args{dialect: d}
}

// Add appends a value and returns its placeholder (e.g. "$3" or "?").
func (a *Args) Add(v any) string {
	a.vals = append(a.vals, v)
	return a.dialect.Placeholder(len(a.vals))
}

// Values returns the accumulated positional arguments.
func (a *Args) Values() []any {
	return a.vals
}

// validIdent reports whether s is a safe SQL identifier (letters, digits,
// underscores, starting with a letter or underscore). Identifiers in specs
// are developer-supplied, but this guards against injection through
// dynamically chosen order fields.
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func quoteIdent(s string) (string, error) {
	if !validIdent(s) {
		return "", fmt.Errorf("invalid identifier %q", s)
	}
	return `"` + s + `"`, nil
}
