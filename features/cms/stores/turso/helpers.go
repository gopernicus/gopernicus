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
// fractional digits, always "Z") keeps created_at lexicographically ordered as
// TEXT, which keyset pagination relies on.
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

// orderField is the keyset order column; must match the cursor's order field.
const orderField = "created_at"

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

// nullableTS formats a *time.Time for storage, or nil for NULL.
func nullableTS(t *time.Time) any {
	if t == nil {
		return nil
	}
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
