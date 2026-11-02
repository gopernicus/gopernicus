package pgx

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// TestCanonicalSQLReadsInsideSnapshotWithSettings is #306's case: a
// tenant-scoped transaction (pgxdb settings) that reads grants and then
// writes, atomically. TransactWithSettings is READ COMMITTED, so the canonical
// reads refuse it; TransactSnapshotWithSettings is REPEATABLE READ, so they
// run, see the tenant setting, and the write joins the same transaction.
func TestCanonicalSQLReadsInsideSnapshotWithSettings(t *testing.T) {
	db := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	comps, err := authorization.New(repos.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	seat := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{seat}}); err != nil {
		t.Fatal(err)
	}
	tenant := []pgxdb.Setting{{Name: "app.tenant_id", Value: "acme"}}

	if err := db.TransactWithSettings(t.Context(), tenant, func(ctx context.Context) error {
		_, err := comps.Relationships.ListRelationshipsBySubject(ctx, "user", "alice", relationships.SubjectRelationshipFilter{}, list.Request{})
		if !errors.Is(err, tuples.ErrSnapshotIsolation) {
			t.Errorf("read under TransactWithSettings = %v, want ErrSnapshotIsolation", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.TransactSnapshotWithSettings(t.Context(), tenant, func(ctx context.Context) error {
		tx, _ := db.TxFrom(ctx)
		var bound string
		if err := tx.QueryRow(ctx, "SELECT current_setting('app.tenant_id')").Scan(&bound); err != nil || bound != "acme" {
			t.Errorf("tenant setting inside = %q, %v", bound, err)
		}
		page, err := comps.Relationships.ListRelationshipsBySubject(ctx, "user", "alice", relationships.SubjectRelationshipFilter{}, list.Request{})
		if err != nil {
			return err
		}
		if len(page.Items) != 1 {
			t.Errorf("grants read inside = %d, want 1", len(page.Items))
		}
		// Last seat: remove it in the same transaction.
		return repos.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{seat}})
	}); err != nil {
		t.Fatalf("TransactSnapshotWithSettings: %v", err)
	}
	page, err := comps.Relationships.ListRelationshipsBySubject(t.Context(), "user", "alice", relationships.SubjectRelationshipFilter{}, list.Request{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("after commit: %d grants, %v; want the seat removed", len(page.Items), err)
	}
}
