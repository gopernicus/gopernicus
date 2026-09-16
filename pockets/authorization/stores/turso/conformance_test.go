//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// They require TURSO_DATABASE_URL, TURSO_AUTH_TOKEN for remote databases, and
// AUTHORIZATION_TURSO_DISPOSABLE_URL naming the exact same disposable URL.
// The suite migrates and deletes authorization data; application URLs must not
// be used. Absent connection settings, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified. The shared
// storetest.Run suite is the executable form of BOTH kinds' port contracts
// (relationship.Storer + role.Storer) plus the engine-over-store adversarial and
// roles families, so the memstore and this recursive-CTE store provably authorize
// identically.
package turso

import (
	"context"
	"os"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

// authorizationTables are the pocket's tables cleared before each newRepos call
// so every leaf subtest starts from a clean, isolated store — including the v3
// optional audit history so every test observes only its own changes.
// No FKs between them, so order is immaterial.
var authorizationTables = []string{"iam_tuples", "iam_audit"}

// TestConformance runs the shared authorization conformance suite (both kinds)
// against a live Turso/libSQL database. Each newRepos call opens a connection,
// applies the canonical migrations, truncates both tables, and constructs the
// repositories via Repositories (exercising both boot-time table probes on every
// run).
func TestConformance(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) storetest.Repositories {
		db := openAndMigrate(t, url, token)
		repos, err := testRepositories(context.Background(), db, WithGuardianPolicy(policy))
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
	})
}

// TestTransactional runs the shared ambient-transaction family: the connector
// (*tursodb.DB) is the transaction.Transactor, and the SAME connector backs the
// repositories, so a Transact-owned BEGIN IMMEDIATE transaction is the one the
// stores join. Each newRepos call builds a fresh, truncated store exactly as
// TestConformance does; the family never calls it for an observer.
func TestTransactional(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.RunTransactional(t, func(t *testing.T) (storetest.Repositories, transaction.Transactor) {
		db := openAndMigrate(t, url, token)
		repos, err := testRepositories(context.Background(), db)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos, db
	})
}

// requireTursoEnv returns the live connection env or skips loudly.
func requireTursoEnv(t *testing.T) (url, token string) {
	t.Helper()
	url = os.Getenv("TURSO_DATABASE_URL")
	token = os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" {
		t.Skip("TURSO_DATABASE_URL not set — turso conformance NOT verified")
	}
	if err := validateDisposableTursoURL(url, os.Getenv(disposableTursoURLEnv)); err != nil {
		t.Fatal(err)
	}
	if token == "" && !strings.HasPrefix(url, "file:") {
		t.Skip("TURSO_AUTH_TOKEN not set — remote turso conformance NOT verified")
	}
	return url, token
}

// openAndMigrate opens a live connection, applies the canonical migrations, and
// truncates both tables so the returned repositories start empty and isolated.
func openAndMigrate(t *testing.T, url, token string) *tursodb.DB {
	t.Helper()
	if err := validateDisposableTursoURL(url, os.Getenv(disposableTursoURLEnv)); err != nil {
		t.Fatal(err)
	}
	db, err := tursodb.Open(context.Background(), tursodb.Config{URL: url, AuthToken: token})
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

func TestAuditConformance(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) storetest.Repositories {
		var opts []Option
		if enabled {
			opts = append(opts, WithAudit())
		}
		_, repos := liveReposWith(t, opts...)
		return repos
	})
}
