package pgx

import (
	"embed"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// orderField is the keyset order column for the paginated auth ports; it must
// match the cursor's order field (design §9 — ORDER BY created_at DESC, id DESC).
const orderField = "created_at"

// scanner abstracts pgx.Row and pgx.Rows for shared scan helpers.
type scanner = pgxdb.Scanner
