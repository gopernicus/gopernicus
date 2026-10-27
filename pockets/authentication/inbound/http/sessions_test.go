package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var _ session.ActivityRepository = (*idleMemSessions)(nil)

// doChangePassword issues a cookie-lane POST /auth/password/change carrying the
// double-submit CSRF token the browser-safe gate now requires (design §9.1, the
// AV3-6.6 parity fix). The session cookie authenticates; the __Host-auth_csrf cookie plus
// its matching X-CSRF-Token header satisfy the gate (newTestHandler's empty Origin
// allowlist passes an Origin-less same-context request, so the token pair is the
// operative check). Cases that expect a pre-gate rejection (no/revoked session ->
// authenticator's 401, which runs before the gate) keep plain do.
func doChangePassword(t *testing.T, h http.Handler, body string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/auth/password/change", strings.NewReader(body))
	r.AddCookie(session)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token"})
	r.Header.Set(csrfHeaderName, "csrf-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRegisterRoute(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"a@example.com","password":"password123456789","display_name":"A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["email"] != "a@example.com" {
		t.Errorf("email = %v", resp["email"])
	}
}

func TestRegisterRouteBadJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
}

func TestRegisterRouteShortPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"b@example.com","password":"short","display_name":"B"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("short password status = %d, want 400", rec.Code)
	}
}

func TestLoginRouteSetsCookie(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"c@example.com","password":"password123456789","display_name":"C"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"c@example.com","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sessionCookie(rec) == nil {
		t.Error("login did not set a session (access) cookie")
	}
	rc := refreshCookie(rec)
	if rc == nil {
		t.Fatal("login did not set a refresh cookie")
	}
	if rc.Path != "/auth" || !rc.HttpOnly || rc.SameSite != http.SameSiteLaxMode {
		t.Errorf("refresh cookie policy = {path=%q httpOnly=%v sameSite=%v}, want {/auth true Lax}", rc.Path, rc.HttpOnly, rc.SameSite)
	}
}

// TestRefreshRouteViaCookie proves the cookie-driven refresh path: /auth/refresh
// with the refresh cookie rotates and sets a fresh pair of cookies.
func TestRefreshRouteViaCookie(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"rc@example.com","password":"password123456789","display_name":"R"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"rc@example.com","password":"password123456789"}`)
	rc := refreshCookie(login)
	if rc == nil {
		t.Fatal("no refresh cookie from login")
	}

	rec := do(t, h, "POST", "/auth/refresh", "", &http.Cookie{Name: rc.Name, Value: rc.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sessionCookie(rec) == nil || refreshCookie(rec) == nil {
		t.Error("refresh did not set a fresh cookie pair")
	}

	// Reuse of the original (now consumed after a second try) refresh cookie
	// eventually 401s; a bare refresh with no token 401s immediately.
	bare := do(t, h, "POST", "/auth/refresh", "")
	if bare.Code != http.StatusUnauthorized {
		t.Errorf("bare refresh status = %d, want 401", bare.Code)
	}
}

func TestLoginRouteWrongPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"d@example.com","password":"password123456789","display_name":"D"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"d@example.com","password":"wrongpass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}
}

func TestLoginRouteRateLimited(t *testing.T) {
	h := newTestHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/login", `{"email":"e@example.com","password":"password123456789"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited status = %d, want 429", rec.Code)
	}
}

// TestVerifyRouteUnknown proves an unknown account is indistinguishable from a
// wrong code: the challenge rail returns the generic challenge_invalid (400), not a
// 404 that would enumerate accounts (design §5.8).
func TestVerifyRouteUnknown(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/verify", `{"email":"ghost@example.com","code":"123456"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("verify unknown status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

func TestForgotPasswordRouteAlwaysAccepted(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/forgot", `{"email":"ghost@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("forgot status = %d, want 202", rec.Code)
	}
}

func TestResetPasswordRouteUnknownToken(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/reset", `{"token":"nope","password":"newpassword12345"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("reset unknown token status = %d, want 404", rec.Code)
	}
}

// TestLogoutRouteNotGated proves logout is NOT credential-gated (§1.5): a bare
// request (no cookie, no bearer) is a 200 no-op that still clears both cookies —
// an expired access JWT must never make logout a no-op.
func TestLogoutRouteNotGated(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/logout", "")
	if rec.Code != http.StatusOK {
		t.Errorf("bare logout status = %d, want 200 (ungated, idempotent)", rec.Code)
	}
}

