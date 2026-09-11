package relationships

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

func TestLookupResourcesDirect(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	)
	res, err := svc.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if len(res.IDs) != 2 {
		t.Fatalf("want 2 ids, got %v", res.IDs)
	}
}

func TestLookupResourcesEmptyIsNonNil(t *testing.T) {
	svc := newTestService(t, &fakeStore{})
	res, err := svc.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "nobody"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.IDs == nil {
		t.Fatalf("IDs must be non-nil even with no access")
	}
	if len(res.IDs) != 0 {
		t.Fatalf("want empty ids, got %v", res.IDs)
	}
}

// TestLookupResourcesPlatformAdminIsNotMagic proves the engine grants a
// platform-admin tuple holder NO unrestricted enumeration: it is enumerated
// only for resources it holds real grants on (none here). Admin-sees-everything
// is host composition, not engine behavior.
func TestLookupResourcesPlatformAdminIsNotMagic(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	store.tuples = append(store.tuples, CreateRelationship{
		ResourceType: "platform", ResourceID: "main", Relation: "admin", SubjectType: "user", SubjectID: "admin1",
	})
	res, err := svc.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "admin1"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.IDs == nil {
		t.Fatalf("IDs must be non-nil")
	}
	if len(res.IDs) != 0 {
		t.Fatalf("platform admin has no post grants → want empty ids, got %v", res.IDs)
	}
}

func TestLookupResourcesThrough(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	// u1 admins org o1; posts p1,p2 belong to o1 → both surface via Through.
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	res, err := svc.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if len(res.IDs) != 2 {
		t.Fatalf("want 2 ids via through, got %v", res.IDs)
	}
}

// =============================================================================
// LookupResourcesPage (the paged prefilter)
// =============================================================================

// lookupCall records one bounded lookup read the engine issued, so a test can
// prove the PAGE — not the universe — is what the store was asked for.
type lookupCall struct {
	method       string
	resourceType string
	relations    []string
	after        string
	limit        int
}

// recordingStore wraps fakeStore and records every lookup read.
type recordingStore struct {
	*fakeStore
	calls []lookupCall
}

func (r *recordingStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	r.calls = append(r.calls, lookupCall{"LookupResourceIDs", resourceType, relations, after, limit})
	return r.fakeStore.LookupResourceIDs(ctx, resourceType, relations, subjectType, subjectID, after, limit)
}

func (r *recordingStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	r.calls = append(r.calls, lookupCall{"LookupResourceIDsByRelationTarget", resourceType, []string{relation}, after, limit})
	return r.fakeStore.LookupResourceIDsByRelationTarget(ctx, resourceType, relation, targetType, targetIDs, after, limit)
}

func (r *recordingStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	r.calls = append(r.calls, lookupCall{"LookupDescendantResourceIDs", resourceType, relations, after, limit})
	return r.fakeStore.LookupDescendantResourceIDs(ctx, resourceType, relations, subjectType, rootIDs, after, limit)
}

// hierarchySchema is a self-referential space tree with TWO self relations, so a
// path may alternate parent and folder hops on its way to a root.
func hierarchySchema() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "space",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"folder": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view"), Through("folder", "view")),
			},
		},
	}})
}

