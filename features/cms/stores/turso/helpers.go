package turso

import (
	"context"
	"embed"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// queryOne runs a single-row query and scans it into a db-tagged row struct via
// tursodb.ScanStruct — the pgx-store queryOne discipline over turso. It routes the
// read through Query (not QueryRow) so the row carries Columns for the strict
// struct scan, then steps exactly one row. A no-rows result maps to
// sdk.ErrNotFound and every driver error to its sentinel through MapError, so
// single-row reads keep the port's error semantics.
func queryOne[T any](ctx context.Context, db tursodb.Querier, query string, args ...any) (T, error) {
	var zero T
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return zero, tursodb.MapError(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, tursodb.MapError(err)
		}
		return zero, sdk.ErrNotFound
	}
	return tursodb.ScanStruct[T](rows)
}
