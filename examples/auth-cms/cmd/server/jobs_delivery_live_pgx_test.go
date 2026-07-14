//go:build livedelivery

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	jobspgx "github.com/gopernicus/gopernicus/features/jobs/stores/pgx"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// TestLiveJobsDeliveryPGX runs the AV3D-3.5 live delivery proof list against a live
// PostgreSQL fenced queue. It requires POSTGRES_TEST_DSN; absent it, the test skips
// LOUDLY — a silent green here would claim the durable jobs-mode delivery path was
// verified against postgres when nothing ran. This skip is the open owner gate.
func TestLiveJobsDeliveryPGX(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — LIVE postgres jobs-mode delivery proof NOT verified (KnownUnknownOpaqueAdmissionParity, ProviderTimeoutAndRetryOffRequestPath, RestartAfterOpaqueAdmission, RestartAfterCheckpointResendsSameSecret, RestartAfterProviderAcceptanceResendsSameSecret, ResendConvergesToLatestGeneration, StatusAndEventsContainNoSecrets, TerminalCleanupAndPurge)")
	}

	runLiveDeliveryProofs(t, "pgx", func(t *testing.T) job.FencedQueueRepository {
		db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
		if err != nil {
			t.Fatalf("pgx connect: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := pgxdb.RunMigrations(context.Background(), db, jobspgx.MigrationsFS, jobspgx.MigrationsDir); err != nil {
			t.Fatalf("pgx migrate: %v", err)
		}
		// Isolate each leaf: truncate the fenced queue table before use.
		if _, err := db.Exec(context.Background(), "TRUNCATE "+strings.Join([]string{"job_queue", "job_schedules", "fenced_job_queue"}, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("pgx truncate: %v", err)
		}
		return jobspgx.NewFencedQueueStore(db)
	})
}
