package pgxdb

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
)

// TestLive_OpenAndMigrate is the one env-gated live test: it opens a real
// connection (Open pings), then runs a migrate-apply round-trip and confirms
// the ledger recorded the host-owned migration stream. It skips loudly when
// POSTGRES_TEST_DSN is unset — a silent green here would be a false green.
//
// Spin a local database with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
func TestLive_OpenAndMigrate(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}

	ctx := context.Background()

	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Keep the run repeatable: drop the ledger and the fixture table first.
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS pg_connector_live_check"); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM schema_migrations WHERE source = 'default' AND version = '0001_init.sql'"); err != nil {
		// The ledger may not exist yet on a clean database; that is fine.
		t.Logf("clean ledger (ignored on first run): %v", err)
	}

	fsys := fstest.MapFS{
		"migrations/0001_init.sql": {Data: []byte("CREATE TABLE pg_connector_live_check (id TEXT PRIMARY KEY);")},
	}

	if err := RunMigrations(ctx, db, fsys, "migrations"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Ledger recorded exactly one row for the host-owned stream.
	var n int
	if err := db.QueryRow(ctx,
		"SELECT count(*) FROM schema_migrations WHERE source = $1", defaultMigrationSource).Scan(&n); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("ledger rows for %q = %d, want 1", defaultMigrationSource, n)
	}

	// The migrated table exists (to_regclass returns non-NULL).
	var regclass *string
	if err := db.QueryRow(ctx,
		"SELECT to_regclass('pg_connector_live_check')").Scan(&regclass); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if regclass == nil {
		t.Fatal("migrated table pg_connector_live_check does not exist")
	}

	// Re-apply is a no-op (checksum guard passes, no error, no new row).
	if err := RunMigrations(ctx, db, fsys, "migrations"); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
