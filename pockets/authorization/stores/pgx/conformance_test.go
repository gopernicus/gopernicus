// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// Setting POSTGRES_TEST_SCHEMA additionally runs the whole live suite inside that
// schema: every fixture drops and recreates it, migrates with pgxdb.WithSchema,
// constructs its stores with WithSchema, and qualifies its own hand-rolled SQL.
// Unset, every fixture behaves exactly as it always has.
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
// The shared storetest.Run suite is the executable form of BOTH kinds' port
// contracts (relationship.Storer + role.Storer) plus the engine-over-store
// adversarial and roles families, so the memstore and this recursive-CTE store
// provably authorize identically. This mirrors the sibling pgx stores' plain
// env-gating (no build tag).
package pgx

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/storetest"
)

// authorizationTables are the pocket's tables cleared before each newRepos call
// so every leaf subtest starts from a clean, isolated store — including the v3
// write-path tables (iam_scopes revision anchors, iam_mutations receipts) so the
// Mutations conformance suite starts from revision 0 with no consumed MutationIDs.
// No FKs between them, so order is immaterial.
var authorizationTables = []string{"iam_relationships", "iam_roles", "iam_scopes", "iam_mutations"}

// fixtureTables are the relation names a hand-rolled fixture statement may name:
// the pocket's own tables plus the migration ledger the destructive fixtures
// clear. qualifySQL rewrites exactly these under the test schema.
var fixtureTables = append(append([]string(nil), authorizationTables...), "schema_migrations")

// fixtureTableRE matches a bare fixtureTables name on word boundaries, so index
// and constraint names that merely embed one (idx_iam_roles_unique,
// ck_iam_relationships_nonempty) are left alone.
var fixtureTableRE = regexp.MustCompile(`\b(` + strings.Join(fixtureTables, "|") + `)\b`)

// dropTestSchemaOnce drops the leg's schema exactly once per process, before the
// first fixture migrates into it, so the run starts from an empty namespace
// without paying a full DROP/re-migrate on every openAndMigrate call.
var dropTestSchemaOnce sync.Once

// testSchemaOnce parses POSTGRES_TEST_SCHEMA once. An unset variable is the zero
// Schema — the default leg, byte-identical to the SQL this store has always run.
var testSchemaOnce = sync.OnceValues(func() (pgxdb.Schema, error) {
	name := os.Getenv("POSTGRES_TEST_SCHEMA")
	if name == "" {
		return pgxdb.Schema{}, nil
	}
	return pgxdb.NewSchema(name)
})

// TestConformance runs the shared authorization conformance suite (both kinds)
// against a live PostgreSQL database. Each newRepos call opens a connection,
// applies the canonical migrations, truncates both tables, and constructs the
// repositories via Repositories (exercising both boot-time table probes on every
// run).
func TestConformance(t *testing.T) {
	dsn := requireDSN(t)

	storetest.Run(t, func(t *testing.T) authorization.Repositories {
		db := openAndMigrate(t, dsn)
		repos, err := Repositories(db, storeOptions(t)...)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
	})
}

// testSchema is the optional schema leg's target, or the zero Schema.
func testSchema(t *testing.T) pgxdb.Schema {
	t.Helper()
	s, err := testSchemaOnce()
	if err != nil {
		t.Fatalf("POSTGRES_TEST_SCHEMA: %v", err)
	}
	return s
}

// storeOptions returns the construction options for the configured leg: none by
// default, WithSchema on the schema leg.
func storeOptions(t *testing.T) []Option {
	t.Helper()
	if s := testSchema(t); !s.IsZero() {
		return []Option{WithSchema(s)}
	}
	return nil
}

// migrateOptions is storeOptions' runner counterpart.
func migrateOptions(t *testing.T) []pgxdb.MigrateOption {
	t.Helper()
	if s := testSchema(t); !s.IsZero() {
		return []pgxdb.MigrateOption{pgxdb.WithSchema(s)}
	}
	return nil
}

// qualify renders one table name under the configured leg's schema.
func qualify(t *testing.T, name string) string {
	t.Helper()
	return testSchema(t).Table(name)
}

// qualifySQL rewrites every bare pocket/ledger table name in a hand-rolled
// fixture statement under the configured leg's schema. It is a TEST-only helper:
// the store's own SQL is qualified at its source, never rewritten.
func qualifySQL(t *testing.T, sql string) string {
	t.Helper()
	if testSchema(t).IsZero() {
		return sql
	}
	return fixtureTableRE.ReplaceAllStringFunc(sql, func(name string) string { return qualify(t, name) })
}

// ensureSchema makes the leg's schema exist and, on the first call of the run,
// drops whatever was there first. It is a no-op on the default leg, where the
// fixtures own their tables individually in the database's default schema. The
// destructive fixtures call it before their own DROP TABLE sweeps, which would
// otherwise name a namespace that does not exist yet.
func ensureSchema(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	s := testSchema(t)
	if s.IsZero() {
		return
	}
	ctx := context.Background()
	var dropErr error
	dropTestSchemaOnce.Do(func() {
		_, dropErr = db.Exec(ctx, `DROP SCHEMA IF EXISTS "`+s.String()+`" CASCADE`)
	})
	if dropErr != nil {
		t.Fatalf("drop schema %s: %v", s, dropErr)
	}
	if _, err := db.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "`+s.String()+`"`); err != nil {
		t.Fatalf("create schema %s: %v", s, err)
	}
}

// requireDSN returns the live connection DSN or skips loudly.
func requireDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	testSchema(t) // fail fast on a malformed POSTGRES_TEST_SCHEMA
	return dsn
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates both tables so the returned repositories start empty and isolated.
func openAndMigrate(t *testing.T, dsn string) *pgxdb.DB {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ensureSchema(t, db)
	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir, migrateOptions(t)...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every authorization table so a store starts empty.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	names := make([]string, 0, len(authorizationTables))
	for _, tbl := range authorizationTables {
		names = append(names, qualify(t, tbl))
	}
	q := "TRUNCATE " + strings.Join(names, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
