package decisions_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// budgetService builds a memstore-backed Service over model with the given
// evaluation limits, returning it together with the backing relationship store so
// tests SEED via the store PORT (the raw Service write path was removed at AZ3-3.4).
func budgetService(t *testing.T, model relationships.Schema, limits authmodel.EvaluationLimits) (authorization.Components, *memory.Relationships) {
	t.Helper()
	store := memory.NewRelationships()
	comps, err := authorization.New(authorization.Repositories{Relationships: store}, authorization.WithRelationshipModel(model), authorization.WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps, store
}

// folderHierarchy is a self-referential folder tree: view is granted directly
// (viewer) or inherited up the parent chain (Through to another folder's view).
func folderHierarchy() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "folder",
		Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("parent", "view")),
			},
		},
	}})
}

func checkFolder(t *testing.T, svc authorization.Components, ctx context.Context, id string) (authmodel.CheckResult, error) {
	t.Helper()
	return svc.Decisions.Check(ctx, authmodel.CheckRequest{
		Principal:  authmodel.PrincipalRef{Type: "user", ID: "u1"},
		Permission: "view",
		Resource:   authmodel.Resource{Type: "folder", ID: id},
	})
}

// TestDepthBoundaryExactlyThroughDepth pins the Through-depth boundary to `>`:
// MaxThroughDepth is the MAXIMUM number of Through hops. A chain needing exactly
// N hops succeeds at MaxThroughDepth==N and is ErrEvaluationLimit at N-1 — the
// exact boundary, not off-by-one.
func TestDepthBoundaryExactlyThroughDepth(t *testing.T) {
	ctx := context.Background()
	// f2 <- f1 <- f0 : Check(f2) reaches f0 (the only grant) in exactly 2 hops.
	tuples := []relationships.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f0"},
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// Exactly at the boundary: 2 hops allowed with MaxThroughDepth=2.
	svc, store := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxThroughDepth: 2})
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
	svc1, store1 := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxThroughDepth: 1})
	if err := store1.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = checkFolder(t, svc1, ctx, "f2")
	if !errors.Is(err, authmodel.ErrEvaluationLimit) {
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
	tuples := []relationships.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f0"},
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// 3 states (f2, f1, f0) fit MaxGraphStates=3 → the grant on f0 is reached.
	ok, okStore := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxGraphStates: 3})
	if err := okStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := checkFolder(t, ok, ctx, "f2")
	if err != nil || !res.Allowed {
		t.Fatalf("MaxGraphStates=3: want allowed, got allowed=%v err=%v", res.Allowed, err)
	}

	// MaxGraphStates=2 exhausts before reaching f0's grant.
	tight, tightStore := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxGraphStates: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := checkFolder(t, tight, ctx, "f2"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
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
	model := relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "node",
		Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"left":   {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "node"}}},
				"right":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "node"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("left", "view"), relationships.Through("right", "view")),
			},
		},
	}})
	tuples := []relationships.CreateRelationship{
		{ResourceType: "node", ResourceID: "t0", Relation: "left", SubjectType: "node", SubjectID: "a"},
		{ResourceType: "node", ResourceID: "t0", Relation: "right", SubjectType: "node", SubjectID: "b"},
		{ResourceType: "node", ResourceID: "a", Relation: "left", SubjectType: "node", SubjectID: "z"},
		{ResourceType: "node", ResourceID: "b", Relation: "left", SubjectType: "node", SubjectID: "z"},
	}

	check := func(svc authorization.Components) (authmodel.CheckResult, error) {
		return svc.Decisions.Check(ctx, authmodel.CheckRequest{
			Principal:  authmodel.PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   authmodel.Resource{Type: "node", ID: "t0"},
		})
	}

	// 4 distinct states (t0, a, b, z) fit exactly — dedup makes z free the 2nd time.
	dedup, dedupStore := budgetService(t, model, authmodel.EvaluationLimits{MaxGraphStates: 4})
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
	tight, tightStore := budgetService(t, model, authmodel.EvaluationLimits{MaxGraphStates: 3})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := check(tight); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("MaxGraphStates=3: want ErrEvaluationLimit, got %v", err)
	}
}

// TestFanoutExhaustion proves per-hop Through fan-out is bounded by
// MaxRelationTargets: a resource with 3 parent targets denies at
// MaxRelationTargets=3 but is ErrEvaluationLimit at 2.
func TestFanoutExhaustion(t *testing.T) {
	ctx := context.Background()
	tuples := []relationships.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p1"},
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p2"},
		{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p3"},
	}

	ok, okStore := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxRelationTargets: 3})
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

	tight, tightStore := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxRelationTargets: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := checkFolder(t, tight, ctx, "f"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("MaxRelationTargets=2 with 3 targets: want ErrEvaluationLimit, got %v", err)
	}
}

