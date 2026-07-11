package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk"
)

// QueryOne runs a single-row query and scans it into a db-tagged row struct via
// ScanStruct — the pgxdb.QueryOne discipline over turso. It routes the read
// through Query (not QueryRow) so the row carries Columns for the strict struct
// scan, then steps exactly one row. A no-rows result maps to sdk.ErrNotFound
// and every driver error to its sentinel through MapError, so single-row reads
// keep the port's error semantics. It is for multi-column struct reads only: a
// one-column read (e.g. INSERT ... RETURNING id) stays on QueryRow(...).Scan.
// db may be a *DB pool or a *Tx.
func QueryOne[T any](ctx context.Context, db Querier, query string, args ...any) (T, error) {
	var zero T
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return zero, MapError(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, MapError(err)
		}
		return zero, sdk.ErrNotFound
	}
	return ScanStruct[T](rows)
}
