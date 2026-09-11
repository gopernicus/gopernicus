package relationships

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestConvergentTraversalCountsRepeatedWork(t *testing.T) {
	store := &graphStore{}
	const depth = 13
	previous := []string{"root"}
	for level := 0; level < depth; level++ {
		next := []string{fmt.Sprintf("%02d-a", level), fmt.Sprintf("%02d-b", level)}
		for _, from := range previous {
			for _, to := range next {
				store.tuples = append(store.tuples, tuple("space", from, "parent", "space", to, ""))
			}
		}
		previous = next
	}
	svc := newLimitedService(t, store, setHierarchySchema(), authmodel.EvaluationLimits{MaxThroughDepth: depth, MaxGraphStates: 27, MaxEvaluationSteps: 200})
	req := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "root"}}
	if _, err := svc.Check(context.Background(), req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("Check error=%v", err)
	}
	_, explanation, err := svc.CheckExplain(context.Background(), req)
	if !errors.Is(err, authmodel.ErrEvaluationLimit) || len(explanation.Steps) > 200 {
		t.Fatalf("Explain steps=%d error=%v", len(explanation.Steps), err)
	}
	// Two independent requests each memoize store reads. Repeated CPU work hits
	// the step ceiling instead of replaying exponentially many physical reads.
	for key, calls := range store.targetsCalls {
		if calls > 2 {
			t.Fatalf("%s read %d times", key, calls)
		}
	}
	for key, calls := range store.directCalls {
		if calls > 2 {
			t.Fatalf("%s read %d times", key, calls)
		}
	}
}

type changedLookupStore struct{ *fakeStore }
type changedLookupReader struct {
	Reader
	owner *fakeStore
}

func (s changedLookupStore) ForModel(model ReadModel) Reader {
	return changedLookupReader{Reader: s.fakeStore.ForModel(model), owner: s.fakeStore}
}

func (r changedLookupReader) LookupResourceIDs(ctx context.Context, rt string, relations []string, st, sid, after string, limit int) ([]string, error) {
	ids, err := r.Reader.LookupResourceIDs(ctx, rt, relations, st, sid, after, limit)
	// Controlled interleaving: reverse discovery saw both grants; the first is
	// revoked before the forward validation begins.
	r.owner.tuples = nil
	return ids, err
}

func TestLookupPageReturnsConflictWhenDiscoveredGrantChanges(t *testing.T) {
	schema := setHierarchySchema()
	def := schema.ResourceTypes["space"]
	def.Permissions["view"] = AnyOf(Direct("viewer"))
	schema.ResourceTypes["space"] = def
	store := changedLookupStore{&fakeStore{tuples: []CreateRelationship{
		tuple("space", "a", "viewer", "user", "u1", ""), tuple("space", "b", "viewer", "user", "u1", ""),
	}}}
	svc := newLimitedService(t, store, schema, authmodel.EvaluationLimits{})
	page, err := svc.LookupResourcesPage(context.Background(), setPrincipal, "view", "space", "", 1)
	if !errors.Is(err, sdk.ErrConflict) || len(page.IDs) != 0 || page.HasMore {
		t.Fatalf("page=%v error=%v", page, err)
	}
}

func TestLookupPageChecksLookaheadRootDepth(t *testing.T) {
	store := &graphStore{fakeStore: fakeStore{tuples: []CreateRelationship{
		tuple("space", "a", "viewer", "user", "u1", ""),
		tuple("space", "b", "parent", "space", "a", ""),
		tuple("space", "z", "parent", "space", "b", ""),
	}}}
	svc := newLimitedService(t, store, setHierarchySchema(), authmodel.EvaluationLimits{MaxThroughDepth: 1})
	if _, err := svc.LookupResourcesPage(context.Background(), setPrincipal, "view", "space", "a", 1); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("lookahead error=%v", err)
	}
}

func TestEvaluationStepBoundaryMatchesCheckBatch(t *testing.T) {
	for _, steps := range []int{1, 2, 3} {
		store := &fakeStore{tuples: []CreateRelationship{tuple("space", "root", "viewer", "user", "u1", "")}}
		schema := setHierarchySchema()
		definition := schema.ResourceTypes["space"]
		definition.Permissions["view"] = AnyOf(Direct("viewer"))
		schema.ResourceTypes["space"] = definition
		svc := newLimitedService(t, store, schema, authmodel.EvaluationLimits{MaxEvaluationSteps: steps})
		req := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "root"}}
		_, err := svc.Check(context.Background(), req)
		_, batchErr := svc.CheckBatch(context.Background(), []authmodel.CheckRequest{req, req})
		if (err != nil) != (steps < 2) || !errors.Is(batchErr, err) {
			t.Fatalf("steps=%d Check=%v batch=%v", steps, err, batchErr)
		}
	}
}

