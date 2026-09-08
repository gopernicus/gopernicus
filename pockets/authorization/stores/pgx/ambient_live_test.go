// The segovia-shaped ambient-transaction proof (plan
// authorization-stores-ambient-transaction, D4 last paragraph): a host's OWN
// application row and the authorization tuple that projects it are written
// inside one connector-owned Transact, and they commit or roll back TOGETHER.
// The shared storetest family proves the join per store method; this test
// proves it across the host/pocket boundary the plan exists for, and proves the
// widened advisory lock: a competing SetRelationTargets on the same key from
// another connection blocks until the host's COMMIT — never slipping between
// the row and the tuple — and the stored targets afterwards are the second
// caller's, never a union. It requires POSTGRES_TEST_DSN and skips loudly
// without it, like the conformance suite.
package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// ambientWait bounds every wait in the test — the outside visibility reads, the
// poll for the blocked competitor, and the competitor's completion after commit.
const ambientWait = 15 * time.Second

var errInjectedHostFailure = errors.New("ambient test: injected host failure")

// appSpaces creates the throwaway host table in the test schema (dropped on
// cleanup) and returns its qualified name.
func appSpaces(t *testing.T, db *pgxdb.DB) string {
	t.Helper()
	ctx := context.Background()
	table := qualify(t, "app_spaces")
	if _, err := db.Exec(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
		t.Fatalf("drop app_spaces: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE `+table+` (id text PRIMARY KEY, parent_id text)`); err != nil {
		t.Fatalf("create app_spaces: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP TABLE IF EXISTS `+table) })
	return table
}

// insertSpace writes the host row on whatever querier ctx selects (the ambient
// transaction inside Transact, the pool outside) — the host's own repository
// shape (db.QuerierFrom(ctx)).
func insertSpace(ctx context.Context, db *pgxdb.DB, table, id, parent string) error {
	_, err := db.QuerierFrom(ctx).Exec(ctx, `INSERT INTO `+table+` (id, parent_id) VALUES (@id, @parent)`, pgx.NamedArgs{"id": id, "parent": parent})
	return err
}

// spaceParent reads the host row's parent through the POOL with a bounded
// context; ok=false when the row is absent.
func spaceParent(t *testing.T, db *pgxdb.DB, table, id string) (parent string, ok bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ambientWait)
	defer cancel()
	err := db.QueryRow(ctx, `SELECT parent_id FROM `+table+` WHERE id = @id`, pgx.NamedArgs{"id": id}).Scan(&parent)
	if err != nil {
		if errors.Is(pgxdb.MapError(err), sdk.ErrNotFound) {
			return "", false
		}
		t.Fatalf("read app_spaces row: %v", err)
	}
	return parent, true
}

// parentTargets reads the tuple through the POOL with a bounded context.
func parentTargets(t *testing.T, s relationship.Storer, spaceID string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ambientWait)
	defer cancel()
	targets, err := s.GetRelationTargets(ctx, "space", spaceID, "parent")
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	ids := make([]string, 0, len(targets))
	for _, tgt := range targets {
		ids = append(ids, tgt.ID)
	}
	return ids
}

func setParent(ctx context.Context, s relationship.Storer, spaceID, parentID string) error {
	return s.SetRelationTargets(ctx, "space", spaceID, "parent", []relationship.CreateRelationship{{
		ResourceType: "space", ResourceID: spaceID, Relation: "parent", SubjectType: "space", SubjectID: parentID,
	}})
}

// waitForAdvisoryWaiter polls pg_stat_activity (through the pool) until some
// backend is waiting on an advisory lock — the competitor blocked on the key
// the open host transaction holds — or the bound expires. No fixed sleeps: the
// poll returns as soon as the wait is observable.
func waitForAdvisoryWaiter(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	deadline := time.Now().Add(ambientWait)
	for {
		var n int
		if err := db.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if n >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("competing SetRelationTargets never blocked on the advisory lock within %s", ambientWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAmbientRowAndTupleCommitTogether is the D4 live test: (1) an injected host
// error after both writes leaves neither the row nor the tuple; (2) a nil
// callback persists both; (3) while (2) is open, a second pool connection sees
// NEITHER, and a concurrent SetRelationTargets on the same key from that
// connection blocks until the Transact commits — afterwards the stored targets
// are the second caller's, never a union.
func TestAmbientRowAndTupleCommitTogether(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	s := repos.Relationships
	table := appSpaces(t, db)

	// (1) Rollback: neither the row nor the tuple survives the injected error.
	err := db.Transact(ctx, func(ctx context.Context) error {
		if err := insertSpace(ctx, db, table, "S", "P"); err != nil {
			return err
		}
		if err := setParent(ctx, s, "S", "P"); err != nil {
			return err
		}
		return errInjectedHostFailure
	})
	if !errors.Is(err, errInjectedHostFailure) {
		t.Fatalf("Transact must return the injected error, got %v", err)
	}
	if _, ok := spaceParent(t, db, table, "S"); ok {
		t.Fatalf("app row survived the rollback")
	}
	if got := parentTargets(t, s, "S"); len(got) != 0 {
		t.Fatalf("tuple survived the rollback: %v — SetRelationTargets committed on its own connection", got)
	}

	// (2)+(3) Commit, with the outside observer and the blocked competitor.
	second := make(chan error, 1)
	err = db.Transact(ctx, func(ctx context.Context) error {
		if err := insertSpace(ctx, db, table, "S", "P"); err != nil {
			return err
		}
		if err := setParent(ctx, s, "S", "P"); err != nil {
			return err
		}
		// A second pool connection sees neither the row nor the tuple.
		if _, ok := spaceParent(t, db, table, "S"); ok {
			t.Fatalf("app row visible outside the open transaction")
		}
		if got := parentTargets(t, s, "S"); len(got) != 0 {
			t.Fatalf("tuple visible outside the open transaction: %v", got)
		}
		// A competing move on the SAME key from the pool: it must block on the
		// advisory lock the ambient transaction holds, for as long as the
		// transaction is open.
		go func() { second <- setParent(context.Background(), s, "S", "P2") }()
		waitForAdvisoryWaiter(t, db)
		select {
		case err := <-second:
			t.Fatalf("competing SetRelationTargets completed while the host transaction was still open (err=%v) — the lock did not widen to the host's commit", err)
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("commit shape: %v", err)
	}
	// The commit releases the lock; the competitor now finishes and its state wins.
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("competing SetRelationTargets after commit: %v", err)
		}
	case <-time.After(ambientWait):
		t.Fatalf("competing SetRelationTargets did not complete within %s of the commit — lock leaked past the commit", ambientWait)
	}
	if parent, ok := spaceParent(t, db, table, "S"); !ok || parent != "P" {
		t.Fatalf("app row after commit: parent=%q ok=%v, want P", parent, ok)
	}
	if got := parentTargets(t, s, "S"); len(got) != 1 || got[0] != "P2" {
		t.Fatalf("targets after the serialized competitor: %v, want exactly [P2] (never a union with P)", got)
	}
}
