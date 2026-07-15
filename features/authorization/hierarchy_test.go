package authorization

import (
	"context"
	"sort"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/memstore"
)

// hierarchySchema is the canonical self-referential hierarchy: a space's `view`
// is granted directly (viewer) or inherited up the `parent` chain (Through to
// another space's `view`). The `parent` relation targets the space type itself
// — the exact self-loop the validator now sanctions.
func hierarchySchema() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "space",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view")),
			},
		},
	}})
}

// hierarchyService builds the engine and returns it together with the backing
// relationship store, so tests SEED via the store PORT (the raw Service write path
// was removed at AZ3-3.4) and exercise the engine via the Service.
func hierarchyService(t *testing.T, model Schema) (*Service, *memstore.Relationships) {
	t.Helper()
	store := memstore.NewRelationships()
	comps, err := NewService(Repositories{Relationships: store}, Config{Model: model})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service, store
}

func sortedEqual(got, want []string) bool {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// TestHierarchySelfReferentialThrough proves the relaxed validator's schema is
// accepted at NewService and that BOTH runtime evaluators resolve a 3-level
// parent chain: Check walks it up, and LookupResources enumerates root plus all
// descendants when the root is granted DIRECTLY.
func TestHierarchySelfReferentialThrough(t *testing.T) {
	ctx := context.Background()
	svc, store := hierarchyService(t, hierarchySchema())

	// root <- mid <- leaf: each child's `parent` points at its parent space.
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "space", ResourceID: "mid", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "leaf", Relation: "parent", SubjectType: "space", SubjectID: "mid"},
		{ResourceType: "space", ResourceID: "root", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}

	user := PrincipalRef{Type: "user", ID: "u1"}

	res, err := svc.Check(ctx, CheckRequest{Principal: user, Permission: "view", Resource: Resource{Type: "space", ID: "leaf"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("Check view on leaf: want allowed, got denied (%s)", res.Reason)
	}

	look, err := svc.LookupResources(ctx, user, "view", "space")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if want := []string{"root", "mid", "leaf"}; !sortedEqual(look.IDs, want) {
		t.Fatalf("LookupResources: want %v, got %v", want, look.IDs)
	}
}

// TestHierarchyLookupOrgSeededRootExpandsDescendants proves the D1(c) closure:
// when the chain root is reachable ONLY through a NON-self Through (here the
// user is admin on an org, with NO direct viewer grant anywhere), Check honors
// the org-derived root and allows the grandchild, AND LookupResources now
// enumerates the org-reachable root PLUS its self-referential descendants —
// the self-hierarchy walk seeds from every root the permission grants, not
// direct-only roots. This is the flip that closed the former D1(b) divergence.
func TestHierarchyLookupOrgSeededRootExpandsDescendants(t *testing.T) {
	ctx := context.Background()

	// Extended schema: an org grants view via `admin`, and a space may inherit
	// view from its org (non-self Through) in addition to its parent chain.
	model := NewSchema([]ResourceSchema{
		{
			Name: "org",
			Def: ResourceTypeDef{
				Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
				Permissions: map[string]PermissionRule{"view": AnyOf(Direct("admin"))},
			},
		},
		{
			Name: "space",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
					"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
					"org":    {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
				},
				Permissions: map[string]PermissionRule{
					"view": AnyOf(Direct("viewer"), Through("org", "view"), Through("parent", "view")),
				},
			},
		},
	})
	svc, store := hierarchyService(t, model)

	// root <- mid <- leaf, root's access flows ONLY from org O; no viewer grants.
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "space", ResourceID: "mid", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "leaf", Relation: "parent", SubjectType: "space", SubjectID: "mid"},
		{ResourceType: "space", ResourceID: "root", Relation: "org", SubjectType: "org", SubjectID: "O"},
		{ResourceType: "org", ResourceID: "O", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}

	user := PrincipalRef{Type: "user", ID: "u1"}

	res, err := svc.Check(ctx, CheckRequest{Principal: user, Permission: "view", Resource: Resource{Type: "space", ID: "leaf"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("Check view on leaf: want allowed (org-derived), got denied (%s)", res.Reason)
	}

	look, err := svc.LookupResources(ctx, user, "view", "space")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	// D1(c) closed: the org-reachable root AND its parent-chain descendants.
	if want := []string{"root", "mid", "leaf"}; !sortedEqual(look.IDs, want) {
		t.Fatalf("LookupResources D1(c): want %v (org root + descendants), got %v", want, look.IDs)
	}
}

// TestHierarchyLookupMultipleSelfRelationsFixpoint proves LookupResources reaches
// a fixpoint across TWO same-permission self-referential relations that
// interleave: a node discovered as a descendant via one relation is a valid root
// for the other. `folder` and `parent` both inherit `view`; the chain crosses
// relations at each hop (root --parent-- a --folder-- b), so a single per-relation
// walk from the roots would miss b. The engine's fixpoint loop must find it.
func TestHierarchyLookupMultipleSelfRelationsFixpoint(t *testing.T) {
	ctx := context.Background()
	model := NewSchema([]ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "doc"}}},
				"folder": {AllowedSubjects: []SubjectTypeRef{{Type: "doc"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view"), Through("folder", "view")),
			},
		},
	}})
	svc, store := hierarchyService(t, model)

	// root (direct viewer) --parent-- a --folder-- b: a inherits via parent from
	// root, b inherits via folder from a.
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "doc", ResourceID: "root", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "doc", ResourceID: "a", Relation: "parent", SubjectType: "doc", SubjectID: "root"},
		{ResourceType: "doc", ResourceID: "b", Relation: "folder", SubjectType: "doc", SubjectID: "a"},
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}

	user := PrincipalRef{Type: "user", ID: "u1"}
	// Check honors the cross-relation chain.
	res, err := svc.Check(ctx, CheckRequest{Principal: user, Permission: "view", Resource: Resource{Type: "doc", ID: "b"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("Check view on b: want allowed (cross-relation chain), got denied (%s)", res.Reason)
	}

	look, err := svc.LookupResources(ctx, user, "view", "doc")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if want := []string{"root", "a", "b"}; !sortedEqual(look.IDs, want) {
		t.Fatalf("LookupResources fixpoint: want %v, got %v", want, look.IDs)
	}
}
