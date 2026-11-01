package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	roles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// erasureHost is this host's wiring of the erase route: the access policy's
// platform-admin UserAdminCheck, the UserErasureRoute opt-in and the
// BeforeErase cleanup run() sets.
type erasureHost struct {
	*machineHost
	authz authorization.Components
}

func newErasureHost(t *testing.T) *erasureHost {
	t.Helper()
	components, err := newAuthorization(nil, nil, environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	policy := access.New(components.Decisions)
	return &erasureHost{machineHost: &machineHost{
		linkHost: newLinkHostTuned(t, func(cfg *authenticationConfig) {
			cfg.UserAdminCheck = policy.UserAdmin
			cfg.UserErasureRoute = true
			cfg.BeforeErase = beforeErase(components.Roles, components.Mutations, authzIntegrityPolicy())
		}),
		system: components.Mutations,
	}, authz: components}
}

// assignments lists every role assignment and relationship of user:id.
func (h *erasureHost) assignments(id string) []roles.Assignment {
	h.t.Helper()
	page, err := h.authz.Roles.ListRoleAssignmentsBySubject(context.Background(), model.PrincipalRef{Type: "user", ID: id}, list.Request{Limit: 100})
	if err != nil {
		h.t.Fatalf("ListRoleAssignmentsBySubject: %v", err)
	}
	return page.Items
}

func (h *erasureHost) erase(admin *linkClient, id string) (int, map[string]any) {
	h.t.Helper()
	resp, payload := admin.mutate("/auth/admin/users/"+id+"/erase", `{}`)
	var body map[string]any
	_ = json.Unmarshal(payload, &body)
	return resp.StatusCode, body
}

func (h *erasureHost) status(admin *linkClient, id string) string {
	h.t.Helper()
	resp, payload := admin.do("GET", "/auth/admin/users/"+id, "", nil)
	var summary struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(payload, &summary) != nil {
		h.t.Fatalf("GET user = %d %s", resp.StatusCode, payload)
	}
	return summary.Status
}

func (h *erasureHost) rail(filter securityevent.ListFilter) []securityevent.SecurityEvent {
	h.t.Helper()
	page, err := h.svc.SecurityEvents.Repository().List(context.Background(), filter, list.Request{Limit: 100})
	if err != nil {
		h.t.Fatalf("List rail: %v", err)
	}
	return page.Items
}

type erasureBody struct {
	User struct {
		Status       string `json:"status"`
		DisplayName  string `json:"display_name"`
		PrimaryEmail string `json:"primary_email"`
	} `json:"user"`
	Changed bool           `json:"changed"`
	Counts  map[string]int `json:"counts"`
}

// TestUserErasureOverRealHTTP is the erase leg over real HTTP: a platform admin
// erases a signed-in user; the directory reports erased with no name or
// address; the user can neither log in nor use its live session; the rail
// holds one user_erased row with the counts while the user's earlier rows are
// untouched; a repeat is a no-op; the erased user cannot be reactivated; and
// an administrator cannot erase themself.
func TestUserErasureOverRealHTTP(t *testing.T) {
	t.Parallel()
	h := newErasureHost(t)
	admin := h.signUpPlatformAdmin("erase-admin@example.com")
	admin.stepUpToAAL2()
	adminID := h.currentUserID(admin)
	target := h.signUp("erase-target@example.com")
	targetID := h.currentUserID(target)
	if logins := h.rail(securityevent.ListFilter{UserID: targetID, EventType: securityevent.TypeLogin}); len(logins) == 0 {
		t.Fatal("the target's login left no row to check afterwards")
	}
	before := h.rail(securityevent.ListFilter{UserID: targetID})

	resp, payload := admin.mutate("/auth/admin/users/"+targetID+"/erase", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("erase = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	var erased erasureBody
	if err := json.Unmarshal(payload, &erased); err != nil {
		t.Fatal(err)
	}
	if !erased.Changed || erased.User.Status != "erased" || erased.User.DisplayName != "" || erased.User.PrimaryEmail != "" ||
		erased.Counts["sessions"] < 1 || erased.Counts["identifiers_blanked"] < 1 || erased.Counts["passwords"] != 1 {
		t.Fatalf("erase body = %s", payload)
	}

	resp, payload = admin.do("GET", "/auth/admin/users/"+targetID, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET user = %d; body=%s", resp.StatusCode, payload)
	}
	var summary struct {
		Status       string `json:"status"`
		DisplayName  string `json:"display_name"`
		PrimaryEmail string `json:"primary_email"`
	}
	if err := json.Unmarshal(payload, &summary); err != nil || summary.Status != "erased" || summary.DisplayName != "" || summary.PrimaryEmail != "" {
		t.Errorf("directory read = %s (%v)", payload, err)
	}

	if resp, _ := target.do("GET", "/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the erased user's live session: GET /auth/me = %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.newClient().do("POST", "/auth/login", `{"email":"erase-target@example.com","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login as the erased user = %d, want 401", resp.StatusCode)
	}

	rows := h.rail(securityevent.ListFilter{EventType: securityevent.TypeUserErased})
	if len(rows) != 1 || rows[0].UserID != targetID || rows[0].Actor.ID != adminID || rows[0].IPAddress == "" {
		t.Fatalf("user_erased rows = %+v", rows)
	}
	for _, key := range []string{"sessions", "identifiers_blanked", "passwords"} {
		if _, ok := rows[0].Details[key]; !ok {
			t.Errorf("user_erased Details lack %q: %v", key, rows[0].Details)
		}
	}
	after := map[string]securityevent.SecurityEvent{}
	for _, evt := range h.rail(securityevent.ListFilter{UserID: targetID}) {
		after[evt.ID] = evt
	}
	for _, evt := range before {
		got, ok := after[evt.ID]
		if !ok || got.IPAddress != evt.IPAddress || got.UserAgent != evt.UserAgent || got.EventType != evt.EventType {
			t.Errorf("rail row %s (%s) changed by the erase: %+v", evt.ID, evt.EventType, got)
		}
	}

	resp, payload = admin.mutate("/auth/admin/users/"+targetID+"/erase", `{}`)
	var repeat erasureBody
	if resp.StatusCode != http.StatusOK || json.Unmarshal(payload, &repeat) != nil || repeat.Changed || repeat.User.Status != "erased" {
		t.Errorf("repeat erase = %d %s, want 200 changed:false", resp.StatusCode, payload)
	}
	if n := len(h.rail(securityevent.ListFilter{EventType: securityevent.TypeUserErased})); n != 1 {
		t.Errorf("a repeat erase wrote a row: %d user_erased rows", n)
	}
	if resp, payload := admin.mutate("/auth/admin/users/"+targetID+"/reactivate", `{}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("reactivate an erased user = %d %s, want 409", resp.StatusCode, payload)
	}
	resp, payload = admin.mutate("/auth/admin/users/"+targetID+"/deactivate", `{}`)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(payload, &repeat) != nil || repeat.Changed || repeat.User.Status != "erased" {
		t.Errorf("deactivate an erased user = %d %s, want 200 changed:false", resp.StatusCode, payload)
	}
	if resp, payload := admin.mutate("/auth/admin/users/"+adminID+"/erase", `{}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("self-erase = %d %s, want 400", resp.StatusCode, payload)
	}
}

// TestUserErasureRefusedForANonAdmin proves the action reaches this host's
// policy: a signed-in user without platform admin is 403 and erases nothing.
func TestUserErasureRefusedForANonAdmin(t *testing.T) {
	t.Parallel()
	h := newErasureHost(t)
	mallory := h.signUp("erase-mallory@example.com")
	victim := h.signUp("erase-victim@example.com")
	victimID := h.currentUserID(victim)
	if resp, payload := mallory.mutate("/auth/admin/users/"+victimID+"/erase", `{}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin erase = %d %s, want 403", resp.StatusCode, payload)
	}
	if resp, _ := victim.do("GET", "/auth/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the victim lost its session to a refused erase: %d", resp.StatusCode)
	}
}

// TestUserErasureRevokesAccessServerSide: one erase request removes a project
// member who also holds the global auditor role from every scope, each removal
// audited with the administrator as actor and the reason user_erase.
func TestUserErasureRevokesAccessServerSide(t *testing.T) {
	t.Parallel()
	h := newErasureHost(t)
	admin := h.signUpPlatformAdmin("revoke-admin@example.com")
	admin.stepUpToAAL2()
	adminID := h.currentUserID(admin)
	target := h.signUp("revoke-target@example.com")
	targetID := h.currentUserID(target)
	trustGrant(t, h.system, demoResourceType, demoResourceID, "owner", subj("user", adminID))
	trustGrant(t, h.system, demoResourceType, demoResourceID, "member", subj("user", targetID))
	if _, err := h.system.AssignRole(proofSystemContext(), mutations.AssignRoleCommand{
		Subject: model.PrincipalRef{Type: "user", ID: targetID}, Role: demoRole, Scope: tuples.Global(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.assignments(targetID); len(got) != 2 {
		t.Fatalf("fixture: assignments = %+v, want the member and the global auditor", got)
	}

	if code, body := h.erase(admin, targetID); code != http.StatusOK || body["changed"] != true {
		t.Fatalf("erase = %d %v, want 200 changed:true", code, body)
	}
	if got := h.assignments(targetID); len(got) != 0 {
		t.Fatalf("assignments after erase = %+v, want none in any scope", got)
	}
	page, err := h.authz.Audit.List(context.Background(), audit.Filter{SubjectType: "user", SubjectID: targetID}, list.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, rec := range page.Items {
		if rec.Change.Action != audit.ActionRemoved {
			continue
		}
		if rec.Source.ActorType != "user" || rec.Source.ActorID != adminID || rec.Source.Reason != "user_erase" || rec.Source.System != "" {
			t.Errorf("removal %s source = %+v, want the admin with reason user_erase", rec.ID, rec.Source)
		}
		removed[rec.Change.Tuple.Relation] = true
	}
	if !removed["member"] || !removed[demoRole] || len(removed) != 2 {
		t.Fatalf("audited removals = %v, want member and %s", removed, demoRole)
	}
}

// TestUserErasureRefusedWhileSoleProjectOwner: the host's last-owner rule
// refuses the cleanup as 409 with nothing erased and, because the owner tuple
// is attempted first, nothing revoked; a second owner lets the re-run finish,
// and a third erase is a no-op.
func TestUserErasureRefusedWhileSoleProjectOwner(t *testing.T) {
	t.Parallel()
	h := newErasureHost(t)
	admin := h.signUpPlatformAdmin("owner-admin@example.com")
	admin.stepUpToAAL2()
	adminID := h.currentUserID(admin)
	target := h.signUp("owner-target@example.com")
	targetID := h.currentUserID(target)
	trustGrant(t, h.system, demoResourceType, "solo", "owner", subj("user", targetID))
	trustGrant(t, h.system, demoResourceType, "solo", "member", subj("user", targetID))

	code, body := h.erase(admin, targetID)
	if code != http.StatusConflict || body["code"] != "conflict" {
		t.Fatalf("erase of the sole owner = %d %v, want 409 conflict", code, body)
	}
	if got := h.status(admin, targetID); got != "active" {
		t.Errorf("status after the refusal = %q, want active", got)
	}
	if got := h.assignments(targetID); len(got) != 2 {
		t.Fatalf("assignments after the refusal = %+v, want the owner and the member intact", got)
	}

	trustGrant(t, h.system, demoResourceType, "solo", "owner", subj("user", adminID))
	if code, body := h.erase(admin, targetID); code != http.StatusOK || body["changed"] != true {
		t.Fatalf("re-run = %d %v, want 200 changed:true", code, body)
	}
	if got := h.assignments(targetID); len(got) != 0 {
		t.Fatalf("assignments after the re-run = %+v, want none", got)
	}
	if code, body := h.erase(admin, targetID); code != http.StatusOK || body["changed"] != false {
		t.Fatalf("third erase = %d %v, want 200 changed:false", code, body)
	}
}
