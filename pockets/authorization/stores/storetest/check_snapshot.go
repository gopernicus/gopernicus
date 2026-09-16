package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type interleavedTupleStore struct {
	tuples.Storer
	after func() error
}

func (s interleavedTupleStore) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	return s.Storer.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
		return fn(ctx, interleavedTupleReader{Reader: r, after: s.after})
	})
}

type interleavedTupleReader struct {
	tuples.Reader
	after func() error
}

func (r interleavedTupleReader) Contains(ctx context.Context, f tuples.Tuple) (bool, error) {
	out, e := r.Reader.Contains(ctx, f)
	if e == nil {
		e = r.after()
	}
	return out, e
}
func (r interleavedTupleReader) ContainsMany(ctx context.Context, f []tuples.Tuple) ([]bool, error) {
	out, e := r.Reader.ContainsMany(ctx, f)
	if e == nil {
		e = r.after()
	}
	return out, e
}
func (r interleavedTupleReader) ReadSets(ctx context.Context, k []tuples.SetKey, n int) ([][]tuples.Tuple, error) {
	out, e := r.Reader.ReadSets(ctx, k, n)
	if e == nil {
		e = r.after()
	}
	return out, e
}
func (r interleavedTupleReader) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	out, e := r.Reader.Lookup(ctx, q)
	if e == nil {
		e = r.after()
	}
	return out, e
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
func RunCheckSnapshots(t *testing.T, factory func(*testing.T) (Repositories, transaction.Transactor)) {
	t.Helper()
	for _, surface := range []string{"decisions"} {
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
				repos.Tuples = interleavedTupleStore{Storer: repos.Tuples, after: func() error {
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
				components, err := authorization.New(repos.Repositories, authorization.WithModel(lookupSnapshotSchema()))
				if err != nil {
					t.Fatal(err)
				}
				var service permissionChecks = components.Decisions
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

// RunSnapshotCheckAmbient requires a snapshot-capable transactor and proves decision snapshots preserve the host's pending
// writes and leave both commit and rollback to the enclosing transaction.
func RunSnapshotCheckAmbient(t *testing.T, factory func(*testing.T) (Repositories, transaction.Transactor)) {
	t.Helper()
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			repos, tx := factory(t)
			components, err := authorization.New(repos.Repositories, authorization.WithModel(lookupSnapshotSchema()))
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
				page, err := roleListSubject(ctx, repos.Tuples, "user", "alice", list.Request{WithCount: true})
				if err != nil || len(page.Items) != 1 || (page.Total == nil || *page.Total != 1) {
					return fmt.Errorf("role listing lost pending fact: %+v/%v", page, err)
				}
				outside, err := roleListSubject(t.Context(), repos.Tuples, "user", "alice", list.Request{WithCount: true})
				if err != nil || len(outside.Items) != 0 || (outside.Total == nil || *outside.Total != 0) {
					return fmt.Errorf("role listing committed borrowed transaction: %+v/%v", outside, err)
				}
				for _, view := range []struct {
					ctx  context.Context
					want int
				}{{ctx, 1}, {t.Context(), 0}} {
					rels, err := components.Relationships.ListRelationshipsBySubject(view.ctx, "user", "alice", relationships.SubjectRelationshipFilter{}, list.Request{WithCount: true})
					if err != nil || len(rels.Items) != view.want || rels.Total == nil || *rels.Total != int64(view.want) {
						return fmt.Errorf("relationship listing lost transaction ownership: %+v/%v; want %d", rels, err, view.want)
					}
					count, err := components.Relationships.CountByResourceAndRelation(view.ctx, "space", "p", "viewer")
					if err != nil || count != view.want {
						return fmt.Errorf("relationship count lost transaction ownership: %d/%v; want %d", count, err, view.want)
					}
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
			page, err := roleListSubject(t.Context(), repos.Tuples, "user", "alice", list.Request{WithCount: true})
			if err != nil || (len(page.Items) == 1) != commit {
				t.Fatalf("role listing after transaction: %+v/%v; committed=%v", page, err, commit)
			}
			count, err := components.Relationships.CountByResourceAndRelation(t.Context(), "space", "p", "viewer")
			if err != nil || (count == 1) != commit {
				t.Fatalf("relationship count after transaction: %d/%v; committed=%v", count, err, commit)
			}
			if allowed, err := checkSnapshotOperation(t.Context(), components.Decisions, "CheckBatch"); err != nil || allowed != commit {
				t.Fatalf("after transaction: %v/%v; committed=%v", allowed, err, commit)
			}
		})
	}
}
