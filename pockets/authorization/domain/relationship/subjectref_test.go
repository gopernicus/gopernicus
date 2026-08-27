package relationship

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestSubjectRefUsersetDistinct proves group, group#member, and group#admin are
// three distinct stored subjects that never compare equal — the userset relation
// is load-bearing, not decorative.
func TestSubjectRefUsersetDistinct(t *testing.T) {
	concrete := SubjectRef{Type: "group", ID: "eng"}
	member := SubjectRef{Type: "group", ID: "eng", Relation: "member"}
	admin := SubjectRef{Type: "group", ID: "eng", Relation: "admin"}

	if concrete == member {
		t.Fatalf("group must not equal group#member")
	}
	if concrete == admin {
		t.Fatalf("group must not equal group#admin")
	}
	if member == admin {
		t.Fatalf("group#member must not equal group#admin")
	}

	if concrete.IsUserset() {
		t.Fatalf("empty Relation must be a concrete subject, not a userset")
	}
	if !member.IsUserset() || !admin.IsUserset() {
		t.Fatalf("non-empty Relation must be a userset")
	}

	if got := concrete.String(); got != "group:eng" {
		t.Fatalf("concrete String() = %q, want group:eng", got)
	}
	if got := member.String(); got != "group:eng#member" {
		t.Fatalf("member String() = %q, want group:eng#member", got)
	}
	if got := admin.String(); got != "group:eng#admin" {
		t.Fatalf("admin String() = %q, want group:eng#admin", got)
	}
}

// TestSubjectRefValidateInvalid rejects empty and malformed references and
// accepts well-formed concrete and userset subjects. Every rejection wraps
// sdk.ErrInvalidInput via ErrInvalidRef.
func TestSubjectRefValidateInvalid(t *testing.T) {
	const long = 300 // > MaxRefFieldLen

	cases := []struct {
		name    string
		ref     SubjectRef
		wantErr bool
	}{
		{"concrete ok", SubjectRef{Type: "user", ID: "u1"}, false},
		{"userset ok", SubjectRef{Type: "group", ID: "eng", Relation: "member"}, false},
		{"empty type", SubjectRef{Type: "", ID: "u1"}, true},
		{"empty id", SubjectRef{Type: "user", ID: ""}, true},
		{"both empty", SubjectRef{}, true},
		{"control char in id", SubjectRef{Type: "user", ID: "u\x00"}, true},
		{"newline in type", SubjectRef{Type: "us\ner", ID: "u1"}, true},
		{"control char in relation", SubjectRef{Type: "group", ID: "eng", Relation: "mem\tber"}, true},
		{"invalid utf8 id", SubjectRef{Type: "user", ID: "\xff\xfe"}, true},
		{"over-long type", SubjectRef{Type: strings.Repeat("a", long), ID: "u1"}, true},
		{"over-long relation", SubjectRef{Type: "group", ID: "eng", Relation: strings.Repeat("r", long)}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate(%+v) = nil, want error", tc.ref)
				}
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("Validate(%+v) error must wrap sdk.ErrInvalidInput, got %v", tc.ref, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", tc.ref, err)
			}
		})
	}
}

// TestCreateRelationshipInvalid proves a tuple with a malformed component is
// rejected and that a well-formed tuple's canonical Subject preserves a
// non-empty userset relation (no erase path).
func TestCreateRelationshipInvalid(t *testing.T) {
	ok := CreateRelationship{
		ResourceType: "org", ResourceID: "acme", Relation: "member",
		SubjectType: "group", SubjectID: "eng", SubjectRelation: "member",
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("well-formed tuple rejected: %v", err)
	}
	if s := ok.Subject(); s.Type != "group" || s.ID != "eng" || s.Relation != "member" {
		t.Fatalf("Subject() must preserve the userset relation, got %+v", s)
	}
	if !ok.Subject().IsUserset() {
		t.Fatalf("Subject() of a userset tuple must report IsUserset")
	}

	bad := []CreateRelationship{
		{ResourceType: "", ResourceID: "acme", Relation: "member", SubjectType: "group", SubjectID: "eng"},
		{ResourceType: "org", ResourceID: "", Relation: "member", SubjectType: "group", SubjectID: "eng"},
		{ResourceType: "org", ResourceID: "acme", Relation: "", SubjectType: "group", SubjectID: "eng"},
		{ResourceType: "org", ResourceID: "acme", Relation: "member", SubjectType: "", SubjectID: "eng"},
		{ResourceType: "org", ResourceID: "acme", Relation: "member", SubjectType: "group", SubjectID: ""},
		{ResourceType: "org", ResourceID: "acme\x00", Relation: "member", SubjectType: "group", SubjectID: "eng"},
	}
	for i, c := range bad {
		if err := c.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("bad tuple %d: want sdk.ErrInvalidInput, got %v", i, err)
		}
	}
}
