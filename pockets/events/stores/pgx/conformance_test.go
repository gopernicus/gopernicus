// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// POSTGRES_TEST_SCHEMA is optional: when set, the whole live suite is dropped and
// re-created inside that schema — migrations run with pgxdb.WithSchema and the
// stores are constructed with WithSchema, so the conformance contract is proved
// under a non-default schema as well. Unset, behaviour is unchanged.
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
// The shared storetest.Run suite is the executable form of the
// outbox.EntryRepository contract; the store-specific AppendTx is proved
// separately in appender_test.go (it takes a *pgxdb.Tx the dialect-blind suite
// cannot). This mirrors the sibling pgx stores' plain env-gating (no build tag).
package pgx

import (
	"context"
	"os"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/events/domain/outbox"
	"github.com/gopernicus/gopernicus/pockets/events/storetest"
)

// outboxTables are the pocket's tables cleared before each newRepo call so every
// leaf subtest starts from a clean, isolated store.
var outboxTables = []string{outboxTable}

// TestConformance runs the shared outbox conformance suite against a live
// PostgreSQL database. Each newRepo call opens a connection, applies the canonical
// migrations, truncates the outbox table, and constructs the Store via New
// (exercising the boot-time table probe on every run).
func TestConformance(t *testing.T) {
	dsn := requireDSN(t)

	storetest.Run(t, func(t *testing.T) outbox.EntryRepository {
		db := openAndMigrate(t, dsn)
		store, err := New(db, storeOptions(t)...)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return store
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

// testSchema returns the schema the live suite runs in, or the zero Schema when
// POSTGRES_TEST_SCHEMA is unset (the default leg: every statement unqualified).
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

// storeOptions returns the store options for the configured leg — none on the
// default leg, WithSchema on the schema leg.
func storeOptions(t *testing.T) []Option {
	t.Helper()
	s := testSchema(t)
	if s.IsZero() {
		return nil
	}
	return []Option{WithSchema(s)}
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates the outbox table so the returned store starts empty and isolated. On
// the schema leg it first drops the schema so the run starts from nothing, and
// migrates into it with pgxdb.WithSchema.
func openAndMigrate(t *testing.T, dsn string) *pgxdb.DB {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	schema := testSchema(t)
	var opts []pgxdb.MigrateOption
	if !schema.IsZero() {
		if _, err := db.Exec(ctx, `DROP SCHEMA IF EXISTS "`+schema.String()+`" CASCADE`); err != nil {
			t.Fatalf("drop schema %q: %v", schema, err)
		}
		opts = append(opts, pgxdb.WithSchema(schema))
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, opts...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every outbox table so a store starts empty. Table names are
// qualified on the schema leg so the default schema is never touched.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	schema := testSchema(t)
	names := make([]string, len(outboxTables))
	for i, table := range outboxTables {
		names[i] = schema.Table(table)
	}
	q := "TRUNCATE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
