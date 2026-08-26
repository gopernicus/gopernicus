// Decoy test: the behavioral proof that WithSchema routes writes, in BOTH
// directions. Live, env-gated on POSTGRES_TEST_DSN like its siblings.
package pgx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

const (
	// decoySchema is this test's own disposable schema, deliberately distinct
	// from POSTGRES_TEST_SCHEMA so the conformance leg's schema is never
	// dropped here.
	decoySchema = "events_decoy"

	// decoyEventType tags this test's rows so cleanup removes only its own.
	decoyEventType = "test.withschema.decoy"
)

// decoyCreateSQL mirrors migrations/0001_event_outbox.sql, applied to public as
// the decoy the store must NOT write to when a schema is set (and MUST write to
// when none is).
const decoyCreateSQL = `CREATE TABLE IF NOT EXISTS public.event_outbox (
    event_id       TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL,
    correlation_id TEXT        NOT NULL DEFAULT '',
    payload        JSON        NOT NULL DEFAULT '{}',
    aggregate_type TEXT,
    aggregate_id   TEXT,
    tenant_id      TEXT,
    created_at     TIMESTAMPTZ NOT NULL,
    published_at   TIMESTAMPTZ,
    CONSTRAINT event_outbox_pk PRIMARY KEY (event_id)
)`

// TestLive_WithSchema_Decoy proves the schema seam routes real writes. A bare
// public.event_outbox decoy stands in for the host's own table — the collision
// this option exists to remove. Constructed with WithSchema before the schema is
// migrated, New must FAIL naming the qualified table (the probe is qualified, not
// search_path-resolved); after migrating, a write must land in the schema and
// leave the decoy untouched; and constructed with NO option, the same write must
// land in the decoy and leave the schema untouched.
//
// Row counts are taken per schema with explicitly qualified SELECTs. Nothing here
// compares to_regclass(...)::text, which renders unqualified whenever the
// relation is visible on the search_path and would false-green.
func TestLive_WithSchema_Decoy(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A free-form DSN may itself pin search_path (exactly the wrapper this
	// option retires). Unpinned is a precondition of the decoy: assert it loudly
	// rather than false-green on a session already pointed elsewhere.
	var current string
	var path []string
	if err := db.QueryRow(ctx, `SELECT current_schema(), current_schemas(true)`).Scan(&current, &path); err != nil {
		t.Fatalf("read search_path: %v", err)
	}
	if current != "public" {
		t.Fatalf("current_schema() = %q, want \"public\" — POSTGRES_TEST_DSN pins search_path; the decoy leg needs an unpinned session", current)
	}
	for _, s := range path {
		if s == decoySchema {
			t.Fatalf("current_schemas(true) = %v contains %q — the decoy schema must not be on the search_path", path, decoySchema)
		}
	}

	schema, err := pgxdb.NewSchema(decoySchema)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	dropDecoySchema(t, db)
	t.Cleanup(func() { dropDecoySchema(t, db) })

	// The conformance leg may already have created public.event_outbox; reuse it
	// when present (IF NOT EXISTS) and count rows rather than assume it is empty.
	if _, err := db.Exec(ctx, decoyCreateSQL); err != nil {
		t.Fatalf("create public decoy: %v", err)
	}

	// Before the schema is migrated, the qualified probe must fail — and name the
	// qualified table, not the visible public decoy.
	_, err = New(db, WithSchema(schema))
	if err == nil {
		t.Fatal("New with an unmigrated schema succeeded — the probe resolved the public decoy instead")
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("New err = %v, want sdk.ErrNotFound", err)
	}
	if want := `"` + decoySchema + `".event_outbox`; !strings.Contains(err.Error(), want) {
		t.Fatalf("New err = %v, want it to name %s", err, want)
	}

	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate into %q: %v", decoySchema, err)
	}

	scoped, err := New(db, WithSchema(schema))
	if err != nil {
		t.Fatalf("New after migrating into %q: %v", decoySchema, err)
	}

	// Positive direction: the schema-scoped write lands in the schema only.
	publicBefore := countRows(t, db, "public")
	if err := scoped.Append(ctx, decoyRecord("scoped")); err != nil {
		t.Fatalf("Append (scoped): %v", err)
	}
	if got := countRows(t, db, decoySchema); got != 1 {
		t.Fatalf("%s.event_outbox count = %d, want 1", decoySchema, got)
	}
	if got := countRows(t, db, "public"); got != publicBefore {
		t.Fatalf("public.event_outbox count = %d, want %d — the scoped write leaked into the host's table", got, publicBefore)
	}

	// Negative direction: with no option the write lands in the decoy and the
	// schema is untouched. This is the "default unchanged" half of the promise.
	bare, err := New(db)
	if err != nil {
		t.Fatalf("New (no option): %v", err)
	}
	if err := bare.Append(ctx, decoyRecord("bare")); err != nil {
		t.Fatalf("Append (bare): %v", err)
	}
	if got, want := countRows(t, db, "public"), publicBefore+1; got != want {
		t.Fatalf("public.event_outbox count = %d, want %d", got, want)
	}
	if got := countRows(t, db, decoySchema); got != 1 {
		t.Fatalf("%s.event_outbox count = %d, want 1 — the unqualified write leaked into the schema", decoySchema, got)
	}

	// Leave the decoy as we found it; the schema goes with the cleanup drop.
	if _, err := db.Exec(ctx, `DELETE FROM public.event_outbox WHERE event_type = $1`, decoyEventType); err != nil {
		t.Fatalf("clean decoy rows: %v", err)
	}
}

// countRows counts the rows in the named schema's outbox with an explicitly
// qualified SELECT.
func countRows(t *testing.T, db *pgxdb.DB, schema string) int {
	t.Helper()
	var n int
	q := fmt.Sprintf(`SELECT count(*) FROM %q.event_outbox`, schema)
	if err := db.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count %s.event_outbox: %v", schema, err)
	}
	return n
}

// dropDecoySchema removes this test's disposable schema and everything in it.
func dropDecoySchema(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+decoySchema+`" CASCADE`); err != nil {
		t.Fatalf("drop schema %q: %v", decoySchema, err)
	}
}

// decoyRecord builds a uniquely keyed envelope so reruns never collide on the
// event_id primary key.
func decoyRecord(tag string) sdkevents.Record {
	id := fmt.Sprintf("decoy-%s-%d", tag, time.Now().UnixNano())
	return sdkevents.Record{
		EventID:    id,
		Type:       decoyEventType,
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{"tag":"` + tag + `"}`),
	}
}
