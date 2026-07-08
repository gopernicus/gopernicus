// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
// The shared storetest.Run suite is the executable form of the
// outbox.EntryRepository contract; the store-specific AppendTx is proved
// separately in appender_test.go (it takes a *pgxdb.Tx the dialect-blind suite
// cannot). This mirrors the sibling pgx stores' plain env-gating (no build tag).
package pgx

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
	"github.com/gopernicus/gopernicus/features/events/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// outboxTables are the feature's tables cleared before each newRepo call so every
// leaf subtest starts from a clean, isolated store.
var outboxTables = []string{"event_outbox"}

// TestConformance runs the shared outbox conformance suite against a live
// PostgreSQL database. Each newRepo call opens a connection, applies the canonical
// migrations, truncates the outbox table, and constructs the Store via New
// (exercising the boot-time table probe on every run).
func TestConformance(t *testing.T) {
	dsn := requireDSN(t)

	storetest.Run(t, func(t *testing.T) outbox.EntryRepository {
		db := openAndMigrate(t, dsn)
		store, err := New(db)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return store
	})
}

// requireDSN returns the live connection DSN or skips loudly.
func requireDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	return dsn
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates the outbox table so the returned store starts empty and isolated.
func openAndMigrate(t *testing.T, dsn string) *pgxdb.DB {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every outbox table so a store starts empty.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	q := "TRUNCATE " + strings.Join(outboxTables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
