package authorizersvc

import (
	"errors"
	"strings"
	"testing"
)

// mixedTargetSchema is a doc whose `parent` relation targets BOTH folder and org.
// doc.view traverses parent, so a runtime parent may be a folder OR an org. The
// `view` permission exists on folder; withOrgView controls whether it also exists
// on org — the difference between an accepted rule and an ambiguous mixed-target
// one.
func mixedTargetSchema(withOrgView bool) Schema {
	orgPerms := map[string]PermissionRule{"read": AnyOf(Direct("member"))}
	if withOrgView {
		orgPerms["view"] = AnyOf(Direct("member"))
	}
	return Schema{ResourceTypes: map[string]ResourceTypeDef{
		"folder": {
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"))},
		},
		"org": {
			Relations:   map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: orgPerms,
		},
		"doc": {
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "folder"}, {Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("viewer"), Through("parent", "view")),
			},
		},
	}}
}

// TestSchemaRejectsMixedTargetThrough proves a Through permission present on only
// SOME of a relation's possible resource targets is rejected as ambiguous, rather
// than treated as satisfied because it exists on one target — the missing branch
// would silently deny at runtime.
func TestSchemaRejectsMixedTargetThrough(t *testing.T) {
	c, err := Compile(mixedTargetSchema(false))
	if err == nil {
		t.Fatal("expected a compile error for a mixed-target Through with the permission missing on org")
	}
	if c != nil {
		t.Fatal("Compile returned a schema alongside an error")
	}
	var ce *SchemaCompileError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *SchemaCompileError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "not found on target") || !strings.Contains(err.Error(), "org") {
		t.Fatalf("error must name the missing target org: %q", err.Error())
	}
}

// TestSchemaAcceptsThroughOnAllTargets proves the same shape compiles once the
// permission exists on EVERY possible resource target.
func TestSchemaAcceptsThroughOnAllTargets(t *testing.T) {
	if _, err := Compile(mixedTargetSchema(true)); err != nil {
		t.Fatalf("Through with the permission on all targets must compile: %v", err)
	}
}

// TestSchemaProjectionAndErrorsDeterministic proves repeated compilation of
// equivalent input yields an identical schema projection (digest + snapshot
// accessors) for a valid schema and an identical ordered error list for an
// invalid one.
func TestSchemaProjectionAndErrorsDeterministic(t *testing.T) {
	valid := orgProjectSchema()
	first, err := Compile(valid)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for i := 0; i < 20; i++ {
		c, err := Compile(orgProjectSchema())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		if c.Digest() != first.Digest() {
			t.Fatalf("digest not stable: %q vs %q", c.Digest(), first.Digest())
		}
		a, b := c.Snapshot(), first.Snapshot()
		if strings.Join(a.ResourceTypes(), ",") != strings.Join(b.ResourceTypes(), ",") {
			t.Fatalf("resource-type projection not stable: %v vs %v", a.ResourceTypes(), b.ResourceTypes())
		}
		if strings.Join(a.Permissions("project"), ",") != strings.Join(b.Permissions("project"), ",") {
			t.Fatalf("permission projection not stable")
		}
	}

	invalid := mixedTargetSchema(false)
	_, err = Compile(invalid)
	var want *SchemaCompileError
	if !errors.As(err, &want) {
		t.Fatalf("expected *SchemaCompileError, got %T", err)
	}
	for i := 0; i < 20; i++ {
		_, err := Compile(mixedTargetSchema(false))
		var got *SchemaCompileError
		if !errors.As(err, &got) {
			t.Fatalf("expected *SchemaCompileError, got %T", err)
		}
		if strings.Join(got.Errors, "\n") != strings.Join(want.Errors, "\n") {
			t.Fatalf("validation errors not deterministic:\n%v\nvs\n%v", got.Errors, want.Errors)
		}
	}
}
