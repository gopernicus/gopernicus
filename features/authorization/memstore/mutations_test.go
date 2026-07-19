package memstore

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

func mustID(t *testing.T) mutation.MutationID {
	t.Helper()
	id, err := mutation.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func grantOwner(t *testing.T, rid, subjectID string) mutation.Command {
	return mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: rid},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: subjectID}}},
	}
}

// TestMutationApplyVisibleToReads proves the shared-state design: a grant applied
// through the Mutations repository is immediately visible to the sibling
// Relationships store (Check + Count) built from the SAME bundle.
func TestMutationApplyVisibleToReads(t *testing.T) {
	ctx := context.Background()
	store := New()
	m := store.Mutations()
	rels := store.Relationships()

	if _, err := m.Apply(ctx, grantOwner(t, "d1", "u1"), nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ok, _ := rels.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); !ok {
		t.Fatalf("a grant via Apply must be visible to the sibling relationship store")
	}
	if ok, _ := rels.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "owner", "user", "u1", 0); !ok {
		t.Fatalf("a grant via Apply must be visible to group-expansion checks")
	}
	if n, _ := rels.CountByResourceAndRelation(ctx, "doc", "d1", "owner"); n != 1 {
		t.Fatalf("owner count via the sibling store must be 1, got %d", n)
	}
}

// TestMutationGuardianDirectAnchorOwner proves a group#member owner is NOT a
// direct anchor: with only a concrete owner and a userset owner present, revoking
// the concrete owner drops the direct-anchor count to zero and is blocked.
func TestMutationGuardianDirectAnchorOwner(t *testing.T) {
	ctx := context.Background()
	store := New()
	m := store.Mutations()

	// Concrete owner establishes the minimum.
	if _, err := m.Apply(ctx, grantOwner(t, "d1", "u1"), nil); err != nil {
		t.Fatalf("establish owner: %v", err)
	}
	// A userset (group#member) owner is allowed while a direct anchor remains, but
	// it is NOT itself a direct anchor.
	usersetOwner := mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "group", ID: "eng", Relation: "member"}}},
	}
	if rcpt, err := m.Apply(ctx, usersetOwner, nil); err != nil || rcpt.Outcome != mutation.OutcomeApplied {
		t.Fatalf("userset owner grant with a direct anchor present must apply: rcpt=%+v err=%v", rcpt, err)
	}

	// Revoking the sole DIRECT owner leaves only the userset owner: blocked.
	revokeConcrete := mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}},
	}
	rcpt, err := m.Apply(ctx, revokeConcrete, nil)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rcpt.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("a group#member owner is not a direct anchor: revoking the last concrete owner must be invariant_blocked, got %q", rcpt.Outcome)
	}
}

// TestMutationRevisionScopeIsolation proves each scope carries an independent
// revision anchor: mutations on d1 never advance d2's revision.
func TestMutationRevisionScopeIsolation(t *testing.T) {
	ctx := context.Background()
	store := New()
	m := store.Mutations()

	a1, _ := m.Apply(ctx, grantOwner(t, "d1", "u1"), nil)
	a2, _ := m.Apply(ctx, grantOwner(t, "d1", "u2"), nil)
	b1, _ := m.Apply(ctx, grantOwner(t, "d2", "u1"), nil)

	if a1.Revision != 1 || a2.Revision != 2 {
		t.Fatalf("d1 revisions must be 1 then 2, got %d then %d", a1.Revision, a2.Revision)
	}
	if b1.Revision != 1 {
		t.Fatalf("d2's first mutation must be revision 1 (independent scope), got %d", b1.Revision)
	}
}

// TestMutationEmptyGuardianPolicyAllowsMemberFirst proves the invariant input is
// configurable: with an empty guardian policy, a member-first command on a fresh
// resource is no longer blocked.
func TestMutationEmptyGuardianPolicyAllowsMemberFirst(t *testing.T) {
	ctx := context.Background()
	store := New(WithGuardianPolicy(mutation.GuardianPolicy{}))
	m := store.Mutations()

	member := mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}},
	}
	rcpt, err := m.Apply(ctx, member, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rcpt.Outcome != mutation.OutcomeApplied {
		t.Fatalf("an empty guardian policy must allow a member-first command, got %q", rcpt.Outcome)
	}
}
