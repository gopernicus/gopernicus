// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// They require POSTGRES_TEST_DSN in the environment. Absent it, the tests skip
// loudly — a silent green here would claim dialect conformance nothing verified.
package pgx

import (
	"context"
	"os"
	"strings"
	"testing"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/storetest"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// authTables are the feature's tables in child-before-parent order, so a
// truncation pass respects any conventional user_id references: api_keys before
// service_accounts, and the oauth/audit/invitation tables before users. A single
// TRUNCATE clears them so a Repositories starts empty (no enforced FKs, matching
// the turso store's logged decision).
var authTables = []string{
	"user_passwords",
	"sessions",
	"api_keys",
	"service_accounts",
	"oauth_accounts",
	"oauth_states",
	"security_events",
	"invitations",
	"user_identifiers",
	"challenges",
	"contact_changes",
	"authentication_grants",
	"users",
}

// TestConformance_Postgres runs the shared auth storetest suite against a live
// PostgreSQL database. Each newRepos call opens a connection, applies the
// canonical migrations, and truncates the feature's tables so every leaf subtest
// starts from a clean, isolated Repositories (the SQL harness half of the
// newRepos contract).
func TestConformance_Postgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}

	storetest.Run(t, func(t *testing.T) auth.Repositories {
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

		return Repositories(db)
	})
}

// truncate clears every auth table so a Repositories starts empty.
func truncate(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	q := "TRUNCATE " + strings.Join(authTables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
