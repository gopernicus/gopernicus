package authorization

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/memstore"
)

// usersetSchema mirrors the storetest fixture: doc.view = Direct(viewer), with
// doc.viewer allowing the concrete group plus the group#member, group#admin, and
// org#member usersets as DISTINCT allowed subjects; group carries member (nested)
// and admin; org carries member. It exercises the exact-pair validator and the
// relation-aware userset expansion together.
func usersetSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "group", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"admin":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
		}},
		{Name: "org", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "doc", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{
					{Type: "user"},
					{Type: "group"},
					{Type: "group", Relation: "member"},
					{Type: "group", Relation: "admin"},
					{Type: "org", Relation: "member"},
				}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"))},
		}},
	})
}

// usersetService builds the engine and returns it together with the backing
// relationship store, so tests SEED via the store PORT (the raw Service write path
// was removed at AZ3-3.4) and exercise the engine via the Service.
func usersetService(t *testing.T) (*Service, *memstore.Relationships) {
	t.Helper()
	store := memstore.NewRelationships()
	comps, err := NewService(Repositories{Relationships: store}, Config{Model: usersetSchema()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service, store
}

func tuple(rt, rid, relation, st, sid, subjRel string) CreateRelationship {
	return CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: relation, SubjectType: st, SubjectID: sid, SubjectRelation: subjRel}
}

func usersetView(t *testing.T, svc *Service, st, sid, docID string, want bool) {
	t.Helper()
	res, err := svc.Check(context.Background(), CheckRequest{
		Principal:  PrincipalRef{Type: st, ID: sid},
		Permission: "view",
		Resource:   Resource{Type: "doc", ID: docID},
	})
	if err != nil {
		t.Fatalf("Check(%s:%s, doc:%s): %v", st, sid, docID, err)
	}
	if res.Allowed != want {
		t.Fatalf("view(%s:%s, doc:%s) = %v, want %v (reason %q)", st, sid, docID, res.Allowed, want, res.Reason)
	}
}

// TestUsersetMemberAdminSeparation proves the stored userset relation changes the
// outcome: a group#member grant is satisfied only by members, a group#admin grant
// only by admins, and neither satisfies the other.
func TestUsersetMemberAdminSeparation(t *testing.T) {
	svc, store := usersetService(t)
	ctx := context.Background()
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		tuple("group", "g", "member", "user", "u_m", ""),
		tuple("group", "g", "admin", "user", "u_a", ""),
		tuple("doc", "dM", "viewer", "group", "g", "member"),
		tuple("doc", "dA", "viewer", "group", "g", "admin"),
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	usersetView(t, svc, "user", "u_m", "dM", true)
	usersetView(t, svc, "user", "u_m", "dA", false)
	usersetView(t, svc, "user", "u_a", "dA", true)
	usersetView(t, svc, "user", "u_a", "dM", false)
}

// TestNestedUsersetTraversal proves a userset may contain another exact userset
// where the schema allows it (nested membership), with the member/admin distinction
// preserved across the nesting.
func TestNestedUsersetTraversal(t *testing.T) {
	svc, store := usersetService(t)
	ctx := context.Background()
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		tuple("group", "inner", "member", "user", "u1", ""),
		tuple("group", "outer", "member", "group", "inner", "member"),
		tuple("doc", "d1", "viewer", "group", "outer", "member"),
		tuple("group", "outer", "admin", "user", "u2", ""),
		tuple("doc", "d2", "viewer", "group", "outer", "admin"),
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	usersetView(t, svc, "user", "u1", "d1", true)  // nested member chain
	usersetView(t, svc, "user", "u1", "d2", false) // not an admin
	usersetView(t, svc, "user", "u2", "d2", true)  // direct admin userset
	usersetView(t, svc, "user", "u2", "d1", false) // not a member
}

// TestUsersetCyclePerRelation proves the expansion cycle is relation-aware: a
// member-userset cycle reaches the member userset but never leaks into the admin
// userset of the same group.
func TestUsersetCyclePerRelation(t *testing.T) {
	svc, store := usersetService(t)
	ctx := context.Background()
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		tuple("group", "a", "member", "group", "b", "member"),
		tuple("group", "b", "member", "group", "a", "member"),
		tuple("group", "b", "member", "user", "u1", ""),
		tuple("doc", "d1", "viewer", "group", "a", "member"),
		tuple("doc", "d2", "viewer", "group", "a", "admin"),
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	usersetView(t, svc, "user", "u1", "d1", true)  // through the member cycle
	usersetView(t, svc, "user", "u1", "d2", false) // member cycle never yields a#admin
}

// TestUsersetConcreteGroupGrantRelationAware proves a concrete-group grant names
// the group entity, not its members: only the principal that IS the group matches.
func TestUsersetConcreteGroupGrantRelationAware(t *testing.T) {
	svc, store := usersetService(t)
	ctx := context.Background()
	if err := store.CreateRelationships(ctx, []CreateRelationship{
		tuple("group", "g", "member", "user", "u1", ""),
		tuple("doc", "dc", "viewer", "group", "g", ""), // concrete group:g
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	usersetView(t, svc, "group", "g", "dc", true)  // the concrete group entity itself
	usersetView(t, svc, "user", "u1", "dc", false) // a member is not the concrete group
}

// TestUsersetMissingRelationRejected proves the exact-pair validator rejects a
// tuple whose (subject type, relation) pair is not declared: a concrete group and
// a group#admin are both refused where only group#member is allowed.
func TestUsersetMissingRelationRejected(t *testing.T) {
	svc, _ := usersetService(t)
	// The exact-pair rejection lives in the schema validator; the store port does not
	// validate, and the raw Service write path was removed at AZ3-3.4 — so this
	// exercises ValidateRelationships directly.
	if err := svc.ValidateRelationships([]CreateRelationship{
		tuple("org", "acme", "member", "group", "eng", ""), // concrete, missing #member
	}); err == nil {
		t.Fatalf("concrete group must be rejected where only group#member is allowed")
	}
	if err := svc.ValidateRelationships([]CreateRelationship{
		tuple("org", "acme", "member", "group", "eng", "admin"), // wrong userset relation
	}); err == nil {
		t.Fatalf("group#admin must not satisfy a group#member requirement")
	}
	if err := svc.ValidateRelationships([]CreateRelationship{
		tuple("org", "acme", "member", "group", "eng", "member"), // exact pair
	}); err != nil {
		t.Fatalf("group#member must be accepted: %v", err)
	}
}
