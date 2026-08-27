// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
//
// The OPTIONAL POSTGRES_TEST_SCHEMA runs the whole live suite inside a named
// schema: migrations are applied with pgxdb.WithSchema and every store is
// constructed WithSchema, so the non-default-schema leg executes the same paths
// as the default one. Unset, every fixture behaves exactly as it did before the
// option existed.
package pgx

import (
	"context"
	"os"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/storetest"
)

// authTables are the pocket's tables in child-before-parent order, so a
// truncation pass respects any conventional user_id references: api_keys before
// service_accounts, and the oauth/audit/invitation tables before users. A single
// TRUNCATE clears them so a Repositories starts empty (no enforced FKs, matching
// the turso store's logged decision).
var authTables = []string{
	passwordsTable,
	sessionsTable,
	apiKeysTable,
	serviceAccountsTable,
	oauthAccountsTable,
	oauthStatesTable,
	securityEventsTable,
	invitationsTable,
	identifiersTable,
	challengesTable,
	contactChangesTable,
	authGrantsTable,
	usersTable,
}

// testSchema resolves the optional POSTGRES_TEST_SCHEMA into the Schema every
// live fixture in this package runs under. Unset → the zero Schema, i.e. the
// unqualified default this store has always used.
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

// storeOpts are the construction options for s: none for the zero Schema, so the
// default leg constructs exactly as it did before.
func storeOpts(s pgxdb.Schema) []Option {
	if s.IsZero() {
		return nil
	}
	return []Option{WithSchema(s)}
}

// migrateOpts are the runner options for s (see storeOpts).
func migrateOpts(s pgxdb.Schema) []pgxdb.MigrateOption {
	if s.IsZero() {
		return nil
	}
	return []pgxdb.MigrateOption{pgxdb.WithSchema(s)}
}

// dropSchema removes the test schema and everything in it, so a run cannot
// inherit a stale ledger or DDL from an earlier one. It is a no-op for the zero
// Schema: the default leg must never drop the host's own default schema.
func dropSchema(t *testing.T, db *pgxdb.DB, s pgxdb.Schema) {
	t.Helper()
	if s.IsZero() {
		return
	}
	if _, err := db.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+s.String()+`" CASCADE`); err != nil {
		t.Fatalf("drop schema %s: %v", s, err)
	}
}

// TestConformance_Postgres runs the shared auth storetest suite against a live
// PostgreSQL database. Each newRepos call opens a connection, applies the
// canonical migrations, and truncates the pocket's tables so every leaf subtest
// starts from a clean, isolated Repositories (the SQL harness half of the
// newRepos contract).
func TestConformance_Postgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	schema := testSchema(t)

	// One drop per run, not per leaf: the schema leg starts from no schema at all
	// so a stale ledger cannot mask a DDL change, and every leaf below then
	// re-applies the (idempotent) stream and truncates.
	if !schema.IsZero() {
		db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		dropSchema(t, db, schema)
		_ = db.Close()
	}

	storetest.Run(t, func(t *testing.T) auth.Repositories {
		db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir, migrateOpts(schema)...); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncate(t, db, schema)
		t.Cleanup(func() { truncate(t, db, schema) })

		repos, err := Repositories(db, storeOpts(schema)...)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
	})
}

// truncate clears every auth table so a Repositories starts empty. The names are
// rendered under schema, so the schema leg never truncates the default schema's
// same-named tables.
func truncate(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	names := make([]string, 0, len(authTables))
	for _, table := range authTables {
		names = append(names, schema.Table(table))
	}
	q := "TRUNCATE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
