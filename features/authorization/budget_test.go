package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
)

// budgetService builds a memstore-backed Service over model with the given
// evaluation limits, returning it together with the backing relationship store so
// tests SEED via the store PORT (the raw Service write path was removed at AZ3-3.4).
func budgetService(t *testing.T, model Schema, limits EvaluationLimits) (*Service, *memstore.Relationships) {
	t.Helper()
	store := memstore.NewRelationships()
	comps, err := NewService(Repositories{Relationships: store}, Config{Model: model, Limits: limits})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service, store
}

// folderHierarchy is a self-referential folder tree: view is granted directly
// (viewer) or inherited up the parent chain (Through to another folder's view).
func folderHierarchy() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "folder",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "folder"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view")),
			},
		},
	}})
}

func checkFolder(t *testing.T, svc *Service, ctx context.Context, id string) (CheckResult, error) {
	t.Helper()
	return svc.Check(ctx, CheckRequest{
		Principal:  PrincipalRef{Type: "user", ID: "u1"},
		Permission: "view",
		Resource:   Resource{Type: "folder", ID: id},
	})
}

// TestDepthBoundaryExactlyThroughDepth pins the Through-depth boundary to `>`:
// MaxThroughDepth is the MAXIMUM number of Through hops. A chain needing exactly
// N hops succeeds at MaxThroughDepth==N and is ErrEvaluationLimit at N-1 — the
// exact boundary, not off-by-one.
func TestDepthBoundaryExactlyThroughDepth(t *testing.T) {
	ctx := context.Background()
	// f2 <- f1 <- f0 : Check(f2) reaches f0 (the only grant) in exactly 2 hops.
	tuples := []CreateRelationship{
		{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f0"},
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// Exactly at the boundary: 2 hops allowed with MaxThroughDepth=2.
	svc, store := budgetService(t, folderHierarchy(), EvaluationLimits{MaxThroughDepth: 2})
	if err := store.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := checkFolder(t, svc, ctx, "f2")
	if err != nil {
		t.Fatalf("MaxThroughDepth=2 Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("MaxThroughDepth=2: want allowed at exactly the depth boundary, got deny (%s)", res.Reason)
	}

	// One hop too shallow: MaxThroughDepth=1 must return the indeterminate limit,
	// never a deny.
	svc1, store1 := budgetService(t, folderHierarchy(), EvaluationLimits{MaxThroughDepth: 1})
	if err := store1.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = checkFolder(t, svc1, ctx, "f2")
	if !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("MaxThroughDepth=1: want ErrEvaluationLimit at N-1, got %v", err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("evaluation-limit must wrap sdk.ErrUnavailable, got %v", err)
	}
}

// TestBudgetGraphStatesExhaustion proves distinct (resource, permission) states
// are charged against MaxGraphStates: a chain of 3 distinct states denies at
// MaxGraphStates=3 but is ErrEvaluationLimit at 2.
func TestBudgetGraphStatesExhaustion(t *testing.T) {
	ctx := context.Background()
	tuples := []CreateRelationship{
		{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f0"},
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// 3 states (f2, f1, f0) fit MaxGraphStates=3 → the grant on f0 is reached.
	ok, okStore := budgetService(t, folderHierarchy(), EvaluationLimits{MaxGraphStates: 3})
	if err := okStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := checkFolder(t, ok, ctx, "f2")
	if err != nil || !res.Allowed {
		t.Fatalf("MaxGraphStates=3: want allowed, got allowed=%v err=%v", res.Allowed, err)
	}

	// MaxGraphStates=2 exhausts before reaching f0's grant.
	tight, tightStore := budgetService(t, folderHierarchy(), EvaluationLimits{MaxGraphStates: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := checkFolder(t, tight, ctx, "f2"); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("MaxGraphStates=2: want ErrEvaluationLimit, got %v", err)
	}
}

// TestDiamondGraphStateDedup proves reconvergent Through paths charge a shared
// (resource, permission) state ONCE. A diamond with 4 distinct states denies
// cleanly at MaxGraphStates=4 (without dedup the shared state would be charged
// twice and exhaust); MaxGraphStates=3 is the exhaustion boundary.
func TestDiamondGraphStateDedup(t *testing.T) {
	ctx := context.Background()
	// t0 -> a and t0 -> b (via left/right); a -> z and b -> z (both via left);
	// z has no grant, so the whole tree is explored and z is reached twice.
	model := NewSchema([]ResourceSchema{{
		Name: "node",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"left":   {AllowedSubjects: []SubjectTypeRef{{Type: "node"}}},
				"right":  {AllowedSubjects: []SubjectTypeRef{{Type: "node"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("left", "view"), Through("right", "view")),
			},
		},
	}})
	tuples := []CreateRelationship{
		{ResourceType: "node", ResourceID: "t0", Relation: "left", SubjectType: "node", SubjectID: "a"},
		{ResourceType: "node", ResourceID: "t0", Relation: "right", SubjectType: "node", SubjectID: "b"},
		{ResourceType: "node", ResourceID: "a", Relation: "left", SubjectType: "node", SubjectID: "z"},
		{ResourceType: "node", ResourceID: "b", Relation: "left", SubjectType: "node", SubjectID: "z"},
	}

	check := func(svc *Service) (CheckResult, error) {
		return svc.Check(ctx, CheckRequest{
			Principal:  PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   Resource{Type: "node", ID: "t0"},
		})
	}

	// 4 distinct states (t0, a, b, z) fit exactly — dedup makes z free the 2nd time.
	dedup, dedupStore := budgetService(t, model, EvaluationLimits{MaxGraphStates: 4})
	if err := dedupStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := check(dedup)
	if err != nil {
		t.Fatalf("MaxGraphStates=4: want a clean deny, got err %v", err)
	}
	if res.Allowed {
		t.Fatalf("MaxGraphStates=4: no grant exists, want deny")
	}

	// One fewer state exhausts (b cannot be charged after t0, a, z).
	tight, tightStore := budgetService(t, model, EvaluationLimits{MaxGraphStates: 3})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := check(tight); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("MaxGraphStates=3: want ErrEvaluationLimit, got %v", err)
	}
}

// TestFanoutExhaustion proves per-hop Through fan-out is bounded by
// MaxRelationTargets: a resource with 3 parent targets denies at
// MaxRelationTargets=3 but is ErrEvaluationLimit at 2.
func TestFanoutExhaustion(t *testing.T) {
	ctx := context.Background()
	tuples := []CreateRelationship{
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p1"},
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p2"},
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p3"},
	}

	ok, okStore := budgetService(t, folderHierarchy(), EvaluationLimits{MaxRelationTargets: 3})
	if err := okStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := checkFolder(t, ok, ctx, "f")
	if err != nil {
		t.Fatalf("MaxRelationTargets=3: want a clean deny, got err %v", err)
	}
	if res.Allowed {
		t.Fatalf("MaxRelationTargets=3: no grant exists, want deny")
	}

	tight, tightStore := budgetService(t, folderHierarchy(), EvaluationLimits{MaxRelationTargets: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := checkFolder(t, tight, ctx, "f"); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("MaxRelationTargets=2 with 3 targets: want ErrEvaluationLimit, got %v", err)
	}
}

// TestBudgetBatchSizeRejected proves an over-size CheckBatch and FilterAuthorized
// are rejected with ErrEvaluationLimit before any store work.
func TestBudgetBatchSizeRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := budgetService(t, folderHierarchy(), EvaluationLimits{MaxBatchSize: 2})

	reqs := []CheckRequest{
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "a"}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "b"}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "c"}},
	}
	if _, err := svc.CheckBatch(ctx, reqs); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("CheckBatch over MaxBatchSize: want ErrEvaluationLimit, got %v", err)
	}
	if _, err := svc.FilterAuthorized(ctx, PrincipalRef{Type: "user", ID: "u1"}, "view", "folder", []string{"a", "b", "c"}); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("FilterAuthorized over MaxBatchSize: want ErrEvaluationLimit, got %v", err)
	}

	// At the limit it is a normal (denying) result, not an error.
	if _, err := svc.CheckBatch(ctx, reqs[:2]); err != nil {
		t.Fatalf("CheckBatch at MaxBatchSize: want no error, got %v", err)
	}
}

