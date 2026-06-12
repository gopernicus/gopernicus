//go:build integration

package jobs_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gopernicus/gopernicus/core/jobs/scheduler"
	"github.com/gopernicus/gopernicus/core/repositories/jobs"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/workers"
	"github.com/gopernicus/gopernicus/workshop/testing/testenv"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
)

// End to end through the real composition-root wiring: NewRepositories →
// EnsureSchedule → scheduler engine → exactly one idempotent job_queue
// row, with a second tick reporting no work.
func TestScheduler_FireEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	db := testpgx.SetupTestPGX(t, ctx, testpgx.WithMigrations(func(ctx context.Context, pool *pgxdb.Pool) error {
		root, err := testenv.ProjectRoot()
		if err != nil {
			return err
		}
		return pgxdb.RunMigrations(ctx, pool, os.DirFS(root), "workshop/migrations/primary")
	}))

	log := slog.New(slog.DiscardHandler)
	repos := jobs.NewRepositories(log, db.Pool, nil, nil)

	require.NoError(t, repos.JobSchedule.EnsureSchedule(ctx, "digest", "@hourly", "digest.send", []byte(`{"a":1}`)))
	// Force the schedule due.
	_, err := db.Pool.Exec(ctx, `UPDATE job_schedules SET next_run_at = now() - interval '1 minute'`)
	require.NoError(t, err)

	engine := scheduler.New(repos.JobSchedule, repos.EnqueueScheduled, log)
	require.NoError(t, engine.WorkFunc()(ctx))

	// Second tick: nothing due → ErrNoWork, and still exactly one job.
	require.ErrorIs(t, engine.WorkFunc()(ctx), workers.ErrNoWork)

	var count int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM job_queue WHERE event_type = 'digest.send'`).Scan(&count))
	require.Equal(t, 1, count, "exactly one fired job")

	var jobID string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT last_job_id FROM job_schedules WHERE name = 'digest'`).Scan(&jobID))
	require.Contains(t, jobID, "sched_", "audit pointer set")

	// The fired job is claimable by the standard worker runner.
	job, err := repos.JobQueue.Checkout(ctx, "test-worker", time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, "digest.send", job.EventType)
	require.NoError(t, repos.JobQueue.Complete(ctx, job.JobID, time.Now().UTC()))
}
