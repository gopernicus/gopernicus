package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

type interleavedCheckStore struct {
	relationships.Storer
	after func() error
}

func (s interleavedCheckStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return interleavedCheckReader{Reader: s.Storer.ForModel(model), after: s.after}
}

type interleavedCheckReader struct {
	relationships.Reader
	after func() error
}

func (r interleavedCheckReader) GetRelationTargets(ctx context.Context, rt, id, rel string) ([]relationships.RelationTarget, error) {
	targets, err := r.Reader.GetRelationTargets(ctx, rt, id, rel)
	if err == nil {
		err = r.after()
	}
	return targets, err
}

func (r interleavedCheckReader) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) ([]string, error) {
	return r.Reader.(relationships.RelationSetReader).FilterRelation(ctx, rt, ids, rel, st, sid, limit)
}

func (r interleavedCheckReader) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]relationships.RelationTarget, error) {
	targets, err := r.Reader.(relationships.RelationSetReader).RelationTargetsFor(ctx, rt, ids, rel)
	if err == nil {
		err = r.after()
	}
	return targets, err
}

func (r interleavedCheckReader) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, relationships.Reader) error) error {
	return r.Reader.(relationships.LookupSnapshotter).ReadLookupSnapshot(ctx, func(ctx context.Context, reader relationships.Reader) error {
		return fn(ctx, interleavedCheckReader{Reader: reader, after: r.after})
	})
}

type permissionChecks interface {
	Check(context.Context, authmodel.CheckRequest) (authmodel.CheckResult, error)
	CheckExplain(context.Context, authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error)
	CheckBatch(context.Context, []authmodel.CheckRequest) ([]authmodel.CheckResult, error)
	FilterAuthorized(context.Context, authmodel.PrincipalRef, string, string, []string) ([]string, error)
}

func checkSnapshotOperation(ctx context.Context, service permissionChecks, operation string) (bool, error) {
	req := authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Permission: "view", Resource: authmodel.Resource{Type: "document", ID: "a"}}
	switch operation {
	case "Check":
		result, err := service.Check(ctx, req)
		return result.Allowed, err
	case "CheckExplain":
		result, _, err := service.CheckExplain(ctx, req)
		return result.Allowed, err
	case "CheckBatch":
		second := req
		second.Resource.ID = "b"
		results, err := service.CheckBatch(ctx, []authmodel.CheckRequest{req, second})
		if err != nil {
			return false, err
		}
		if len(results) != 2 {
			return false, fmt.Errorf("batch returned %d results", len(results))
		}
		return results[0].Allowed || results[1].Allowed, nil
	default:
		ids, err := service.FilterAuthorized(ctx, req.Principal, req.Permission, req.Resource.Type, []string{"a", "b"})
		return len(ids) > 0, err
	}
}

// RunCheckSnapshots commits a graph change between a Through edge read and its
// permission read. No committed state grants either document: removing the old
// edge before granting its target must not produce an impossible in-flight grant.
// SQL fixtures commit the writer atomically on a separate connection. Memory's
// separate writes also have no granting intermediate state.
func RunCheckSnapshots(t *testing.T, factory func(*testing.T) (authorization.Repositories, transaction.Transactor)) {
	t.Helper()
	for _, surface := range []string{"relationships", "decisions"} {
		for _, operation := range []string{"Check", "CheckExplain", "CheckBatch", "FilterAuthorized"} {
			t.Run(surface+"/"+operation, func(t *testing.T) {
				repos, tx := factory(t)
				ctx := t.Context()
				writer := repos.Relationships
				var rows []relationships.CreateRelationship
				for _, id := range []string{"a", "b"} {
					rows = append(rows, relationships.CreateRelationship{ResourceType: "document", ResourceID: id, Relation: "parent", SubjectType: "space", SubjectID: "p"})
				}
				if err := writer.CreateRelationships(ctx, rows); err != nil {
					t.Fatal(err)
				}
				armed, changed := false, false
				repos.Relationships = interleavedCheckStore{Storer: writer, after: func() error {
					if !armed || changed {
						return nil
					}
					changed = true
					change := func(ctx context.Context) error {
						for _, row := range rows {
							if err := writer.DeleteRelationshipTarget(ctx, row.ResourceType, row.ResourceID, row.Relation, row.Subject()); err != nil {
								return err
							}
						}
						return writer.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "space", ResourceID: "p", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}})
					}
					if tx != nil {
						return tx.Transact(ctx, change)
					}
					return change(ctx)
				}}
				components, err := authorization.New(repos, authorization.WithRelationshipModel(lookupSnapshotSchema()))
				if err != nil {
					t.Fatal(err)
				}
				var service permissionChecks = components.Relationships
				if surface == "decisions" {
					service = components.Decisions
				}
				if allowed, err := checkSnapshotOperation(ctx, service, operation); err != nil || allowed {
					t.Fatalf("initial state: allowed=%v err=%v", allowed, err)
				}
				armed = true
				if allowed, err := checkSnapshotOperation(ctx, service, operation); err != nil || allowed || !changed {
					t.Fatalf("in-flight operation: allowed=%v changed=%v err=%v", allowed, changed, err)
				}
				if allowed, err := checkSnapshotOperation(ctx, service, operation); err != nil || allowed {
					t.Fatalf("committed state: allowed=%v err=%v", allowed, err)
				}
				if err := writer.CreateRelationships(ctx, rows); err != nil {
					t.Fatal(err)
				}
				if allowed, err := checkSnapshotOperation(ctx, service, operation); err != nil || !allowed {
					t.Fatalf("fresh granting state: allowed=%v err=%v", allowed, err)
				}
			})
		}
	}
}

// RunCheckAmbient proves ordinary decision snapshots preserve the host's pending
// writes and leave both commit and rollback to the enclosing transaction.
func RunCheckAmbient(t *testing.T, factory func(*testing.T) (authorization.Repositories, transaction.Transactor)) {
	t.Helper()
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			repos, tx := factory(t)
			components, err := authorization.New(repos, authorization.WithRelationshipModel(lookupSnapshotSchema()))
			if err != nil {
				t.Fatal(err)
			}
			aborted := errors.New("host aborts")
			err = tx.Transact(t.Context(), func(ctx context.Context) error {
				rows := []relationships.CreateRelationship{{ResourceType: "space", ResourceID: "p", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}}
				for _, id := range []string{"a", "b"} {
					rows = append(rows, relationships.CreateRelationship{ResourceType: "document", ResourceID: id, Relation: "parent", SubjectType: "space", SubjectID: "p"})
				}
				if err := repos.Relationships.CreateRelationships(ctx, rows); err != nil {
					return err
				}
				for _, operation := range []string{"Check", "CheckExplain", "CheckBatch", "FilterAuthorized"} {
					if allowed, err := checkSnapshotOperation(ctx, components.Decisions, operation); err != nil || !allowed {
						return fmt.Errorf("%s lost pending writes: %v/%v", operation, allowed, err)
					}
					if allowed, err := checkSnapshotOperation(t.Context(), components.Decisions, operation); err != nil || allowed {
						return fmt.Errorf("%s committed borrowed transaction: %v/%v", operation, allowed, err)
					}
				}
				if !commit {
					return aborted
				}
				return nil
			})
			want := error(nil)
			if !commit {
				want = aborted
			}
			if !errors.Is(err, want) {
				t.Fatalf("transaction=%v; want %v", err, want)
			}
			if allowed, err := checkSnapshotOperation(t.Context(), components.Decisions, "CheckBatch"); err != nil || allowed != commit {
				t.Fatalf("after transaction: %v/%v; committed=%v", allowed, err, commit)
			}
		})
	}
}
