package authorizersvc

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSchemaValid(t *testing.T) {
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"org": {
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
		},
		"project": {
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"org":    {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("member"), Through("org", "manage")),
			},
		},
	}}
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
}

func TestValidateSchemaUnknownDirectRelation(t *testing.T) {
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"org": {
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("owner"))}, // owner undefined
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "direct relation")
}

func TestValidateSchemaUnknownThroughRelation(t *testing.T) {
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"project": {
			Relations:   map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"view": AnyOf(Through("org", "manage"))}, // org relation undefined
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "through-relation")
}

func TestValidateSchemaThroughPermissionMissingOnTarget(t *testing.T) {
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"org": {
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
		},
		"project": {
			Relations: map[string]RelationDef{
				"org": {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(Through("org", "nonexistent"))},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "not found on target")
}

func TestValidateSchemaCircularThrough(t *testing.T) {
	// a.perm -> through(rel_b) -> b.perm -> through(rel_a) -> a.perm ...
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"a": {
			Relations:   map[string]RelationDef{"rel_b": {AllowedSubjects: []SubjectTypeRef{{Type: "b"}}}},
			Permissions: map[string]PermissionRule{"perm": AnyOf(Through("rel_b", "perm"))},
		},
		"b": {
			Relations:   map[string]RelationDef{"rel_a": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}}},
			Permissions: map[string]PermissionRule{"perm": AnyOf(Through("rel_a", "perm"))},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "circular")
}

func TestValidateSchemaSelfReferentialThrough(t *testing.T) {
	// The canonical hierarchy: space.view = viewer OR parent's view. The self
	// edge (parent -> space, same permission) must be accepted.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view")),
			},
		},
	}}
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("self-referential hierarchy rejected: %v", err)
	}
}

func TestValidateSchemaMutualCrossTypeCycle(t *testing.T) {
	// a.x -> through(rel_b) -> b.x -> through(rel_a) -> a.x: a genuine
	// cross-type cycle the relaxation must still reject.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"a": {
			Relations:   map[string]RelationDef{"rel_b": {AllowedSubjects: []SubjectTypeRef{{Type: "b"}}}},
			Permissions: map[string]PermissionRule{"x": AnyOf(Through("rel_b", "x"))},
		},
		"b": {
			Relations:   map[string]RelationDef{"rel_a": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}}},
			Permissions: map[string]PermissionRule{"x": AnyOf(Through("rel_a", "x"))},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "circular")
}

func TestValidateSchemaCrossPermissionSelfTypeCycle(t *testing.T) {
	// space.view -> parent.admin -> parent.view -> ...: self type, but the
	// permission alternates, so it is a real cycle, not a sanctioned self-loop.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{
				"view":  AnyOf(Through("parent", "admin")),
				"admin": AnyOf(Through("parent", "view")),
			},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "circular")
}

func TestValidateSchemaRealCycleBehindSelfEdge(t *testing.T) {
	// space.view has a sanctioned self-edge (parent.view) alongside a genuine
	// cross-type cycle (space.view -> org.view -> space.view). The self-edge
	// must not mask the real cycle.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"org":    {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Through("parent", "view"), Through("org", "view")),
			},
		},
		"org": {
			Relations: map[string]RelationDef{
				"space_ref": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Through("space_ref", "view")),
			},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "circular")
}

func TestValidateSchemaSelfOnlyThroughUnsatisfiable(t *testing.T) {
	// space.view = parent.view only: terminates, but no grant ever bottoms out.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Through("parent", "view")),
			},
		},
	}}
	assertValidationErr(t, ValidateSchema(schema), "unsatisfiable")
}

func TestValidateSchemaMixedSubjectSelfAndOther(t *testing.T) {
	// parent allows both space (self, sanctioned) and org (validated normally).
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}, {Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view")),
			},
		},
		"org": {
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("member")),
			},
		},
	}}
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("mixed self/other subject schema rejected: %v", err)
	}
}

func TestValidateSchemaTwoSelfReferentialRelations(t *testing.T) {
	// Both parent and origin are self relations; each self edge is sanctioned.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"space": {
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"origin": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view"), Through("origin", "view")),
			},
		},
	}}
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("two self-referential relations rejected: %v", err)
	}
}

func assertValidationErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a validation error mentioning %q, got nil", want)
	}
	var se *SchemaValidationError
	if !errors.As(err, &se) {
		t.Fatalf("expected *SchemaValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not mention %q", err.Error(), want)
	}
}
