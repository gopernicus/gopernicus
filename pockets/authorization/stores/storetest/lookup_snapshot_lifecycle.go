package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

// RunLookupSnapshotLifecycle checks model scope and cleanup on every callback exit.
func RunLookupSnapshotLifecycle(t *testing.T, factory func(*testing.T) authorization.Repositories) {
	for _, exit := range []string{"success", "error", "cancel", "panic"} {
		t.Run("lifetime/"+exit, func(t *testing.T) {
			repos := factory(t)
			scoped := repos.Relationships.ForModel(relationships.ReadModel{})
			source, ok := scoped.(relationships.LookupSnapshotter)
			if !ok {
				t.Fatal("model-scoped reader does not provide lookup snapshots")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("callback failure")
			var retained relationships.Reader
			var got error
			var panicked bool
			func() {
				defer func() {
					if p := recover(); p != nil {
						if p != failure {
							panic(p)
						}
						panicked = true
					}
				}()
				got = source.ReadLookupSnapshot(ctx, func(ctx context.Context, r relationships.Reader) error {
					retained = r
					switch exit {
					case "error":
						return failure
					case "cancel":
						cancel()
						assertLookupReaderError(t, ctx, r, context.Canceled)
					case "panic":
						panic(failure)
					}
					return nil
				})
			}()
			if (exit == "panic") != panicked {
				t.Fatalf("panic=%v", panicked)
			}
			want := map[string]error{"error": failure, "cancel": context.Canceled}[exit]
			if !errors.Is(got, want) {
				t.Fatalf("got %v want %v", got, want)
			}
			assertLookupReaderError(t, t.Context(), retained, tuplecache.ErrSnapshotClosed)
			// A fresh callback after every exit proves the adapter cleaned up. SQL
			// SQLite fixture uses one connection, so leaked transactions time out.
			fresh, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			if err := source.ReadLookupSnapshot(fresh, func(ctx context.Context, r relationships.Reader) error {
				_, err := r.LookupResourceIDs(ctx, "space", []string{"viewer"}, "user", "alice", "", 10)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("scope and invalid calls", func(t *testing.T) {
		repos := factory(t)
		row := relationships.CreateRelationship{ResourceType: "space", ResourceID: "a", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
		if err := repos.Relationships.CreateRelationships(t.Context(), []relationships.CreateRelationship{row}); err != nil {
			t.Fatal(err)
		}
		for _, allowed := range []bool{false, true} {
			model := relationships.ReadModel{}
			if allowed {
				model = relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "space", Relation: "viewer", SubjectType: "user"}})
			}
			source := repos.Relationships.ForModel(model).(relationships.LookupSnapshotter)
			if err := source.ReadLookupSnapshot(t.Context(), func(ctx context.Context, r relationships.Reader) error {
				ids, err := r.LookupResourceIDs(ctx, "space", []string{"viewer"}, "user", "alice", "", 10)
				if err != nil || (len(ids) == 1) != allowed {
					t.Fatalf("scoped lookup=%v/%v allowed=%v", ids, err, allowed)
				}
				held, err := r.CheckRelationWithGroupExpansion(ctx, "space", "a", "viewer", "user", "alice", 100)
				if err != nil || held != allowed {
					t.Fatalf("scoped check=%v/%v allowed=%v", held, err, allowed)
				}
				targets, err := r.GetRelationTargets(ctx, "space", "a", "viewer")
				if err != nil || (len(targets) == 1) != allowed {
					t.Fatalf("scoped targets=%v/%v", targets, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := source.ReadLookupSnapshot(canceled, func(context.Context, relationships.Reader) error { t.Fatal("canceled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := source.ReadLookupSnapshot(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatal(err)
			}
		}
	})
}

func assertLookupReaderError(t *testing.T, ctx context.Context, r relationships.Reader, want error) {
	t.Helper()
	if r == nil {
		t.Fatal("missing scoped reader")
	}
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("reader error %v; want %v", err, want)
		}
	}
	_, err := r.LookupResourceIDs(ctx, "space", []string{"viewer"}, "user", "alice", "", 10)
	check(err)
	_, err = r.LookupResourceIDsByRelationTarget(ctx, "space", "parent", "space", []string{"a"}, "", 10)
	check(err)
	_, err = r.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"a"}, "", 10)
	check(err)
	_, err = r.CheckRelationWithGroupExpansion(ctx, "space", "a", "viewer", "user", "alice", 100)
	check(err)
	_, err = r.CheckBatchDirect(ctx, "space", []string{"a"}, "viewer", "user", "alice", 100)
	check(err)
	_, err = r.GetRelationTargets(ctx, "space", "a", "parent")
	check(err)
	sets, ok := r.(relationships.RelationSetReader)
	if !ok {
		t.Fatal("snapshot lost set-reader capability")
	}
	_, err = sets.FilterRelation(ctx, "space", []string{"a"}, "viewer", "user", "alice", 100)
	check(err)
	_, err = sets.RelationTargetsFor(ctx, "space", []string{"a"}, "parent")
	check(err)
}
