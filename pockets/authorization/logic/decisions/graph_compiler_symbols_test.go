package decisions

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestCompileUsesReferenceNameContract(t *testing.T) {
	fields := []struct {
		name string
		make func(string) Model
	}{
		{"resource type name", func(name string) Model {
			s := orgProjectSchema()
			s.ResourceTypes[name] = s.ResourceTypes["org"]
			delete(s.ResourceTypes, "org")
			s.ResourceTypes["project"].Relations["org"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: name}}}
			return s
		}},
		{"relation name", func(name string) Model {
			s := orgProjectSchema()
			s.ResourceTypes["org"].Relations[name] = s.ResourceTypes["org"].Relations["admin"]
			delete(s.ResourceTypes["org"].Relations, "admin")
			s.ResourceTypes["org"].Permissions["manage"] = AnyOf(Direct(name))
			return s
		}},
		{"permission name", func(name string) Model {
			s := orgProjectSchema()
			s.ResourceTypes["org"].Permissions[name] = s.ResourceTypes["org"].Permissions["manage"]
			delete(s.ResourceTypes["org"].Permissions, "manage")
			s.ResourceTypes["project"].Permissions["view"] = AnyOf(Through("org", name))
			return s
		}},
		{"allowed subject type", func(name string) Model {
			s := orgProjectSchema()
			s.ResourceTypes["org"].Relations["admin"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: name}}}
			return s
		}},
		{"allowed subject relation", func(name string) Model {
			s := usersetSchema()
			s.ResourceTypes["group"].Relations[name] = s.ResourceTypes["group"].Relations["member"]
			delete(s.ResourceTypes["group"].Relations, "member")
			s.ResourceTypes["doc"].Relations["viewer"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "group", Relation: name}}}
			return s
		}},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			for _, valid := range []string{"a.b:c/#-_ 空", strings.Repeat("x", tuples.MaxRefFieldLen)} {
				if _, err := Compile(field.make(valid)); err != nil {
					t.Fatalf("valid opaque name rejected: %v", err)
				}
			}
			invalidNames := []string{strings.Repeat("x", tuples.MaxRefFieldLen+1), "bad\nname", string([]byte{0xff})}
			if field.name != "allowed subject relation" {
				invalidNames = append(invalidNames, "")
			}
			for _, invalid := range invalidNames {
				_, err := Compile(field.make(invalid))
				want := tuples.ValidateRefField(field.name, invalid)
				if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), want.Error()) {
					t.Fatalf("Compile = %v; want shared field refusal %v", err, want)
				}
			}
		})
	}
}

func TestCompileRejectsMalformedCheckFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check Expression
		want  string
	}{
		{"ignored direct permission", Expression{Relation: "admin", Permission: "manage"}, "permission requires Through"},
		{"permission without traversal", Expression{Permission: "manage"}, "must name exactly one operation"},
		{"direct name", Direct("bad\n"), "relation contains a control character"},
		{"through name", Through("bad\n", "manage"), "relation contains a control character"},
		{"through permission", Through("org", "bad\n"), "permission contains a control character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := orgProjectSchema()
			s.ResourceTypes["project"].Permissions["view"] = AnyOf(tc.check)
			_, err := Compile(s)
			if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile = %v; want %s", err, tc.want)
			}
		})
	}
}

func TestCompileDistinguishesDottedPermissionPairs(t *testing.T) {
	// The middle two permissions have the same dotted display name, a.b.c,
	// but are different graph vertices and this chain is acyclic.
	s := Model{ResourceTypes: map[string]ResourceTypeDef{
		"start": {
			Relations:   map[string]RelationDef{"next": {AllowedSubjects: []SubjectTypeRef{{Type: "a.b"}}}},
			Permissions: map[string]Expression{"view": AnyOf(Through("next", "c"))},
		},
		"a.b": {
			Relations:   map[string]RelationDef{"next": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}}},
			Permissions: map[string]Expression{"c": AnyOf(Through("next", "b.c"))},
		},
		"a": {
			Relations:   map[string]RelationDef{"next": {AllowedSubjects: []SubjectTypeRef{{Type: "leaf"}}}},
			Permissions: map[string]Expression{"b.c": AnyOf(Through("next", "read"))},
		},
		"leaf": {
			Relations:   map[string]RelationDef{"reader": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]Expression{"read": AnyOf(Direct("reader"))},
		},
	}}
	if _, err := Compile(s); err != nil {
		t.Fatalf("acyclic dotted model: %v", err)
	}
	if err := ValidateSchema(s); err != nil {
		t.Fatalf("shared acyclic validator: %v", err)
	}
	s.ResourceTypes["leaf"].Relations["next"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "a.b"}}}
	s.ResourceTypes["leaf"].Permissions["read"] = AnyOf(Direct("reader"), Through("next", "c"))
	_, err := Compile(s)
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "circular through-relation") {
		t.Fatalf("real dotted cycle accepted: %v", err)
	}
}
