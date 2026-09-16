package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	authzmem "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"

	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// hostAuthz builds the guarded components the way run() does. It fatals on error so the
// construction matrix (a guard requires a mutation repository, etc.) is exercised.
func hostAuthz(t *testing.T) authorization.Components {
	t.Helper()
	comps, err := newAuthorization(nil, nil)
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	return comps
}

func seedTrustedOwner(t *testing.T, sm *mutations.Service, resourceID, userID string) {
	t.Helper()
	if _, err := sm.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{

		ResourceType: demoResourceType,
		ResourceID:   resourceID,
		Relation:     "owner",
		Subject:      relationships.SubjectRef{Type: "user", ID: userID},
	}); err != nil {
		t.Fatalf("seed owner %s on %s: %v", userID, resourceID, err)
	}
}

func actor(userID string) sdk.Principal { return sdk.Principal{Type: "user", ID: userID} }

func allowed(t *testing.T, svc authorization.Components, userID, permission, resourceID string) bool {
	t.Helper()
	res, err := svc.Decisions.Check(context.Background(), model.CheckRequest{
		Principal:  model.PrincipalRef{Type: "user", ID: userID},
		Permission: permission,
		Resource:   model.Resource{Type: demoResourceType, ID: resourceID},
	})
	if err != nil {
		t.Fatalf("Check %s %s/%s: %v", userID, permission, resourceID, err)
	}
	return res.Allowed
}

func TestAuthorizationCompositionInboundPolicy(t *testing.T) {
	comps := hostAuthz(t)
	if comps.Decisions == nil || comps.Mutations == nil {
		t.Fatal("guarded composition must return both Service and TupleWriter")
	}
	_, err := admitGrantRelationship(context.Background(), comps, actor("nobody"), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "x"},
	})
	if errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatal("actor mutation hit the read-only posture: Config.Guard was not wired")
	}
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("unauthorized actor grant: want forbidden, got %v", err)
	}
}

func TestHostAdmissionManageAccessAllowsAndDenies(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	seedTrustedOwner(t, sm, "p1", "u-owner")

	// The owner may grant a member.
	rcpt, err := admitGrantRelationship(context.Background(), svc, actor("u-owner"), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-member"},
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("owner grant: outcome=%v err=%v", rcpt.Outcome, err)
	}
	if !allowed(t, svc, "u-member", demoPermission, "p1") {
		t.Fatal("granted member does not have view")
	}

	// A non-owner may not, and nothing is written.
	_, err = admitGrantRelationship(context.Background(), svc, actor("u-stranger"), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p1", Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-intruder"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-owner grant: want forbidden, got %v", err)
	}
	if allowed(t, svc, "u-intruder", demoPermission, "p1") {
		t.Fatal("denied grant committed a member row")
	}
}

// TestInboundRejectsSelfGrant proves an untrusted actor cannot self-escalate: a
// non-owner granting ITSELF owner (or a lesser relation) is denied by the guard before
// Apply, and no row is written. This is the acceptance's "untrusted service calls cannot
// self-grant".
func TestInboundRejectsSelfGrant(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	seedTrustedOwner(t, sm, "p1", "u-owner")

	_, err := admitGrantRelationship(context.Background(), svc, actor("u-evil"), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p1", Relation: "owner",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-evil"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-escalation to owner: want forbidden, got %v", err)
	}
	if allowed(t, svc, "u-evil", manageAccessPerm, "p1") {
		t.Fatal("self-escalation wrote an owner row (manage_access now satisfied)")
	}
}

