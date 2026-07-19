package mutation_test

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

func rev(v mutation.Revision) *mutation.Revision { return &v }

func strongID(t *testing.T) mutation.MutationID {
	t.Helper()
	id, err := mutation.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func grantCmd(t *testing.T) mutation.Command {
	t.Helper()
	return mutation.Command{
		MutationID:    strongID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}},
	}
}

// TestMutationIDNewIsStrongAndUnique proves the canonical generator clears the
// structural floor and does not collide.
func TestMutationIDNewIsStrongAndUnique(t *testing.T) {
	seen := map[mutation.MutationID]bool{}
	for i := 0; i < 1000; i++ {
		id := strongID(t)
		if err := id.Validate(); err != nil {
			t.Fatalf("NewMutationID produced an invalid id %q: %v", id, err)
		}
		if len(id) < mutation.MinMutationIDLen {
			t.Fatalf("NewMutationID too short: %d < %d", len(id), mutation.MinMutationIDLen)
		}
		if seen[id] {
			t.Fatalf("duplicate MutationID: %q", id)
		}
		seen[id] = true
	}
}

// TestMutationIDValidateRejectsWeak proves empty, short, and control-bearing ids
// are rejected as invalid commands.
func TestMutationIDValidateRejectsWeak(t *testing.T) {
	cases := map[string]mutation.MutationID{
		"empty":   "",
		"short":   "abc123",
		"control": mutation.MutationID("aaaaaaaaaaaaaaaaaaaaaaaaaa\x00"),
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			err := id.Validate()
			if err == nil {
				t.Fatalf("id %q must be rejected", id)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestMutationCommandValidateAccepts covers each well-formed operation shape.
func TestMutationCommandValidateAccepts(t *testing.T) {
	resScope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"}
	subScope := mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"}
	ok := []mutation.Command{
		grantCmd(t),
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpReplace,
			Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}}},
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRevoke,
			Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}}},
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpPurge},
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpTeardown},
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRoleAssign,
			Roles: []mutation.RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "editor"}}},
		{MutationID: strongID(t), Scope: subScope, Operation: mutation.OpRoleAssign,
			Roles: []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "admin"}}},
		// One subject may hold multiple DISTINCT roles in one command; only an
		// exact-duplicate (subject, role) row is rejected.
		{MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRoleAssign,
			Roles: []mutation.RoleRow{
				{SubjectType: "user", SubjectID: "u2", Role: "editor"},
				{SubjectType: "user", SubjectID: "u2", Role: "auditor"},
			}},
	}
	for i, c := range ok {
		if err := c.Validate(); err != nil {
			t.Fatalf("case %d must validate: %v", i, err)
		}
	}
}

// TestMutationCommandValidateRejects proves the operation/scope/rows matrix and
// the single-scope structural guarantee.
func TestMutationCommandValidateRejects(t *testing.T) {
	resScope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"}
	subScope := mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"}
	rel := mutation.RelationshipRow{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}
	role := mutation.RoleRow{SubjectType: "user", SubjectID: "u2", Role: "editor"}

	cases := map[string]mutation.Command{
		"weak id":                {MutationID: "short", Scope: resScope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel}},
		"bad scope kind":         {MutationID: strongID(t), Scope: mutation.ScopeKey{Kind: "planet", Type: "doc", ID: "d1"}, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel}},
		"empty scope id":         {MutationID: strongID(t), Scope: mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc"}, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel}},
		"grant on subject scope": {MutationID: strongID(t), Scope: subScope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel}},
		"grant no rows":          {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpGrant},
		"grant with role rows":   {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel}, Roles: []mutation.RoleRow{role}},
		"purge with rows":        {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpPurge, Relationships: []mutation.RelationshipRow{rel}},
		"role no rows":           {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRoleAssign},
		"role with rel rows":     {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRoleAssign, Relationships: []mutation.RelationshipRow{rel}, Roles: []mutation.RoleRow{role}},
		"global role subject mismatch": {MutationID: strongID(t), Scope: subScope, Operation: mutation.OpRoleAssign,
			Roles: []mutation.RoleRow{{SubjectType: "user", SubjectID: "someone-else", Role: "admin"}}},
		"unknown operation": {MutationID: strongID(t), Scope: resScope, Operation: "detonate", Relationships: []mutation.RelationshipRow{rel}},
		"grant duplicate subject different relation": {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpGrant,
			Relationships: []mutation.RelationshipRow{
				{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"grant duplicate subject same relation": {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpGrant,
			Relationships: []mutation.RelationshipRow{
				{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"revoke duplicate subject": {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRevoke,
			Relationships: []mutation.RelationshipRow{
				{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"role assign duplicate row": {MutationID: strongID(t), Scope: resScope, Operation: mutation.OpRoleAssign,
			Roles: []mutation.RoleRow{
				{SubjectType: "user", SubjectID: "u2", Role: "editor"},
				{SubjectType: "user", SubjectID: "u2", Role: "editor"},
			}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.Validate()
			if err == nil {
				t.Fatalf("command %q must be rejected", name)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestMutationScopeCanonicalOrdering proves the lock-order key is stable, distinct
// per scope, and disambiguates kind/type/id boundaries (no aliasing).
func TestMutationScopeCanonicalOrdering(t *testing.T) {
	// A resource scope and a subject scope with the same Type/ID must not alias.
	res := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "user", ID: "u1"}
	sub := mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"}
	if res.Canonical() == sub.Canonical() {
		t.Fatalf("resource and subject scopes must not share a canonical key")
	}
	// Boundary aliasing: ("ab","c") and ("a","bc") must differ.
	a := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "ab", ID: "c"}
	b := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "a", ID: "bc"}
	if a.Canonical() == b.Canonical() {
		t.Fatalf("length-prefixed canonical key must not alias across field boundaries")
	}
	// Deterministic.
	if res.Canonical() != res.Canonical() {
		t.Fatalf("canonical key must be deterministic")
	}
}

func TestMutationExpectedRevisionOptional(t *testing.T) {
	c := grantCmd(t)
	if c.ExpectedRevision != nil {
		t.Fatalf("default command must have no expected revision")
	}
	c.ExpectedRevision = rev(3)
	if err := c.Validate(); err != nil {
		t.Fatalf("an expected revision must not break structural validation: %v", err)
	}
}
