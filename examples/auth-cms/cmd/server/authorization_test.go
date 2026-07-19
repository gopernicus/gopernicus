package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	authzmem "github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// AZ3-4.1 host proof suite. Every case runs over the REAL guarded composition
// newAuthorization() builds — the same wiring run() serves: the shared-state memstore
// bundle, the project-scoped guardian minimum, and the host MutationGuard (manage_access
// + platform-admin over the DecisionView). It proves the composed HOST behavior: an
// untrusted actor cannot self-grant, both baseline and guarded invitation adapters
// are idempotent under their chosen semantics, write capabilities remain separately
// placed, and no caller can construct a system actor. There is no
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

// hostGranter builds the reference relationshipGranter over sm plus a resource registry
// seeded with the given resource keys — the exact wiring run() composes (the SystemMutator
// and the host resource-existence seam).
func hostGranter(sm *authorization.SystemMutator, existing ...string) (guardedRelationshipGranter, *hostResourceRegistry) {
	reg := newHostResourceRegistry(existing...)
	return guardedRelationshipGranter{system: sm, exists: reg.Exists}, reg
}

// demoGrant is a GrantInput on the demo resource, distinguished by its OperationID — the
// logical invitation identity authentication supplies (the invitation row id, or a fresh
// value for direct-add).
func demoGrant(operationID, relation, subjectID string) auth.GrantInput {
	return auth.GrantInput{
		OperationID:  operationID,
		ResourceType: demoResourceType,
		ResourceID:   demoResourceID,
		Relation:     relation,
		SubjectType:  "user",
		SubjectID:    subjectID,
	}
}

