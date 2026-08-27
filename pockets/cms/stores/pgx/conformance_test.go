// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
//
// Setting POSTGRES_TEST_SCHEMA runs the same suite inside that schema: the
// schema is dropped, the migrations are applied with pgxdb.WithSchema, the
// repositories are constructed with WithSchema, and every fixture statement is
// qualified. That leg is the behavioural proof that WithSchema reaches every
// executed path. Unset, the run is exactly the default-schema run it always was.
package pgx

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/cms"
	"github.com/gopernicus/gopernicus/pockets/cms/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// decoyEntryID identifies the row the cascade-containment test plants in a bare
// public.entries — a name no conformance fixture generates.
const decoyEntryID = "cms-pgx-schema-decoy"

// cmsTables are the feature's tables; a single TRUNCATE ... CASCADE clears them
// and their foreign-key children in one statement, so a Repositories starts
// empty regardless of row order.
var cmsTables = []string{
	"entry_terms",
	"entry_fields",
	"entries",
	"menu_items",
	"menus",
	"terms",
	"assets",
	"inquiries",
}

// TestConformance_Postgres runs the shared cms storetest suite against a live
// PostgreSQL database. Each newRepos call opens a connection, applies the
// canonical migrations, and truncates the feature's tables so every leaf subtest
// starts from a clean, isolated Repositories (the SQL harness half of the
// newRepos contract). With POSTGRES_TEST_SCHEMA set, all three happen inside
// that schema.
func TestConformance_Postgres(t *testing.T) {
	dsn := requireDSN(t)
	schema := testSchema(t)
	if !schema.IsZero() {
		dropSchema(t, dsn, schema)
	}

	storetest.Run(t, func(t *testing.T) cms.Repositories {
		db := openDB(t, dsn)

		if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncate(t, db, schema)
		t.Cleanup(func() { truncate(t, db, schema) })

		return Repositories(db, WithSchema(schema))
	})
}

// TestLive_TruncateCascadeStaysInSchema proves the containment the schema leg
// depends on: entry_fields and entry_terms reference entries with ON DELETE
// CASCADE, so a TRUNCATE of the schema's tables cascades — and it must cascade
// to the SCHEMA's children, never to a host's own bare public.entries. The decoy
// row planted in public survives the schema leg's TRUNCATE.
func TestLive_TruncateCascadeStaysInSchema(t *testing.T) {
	dsn := requireDSN(t)
	schema := testSchema(t)
	if schema.IsZero() {
		t.Skip("POSTGRES_TEST_SCHEMA not set — cascade containment NOT verified")
	}

	ctx := context.Background()
	db := openDB(t, dsn)
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate into %s: %v", schema, err)
	}
	plantPublicDecoy(t, db)

	truncate(t, db, schema)

	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM public.entries WHERE id = $1", decoyEntryID).Scan(&n); err != nil {
		t.Fatalf("count decoy: %v", err)
	}
	if n != 1 {
		t.Fatalf("public.entries decoy row count = %d, want 1 — the schema TRUNCATE ... CASCADE escaped %s", n, schema)
	}
}

// TestLive_StatusCheck pins the boot gate: a well-formed but absent schema names
// the qualified table and wraps sdk.ErrNotFound, and the configured, migrated
// schema passes.
func TestLive_StatusCheck(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	db := openDB(t, dsn)

	t.Run("absent schema", func(t *testing.T) {
		absent, err := pgxdb.NewSchema("cms_status_check_absent")
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}
		err = StatusCheck(ctx, db, WithSchema(absent))
		if !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("StatusCheck error = %v, want sdk.ErrNotFound", err)
		}
		if want := `"cms_status_check_absent".entries`; !strings.Contains(err.Error(), want) {
			t.Errorf("StatusCheck error %q does not name %s", err, want)
		}
	})

	t.Run("migrated schema", func(t *testing.T) {
		schema := testSchema(t)
		if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		if err := StatusCheck(ctx, db, WithSchema(schema)); err != nil {
			t.Fatalf("StatusCheck: %v", err)
		}
	})
}

// requireDSN returns POSTGRES_TEST_DSN or skips loudly.
func requireDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	return dsn
}

// testSchema returns the schema named by POSTGRES_TEST_SCHEMA, or the zero
// Schema (the default, unqualified leg) when it is unset.
func testSchema(t *testing.T) pgxdb.Schema {
	t.Helper()
	name := os.Getenv("POSTGRES_TEST_SCHEMA")
	if name == "" {
		return pgxdb.Schema{}
	}
	s, err := pgxdb.NewSchema(name)
	if err != nil {
		t.Fatalf("POSTGRES_TEST_SCHEMA=%q: %v", name, err)
	}
	return s
}

// openDB opens a connector pool closed at test end.
func openDB(t *testing.T, dsn string) *pgxdb.DB {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// dropSchema removes the test schema and everything in it, so the schema leg
// starts from nothing exactly as the default leg's TRUNCATE does.
func dropSchema(t *testing.T, dsn string, schema pgxdb.Schema) {
	t.Helper()
	db := openDB(t, dsn)
	if _, err := db.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema.String()+`" CASCADE`); err != nil {
		t.Fatalf("drop schema %s: %v", schema, err)
	}
}

// truncate clears every cms table so a Repositories starts empty, qualified by
// the leg's schema.
func truncate(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	names := make([]string, 0, len(cmsTables))
	for _, tbl := range cmsTables {
		names = append(names, schema.Table(tbl))
	}
	q := "TRUNCATE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// plantPublicDecoy creates a bare public.entries from the canonical migration
// DDL (idempotent) and seeds one identifiable row. search_path is pinned to
// public for the transaction because a free-form POSTGRES_TEST_DSN may pin it
// elsewhere, which would put the decoy in the very schema under test.
func plantPublicDecoy(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	ctx := context.Background()
	ddl, err := MigrationsFS.ReadFile(MigrationsDir + "/0018_entries.sql")
	if err != nil {
		t.Fatalf("read entries DDL: %v", err)
	}
	err = db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO public"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(ddl)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO public.entries (id, type, slug, title, status, created_at, updated_at)
			VALUES ($1, 'decoy', $1, 'decoy', 'draft', now(), now())
			ON CONFLICT (id) DO NOTHING`, decoyEntryID)
		return err
	})
	if err != nil {
		t.Fatalf("plant public decoy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM public.entries WHERE id = $1", decoyEntryID)
	})
}
