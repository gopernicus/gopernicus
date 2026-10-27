package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// foreignTimeout bounds every foreign-instance callback so a misrouted lock
// wait fails instead of hanging.
const foreignTimeout = 30 * time.Second

var errForeignRefused = errors.New("foreign workflow refused")

// openForeign is a second *pgxdb.DB on the same DSN: a different instance, so
// its ambient transaction is invisible to the store. It is a bare Open — no
// migration or truncation of its own on the store's schema.
func openForeign(t *testing.T) *pgxdb.DB {
	t.Helper()
	foreign, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: sdk.Secret(requireDSN(t)), LockTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open foreign: %v", err)
	}
	t.Cleanup(func() { _ = foreign.Close() })
	return foreign
}

func boundedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), foreignTimeout)
	t.Cleanup(cancel)
	return ctx
}

func foreignRelationship(id string) relationships.CreateRelationship {
	return relationships.CreateRelationship{ResourceType: "doc", ResourceID: id, Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
}

// inForeignSnapshot runs fn inside foreign.TransactSnapshot after pinning its
// snapshot, so anything the store reads through the foreign transaction cannot
// see a row committed afterwards.
func inForeignSnapshot(t *testing.T, foreign *pgxdb.DB, fn func(ctx context.Context)) {
	t.Helper()
	err := foreign.TransactSnapshot(boundedContext(t), func(ctx context.Context) error {
		var pinned bool
		if err := foreign.QuerierFrom(ctx).QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+qualify(t, "iam_tuples")+")").Scan(&pinned); err != nil {
			return err
		}
		fn(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("foreign.TransactSnapshot: %v", err)
	}
}

// inForeignTransact runs fn inside foreign.Transact (the server's default READ
// COMMITTED) and returns fn's error unchanged.
func inForeignTransact(t *testing.T, foreign *pgxdb.DB, fn func(ctx context.Context) error) error {
	t.Helper()
	return foreign.Transact(boundedContext(t), fn)
}

// TestForeignInstanceTransactionIsInvisible: a store over one *pgxdb.DB called
// inside ANOTHER instance's transaction routes every site to its own pool —
// it neither joins nor refuses the foreign transaction — and the store's own
// instance still nests independently around a foreign one.
func TestForeignInstanceTransactionIsInvisible(t *testing.T) {
	db, repos := liveReposWith(t)
	foreign := openForeign(t)

	t.Run("reads/Recorded", func(t *testing.T) {
		inForeignSnapshot(t, foreign, func(ctx context.Context) {
			cmd := grantCmd("recorded", "viewer", "alice")
			cmd.OperationID = "foreign-recorded"
			if _, err := repos.Mutations.Apply(boundedContext(t), cmd, nil); err != nil {
				t.Fatalf("Apply fixture: %v", err)
			}
			if _, found, err := repos.Operations.Recorded(ctx, cmd.OperationID); err != nil || !found {
				t.Fatalf("Recorded in a foreign snapshot = found %t, %v; want the committed record (read the foreign transaction)", found, err)
			}
		})
	})
	t.Run("reads/read_model", func(t *testing.T) {
		inForeignSnapshot(t, foreign, func(ctx context.Context) {
			if err := repos.Relationships.CreateRelationships(boundedContext(t), []relationships.CreateRelationship{foreignRelationship("read-model")}); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "read-model", "viewer")
			if err != nil || len(targets) != 1 {
				t.Fatalf("GetRelationTargets in a foreign snapshot = %v, %v; want one target (read the foreign transaction)", targets, err)
			}
		})
	})
	t.Run("reads/tuples.reader", func(t *testing.T) {
		inForeignSnapshot(t, foreign, func(ctx context.Context) {
			fact := foreignRelationship("tuples-reader").Tuple()
			if err := repos.Tuples.ApplyTuples(boundedContext(t), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if found, err := repos.Tuples.Contains(ctx, fact); err != nil || !found {
				t.Fatalf("Contains in a foreign snapshot = %t, %v; want true (read the foreign transaction)", found, err)
			}
		})
	})
	t.Run("reads/rawView", func(t *testing.T) {
		inForeignSnapshot(t, foreign, func(ctx context.Context) {
			if err := repos.Relationships.CreateRelationships(boundedContext(t), []relationships.CreateRelationship{foreignRelationship("raw-view")}); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if found, err := repos.Relationships.CheckRelationExists(ctx, "doc", "raw-view", "viewer", "user", "alice"); err != nil || !found {
				t.Fatalf("CheckRelationExists in a foreign snapshot = %t, %v; want true (read the foreign transaction)", found, err)
			}
		})
	})
	t.Run("ReadTupleSnapshot routing", func(t *testing.T) {
		inForeignSnapshot(t, foreign, func(ctx context.Context) {
			fact := foreignRelationship("snapshot-routing").Tuple()
			if err := repos.Tuples.ApplyTuples(boundedContext(t), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if err := repos.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
				if found, err := r.Contains(ctx, fact); err != nil || !found {
					t.Fatalf("ReadTupleSnapshot in a foreign snapshot: Contains = %t, %v; want true (borrowed the foreign snapshot)", found, err)
				}
				return nil
			}); err != nil {
				t.Fatalf("ReadTupleSnapshot in a foreign snapshot = %v", err)
			}
		})
	})
	t.Run("ReadTupleSnapshot isolation", func(t *testing.T) {
		err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			return repos.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
				_, err := r.Contains(ctx, foreignRelationship("snapshot").Tuple())
				return err
			})
		})
		if err != nil {
			t.Fatalf("ReadTupleSnapshot inside a foreign READ COMMITTED transaction = %v, want its own snapshot (judged the foreign isolation)", err)
		}
	})
	t.Run("CacheableContext", func(t *testing.T) {
		source := &tupleSource{db: db}
		if err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			if !source.CacheableContext(ctx) {
				t.Fatal("CacheableContext inside a foreign transaction = false, want true")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("WithTupleCache construction", func(t *testing.T) {
		cacheDB, cfg := cacheFixture(t, true)
		if err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			_, err := testRepositories(ctx, cacheDB, cacheOptions(cfg)...)
			return err
		}); err != nil {
			t.Fatalf("WithTupleCache construction inside a foreign transaction = %v, want success", err)
		}
	})
	t.Run("Apply", func(t *testing.T) {
		if err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			_, err := repos.Mutations.Apply(ctx, grantCmd("apply", "viewer", "alice"), nil)
			return err
		}); err != nil {
			t.Fatalf("Apply inside a foreign transaction = %v, want a result", err)
		}
	})
	t.Run("Fence", func(t *testing.T) {
		if err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			_, err := repos.Operations.Fence(ctx, "foreign-fence")
			return err
		}); err != nil {
			t.Fatalf("Fence inside a foreign transaction = %v, want a result", err)
		}
	})
	t.Run("Expire", func(t *testing.T) {
		if err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			_, err := repos.OperationExpiry.Expire(ctx, time.Now(), 10)
			return err
		}); err != nil {
			t.Fatalf("Expire inside a foreign transaction = %v, want a count", err)
		}
	})
	t.Run("writes", func(t *testing.T) {
		rel := foreignRelationship("write")
		err := inForeignTransact(t, foreign, func(ctx context.Context) error {
			if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{rel}); err != nil {
				return err
			}
			return errForeignRefused
		})
		if err != errForeignRefused {
			t.Fatalf("foreign callback = %v, want the injected refusal", err)
		}
		if found, err := repos.Tuples.Contains(boundedContext(t), rel.Tuple()); err != nil || !found {
			t.Fatalf("write after the foreign rollback = %t, %v; want kept (joined the foreign transaction)", found, err)
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
	})
}
