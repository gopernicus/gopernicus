// Package sqliteq adapts a moderncdb SQLite connection to the crud.Querier
// surface. It is a leaf package: importing crud alone pulls no driver.
package sqliteq

import (
	"context"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/sqlite/moderncdb"
)

// Querier adapts *moderncdb.DB. moderncdb maps driver errors to its
// sentinels (ErrDuplicateEntry, ErrConstraintFailed); spec MapError funcs
// translate those to domain errors.
type Querier struct {
	db *moderncdb.DB
}

var _ crud.Querier = Querier{}

// New wraps a moderncdb connection.
func New(db *moderncdb.DB) Querier {
	return Querier{db: db}
}

func (q Querier) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	rows, err := q.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (q Querier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := q.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}