func TestFilterAndLookupKeepEachCandidatesRootDepth(t *testing.T) {
	store := &graphStore{fakeStore: fakeStore{tuples: []CreateRelationship{
		tuple("space", "root", "viewer", "user", "u1", ""),
		tuple("space", "middle", "parent", "space", "root", ""),
		tuple("space", "leaf", "parent", "space", "middle", ""),
	}}}
	svc := newLimitedService(t, store, setHierarchySchema(), authmodel.EvaluationLimits{MaxThroughDepth: 1})
	for _, ids := range [][]string{{"middle", "leaf"}, {"leaf", "middle"}} {
		if _, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "space", ids); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("Filter(%v)=%v", ids, err)
		}
	}
	if _, err := svc.LookupResources(context.Background(), setPrincipal, "view", "space"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("Lookup=%v", err)
	}
	if _, err := svc.LookupResourcesPage(context.Background(), setPrincipal, "view", "space", "", 2); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("page=%v", err)
	}
}

type failingBranchStore struct {
	*fakeStore
	calls [][]string
	fail  error
}
type failingBranchReader struct {
	Reader
	owner *failingBranchStore
}

type nilScopedStore struct{ *fakeStore }

func (nilScopedStore) ForModel(ReadModel) Reader { return nil }

func TestModelScopedReaderIsMandatory(t *testing.T) {
	if _, err := newService(nilScopedStore{&fakeStore{}}, setHierarchySchema(), serviceConfig{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil scoped reader accepted: %v", err)
	}
}

func TestThroughTargetOrderIsCanonicalAcrossEntryPoints(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		targets := []CreateRelationship{
			tuple("space", "root", "parent", "space", "a-too-deep", ""),
			tuple("space", "root", "parent", "space", "z-granted", ""),
		}
		if reverse {
			slices.Reverse(targets)
		}
		store := &graphStore{fakeStore: fakeStore{tuples: append(targets,
			tuple("space", "a-too-deep", "parent", "space", "deeper", ""),
			tuple("space", "z-granted", "viewer", "user", "u1", ""),
		)}}
		svc := newLimitedService(t, store, setHierarchySchema(), authmodel.EvaluationLimits{MaxThroughDepth: 1})
		req := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "root"}}
		_, checkErr := svc.Check(context.Background(), req)
		_, _, explainErr := svc.CheckExplain(context.Background(), req)
		_, batchErr := svc.CheckBatch(context.Background(), []authmodel.CheckRequest{req})
		_, filterErr := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "space", []string{"root"})
		_, guardErr := svc.EvaluateWith(context.Background(), modelStoreView{store}, req)
		for _, err := range []error{checkErr, explainErr, batchErr, filterErr, guardErr} {
			if !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("reverse=%v: error=%v, want canonical earlier target's depth failure", reverse, err)
			}
		}
	}
}

func (s *failingBranchStore) ForModel(model ReadModel) Reader {
	return failingBranchReader{Reader: s.fakeStore.ForModel(model), owner: s}
}

func (r failingBranchReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, rel, st, sid string, max int) (bool, error) {
	if rel == "z" {
		return false, r.owner.fail
	}
	return r.Reader.CheckRelationWithGroupExpansion(ctx, rt, rid, rel, st, sid, max)
}

func (r failingBranchReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) (map[string]bool, error) {
	if rel == "z" {
		r.owner.calls = append(r.owner.calls, slices.Clone(ids))
		return nil, r.owner.fail
	}
	return r.Reader.CheckBatchDirect(ctx, rt, ids, rel, st, sid, max)
}

func TestDirectBatchSkipsResolvedCandidatesAndBranches(t *testing.T) {
	schema := NewSchema([]ResourceSchema{{Name: "doc", Def: ResourceTypeDef{Relations: map[string]RelationDef{
		"a": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}, "z": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
	}, Permissions: map[string]PermissionRule{"view": AnyOf(Direct("a"), Direct("z"))}}}})
	store := &failingBranchStore{fakeStore: &fakeStore{tuples: []CreateRelationship{tuple("doc", "granted", "a", "user", "u1", "")}}, fail: errors.New("later branch failed")}
	svc := newLimitedService(t, store, schema, authmodel.EvaluationLimits{})
	request := func(id string) authmodel.CheckRequest {
		return authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: id}}
	}
	results, err := svc.CheckBatch(context.Background(), []authmodel.CheckRequest{request("granted"), request("granted")})
	if err != nil || len(results) != 2 || !results[0].Allowed || results[0].Reason != "direct:a" || len(store.calls) != 0 {
		t.Fatalf("resolved batch=%v error=%v calls=%v", results, err, store.calls)
	}
	_, err = svc.CheckBatch(context.Background(), []authmodel.CheckRequest{request("granted"), request("pending"), request("granted"), request("pending")})
	if !errors.Is(err, store.fail) || len(store.calls) != 1 || !slices.Equal(store.calls[0], []string{"pending"}) {
		t.Fatalf("unresolved error=%v calls=%v", err, store.calls)
	}
}
