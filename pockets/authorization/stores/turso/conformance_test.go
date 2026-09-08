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
	"errors"
	"os"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/storetest"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// authorizationTables are the pocket's tables cleared before each newRepos call
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

// TestTransactional runs the shared ambient-transaction family: the connector
// (*tursodb.DB) is the crud.Transactor, and the SAME connector backs the
// repositories, so a Transact-owned BEGIN IMMEDIATE transaction is the one the
// stores join. Each newRepos call builds a fresh, truncated store exactly as
// TestConformance does; the family never calls it for an observer.
func TestTransactional(t *testing.T) {
	url, token := requireTursoEnv(t)

	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, crud.Transactor) {
		db := openAndMigrate(t, url, token)
		repos, err := Repositories(db)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos, db
	})
}

// TestTransactionalRefusalLeavesLedgerUntouched is the adapter-local half of the
// shared MutationRefusesAmbientTransaction spec: the port exposes no anchor or
// receipt reader, so the proof that a refused guarded mutation touched neither
// iam_scopes nor iam_mutations is direct SQL here — checked through the pool
// while the host transaction is still OPEN (so it does not lean on the rollback
// to hide effects) and again after it.
func TestTransactionalRefusalLeavesLedgerUntouched(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	m := repos.Mutations

	mustApplyLive(t, m, grantCmd(mutID(t), "M", "owner", "u1"))
	before := ledgerState(t, db)

	err := db.Transact(ctx, func(ctx context.Context) error {
		rcpt, err := m.Apply(ctx, grantCmd(mutID(t), "M", "viewer", "u2"), nil)
		if !errors.Is(err, mutation.ErrGuardedInsideTransaction) || rcpt != nil {
			t.Fatalf("Apply inside Transact: want ErrGuardedInsideTransaction + nil receipt, got %+v, %v", rcpt, err)
		}
		if open := ledgerState(t, db); open != before {
			t.Fatalf("ledger changed while the host transaction was open: before=%+v now=%+v", before, open)
		}
		return err
	})
	if !errors.Is(err, mutation.ErrGuardedInsideTransaction) {
		t.Fatalf("Transact must return the refusal, got %v", err)
	}
	if after := ledgerState(t, db); after != before {
		t.Fatalf("ledger changed across the refused mutation: before=%+v after=%+v", before, after)
	}
}

// ledger is the direct-SQL view of the write-path tables: receipt rows, anchor
// rows, and the sum of anchor revisions (a bump moves it; a bare revision-0
// insert moves the count).
type ledger struct {
	receipts, anchors int
	revisionSum       int64
}

// ledgerState reads ledger through the POOL (never the ambient transaction).
func ledgerState(t *testing.T, db *tursodb.DB) ledger {
	t.Helper()
	var l ledger
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM iam_mutations`).Scan(&l.receipts); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*), coalesce(sum(revision), 0) FROM iam_scopes`).Scan(&l.anchors, &l.revisionSum); err != nil {
		t.Fatalf("count anchors: %v", err)
	}
	return l
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
