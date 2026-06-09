// Package pgxq adapts a pgxdb.Querier (pool or tx) to the crud.Querier
// surface. It is a leaf package: importing crud alone pulls no driver.
package pgxq

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
)

// Querier adapts pgxdb.Querier. Driver errors are mapped through
// pgxdb.HandlePgError to the shared infrastructure sentinels; spec MapError
// funcs translate those to domain errors.
type Querier struct {
	q pgxdb.Querier
}

var _ crud.Querier = Querier{}

// New wraps a pgx pool or transaction.
func New(q pgxdb.Querier) Querier {
	return Querier{q: q}
}

func (q Querier) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	rows, err := q.q.Query(ctx, query, args...)
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	return pgxRows{rows: rows}, nil
}

func (q Querier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := q.q.Exec(ctx, query, args...)
	if err != nil {
		return 0, pgxdb.HandlePgError(err)
	}
	return tag.RowsAffected(), nil
}

// pgxRows adapts pgx.Rows to crud.Rows (column names from field
// descriptions; Close without an error return).
type pgxRows struct {
	rows pgx.Rows
}

func (r pgxRows) Columns() ([]string, error) {
	fields := r.rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.Name
	}
	return cols, nil
}

func (r pgxRows) Next() bool { return r.rows.Next() }

func (r pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }

func (r pgxRows) Err() error { return pgxdb.HandlePgError(r.rows.Err()) }

func (r pgxRows) Close() error {
	r.rows.Close()
	return nil
}
