package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// routeSpec is one method/path pair of the bundled lifecycle surface.
type routeSpec struct{ method, path string }

// machineLifecycleMutations are the three state changes; machineLifecycleReads
// the two body-less reads. Only the mutations carry the browser-safe
// Origin/CSRF gate, so the split is what the CSRF case asserts against.
var (
	machineLifecycleMutations = []routeSpec{
		{"POST", "/auth/service-accounts"},
		{"POST", "/auth/service-accounts/sa-1/keys"},
		{"POST", "/auth/api-keys/k-1/revoke"},
	}
	machineLifecycleReads = []routeSpec{
		{"GET", "/auth/service-accounts"},
		{"GET", "/auth/service-accounts/sa-1/keys"},
	}
	// machineLifecycleRoutes is the full bundled lifecycle surface (design §4.1).
	// Every identity case below asserts the SAME answer on all five: a posture
	// that protected four of them would be no posture at all.
	machineLifecycleRoutes = append(append([]routeSpec{}, machineLifecycleMutations...), machineLifecycleReads...)
)

// allowMachineGate is the stub host gate that authorizes every caller. The
// pocket cannot import pockets/authorization (guard-pocket-no-cross-pocket),
// so the real permission gate's 403 body is proven in examples/auth-cms; here a
// stub stands for "the host said yes".
func allowMachineGate(next http.Handler) http.Handler { return next }

// denyMachineGate is the stub host gate that refuses every caller with a 403,
// standing in for authorization's permission_denied gate.
func denyMachineGate(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		web.RespondJSONError(w, web.ErrForbidden("permission denied"))
	})
}

// machineFixture is a mounted route table plus the service behind it, so a case
// can mint real machine credentials without going through the gated routes.
type machineFixture struct {
	h   http.Handler
	svc *authsvc.Service
}

