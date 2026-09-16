package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
)

// RunReadSnapshots checks the common source contract independently of cache byte
// storage. The factory returns an empty enabled authority with all writer ports.
func RunReadSnapshots(t *testing.T, factory func(*testing.T) Repositories) {
	t.Helper()
	t.Run("snapshot fact isolation and lifetime", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()
		source := repos.TupleSource
		if source == nil || source.Binding() == "" {
			t.Fatal("missing source binding")
		}
		for _, reader := range []any{repos.Relationships, repos.Tuples} {
			bound, ok := reader.(interface{ TupleCacheBinding() string })
			if !ok || bound.TupleCacheBinding() != source.Binding() {
				t.Fatal("fact binding mismatch")
			}
		}
		tuple := relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}
		if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
			t.Fatal(err)
		}
		assignment := roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer", Scope: fixtureScope("document", "d1")}
		if err := assignRole(ctx, repos.Tuples, assignment); err != nil {
			t.Fatal(err)
		}
		model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "document", Relation: "viewer", SubjectType: "user"}})
		var retained tuplecache.CheckReads
		var scoped relationships.CheckReader
		writeCtx := ctx
		err := source.ReadSnapshot(ctx, func(ctx context.Context, reads tuplecache.CheckReads) error {
			retained = reads
			scoped = reads.(relationships.CheckReadSource).ForChecks(model)
			// Completing writes during the callback proves it does not hold the source's
			// write lock; subsequent reads must still see the original snapshot.
			if err := repos.Relationships.DeleteResourceRelationships(writeCtx, "document", "d1"); err != nil {
				return err
			}
			if err := unassignRole(writeCtx, repos.Tuples, assignment.SubjectType, assignment.SubjectID, assignment.Role, assignment.Scope.Type, assignment.Scope.ID); err != nil {
				return err
			}
			held, err := scoped.CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "u1", 100)
			if err != nil || !held {
				t.Fatalf("snapshot relation=%v/%v", held, err)
			}
			targets, err := scoped.GetRelationTargets(ctx, "document", "d1", "viewer")
			if err != nil || len(targets) != 1 {
				t.Fatalf("snapshot targets=%v/%v", targets, err)
			}
			batch, err := scoped.CheckBatchDirect(ctx, "document", []string{"d1", "d2", "d1"}, "viewer", "user", "u1", 100)
			if err != nil || !batch["d1"] || batch["d2"] {
				t.Fatalf("snapshot batch=%v/%v", batch, err)
			}
			if sets, ok := scoped.(relationships.RelationSetReader); ok {
				filtered, err := sets.FilterRelation(ctx, "document", []string{"d1", "d2", "d1"}, "viewer", "user", "u1", 100)
				if err != nil || len(filtered) != 1 || filtered[0] != "d1" {
					t.Fatalf("snapshot filter=%v/%v", filtered, err)
				}
				targetSet, err := sets.RelationTargetsFor(ctx, "document", []string{"d1", "d2", "d1"}, "viewer")
				if err != nil || len(targetSet) != 1 || len(targetSet["d1"]) != 1 {
					t.Fatalf("snapshot target set=%v/%v", targetSet, err)
				}
				zero := reads.(relationships.CheckReadSource).ForChecks(relationships.ReadModel{}).(relationships.RelationSetReader)
				if targets, err := zero.RelationTargetsFor(ctx, "document", []string{"d1"}, "viewer"); err != nil || len(targets) != 0 {
					t.Fatalf("zero model target set=%v/%v", targets, err)
				}
				if ids, err := zero.FilterRelation(ctx, "document", []string{"d1"}, "viewer", "user", "u1", 100); err != nil || len(ids) != 0 {
					t.Fatalf("zero model filter=%v/%v", ids, err)
				}
			}

			held, err = reads.Contains(ctx, roleFact("user", "u1", "viewer", "document", "d1"))
			if err != nil || !held {
				t.Fatalf("snapshot role=%v/%v", held, err)
			}
			denied, err := reads.(relationships.CheckReadSource).ForChecks(relationships.ReadModel{}).CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "u1", 100)
			if err != nil || denied {
				t.Fatalf("zero model granted=%v/%v", denied, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSnapshotClosed(t, ctx, retained, scoped, model)

	})
	for _, exit := range []string{"error", "cancel", "panic"} {
		t.Run("close on "+exit, func(t *testing.T) {
			source := factory(t).TupleSource
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var retained tuplecache.CheckReads
			var scoped relationships.CheckReader
			failure := errors.New("callback failure")
			panicked := false
			var got error
			func() {
				defer func() {
					if value := recover(); value != nil {
						if value != failure {
							panic(value)
						}
						panicked = true
					}
				}()
				got = source.ReadSnapshot(ctx, func(ctx context.Context, reads tuplecache.CheckReads) error {
					retained = reads
					scoped = reads.(relationships.CheckReadSource).ForChecks(relationships.ReadModel{})
					switch exit {
					case "error":
						return failure
					case "cancel":
						cancel()
						return nil
					default:
						panic(failure)
					}
				})
			}()
			switch exit {
			case "error":
				if !errors.Is(got, failure) {
					t.Fatal(got)
				}
			case "cancel":
				if !errors.Is(got, context.Canceled) {
					t.Fatal(got)
				}
			case "panic":
				if !panicked {
					t.Fatal("callback panic swallowed")
				}
			}
			assertSnapshotClosed(t, context.Background(), retained, scoped, relationships.ReadModel{})
		})
	}
}

func assertSnapshotClosed(t *testing.T, ctx context.Context, reads tuplecache.CheckReads, scoped relationships.CheckReader, model relationships.ReadModel) {
	t.Helper()
	if _, err := reads.Contains(ctx, roleFact("user", "u1", "viewer", "document", "d1")); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
		t.Fatalf("retained role: %v", err)
	}
	for _, reader := range []relationships.CheckReader{scoped, reads.(relationships.CheckReadSource).ForChecks(model)} {
		if _, err := reader.CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "u1", 100); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
			t.Fatalf("retained relation: %v", err)
		}
		if _, err := reader.GetRelationTargets(ctx, "document", "d1", "viewer"); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
			t.Fatalf("retained targets: %v", err)
		}
		if _, err := reader.CheckBatchDirect(ctx, "document", []string{"d1"}, "viewer", "user", "u1", 100); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
			t.Fatalf("retained batch: %v", err)
		}
		sets, ok := reader.(relationships.RelationSetReader)
		if !ok {
			continue
		} // set reads are an optional capability
		for _, ids := range [][]string{nil, {"d1"}} {
			if _, err := sets.FilterRelation(ctx, "document", ids, "viewer", "user", "u1", 100); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
				t.Fatalf("retained filter: %v", err)
			}
			if _, err := sets.RelationTargetsFor(ctx, "document", ids, "viewer"); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
				t.Fatalf("retained target set: %v", err)
			}
		}
	}
}
