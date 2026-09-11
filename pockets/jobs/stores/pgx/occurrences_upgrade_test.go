package pgx

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
)

func TestOccurrencesMigrationPreservesSchedules(t *testing.T) {
	db, _ := openAndMigrate(t, requireDSN(t))
	ctx := context.Background()
	schema, err := pgxdb.NewSchema("jobs_occurrence_upgrade")
	if err != nil {
		t.Fatal(err)
	}
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "0004_job_schedule_occurrences.sql" {
			continue
		}
		path := MigrationsDir + "/" + entry.Name()
		data, err := fs.ReadFile(MigrationsFS, path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: data}
	}
	if err := pgxdb.RunMigrations(ctx, db, old, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	repo := NewScheduleStore(db, WithSchema(schema))
	now := time.Now().UTC().Truncate(time.Microsecond)
	sch, err := repo.Ensure(ctx, schedule.Ensure{Name: "legacy", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}, Payload: []byte(`{"legacy":true}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
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
