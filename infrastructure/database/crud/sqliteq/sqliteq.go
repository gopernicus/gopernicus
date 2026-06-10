// Package sqliteq adapts a moderncdb SQLite connection to the crud.Querier
// surface. It is a leaf package: importing crud alone pulls no driver.
package sqliteq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/sqlite/moderncdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Conn is the execution surface this adapter needs — satisfied by both
// *moderncdb.DB and *moderncdb.Tx, so a store can run inside or outside a
// transaction without a second constructor.
type Conn interface {
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var (
	_ Conn = (*moderncdb.DB)(nil)
	_ Conn = (*moderncdb.Tx)(nil)
)

// Querier adapts a moderncdb connection or transaction. moderncdb maps
// driver errors to its sentinels (ErrDuplicateEntry, ErrConstraintFailed);
// spec MapError funcs translate those to domain errors.
type Querier struct {
	db Conn
}

var _ crud.Querier = Querier{}

// New wraps a moderncdb connection or transaction.
func New(db Conn) Querier {
	return Querier{db: db}
}

func (q Querier) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	rows, err := q.db.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	return rows, nil
}

func (q Querier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := q.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, mapError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// mapError translates moderncdb sentinels into the sdk/errs taxonomy — the
// dialect-neutral contract spec MapError funcs are written against. The
// original error stays in the wrap chain. FK is checked before the generic
// constraint sentinel it wraps.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, moderncdb.ErrDuplicateEntry):
		return fmt.Errorf("%w: %w", errs.ErrAlreadyExists, err)
	case errors.Is(err, moderncdb.ErrForeignKeyViolation):
		return fmt.Errorf("%w: %w", errs.ErrInvalidReference, err)
	case errors.Is(err, moderncdb.ErrConstraintFailed):
		return fmt.Errorf("%w: %w", errs.ErrInvalidInput, err)
	default:
		return err
	}
}
