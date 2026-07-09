// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
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
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// authorizationTables are the feature's tables cleared before each newRepos call
// so every leaf subtest starts from a clean, isolated store. No FKs between them,
// so order is immaterial.
var authorizationTables = []string{"iam_relationships", "iam_roles"}

// TestConformance runs the shared authorization conformance suite (both kinds)
// against a live PostgreSQL database. Each newRepos call opens a connection,
// applies the canonical migrations, truncates both tables, and constructs the
// repositories via Repositories (exercising both boot-time table probes on every
// run).
func TestConformance(t *testing.T) {
	dsn := requireDSN(t)

	storetest.Run(t, func(t *testing.T) authorization.Repositories {
		db := openAndMigrate(t, dsn)
		repos, err := Repositories(db)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
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

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates both tables so the returned repositories start empty and isolated.
func openAndMigrate(t *testing.T, dsn string) *pgxdb.DB {
	t.Helper()
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every authorization table so a store starts empty.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	q := "TRUNCATE " + strings.Join(authorizationTables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
