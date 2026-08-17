package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk"
)

// CHAU-1.7 — the account-lifecycle proof, driven through EXPORTED host seams over
// real HTTP. It is a HOST test in a separate module, so nothing here reaches a
// feature-internal package: a reader can copy the wiring and the request sequence
// into an admin console directly.
//
// It walks the definition of done: list the directory, deactivate a user, prove
// every credential path denies them GENERICALLY, prove the replay is idempotent,
// reactivate, and log in again.

const (
	adminEmail  = "console-admin@example.com"
	targetEmail = "console-target@example.com"
)

// adminHost is a running host whose Config.UserAdminCheck authorizes exactly the
// user ids added to allow. That closure IS the seam under test: authentication
// asks the host a question and the host answers with its own policy — no role
// string, no authorization import inside the feature.
type adminHost struct {
	*linkHost
	mu    sync.Mutex
	allow map[string]bool
	// asked records every question the policy was posed, so a test can prove the
	// check ran BEFORE any target resolution or mutation.
	asked []auth.UserAdminCheckRequest
}

func newAdminHost(t *testing.T) *adminHost {
	t.Helper()

	h := &adminHost{allow: map[string]bool{}}

	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *auth.Config) {
		cfg.UserAdminCheck = func(_ context.Context, req auth.UserAdminCheckRequest) error {
			h.mu.Lock()
			h.asked = append(h.asked, req)
			allowed := h.allow[req.Principal.ID]
			h.mu.Unlock()
			if !allowed {
				// A denial wraps sdk.ErrForbidden; the feature maps it to 403 and
				// never proceeds to resolve the target.
				return sdk.ErrForbidden
			}
			return nil
		}
	})
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	origins := allowedOrigins()
	if len(origins) == 0 {
		t.Fatal("host has no allowed origins")
	}
	h.linkHost = &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: origins[0]}
	return h
}

func (h *adminHost) authorize(userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allow[userID] = true
}

func (h *adminHost) questions() []auth.UserAdminCheckRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]auth.UserAdminCheckRequest(nil), h.asked...)
}

// adminUserResponse mirrors the published directory JSON.
type adminUserResponse struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	StatusChangedAt string `json:"status_changed_at"`
	PrimaryEmail    string `json:"primary_email"`
	EmailVerified   bool   `json:"email_verified"`
}

type adminPageResponse struct {
	Items []adminUserResponse `json:"items"`
}

type adminStatusResponse struct {
	User    adminUserResponse `json:"user"`
	Changed bool              `json:"changed"`
}

// userIDFor reads the caller's own id from the hydration endpoint — the only
// exported way a host learns a user id without touching a repository.
func (c *linkClient) userIDFor() string {
	c.t.Helper()
	resp, body := c.do("GET", "/auth/me", "", nil)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET /auth/me = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil || me.ID == "" {
		c.t.Fatalf("decode /auth/me %q: id=%q err=%v", body, me.ID, err)
	}
	return me.ID
}

