//go:build livedelivery && integration

package main

import (
	"context"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	jobsturso "github.com/gopernicus/gopernicus/features/jobs/stores/turso"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// TestLiveJobsDeliveryTurso runs the AV3D-3.5 live delivery proof list against a live
// Turso/libSQL fenced queue. It requires TURSO_DATABASE_URL + TURSO_AUTH_TOKEN and the
// `integration` build tag (the repo's turso convention); absent the env it skips
// LOUDLY — the open owner gate for the turso dialect.
func TestLiveJobsDeliveryTurso(t *testing.T) {
	url := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — LIVE turso jobs-mode delivery proof NOT verified (KnownUnknownOpaqueAdmissionParity, ProviderTimeoutAndRetryOffRequestPath, RestartAfterOpaqueAdmission, RestartAfterCheckpointResendsSameSecret, RestartAfterProviderAcceptanceResendsSameSecret, ResendConvergesToLatestGeneration, StatusAndEventsContainNoSecrets, TerminalCleanupAndPurge)")
	}

	runLiveDeliveryProofs(t, "turso", func(t *testing.T) job.FencedQueueRepository {
		db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: token})
		if err != nil {
			t.Fatalf("turso connect: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		if err := tursodb.RunMigrations(context.Background(), db, jobsturso.MigrationsFS, jobsturso.MigrationsDir); err != nil {
			t.Fatalf("turso migrate: %v", err)
		}
		// Isolate each leaf: clear the jobs tables before use.
		for _, tbl := range []string{"job_queue", "job_schedules", "fenced_job_queue"} {
			if _, err := db.Exec(context.Background(), "DELETE FROM "+tbl); err != nil {
				t.Fatalf("turso truncate %s: %v", tbl, err)
			}
		}
		return jobsturso.NewFencedQueueStore(db)
	})
}
