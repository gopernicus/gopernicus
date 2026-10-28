package pgx

import (
	"embed"
	"fmt"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// errIDRequired rejects an empty job or schedule id: the jobs services mint ids
// and the store never does, so an empty-id generator fails loudly instead of
// turning the next insert into a false sdk.ErrAlreadyExists.
var errIDRequired = fmt.Errorf("jobs pgx: id required; the jobs service mints it: %w", sdk.ErrInvalidInput)

// scanner abstracts pgx.Row and pgx.Rows for the shared Claim scan.
type scanner = pgxdb.Scanner

// payloadValue returns a non-empty JSON text for storage: the raw payload, or
// "{}" when it is empty (the column is NOT NULL). It is stored into a JSON (not
// JSONB) column, so these exact bytes round-trip verbatim.
func payloadValue(p []byte) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}
