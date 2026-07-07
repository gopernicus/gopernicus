package turso

import (
	"embed"
	"time"
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

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

// formatTS renders t in the fixed-width UTC layout used for storage.
func formatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
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
