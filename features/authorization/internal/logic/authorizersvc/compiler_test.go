package authorizersvc

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// orgProjectSchema is a valid multi-type schema: a project inherits `view` from
// its org via a Through traversal, plus a direct `member` grant.
func orgProjectSchema() Schema {
	return Schema{ResourceTypes: map[string]ResourceTypeDef{
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
}

// usersetSchema is a valid schema exercising a userset subject: a doc's `viewer`
// may be the userset group#member, and `group` declares that `member` relation.
func usersetSchema() Schema {
	return Schema{ResourceTypes: map[string]ResourceTypeDef{
		"group": {
			Relations: map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
		},
		"doc": {
			Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "group", Relation: "member"}}}},
			Permissions: map[string]PermissionRule{"read": AnyOf(Direct("viewer"))},
		},
	}}
}

func selfHierarchySchema() Schema {
	return Schema{ResourceTypes: map[string]ResourceTypeDef{
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
}

func TestCompileAcceptsValidSchemas(t *testing.T) {
	cases := map[string]Schema{
		"org/project through": orgProjectSchema(),
		"userset subject":     usersetSchema(),
		"self hierarchy":      selfHierarchySchema(),
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := Compile(schema)
			if err != nil {
				t.Fatalf("valid schema rejected: %v", err)
			}
			if c.Digest() == "" {
				t.Fatal("compiled schema has an empty digest")
			}
			if c.EncodingVersion() != SchemaEncodingVersion {
				t.Fatalf("encoding version = %q, want %q", c.EncodingVersion(), SchemaEncodingVersion)
			}
		})
	}
}

func TestCompileClassifiesNavigationalRelation(t *testing.T) {
	c, err := Compile(orgProjectSchema())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	snap := c.Snapshot()

	nav, ok := snap.RelationIsNavigational("project", "org")
	if !ok || !nav {
		t.Fatalf("project.org: want navigational, got nav=%v ok=%v", nav, ok)
	}
	direct, ok := snap.RelationIsNavigational("project", "member")
	if !ok || direct {
		t.Fatalf("project.member: want direct, got nav=%v ok=%v", direct, ok)
	}
}

func TestCompileRejects(t *testing.T) {
	cases := map[string]struct {
		schema Schema
		want   string
	}{
		"empty permission rule": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf()},
				},
			}},
			want: "permission rule is empty",
		},
		"ambiguous check both direct and through": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(PermissionCheck{Relation: "admin", Through: "admin", Permission: "manage"})},
				},
			}},
			want: "ambiguous check",
		},
		"ambiguous check neither": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(PermissionCheck{})},
				},
			}},
			want: "neither a direct relation nor a through traversal",
		},
		"unknown direct relation": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("owner"))},
				},
			}},
			want: "direct relation",
		},
		"unknown userset relation": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"group": {
					Relations: map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
				},
				"doc": {
					Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "group", Relation: "admin"}}}},
					Permissions: map[string]PermissionRule{"read": AnyOf(Direct("viewer"))},
				},
			}},
			want: "unknown relation",
		},
		"userset subject on unknown type": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"doc": {
					Relations:   map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "team", Relation: "member"}}}},
					Permissions: map[string]PermissionRule{"read": AnyOf(Direct("viewer"))},
				},
			}},
			want: "unknown resource type",
		},
		"userset target on through relation": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"space": {
					Relations: map[string]RelationDef{
						"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
						"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
						"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space", Relation: "member"}}},
					},
					Permissions: map[string]PermissionRule{
						"view": AnyOf(Direct("viewer"), Through("parent", "view")),
					},
				},
			}},
			want: "must contain concrete resource subjects only",
		},
		"through permission missing on target": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
				},
				"project": {
					Relations:   map[string]RelationDef{"org": {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}}},
					Permissions: map[string]PermissionRule{"view": AnyOf(Through("org", "nonexistent"))},
				},
			}},
			want: "not found on target",
		},
		"duplicate allowed subject": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
				},
			}},
			want: "duplicate allowed subject",
		},
		"duplicate check": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"), Direct("admin"))},
				},
			}},
			want: "duplicate check",
		},
		"relation with no allowed subjects": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"org": {
					Relations:   map[string]RelationDef{"admin": {}},
					Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
				},
			}},
			want: "no allowed subjects",
		},
		"genuine cross-type cycle": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"a": {
					Relations:   map[string]RelationDef{"rel_b": {AllowedSubjects: []SubjectTypeRef{{Type: "b"}}}},
					Permissions: map[string]PermissionRule{"perm": AnyOf(Through("rel_b", "perm"))},
				},
				"b": {
					Relations:   map[string]RelationDef{"rel_a": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}}},
					Permissions: map[string]PermissionRule{"perm": AnyOf(Through("rel_a", "perm"))},
				},
			}},
			want: "circular",
		},
		"unsatisfiable self loop": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{
				"space": {
					Relations:   map[string]RelationDef{"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}}},
					Permissions: map[string]PermissionRule{"view": AnyOf(Through("parent", "view"))},
				},
			}},
			want: "unsatisfiable",
		},
		"no resource types": {
			schema: Schema{ResourceTypes: map[string]ResourceTypeDef{}},
			want:   "no resource types",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := Compile(tc.schema)
			if err == nil {
				t.Fatalf("expected compile error mentioning %q, got a compiled schema", tc.want)
			}
			if c != nil {
				t.Fatal("Compile returned a non-nil schema alongside an error")
			}
			var ce *SchemaCompileError
			if !errors.As(err, &ce) {
				t.Fatalf("expected *SchemaCompileError, got %T: %v", err, err)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatal("SchemaCompileError does not wrap sdk.ErrInvalidInput")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestCompileErrorsSortedDeterministic(t *testing.T) {
	// Two undefined direct relations produce two errors that must be reported
	// in the same sorted order every time.
	schema := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"org": {
			Relations: map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{
				"manage": AnyOf(Direct("zeta")),
				"read":   AnyOf(Direct("alpha")),
			},
		},
	}}

	var first []string
	for i := 0; i < 20; i++ {
		_, err := Compile(schema)
		var ce *SchemaCompileError
		if !errors.As(err, &ce) {
			t.Fatalf("expected *SchemaCompileError, got %T", err)
		}
		if !sort.StringsAreSorted(ce.Errors) {
			t.Fatalf("errors not sorted: %v", ce.Errors)
		}
		if first == nil {
			first = ce.Errors
			continue
		}
		if strings.Join(first, "\n") != strings.Join(ce.Errors, "\n") {
			t.Fatalf("error order not deterministic:\n%v\nvs\n%v", first, ce.Errors)
		}
	}
}

