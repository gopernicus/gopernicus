package turso

import (
	"context"
	"errors"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// foreignTimeout bounds every foreign-instance callback: generous, because
// Tx.Commit uses the Begin context, and finite, so a misrouted busy wait fails
// instead of hanging.
const foreignTimeout = 30 * time.Second

var errForeignRefused = errors.New("foreign workflow refused")

func boundedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), foreignTimeout)
	t.Cleanup(cancel)
	return ctx
}

func foreignRelationship(id string) relationships.CreateRelationship {
	return relationships.CreateRelationship{ResourceType: "doc", ResourceID: id, Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
}

func foreignGrant(resourceID string) mutations.Command {
	return mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: resourceID}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "alice"}}}}
}

func tupleRows(t *testing.T, db *tursodb.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(boundedContext(t), "SELECT count(*) FROM main.iam_tuples").Scan(&n); err != nil {
		t.Fatalf("count iam_tuples: %v", err)
	}
	return n
}

// TestForeignInstanceTransactionIsInvisible: the store and the foreign
// instance are two migrated SQLite files with the same schema, so a store that
// joined the foreign transaction would silently read and write the wrong
// database. Every site must route to the store's own file instead — neither
// joining nor refusing the foreign transaction — and the store's own instance
// still nests independently around a foreign one.
func TestForeignInstanceTransactionIsInvisible(t *testing.T) {
	db := canonicalFixture(t, false)
	foreign := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	inForeign := func(t *testing.T, fn func(ctx context.Context) error) error {
		t.Helper()
		return foreign.Transact(boundedContext(t), fn)
	}

	t.Run("reads/Recorded", func(t *testing.T) {
		cmd := foreignGrant("recorded")
		cmd.OperationID = "foreign-recorded"
		if _, err := repos.Mutations.Apply(boundedContext(t), cmd, nil); err != nil {
			t.Fatalf("Apply fixture: %v", err)
		}
		if err := inForeign(t, func(ctx context.Context) error {
			if _, found, err := repos.Operations.Recorded(ctx, cmd.OperationID); err != nil || !found {
				t.Fatalf("Recorded inside a foreign transaction = found %t, %v; want the store's record (read the foreign database)", found, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reads/read_model", func(t *testing.T) {
		if err := repos.Relationships.CreateRelationships(boundedContext(t), []relationships.CreateRelationship{foreignRelationship("read-model")}); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := inForeign(t, func(ctx context.Context) error {
			targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "read-model", "viewer")
			if err != nil || len(targets) != 1 {
				t.Fatalf("GetRelationTargets inside a foreign transaction = %v, %v; want one target (read the foreign database)", targets, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reads/tuples.reader", func(t *testing.T) {
		fact := foreignRelationship("tuples-reader").Tuple()
		if err := repos.Tuples.ApplyTuples(boundedContext(t), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := inForeign(t, func(ctx context.Context) error {
			if found, err := repos.Tuples.Contains(ctx, fact); err != nil || !found {
				t.Fatalf("Contains inside a foreign transaction = %t, %v; want true (read the foreign database)", found, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reads/rawView", func(t *testing.T) {
		if err := repos.Relationships.CreateRelationships(boundedContext(t), []relationships.CreateRelationship{foreignRelationship("raw-view")}); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := inForeign(t, func(ctx context.Context) error {
			if found, err := repos.Relationships.CheckRelationExists(ctx, "doc", "raw-view", "viewer", "user", "alice"); err != nil || !found {
				t.Fatalf("CheckRelationExists inside a foreign transaction = %t, %v; want true (read the foreign database)", found, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ReadTupleSnapshot", func(t *testing.T) {
		fact := foreignRelationship("snapshot").Tuple()
		if err := repos.Tuples.ApplyTuples(boundedContext(t), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := inForeign(t, func(ctx context.Context) error {
			return repos.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
				if found, err := r.Contains(ctx, fact); err != nil || !found {
					t.Fatalf("ReadTupleSnapshot inside a foreign transaction: Contains = %t, %v; want true (snapshot of the foreign database)", found, err)
				}
				return nil
			})
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("CacheableContext", func(t *testing.T) {
		source := &tupleSource{db: db}
		if err := inForeign(t, func(ctx context.Context) error {
			if !source.CacheableContext(ctx) {
				t.Fatal("CacheableContext inside a foreign transaction = false, want true")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("WithTupleCache construction", func(t *testing.T) {
		cacheDB := canonicalFixture(t, true)
		if err := inForeign(t, func(ctx context.Context) error {
			_, err := testRepositories(ctx, cacheDB, WithTupleCache())
			return err
		}); err != nil {
			t.Fatalf("WithTupleCache construction inside a foreign transaction = %v, want success", err)
		}
	})
	t.Run("Apply", func(t *testing.T) {
		if err := inForeign(t, func(ctx context.Context) error {
			_, err := repos.Mutations.Apply(ctx, foreignGrant("apply"), nil)
			return err
		}); err != nil {
			t.Fatalf("Apply inside a foreign transaction = %v, want a result", err)
		}
	})
	t.Run("Fence", func(t *testing.T) {
		if err := inForeign(t, func(ctx context.Context) error {
			_, err := repos.Operations.Fence(ctx, "foreign-fence")
			return err
		}); err != nil {
			t.Fatalf("Fence inside a foreign transaction = %v, want a result", err)
		}
	})
	t.Run("Expire", func(t *testing.T) {
		if err := inForeign(t, func(ctx context.Context) error {
			_, err := repos.OperationExpiry.Expire(ctx, time.Now(), 10)
			return err
		}); err != nil {
			t.Fatalf("Expire inside a foreign transaction = %v, want a count", err)
		}
	})
	t.Run("writes", func(t *testing.T) {
		rel := foreignRelationship("write")
		err := inForeign(t, func(ctx context.Context) error {
			if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{rel}); err != nil {
				return err
			}
			return errForeignRefused
		})
		if err != errForeignRefused {
			t.Fatalf("foreign callback = %v, want the injected refusal", err)
		}
		if found, err := repos.Tuples.Contains(boundedContext(t), rel.Tuple()); err != nil || !found {
			t.Fatalf("write after the foreign rollback = %t, %v; want kept in the store's file (joined the foreign transaction)", found, err)
		}
		if n := tupleRows(t, foreign); n != 0 {
			t.Fatalf("foreign file holds %d iam_tuples rows, want 0 (the store wrote the wrong database)", n)
		}
	})
	t.Run("writes, foreign commits", func(t *testing.T) {
		rel := foreignRelationship("write-commit")
		if err := inForeign(t, func(ctx context.Context) error {
			return repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{rel})
		}); err != nil {
			t.Fatalf("foreign callback: %v", err)
		}
		if found, err := repos.Tuples.Contains(boundedContext(t), rel.Tuple()); err != nil || !found {
			t.Fatalf("write after the foreign commit = %t, %v; want it in the store's file (wrote the foreign database)", found, err)
		}
		if n := tupleRows(t, foreign); n != 0 {
			t.Fatalf("foreign file holds %d iam_tuples rows, want 0 (the store wrote the wrong database)", n)
		}
	})
	t.Run("own instance nests around a foreign one", func(t *testing.T) {
		rel := foreignRelationship("nested")
		err := db.Transact(boundedContext(t), func(ctx context.Context) error {
			if err := foreign.Transact(ctx, func(ctx context.Context) error {
				return repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{rel})
			}); err != nil {
				return err
			}
			return errForeignRefused
		})
		if err != errForeignRefused {
			t.Fatalf("db.Transact around foreign.Transact = %v, want the injected refusal (nesting refused)", err)
		}
		if found, err := repos.Tuples.Contains(boundedContext(t), rel.Tuple()); err != nil || found {
			t.Fatalf("write inside foreign inside db after db rolled back = %t, %v; want gone (store left db's transaction)", found, err)
		}
		if n := tupleRows(t, foreign); n != 0 {
			t.Fatalf("foreign file holds %d iam_tuples rows, want 0", n)
		}
	})
}
