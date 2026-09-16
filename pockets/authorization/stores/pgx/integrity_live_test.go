package pgx

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestIntegrityRejectsStaleAmbientOwners(t *testing.T) {
	db, r := liveReposWith(t, WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()), WithAudit())
	ctx := auditContext()
	a := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}
	b := a
	b.Subject.ID = "b"
	if err := r.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{a, b}}); err != nil {
		t.Fatal(err)
	}
	err := db.TransactSnapshot(ctx, func(ambient context.Context) error {
		facts, err := r.Tuples.Lookup(ambient, tuples.Query{Scope: &a.Scope})
		if err != nil || len(facts) != 2 {
			t.Fatalf("pin owners: %v %v", facts, err)
		}
		// Another committed writer removes b after this transaction's snapshot.
		if err := r.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{b}}); err != nil {
			return err
		}
		err = r.Tuples.ApplyTuples(ambient, tuples.Changes{Remove: []tuples.Tuple{a}})
		if !errors.Is(err, mutations.ErrConcurrentMutation) {
			t.Fatalf("stale owner count accepted: %v", err)
		}
		return nil // The failed write rolled back to its savepoint, including audit.
	})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := r.Tuples.Lookup(ctx, tuples.Query{Scope: &a.Scope})
	if err != nil || len(facts) != 1 || facts[0] != a {
		t.Fatalf("stale write orphaned resource: %v %v", facts, err)
	}
	if records := auditRecords(t, r); len(records) != 3 {
		t.Fatalf("failed write leaked audit: %+v", records)
	}
}

func TestIntegrityFailurePreservesAmbientHostWork(t *testing.T) {
	db, r := liveReposWith(t, WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()), WithAudit())
	ctx := auditContext()
	owner := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}
	if err := r.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{owner}}); err != nil {
		t.Fatal(err)
	}
	other := owner
	other.Scope.ID = "host"
	if err := db.Transact(ctx, func(ambient context.Context) error {
		if err := r.Tuples.ApplyTuples(ambient, tuples.Changes{Add: []tuples.Tuple{other}}); err != nil {
			return err
		}
		if err := r.Tuples.DeleteScope(ambient, owner.Scope); !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("last owner was removed: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := r.Tuples.Lookup(ctx, tuples.Query{})
	if err != nil || len(facts) != 2 {
		t.Fatalf("savepoint lost host work or owner: %v %v", facts, err)
	}
	if records := auditRecords(t, r); len(records) != 2 {
		t.Fatalf("failed deletion recorded audit: %+v", records)
	}
}