func TestSchemaDigestDeterministicAcrossIterationOrder(t *testing.T) {
	want := mustDigest(t, orgProjectSchema())
	for i := 0; i < 50; i++ {
		if got := mustDigest(t, orgProjectSchema()); got != want {
			t.Fatalf("digest not stable across compiles: %q vs %q", got, want)
		}
	}
}

func TestSchemaDigestNormalizesDeclarationOrder(t *testing.T) {
	ordered := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"doc": {
			Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}}},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("viewer"), Direct("editor")),
			},
			// editor added below so both permissions resolve.
		},
	}}
	ordered.ResourceTypes["doc"] = withEditor(ordered.ResourceTypes["doc"])

	reordered := Schema{ResourceTypes: map[string]ResourceTypeDef{
		"doc": {
			Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "service_account"}, {Type: "user"}}}},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("editor"), Direct("viewer")),
			},
		},
	}}
	reordered.ResourceTypes["doc"] = withEditor(reordered.ResourceTypes["doc"])

	if a, b := mustDigest(t, ordered), mustDigest(t, reordered); a != b {
		t.Fatalf("digest is sensitive to subject/check order: %q vs %q", a, b)
	}
}

func TestSchemaDigestChangesWithPolicy(t *testing.T) {
	base := mustDigest(t, orgProjectSchema())

	changed := orgProjectSchema()
	changed.ResourceTypes["project"] = withEditor(changed.ResourceTypes["project"])
	if mustDigest(t, changed) == base {
		t.Fatal("digest did not change after adding a relation/permission")
	}
}

// withEditor adds an `editor` relation (and keeps existing rules) so a schema
// referencing Direct("editor") compiles.
func withEditor(rt ResourceTypeDef) ResourceTypeDef {
	relations := map[string]RelationDef{"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}
	for k, v := range rt.Relations {
		relations[k] = v
	}
	permissions := map[string]PermissionRule{}
	for k, v := range rt.Permissions {
		permissions[k] = v
	}
	return ResourceTypeDef{Relations: relations, Permissions: permissions}
}

func mustDigest(t *testing.T, schema Schema) string {
	t.Helper()
	c, err := Compile(schema)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return c.Digest()
}
