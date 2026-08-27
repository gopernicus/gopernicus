package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// GET /auth/me transport tests (ruling 6). The route is session HYDRATION: it
// returns the SAME userResponse body login and register return, it is
// live-session-gated so a revoked access JWT cannot hydrate, and it is human-only
// — a machine principal gets a 401, never a fabricated profile.

// meFixture holds the mem stores and the mounted handler so a test can seed
// identity and revoke sessions directly.
type meFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	sessions  *memSessions
}

// newMeHandler wires a real authsvc.Service over the mem stores — including the
// machine repos, so an API-key principal is a REAL credential in these tests — and
// mounts the routes, optionally beneath a host prefix.
func newMeHandler(t *testing.T, prefix string) meFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:           users,
		Identifiers:     idents,
		Passwords:       passwords,
		Sessions:        sessions,
		Challenges:      challenges,
		Protector:       memProtector{},
		PasswordResets:  &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:          fakeHasher{},
		Deliver:         router,
		Queue:           stubQueue{},
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          authsvc.CookieConfig{},
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
		TokenSigner:     newFakeSigner(),
	})
	h := web.NewWebHandler()
	var r feature.RouteRegistrar = h
	if prefix != "" {
		r = feature.PrefixRegistrar{Prefix: prefix, Next: h}
	}
	Mount(r, Deps{Auth: svc, ListStrategy: crud.StrategyCursor, MachineGate: allowMachineGate})
	return meFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions}
}

// bearerRequest builds a GET carrying an Authorization: Bearer credential.
func bearerRequest(path, token string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// meBody decodes the hydration body.
func meBody(t *testing.T, rec *httptest.ResponseRecorder) userResponse {
	t.Helper()
	var resp userResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /auth/me body %q: %v", rec.Body, err)
	}
	return resp
}

// TestMeRequiresLiveSession proves both denials: no credential, and a session
// revoked after the access JWT was minted (the immediate-revocation tier, ruling
// 6 — not RequireUser's stale window).
func TestMeRequiresLiveSession(t *testing.T) {
	f := newMeHandler(t, "")
	if rec := do(t, f.h, "GET", "/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-credential status = %d, want 401; body=%s", rec.Code, rec.Body)
	}

	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")
	if rec := do(t, f.h, "GET", "/auth/me", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("live-session status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	if err := f.sessions.DeleteByUser(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if rec := do(t, f.h, "GET", "/auth/me", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked-session status = %d, want 401; body=%s", rec.Code, rec.Body)
	}
}

// TestMeHydratesVerifiedAccount proves the body over BOTH user credential lanes —
// the session cookie and a bearer access JWT — matches login's body byte for byte,
// with the unmasked email, and is not cacheable.
func TestMeHydratesVerifiedAccount(t *testing.T) {
	f := newMeHandler(t, "")
	f.seedAccount("u1", "alice@example.com")

	loginRec := do(t, f.h, "POST", "/auth/login", `{"email":"alice@example.com","password":"password123456789"}`)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginRec.Code, loginRec.Body)
	}
	want := meBody(t, loginRec)
	if want.Email != "alice@example.com" || !want.EmailVerified || want.DisplayName != "Seed" {
		t.Fatalf("login body = %+v, want the unmasked verified identity", want)
	}
	cookie := sessionCookie(loginRec)

	cookieRec := do(t, f.h, "GET", "/auth/me", "", cookie)
	if cookieRec.Code != http.StatusOK {
		t.Fatalf("cookie-session status = %d, want 200; body=%s", cookieRec.Code, cookieRec.Body)
	}
	if got := meBody(t, cookieRec); got != want {
		t.Errorf("cookie-session body = %+v, want %+v (login and /auth/me must not drift)", got, want)
	}
	if got := cookieRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	// The bearer lane: POST /auth/token mints a session-backed access JWT.
	tokenRec := do(t, f.h, "POST", "/auth/token", `{"email":"alice@example.com","password":"password123456789"}`)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want 200; body=%s", tokenRec.Code, tokenRec.Body)
	}
	var minted tokenResponse
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode token body: %v", err)
	}
	bearerRec := serve(f.h, bearerRequest("/auth/me", minted.AccessToken))
	if bearerRec.Code != http.StatusOK {
		t.Fatalf("bearer-session status = %d, want 200; body=%s", bearerRec.Code, bearerRec.Body)
	}
	if got := meBody(t, bearerRec); got != want {
		t.Errorf("bearer-session body = %+v, want %+v", got, want)
	}
}

