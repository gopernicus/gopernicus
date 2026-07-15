package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// AZ3-4.1 host proof suite. Every case runs over the REAL guarded composition
// newAuthorization() builds — the same wiring run() serves: the shared-state memstore
// bundle, the project-scoped guardian minimum, and the host MutationGuard (manage_access
// + platform-admin over the DecisionView). It proves the composed HOST behavior: an
// untrusted actor cannot self-grant, invitation acceptance is visibly trusted and
// idempotent through the SystemMutator, ordinary host code has no raw write or
// constructible system actor, and the three postures stay demonstrable. There is no
// browser flow — the actor-facing HTTP mutation surface is deferred with AZADM.

// hostAuthz builds the guarded components the way run() does. It fatals on error so the
// construction matrix (a guard requires a mutation repository, etc.) is exercised.
func hostAuthz(t *testing.T) authorization.Components {
	t.Helper()
	comps, err := newAuthorization()
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	return comps
}

func mustMutationID(t *testing.T) authorization.MutationID {
	t.Helper()
	id, err := authorization.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

// seedTrustedOwner establishes a resource's owner through the trusted SystemMutator so an
// actor can then prove manage_access through the guard (the first owner is inherently
// trusted — it cannot yet prove it manages the resource).
func seedTrustedOwner(t *testing.T, sm *authorization.SystemMutator, resourceID, userID string) {
	t.Helper()
	if _, err := sm.GrantRelationship(context.Background(), authorization.GrantRelationshipCommand{
		MutationID:   mustMutationID(t),
		ResourceType: demoResourceType,
		ResourceID:   resourceID,
		Relation:     "owner",
		Subject:      authorization.SubjectRef{Type: "user", ID: userID},
	}); err != nil {
		t.Fatalf("seed owner %s on %s: %v", userID, resourceID, err)
	}
}

func actor(userID string) authorization.Actor {
	return hostActor(identity.Principal{Type: "user", ID: userID})
}

func allowed(t *testing.T, svc *authorization.Service, userID, permission, resourceID string) bool {
	t.Helper()
	res, err := svc.Check(context.Background(), authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: "user", ID: userID},
		Permission: permission,
		Resource:   authorization.Resource{Type: demoResourceType, ID: resourceID},
	})
	if err != nil {
		t.Fatalf("Check %s %s/%s: %v", userID, permission, resourceID, err)
	}
	return res.Allowed
}

// TestAuthorizationCompositionGuardedPosture proves newAuthorization() wires the GUARDED
// actor-mutation posture (Config.Guard set), not the read-only one: an unauthorized
// actor-facing grant fails with a policy denial (forbidden), NOT ErrMutationsNotConfigured
// (which is the read-only/unwired posture). The SystemMutator is returned separately.
func TestAuthorizationCompositionGuardedPosture(t *testing.T) {
	comps := hostAuthz(t)
	if comps.Service == nil || comps.SystemMutator == nil {
		t.Fatal("guarded composition must return both Service and SystemMutator")
	}
	_, err := comps.Service.GrantRelationship(context.Background(), actor("nobody"), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "x"},
	})
	if errors.Is(err, authorization.ErrMutationsNotConfigured) {
		t.Fatal("actor mutation hit the read-only posture: Config.Guard was not wired")
	}
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("unauthorized actor grant: want forbidden, got %v", err)
	}
}

// TestHostMutationGuardManageAccessAllowsAndDenies proves the host guard reads
// manage_access (its backing owner relation) through the DecisionView: an owner actor's
// grant commits, a non-owner actor's grant is denied and commits nothing.
func TestHostMutationGuardManageAccessAllowsAndDenies(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	seedTrustedOwner(t, sm, "p1", "u-owner")

	// The owner may grant a member.
	rcpt, err := svc.GrantRelationship(context.Background(), actor("u-owner"), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-member"},
	})
	if err != nil || rcpt.Outcome != authorization.OutcomeApplied {
		t.Fatalf("owner grant: outcome=%v err=%v", rcpt.Outcome, err)
	}
	if !allowed(t, svc, "u-member", demoPermission, "p1") {
		t.Fatal("granted member does not have view")
	}

	// A non-owner may not, and nothing is written.
	_, err = svc.GrantRelationship(context.Background(), actor("u-stranger"), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-intruder"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-owner grant: want forbidden, got %v", err)
	}
	if allowed(t, svc, "u-intruder", demoPermission, "p1") {
		t.Fatal("denied grant committed a member row")
	}
}

