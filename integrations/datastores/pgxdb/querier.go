package pgxdb

import (
	"context"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the query surface common to *DB and *Tx: Exec/Query/QueryRow.
// App repositories should accept this interface rather than a concrete *DB or
// *Tx, so the same store code runs unchanged against a pool for standalone
// operations or a transaction for composed, atomic workflows.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, query string, args ...any) (jackpgx.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) jackpgx.Row
}

// Scanner abstracts pgx.Row and pgx.Rows for shared scan helpers, so a store
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
