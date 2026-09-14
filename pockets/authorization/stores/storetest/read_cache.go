package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

// RunReadSnapshots checks the common source contract independently of cache byte
// storage. The factory returns an empty enabled authority with all writer ports.
func RunReadSnapshots(t *testing.T, factory func(*testing.T) authorization.Repositories) {
	t.Helper()
	t.Run("snapshot fact isolation and lifetime", func(t *testing.T) {
		repos := factory(t)
		ctx := context.Background()
		source := repos.CacheSource
		if source == nil || source.CacheBinding() == "" {
			t.Fatal("missing source binding")
		}
		for _, reader := range []any{repos.Relationships, repos.Roles} {
			bound, ok := reader.(interface{ CacheBinding() string })
			if !ok || bound.CacheBinding() != source.CacheBinding() {
				t.Fatal("fact binding mismatch")
			}
		}
		initial, err := source.Observe(ctx)
		if err != nil || initial.Validate() != nil {
			t.Fatalf("initial version: %+v/%v", initial, err)
		}
		tuple := relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}
		if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
			t.Fatal(err)
		}
		assignment := roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer", ResourceType: "document", ResourceID: "d1"}
		if err := repos.Roles.Assign(ctx, assignment); err != nil {
			t.Fatal(err)
		}
		before, err := source.Observe(ctx)
		if err != nil || before.Epoch != initial.Epoch || before.Generation <= initial.Generation {
			t.Fatalf("writes did not advance version: %+v/%v", before, err)
		}
		model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "document", Relation: "viewer", SubjectType: "user"}})
		var retained decisions.CheckReads
		var scoped relationships.CheckReader
		writeCtx := ctx
		err = source.ReadSnapshot(ctx, func(ctx context.Context, v decisions.CacheVersion, reads decisions.CheckReads) error {
			retained = reads
			scoped = reads.ForChecks(model)
			if v != before {
				t.Fatalf("snapshot version=%+v want=%+v", v, before)
			}
			// Completing writes during the callback proves it does not hold the source's
			// write lock; subsequent reads must still see the original generation.
			if err := repos.Relationships.DeleteResourceRelationships(writeCtx, "document", "d1"); err != nil {
				return err
			}
			if err := repos.Roles.Unassign(writeCtx, assignment.SubjectType, assignment.SubjectID, assignment.Role, assignment.ResourceType, assignment.ResourceID); err != nil {
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
			held, err = reads.HasExactRole(ctx, "user", "u1", "viewer", "document", "d1")
			if err != nil || !held {
				t.Fatalf("snapshot role=%v/%v", held, err)
			}
			denied, err := reads.ForChecks(relationships.ReadModel{}).CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "u1", 100)
			if err != nil || denied {
				t.Fatalf("zero model granted=%v/%v", denied, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSnapshotClosed(t, ctx, retained, scoped, model)
		after, err := source.Observe(ctx)
		if err != nil || after.Generation <= before.Generation {
			t.Fatalf("delete version=%+v/%v", after, err)
		}
	})
	for _, exit := range []string{"error", "cancel", "panic"} {
		t.Run("close on "+exit, func(t *testing.T) {
			source := factory(t).CacheSource
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var retained decisions.CheckReads
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
				got = source.ReadSnapshot(ctx, func(ctx context.Context, _ decisions.CacheVersion, reads decisions.CheckReads) error {
					retained = reads
					scoped = reads.ForChecks(relationships.ReadModel{})
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

func assertSnapshotClosed(t *testing.T, ctx context.Context, reads decisions.CheckReads, scoped relationships.CheckReader, model relationships.ReadModel) {
	t.Helper()
	if _, err := reads.HasExactRole(ctx, "user", "u1", "viewer", "document", "d1"); !errors.Is(err, decisions.ErrSnapshotClosed) {
		t.Fatalf("retained role: %v", err)
	}
	for _, reader := range []relationships.CheckReader{scoped, reads.ForChecks(model)} {
		if _, err := reader.CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "u1", 100); !errors.Is(err, decisions.ErrSnapshotClosed) {
			t.Fatalf("retained relation: %v", err)
		}
		if _, err := reader.GetRelationTargets(ctx, "document", "d1", "viewer"); !errors.Is(err, decisions.ErrSnapshotClosed) {
			t.Fatalf("retained targets: %v", err)
		}
		if _, err := reader.CheckBatchDirect(ctx, "document", []string{"d1"}, "viewer", "user", "u1", 100); !errors.Is(err, decisions.ErrSnapshotClosed) {
			t.Fatalf("retained batch: %v", err)
		}
	}
}