func TestHostAdmissionPlatformAdminShortCircuit(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	if err := seedAuthorization(context.Background(), sm); err != nil { // seeds platform:main#admin@user:demo-owner
		t.Fatalf("seedAuthorization: %v", err)
	}

	// demo-owner is a platform admin but not an owner of project p2; the short-circuit
	// lets it grant anyway.
	rcpt, err := admitGrantRelationship(context.Background(), svc, actor(seedOwnerSubject.ID), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p2", Relation: "owner",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-p2-owner"},
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("platform-admin short-circuit grant: outcome=%v err=%v", rcpt.Outcome, err)
	}

	// Tear down the platform-admin scope (trusted), then the SAME actor is denied — the
	// guard read the now-absent admin tuple through the view.
	if _, err := sm.TeardownResourceAuthorization(context.Background(), mutations.TeardownResourceAuthorizationCommand{
		ResourceType: platformResourceType, ResourceID: platformResourceID,
		Reason: "az3-4.1 host test: revoke platform admin",
	}); err != nil {
		t.Fatalf("teardown platform admin: %v", err)
	}
	_, err = admitGrantRelationship(context.Background(), svc, actor(seedOwnerSubject.ID), mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "p3", Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-x"},
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("after platform-admin teardown: want forbidden, got %v", err)
	}
}

func TestHostAdmissionGlobalMutationTrustedOnly(t *testing.T) {
	comps := hostAuthz(t)
	_, err := admitAssignRole(context.Background(), comps, actor("u-nobody"), mutations.AssignRoleCommand{

		Subject: model.PrincipalRef{Type: "user", ID: "u-target"},
		Role:    "auditor",
		Scope:   tuples.Global(),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-admin global role assign: want forbidden, got %v", err)
	}
}

func hostGranter(sm *mutations.Service, existing ...string) (integrityRelationshipGranter, *hostResourceRegistry) {
	reg := newHostResourceRegistry(existing...)
	return integrityRelationshipGranter{system: sm, exists: reg.Exists}, reg
}

// demoGrant is a GrantInput on the demo resource, distinguished by its OperationID — the
// logical invitation identity authentication supplies (the invitation row id, or a fresh
// value for direct-add).
func demoGrant(operationID, relation, subjectID string) invitations.GrantInput {
	return invitations.GrantInput{
		OperationID:  operationID,
		ResourceType: demoResourceType,
		ResourceID:   demoResourceID,
		Relation:     relation,
		SubjectType:  "user",
		SubjectID:    subjectID,
	}
}

func TestBaselineInvitationGranterNeedsNoMutationLifecycle(t *testing.T) {
	store := authzmem.New()
	comps, err := authorization.New(authorization.Repositories{Tuples: store.Tuples()}, authorization.WithModel(authzSchema()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reg := newHostResourceRegistry(resourceKey(demoResourceType, demoResourceID))
	g := relationshipGranter{writer: comps.RelationshipWriter, reader: comps.Relationships, exists: reg.Exists}
	ctx := context.Background()
	in := demoGrant("optional-operation-id", "member", "invitee")
	if err := g.Grant(ctx, in); err != nil {
		t.Fatalf("baseline invitation grant: %v", err)
	}
	if !allowed(t, comps, "invitee", demoPermission, demoResourceID) {
		t.Fatal("baseline invitation did not grant membership")
	}
	if err := comps.RelationshipWriter.DeleteRelationship(ctx,
		model.Resource{Type: demoResourceType, ID: demoResourceID}, "member",
		relationships.SubjectRef{Type: "user", ID: "invitee"}); err != nil {
		t.Fatalf("delete grant: %v", err)
	}
	if err := g.Grant(ctx, in); err != nil {
		t.Fatalf("same operation id must not suppress state restoration: %v", err)
	}
	if !allowed(t, comps, "invitee", demoPermission, demoResourceID) {
		t.Fatal("baseline re-grant did not restore membership")
	}
	if _, err := comps.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{}); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("advanced mutation repository unexpectedly required/wired: %v", err)
	}
}

func TestInvitationAcceptanceTrustedAndIdempotent(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
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

	rcpt, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "invitee"},
	})
	if err != nil {
		t.Fatalf("retried invitation grant: %v", err)
	}
	if rcpt.Outcome != mutations.OutcomeNoChange {
		t.Fatal("repeated invitation grant should leave an existing tuple unchanged")
	}

	// And the host seam itself stays idempotent (nil on the retry of the same invitation).
	if err := g.Grant(ctx, demoGrant("inv-A", "member", "invitee")); err != nil {
		t.Fatalf("idempotent retried grant through the granter: %v", err)
	}
}

