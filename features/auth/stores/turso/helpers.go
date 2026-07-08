package turso

import (
	"database/sql"
	"embed"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// tsLayout is a fixed-width UTC timestamp layout. Fixed width (always 9
// fractional digits, always "Z") keeps stored timestamps lexicographically
// ordered as TEXT — the ecosystem timestamp-storage convention. time.RFC3339Nano
// trims trailing fractional zeros and would break ordering.
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

// orderField is the keyset order column for the paginated auth ports; it must
// match the cursor's order field (design §9 — ORDER BY created_at DESC, id DESC).
const orderField = "created_at"

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner = tursodb.Scanner

// formatTS renders t in the fixed-width UTC layout used for storage.
func formatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
}

// nullableTS renders a possibly-zero timestamp for storage: a zero time stores as
// NULL (never expires / not set), any other value as a fixed-width TEXT timestamp.
func nullableTS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTS(t)
}

// parseNullTime parses a nullable stored timestamp: a NULL or empty column reads
// back as the zero time (the domain's "not set" sentinel).
func parseNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return parseTime(ns.String)
}

// parseTime parses a stored timestamp, tolerating both the fixed-width layout
// and RFC3339 variants.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(tsLayout, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// boolToInt maps a Go bool to the 0/1 INTEGER stored in SQLite/libSQL.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
