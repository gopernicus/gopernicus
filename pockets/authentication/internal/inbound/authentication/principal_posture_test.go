package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The bundled-route posture matrix. Mount resolves each semantic route group to
// ONE authenticator — the host's override where Config.BundledRouteAuth named one,
// the audited default otherwise — so this file pins both halves: what each default
// admits and refuses, and that overriding one slot changes that surface and
// nothing else.

// --- the user-administration repository (the only port the fixture still lacked) ---

// memUserAdmin is an inert operator directory: the posture cases assert who
// REACHES Config.UserAdminCheck, never what the directory holds.
type memUserAdmin struct{}

func (memUserAdmin) List(context.Context, crud.ListRequest) (crud.Page[user.Summary], error) {
	return crud.Page[user.Summary]{Items: []user.Summary{}}, nil
}
func (memUserAdmin) GetSummary(context.Context, string) (user.Summary, error) {
	return user.Summary{}, nil
}
func (memUserAdmin) SetStatus(context.Context, string, user.Status, time.Time) (user.StatusChange, error) {
	return user.StatusChange{}, nil
}

// --- fixture ---

// postureFixture mounts the WHOLE bundled surface — machine identity, user
// administration, invitations, OAuth and the HTML account pages — over one real
// authsvc.Service, so a posture case can compare route groups against each other
// on the same credential.
type postureFixture struct {
	h         http.Handler
	svc       *authsvc.Service
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	inv       *spyInvitationService

	mu sync.Mutex
	// adminChecks records every principal that reached Config.UserAdminCheck.
	adminChecks []authsvc.UserAdminCheckRequest
	// machineGateRuns counts how often the host's machine gate ran, so a case can
	// prove the authenticator refused BEFORE the host policy was consulted.
	machineGateRuns int
}

func newPostureFixture(t *testing.T, routeAuth RouteAuthentication) *postureFixture {
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
	f := &postureFixture{users: users, idents: idents, passwords: passwords, inv: &spyInvitationService{}}
	f.svc = authsvc.NewService(authsvc.Deps{
		Users:               users,
		Identifiers:         idents,
		Passwords:           passwords,
		Sessions:            sessions,
		Challenges:          challenges,
		Protector:           memProtector{},
		PasswordResets:      &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		CredentialMutations: &memCredentialMutations{users: users, idents: idents, passwords: passwords},
		Hasher:              fakeHasher{},
		Deliver:             router,
		Queue:               stubQueue{},
		Limiter:             ratelimiter.NewMemory(),
		Cookie:              authsvc.CookieConfig{},
		TokenSigner:         newFakeSigner(),

		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},

		UserAdmin:      memUserAdmin{},
		UserAdminCheck: f.recordAdminCheck,

		OAuthAccounts:     &memOAuthAccounts{},
		OAuthStates:       &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:         []oauth.Provider{stubProvider{}},
		OAuthCallbackBase: "https://app.example.com",
		PublicAuthBaseURL: "https://auth.example.com",
	})
	h := web.NewWebHandler()
	Mount(h, Deps{
		Auth:         f.svc,
		Invitations:  f.inv,
		ListStrategy: crud.StrategyCursor,
		MachineGate:  f.countingMachineGate,
		Views:        stubViews{},
		RouteAuth:    routeAuth,
	})
	f.h = h
	return f
}

// recordAdminCheck is the host user-administration policy: it records the
// principal the pocket resolved and authorizes it, so a case asserts WHO reached
// the seam rather than what the host decided.
func (f *postureFixture) recordAdminCheck(_ context.Context, req authsvc.UserAdminCheckRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adminChecks = append(f.adminChecks, req)
	return nil
}

// countingMachineGate is the host's machine-identity policy: allow-all, counted.
func (f *postureFixture) countingMachineGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.machineGateRuns++
		f.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (f *postureFixture) checks() []authsvc.UserAdminCheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]authsvc.UserAdminCheckRequest(nil), f.adminChecks...)
}

func (f *postureFixture) gateRuns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.machineGateRuns
}

// seedAccount inserts a user with a password and a verified primary email so the
// cookie lane resolves it.
func (f *postureFixture) seedAccount(userID, emailAddr string) {
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "Seed"}
	f.users.mu.Unlock()
	f.passwords.mu.Lock()
	f.passwords.m[userID] = "hash:password123456789"
	f.passwords.mu.Unlock()
	f.idents.insert(verifiedEmail("id-"+userID, userID, emailAddr))
}

