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

func hierarchyService(t *testing.T, model Schema) *Service {
	t.Helper()
	svc, err := NewService(Repositories{Relationships: memstore.NewRelationships()}, Config{Model: model})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
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
	svc := hierarchyService(t, hierarchySchema())

	// root <- mid <- leaf: each child's `parent` points at its parent space.
	if err := svc.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "space", ResourceID: "mid", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "leaf", Relation: "parent", SubjectType: "space", SubjectID: "mid"},
		{ResourceType: "space", ResourceID: "root", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}

	user := Subject{Type: "user", ID: "u1"}

	res, err := svc.Check(ctx, CheckRequest{Subject: user, Permission: "view", Resource: Resource{Type: "space", ID: "leaf"}})
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

// TestHierarchyLookupBoundaryOrgSeededRoot pins the documented D1(b) boundary:
// when the chain root is reachable ONLY through a NON-self Through (here the
// user is admin on an org, with NO direct viewer grant anywhere), Check honors
// the org-derived root and allows the grandchild, but LookupResources returns
// only the org-reachable root — its self-referential descendant walk seeds from
// DIRECT grants alone, so mid/leaf are missing.
//
// This asymmetry is deliberate, not a bug: closing it is the named D1(c) engine
// follow-up. When that lands, this test's LookupResources assertion flips to
// expect {root, mid, leaf} — the flip is the signal the boundary moved.
func TestHierarchyLookupBoundaryOrgSeededRoot(t *testing.T) {
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
	svc := hierarchyService(t, model)

	// root <- mid <- leaf, root's access flows ONLY from org O; no viewer grants.
	if err := svc.CreateRelationships(ctx, []CreateRelationship{
		{ResourceType: "space", ResourceID: "mid", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "leaf", Relation: "parent", SubjectType: "space", SubjectID: "mid"},
		{ResourceType: "space", ResourceID: "root", Relation: "org", SubjectType: "org", SubjectID: "O"},
		{ResourceType: "org", ResourceID: "O", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}

	user := Subject{Type: "user", ID: "u1"}

	res, err := svc.Check(ctx, CheckRequest{Subject: user, Permission: "view", Resource: Resource{Type: "space", ID: "leaf"}})
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
	// D1(b) boundary: only the org-reachable root; descendants are NOT expanded.
	if want := []string{"root"}; !sortedEqual(look.IDs, want) {
		t.Fatalf("LookupResources D1(b) boundary: want %v (org-reachable root only), got %v", want, look.IDs)
	}
}
