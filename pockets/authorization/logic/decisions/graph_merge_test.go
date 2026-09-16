package decisions

import "testing"

// The merge cases below pin the deterministic composition semantics NewSchema
// guarantees: duplicate resource-type names UNION their relations and permissions
// with last-writer-wins override, and Remove() deletes. Every case compiles the
// merged result and compares digests so the observable policy — not just the map
// shape — is asserted.

// TestNewSchemaMergeDuplicateTypeUnionsMembers proves a resource type declared in
// two contributing slices is merged: relations and permissions from both survive,
// and a later contributor overrides a colliding relation/permission by name.
func TestNewSchemaMergeDuplicateTypeUnionsMembers(t *testing.T) {
	base := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"view": AnyOf(Direct("viewer"))},
		},
	}}
	addition := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"edit": AnyOf(Direct("editor"))},
		},
	}}

	schema := NewSchema(base, addition)
	if got := len(schema.ResourceTypes); got != 1 {
		t.Fatalf("duplicate type name must merge into one type, got %d", got)
	}
	doc := schema.ResourceTypes["doc"]
	for _, rel := range []string{"viewer", "editor"} {
		if _, ok := doc.Relations[rel]; !ok {
			t.Fatalf("merged doc lost relation %q", rel)
		}
	}
	for _, perm := range []string{"view", "edit"} {
		if _, ok := doc.Permissions[perm]; !ok {
			t.Fatalf("merged doc lost permission %q", perm)
		}
	}
	if _, err := Compile(schema); err != nil {
		t.Fatalf("merged schema failed to compile: %v", err)
	}
}

// TestNewSchemaMergeOverrideReplacesRelation proves a later contributor's relation
// of the same name REPLACES the earlier one's subjects (last-writer-wins), it is
// not appended.
func TestNewSchemaMergeOverrideReplacesRelation(t *testing.T) {
	base := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"view": AnyOf(Direct("viewer"))},
		},
	}}
	override := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "service_account"}}}},
		},
	}}

	schema := NewSchema(base, override)
	subs := schema.ResourceTypes["doc"].Relations["viewer"].AllowedSubjects
	if len(subs) != 1 || subs[0].Type != "service_account" {
		t.Fatalf("override must replace the relation subjects, got %v", subs)
	}
}

// TestNewSchemaMergePermissionOverrideAndRemove proves a later contributor can
// both override a permission (by name) and delete one with Remove().
func TestExplicitMergePermissionOverrideAndRemove(t *testing.T) {
	base := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]Expression{
				"view": AnyOf(Direct("viewer")),
				"edit": AnyOf(Direct("editor")),
			},
		},
	}}
	override := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Permissions: map[string]Expression{
				"view": AnyOf(Direct("viewer"), Direct("editor")),
				"edit": Remove(),
			},
		},
	}}

	schema := NewSchema([]ResourceSchema{{Name: "doc", Def: MergeResourceType(base[0].Def, override[0].Def)}})
	doc := schema.ResourceTypes["doc"]
	if got := len(doc.Permissions["view"].AnyOf); got != 2 {
		t.Fatalf("override must apply: view has %d checks, want 2", got)
	}
	if _, ok := doc.Permissions["edit"]; ok {
		t.Fatalf("Remove() must delete the edit permission during merge")
	}
	if _, err := Compile(schema); err != nil {
		t.Fatalf("merged schema failed to compile: %v", err)
	}
}

// TestNewSchemaMergeIsDeterministic proves composition is a pure function of its
// inputs: repeatedly composing and compiling the same contributors yields one
// stable digest, and an equivalent final schema reached by a different merge path
// (single combined slice vs. two duplicate-type slices) compiles to the same
// digest — the projection depends on the resulting policy, not the merge route.
func TestNewSchemaMergeIsDeterministic(t *testing.T) {
	base := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"view": AnyOf(Direct("viewer"))},
		},
	}}
	addition := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"edit": AnyOf(Direct("editor"))},
		},
	}}

	want := mustDigest(t, NewSchema(base, addition))
	for i := 0; i < 20; i++ {
		if got := mustDigest(t, NewSchema(base, addition)); got != want {
			t.Fatalf("merge digest not stable: %q vs %q", got, want)
		}
	}

	combined := []ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]Expression{
				"view": AnyOf(Direct("viewer")),
				"edit": AnyOf(Direct("editor")),
			},
		},
	}}
	if got := mustDigest(t, NewSchema(combined)); got != want {
		t.Fatalf("equivalent final schema via a different merge path has a different digest: %q vs %q", got, want)
	}
}

func TestNewSchemaRejectsDuplicatePermissionContributions(t *testing.T) {
	contribution := []ResourceSchema{{Name: "doc", Def: ResourceTypeDef{Permissions: map[string]Expression{"view": RoleIn("viewer")}}}}
	schema := NewSchema(contribution, contribution)
	if _, err := Compile(schema); err == nil {
		t.Fatal("independent duplicate permissions silently overrode policy")
	}
	_, f := expressionService(t)
	if _, err := NewService(f, WithModel(schema)); err == nil {
		t.Fatal("WithModel discarded duplicate declaration error")
	}
	explicit := NewSchema([]ResourceSchema{{Name: "doc", Def: MergeResourceType(contribution[0].Def, contribution[0].Def)}})
	if _, err := Compile(explicit); err != nil {
		t.Fatalf("deliberate explicit replacement: %v", err)
	}
}

func TestExplicitMergeOwnsOverrideExpressions(t *testing.T) {
	override := ResourceTypeDef{Permissions: map[string]Expression{"view": All(Role("admin"), RoleIn("viewer"))}}
	merged := MergeResourceType(ResourceTypeDef{}, override)
	override.Permissions["view"].AllOf[0].RoleName = "changed"
	if merged.Permissions["view"].AllOf[0].RoleName != "admin" {
		t.Fatal("explicit merge aliases override")
	}
}