// TestUserAdminLifecycleThroughHostSeams is the end-to-end walk.
func TestUserAdminLifecycleThroughHostSeams(t *testing.T) {
	host := newAdminHost(t)

	admin := host.signUp(adminEmail)
	target := host.signUp(targetEmail)

	adminID := admin.userIDFor()
	targetID := target.userIDFor()
	host.authorize(adminID)

	// 1. The directory lists both users with their real, unmasked addresses — this
	//    surface is already explicitly authorized, so nothing is masked here.
	resp, body := admin.do("GET", "/auth/admin/users?limit=50", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/admin/users = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("directory Cache-Control = %q, want no-store", got)
	}
	var page adminPageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode directory %q: %v", body, err)
	}
	found := map[string]adminUserResponse{}
	for _, u := range page.Items {
		found[u.ID] = u
	}
	if len(found) < 2 {
		t.Fatalf("directory listed %d users, want at least 2: %s", len(found), body)
	}
	if got := found[targetID]; got.PrimaryEmail != targetEmail || !got.EmailVerified || got.Status != "active" {
		t.Errorf("target row = %+v, want the verified active %q", got, targetEmail)
	}

	// 2. The single-user read agrees with the list projection.
	one, oneBody := admin.do("GET", "/auth/admin/users/"+targetID, "", nil)
	if one.StatusCode != http.StatusOK {
		t.Fatalf("GET one user = %d, want 200; body=%s", one.StatusCode, oneBody)
	}
	var single adminUserResponse
	if err := json.Unmarshal(oneBody, &single); err != nil {
		t.Fatalf("decode single %q: %v", oneBody, err)
	}
	if single.PrimaryEmail != targetEmail || single.Status != "active" {
		t.Errorf("single read = %+v, want the active target", single)
	}
	if single.StatusChangedAt != "" {
		t.Errorf("status_changed_at = %q, want it omitted before the first transition", single.StatusChangedAt)
	}

	// 3. The target is live right now.
	if resp, _ := target.do("GET", "/auth/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("target /auth/me before deactivation = %d, want 200", resp.StatusCode)
	}

	// 4. Deactivate.
	csrf := admin.csrfToken()
	header := http.Header{"X-CSRF-Token": {csrf}}
	deact, deactBody := admin.do("POST", "/auth/admin/users/"+targetID+"/deactivate", `{}`, header)
	if deact.StatusCode != http.StatusOK {
		t.Fatalf("deactivate = %d, want 200; body=%s", deact.StatusCode, deactBody)
	}
	var status adminStatusResponse
	if err := json.Unmarshal(deactBody, &status); err != nil {
		t.Fatalf("decode deactivate %q: %v", deactBody, err)
	}
	if !status.Changed {
		t.Error("deactivate changed = false on a real transition")
	}
	if status.User.Status != "deactivated" || status.User.StatusChangedAt == "" {
		t.Errorf("deactivate result = %+v, want deactivated with a transition time", status.User)
	}

	// 5. Every credential path denies the target — GENERICALLY. The login response
	//    must be indistinguishable from a wrong password, because a distinguishable
	//    one would turn the console into an account-status oracle.
	wrongPassword, wrongBody := target.do("POST", "/auth/login",
		`{"email":"`+targetEmail+`","password":"definitely-not-the-password"}`, nil)
	deactivatedLogin, deactivatedBody := target.do("POST", "/auth/login",
		`{"email":"`+targetEmail+`","password":"`+linkPassword+`"}`, nil)

	if deactivatedLogin.StatusCode != wrongPassword.StatusCode {
		t.Errorf("deactivated login = %d but a wrong password = %d; the two must be identical",
			deactivatedLogin.StatusCode, wrongPassword.StatusCode)
	}
	if string(deactivatedBody) != string(wrongBody) {
		t.Errorf("deactivated login body differs from a wrong-password body:\n deactivated=%s\n wrong=%s",
			deactivatedBody, wrongBody)
	}
	if strings.Contains(strings.ToLower(string(deactivatedBody)), "deactiv") {
		t.Errorf("login response leaks the lifecycle status: %s", deactivatedBody)
	}

	// The live session is gone, so hydration and refresh both fail immediately —
	// no waiting for the access JWT to expire.
	if resp, body := target.do("GET", "/auth/me", "", nil); resp.StatusCode == http.StatusOK {
		t.Errorf("target /auth/me after deactivation = 200; the session must be revoked: %s", body)
	}
	if resp, body := target.do("POST", "/auth/refresh", `{}`, nil); resp.StatusCode == http.StatusOK {
		t.Errorf("target refresh after deactivation = 200; the session row was deleted: %s", body)
	}

	// 6. Replaying the deactivate is idempotent, not an error.
	replay, replayBody := admin.do("POST", "/auth/admin/users/"+targetID+"/deactivate", `{}`, http.Header{"X-CSRF-Token": {admin.csrfToken()}})
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replayed deactivate = %d, want 200; body=%s", replay.StatusCode, replayBody)
	}
	var replayed adminStatusResponse
	if err := json.Unmarshal(replayBody, &replayed); err != nil {
		t.Fatalf("decode replay %q: %v", replayBody, err)
	}
	if replayed.Changed {
		t.Error("replayed deactivate reported changed = true")
	}
	if replayed.User.StatusChangedAt != status.User.StatusChangedAt {
		t.Errorf("replay moved status_changed_at: %q → %q", status.User.StatusChangedAt, replayed.User.StatusChangedAt)
	}

	// 7. Reactivate, and the target can log in again.
	react, reactBody := admin.do("POST", "/auth/admin/users/"+targetID+"/reactivate", `{}`, http.Header{"X-CSRF-Token": {admin.csrfToken()}})
	if react.StatusCode != http.StatusOK {
		t.Fatalf("reactivate = %d, want 200; body=%s", react.StatusCode, reactBody)
	}
	var reactivated adminStatusResponse
	if err := json.Unmarshal(reactBody, &reactivated); err != nil {
		t.Fatalf("decode reactivate %q: %v", reactBody, err)
	}
	if !reactivated.Changed || reactivated.User.Status != "active" {
		t.Fatalf("reactivate result = %+v, want a changed active user", reactivated)
	}

	fresh := host.newClient()
	if resp, body := fresh.do("POST", "/auth/login",
		`{"email":"`+targetEmail+`","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after reactivation = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if resp, _ := fresh.do("GET", "/auth/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Error("a reactivated user could not hydrate its fresh session")
	}

	// 8. The host policy saw every action with the right target.
	seen := map[auth.UserAdminAction]string{}
	for _, q := range host.questions() {
		seen[q.Action] = q.TargetUserID
		if q.Principal.ID != adminID {
			t.Errorf("policy was asked about principal %q, want the calling admin %q", q.Principal.ID, adminID)
		}
	}
	for _, want := range []auth.UserAdminAction{auth.UserAdminList, auth.UserAdminRead, auth.UserAdminDeactivate, auth.UserAdminReactivate} {
		if _, ok := seen[want]; !ok {
			t.Errorf("the host policy was never asked about %q", want)
		}
	}
	if seen[auth.UserAdminList] != "" {
		t.Errorf("the list action carried a target user %q, want none", seen[auth.UserAdminList])
	}
	if seen[auth.UserAdminDeactivate] != targetID {
		t.Errorf("deactivate target = %q, want %q", seen[auth.UserAdminDeactivate], targetID)
	}
}

// TestUserAdminDeniesUnauthorized proves the host policy is the gate, and that a
// denial happens BEFORE the target is resolved — an unauthorized caller cannot
// use the response to probe which user ids exist.
func TestUserAdminDeniesUnauthorized(t *testing.T) {
	host := newAdminHost(t)
	admin := host.signUp("denies-admin@example.com")
	stranger := host.signUp("denies-stranger@example.com")
	host.authorize(admin.userIDFor())

	targetID := stranger.userIDFor()

	if resp, body := stranger.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unauthorized list = %d, want 403; body=%s", resp.StatusCode, body)
	}

	// A real id and a nonsense id must answer identically for an unauthorized
	// caller: both are 403, never 403-vs-404.
	real, realBody := stranger.do("GET", "/auth/admin/users/"+targetID, "", nil)
	fake, fakeBody := stranger.do("GET", "/auth/admin/users/definitely-not-a-user", "", nil)
	if real.StatusCode != http.StatusForbidden || fake.StatusCode != http.StatusForbidden {
		t.Errorf("unauthorized reads = %d / %d, want 403 / 403", real.StatusCode, fake.StatusCode)
	}
	if string(realBody) != string(fakeBody) {
		t.Errorf("an unauthorized read distinguishes a real id from a fake one:\n real=%s\n fake=%s", realBody, fakeBody)
	}

	if resp, body := stranger.do("POST", "/auth/admin/users/"+targetID+"/deactivate", `{}`,
		http.Header{"X-CSRF-Token": {stranger.csrfToken()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unauthorized deactivate = %d, want 403; body=%s", resp.StatusCode, body)
	}

	// The stranger is still active: a denied mutation changed nothing.
	if resp, body := admin.do("GET", "/auth/admin/users/"+targetID, "", nil); resp.StatusCode == http.StatusOK {
		var got adminUserResponse
		if err := json.Unmarshal(body, &got); err == nil && got.Status != "active" {
			t.Errorf("a denied deactivate still transitioned the user: %+v", got)
		}
	}
}

// TestUserAdminRequiresSession pins that the admin surface is live-session-gated,
// not merely authorization-gated.
func TestUserAdminRequiresSession(t *testing.T) {
	host := newAdminHost(t)
	anon := host.newClient()

	for _, path := range []string{"/auth/admin/users", "/auth/admin/users/anything"} {
		if resp, body := anon.do("GET", path, "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401; body=%s", path, resp.StatusCode, body)
		}
	}
	if len(host.questions()) != 0 {
		t.Errorf("the host policy was consulted for an unauthenticated caller: %+v", host.questions())
	}
}

// TestUserAdminRoutesAbsentWithoutCheck is the deny-by-absence proof: this host's
// store ALWAYS supplies the administration repositories, so route presence must
// come from the host's explicit Config.UserAdminCheck and nothing else.
func TestUserAdminRoutesAbsentWithoutCheck(t *testing.T) {
	// The default host config wires no UserAdminCheck.
	svc := bootInProcess(t, &recordingSender{}, nil)
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	origins := allowedOrigins()
	host := &linkHost{t: t, srv: srv, svc: svc, sender: &recordingSender{}, origin: origins[0]}
	c := host.newClient()

	// The capability IS present in the bundle …
	if authmem.New().Repositories().UserAdmin == nil {
		t.Fatal("this host's store does not supply UserAdmin; the deny-by-absence proof would be vacuous")
	}
	// … and the routes are still absent.
	if resp, _ := c.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /auth/admin/users without a UserAdminCheck = %d, want 404", resp.StatusCode)
	}
}

// TestUserAdminCheckWithoutReposFailsLoudly pins the construction guard: a host
// cannot advertise a deactivate button without the wiring that makes it real.
func TestUserAdminCheckWithoutReposFailsLoudly(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.UserAdminCheck = func(context.Context, auth.UserAdminCheckRequest) error { return nil }

	repos := authmem.New().Repositories()
	repos.ActiveSessions = nil // the fence is missing; the directory alone is not enough

	if _, err := auth.NewService(repos, cfg); err == nil {
		t.Fatal("NewService accepted a UserAdminCheck with no fenced session mint")
	} else if !errors.Is(err, auth.ErrUserAdminReposRequired) {
		t.Fatalf("NewService error = %v, want ErrUserAdminReposRequired", err)
	}

	repos = authmem.New().Repositories()
	repos.UserAdmin = nil
	if _, err := auth.NewService(repos, cfg); !errors.Is(err, auth.ErrUserAdminReposRequired) {
		t.Fatalf("NewService error = %v, want ErrUserAdminReposRequired", err)
	}
}
