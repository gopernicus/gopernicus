// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
// The ConcurrentClaim case is the load-bearing one: FOR UPDATE SKIP LOCKED must
// make N workers each claim a distinct job with no contention and no double-claim.
//
// POSTGRES_TEST_SCHEMA is optional. Set it and the whole suite runs in that
// schema — migrated with pgxdb.WithSchema, constructed with WithSchema, and
// truncated by qualified name — which is the behavioral proof that every
// executed statement is qualified. Unset, the run is identical to the default
// one it has always been.
package pgx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/jobs/domain/job"
	"github.com/gopernicus/gopernicus/pockets/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/pockets/jobs/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
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
		db, opts := openAndMigrate(t, dsn)
		return NewQueueStore(db, append(opts, WithLease(storetest.Lease))...)
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
		db, opts := openAndMigrate(t, dsn)
		return NewFencedQueueStore(db, opts...)
	})
}

// TestConformance_Schedules runs the shared schedule conformance suite against a
// live PostgreSQL database, cleaning the tables per newRepo call.
func TestConformance_Schedules(t *testing.T) {
	dsn := requireDSN(t)

	storetest.RunSchedules(t, func(t *testing.T) schedule.Repository {
		db, opts := openAndMigrate(t, dsn)
		return NewScheduleStore(db, opts...)
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

// testSchema returns the optional schema the whole live suite runs in, read from
// POSTGRES_TEST_SCHEMA. The zero Schema (env unset) is the default leg: bare
// names, byte-identical to the run this suite has always made.
func testSchema(t *testing.T) pgxdb.Schema {
	t.Helper()
	name := os.Getenv("POSTGRES_TEST_SCHEMA")
	if name == "" {
		return pgxdb.Schema{}
	}
	s, err := pgxdb.NewSchema(name)
	if err != nil {
		t.Fatalf("POSTGRES_TEST_SCHEMA=%q is not a valid schema: %v", name, err)
	}
	return s
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates the jobs tables so the returned store starts empty and isolated. It
// returns the store options that place the store in the same schema the
// migrations just reached, so a mismatch is impossible by construction.
func openAndMigrate(t *testing.T, dsn string) (*pgxdb.DB, []Option) {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := testSchema(t)
	var (
		opts        []Option
		migrateOpts []pgxdb.MigrateOption
	)
	if !schema.IsZero() {
		dropSchema(t, db, schema)
		opts = append(opts, WithSchema(schema))
		migrateOpts = append(migrateOpts, pgxdb.WithSchema(schema))
	}

	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir, migrateOpts...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })
	return db, opts
}

// dropSchema removes the test schema and everything in it so each run starts
// from the canonical migrations rather than whatever a previous run left.
func dropSchema(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	q := fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schema)
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("drop schema %s: %v", schema, err)
	}
}

// truncate clears every jobs table so a store starts empty, by qualified name
// when the suite runs in a schema.
func truncate(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	qualified := make([]string, len(jobTables))
	for i, table := range jobTables {
		qualified[i] = schema.Table(table)
	}
	q := "TRUNCATE " + strings.Join(qualified, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// TestLive_StatusCheck pins the boot gate WithSchema exists for: a well-formed
// schema the migrations never reached must fail with sdk.ErrNotFound naming the
// QUALIFIED table (not silently fall back to the host's own unqualified tables),
// and the schema the migrations did reach must pass.
func TestLive_StatusCheck(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	ctx := context.Background()

	t.Run("absent schema is not found", func(t *testing.T) {
		absent, err := pgxdb.NewSchema("jobs_statuscheck_absent")
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}
		err = StatusCheck(ctx, db, WithSchema(absent))
		if err == nil {
			t.Fatal("StatusCheck against an unmigrated schema returned nil")
		}
		if !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("StatusCheck error = %v, want sdk.ErrNotFound", err)
		}
		if want := absent.Table("job_queue"); !strings.Contains(err.Error(), want) {
			t.Errorf("StatusCheck error %q does not name the qualified table %q", err, want)
		}
	})

	t.Run("migrated schema passes", func(t *testing.T) {
		if err := StatusCheck(ctx, db, opts...); err != nil {
			t.Fatalf("StatusCheck against the migrated schema: %v", err)
		}
	})
}