// TestBaselineInvitationGranterNeedsNoMutationLifecycle proves the bundled
// ordinary-sharing adapter satisfies auth.Granter with only relationship state
// and schema configured. Reusing the same OperationID after deletion restores the
// tuple because the adapter does not consume it as a permanent replay identity.
func TestBaselineInvitationGranterNeedsNoMutationLifecycle(t *testing.T) {
	rels := authzmem.NewRelationships()
	comps, err := authorization.NewService(authorization.Repositories{Relationships: rels}, authorization.Config{Model: authzSchema()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reg := newHostResourceRegistry(resourceKey(demoResourceType, demoResourceID))
	g := relationshipGranter{writer: comps.RelationshipWriter, reader: comps.Service, exists: reg.Exists}
	ctx := context.Background()
	in := demoGrant("optional-operation-id", "member", "invitee")
	if err := g.Grant(ctx, in); err != nil {
		t.Fatalf("baseline invitation grant: %v", err)
	}
	if !allowed(t, comps.Service, "invitee", demoPermission, demoResourceID) {
		t.Fatal("baseline invitation did not grant membership")
	}
	if err := comps.RelationshipWriter.DeleteRelationship(ctx,
		authorization.Resource{Type: demoResourceType, ID: demoResourceID}, "member",
		authorization.SubjectRef{Type: "user", ID: "invitee"}); err != nil {
		t.Fatalf("delete grant: %v", err)
	}
	if err := g.Grant(ctx, in); err != nil {
		t.Fatalf("same operation id must not suppress state restoration: %v", err)
	}
	if !allowed(t, comps.Service, "invitee", demoPermission, demoResourceID) {
		t.Fatal("baseline re-grant did not restore membership")
	}
	if _, err := comps.SystemMutator.GrantRelationship(ctx, authorization.GrantRelationshipCommand{}); !errors.Is(err, authorization.ErrMutationsNotConfigured) {
		t.Fatalf("advanced mutation repository unexpectedly required/wired: %v", err)
	}
}

// TestInvitationAcceptanceTrustedAndIdempotent proves the host invitation seam
// (relationshipGranter → the deliberately held SystemMutator, operation-scoped MutationID)
// is trusted and idempotent: an EXACT retry of one invitation (same OperationID) grants view
// and REPLAYS — it does NOT duplicate the stored mutation or bump the revision again. This is
// adversarial case 1 and the acceptance's "invitation acceptance is visibly trusted and
// idempotent".
func TestInvitationAcceptanceTrustedAndIdempotent(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil { // establish the owner minimum
		t.Fatalf("seedAuthorization: %v", err)
	}
	g, _ := hostGranter(sm, resourceKey(demoResourceType, demoResourceID))

	// First acceptance grants the member — TRUSTED (it bypasses the host guard: an invitee
	// is not an owner and could never self-grant through the actor path).
	if err := g.Grant(ctx, demoGrant("inv-A", "member", "invitee")); err != nil {
		t.Fatalf("first invitation grant: %v", err)
	}
	if !allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("invitee does not have view after acceptance")
	}

	// The operation-scoped MutationID makes a retry of the SAME invitation a replay: applying
	// its derived id again through the SystemMutator returns Replayed=true and does not re-apply.
	derived := authorization.DeriveMutationID("auth-cms/invitation-grant",
		"inv-A", demoResourceType, demoResourceID, "member", "user", "invitee")
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

	// And the host seam itself stays idempotent (nil on the retry of the same invitation).
	if err := g.Grant(ctx, demoGrant("inv-A", "member", "invitee")); err != nil {
		t.Fatalf("idempotent retried grant through the granter: %v", err)
	}
}

// TestInvitationReinviteAfterRevokeRestoresTuple is the CORE bug fix (adversarial case 2):
// deriving the MutationID from the tuple alone made a re-invitation after a revoke a silent
// replay that never restored the tuple. With operation-scoped identity, a DISTINCT invitation
// (distinct OperationID → distinct MutationID) is a FRESH grant that restores the revoked
// tuple end-to-end.
func TestInvitationReinviteAfterRevokeRestoresTuple(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}
	g, _ := hostGranter(sm, resourceKey(demoResourceType, demoResourceID))

	// Invitation A grants tuple T (project:demo#member@user:invitee).
	if err := g.Grant(ctx, demoGrant("inv-A", "member", "invitee")); err != nil {
		t.Fatalf("invitation A grant: %v", err)
	}
	if !allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("invitee lacks view after invitation A")
	}

	// The demo owner (holds manage_access on project:demo) revokes T through the guarded
	// actor path — the tuple is gone.
	if _, err := svc.RevokeRelationship(ctx, actor(seedOwnerSubject.ID), authorization.RevokeRelationshipCommand{
		MutationID:   mustMutationID(t),
		ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "member",
		Subject: authorization.SubjectRef{Type: "user", ID: "invitee"},
	}); err != nil {
		t.Fatalf("revoke tuple T: %v", err)
	}
	if allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("view survived the revoke: tuple T was not removed")
	}

	// Invitation B is a DISTINCT invitation for the same tuple (distinct OperationID). Under
	// the retired tuple-derived id this would replay A's consumed mutation and restore
	// NOTHING; with operation-scoped identity it is a fresh grant that restores T.
	if err := g.Grant(ctx, demoGrant("inv-B", "member", "invitee")); err != nil {
		t.Fatalf("invitation B grant: %v", err)
	}
	if !allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("re-invitation after revoke did NOT restore the tuple (the core bug)")
	}
}

// TestInvitationGrantDifferentRelationConflicts proves adversarial case 3: an existing
// DIFFERENT relation for the subject makes a grant a semantic conflict — the host Granter
// returns a loud error wrapping sdk.ErrConflict (it does NOT ReplaceRelationship), so
// authentication never records the invitation as accepted and no relation is upgraded.
func TestInvitationGrantDifferentRelationConflicts(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}
	g, _ := hostGranter(sm, resourceKey(demoResourceType, demoResourceID))

	// The subject already holds member on project:demo.
	if err := g.Grant(ctx, demoGrant("inv-member", "member", "conflictee")); err != nil {
		t.Fatalf("seed member grant: %v", err)
	}

	// A DIFFERENT relation (owner) for the same subject is the one-relation conflict.
	err := g.Grant(ctx, demoGrant("inv-owner", "owner", "conflictee"))
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("conflicting relation grant: want sdk.ErrConflict, got %v", err)
	}
	// State is not advanced: the subject still holds only member (no owner ⇒ no manage_access).
	if allowed(t, svc, "conflictee", manageAccessPerm, demoResourceID) {
		t.Fatal("refused conflict nonetheless wrote an owner row (manage_access satisfied)")
	}
	if !allowed(t, svc, "conflictee", demoPermission, demoResourceID) {
		t.Fatal("the original member tuple was disturbed by the refused conflict")
	}
}

