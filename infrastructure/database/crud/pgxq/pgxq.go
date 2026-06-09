// Package pgxq adapts a pgxdb.Querier (pool or tx) to the crud.Querier
// surface. It is a leaf package: importing crud alone pulls no driver.
package pgxq

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Querier adapts pgxdb.Querier. Driver errors map through pgxdb.HandlePgError
// and then into the sdk/errs taxonomy — the dialect-neutral contract spec
// MapError funcs are written against. The original error stays in the wrap
// chain for debugging.
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
		return nil, mapError(err)
	}
	return pgxRows{rows: rows}, nil
}

func (q Querier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := q.q.Exec(ctx, query, args...)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
}

// mapError translates pgx driver errors into the sdk/errs taxonomy.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	err = pgxdb.HandlePgError(err)
	switch {
	case errors.Is(err, pgxdb.ErrDBDuplicatedEntry):
		return fmt.Errorf("%w: %w", errs.ErrAlreadyExists, err)
	case errors.Is(err, pgxdb.ErrDBForeignKeyViolation):
		return fmt.Errorf("%w: %w", errs.ErrInvalidReference, err)
	case errors.Is(err, pgxdb.ErrDBCheckViolation), errors.Is(err, pgxdb.ErrDBNotNullViolation):
		return fmt.Errorf("%w: %w", errs.ErrInvalidInput, err)
	default:
		return err
	}
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

func (r pgxRows) Err() error { return mapError(r.rows.Err()) }

func (r pgxRows) Close() error {
	r.rows.Close()
	return nil
}