// newMachineFixture wires the machine repositories and mounts the route table
// with the given host gate. A nil gate is the deny-by-absence posture: the
// lifecycle routes are not registered at all.
func newMachineFixture(t *testing.T, gate web.Middleware) machineFixture {
	t.Helper()
	users := newMemUsers()
	svc := authsvc.NewService(authsvc.Deps{
		Users:           users,
		Identifiers:     newMemIdentifiers(users),
		Passwords:       &memPasswords{m: map[string]string{}},
		Sessions:        &memSessions{m: map[string]session.Session{}},
		Hasher:          fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          authsvc.CookieConfig{},
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
		TokenSigner:     newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, Deps{Auth: svc, ListStrategy: crud.StrategyCursor, MachineGate: gate})
	return machineFixture{h: h, svc: svc}
}

// bearer runs a request with an Authorization bearer credential instead of a cookie.
func bearer(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// actAsUserKey mints a live act-as-user API key owned by userID, bypassing the
// gated routes: the point of the case is what the KEY may do afterwards.
func actAsUserKey(t *testing.T, svc *authsvc.Service, userID string) string {
	t.Helper()
	ctx := context.Background()
	sa, err := svc.CreateServiceAccount(ctx, userID, "impersonator", "", true, userID)
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	return raw
}

// userIDFor registers/logs in a user and returns its id alongside the session cookie.
func userIDFor(t *testing.T, h http.Handler, email string) (string, *http.Cookie) {
	t.Helper()
	c := sessionFor(t, h, email)
	me := do(t, h, "GET", "/auth/me", "", c)
	if me.Code != http.StatusOK {
		t.Fatalf("/auth/me status = %d, want 200; body=%s", me.Code, me.Body)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("/auth/me returned no user id")
	}
	return resp.ID, c
}

// TestMachineRoutesGateAbsent_NotMounted proves deny-by-absence (D1): with both
// machine repositories wired but no host gate, every lifecycle route answers 404
// — not 401/403, it is not there — while key AUTHENTICATION is untouched.
func TestMachineRoutesGateAbsent_NotMounted(t *testing.T) {
	fx := newMachineFixture(t, nil)
	for _, tc := range machineLifecycleRoutes {
		if rec := do(t, fx.h, tc.method, tc.path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("no gate: %s %s = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}

	userID, _ := userIDFor(t, fx.h, "nogate@example.com")
	raw := actAsUserKey(t, fx.svc, userID)
	me := bearer(t, fx.h, "GET", "/auth/me", raw)
	if me.Code != http.StatusOK {
		t.Errorf("bearer API key on /auth/me = %d, want 200 — key authentication must survive the absent gate; body=%s", me.Code, me.Body)
	}
}

// TestMachineRoutesGateSet_NoCredentialUnauthorized proves the mounted routes
// still fail closed without any credential.
func TestMachineRoutesGateSet_NoCredentialUnauthorized(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	for _, tc := range machineLifecycleRoutes {
		if rec := do(t, fx.h, tc.method, tc.path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("gated, no credential: %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestMachineRoutesRefuseAPIKeyBearer pins the human-only rule: an act-as-user
// API key resolves to its owner everywhere else (proven here on /auth/me), yet
// RequireUser refuses it on every lifecycle route — a key can never mint or
// revoke another key through the bundled surface.
func TestMachineRoutesRefuseAPIKeyBearer(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	userID, _ := userIDFor(t, fx.h, "actas@example.com")
	raw := actAsUserKey(t, fx.svc, userID)

	if me := bearer(t, fx.h, "GET", "/auth/me", raw); me.Code != http.StatusOK {
		t.Fatalf("act-as-user key on /auth/me = %d, want 200 (the key must be live for this case to mean anything)", me.Code)
	}
	for _, tc := range machineLifecycleRoutes {
		if rec := bearer(t, fx.h, tc.method, tc.path, raw); rec.Code != http.StatusUnauthorized {
			t.Errorf("act-as-user key: %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestMachineRoutesRefuseRevokedSession proves RequireLiveSession's place in the
// stack: a logged-out session's outstanding access JWT is refused within one
// round-trip instead of surviving for AccessTokenTTL.
func TestMachineRoutesRefuseRevokedSession(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	c := sessionFor(t, fx.h, "revoked@example.com")
	if list := do(t, fx.h, "GET", "/auth/service-accounts", "", c); list.Code != http.StatusOK {
		t.Fatalf("live session list = %d, want 200; body=%s", list.Code, list.Body)
	}
	if out := do(t, fx.h, "POST", "/auth/logout", "", c); out.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200; body=%s", out.Code, out.Body)
	}
	for _, tc := range machineLifecycleRoutes {
		if rec := do(t, fx.h, tc.method, tc.path, "", c); rec.Code != http.StatusUnauthorized {
			t.Errorf("revoked session: %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestMachineRoutesGateRefuses proves the host's denial is what the caller sees:
// a live human the gate refuses gets the gate's own 403 on every route.
func TestMachineRoutesGateRefuses(t *testing.T) {
	fx := newMachineFixture(t, denyMachineGate)
	c := sessionFor(t, fx.h, "denied@example.com")
	for _, tc := range machineLifecycleReads {
		rec := do(t, fx.h, tc.method, tc.path, "", c)
		if rec.Code != http.StatusForbidden {
			t.Errorf("refusing gate: %s %s = %d, want 403; body=%s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	for _, tc := range machineLifecycleMutations {
		rec := machinePOST(t, fx.h, tc.path, `{"name":"bot"}`, c)
		if rec.Code != http.StatusForbidden {
			t.Errorf("refusing gate: %s %s = %d, want 403; body=%s", tc.method, tc.path, rec.Code, rec.Body)
		}
		if _, message := errorEnvelope(t, rec); message != "permission denied" {
			t.Errorf("refusing gate: %s %s message = %q, want the gate's own denial", tc.method, tc.path, message)
		}
	}
}

// crossOriginPOST issues a cookie-authenticated POST carrying a well-formed
// double-submit pair but an Origin the host never allowlisted — the forged
// cross-site shape the browser-safe gate exists to refuse.
func crossOriginPOST(t *testing.T, h http.Handler, path, body, origin string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set(csrfHeaderName, "tok")
	r.AddCookie(c)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestMachineRoutesRefuseUnsafeBrowserMutation pins the CSRF rung of the stack:
// a cookie-authenticated mint or revoke must clear the browser-safe gate BEFORE
// the host's policy is consulted, so a missing double-submit token and a
// non-allowlisted Origin are both 403 on all three mutations — under an
// ALLOW-ALL gate, which proves the refusal is the pocket's, not the host's. The
// two body-less GETs are not state changes and still serve.
func TestMachineRoutesRefuseUnsafeBrowserMutation(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	c := sessionFor(t, fx.h, "csrf@example.com")

	for _, tc := range machineLifecycleMutations {
		missing := do(t, fx.h, tc.method, tc.path, `{"name":"bot"}`, c)
		if missing.Code != http.StatusForbidden {
			t.Errorf("no CSRF token: %s %s = %d, want 403; body=%s", tc.method, tc.path, missing.Code, missing.Body)
			continue
		}
		if code, _ := errorEnvelope(t, missing); code != "permission_denied" {
			t.Errorf("no CSRF token: %s %s code = %q, want permission_denied", tc.method, tc.path, code)
		}

		forged := crossOriginPOST(t, fx.h, tc.path, `{"name":"bot"}`, "https://evil.example.com", c)
		if forged.Code != http.StatusForbidden {
			t.Errorf("disallowed Origin: %s %s = %d, want 403; body=%s", tc.method, tc.path, forged.Code, forged.Body)
			continue
		}
		if code, _ := errorEnvelope(t, forged); code != originRejectedCode {
			t.Errorf("disallowed Origin: %s %s code = %q, want %q", tc.method, tc.path, code, originRejectedCode)
		}
	}

	for _, tc := range machineLifecycleReads {
		if rec := do(t, fx.h, tc.method, tc.path, "", c); rec.Code != http.StatusOK {
			t.Errorf("browser-safe gate leaked onto a read: %s %s = %d, want 200; body=%s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
}

// TestMachineRoutesGateStackOrder pins the registration order web.Handle
// applies outermost-first: RequireUser, then RequireLiveSession, then the gate.
// A credential-less request must be answered 401 by the pocket WITHOUT the
// host's gate ever running — a gate placed outermost would have to answer for
// unauthenticated traffic it knows nothing about.
func TestMachineRoutesGateStackOrder(t *testing.T) {
	var reached int
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached++
			next.ServeHTTP(w, r)
		})
	}
	fx := newMachineFixture(t, counting)

	if rec := do(t, fx.h, "GET", "/auth/service-accounts", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d, want 401", rec.Code)
	}
	if reached != 0 {
		t.Errorf("gate ran %d times for an unauthenticated request, want 0 (the identity gates are outermost)", reached)
	}

	c := sessionFor(t, fx.h, "order@example.com")
	if rec := do(t, fx.h, "GET", "/auth/service-accounts", "", c); rec.Code != http.StatusOK {
		t.Fatalf("live session = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if reached != 1 {
		t.Errorf("gate ran %d times for an authenticated request, want 1", reached)
	}

	// The gate is scoped to the lifecycle routes: the rest of the auth surface
	// must never run a host's machine-identity policy.
	do(t, fx.h, "POST", "/auth/login", `{"email":"order@example.com","password":"password123456789"}`)
	do(t, fx.h, "GET", "/auth/me", "", c)
	if reached != 1 {
		t.Errorf("gate ran %d times after /auth/login and /auth/me, want 1 — it is mounted on the lifecycle routes only", reached)
	}
}
