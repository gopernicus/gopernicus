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

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// memAuthGrants is the inbound test's in-memory step-up grant store.
type memAuthGrants struct {
	users    *memUsers
	sessions *memSessions
	mu       sync.Mutex
	m        map[string]authgrant.Grant
	n        int
}

func newFakeAuthGrants() *memAuthGrants { return &memAuthGrants{m: map[string]authgrant.Grant{}} }

func (f *memAuthGrants) Create(ctx context.Context, g authgrant.Grant, expectedAuthRevision int64, now time.Time) (authgrant.Grant, error) {
	if err := ctx.Err(); err != nil {
		return authgrant.Grant{}, err
	}
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	u, ok := f.users.byID[g.UserID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	sess, ok := f.sessions.m[g.SessionID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	if !u.Active() || sess.UserID != g.UserID || !now.Before(sess.ExpiresAt) {
		return authgrant.Grant{}, sdk.ErrUnauthorized
	}
	if u.AuthRevision != expectedAuthRevision {
		return authgrant.Grant{}, sdk.ErrConflict
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if g.ID == "" {
		f.n++
		g.ID = "grant-" + itoa(f.n)
	}
	f.m[g.ID] = g
	return g, nil
}

func (f *memAuthGrants) Consume(ctx context.Context, req authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	if err := ctx.Err(); err != nil {
		return authgrant.Grant{}, err
	}
	if err := req.Validate(); err != nil {
		return authgrant.Grant{}, err
	}
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	u, ok := f.users.byID[req.UserID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	sess, ok := f.sessions.m[req.SessionID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	if !u.Active() || sess.UserID != req.UserID || !now.Before(sess.ExpiresAt) {
		return authgrant.Grant{}, sdk.ErrUnauthorized
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, g := range f.m {
		if g.Consumed() || g.SessionID != req.SessionID || g.Purpose != req.Purpose || g.ContextDigest != req.ContextDigest {
			continue
		}
		if g.UserID != req.UserID || (!g.Expired(now) && !req.Accepts(g.AuthenticatedAt, g.Assurance, now)) {
			continue
		}
		g.ConsumedAt = now.UTC()
		f.m[id] = g
		if g.Expired(now) {
			return authgrant.Grant{}, sdk.ErrExpired
		}
		return g, nil
	}
	return authgrant.Grant{}, sdk.ErrNotFound
}

func (f *memAuthGrants) DeleteBySession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, g := range f.m {
		if g.SessionID == sessionID {
			delete(f.m, id)
		}
	}
	return nil
}

// newStepUpHandler mounts the full route table with the step-up grant subsystem
// wired and the browser-safe-mutation policy configured for the session cookie.
func newStepUpHandler(t *testing.T) (http.Handler, *memAuthGrants) {
	t.Helper()
	users := newMemUsers()
	grants := &memAuthGrants{m: map[string]authgrant.Grant{}}
	svc := newServiceWithFakes(authenticationFixture{
		Users:                users,
		Identifiers:          newMemIdentifiers(users),
		Passwords:            &memPasswords{m: map[string]string{}},
		Sessions:             &memSessions{m: map[string]session.Session{}},
		AuthenticationGrants: grants,
		Hasher:               fakeHasher{},
		Limiter:              ratelimiter.NewMemory(),
		TokenSigner:          newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Mutation: MutationSecurity{
		AllowedOrigins:    []string{"https://app.example.com"},
		SessionCookieName: svc.SessionCookieName(),
	}})
	return h, grants
}

// registerAndBearer registers a user and returns a bearer access token for it.
func registerAndBearer(t *testing.T, h http.Handler, email string) string {
	t.Helper()
	do(t, h, "POST", "/auth/register", `{"email":"`+email+`","password":"password123456789","display_name":"S"}`)
	rec := do(t, h, "POST", "/auth/token", `{"email":"`+email+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d; body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return resp.AccessToken
}

// jsonBearer POSTs a JSON body with a Bearer credential (bearer-only requests skip
// the browser CSRF gate, so they exercise the handler directly).
func jsonBearer(h http.Handler, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestStepUpRoutesRequireAccessTokenLive(t *testing.T) {
	h, _ := newStepUpHandler(t)
	for _, path := range []string{"/auth/step-up/begin", "/auth/step-up/password", "/auth/step-up/code"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status = %d, want 401", path, rec.Code)
		}
	}
}

func TestStepUpPasswordEarnsGrantOverBearer(t *testing.T) {
	h, grants := newStepUpHandler(t)
	token := registerAndBearer(t, h, "stepup@example.com")

	rec := jsonBearer(h, "/auth/step-up/password", token,
		`{"purpose":"`+authgrant.PurposeSetPassword+`","context":"","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("step-up status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if len(grants.m) != 1 {
		t.Fatalf("grants persisted = %d, want 1", len(grants.m))
	}
}

func TestStepUpWrongPasswordGeneric(t *testing.T) {
	h, grants := newStepUpHandler(t)
	token := registerAndBearer(t, h, "badpw@example.com")

	rec := jsonBearer(h, "/auth/step-up/password", token,
		`{"purpose":"`+authgrant.PurposeSetPassword+`","context":"","password":"wrong-password"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password status = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(grants.m) != 0 {
		t.Fatalf("a rejected step-up minted a grant: %d", len(grants.m))
	}
}

func TestStepUpCookieMutationRequiresCSRF(t *testing.T) {
	h, _ := newStepUpHandler(t)
	// Log in over the cookie lane to obtain a live session cookie.
	do(t, h, "POST", "/auth/register", `{"email":"csrf@example.com","password":"password123456789","display_name":"S"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"csrf@example.com","password":"password123456789"}`)
	cookie := sessionCookie(rec)
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	// A cookie-authenticated mutation with no CSRF double-submit token is rejected
	// by the browser-safe gate before the handler runs.
	r := httptest.NewRequest("POST", "/auth/step-up/password", strings.NewReader(`{"purpose":"set_password","password":"password123456789"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, r)
	if got.Code != http.StatusForbidden {
		t.Fatalf("cookie mutation without CSRF status = %d, want 403; body=%s", got.Code, got.Body)
	}
}
