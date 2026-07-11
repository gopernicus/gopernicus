package pgxdb

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// QueryOne runs a single-row query with NamedArgs and scans it into a db-tagged
// row struct via pgx.RowToStructByName (STRICT — never the Lax variant). A
// no-rows result maps to sdk.ErrNotFound (and every other driver error to its
// sentinel) through MapError, so single-row reads keep the port's error
// semantics. It is for multi-column struct reads only: a one-column read (e.g.
// INSERT ... RETURNING id) stays on QueryRow(...).Scan. db may be a *DB pool or
// a *Tx.
func QueryOne[T any](ctx context.Context, db Querier, sql string, args pgx.NamedArgs) (T, error) {
	var zero T
	rows, err := db.Query(ctx, sql, args)
	if err != nil {
		return zero, MapError(err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
	if err != nil {
		return zero, MapError(err)
	}
	return row, nil
}
