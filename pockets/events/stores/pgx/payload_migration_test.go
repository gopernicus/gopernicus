package pgx

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/fstest"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestPayloadMigrationPreservesHistoricalBytes(t *testing.T) {
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: requireDSN(t), MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	schema, err := pgxdb.NewSchema("events_payload_upgrade")
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		if _, err := db.Exec(ctx, `DROP SCHEMA IF EXISTS "events_payload_upgrade" CASCADE`); err != nil {
			t.Error(err)
		}
	}
	drop()
	defer drop()
	original, err := MigrationsFS.ReadFile("migrations/0001_event_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{"migrations/0001_event_outbox.sql": &fstest.MapFile{Data: original}}
	if err := pgxdb.RunMigrations(ctx, db, old, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	payload := []byte(" { \"b\":2, \"a\": 1 }\n")
	if _, err := db.Exec(ctx, `INSERT INTO `+schema.Table(outboxTable)+` (event_id,event_type,occurred_at,payload,created_at) VALUES ('historical','test.historical',now(),$1::json,now())`, string(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), db, WithSchema(schema)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("legacy schema New = %v", err)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	store, err := New(context.Background(), db, WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.ListUnpublished(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !bytes.Equal(entries[0].Payload, payload) {
		t.Fatalf("upgraded payload = %+v, want %q", entries, payload)
	}
	record := rec("binary-after-upgrade")
	record.Payload = []byte{0, 255, 128}
	if err := store.Append(ctx, record); err != nil {
		t.Fatal(err)
	}
}

func TestNilDependenciesAndInvalidBatch(t *testing.T) {
	if _, err := New(context.Background(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New(nil) = %v", err)
	}
	if err := (&Store{}).AppendTx(context.Background(), nil, rec("a")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("AppendTx(nil) = %v", err)
	}
	invalid := rec("")
	if err := insertRecords(context.Background(), nil, "event_outbox", rec("valid-first"), invalid); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid batch = %v", err)
	}
}
