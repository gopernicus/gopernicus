package mutation_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

func rel(relation, st, sid string) mutation.RelationshipRow {
	return mutation.RelationshipRow{Relation: relation, Subject: relationship.SubjectRef{Type: st, ID: sid}}
}

// TestMutationPayloadDigestDeterministic proves row order does not change the
// digest and the encoding version is published.
func TestMutationPayloadDigestDeterministic(t *testing.T) {
	scope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"}
	a := mutation.Command{Scope: scope, Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{rel("owner", "user", "u1"), rel("member", "group", "eng")}}
	b := mutation.Command{Scope: scope, Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{rel("member", "group", "eng"), rel("owner", "user", "u1")}}
	if a.PayloadDigest() != b.PayloadDigest() {
		t.Fatalf("digest must be independent of row order")
	}
	if a.PayloadEncoding() != mutation.MutationEncodingVersion {
		t.Fatalf("encoding version mismatch: %q", a.PayloadEncoding())
	}
}

// TestMutationPayloadDigestActorIndependentButStateSensitive proves the digest
// ignores the MutationID and ExpectedRevision (actor-independent, precondition-
// independent) but changes with the requested state.
func TestMutationPayloadDigestActorIndependentButStateSensitive(t *testing.T) {
	scope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"}
	base := mutation.Command{MutationID: "id-one-aaaaaaaaaaaaaaaaaaaa", Scope: scope, Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{rel("owner", "user", "u1")}}

	// Different MutationID + expected revision, same payload → same digest.
	r := mutation.Revision(5)
	same := base
	same.MutationID = "id-two-bbbbbbbbbbbbbbbbbbbb"
	same.ExpectedRevision = &r
	if base.PayloadDigest() != same.PayloadDigest() {
		t.Fatalf("digest must ignore MutationID and ExpectedRevision")
	}

	// Change relation, subject, operation, or scope → different digest.
	variants := map[string]mutation.Command{
		"relation":  {Scope: scope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel("member", "user", "u1")}},
		"subject":   {Scope: scope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel("owner", "user", "u2")}},
		"userset":   {Scope: scope, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1", Relation: "member"}}}},
		"operation": {Scope: scope, Operation: mutation.OpRevoke, Relationships: []mutation.RelationshipRow{rel("owner", "user", "u1")}},
		"scope":     {Scope: mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d2"}, Operation: mutation.OpGrant, Relationships: []mutation.RelationshipRow{rel("owner", "user", "u1")}},
	}
	for name, v := range variants {
		if v.PayloadDigest() == base.PayloadDigest() {
			t.Fatalf("changing %s must change the digest", name)
		}
	}
}

// TestMutationPayloadDigestRoleRows proves role-row payloads are covered too.
func TestMutationPayloadDigestRoleRows(t *testing.T) {
	scope := mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"}
	a := mutation.Command{Scope: scope, Operation: mutation.OpRoleAssign,
		Roles: []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "admin"}}}
	b := mutation.Command{Scope: scope, Operation: mutation.OpRoleAssign,
		Roles: []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "editor"}}}
	if a.PayloadDigest() == b.PayloadDigest() {
		t.Fatalf("different role must change the digest")
	}
}
