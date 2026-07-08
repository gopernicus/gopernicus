//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// They require TURSO_DATABASE_URL and TURSO_AUTH_TOKEN in the environment (or a
// .env loaded by the caller). Absent those, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified. The shared
// storetest.Run suite is the executable form of the outbox.EntryRepository
// contract; the store-specific AppendTx is proved separately in appender_test.go
// (it takes a *tursodb.Tx the dialect-blind suite cannot).
package turso

import (
	"context"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
	"github.com/gopernicus/gopernicus/features/events/storetest"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// outboxTables are the feature's tables cleared before each newRepo call so every
// leaf subtest starts from a clean, isolated store.
var outboxTables = []string{"event_outbox"}

// TestConformance runs the shared outbox conformance suite against a live
// Turso/libSQL database. Each newRepo call opens a connection, applies the
// canonical migrations, truncates the outbox table, and constructs the Store via
// New (exercising the boot-time table probe on every run).
func TestConformance(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.Run(t, func(t *testing.T) outbox.EntryRepository {
		db := openAndMigrate(t, url, token)
		store, err := New(db)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return store
	})
}

// requireTursoEnv returns the live connection env or skips loudly.
func requireTursoEnv(t *testing.T) (url, token string) {
	t.Helper()
	url = os.Getenv("TURSO_DATABASE_URL")
	token = os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified")
	}
	return url, token
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates the outbox table so the returned store starts empty and isolated.
func openAndMigrate(t *testing.T, url, token string) *tursodb.DB {
	t.Helper()
	db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: token})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every outbox table so a store starts empty.
func truncate(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range outboxTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}
