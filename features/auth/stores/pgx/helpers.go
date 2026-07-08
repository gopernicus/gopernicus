package pgx

import (
	"embed"
	"time"
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
type scanner interface {
	Scan(dest ...any) error
}

// nullableTime maps a possibly-zero timestamp to a nullable TIMESTAMPTZ arg: a
// zero time stores as NULL (never expires / not revoked / not set), any other
// value as its UTC instant.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// fromNullableTime maps a scanned nullable timestamp back to the domain's
// zero-value "not set" sentinel: NULL reads back as the zero time.
func fromNullableTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.UTC()
}