// TestMeHydratesAllowedUnverifiedAccount is the case ActiveVerifiedIdentifier
// cannot serve: a just-registered account logs in (verification is not required)
// and hydrates with its email present and email_verified false.
//
// It also pins the no-drift contract across ALL THREE bodies with a direct
// body-to-body comparison: register submits the mixed-case "New@Example.com", so
// echoing the raw submitted address would make register/login disagree with
// /auth/me, which can only ever report the normalized stored identifier.
func TestMeHydratesAllowedUnverifiedAccount(t *testing.T) {
	f := newMeHandler(t, "")
	reg := do(t, f.h, "POST", "/auth/register", `{"email":"New@Example.com","password":"password123456789","display_name":"Nina"}`)
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", reg.Code, reg.Body)
	}
	registered := meBody(t, reg)
	loginRec := do(t, f.h, "POST", "/auth/login", `{"email":"New@Example.com","password":"password123456789"}`)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginRec.Code, loginRec.Body)
	}
	loggedIn := meBody(t, loginRec)

	rec := do(t, f.h, "GET", "/auth/me", "", sessionCookie(loginRec))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	got := meBody(t, rec)
	if got.Email != "new@example.com" {
		t.Errorf("email = %q, want the normalized primary email of an unverified account", got.Email)
	}
	if got.EmailVerified {
		t.Error("email_verified = true for an unverified account")
	}
	if got.DisplayName != "Nina" || got.ID == "" {
		t.Errorf("body = %+v, want the registered profile", got)
	}
	if registered.Email != got.Email {
		t.Errorf("register email = %q, /auth/me email = %q; the two must not drift", registered.Email, got.Email)
	}
	if loggedIn.Email != got.Email {
		t.Errorf("login email = %q, /auth/me email = %q; the two must not drift", loggedIn.Email, got.Email)
	}
	if registered != got || loggedIn != got {
		t.Errorf("register = %+v, login = %+v, /auth/me = %+v; all three bodies must agree", registered, loggedIn, got)
	}
}

// TestMeRejectsMachinePrincipal proves the human-only gate: RequireLiveSession
// admits a valid API key (a machine caller has no session row), so /auth/me must
// deny it itself. The delivery-status control proves the very same key IS a live
// credential — it gets a 400 there, not a 401.
func TestMeRejectsMachinePrincipal(t *testing.T) {
	f := newMeHandler(t, "")
	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	create := machinePOST(t, f.h, "/auth/service-accounts", `{"name":"bot"}`, cookie)
	if create.Code != http.StatusCreated {
		t.Fatalf("create service account status = %d, want 201; body=%s", create.Code, create.Body)
	}
	var sa map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &sa); err != nil {
		t.Fatalf("decode service account: %v", err)
	}
	mint := machinePOST(t, f.h, "/auth/service-accounts/"+sa["id"].(string)+"/keys", `{"name":"deploy"}`, cookie)
	if mint.Code != http.StatusCreated {
		t.Fatalf("mint key status = %d, want 201; body=%s", mint.Code, mint.Body)
	}
	var minted map[string]any
	if err := json.Unmarshal(mint.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode minted key: %v", err)
	}
	key, _ := minted["key"].(string)
	if key == "" {
		t.Fatal("mint returned no plaintext key")
	}

	if rec := serve(f.h, bearerRequest("/auth/delivery/status", key)); rec.Code != http.StatusBadRequest {
		t.Fatalf("api-key delivery-status = %d, want 400 (the key must be a LIVE credential); body=%s", rec.Code, rec.Body)
	}

	rec := serve(f.h, bearerRequest("/auth/me", key))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api-key /auth/me = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(rec.Body.Bytes()) > 0 {
		var resp userResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil && resp.ID != "" {
			t.Fatalf("machine principal received a profile: %+v", resp)
		}
	}
}

// TestMePrefixedMount proves the route rides feature.PrefixRegistrar with no
// handler change: a host mounting beneath /api/v1 serves /api/v1/auth/me and the
// unprefixed path is absent.
func TestMePrefixedMount(t *testing.T) {
	f := newMeHandler(t, "/api/v1")
	f.seedAccount("u1", "alice@example.com")

	loginRec := do(t, f.h, "POST", "/api/v1/auth/login", `{"email":"alice@example.com","password":"password123456789"}`)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("prefixed login status = %d, want 200; body=%s", loginRec.Code, loginRec.Body)
	}
	cookie := sessionCookie(loginRec)

	rec := do(t, f.h, "GET", "/api/v1/auth/me", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("prefixed status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := meBody(t, rec); got.Email != "alice@example.com" || !got.EmailVerified {
		t.Errorf("prefixed body = %+v, want the verified identity", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if unprefixed := do(t, f.h, "GET", "/auth/me", "", cookie); unprefixed.Code != http.StatusNotFound {
		t.Errorf("unprefixed /auth/me = %d, want 404 under a prefixed mount", unprefixed.Code)
	}
}

// seedAccount inserts a user with a password and a verified primary email so
// login resolves it.
func (f meFixture) seedAccount(userID, emailAddr string) {
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "Seed"}
	f.users.mu.Unlock()
	f.passwords.mu.Lock()
	f.passwords.m[userID] = "hash:password123456789"
	f.passwords.mu.Unlock()
	f.idents.insert(verifiedEmail("id-primary", userID, emailAddr))
}

// login authenticates the seeded account over the cookie lane.
func (f meFixture) login(t *testing.T, emailAddr string) *http.Cookie {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"`+emailAddr+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("login set no session cookie")
	}
	return c
}