// TestBudgetBatchSizeRejected proves an over-size CheckBatch and FilterAuthorized
// are rejected with ErrEvaluationLimit before any store work.
func TestBudgetBatchSizeRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxBatchSize: 2})

	reqs := []authmodel.CheckRequest{
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "folder", ID: "a"}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "folder", ID: "b"}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "folder", ID: "c"}},
	}
	if _, err := svc.Decisions.CheckBatch(ctx, reqs); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("CheckBatch over MaxBatchSize: want ErrEvaluationLimit, got %v", err)
	}
	if _, err := svc.Decisions.FilterAuthorized(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "folder", []string{"a", "b", "c"}); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("FilterAuthorized over MaxBatchSize: want ErrEvaluationLimit, got %v", err)
	}

	// At the limit it is a normal (denying) result, not an error.
	if _, err := svc.Decisions.CheckBatch(ctx, reqs[:2]); err != nil {
		t.Fatalf("CheckBatch at MaxBatchSize: want no error, got %v", err)
	}
}

// TestBudgetLookupResultsExhaustion proves LookupAllResourceIDs reports overflow as
// ErrEvaluationLimit rather than a truncated slice presented as complete.
func TestBudgetLookupResultsExhaustion(t *testing.T) {
	ctx := context.Background()
	model := relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "doc",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
		},
	}})
	tuples := []relationships.CreateRelationship{
		{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "d2", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "d3", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}

	// 3 accessible docs exceed MaxLookupResults=2 → indeterminate, never [d1 d2].
	tight, tightStore := budgetService(t, model, authmodel.EvaluationLimits{MaxLookupResults: 2})
	if err := tightStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := tight.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("LookupAllResourceIDs over MaxLookupResults: want ErrEvaluationLimit, got %v", err)
	}

	// A budget that fits returns the complete set.
	ok, okStore := budgetService(t, model, authmodel.EvaluationLimits{MaxLookupResults: 3})
	if err := okStore.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := ok.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs within budget: %v", err)
	}
	if len(res.IDs) != 3 {
		t.Fatalf("want 3 complete IDs, got %v", res.IDs)
	}
}

// TestBudgetLookupResultsBeatsASmallLimit is the load-bearing proof of where the
// budget bites once LookupResourceIDPage PAGES. MaxLookupResults is no longer a
// total-results cap:
//
//   - A TOP-LEVEL enumeration larger than the budget now PAGES. Three docs under
//     MaxLookupResults=2 are read one page at a time — no error, HasMore on every
//     page but the last, and the walk yields all three in the plain order.
//   - The "overflow beats a small limit" property MOVES to the work the paging
//     cannot page (plan A3): an INTERMEDIATE Through target set over the budget,
//     and a self-hierarchy ROOT set over the budget, are still ErrEvaluationLimit
//     on EVERY page — never a short list that looks complete.
func TestBudgetLookupResultsBeatsASmallLimit(t *testing.T) {
	ctx := context.Background()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	t.Run("a top-level enumeration over the budget pages", func(t *testing.T) {
		model := relationships.NewSchema([]relationships.ResourceSchema{{
			Name: "doc",
			Def: relationships.ResourceTypeDef{
				Relations:   map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
				Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
			},
		}})
		svc, store := budgetService(t, model, authmodel.EvaluationLimits{MaxLookupResults: 2})
		if err := store.CreateRelationships(ctx, []relationships.CreateRelationship{
			{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "doc", ResourceID: "d2", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "doc", ResourceID: "d3", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		}); err != nil {
			t.Fatalf("create: %v", err)
		}

		for _, limit := range []int{1, 2} {
			var got []string
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > 8 {
					t.Fatalf("limit %d: page walk did not terminate", limit)
				}
				res, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
					Principal: principal, Permission: "view", ResourceType: "doc", Limit: limit, After: cursor,
				})
				if err != nil {
					t.Fatalf("limit %d, after %q: want a page, got %v", limit, cursor, err)
				}
				if len(res.IDs) > limit {
					t.Fatalf("limit %d: page of %d ids", limit, len(res.IDs))
				}
				got = append(got, res.IDs...)
				if !res.HasMore {
					break
				}
				if res.NextCursor == "" {
					t.Fatalf("limit %d: HasMore without a continuation", limit)
				}
				cursor = res.NextCursor
			}
			if want := []string{"d1", "d2", "d3"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("limit %d: pages concatenated to %v, want %v", limit, got, want)
			}
		}

		// The classic method still refuses to answer at all: it is the COMPLETE
		// enumeration, and three docs do not fit a budget of two.
		if _, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, "view", "doc"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("LookupAllResourceIDs over MaxLookupResults: want ErrEvaluationLimit, got %v", err)
		}
	})

	t.Run("an intermediate node over the budget fails on every page", func(t *testing.T) {
		model := relationships.NewSchema([]relationships.ResourceSchema{
			{Name: "org", Def: relationships.ResourceTypeDef{
				Relations:   map[string]relationships.RelationDef{"admin": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
				Permissions: map[string]relationships.PermissionRule{"manage": relationships.AnyOf(relationships.Direct("admin"))},
			}},
			{Name: "post", Def: relationships.ResourceTypeDef{
				Relations:   map[string]relationships.RelationDef{"org": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "org"}}}},
				Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Through("org", "manage"))},
			}},
		})
		svc, store := budgetService(t, model, authmodel.EvaluationLimits{MaxLookupResults: 2})
		var tuples []relationships.CreateRelationship
		for _, org := range []string{"o1", "o2", "o3"} { // 3 orgs > the budget of 2
			tuples = append(tuples,
				relationships.CreateRelationship{ResourceType: "org", ResourceID: org, Relation: "admin", SubjectType: "user", SubjectID: "u1"},
				relationships.CreateRelationship{ResourceType: "post", ResourceID: "p_" + org, Relation: "org", SubjectType: "org", SubjectID: org},
			)
		}
		if err := store.CreateRelationships(ctx, tuples); err != nil {
			t.Fatalf("create: %v", err)
		}

		for _, limit := range []int{1, 2} {
			_, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
				Principal: principal, Permission: "view", ResourceType: "post", Limit: limit,
			})
			if !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("limit %d over an intermediate set of 3: want ErrEvaluationLimit, got %v", limit, err)
			}
			if !errors.Is(err, sdk.ErrUnavailable) {
				t.Fatalf("evaluation-limit must wrap sdk.ErrUnavailable, got %v", err)
			}
		}
	})

	t.Run("a hierarchy root set over the budget fails on every page", func(t *testing.T) {
		svc, store := budgetService(t, folderHierarchy(), authmodel.EvaluationLimits{MaxLookupResults: 2})
		if err := store.CreateRelationships(ctx, []relationships.CreateRelationship{
			{ResourceType: "folder", ResourceID: "f1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "folder", ResourceID: "f2", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "folder", ResourceID: "f3", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "folder", ResourceID: "f4", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
		for _, limit := range []int{1, 2} {
			// The closure needs the COMPLETE root set before it can page, so an
			// over-budget root set is indeterminate however small the page is.
			_, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
				Principal: principal, Permission: "view", ResourceType: "folder", Limit: limit,
			})
			if !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("limit %d over a root set of 3: want ErrEvaluationLimit, got %v", limit, err)
			}
		}
	})
}