func TestLogoutRouteClearsSession(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"f@example.com","password":"password123456789","display_name":"F"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"f@example.com","password":"password123456789"}`)
	access := sessionCookie(login)
	refresh := refreshCookie(login)
	if access == nil || refresh == nil {
		t.Fatal("login did not set both cookies")
	}

	rec := do(t, h, "POST", "/auth/logout", "",
		&http.Cookie{Name: access.Name, Value: access.Value},
		&http.Cookie{Name: refresh.Name, Value: refresh.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	cleared := sessionCookie(rec)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("logout did not clear the access cookie: %+v", cleared)
	}
	if rc := refreshCookie(rec); rc == nil || rc.MaxAge >= 0 {
		t.Errorf("logout did not clear the refresh cookie: %+v", rc)
	}

	// The session was deleted: a Live()-gated route rejects the access cookie
	// even though the stateless access JWT itself is still within its TTL.
	stale := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123456789","new_password":"newpassword456789"}`,
		&http.Cookie{Name: access.Name, Value: access.Value})
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("live-session route after logout status = %d, want 401", stale.Code)
	}
}

func TestChangePasswordRouteRequiresSession(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/change", `{"current_password":"password123456789","new_password":"newpassword456789"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("change without session status = %d, want 401", rec.Code)
	}
}

