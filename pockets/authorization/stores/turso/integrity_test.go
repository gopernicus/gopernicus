package turso

import (
	"context"
	"errors"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestLocalConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, policy mutations.IntegrityPolicy) storetest.Repositories {
		db := canonicalFixture(t, false)
		repos, err := testRepositories(t.Context(), db, WithIntegrityPolicy(policy))
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestIntegrityFailurePreservesAmbientHostWork(t *testing.T) {
	db := canonicalFixture(t, false)
	r, err := testRepositories(t.Context(), db, WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()), WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithSource(t.Context(), audit.Source{System: "integrity-test"})
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
	records, err := r.Audit.List(ctx, audit.Filter{}, list.Request{})
	if err != nil || len(records.Items) != 2 {
		t.Fatalf("failed deletion recorded audit: %+v %v", records, err)
	}
}

func TestIntegrityIgnoresTemporaryTupleShadow(t *testing.T) {
	db := canonicalFixture(t, false)
	r, err := testRepositories(t.Context(), db, WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
	if err != nil {
		t.Fatal(err)
	}
	owner := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}
	if err := r.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{owner}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transact(t.Context(), func(ctx context.Context) error {
		tx, ok := tursodb.TxFromContext(ctx)
		if !ok {
			t.Fatal("missing ambient transaction")
		}
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE iam_tuples AS SELECT * FROM main.iam_tuples`); err != nil {
			return err
		}
		if err := r.Tuples.DeleteScope(ctx, owner.Scope); !errors.Is(err, mutations.ErrInvariantBlocked) {
			t.Fatalf("temporary owner bypassed real integrity: %v", err)
		}
		_, err := tx.Exec(ctx, `DROP TABLE temp.iam_tuples`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if exists, err := r.Tuples.Contains(t.Context(), owner); err != nil || !exists {
		t.Fatalf("real owner deleted: %v %v", exists, err)
	}
}
