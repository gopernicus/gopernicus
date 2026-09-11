package mutations

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

func grantCmd(t *testing.T) Command {
	t.Helper()
	return Command{Target: Target{Kind: TargetResource, Type: "doc", ID: "d1"}, Operation: OpGrant, Relationships: []RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}}}
}
func TestMutationCommandValidateAccepts(t *testing.T) {
	resScope := Target{Kind: TargetResource, Type: "doc", ID: "d1"}
	subScope := Target{Kind: TargetSubject, Type: "user", ID: "u1"}
	ok := []Command{
		grantCmd(t),
		{Target: resScope, Operation: OpReplace,
			Relationships: []RelationshipRow{{Relation: "member", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}}},
		{Target: resScope, Operation: OpRevoke,
			Relationships: []RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}}},
		{Target: resScope, Operation: OpPurge},
		{Target: resScope, Operation: OpTeardown},
		{Target: resScope, Operation: OpRoleAssign,
			Roles: []RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "editor"}}},
		{Target: subScope, Operation: OpRoleAssign,
			Roles: []RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "admin"}}},
		// One subject may hold multiple DISTINCT roles in one command; only an
		// exact-duplicate (subject, role) row is rejected.
		{Target: resScope, Operation: OpRoleAssign,
			Roles: []RoleRow{
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
	resScope := Target{Kind: TargetResource, Type: "doc", ID: "d1"}
	subScope := Target{Kind: TargetSubject, Type: "user", ID: "u1"}
	rel := RelationshipRow{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}
	role := RoleRow{SubjectType: "user", SubjectID: "u2", Role: "editor"}

	cases := map[string]Command{
		"bad scope kind":         {Target: Target{Kind: "planet", Type: "doc", ID: "d1"}, Operation: OpGrant, Relationships: []RelationshipRow{rel}},
		"empty scope id":         {Target: Target{Kind: TargetResource, Type: "doc"}, Operation: OpGrant, Relationships: []RelationshipRow{rel}},
		"grant on subject scope": {Target: subScope, Operation: OpGrant, Relationships: []RelationshipRow{rel}},
		"grant no rows":          {Target: resScope, Operation: OpGrant},
		"grant with role rows":   {Target: resScope, Operation: OpGrant, Relationships: []RelationshipRow{rel}, Roles: []RoleRow{role}},
		"purge with rows":        {Target: resScope, Operation: OpPurge, Relationships: []RelationshipRow{rel}},
		"role no rows":           {Target: resScope, Operation: OpRoleAssign},
		"role with rel rows":     {Target: resScope, Operation: OpRoleAssign, Relationships: []RelationshipRow{rel}, Roles: []RoleRow{role}},
		"global role subject mismatch": {Target: subScope, Operation: OpRoleAssign,
			Roles: []RoleRow{{SubjectType: "user", SubjectID: "someone-else", Role: "admin"}}},
		"unknown operation": {Target: resScope, Operation: "detonate", Relationships: []RelationshipRow{rel}},
		"grant duplicate subject different relation": {Target: resScope, Operation: OpGrant,
			Relationships: []RelationshipRow{
				{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "member", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"grant duplicate subject same relation": {Target: resScope, Operation: OpGrant,
			Relationships: []RelationshipRow{
				{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"revoke duplicate subject": {Target: resScope, Operation: OpRevoke,
			Relationships: []RelationshipRow{
				{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
				{Relation: "member", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}},
			}},
		"role assign duplicate row": {Target: resScope, Operation: OpRoleAssign,
			Roles: []RoleRow{
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