func (f *postureFixture) login(t *testing.T, emailAddr string) *http.Cookie {
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

// key mints a key on a fresh service account, bypassing the gated lifecycle
// routes: the point of every case is what the KEY may do afterwards.
func (f *postureFixture) key(t *testing.T, name string, actAsUser bool, ownerUserID string) (raw, serviceAccountID string) {
	t.Helper()
	ctx := context.Background()
	sa, err := f.svc.CreateServiceAccount(ctx, "admin", name, "", actAsUser, ownerUserID)
	if err != nil {
		t.Fatalf("CreateServiceAccount(%s): %v", name, err)
	}
	_, raw, err = f.svc.MintAPIKey(ctx, sa.ID, name, time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey(%s): %v", name, err)
	}
	return raw, sa.ID
}

// request drives one probe with an optional bearer credential and cookie.
func (f *postureFixture) request(method, path, body, bearerToken string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if bearerToken != "" {
		r.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

// --- the audited defaults ---

// TestDefaultUserAdministrationAdmitsAMachinePrincipal pins the audit's
// deliberate exception: user administration keeps its documented contract that a
// machine principal REACHES Config.UserAdminCheck, and the host — not the pocket
// — decides whether a service account may administer users.
func TestDefaultUserAdministrationAdmitsAMachinePrincipal(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	rawKey, saID := f.key(t, "bot", false, "")

	rec := f.request("GET", "/auth/admin/users", "", rawKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("service-account key on /auth/admin/users = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	checks := f.checks()
	if len(checks) != 1 {
		t.Fatalf("host checks = %d, want 1", len(checks))
	}
	if checks[0].Principal.Type != authsvc.PrincipalServiceAccount || checks[0].Principal.ID != saID {
		t.Errorf("host check principal = %+v, want the service account %s", checks[0].Principal, saID)
	}
}

// TestDefaultSessionHydrationAndInvitationsPreserveActAsUserKeys pins the other
// two API-key-capable groups: an act-as-user key acts as its human owner, while a
// SELF-acting service account is refused by the handlers' CurrentUser requirement.
func TestDefaultSessionHydrationAndInvitationsPreserveActAsUserKeys(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	actKey, _ := f.key(t, "personal", true, "u1")
	selfKey, _ := f.key(t, "bot", false, "")

	me := f.request("GET", "/auth/me", "", actKey, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("act-as-user key on /auth/me = %d, want 200; body=%s", me.Code, me.Body)
	}
	var hydrated userResponse
	if err := json.Unmarshal(me.Body.Bytes(), &hydrated); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	if hydrated.ID != "u1" {
		t.Errorf("hydrated id = %q, want the key's human owner u1", hydrated.ID)
	}
	if rec := f.request("GET", "/auth/me", "", selfKey, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("self-acting key on /auth/me = %d, want 401", rec.Code)
	}

	mine := f.request("GET", "/auth/invitations/mine", "", actKey, nil)
	if mine.Code != http.StatusOK {
		t.Fatalf("act-as-user key on /auth/invitations/mine = %d, want 200; body=%s", mine.Code, mine.Body)
	}
	if !f.inv.mineCalled {
		t.Error("the act-as-user key never reached the invitation service")
	}
	if rec := f.request("GET", "/auth/invitations/mine", "", selfKey, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("self-acting key on /auth/invitations/mine = %d, want 401", rec.Code)
	}
}

// TestDefaultSessionAndCredentialSurfacesRefuseEveryAPIKey pins the tightening
// the audit chose: the established-session reads and every human-credential
// mutation refuse a key AT THE AUTHENTICATOR — an act-as-user key included, so
// "acts as a person" never becomes "is a person's session".
func TestDefaultSessionAndCredentialSurfacesRefuseEveryAPIKey(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	actKey, _ := f.key(t, "personal", true, "u1")
	selfKey, _ := f.key(t, "bot", false, "")

	sessionSecurityReads := []struct{ method, path string }{
		{"GET", "/auth/delivery/status"},
		{"GET", "/auth/methods"},
		{"GET", "/auth/csrf"},
	}
	credentialMutations := []struct{ method, path, body string }{
		{"POST", "/auth/password/change", `{}`},
		{"POST", "/auth/step-up/begin", `{}`},
		{"POST", "/auth/identifiers/email", `{}`},
		{"PATCH", "/auth/identifiers/id-1", `{}`},
		{"POST", "/auth/oauth/google/unlink/start", `{}`},
	}
	for _, k := range []struct {
		name string
		raw  string
	}{{"act-as-user key", actKey}, {"self-acting key", selfKey}} {
		for _, p := range sessionSecurityReads {
			// The receipt is deliberately present: without the authenticator's refusal
			// the handler would answer its own status, never 401.
			if rec := f.request(p.method, p.path+"?receipt=r1", "", k.raw, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s %s = %d, want 401; body=%s", k.name, p.method, p.path, rec.Code, rec.Body)
			}
		}
		for _, p := range credentialMutations {
			if rec := f.request(p.method, p.path, p.body, k.raw, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s %s = %d, want 401; body=%s", k.name, p.method, p.path, rec.Code, rec.Body)
			}
		}
	}
}

// TestDefaultMachineLifecycleRefusesAKeyBesideAValidCookie pins "a key never
// mints a key": the header is authoritative, so presenting a key ALONGSIDE a live
// session cookie is refused rather than quietly falling back to the cookie — and
// the host's gate never runs for a refused credential.
func TestDefaultMachineLifecycleRefusesAKeyBesideAValidCookie(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")
	actKey, _ := f.key(t, "personal", true, "u1")

	// The cookie alone reaches the host gate.
	if rec := f.request("GET", "/auth/service-accounts", "", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("live session on /auth/service-accounts = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	before := f.gateRuns()

	for _, p := range machineLifecycleRoutes {
		if rec := f.request(p.method, p.path, "", actKey, cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("api key beside a valid cookie on %s %s = %d, want 401; body=%s", p.method, p.path, rec.Code, rec.Body)
		}
	}
	if runs := f.gateRuns() - before; runs != 0 {
		t.Errorf("the host machine gate ran %d times for a refused credential, want 0", runs)
	}
}

// TestDefaultBrowserAccountReadsOnlyItsCookie pins the bundled browser UI's
// posture: it never consults a header, it requires a live session, and on denial
// it 303s to the login path — carrying a validated return_to on a GET and the
// same fixed HTML security headers every rendered page carries, so the redirect
// is neither cached nor a referrer leak.
func TestDefaultBrowserAccountReadsOnlyItsCookie(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	// A live cookie renders the page.
	if rec := f.request("GET", "/auth/account", "", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("live cookie on /auth/account = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	// A bearer access token is a header credential the surface never reads, so the
	// page redirects exactly as it would with no credential at all.
	token := f.bearerAccessToken(t, "alice@example.com")
	bearerRec := f.request("GET", "/auth/account", "", token, nil)
	if bearerRec.Code != http.StatusSeeOther {
		t.Fatalf("bearer access token on /auth/account = %d, want 303 (headers are never read)", bearerRec.Code)
	}

	// The denial redirect keeps path AND query in a validated return_to.
	denied := f.request("GET", "/auth/account?tab=security", "", "", nil)
	if denied.Code != http.StatusSeeOther {
		t.Fatalf("no credential on /auth/account = %d, want 303", denied.Code)
	}
	if loc := denied.Header().Get("Location"); loc != "/auth/login?return_to=%2Fauth%2Faccount%3Ftab%3Dsecurity" {
		t.Errorf("Location = %q, want the validated path?query return_to", loc)
	}
	for header, want := range map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := denied.Header().Get(header); got != want {
			t.Errorf("denial %s = %q, want %q", header, got, want)
		}
	}
	if denied.Header().Get("Content-Security-Policy") == "" {
		t.Error("denial carried no Content-Security-Policy")
	}

	// The form-only identifier edit POST redirects too — without a return_to, so a
	// later GET can never replay the mutation.
	post := f.request("POST", "/auth/identifiers/id-u1", "", "", nil)
	if post.Code != http.StatusSeeOther {
		t.Fatalf("no credential on POST /auth/identifiers/{id} = %d, want 303", post.Code)
	}
	if loc := post.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("POST denial Location = %q, want the bare login path", loc)
	}
}

// bearerAccessToken mints a session-backed access JWT over POST /auth/token.
func (f *postureFixture) bearerAccessToken(t *testing.T, emailAddr string) string {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/token", `{"email":"`+emailAddr+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d; body=%s", rec.Code, rec.Body)
	}
	var minted tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode token body: %v", err)
	}
	return minted.AccessToken
}

// --- overrides: one slot at a time ---

// slotProbe is one route that is representative of a bundled group. Under every
// audited default a live session cookie is ADMITTED on all eight (the status then
// depends on the handler and, for the mutations, on the browser-safe gate — never
// 401), so a single override is visible as the one probe that turns 401.
type slotProbe struct {
	slot   string
	method string
	path   string
	body   string
	// set installs an override into the slot this probe covers.
	set func(*RouteAuthentication, web.Middleware)
}

func slotProbes() []slotProbe {
	return []slotProbe{
		{"OAuthLinkStart", "GET", "/auth/oauth/google/link/start", "", func(a *RouteAuthentication, mw web.Middleware) { a.OAuthLinkStart = mw }},
		{"SessionSecurityReads", "GET", "/auth/methods", "", func(a *RouteAuthentication, mw web.Middleware) { a.SessionSecurityReads = mw }},
		{"SessionHydration", "GET", "/auth/me", "", func(a *RouteAuthentication, mw web.Middleware) { a.SessionHydration = mw }},
		{"CredentialManagement", "POST", "/auth/step-up/begin", `{}`, func(a *RouteAuthentication, mw web.Middleware) { a.CredentialManagement = mw }},
		{"MachineLifecycle", "GET", "/auth/service-accounts", "", func(a *RouteAuthentication, mw web.Middleware) { a.MachineLifecycle = mw }},
		{"UserAdministration", "GET", "/auth/admin/users", "", func(a *RouteAuthentication, mw web.Middleware) { a.UserAdministration = mw }},
		{"Invitations", "GET", "/auth/invitations/mine", "", func(a *RouteAuthentication, mw web.Middleware) { a.Invitations = mw }},
		{"BrowserAccount", "GET", "/auth/account", "", func(a *RouteAuthentication, mw web.Middleware) { a.BrowserAccount = mw }},
	}
}

// TestBundledRouteDefaultsAdmitALiveSession is the control the override matrix
// reads against: with every slot unset, one live session cookie is admitted by
// all eight authenticators.
func TestBundledRouteDefaultsAdmitALiveSession(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")
	for _, p := range slotProbes() {
		if rec := f.request(p.method, p.path, p.body, "", cookie); rec.Code == http.StatusUnauthorized {
			t.Errorf("%s default refused a live session on %s %s; body=%s", p.slot, p.method, p.path, rec.Body)
		}
	}
}

// TestBundledRouteOverridesAreIsolated configures each slot in isolation with a
// machines-only authenticator and proves the substitution is exactly one surface
// wide: the overridden group refuses the live session cookie every default
// admits, and all SEVEN unconfigured groups keep their audited defaults.
func TestBundledRouteOverridesAreIsolated(t *testing.T) {
	probes := slotProbes()
	for _, target := range probes {
		t.Run(target.slot, func(t *testing.T) {
			// The override must be built over the fixture's own Service, so it is
			// installed after construction through a two-step build.
			f := newPostureFixture(t, RouteAuthentication{})
			var routeAuth RouteAuthentication
			target.set(&routeAuth, f.svc.RequireAPIKey())
			f = newPostureFixtureWith(t, f, routeAuth)

			f.seedAccount("u1", "alice@example.com")
			cookie := f.login(t, "alice@example.com")
			for _, p := range probes {
				rec := f.request(p.method, p.path, p.body, "", cookie)
				if p.slot == target.slot {
					if rec.Code != http.StatusUnauthorized {
						t.Errorf("overridden %s: %s %s = %d, want 401; body=%s", p.slot, p.method, p.path, rec.Code, rec.Body)
					}
					continue
				}
				if rec.Code == http.StatusUnauthorized {
					t.Errorf("overriding %s also changed %s: %s %s = 401; body=%s", target.slot, p.slot, p.method, p.path, rec.Body)
				}
			}
		})
	}
}

// newPostureFixtureWith remounts a fixture's Service behind routeAuth. Middleware
// is built FROM the service, so an override can only be named once the service
// exists — the same ordering authentication.NewService performs internally when
// it freezes Config.BundledRouteAuth.
func newPostureFixtureWith(t *testing.T, base *postureFixture, routeAuth RouteAuthentication) *postureFixture {
	t.Helper()
	h := web.NewWebHandler()
	Mount(h, Deps{
		Auth:         base.svc,
		Invitations:  base.inv,
		ListStrategy: crud.StrategyCursor,
		MachineGate:  base.countingMachineGate,
		Views:        stubViews{},
		RouteAuth:    routeAuth,
	})
	base.h = h
	return base
}
