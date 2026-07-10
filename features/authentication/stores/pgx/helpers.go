package pgx

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// queryOne runs a single-row query with NamedArgs and scans it into a db-tagged
// row struct via pgx.RowToStructByName. A no-rows result maps to
// sdk.ErrNotFound (and every other driver error to its sentinel) through
// MapError, so single-row reads keep the port's error semantics.
func queryOne[T any](ctx context.Context, db pgxdb.Querier, sql string, args pgx.NamedArgs) (T, error) {
	var zero T
	rows, err := db.Query(ctx, sql, args)
	if err != nil {
		return zero, pgxdb.MapError(err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
	if err != nil {
		return zero, pgxdb.MapError(err)
	}
	return row, nil
}