// TestChangePasswordCookieRequiresCSRF proves the AV3-6.6 parity fix: a live cookie
// session is not enough for the change-password mutation — the browser-safe gate
// (design §9.1) rejects a cookie-lane request with no double-submit CSRF token.
func TestChangePasswordCookieRequiresCSRF(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"csrf-cp@example.com","password":"password123456789","display_name":"C"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"csrf-cp@example.com","password":"password123456789"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123456789","new_password":"newpassword456789"}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie change without CSRF status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

func TestChangePasswordRouteStrictDecode(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"g@example.com","password":"password123456789","display_name":"G"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"g@example.com","password":"password123456789"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	rec := doChangePassword(t, h,
		`{"current_password":"password123456789","new_password":"newpassword456789","extra":1}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

func TestChangePasswordRouteWrongCurrent(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"h@example.com","password":"password123456789","display_name":"H"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"h@example.com","password":"password123456789"}`)
	c := sessionCookie(login)
	rec := doChangePassword(t, h,
		`{"current_password":"wrongcurrent","new_password":"newpassword456789"}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong-current status = %d, want 401", rec.Code)
	}
}

func TestChangePasswordRouteHappyPath(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"i@example.com","password":"password123456789","display_name":"I"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"password123456789"}`)
	old := sessionCookie(login)
	if old == nil {
		t.Fatal("no session cookie from login")
	}

	rec := doChangePassword(t, h,
		`{"current_password":"password123456789","new_password":"newpassword456789"}`,
		&http.Cookie{Name: old.Name, Value: old.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("change status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	fresh := sessionCookie(rec)
	if fresh == nil || fresh.Value == "" || fresh.Value == old.Value {
		t.Errorf("change did not set a fresh session cookie: old=%v new=%v", old, fresh)
	}

	// The old session is revoked: the Live()-gated route rejects the old
	// access cookie.
	stale := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"newpassword456789","new_password":"finalpass789012"}`,
		&http.Cookie{Name: old.Name, Value: old.Value})
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("old cookie after change status = %d, want 401", stale.Code)
	}
	// The fresh session cookie is live: it passes the Live() gate and changes
	// the password again.
	ok := doChangePassword(t, h,
		`{"current_password":"newpassword456789","new_password":"finalpass789012"}`,
		&http.Cookie{Name: fresh.Name, Value: fresh.Value})
	if ok.Code != http.StatusOK {
		t.Errorf("fresh cookie live-session change status = %d, want 200; body=%s", ok.Code, ok.Body)
	}
	// Re-login with the final password succeeds.
	relogin := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"finalpass789012"}`)
	if relogin.Code != http.StatusOK {
		t.Errorf("re-login with new password status = %d, want 200", relogin.Code)
	}
}

// idleMemSessions adds the idle-timeout capability to memSessions: a stamp per
// row that only RotateAt writes, and a compare-and-swap DeleteIfIdle.
type idleMemSessions struct {
	*memSessions
	stampMu sync.Mutex
	stamps  map[string]time.Time
}

func (s *idleMemSessions) GetByRefreshHashWithActivity(ctx context.Context, hash string) (session.Session, session.RefreshMatch, session.Activity, error) {
	sess, match, err := s.GetByRefreshHash(ctx, hash)
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	s.stampMu.Lock()
	defer s.stampMu.Unlock()
	stamp, ok := s.stamps[sess.ID]
	return sess, match, session.Activity{LastRotatedAt: stamp, Stamped: ok}, nil
}

func (s *idleMemSessions) RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error {
	if err := s.Rotate(ctx, id, expectedCurrentHash, newHash); err != nil {
		return err
	}
	s.stampMu.Lock()
	defer s.stampMu.Unlock()
	s.stamps[id] = now
	return nil
}

func (s *idleMemSessions) DeleteIfIdle(_ context.Context, id string, observed session.Activity, observedRotationCount int) (bool, error) {
	s.stampMu.Lock()
	defer s.stampMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok || sess.RotationCount != observedRotationCount {
		return false, nil
	}
	stamp, stamped := s.stamps[id]
	if stamped != observed.Stamped || (stamped && !stamp.Equal(observed.LastRotatedAt)) {
		return false, nil
	}
	delete(s.m, id)
	delete(s.stamps, id)
	return true, nil
}

func (s *idleMemSessions) ProbeSchema(context.Context) error { return nil }

// TestRefreshRouteIdleExpiryIsTheGenericDenial: with SessionsConfig.IdleTimeout
// wired, POST /auth/refresh inside the window rotates; after a full window
// without a refresh it answers exactly the response an unknown refresh token
// gets (the pinned generic 401 — no idle-specific code or body) and the
// session row is gone.
func TestRefreshRouteIdleExpiryIsTheGenericDenial(t *testing.T) {
	var clockMu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(d)
	}
	users := newMemUsers()
	passwords := &memPasswords{m: map[string]string{}}
	inner := &memSessions{m: map[string]session.Session{}}
	sessions := &idleMemSessions{memSessions: inner, stamps: map[string]time.Time{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	// The wrapper is not a *memSessions, so wire the user fakes to the inner
	// store here, as newServiceWithFakes does for a bare one.
	wireCredentialFakes(users, passwords, inner, &memOAuthAccounts{}, nil, challenges)
	svc := newServiceWithFakes(authenticationFixture{
		Users:          users,
		Identifiers:    newMemIdentifiers(users),
		Passwords:      passwords,
		Sessions:       sessions,
		ActiveSessions: memActiveSessions{users, inner},
		Challenges:     challenges,
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: inner},
		Hasher:         fakeHasher{},
		Limiter:        ratelimiter.NewMemory(),
		Clock:          clock,
		AccessTokenTTL: time.Minute,
		IdleTimeout:    3 * time.Minute,
	})
	mux := web.NewWebHandler()
	mount(mux, mountDeps{Auth: svc})

	do(t, mux, "POST", "/auth/register", `{"email":"idle@example.com","password":"password123456789","display_name":"I"}`)
	login := do(t, mux, "POST", "/auth/login", `{"email":"idle@example.com","password":"password123456789"}`)
	rc := refreshCookie(login)
	if rc == nil {
		t.Fatalf("no refresh cookie from login; status %d body=%s", login.Code, login.Body)
	}

	advance(2 * time.Minute)
	rotated := do(t, mux, "POST", "/auth/refresh", "", &http.Cookie{Name: rc.Name, Value: rc.Value})
	next := refreshCookie(rotated)
	if rotated.Code != http.StatusOK || next == nil || next.Value == "" {
		t.Fatalf("refresh inside the idle window = %d; body=%s", rotated.Code, rotated.Body)
	}
	if len(sessions.stamps) != 1 {
		t.Fatalf("rotation did not stamp: %v", sessions.stamps)
	}

	advance(3 * time.Minute)
	idle := do(t, mux, "POST", "/auth/refresh", "", &http.Cookie{Name: next.Name, Value: next.Value})
	unknown := do(t, mux, "POST", "/auth/refresh", "", &http.Cookie{Name: next.Name, Value: "not-a-refresh-token"})
	if idle.Code != http.StatusUnauthorized {
		t.Fatalf("idle refresh = %d, want 401; body=%s", idle.Code, idle.Body)
	}
	if idle.Code != unknown.Code || idle.Body.String() != unknown.Body.String() {
		t.Errorf("idle refresh = %d %q, want the unknown-token response %d %q", idle.Code, idle.Body, unknown.Code, unknown.Body)
	}
	inner.mu.Lock()
	rows := len(inner.m)
	inner.mu.Unlock()
	if rows != 0 {
		t.Errorf("idle session rows = %d, want 0", rows)
	}
}
