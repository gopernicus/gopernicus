package turso

import (
	"context"
	"database/sql"
)

// Querier is the query surface common to *DB and *Tx: Exec/Query/QueryRow.
// App repositories should accept this interface rather than a concrete *DB or
// *Tx, so the same store code runs unchanged against a connection for standalone
// operations or a transaction for composed, atomic workflows.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
}

// Scanner abstracts *sql.Row and *sql.Rows for shared scan helpers, so a store
// can scan a single-row QueryRow result and a Rows element through the same
// function.
type Scanner interface {
	Scan(dest ...any) error
}

// Compile-time assertions that *DB and *Tx satisfy Querier.
var (
	_ Querier = (*DB)(nil)
	_ Querier = (*Tx)(nil)
)
