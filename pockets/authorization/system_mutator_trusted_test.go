package authorization

import (
	"context"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// newTrustedComponents builds a Components over the memstore bundle with the guardian
// invariant disabled (empty policy — last-owner protection is AZ3-3.2's concern, not
// these parity/idempotency cases) and both kinds plus the atomic mutation repository
// wired. No Guard is configured: the actor path is read-only, and the trusted
// SystemMutator is the write surface under test.
func newTrustedComponents(t *testing.T) Components {
	t.Helper()
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	comps, err := New(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, WithRelationshipModel(lifecycleModel()))
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

	rcpt, err := comps.SystemMutator.GrantRelationship(ctx, mutations.GrantRelationshipCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("want applied, got %s", rcpt.Outcome)
	}
	res, err := comps.Decisions.Check(ctx, authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "edit", Resource: authmodel.Resource{Type: "doc", ID: "d1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("trusted grant not visible to Check: allowed=%v err=%v", res.Allowed, err)
	}
}

// TestSystemMutatorTrustedRunsSemanticValidator proves the trusted path runs the
// current-schema semantic validator inside Apply (parity): a grant of a relation the
// schema does not accept is refused, exactly as the actor-facing path would refuse it
// — a trusted caller is not a schema bypass.
func TestSystemMutatorTrustedRunsSemanticValidator(t *testing.T) {
	comps := newTrustedComponents(t)
	_, err := comps.SystemMutator.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{

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

	if _, err := comps.SystemMutator.AssignRole(ctx, mutations.AssignRoleCommand{

		Subject: authmodel.PrincipalRef{Type: "user", ID: "u1"},
		Role:    "auditor",
	}); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	ok, err := comps.Roles.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "auditor", "doc", "d1")
	if err != nil || !ok {
		t.Fatalf("global trusted role not visible via HasRole fallback: ok=%v err=%v", ok, err)
	}
}

func TestTrustedDuplicateGrantReportsNoChange(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	// Deterministic id from the invitation operation identity (its resulting tuple).
	cmd := mutations.GrantRelationshipCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	}

	first, err := comps.SystemMutator.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if first.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first grant: want applied, got outcome=%s", first.Outcome)
	}

	replay, err := comps.SystemMutator.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("replay grant: %v", err)
	}
	if replay.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("duplicate grant: %+v", replay)
	}
}

// TestBaselineWriterHeldApartFromService pins capability placement: the ordinary
// Service exposes no baseline create/delete methods. Those normal state operations
// live on Components.RelationshipWriter; AssignRole/UnassignRole remain guarded on
// Service because the baseline capability is relationship-specific.
func TestBaselineWriterHeldApartFromService(t *testing.T) {
	svcType := reflect.TypeOf(&mutations.Service{})
	removed := []string{
		"CreateRelationships",
		"DeleteRelationship",
		"DeleteResourceRelationships",
		"DeleteByResourceAndSubject",
	}
	for _, name := range removed {
		if _, ok := svcType.MethodByName(name); ok {
			t.Fatalf("Service.%s must stay on the separately held RelationshipWriter", name)
		}
	}

	actorType := reflect.TypeOf(mutations.Actor{})
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
