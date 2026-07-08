package pgx

import (
	"embed"
	"strconv"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
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
type scanner = pgxdb.Scanner

// placeholder returns the postgres positional parameter marker $n. Statements
// with a dynamic WHERE clause number their placeholders from len(args)+1.
func placeholder(n int) string {
	return "$" + strconv.Itoa(n)
}
