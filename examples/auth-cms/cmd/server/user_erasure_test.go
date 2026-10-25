package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// erasureHost is this host's wiring of the erase route: the access policy's
// platform-admin UserAdminCheck and the UserErasureRoute opt-in run() sets.
type erasureHost struct {
	*machineHost
}

func newErasureHost(t *testing.T) *erasureHost {
	t.Helper()
	components, err := newAuthorization(nil, nil, environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	policy := access.New(components.Decisions)
	return &erasureHost{&machineHost{
		linkHost: newLinkHostTuned(t, func(cfg *authenticationConfig) {
			cfg.UserAdminCheck = policy.UserAdmin
			cfg.UserErasureRoute = true
		}),
		system: components.Mutations,
	}}
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