// TestInvitationGrantDeletedResourceNotFound proves adversarial case 4: a grant against a
// since-deleted host resource fails wrapping sdk.ErrNotFound and writes no tuple. Only the
// host knows the resource is gone, so the check is the Granter's duty (design D2).
func TestInvitationGrantDeletedResourceNotFound(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()

	// A separate project the host once owned, then destroyed.
	const gone = "pdel"
	g, reg := hostGranter(sm, resourceKey(demoResourceType, gone))
	reg.remove(demoResourceType, gone)

	err := g.Grant(ctx, auth.GrantInput{
		OperationID: "inv-ghost", ResourceType: demoResourceType, ResourceID: gone,
		Relation: "member", SubjectType: "user", SubjectID: "invitee",
	})
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("grant against deleted resource: want sdk.ErrNotFound, got %v", err)
	}
	if allowed(t, svc, "invitee", demoPermission, gone) {
		t.Fatal("a grant against a deleted resource wrote a tuple")
	}
}

// TestHostInviteCheckPermissionMapping proves adversarial case 5 at the host-policy layer:
// hostInviteCheck (auth.Config.InviteCheck) lets a member-capable manager invite a member but
// NOT an owner (the editor→owner escalation guard), reserves owner-granting to platform
// admins, and requires manage_access for both non-owner create and list. Denials wrap
// sdk.ErrForbidden (the feature maps that to 403 before the invitation service is reached —
// proven in the authentication feature's handler tests).
func TestHostInviteCheckPermissionMapping(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil { // seeds platform:main#admin@demo-owner
		t.Fatalf("seedAuthorization: %v", err)
	}
	seedTrustedOwner(t, sm, "pinv", "u-manager") // u-manager holds manage_access on project:pinv
	check := hostInviteCheck(svc)

	create := func(subjectID, relation, resourceID string) error {
		return check(ctx, auth.InviteCheckRequest{
			Principal:    identity.Principal{Type: "user", ID: subjectID},
			Action:       auth.InviteCreate,
			ResourceType: demoResourceType, ResourceID: resourceID, Relation: relation,
		})
	}
	list := func(subjectID, resourceID string) error {
		return check(ctx, auth.InviteCheckRequest{
			Principal:    identity.Principal{Type: "user", ID: subjectID},
			Action:       auth.InviteList,
			ResourceType: demoResourceType, ResourceID: resourceID,
		})
	}

	// A member-capable manager MAY invite a member...
	if err := create("u-manager", "member", "pinv"); err != nil {
		t.Fatalf("manager invite member: %v", err)
	}
	// ...but MAY NOT invite an owner (escalation guard).
	if err := create("u-manager", "owner", "pinv"); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("manager invite owner: want forbidden, got %v", err)
	}
	// A platform admin MAY invite an owner (even on a resource it does not own).
	if err := create(seedOwnerSubject.ID, "owner", "pnew"); err != nil {
		t.Fatalf("platform admin invite owner: %v", err)
	}
	// A stranger (no manage_access) is denied create and list; the manager may list.
	if err := create("u-stranger", "member", "pinv"); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("stranger create: want forbidden, got %v", err)
	}
	if err := list("u-stranger", "pinv"); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("stranger list: want forbidden, got %v", err)
	}
	if err := list("u-manager", "pinv"); err != nil {
		t.Fatalf("manager list: %v", err)
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
			t.Fatalf("Service.%s must remain on the separately placed RelationshipWriter, not the handler-facing Service", name)
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
