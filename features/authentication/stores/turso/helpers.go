package turso

import (
	"embed"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner = tursodb.Scanner
