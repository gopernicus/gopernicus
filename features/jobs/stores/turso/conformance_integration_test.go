//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// They require TURSO_DATABASE_URL and TURSO_AUTH_TOKEN in the environment (or a
// .env loaded by the caller). Absent those, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified. The ConcurrentClaim
// case is the load-bearing one: it proves SQLITE_BUSY / Turso remote write
// serialization surfaces as adapter-internal waiting, never a failed claim.
package turso

import (
	"context"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/logic/job"
	"github.com/gopernicus/gopernicus/features/jobs/logic/schedule"
	"github.com/gopernicus/gopernicus/features/jobs/storetest"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// jobTables are the feature's tables cleared before each newRepo call so every
// leaf subtest starts from a clean, isolated store.
var jobTables = []string{"job_queue", "job_schedules"}

// TestConformance_Queue runs the shared queue conformance suite against a live
// Turso/libSQL database. Each newRepo call opens a connection, applies the
// canonical migrations, truncates the jobs tables, and constructs the Queue with
// storetest.Lease so the lease-expiry case is honored with real wall-clock time.
func TestConformance_Queue(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.RunQueue(t, func(t *testing.T) job.QueueRepository {
		db := openAndMigrate(t, url, token)
		return NewQueueStore(db, WithLease(storetest.Lease))
	})
}

// TestConformance_Schedules runs the shared schedule conformance suite against a
// live Turso/libSQL database, cleaning the tables per newRepo call.
func TestConformance_Schedules(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.RunSchedules(t, func(t *testing.T) schedule.Repository {
		db := openAndMigrate(t, url, token)
		return NewScheduleStore(db)
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
// truncates the jobs tables so the returned store starts empty and isolated.
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

// truncate clears every jobs table so a store starts empty.
func truncate(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range jobTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}
