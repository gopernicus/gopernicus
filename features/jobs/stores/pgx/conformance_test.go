// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
// The ConcurrentClaim case is the load-bearing one: FOR UPDATE SKIP LOCKED must
// make N workers each claim a distinct job with no contention and no double-claim.
package pgx

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/features/jobs/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// jobTables are the feature's tables cleared before each newRepo call so every
// leaf subtest starts from a clean, isolated store.
var jobTables = []string{"job_queue", "job_schedules", "fenced_job_queue"}

// TestConformance_Queue runs the shared queue conformance suite against a live
// PostgreSQL database. Each newRepo call opens a connection, applies the canonical
// migrations via the connector's RunMigrations, truncates the jobs tables, and
// constructs the Queue with storetest.Lease so the lease-expiry case is honored
// with real wall-clock time.
func TestConformance_Queue(t *testing.T) {
	dsn := requireDSN(t)

	storetest.RunQueue(t, func(t *testing.T) job.QueueRepository {
		db := openAndMigrate(t, dsn)
		return NewQueueStore(db, WithLease(storetest.Lease))
	})
}

// TestConformance_FencedQueue runs the shared fenced/keyed/checkpointed queue
// conformance suite (job.FencedQueueRepository) against a live PostgreSQL
// database. Each newRepo call opens a connection, applies the canonical
// migrations (including 0003_fenced_job_queue), truncates the jobs tables, and
// constructs the FencedQueue. The lease is per-claim (the suite passes
// storetest.Lease to Claim), so the store takes no lease option; the
// stale-claim/reclaim, checkpoint-crash, and byte-exact non-UTF8 payload cases
// run with real wall-clock time and a byte-exact BYTEA column.
func TestConformance_FencedQueue(t *testing.T) {
	dsn := requireDSN(t)

	storetest.RunFencedQueue(t, func(t *testing.T) job.FencedQueueRepository {
		db := openAndMigrate(t, dsn)
		return NewFencedQueueStore(db)
	})
}

// TestConformance_Schedules runs the shared schedule conformance suite against a
// live PostgreSQL database, cleaning the tables per newRepo call.
func TestConformance_Schedules(t *testing.T) {
	dsn := requireDSN(t)

	storetest.RunSchedules(t, func(t *testing.T) schedule.Repository {
		db := openAndMigrate(t, dsn)
		return NewScheduleStore(db)
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
// truncates the jobs tables so the returned store starts empty and isolated.
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

// truncate clears every jobs table so a store starts empty.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	q := "TRUNCATE " + strings.Join(jobTables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