func TestInvitationReinviteAfterRevokeRestoresTuple(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
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
	if _, err := admitRevokeRelationship(ctx, svc, actor(seedOwnerSubject.ID), mutations.RevokeRelationshipCommand{

		ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "invitee"},
	}); err != nil {
		t.Fatalf("revoke tuple T: %v", err)
	}
	if allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("view survived the revoke: tuple T was not removed")
	}

	if err := g.Grant(ctx, demoGrant("inv-B", "member", "invitee")); err != nil {
		t.Fatalf("invitation B grant: %v", err)
	}
	if !allowed(t, svc, "invitee", demoPermission, demoResourceID) {
		t.Fatal("re-invitation after revoke did NOT restore the tuple (the core bug)")
	}
}

// Accepting member preserves an existing owner on the same subject/resource.
func TestInvitationMemberAndOwnerCoexist(t *testing.T) {
	comps := hostAuthz(t)
	ctx := context.Background()
	if err := seedAuthorization(ctx, comps.Mutations); err != nil {
		t.Fatal(err)
	}
	reg := newHostResourceRegistry(resourceKey(demoResourceType, demoResourceID))
	raw := relationshipGranter{writer: comps.RelationshipWriter, reader: comps.Relationships, exists: reg.Exists}
	guarded, _ := hostGranter(comps.Mutations, resourceKey(demoResourceType, demoResourceID))
	for name, g := range map[string]invitations.Granter{"raw": raw, "guarded": guarded} {
		t.Run(name, func(t *testing.T) {
			id := "owner-" + name
			if err := g.Grant(ctx, demoGrant("owner", "owner", id)); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := g.Grant(ctx, demoGrant("member", "member", id)); err != nil {
					t.Fatal(err)
				}
			}
			for _, role := range []string{"owner", "member"} {
				ok, err := comps.Roles.HasRoleIn(ctx, model.PrincipalRef{Type: "user", ID: id}, role, model.Resource{Type: demoResourceType, ID: demoResourceID})
				if err != nil || !ok {
					t.Fatalf("missing %s: %v", role, err)
				}
			}
			if !allowed(t, comps, id, manageAccessPerm, demoResourceID) {
				t.Fatal("invitation displaced owner")
			}
		})
	}
}