// TestBudgetLookupResultsExhaustion proves LookupResources reports overflow as
// ErrEvaluationLimit rather than a truncated slice presented as complete.
func TestBudgetLookupResultsExhaustion(t *testing.T) {
	ctx := context.Background()
	model := NewSchema([]ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"))},
		},
	}})
	tuples := []CreateRelationship{
		{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "d2", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "d3", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// 3 accessible docs exceed MaxLookupResults=2 → indeterminate, never [d1 d2].
	tight, tightStore := budgetService(t, model, EvaluationLimits{MaxLookupResults: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := tight.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u1"}, "view", "doc"); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("LookupResources over MaxLookupResults: want ErrEvaluationLimit, got %v", err)
	}

	// A budget that fits returns the complete set.
	ok, okStore := budgetService(t, model, EvaluationLimits{MaxLookupResults: 3})
	if err := okStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := ok.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
	if err != nil {
		t.Fatalf("LookupResources within budget: %v", err)
	}
	if len(res.IDs) != 3 {
		t.Fatalf("want 3 complete IDs, got %v", res.IDs)
	}
}

// TestCancelBeforeStoreCall proves a canceled context short-circuits Check and
// LookupResources with the context error (fail closed), never a deny or a list.
func TestCancelBeforeStoreCall(t *testing.T) {
	model := folderHierarchy()
	svc, store := budgetService(t, model, EvaluationLimits{})
	if err := store.CreateRelationships(context.Background(), []CreateRelationship{
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := checkFolder(t, svc, ctx, "f0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check on canceled ctx: want context.Canceled, got %v", err)
	}
	if _, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u1"}, "view", "folder"); !errors.Is(err, context.Canceled) {
		t.Fatalf("LookupResources on canceled ctx: want context.Canceled, got %v", err)
	}
}

// TestSiblingThroughLookupNotSuppressed is the documented high-finding #4 (second
// half): two sibling Through relations that traverse the SAME target
// (type, permission) must both enumerate. The old shared visited-key set marked
// the target complete on the first sibling and returned empty on the second; the
// stack/memo split reuses the completed sub-result instead of suppressing it.
func TestSiblingThroughLookupNotSuppressed(t *testing.T) {
	ctx := context.Background()
	model := NewSchema([]ResourceSchema{
		{Name: "group", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"))},
		}},
		{Name: "doc", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"primary":   {AllowedSubjects: []SubjectTypeRef{{Type: "group"}}},
				"secondary": {AllowedSubjects: []SubjectTypeRef{{Type: "group"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Through("primary", "view"), Through("secondary", "view")),
			},
		}},
	})
	svc, store := budgetService(t, model, EvaluationLimits{})
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "group", ResourceID: "gp", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "group", ResourceID: "gs", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "dp", Relation: "primary", SubjectType: "group", SubjectID: "gp"},
		{ResourceType: "doc", ResourceID: "ds", Relation: "secondary", SubjectType: "group", SubjectID: "gs"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	// Both the primary-derived dp and the secondary-derived ds must appear: the
	// second Through reuses the memoized group:view result rather than getting an
	// empty (suppressed) set.
	if want := []string{"dp", "ds"}; !sortedEqual(res.IDs, want) {
		t.Fatalf("sibling Through suppression: want %v, got %v", want, res.IDs)
	}
}