// TestCancelBeforeStoreCall proves a canceled context short-circuits Check and
// LookupAllResourceIDs with the context error (fail closed), never a deny or a list.
func TestCancelBeforeStoreCall(t *testing.T) {
	model := folderHierarchy()
	svc, store := budgetService(t, model, authmodel.EvaluationLimits{})
	if err := store.CreateRelationships(context.Background(), []relationships.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := checkFolder(t, svc, ctx, "f0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check on canceled ctx: want context.Canceled, got %v", err)
	}
	if _, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "folder"); !errors.Is(err, context.Canceled) {
		t.Fatalf("LookupAllResourceIDs on canceled ctx: want context.Canceled, got %v", err)
	}
}

// TestSiblingThroughLookupNotSuppressed is the documented high-finding #4 (second
// half): two sibling Through relations that traverse the SAME target
// (type, permission) must both enumerate. The old shared visited-key set marked
// the target complete on the first sibling and returned empty on the second; the
// stack/memo split reuses the completed sub-result instead of suppressing it.
func TestSiblingThroughLookupNotSuppressed(t *testing.T) {
	ctx := context.Background()
	model := relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "group", Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
		}},
		{Name: "doc", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"primary":   {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group"}}},
				"secondary": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Through("primary", "view"), relationships.Through("secondary", "view")),
			},
		}},
	})
	svc, store := budgetService(t, model, authmodel.EvaluationLimits{})
	if err := store.CreateRelationships(ctx, []relationships.CreateRelationship{
		{ResourceType: "group", ResourceID: "gp", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "group", ResourceID: "gs", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "dp", Relation: "primary", SubjectType: "group", SubjectID: "gp"},
		{ResourceType: "doc", ResourceID: "ds", Relation: "secondary", SubjectType: "group", SubjectID: "gs"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs: %v", err)
	}
	// Both the primary-derived dp and the secondary-derived ds must appear: the
	// second Through reuses the memoized group:view result rather than getting an
	// empty (suppressed) set.
	if want := []string{"dp", "ds"}; !sortedEqual(res.IDs, want) {
		t.Fatalf("sibling Through suppression: want %v, got %v", want, res.IDs)
	}
}
