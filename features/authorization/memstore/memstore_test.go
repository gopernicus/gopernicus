package memstore

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

func mustCreate(t *testing.T, r *Relationships, tuples ...relationship.CreateRelationship) {
	t.Helper()
	if err := r.CreateRelationships(context.Background(), tuples); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
}

func rel(rt, rid, relation, st, sid string) relationship.CreateRelationship {
	return relationship.CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: relation, SubjectType: st, SubjectID: sid}
}

func TestGroupExpansionTransitive(t *testing.T) {
	r := NewRelationships()
	// u1 ∈ group:eng (member); group:eng is viewer of doc:d1.
	mustCreate(t, r,
		rel("group", "eng", "member", "user", "u1"),
		rel("doc", "d1", "viewer", "group", "eng"),
	)
	ok, err := r.CheckRelationWithGroupExpansion(context.Background(), "doc", "d1", "viewer", "user", "u1")
	if err != nil || !ok {
		t.Fatalf("transitive group membership should grant viewer: ok=%v err=%v", ok, err)
	}
	// u2 is not a member.
	if ok, _ := r.CheckRelationWithGroupExpansion(context.Background(), "doc", "d1", "viewer", "user", "u2"); ok {
		t.Fatalf("non-member must be denied")
	}
}

func TestGroupExpansionCycleSafe(t *testing.T) {
	r := NewRelationships()
	// A ∈ B and B ∈ A (mutual membership) must not loop.
	mustCreate(t, r,
		rel("group", "a", "member", "group", "b"),
		rel("group", "b", "member", "group", "a"),
		rel("doc", "d1", "viewer", "group", "a"),
		rel("group", "b", "member", "user", "u1"),
	)
	ok, err := r.CheckRelationWithGroupExpansion(context.Background(), "doc", "d1", "viewer", "user", "u1")
	if err != nil || !ok {
		t.Fatalf("cycle expansion should terminate and grant: ok=%v err=%v", ok, err)
	}
}

func TestUniqueSubjectResourceDoNothing(t *testing.T) {
	r := NewRelationships()
	mustCreate(t, r, rel("doc", "d1", "owner", "user", "u1"))
	// A SECOND, different relation for the same subject on the same resource is
	// a silent no-op — the existing "owner" row survives, no "member" appears.
	mustCreate(t, r, rel("doc", "d1", "member", "user", "u1"))

	if ok, _ := r.CheckRelationExists(context.Background(), "doc", "d1", "owner", "user", "u1"); !ok {
		t.Fatalf("original owner relation must survive")
	}
	if ok, _ := r.CheckRelationExists(context.Background(), "doc", "d1", "member", "user", "u1"); ok {
		t.Fatalf("second relation must have been skipped (one-relation-per-subject-per-resource)")
	}
	if n, _ := r.CountByResourceAndRelation(context.Background(), "doc", "d1", "owner"); n != 1 {
		t.Fatalf("owner count must be 1, got %d", n)
	}
}

func TestEmptyIDGetsAssigned(t *testing.T) {
	r := NewRelationships()
	// Empty RelationshipID (a cryptids.Database batch) must get an id assigned so
	// the row is readable via the listing.
	mustCreate(t, r, rel("doc", "d1", "owner", "user", "u1"))
	page, err := r.ListRelationshipsBySubject(context.Background(), "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID == "" {
		t.Fatalf("row must be present with a non-empty assigned id: %+v", page.Items)
	}
}

func TestCountIsDirectOnly(t *testing.T) {
	r := NewRelationships()
	// Direct owner + a member via group expansion; count must stay direct-only.
	mustCreate(t, r,
		rel("doc", "d1", "owner", "user", "u1"),
		rel("group", "eng", "member", "user", "u2"),
		rel("doc", "d1", "owner", "group", "eng"),
	)
	// u2 is an owner via group expansion, but the direct count is 2 (u1 + group:eng),
	// never the expanded membership.
	n, _ := r.CountByResourceAndRelation(context.Background(), "doc", "d1", "owner")
	if n != 2 {
		t.Fatalf("direct owner count must be 2 (u1 + group:eng), got %d", n)
	}
}