// TestGuardedActorCannotSelfGrant proves an untrusted actor cannot self-escalate: a
// non-owner granting ITSELF owner (or a lesser relation) is denied by the guard before
// Apply, and no row is written. This is the acceptance's "untrusted service calls cannot
// self-grant".
func TestGuardedActorCannotSelfGrant(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	seedTrustedOwner(t, sm, "p1", "u-owner")

	_, err := svc.GrantRelationship(context.Background(), actor("u-evil"), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p1", Relation: "owner",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-evil"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-escalation to owner: want forbidden, got %v", err)
	}
	if allowed(t, svc, "u-evil", manageAccessPerm, "p1") {
		t.Fatal("self-escalation wrote an owner row (manage_access now satisfied)")
	}
}

// TestHostMutationGuardPlatformAdminShortCircuit proves the platform-admin recipe is
// composed IN the host guard over the DecisionView: a platform admin who is NOT a project
// owner may still drive an actor-facing grant. The short-circuit is removed by a trusted
// teardown of the platform-admin scope — after which the same actor is denied, proving the
// guard re-reads live authorization data through the view rather than caching a decision.
func TestHostMutationGuardPlatformAdminShortCircuit(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	if err := seedAuthorization(context.Background(), sm); err != nil { // seeds platform:main#admin@user:demo-owner
		t.Fatalf("seedAuthorization: %v", err)
	}

	// demo-owner is a platform admin but not an owner of project p2; the short-circuit
	// lets it grant anyway.
	rcpt, err := svc.GrantRelationship(context.Background(), actor(seedOwnerSubject.ID), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p2", Relation: "owner",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-p2-owner"},
	})
	if err != nil || rcpt.Outcome != authorization.OutcomeApplied {
		t.Fatalf("platform-admin short-circuit grant: outcome=%v err=%v", rcpt.Outcome, err)
	}

	// Tear down the platform-admin scope (trusted), then the SAME actor is denied — the
	// guard read the now-absent admin tuple through the view.
	if _, err := sm.TeardownAuthorizationScope(context.Background(), authorization.TeardownAuthorizationScopeCommand{
		MutationID: mustMutationID(t), ResourceType: platformResourceType, ResourceID: platformResourceID,
		Reason: "az3-4.1 host test: revoke platform admin",
	}); err != nil {
		t.Fatalf("teardown platform admin: %v", err)
	}
	_, err = svc.GrantRelationship(context.Background(), actor(seedOwnerSubject.ID), authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "p3", Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-x"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("after platform-admin teardown: want forbidden, got %v", err)
	}
}

// TestHostMutationGuardGlobalMutationTrustedOnly proves the guard refuses a global
// (subject-scoped) actor mutation from a non-admin: global role mutation has a blast
// radius that belongs to a trusted holder, so only a platform admin (short-circuit) or the
// SystemMutator may drive it.
func TestHostMutationGuardGlobalMutationTrustedOnly(t *testing.T) {
	comps := hostAuthz(t)
	_, err := comps.Service.AssignRole(context.Background(), actor("u-nobody"), authorization.AssignRoleCommand{
		MutationID: mustMutationID(t),
		Subject:    authorization.PrincipalRef{Type: "user", ID: "u-target"},
		Role:       "auditor",
		// empty scope pair = GLOBAL
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-admin global role assign: want forbidden, got %v", err)
	}
}

// TestInvitationAcceptanceTrustedAndIdempotent proves the host invitation seam
// (relationshipGranter → the deliberately held SystemMutator, stable derived MutationID)
// is trusted and idempotent: accepting the same invitation twice grants view and does NOT
// duplicate the stored mutation or bump the revision again (the second application is a
// replay). This is the acceptance's "invitation acceptance is visibly trusted and
// idempotent".
func TestInvitationAcceptanceTrustedAndIdempotent(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	if err := seedAuthorization(context.Background(), sm); err != nil { // establish the owner minimum
		t.Fatalf("seedAuthorization: %v", err)
	}
	g := relationshipGranter{system: sm}
	ctx := context.Background()

	// First acceptance grants the member — TRUSTED (it bypasses the host guard: an invitee
	// is not an owner and could never self-grant through the actor path).
	if err := g.Grant(ctx, demoResourceType, demoResourceID, "member", "user", "invitee"); err != nil {
		t.Fatalf("first invitation grant: %v", err)
	}
	if !allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("invitee does not have view after acceptance")
	}

	// The stable derived MutationID makes a retried acceptance a replay: applying it again
	// through the SystemMutator returns Replayed=true and does not re-apply.
	derived := authorization.DeriveMutationID("auth-cms/invitation-grant",
		demoResourceType, demoResourceID, "member", "user", "invitee")
	rcpt, err := sm.GrantRelationship(ctx, authorization.GrantRelationshipCommand{
		MutationID: derived, ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "invitee"},
	})
	if err != nil {
		t.Fatalf("retried invitation grant: %v", err)
	}
	if !rcpt.Replayed {
		t.Fatal("retried invitation grant was not a replay: a duplicate mutation/revision bump would occur")
	}

	// And the host seam itself stays idempotent (nil on the retry).
	if err := g.Grant(ctx, demoResourceType, demoResourceID, "member", "user", "invitee"); err != nil {
		t.Fatalf("idempotent retried grant through the granter: %v", err)
	}
}

