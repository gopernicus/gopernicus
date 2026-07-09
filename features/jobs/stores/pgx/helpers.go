package pgx

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"

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

// scanner abstracts pgx.Row and pgx.Rows for the shared Claim scan.
type scanner = pgxdb.Scanner

// queryOne runs a single-row query with NamedArgs and scans it into a db-tagged
// row struct via pgx.RowToStructByName. A no-rows result maps to errs.ErrNotFound
// (and every other driver error to its sentinel) through MapError, so single-row
// reads keep the port's error semantics.
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

// payloadValue returns a non-empty JSON text for storage: the raw payload, or
// "{}" when it is empty (the column is NOT NULL). It is stored into a JSON (not
// JSONB) column, so these exact bytes round-trip verbatim.
func payloadValue(p []byte) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

// newID returns a random, collision-free identifier with the given prefix.
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
