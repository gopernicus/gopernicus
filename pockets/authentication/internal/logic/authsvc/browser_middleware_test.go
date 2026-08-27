package authsvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Browser identity-gate tests (#12). RequirePrincipalBrowser / RequireLiveSessionBrowser
// resolve the SAME credentials and stash the SAME context as their JSON siblings, but
// on denial they 303 to the configured login path instead of writing a JSON 401 — a
// GET/HEAD carrying a validated return_to, an unsafe method none. The existing JSON
// middleware tests (machine_test.go / refresh_test.go) prove the 401 siblings are
// unchanged.

// browserHarness wires the full user rail plus the machine rail and exposes the signer
// so a test can craft an expired access JWT. The default browser login path
// ("/auth/login") is used (Deps.BrowserLoginPath unset).
type browserHarness struct {
	svc    *Service
	sess   *fakeSessions
	signer *fakeSigner
	sas    *fakeServiceAccounts
	keys   *fakeAPIKeys
}

func newBrowserHarness(t *testing.T) *browserHarness {
	t.Helper()
	users := newFakeUsers()
	sess := newFakeSessions()
	signer := newFakeSigner()
	sas := newFakeServiceAccounts()
	keys := newFakeAPIKeys()
	svc := NewService(Deps{
		Users:           users,
		Identifiers:     newFakeIdentifiers(users),
		Passwords:       newFakePasswords(),
		Sessions:        sess,
		Challenges:      newFakeChallenges(),
		Protector:       newFakeProtector("k1", "k1"),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          CookieConfig{},
		ServiceAccounts: sas,
		APIKeys:         keys,
		TokenSigner:     signer,
	})
	wireSyncDelivery(t, svc, &recordingMailer{}, nil)
	return &browserHarness{svc: svc, sess: sess, signer: signer, sas: sas, keys: keys}
}

// loginPair registers + logs in a user through the full rail and returns the pair.
func (bh *browserHarness) loginPair(t *testing.T, email, password string) TokenPair {
	t.Helper()
	if _, err := bh.svc.Register(context.Background(), email, password, "Test"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, _, err := bh.svc.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return pair
}

func (bh *browserHarness) oneSessionID(t *testing.T) string {
	t.Helper()
	bh.sess.mu.Lock()
	defer bh.sess.mu.Unlock()
	for id := range bh.sess.m {
		return id
	}
	t.Fatal("no session in store")
	return ""
}

// noContent is the downstream handler; a redirect means it was never reached.
func noContent() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

// assertLoginRedirect asserts a 303 to the login path with the exact expected query.
func assertLoginRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantLocation string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (login redirect)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != wantLocation {
		t.Fatalf("Location = %q, want %q", loc, wantLocation)
	}
}

// --- denial: missing / malformed / expired ---

func TestBrowserGatesRedirectMissingCredential(t *testing.T) {
	bh := newBrowserHarness(t)
	for _, gate := range []struct {
		name string
		mw   func(http.Handler) http.Handler
	}{
		{"principal", bh.svc.RequirePrincipalBrowser},
		{"live-session", bh.svc.RequireLiveSessionBrowser},
	} {
		rec := httptest.NewRecorder()
		gate.mw(noContent()).ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
		if rec.Code == http.StatusNoContent {
			t.Fatalf("%s gate admitted a request with no credential", gate.name)
		}
		assertLoginRedirect(t, rec, "/auth/login?return_to=%2Fadmin")
	}
}

func TestBrowserGatesRedirectMalformedBearer(t *testing.T) {
	bh := newBrowserHarness(t)
	for _, gate := range []func(http.Handler) http.Handler{
		bh.svc.RequirePrincipalBrowser, bh.svc.RequireLiveSessionBrowser,
	} {
		req := httptest.NewRequest("GET", "/admin", nil)
		req.Header.Set("Authorization", "Bearer aaa.bbb.ccc") // JWT-shaped garbage
		rec := httptest.NewRecorder()
		gate(noContent()).ServeHTTP(rec, req)
		assertLoginRedirect(t, rec, "/auth/login?return_to=%2Fadmin")
	}
}

func TestBrowserGatesRedirectExpiredJWT(t *testing.T) {
	bh := newBrowserHarness(t)
	expired, err := bh.signer.Sign(map[string]any{tokenClaimUserID: "u1", tokenClaimSessionID: "s1"}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	for _, gate := range []func(http.Handler) http.Handler{
		bh.svc.RequirePrincipalBrowser, bh.svc.RequireLiveSessionBrowser,
	} {
		req := httptest.NewRequest("GET", "/admin", nil)
		req.AddCookie(&http.Cookie{Name: bh.svc.SessionCookieName(), Value: expired})
		rec := httptest.NewRecorder()
		gate(noContent()).ServeHTTP(rec, req)
		assertLoginRedirect(t, rec, "/auth/login?return_to=%2Fadmin")
	}
}

// --- revoked session: passes principal, redirects live-session ---

func TestBrowserRevokedSessionPassesPrincipalRedirectsLiveSession(t *testing.T) {
	bh := newBrowserHarness(t)
	pair := bh.loginPair(t, "rev@example.com", "password123456789")
	sessionID := bh.oneSessionID(t)
	if err := bh.sess.Delete(context.Background(), sessionID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// The stateless principal gate still admits the unexpired JWT.
	reqP := httptest.NewRequest("GET", "/admin", nil)
	reqP.AddCookie(&http.Cookie{Name: bh.svc.SessionCookieName(), Value: pair.AccessToken})
	recP := httptest.NewRecorder()
	bh.svc.RequirePrincipalBrowser(noContent()).ServeHTTP(recP, reqP)
	if recP.Code != http.StatusNoContent {
		t.Fatalf("RequirePrincipalBrowser on revoked session = %d, want 204 (stateless pass)", recP.Code)
	}

	// The live-session gate denies immediately and redirects.
	reqL := httptest.NewRequest("GET", "/admin", nil)
	reqL.AddCookie(&http.Cookie{Name: bh.svc.SessionCookieName(), Value: pair.AccessToken})
	recL := httptest.NewRecorder()
	bh.svc.RequireLiveSessionBrowser(noContent()).ServeHTTP(recL, reqL)
	assertLoginRedirect(t, recL, "/auth/login?return_to=%2Fadmin")
}

// --- valid principals preserve the context stash (parity with the JSON gates) ---

func TestBrowserGatesPreserveContextStash(t *testing.T) {
	bh := newBrowserHarness(t)
	pair := bh.loginPair(t, "ctx@example.com", "password123456789")
	sessionID := bh.oneSessionID(t)

	var gotP identity.Principal
	var gotSession string
	var gotSessionOK bool
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotP, _ = identity.FromContext(r.Context())
		gotSession, gotSessionOK = bh.svc.CurrentSessionID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	// User session cookie through RequirePrincipalBrowser: user principal, no session id.
	reqCookie := httptest.NewRequest("GET", "/admin", nil)
	reqCookie.AddCookie(&http.Cookie{Name: bh.svc.SessionCookieName(), Value: pair.AccessToken})
	recCookie := httptest.NewRecorder()
	bh.svc.RequirePrincipalBrowser(capture).ServeHTTP(recCookie, reqCookie)
	if recCookie.Code != http.StatusNoContent || gotP.Type != PrincipalUser {
		t.Fatalf("principal (cookie) = %+v status=%d, want a user principal 204", gotP, recCookie.Code)
	}

	// Bearer JWT through RequireLiveSessionBrowser: user principal AND the live session id.
	reqBearer := httptest.NewRequest("GET", "/admin", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	recBearer := httptest.NewRecorder()
	bh.svc.RequireLiveSessionBrowser(capture).ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusNoContent || gotP.Type != PrincipalUser {
		t.Fatalf("principal (bearer) = %+v status=%d, want a user principal 204", gotP, recBearer.Code)
	}
	if !gotSessionOK || gotSession != sessionID {
		t.Fatalf("CurrentSessionID = (%q, %v), want the live session id %q", gotSession, gotSessionOK, sessionID)
	}

	// API key through both gates: service-account principal, and NO session id on the
	// live-session path (a machine caller has no session row).
	sa, err := bh.svc.CreateServiceAccount(context.Background(), "admin", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, rawKey, err := bh.svc.MintAPIKey(context.Background(), sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	reqKey := httptest.NewRequest("GET", "/admin", nil)
	reqKey.Header.Set("Authorization", "Bearer "+rawKey)
	recKey := httptest.NewRecorder()
	bh.svc.RequireLiveSessionBrowser(capture).ServeHTTP(recKey, reqKey)
	if recKey.Code != http.StatusNoContent || gotP.Type != PrincipalServiceAccount || gotP.ID != sa.ID {
		t.Fatalf("api-key principal = %+v status=%d, want {service_account, %s} 204", gotP, recKey.Code, sa.ID)
	}
	if gotSessionOK {
		t.Fatalf("CurrentSessionID reported present for a machine caller: %q", gotSession)
	}
}

// --- return_to method policy ---

func TestBrowserGateReturnToMethodPolicy(t *testing.T) {
	bh := newBrowserHarness(t)
	cases := []struct {
		method string
		want   string
	}{
		{"GET", "/auth/login?return_to=%2Fadmin%2Fusers%3Fpage%3D2"},
		{"HEAD", "/auth/login?return_to=%2Fadmin%2Fusers%3Fpage%3D2"},
		{"POST", "/auth/login"},
		{"PUT", "/auth/login"},
		{"DELETE", "/auth/login"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/admin/users?page=2", nil)
		rec := httptest.NewRecorder()
		bh.svc.RequirePrincipalBrowser(noContent()).ServeHTTP(rec, req)
		assertLoginRedirect(t, rec, c.want)
	}
}

// --- open-redirect return_to candidates are never reflected ---

func TestBrowserGateOmitsUnsafeReturnTo(t *testing.T) {
	bh := newBrowserHarness(t)
	// Each target is an open-redirect vector after normalization; none may seed a
	// return_to, so the login redirect must carry no query.
	for _, target := range []string{"//evil.com", "/\\evil.com", "/a\x00b", "/nav\r\nSet-Cookie:x"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.URL.Path = target
		req.URL.RawQuery = ""
		rec := httptest.NewRecorder()
		bh.svc.RequirePrincipalBrowser(noContent()).ServeHTTP(rec, req)
		assertLoginRedirect(t, rec, "/auth/login")
	}
}
