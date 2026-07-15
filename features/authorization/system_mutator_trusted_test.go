package authorization

import (
	"context"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
)

// -----------------------------------------------------------------------------
// AZ3-3.4 — trusted SystemMutator convenience surface, its parity with the
// actor-facing guarded seam (schema digest + semantic validator), the invitation
// deterministic-MutationID idempotency contract, and the legacy raw-method removal.
// All over the REAL memstore bundle (shared-state relationship/role/mutation repos).
// -----------------------------------------------------------------------------

// newTrustedComponents builds a Components over the memstore bundle with the guardian
// invariant disabled (empty policy — last-owner protection is AZ3-3.2's concern, not
// these parity/idempotency cases) and both kinds plus the atomic mutation repository
// wired. No Guard is configured: the actor path is read-only, and the trusted
// SystemMutator is the write surface under test.
func newTrustedComponents(t *testing.T) Components {
	t.Helper()
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, Config{Model: lifecycleModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// TestSystemMutatorGrantRelationshipTrustedApplies proves the trusted convenience
// method commits an atomic grant (a bootstrap/invitation seam) that the read side
// then honors, bypassing only the host guard.
func TestSystemMutatorGrantRelationshipTrustedApplies(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	rcpt, err := comps.SystemMutator.GrantRelationship(ctx, GrantRelationshipCommand{
		MutationID:   mustID(t),
		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt.Outcome != OutcomeApplied {
		t.Fatalf("want applied, got %s", rcpt.Outcome)
	}
	res, err := comps.Service.Check(ctx, CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "edit", Resource: Resource{Type: "doc", ID: "d1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("trusted grant not visible to Check: allowed=%v err=%v", res.Allowed, err)
	}
}

// TestSystemMutatorTrustedStampsSchemaDigest proves the trusted path stamps the
// governing schema digest onto the receipt exactly as the guarded seam does (AZ3-3.1
// parity): the receipt records the schema that governed the application.
func TestSystemMutatorTrustedStampsSchemaDigest(t *testing.T) {
	comps := newTrustedComponents(t)
	digest, err := comps.Service.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest: %v", err)
	}
	rcpt, err := comps.SystemMutator.GrantRelationship(context.Background(), GrantRelationshipCommand{
		MutationID:   mustID(t),
		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if digest == "" || rcpt.SchemaDigest != digest {
		t.Fatalf("trusted receipt digest %q != governing digest %q", rcpt.SchemaDigest, digest)
	}
}

// TestSystemMutatorTrustedRunsSemanticValidator proves the trusted path runs the
// current-schema semantic validator inside Apply (parity): a grant of a relation the
// schema does not accept is refused, exactly as the actor-facing path would refuse it
// — a trusted caller is not a schema bypass.
func TestSystemMutatorTrustedRunsSemanticValidator(t *testing.T) {
	comps := newTrustedComponents(t)
	_, err := comps.SystemMutator.GrantRelationship(context.Background(), GrantRelationshipCommand{
		MutationID:   mustID(t),
		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "nonexistent_relation",
		Subject:      subjU("u1"),
	})
	if err == nil {
		t.Fatalf("trusted grant of an unknown relation must be rejected by the semantic validator")
	}
}

// TestSystemMutatorAssignRoleTrusted proves the trusted role assignment commits and
// the read side then reports the role.
func TestSystemMutatorAssignRoleTrusted(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	if _, err := comps.SystemMutator.AssignRole(ctx, AssignRoleCommand{
		MutationID: mustID(t),
		Subject:    PrincipalRef{Type: "user", ID: "u1"},
		Role:       "auditor",
	}); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	ok, err := comps.Service.HasRole(ctx, PrincipalRef{Type: "user", ID: "u1"}, "auditor", "doc", "d1")
	if err != nil || !ok {
		t.Fatalf("global trusted role not visible via HasRole fallback: ok=%v err=%v", ok, err)
	}
}

// TestInvitationStableMutationIDReplaysWithoutDuplicateBump proves the invitation
// idempotency contract this task owns: a grant retried under a STABLE derived
// MutationID replays its stored receipt (Replayed=true) and does NOT bump the scope
// revision a second time — no duplicate stored mutation, no duplicate revision.
func TestInvitationStableMutationIDReplaysWithoutDuplicateBump(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	// Deterministic id from the invitation operation identity (its resulting tuple).
	id := DeriveMutationID("invitation-grant", "doc", "d1", "owner", "user", "u1")
	cmd := GrantRelationshipCommand{
		MutationID:   id,
		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	}

	first, err := comps.SystemMutator.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if first.Outcome != OutcomeApplied || first.Replayed {
		t.Fatalf("first grant: want applied non-replay, got outcome=%s replayed=%v", first.Outcome, first.Replayed)
	}

	replay, err := comps.SystemMutator.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("replay grant: %v", err)
	}
	if !replay.Replayed {
		t.Fatalf("retry under a stable MutationID must be a replay, got replayed=false")
	}
	if replay.Revision != first.Revision {
		t.Fatalf("replay bumped the revision: first=%d replay=%d", first.Revision, replay.Revision)
	}
}

// TestTrustedDeriveMutationIDStableAndValid pins the derivation contract: it is
// deterministic, distinguishes different operation identities (including part
// boundaries), and always satisfies MutationID.Validate.
func TestTrustedDeriveMutationIDStableAndValid(t *testing.T) {
	a := DeriveMutationID("grant", "doc", "d1", "owner", "user", "u1")
	b := DeriveMutationID("grant", "doc", "d1", "owner", "user", "u1")
	if a != b {
		t.Fatalf("derivation is not deterministic: %q != %q", a, b)
	}
	if c := DeriveMutationID("grant", "doc", "d1", "owner", "user", "u2"); c == a {
		t.Fatalf("different identity derived the same id")
	}
	// Part-boundary must matter (length-prefixed): ["a","bc"] != ["ab","c"].
	if DeriveMutationID("a", "bc") == DeriveMutationID("ab", "c") {
		t.Fatalf("part boundaries alias")
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("derived id must satisfy MutationID.Validate, got %v", err)
	}
}

// TestLegacyRawMutationMethodsRemovedFromService pins the pre-tag breaking removal:
// the ordinary Service exposes no raw create/delete/assign/unassign write method — no
// unguarded synonym for the guarded lifecycle remains. AssignRole/UnassignRole exist
// only in their GUARDED form (first arg is an Actor), and the raw relationship writes
// are gone entirely.
func TestLegacyRawMutationMethodsRemovedFromService(t *testing.T) {
	svcType := reflect.TypeOf(&Service{})
	removed := []string{
		"CreateRelationships",
		"DeleteRelationship",
		"DeleteResourceRelationships",
		"DeleteByResourceAndSubject",
	}
	for _, name := range removed {
		if _, ok := svcType.MethodByName(name); ok {
			t.Fatalf("Service.%s must be removed (raw unguarded write path)", name)
		}
	}

	actorType := reflect.TypeOf(Actor{})
	for _, name := range []string{"AssignRole", "UnassignRole"} {
		m, ok := svcType.MethodByName(name)
		if !ok {
			t.Fatalf("Service.%s (guarded) must exist", name)
		}
		// method value signature: (*Service, context.Context, Actor, Command...)
		if m.Type.NumIn() < 3 || m.Type.In(2) != actorType {
			t.Fatalf("Service.%s must be the GUARDED form taking an Actor, got %s", name, m.Type)
		}
	}
}
