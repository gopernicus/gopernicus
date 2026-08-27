package authorizersvc

import "testing"

func TestDSLHelpers(t *testing.T) {
	if got := Direct("owner"); got.Relation != "owner" || got.Through != "" {
		t.Fatalf("Direct: got %+v", got)
	}
	if got := Through("org", "admin"); got.Through != "org" || got.Permission != "admin" || got.Relation != "" {
		t.Fatalf("Through: got %+v", got)
	}
	rule := AnyOf(Direct("owner"), Through("org", "admin"))
	if len(rule.AnyOf) != 2 {
		t.Fatalf("AnyOf: want 2 checks, got %d", len(rule.AnyOf))
	}
	if rule.IsRemove() {
		t.Fatalf("AnyOf rule must not be a remove rule")
	}
	if !Remove().IsRemove() {
		t.Fatalf("Remove must yield a remove rule")
	}
}

func TestNewSchemaComposesAndCopies(t *testing.T) {
	tenant := []ResourceSchema{{
		Name: "tenant",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("owner"))},
		},
	}}
	project := []ResourceSchema{{
		Name: "project",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("member"))},
		},
	}}

	schema := NewSchema(tenant, project)
	if len(schema.ResourceTypes) != 2 {
		t.Fatalf("want 2 resource types, got %d", len(schema.ResourceTypes))
	}

	// NewSchema deep-copies: mutating the source slice must not leak in.
	tenant[0].Def.Relations["owner"] = RelationDef{}
	if len(schema.ResourceTypes["tenant"].Relations["owner"].AllowedSubjects) != 1 {
		t.Fatalf("NewSchema did not deep-copy resource definitions")
	}
}

func TestMergeResourceTypeOverrideAndRemove(t *testing.T) {
	base := ResourceTypeDef{
		Relations: map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
		Permissions: map[string]PermissionRule{
			"delete":           AnyOf(Direct("owner")),
			"dangerous_action": AnyOf(Direct("owner")),
		},
	}
	override := ResourceTypeDef{
		Relations: map[string]RelationDef{"super_admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
		Permissions: map[string]PermissionRule{
			"delete":           AnyOf(Direct("owner"), Direct("super_admin")),
			"dangerous_action": Remove(),
		},
	}

	merged := MergeResourceType(base, override)

	if _, ok := merged.Relations["super_admin"]; !ok {
		t.Fatalf("override relation not merged in")
	}
	if _, ok := merged.Relations["owner"]; !ok {
		t.Fatalf("base relation lost in merge")
	}
	if got := len(merged.Permissions["delete"].AnyOf); got != 2 {
		t.Fatalf("override permission not applied: delete has %d checks", got)
	}
	if _, ok := merged.Permissions["dangerous_action"]; ok {
		t.Fatalf("Remove() did not delete the permission during merge")
	}

	// Merge must not mutate base.
	if got := len(base.Permissions["delete"].AnyOf); got != 1 {
		t.Fatalf("merge mutated base: delete now has %d checks", got)
	}
}