// TestInvitationGrantDeletedResourceNotFound proves adversarial case 4: a grant against a
// since-deleted host resource fails wrapping sdk.ErrNotFound and writes no tuple. Only the
// host knows the resource is gone, so the check is the Granter's duty (design D2).
func TestInvitationGrantDeletedResourceNotFound(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	ctx := context.Background()

	// A separate project the host once owned, then destroyed.
	const gone = "pdel"
	g, reg := hostGranter(sm, resourceKey(demoResourceType, gone))
	reg.remove(demoResourceType, gone)

	err := g.Grant(ctx, invitations.GrantInput{
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
// hostInviteCheck (authenticationConfig.InviteCheck) lets a member-capable manager invite a member but
// NOT an owner (the editor→owner escalation guard), reserves owner-granting to platform
// admins, and requires manage_access for both non-owner create and list. Denials wrap
// sdk.ErrForbidden (the pocket maps that to 403 before the invitation service is reached —
// proven in the authentication pocket's handler tests).
func TestHostInviteCheckPermissionMapping(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil { // seeds platform:main#admin@demo-owner
		t.Fatalf("seedAuthorization: %v", err)
	}
	seedTrustedOwner(t, sm, "pinv", "u-manager") // u-manager holds manage_access on project:pinv
	check := hostInviteCheck(svc.Decisions)

	create := func(subjectID, relation, resourceID string) error {
		return check(ctx, authenticationhttp.InviteCheckRequest{
			Principal:    sdk.Principal{Type: "user", ID: subjectID},
			Action:       authenticationhttp.InviteCreate,
			ResourceType: demoResourceType, ResourceID: resourceID, Relation: relation,
		})
	}
	list := func(subjectID, resourceID string) error {
		return check(ctx, authenticationhttp.InviteCheckRequest{
			Principal:    sdk.Principal{Type: "user", ID: subjectID},
			Action:       authenticationhttp.InviteList,
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

// TestAuthorizationPosturesDemonstrable proves the two live postures this host composes
// stay demonstrable over the guarded engine: the host-authored closure (isPlatformAdmin, a
// host Check recipe that fails closed) and the flagship engine (authorizer.Check). The
// third posture — no authorization — is the ungated public cms routes and the retained
// middle-posture git artifact (README), not an engine call.
func TestAuthorizationPosturesDemonstrable(t *testing.T) {
	comps := hostAuthz(t)
	svc, sm := comps, comps.Mutations
	ctx := context.Background()
	if err := seedAuthorization(ctx, sm); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}

	// Host-authored closure: the seeded admin passes, a stranger fails closed.
	if !isPlatformAdmin(ctx, svc.Decisions, "user", seedOwnerSubject.ID) {
		t.Fatal("host-authored closure: seeded platform admin not recognized")
	}
	if isPlatformAdmin(ctx, svc.Decisions, "user", "u-stranger") {
		t.Fatal("host-authored closure: stranger recognized as platform admin (must fail closed)")
	}

	// Flagship engine: a granted member has view; an ungranted subject does not.
	seedTrustedOwner(t, sm, "pflag", "u-flag-owner")
	if _, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: "pflag", Relation: "member",
		Subject: relationships.SubjectRef{Type: "user", ID: "u-flag-member"},
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

// =============================================================================
// The composed both-kinds proof (roles-model T5b): ONE resource type, two models,
// one decision surface.
// =============================================================================

func TestHostUnifiedModelDeclaresGraphAndExactPermissions(t *testing.T) {
	c, err := newAuthorization(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{demoPermission, manageAccessPerm, demoAuditPermission} {
		if !c.Decisions.DeclaresPermission(demoResourceType, p) {
			t.Fatalf("undeclared %s", p)
		}
	}
}

// demoAuditHost is one RUNNING host composition — the real authentication pocket,
// the real guarded authorization components (both models), and the real
// registerDemoRoutes registration — so /demo/audit is driven over HTTP with real
// credentials, through the real RequirePrincipal + role-model gate chain.
type demoAuditHost struct {
	*linkHost
	comps authorization.Components
}

func newDemoAuditHost(t *testing.T) *demoAuditHost {
	t.Helper()
	sender := &recordingSender{}
	authSvc := bootInProcess(t, sender, nil)
	comps := hostAuthz(t)
	if err := seedAuthorization(context.Background(), comps.Mutations); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}
	router := web.NewWebHandler()
	if err := authSvc.HTTP.Register(pockets.Mount{
		Router: router, Logger: quietLog(), Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog())),
	}); err != nil {
		t.Fatalf("auth.Register: %v", err)
	}
	registerDemoRoutes(router, authSvc.HTTP, comps.Decisions, comps.Roles, comps.HTTP)
	t.Cleanup(runDelivery(t, authSvc))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	origins := hostAllowedOrigins(t)
	return &demoAuditHost{
		linkHost: &linkHost{t: t, srv: srv, svc: authSvc, sender: sender, origin: origins[0]},
		comps:    comps,
	}
}

// principalID reads the signed-up client's resolved principal id off /demo/whoami.
func (h *demoAuditHost) principalID(c *linkClient) string {
	h.t.Helper()
	resp, body := c.do("GET", "/demo/whoami", "", nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /demo/whoami = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out struct {
		PrincipalID string `json:"principal_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.PrincipalID == "" {
		h.t.Fatalf("decode whoami %q: %v", body, err)
	}
	return out.PrincipalID
}

func (h *demoAuditHost) assignAuditor(userID, resourceType, resourceID string) {
	h.t.Helper()
	if _, err := h.comps.Mutations.AssignRole(context.Background(), mutations.AssignRoleCommand{

		Subject: model.PrincipalRef{Type: "user", ID: userID},
		Role:    demoRole,
		Scope:   testRoleScope(resourceType, resourceID),
	}); err != nil {
		h.t.Fatalf("AssignRole(%s, %s, %s/%s): %v", userID, demoRole, resourceType, resourceID, err)
	}
}

// status drives one authenticated GET and returns its status code and body.
func (h *demoAuditHost) get(c *linkClient, path string) (int, []byte) {
	h.t.Helper()
	resp, body := c.do("GET", path, "", nil)
	return resp.StatusCode, body
}

// The named audit permission checks scoped membership. A global auditor alone
// is denied, while the platform-admin recipe remains independently composed.
func TestDemoAuditRouteUsesScopedPredicate(t *testing.T) {
	h := newDemoAuditHost(t)

	// No credential at all: the route's RequirePrincipal answers before any decision.
	anon := h.newClient()
	if code, body := h.get(anon, "/demo/audit"); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /demo/audit = %d, want 401; body=%s", code, body)
	}

	// (1) The role-owned pair: an auditor with no relationship tuple is allowed.
	auditor := h.signUp("role-model-auditor@example.com")
	auditorID := h.principalID(auditor)
	if code, body := h.get(auditor, "/demo/audit"); code != http.StatusForbidden {
		t.Fatalf("GET /demo/audit before the role = %d, want 403; body=%s", code, body)
	}
	h.assignAuditor(auditorID, demoResourceType, demoResourceID)
	code, body := h.get(auditor, "/demo/audit")
	if code != http.StatusOK {
		t.Fatalf("GET /demo/audit as a scoped auditor = %d, want 200; body=%s", code, body)
	}
	if !strings.Contains(string(body), auditorID) {
		t.Fatalf("scoped auditor %s missing from the direct-scope read-back: %s", auditorID, body)
	}
	// The role grants ONLY its modeled pair: project/view is the other model's.
	if code, body := h.get(auditor, "/demo/members-only"); code != http.StatusForbidden {
		t.Fatalf("GET /demo/members-only as an auditor with no tuple = %d, want 403; body=%s", code, body)
	}

	// (2) The relationship-owned pair: a member without the role is refused by the
	// role model, and still passes the relationship-owned gate.
	member := h.signUp("role-model-member@example.com")
	memberID := h.principalID(member)
	if _, err := h.comps.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{

		ResourceType: demoResourceType,
		ResourceID:   demoResourceID,
		Relation:     demoRelation,
		Subject:      relationships.SubjectRef{Type: "user", ID: memberID},
	}); err != nil {
		t.Fatalf("grant %s member on %s/%s: %v", memberID, demoResourceType, demoResourceID, err)
	}
	if code, body := h.get(member, "/demo/members-only"); code != http.StatusOK {
		t.Fatalf("GET /demo/members-only as a member = %d, want 200; body=%s", code, body)
	}
	if code, body := h.get(member, "/demo/audit"); code != http.StatusForbidden {
		t.Fatalf("GET /demo/audit as a project/view holder without the role = %d, want 403; body=%s", code, body)
	}

	// (3) A global auditor does not satisfy scoped audit policy or platform admin.
	global := h.signUp("role-model-global@example.com")
	globalID := h.principalID(global)
	h.assignAuditor(globalID, "", "")
	code, body = h.get(global, "/demo/audit")
	if code != http.StatusForbidden {
		t.Fatalf("GET /demo/audit as a global auditor = %d, want 403; body=%s", code, body)
	}
	if strings.Contains(string(body), globalID) {
		t.Fatalf("global auditor %s must not appear in the DIRECT-scope read-back: %s", globalID, body)
	}
	if isPlatformAdmin(context.Background(), h.comps.Decisions, "user", globalID) {
		t.Fatal("a globally assigned role must not make its holder a platform admin")
	}
	if code, body := h.get(global, "/demo/members-only"); code != http.StatusForbidden {
		t.Fatalf("GET /demo/members-only as a global auditor = %d, want 403; body=%s", code, body)
	}

	// The platform-admin recipe is unchanged and still host-composed over the
	// relationship kind's platform:main#admin tuple.
	if !isPlatformAdmin(context.Background(), h.comps.Decisions, "user", seedOwnerSubject.ID) {
		t.Fatal("the seeded platform admin recipe stopped governing the relationship-owned kind")
	}
}

// Historical table-driven fixture coordinates are converted explicitly at the boundary.
func testRoleScope(rt, id string) tuples.Scope {
	if rt == "" && id == "" {
		return tuples.Global()
	}
	return tuples.On(rt, id)
}
