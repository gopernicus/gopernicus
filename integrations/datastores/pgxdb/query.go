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

// Collect runs a parent-bounded, unpaginated query and scans every row into T
// via pgx.RowToStructByName (STRICT — never the Lax variant), mapping both the
// query error and the collect error through MapError so the port's error
// semantics survive. No rows is not an error: the result is an empty, non-nil
// []T, so a caller that marshals it says [] and never null.
//
// It is the read for every child of one aggregate — the caller bounds the query
// by its parent key. It is NOT a paging primitive: a query whose result set the
// parent does not bound belongs on List/ListQuery, which owns limits, ordering,
// and cursors. db may be a *DB pool or a *Tx.
func Collect[T any](ctx context.Context, db Querier, sql string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, MapError(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, MapError(err)
	}
	return items, nil
}
