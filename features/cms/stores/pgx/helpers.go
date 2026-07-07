package pgx

import (
	"embed"
	"strconv"
	"time"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// orderField is the keyset order column; must match the cursor's order field.
const orderField = "created_at"

// scanner abstracts pgx.Row and pgx.Rows for shared scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

// placeholder returns the postgres positional parameter marker $n. Statements
// with a dynamic WHERE clause number their placeholders from len(args)+1.
func placeholder(n int) string {
	return "$" + strconv.Itoa(n)
}

// publishedAt normalizes a nullable timestamp for storage: nil stays NULL, a
// value is UTC-normalized (TIMESTAMPTZ stores the instant regardless, but this
// keeps the wire value consistent with the store's UTC convention).
func publishedAt(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
