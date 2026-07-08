package pgxdb

import "context"

// ExecAffecting runs a write and reports how many rows it changed, normalizing
// the driver's rows-affected surface to (int64, error). It owns only that
// mechanical normalization: the port semantic — mapping zero to errs.ErrNotFound
// for an expects-one write, or comparing to 1 for a compare-and-set — stays with
// the calling adapter, as does any retry wrapping. db may be a *DB pool or a *Tx.
func ExecAffecting(ctx context.Context, db Querier, query string, args ...any) (int64, error) {
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