func newServiceWith(t *testing.T, store Storer, schema Schema, limits authmodel.EvaluationLimits) *Service {
	t.Helper()
	svc, err := newService(store, schema, serviceConfig{limits: limits})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// walkPages pages the enumeration to exhaustion at the given page size and
// returns the concatenation. It fails on a page over the limit, a nil IDs, a
// HasMore that cannot advance, and a walk that does not terminate.
func walkPages(t *testing.T, svc *Service, principal authmodel.PrincipalRef, permission, resourceType string, limit int) []string {
	t.Helper()
	all := []string{}
	after := ""
	for pages := 0; ; pages++ {
		if pages > 64 {
			t.Fatalf("page walk did not terminate at limit %d", limit)
		}
		res, err := svc.LookupResourcesPage(context.Background(), principal, permission, resourceType, after, limit)
		if err != nil {
			t.Fatalf("LookupResourcesPage(after=%q, limit=%d): %v", after, limit, err)
		}
		if res.IDs == nil {
			t.Fatalf("IDs must be non-nil (after=%q, limit=%d)", after, limit)
		}
		if len(res.IDs) > limit {
			t.Fatalf("page of %d ids exceeds limit %d", len(res.IDs), limit)
		}
		all = append(all, res.IDs...)
		if !res.HasMore {
			return all
		}
		if len(res.IDs) == 0 {
			t.Fatalf("HasMore on an empty page cannot advance (after=%q, limit=%d)", after, limit)
		}
		after = res.IDs[len(res.IDs)-1]
	}
}

// assertPagedParity proves the load-bearing property of the paged prefilter: at
// every page size, the concatenation of the pages is EXACTLY the plain
// enumeration — same ids, same order, no repeats.
func assertPagedParity(t *testing.T, svc *Service, principal authmodel.PrincipalRef, permission, resourceType string, want []string) {
	t.Helper()
	plain, err := svc.LookupResources(context.Background(), principal, permission, resourceType)
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !reflect.DeepEqual(plain.IDs, want) {
		t.Fatalf("plain LookupResources = %v, want %v", plain.IDs, want)
	}
	for _, limit := range []int{1, 2, 7, len(want)} {
		if limit == 0 {
			continue
		}
		got := walkPages(t, svc, principal, permission, resourceType, limit)
		if !reflect.DeepEqual(got, plain.IDs) {
			t.Fatalf("limit %d: pages concatenated to %v, want the plain result %v", limit, got, plain.IDs)
		}
	}
}

// TestLookupResourcesPageParityDirect walks a direct-only enumeration at every
// page size.
func TestLookupResourcesPageParityDirect(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5"} {
		store.tuples = append(store.tuples, CreateRelationship{
			ResourceType: "post", ResourceID: id, Relation: "owner", SubjectType: "user", SubjectID: "u1",
		})
	}
	assertPagedParity(t, svc, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "delete", "post",
		[]string{"p1", "p2", "p3", "p4", "p5"})
}

// TestLookupResourcesPageParityDirectAndThroughOverlap walks an enumeration fed
// by TWO streams — a direct relation and a non-self Through hop — whose ids
// OVERLAP, so the union's dedup and the lookahead must agree across pages.
func TestLookupResourcesPageParityDirectAndThroughOverlap(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
	)
	for _, id := range []string{"p1", "p3", "p5"} {
		store.tuples = append(store.tuples, CreateRelationship{
			ResourceType: "post", ResourceID: id, Relation: "owner", SubjectType: "user", SubjectID: "u1",
		})
	}
	for _, id := range []string{"p3", "p4", "p6"} { // p3 is in BOTH streams
		store.tuples = append(store.tuples, CreateRelationship{
			ResourceType: "post", ResourceID: id, Relation: "org", SubjectType: "org", SubjectID: "o1",
		})
	}
	assertPagedParity(t, svc, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "post",
		[]string{"p1", "p3", "p4", "p5", "p6"})
}

// TestLookupResourcesPageParitySelfHierarchy walks a self hierarchy whose
// descendants sort BEFORE their root (a_child under z_root) and whose deepest
// member is reached by a path that ALTERNATES the two self relations
// (b_deep -parent-> f_x -folder-> m_mid -parent-> z_root). Root id order does not
// constrain descendant id order, which is why the root set is complete and only
// the closure is paged.
func TestLookupResourcesPageParitySelfHierarchy(t *testing.T) {
	store := &fakeStore{}
	svc := newServiceWith(t, store, hierarchySchema(), authmodel.EvaluationLimits{})
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "space", ResourceID: "z_root", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "space", ResourceID: "a_child", Relation: "parent", SubjectType: "space", SubjectID: "z_root"},
		CreateRelationship{ResourceType: "space", ResourceID: "m_mid", Relation: "parent", SubjectType: "space", SubjectID: "z_root"},
		CreateRelationship{ResourceType: "space", ResourceID: "f_x", Relation: "folder", SubjectType: "space", SubjectID: "m_mid"},
		CreateRelationship{ResourceType: "space", ResourceID: "b_deep", Relation: "parent", SubjectType: "space", SubjectID: "f_x"},
	)
	assertPagedParity(t, svc, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "space",
		[]string{"a_child", "b_deep", "f_x", "m_mid", "z_root"})
}

// TestLookupResourcesPageEmptyResultIsNonNil pins the non-nil contract on the
// paged surface: no access is an empty slice with no continuation, never nil.
func TestLookupResourcesPageEmptyResultIsNonNil(t *testing.T) {
	svc := newTestService(t, &fakeStore{})
	for _, permission := range []string{"delete", "fly"} { // declared, and declared by nobody
		res, err := svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "nobody"}, permission, "post", "", 5)
		if err != nil {
			t.Fatalf("%s: LookupResourcesPage: %v", permission, err)
		}
		if res.IDs == nil || len(res.IDs) != 0 || res.HasMore {
			t.Fatalf("%s: want an empty non-nil page with no continuation, got %+v", permission, res)
		}
	}
}

