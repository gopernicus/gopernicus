package memory

import (
	"context"
	"errors"
	"testing"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func grantOwner(t *testing.T, rid, subjectID string) mutation.Command {
	return mutation.Command{

		Target:        mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: rid},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: subjectID}}},
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
	store := New(WithGuardianPolicy(mutation.DefaultGuardianPolicy()))
	m := store.Mutations()

	// Concrete owner establishes the minimum.
	if _, err := m.Apply(ctx, grantOwner(t, "d1", "u1"), nil); err != nil {
		t.Fatalf("establish owner: %v", err)
	}
	// A userset (group#member) owner is allowed while a direct anchor remains, but
	// it is NOT itself a direct anchor.
	usersetOwner := mutation.Command{

		Target:        mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "group", ID: "eng", Relation: "member"}}},
	}
	if rcpt, err := m.Apply(ctx, usersetOwner, nil); err != nil || rcpt.Outcome != mutation.OutcomeApplied {
		t.Fatalf("userset owner grant with a direct anchor present must apply: rcpt=%+v err=%v", rcpt, err)
	}

	// Revoking the sole DIRECT owner leaves only the userset owner: blocked.
	revokeConcrete := mutation.Command{

		Target:        mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
	}
	rcpt, err := m.Apply(ctx, revokeConcrete, nil)
	if !errors.Is(err, mutation.ErrInvariantBlocked) || rcpt != nil {
		t.Fatalf("revoking the last concrete owner: result=%+v err=%v", rcpt, err)
	}
}

// Empty guardian policy permits ordinary non-owner establishment.
func TestMutationEmptyGuardianPolicyAllowsMemberFirst(t *testing.T) {
	ctx := context.Background()
	store := New(WithGuardianPolicy(mutation.GuardianPolicy{}))
	m := store.Mutations()

	member := mutation.Command{

		Target:        mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
	}
	rcpt, err := m.Apply(ctx, member, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rcpt.Outcome != mutation.OutcomeApplied {
		t.Fatalf("an empty guardian policy must allow a member-first command, got %q", rcpt.Outcome)
	}
}
