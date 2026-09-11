//go:build integration

package turso

import (
	"context"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
)

func TestOccurrencesMigrationPreservesSchedules(t *testing.T) {
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	ctx := context.Background()
	// Restore this disposable fixture to migration 0003, retaining its schedule.
	repo := NewScheduleStore(db)
	now := time.Now().UTC()
	sch, err := repo.Ensure(ctx, schedule.Ensure{Name: "legacy", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}, Payload: []byte(`{"legacy":true}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DROP TABLE job_schedule_occurrences`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM schema_migrations WHERE source = 'default' AND version = '0004_job_schedule_occurrences.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, sch.ID)
	if err != nil || got.ID != sch.ID || !got.NextRunAt.Equal(sch.NextRunAt) || string(got.Payload) != string(sch.Payload) {
		t.Fatalf("migration changed schedule: %+v %v", got, err)
	}
	if won, err := repo.ClaimDue(ctx, got, now.Add(time.Hour), now); err != nil || !won {
		t.Fatalf("upgraded claim: %v %v", won, err)
	}
	if pending, err := repo.ListPending(ctx, 1, nil); err != nil || len(pending) != 1 {
		t.Fatalf("upgraded pending: %v %v", pending, err)
	}
}
