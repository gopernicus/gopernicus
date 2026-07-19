//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// They require TURSO_DATABASE_URL and TURSO_AUTH_TOKEN in the environment (or a
// .env loaded by the caller). Absent those, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified. The shared
// storetest.Run suite is the executable form of BOTH kinds' port contracts
// (relationship.Storer + role.Storer) plus the engine-over-store adversarial and
// roles families, so the memstore and this recursive-CTE store provably authorize
// identically.
package turso

import (
	"context"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/storetest"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// authorizationTables are the feature's tables cleared before each newRepos call
// so every leaf subtest starts from a clean, isolated store — including the v3
// write-path tables (iam_scopes revision anchors, iam_mutations receipts) so the
// Mutations conformance suite starts from revision 0 with no consumed MutationIDs.
// No FKs between them, so order is immaterial.
var authorizationTables = []string{"iam_relationships", "iam_roles", "iam_scopes", "iam_mutations"}

// TestConformance runs the shared authorization conformance suite (both kinds)
// against a live Turso/libSQL database. Each newRepos call opens a connection,
// applies the canonical migrations, truncates both tables, and constructs the
// repositories via Repositories (exercising both boot-time table probes on every
// run).
func TestConformance(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.Run(t, func(t *testing.T) authorization.Repositories {
		db := openAndMigrate(t, url, token)
		repos, err := Repositories(db)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
	})
}

// requireTursoEnv returns the live connection env or skips loudly.
func requireTursoEnv(t *testing.T) (url, token string) {
	t.Helper()
	url = os.Getenv("TURSO_DATABASE_URL")
	token = os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified")
	}
	return url, token
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates both tables so the returned repositories start empty and isolated.
func openAndMigrate(t *testing.T, url, token string) *tursodb.DB {
	t.Helper()
	db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: token})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })
	return db
}

// truncate clears every authorization table so a store starts empty.
func truncate(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range authorizationTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}
