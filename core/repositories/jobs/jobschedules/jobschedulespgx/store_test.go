// gopernicus:bootstrap kind=integrationtest/store_test.go template=1cf1c3c46901
//go:build integration

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom integration tests for the JobSchedule store here.
//
// The setupTestStore() helper in generated_test.go provides a test database
// and store instance. Use it as the basis for custom tests:
//
//	func TestStore_MyCustomQuery(t *testing.T) {
//		ctx, db, store := setupTestStore(t)
//		pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)
//		// ... test custom store methods
//	}

package jobschedulespgx

import (
	"context"
	"os"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/workshop/testing/testenv"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gopernicus/gopernicus/core/repositories/jobs/jobschedules"
	"github.com/gopernicus/gopernicus/workshop/testing/pgxfixtures"
)

// migrateTestDB applies this project's migrations to the test database, so
// tests run against the same schema as 'gopernicus db migrate'. Replace it
// if this store's tests need a different schema setup. testenv.ProjectRoot
// resolves the module root, so the migrations path works at any test working
// directory.
func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
	root, err := testenv.ProjectRoot()
	if err != nil {
		return err
	}
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(root), "workshop/migrations/primary")
}

// testPGXOptions provides extra options to testpgx.SetupTestPGX in the
// generated setupTestStore helper. Use it to pick a Postgres image with
// required extensions, e.g.:
//
//	var testPGXOptions = []testpgx.Option{
//		testpgx.WithPostgresVersion("pgvector/pgvector:pg17"),
//		testpgx.WithExtensions("vector", "pg_trgm"),
//	}
var testPGXOptions []testpgx.Option

// ─── scheduler claim-path integration ────────────────────────────────────────

// Two concurrent claimers, one slot: the next_run_at compare-and-set must
// let exactly one win — the no-leader-election guarantee the scheduler
// engine is built on.
func TestStore_ClaimDueExactlyOnce(t *testing.T) {
	ctx, db, store := setupTestStore(t)
	pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)

	slot := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO job_schedules (schedule_id, name, event_type, cron_expr, payload, enabled, next_run_at)
		VALUES ('s1', 'digest', 'digest.send', '@hourly', '{}', TRUE, $1)`, slot)
	require.NoError(t, err)

	next := slot.Add(time.Hour)
	now := time.Now().UTC()

	const racers = 8
	wins := make(chan bool, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := store.ClaimDue(ctx, "s1", slot, next, now)
			require.NoError(t, err)
			wins <- claimed
		}()
	}
	wg.Wait()
	close(wins)

	winners := 0
	for w := range wins {
		if w {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one racer must claim the slot")

	// And the slot is gone: ListDue is empty.
	due, err := store.ListDue(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, due)
}

// EnsureSchedule is idempotent by name, recomputes next_run_at on cron
// change, and leaves unchanged schedules (and their slots) alone.
func TestRepository_EnsureSchedule(t *testing.T) {
	ctx, db, store := setupTestStore(t)
	pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)
	repo := jobschedules.NewRepository(jobschedules.NewCacheStore(store, nil))

	require.NoError(t, repo.EnsureSchedule(ctx, "digest", "0 9 * * *", "digest.send", nil))
	first, err := repo.GetByName(ctx, "digest")
	require.NoError(t, err)

	// Same inputs → no-op (slot preserved).
	require.NoError(t, repo.EnsureSchedule(ctx, "digest", "0 9 * * *", "digest.send", nil))
	same, err := repo.GetByName(ctx, "digest")
	require.NoError(t, err)
	require.Equal(t, first.NextRunAt, same.NextRunAt)
	require.Equal(t, first.ScheduleID, same.ScheduleID)

	// Changed cron → next_run_at recomputed.
	require.NoError(t, repo.EnsureSchedule(ctx, "digest", "*/5 * * * *", "digest.send", nil))
	changed, err := repo.GetByName(ctx, "digest")
	require.NoError(t, err)
	require.NotEqual(t, first.NextRunAt, changed.NextRunAt)
	require.Equal(t, "*/5 * * * *", changed.CronExpr)

	// Bad cron rejected.
	require.Error(t, repo.EnsureSchedule(ctx, "bad", "nope", "x", nil))

	// Boot race: two instances ensure the same schedule concurrently —
	// both must succeed (one wins the insert, the loser defers to it).
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.EnsureSchedule(ctx, "race", "@hourly", "race.fire", nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var raceCount int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM job_schedules WHERE name = 'race'`).Scan(&raceCount))
	require.Equal(t, 1, raceCount)
}