func TestDescendantWalk(t *testing.T) {
	r := NewRelationships()
	// space parent chain: s1 ← s2 ← s3 (s2.parent=s1, s3.parent=s2).
	mustCreate(t, r,
		rel("space", "s2", "parent", "space", "s1"),
		rel("space", "s3", "parent", "space", "s2"),
	)
	ids, err := r.LookupDescendantResourceIDs(context.Background(), "space", "parent", "space", []string{"s1"})
	if err != nil {
		t.Fatalf("descendants: %v", err)
	}
	if len(ids) != 2 || ids[0] != "s2" || ids[1] != "s3" {
		t.Fatalf("want [s2 s3], got %v", ids)
	}
}

func TestListingKeysetRoundTrip(t *testing.T) {
	r := NewRelationships()
	mustCreate(t, r,
		rel("doc", "d1", "viewer", "user", "u1"),
		rel("doc", "d2", "viewer", "user", "u1"),
		rel("doc", "d3", "viewer", "user", "u1"),
	)
	// Page size 2, then follow the cursor.
	p1, err := r.ListRelationshipsBySubject(context.Background(), "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1.Items) != 2 || !p1.HasMore || p1.NextCursor == "" {
		t.Fatalf("page1 should have 2 items and more: %+v", p1)
	}
	p2, err := r.ListRelationshipsBySubject(context.Background(), "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2.Items) != 1 || p2.HasMore {
		t.Fatalf("page2 should have the final item: %+v", p2)
	}
	// No overlap across pages.
	seen := map[string]bool{}
	for _, it := range append(p1.Items, p2.Items...) {
		if seen[it.ResourceID] {
			t.Fatalf("resource %s appeared twice across pages", it.ResourceID)
		}
		seen[it.ResourceID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 distinct resources across pages, got %d", len(seen))
	}
}

func TestListingEmptyPage(t *testing.T) {
	r := NewRelationships()
	page, err := r.ListRelationshipsByResource(context.Background(), "doc", "nope", relationship.ResourceRelationshipFilter{}, crud.ListRequest{})
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if len(page.Items) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty page shape wrong: %+v", page)
	}
}

// ---- roles kind ----

func TestRoleAssignIdempotentRetainsCreatedAt(t *testing.T) {
	roles := NewRoles()
	a := role.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor", ResourceType: "doc", ResourceID: "d1"}
	if err := roles.Assign(context.Background(), a); err != nil {
		t.Fatalf("assign: %v", err)
	}
	first, _ := roles.ListBySubject(context.Background(), "user", "u1", crud.ListRequest{})
	created := first.Items[0].CreatedAt

	if err := roles.Assign(context.Background(), a); err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	second, _ := roles.ListBySubject(context.Background(), "user", "u1", crud.ListRequest{})
	if len(second.Items) != 1 {
		t.Fatalf("duplicate assign must keep one row, got %d", len(second.Items))
	}
	if !second.Items[0].CreatedAt.Equal(created) {
		t.Fatalf("duplicate assign must retain original CreatedAt")
	}
}

func TestRoleHasExactScope(t *testing.T) {
	roles := NewRoles()
	// A global grant does NOT satisfy a scoped store lookup and vice versa.
	if err := roles.Assign(context.Background(), role.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"}); err != nil {
		t.Fatalf("assign global: %v", err)
	}
	if ok, _ := roles.HasExactRole(context.Background(), "user", "u1", "editor", "doc", "d1"); ok {
		t.Fatalf("global grant must NOT satisfy a scoped exact lookup")
	}
	if ok, _ := roles.HasExactRole(context.Background(), "user", "u1", "editor", "", ""); !ok {
		t.Fatalf("global grant must satisfy the exact global lookup")
	}
}

func TestRoleUnassignIdempotent(t *testing.T) {
	roles := NewRoles()
	// Unassign of an absent assignment is nil; repeat is nil.
	if err := roles.Unassign(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("unassign absent: %v", err)
	}
	_ = roles.Assign(context.Background(), role.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"})
	if err := roles.Unassign(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if err := roles.Unassign(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("repeat unassign: %v", err)
	}
	if ok, _ := roles.HasExactRole(context.Background(), "user", "u1", "editor", "", ""); ok {
		t.Fatalf("assignment should be gone")
	}
}