// TestLookupResourcesPageIntermediateOverflowOnEveryPage is the A3 cliff: paging
// the TOP-LEVEL result does not page an INTERMEDIATE Through target set, so a
// principal over MaxLookupResults orgs is indeterminate on EVERY page — never a
// short list that looks complete.
func TestLookupResourcesPageIntermediateOverflowOnEveryPage(t *testing.T) {
	store := &fakeStore{}
	svc := newServiceWith(t, store, testSchema(), authmodel.EvaluationLimits{MaxLookupResults: 2})
	for _, org := range []string{"o1", "o2", "o3"} {
		store.tuples = append(store.tuples,
			CreateRelationship{ResourceType: "org", ResourceID: org, Relation: "admin", SubjectType: "user", SubjectID: "u1"},
			CreateRelationship{ResourceType: "post", ResourceID: "p_" + org, Relation: "org", SubjectType: "org", SubjectID: org},
		)
	}
	for _, after := range []string{"", "p_o1"} {
		_, err := svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "post", after, 1)
		if !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("after %q: want ErrEvaluationLimit from the over-budget intermediate node, got %v", after, err)
		}
	}
}

// TestLookupResourcesPageHierarchyRootOverflowOnEveryPage is the same cliff for a
// self hierarchy: the non-descendant ROOT set must be complete before it seeds
// the closure, so an over-budget root set is indeterminate on every page.
func TestLookupResourcesPageHierarchyRootOverflowOnEveryPage(t *testing.T) {
	store := &fakeStore{}
	svc := newServiceWith(t, store, hierarchySchema(), authmodel.EvaluationLimits{MaxLookupResults: 2})
	for _, id := range []string{"s1", "s2", "s3"} {
		store.tuples = append(store.tuples, CreateRelationship{
			ResourceType: "space", ResourceID: id, Relation: "viewer", SubjectType: "user", SubjectID: "u1",
		})
	}
	store.tuples = append(store.tuples, CreateRelationship{
		ResourceType: "space", ResourceID: "s4", Relation: "parent", SubjectType: "space", SubjectID: "s1",
	})
	for _, after := range []string{"", "s1"} {
		_, err := svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "space", after, 1)
		if !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("after %q: want ErrEvaluationLimit from the over-budget root set, got %v", after, err)
		}
	}
}

// TestLookupResourcesPagePushesAfterAndPageSizeToTheStore proves the keyset is
// PUSHED DOWN: every top-level leaf read carries the page's own after and fetches
// exactly one page plus the lookahead row — not the whole universe. The complete
// sets the design requires (an intermediate target set, a hierarchy root set) are
// the only reads that still start from the beginning at the budget cap.
func TestLookupResourcesPagePushesAfterAndPageSizeToTheStore(t *testing.T) {
	inner := &fakeStore{}
	store := &recordingStore{fakeStore: inner}
	svc := newTestService(t, store)
	inner.tuples = append(inner.tuples,
		CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)

	if _, err := svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "post", "p0", 2); err != nil {
		t.Fatalf("LookupResourcesPage: %v", err)
	}

	var topLevel int
	for _, call := range store.calls {
		if call.resourceType != "post" {
			continue // the intermediate org read is deliberately complete
		}
		topLevel++
		if call.after != "p0" || call.limit != 3 {
			t.Fatalf("%s on post: got after=%q limit=%d, want after=%q limit=%d", call.method, call.after, call.limit, "p0", 3)
		}
	}
	if topLevel != 2 {
		t.Fatalf("want one bounded read per top-level stream (direct + through), got %d of %+v", topLevel, store.calls)
	}
}

// TestLookupResourcesPageCancellationBeforeTheFirstStoreCall proves a canceled
// context fails closed before any store is touched.
func TestLookupResourcesPageCancellationBeforeTheFirstStoreCall(t *testing.T) {
	inner := &fakeStore{}
	inner.tuples = append(inner.tuples, CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	})
	store := &recordingStore{fakeStore: inner}
	svc := newTestService(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.LookupResourcesPage(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "delete", "post", "", 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("no store call may begin after cancellation, got %+v", store.calls)
	}
}

func (s *recordingStore) ForModel(ReadModel) Reader { return s }