// TestHostSystemMutatorHeldApartFromService proves ordinary host code cannot recover the
// trusted SystemMutator from the actor-facing Service: no Service method returns a
// *SystemMutator and no exported field holds one. HTTP handlers receive only the Service.
func TestHostSystemMutatorHeldApartFromService(t *testing.T) {
	smType := reflect.TypeOf(&authorization.SystemMutator{})
	svcType := reflect.TypeOf(&authorization.Service{})
	for i := 0; i < svcType.NumMethod(); i++ {
		m := svcType.Method(i)
		for o := 0; o < m.Type.NumOut(); o++ {
			if m.Type.Out(o) == smType {
				t.Fatalf("Service.%s returns a *SystemMutator: ordinary code could recover the trusted capability", m.Name)
			}
		}
	}
	// The struct's own fields are unexported, so a host cannot reach one by field access
	// either (reflect sees them but cannot set/read across packages); the method scan above
	// is the reachable-surface proof.
}

// TestHostAuthorizationHasNoRawWriteOrSystemActor proves the guarantee at the HOST scope:
// the Service type this host hands to handlers exposes no raw create/delete write method,
// and the Actor a host constructs carries no system/privilege synonym — trusted writes are
// reachable ONLY through the separately held SystemMutator.
func TestHostAuthorizationHasNoRawWriteOrSystemActor(t *testing.T) {
	svcType := reflect.TypeOf(&authorization.Service{})
	for _, name := range []string{
		"CreateRelationships",
		"DeleteRelationship",
		"DeleteResourceRelationships",
		"DeleteByResourceAndSubject",
	} {
		if _, ok := svcType.MethodByName(name); ok {
			t.Fatalf("Service.%s is a raw unguarded write path and must not exist on the host-facing surface", name)
		}
	}
	// Every actor-facing mutation method takes an Actor (never a privilege flag).
	actorType := reflect.TypeOf(authorization.Actor{})
	for _, name := range []string{"GrantRelationship", "RevokeRelationship", "ReplaceRelationship", "AssignRole", "UnassignRole"} {
		m, ok := svcType.MethodByName(name)
		if !ok {
			t.Fatalf("Service.%s (guarded) must exist", name)
		}
		if m.Type.NumIn() < 3 || m.Type.In(2) != actorType {
			t.Fatalf("Service.%s must take an Actor as its first argument, got %s", name, m.Type)
		}
	}
	// Actor carries no constructible system/privilege synonym.
	for i := 0; i < actorType.NumField(); i++ {
		n := strings.ToLower(actorType.Field(i).Name)
		if strings.Contains(n, "system") || strings.Contains(n, "kind") || strings.Contains(n, "trust") {
			t.Fatalf("Actor exposes a privilege field %q; trusted writes must go through SystemMutator", actorType.Field(i).Name)
		}
	}
}

// TestAuthorizationPosturesDemonstrable proves the two live postures this host composes
// stay demonstrable over the guarded engine: the host-authored closure (isPlatformAdmin, a
// host Check recipe that fails closed) and the flagship engine (authorizer.Check). The
// third posture — no authorization — is the ungated public cms routes and the retained
// middle-posture git artifact (README), not an engine call.
func TestAuthorizationPosturesDemonstrable(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}

	// Host-authored closure: the seeded admin passes, a stranger fails closed.
	if !isPlatformAdmin(ctx, svc, "user", seedOwnerSubject.ID) {
		t.Fatal("host-authored closure: seeded platform admin not recognized")
	}
	if isPlatformAdmin(ctx, svc, "user", "u-stranger") {
		t.Fatal("host-authored closure: stranger recognized as platform admin (must fail closed)")
	}

	// Flagship engine: a granted member has view; an ungranted subject does not.
	seedTrustedOwner(t, sm, "pflag", "u-flag-owner")
	if _, err := sm.GrantRelationship(ctx, authorization.GrantRelationshipCommand{
		MutationID: mustMutationID(t), ResourceType: demoResourceType, ResourceID: "pflag", Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "u-flag-member"},
	}); err != nil {
		t.Fatalf("seed flagship member: %v", err)
	}
	if !allowed(t, svc, "u-flag-member", demoPermission, "pflag") {
		t.Fatal("flagship engine: granted member lacks view")
	}
	if allowed(t, svc, "u-outsider", demoPermission, "pflag") {
		t.Fatal("flagship engine: outsider has view")
	}
}
