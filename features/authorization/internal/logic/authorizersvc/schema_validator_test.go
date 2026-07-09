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
