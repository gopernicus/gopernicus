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

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The bundled-route posture matrix. Mount resolves each semantic route group to
// ONE authenticator — the host's override where WithRouteAuthentication named one,
// the audited default otherwise — so this file pins both halves: what each default
// admits and refuses, and that overriding one slot changes that surface and
// nothing else.

// --- the user-administration repository (the only port the fixture still lacked) ---

// memUserAdmin is an inert operator directory: the posture cases assert who
// REACHES WithUserAdminCheck, never what the directory holds.
type memUserAdmin struct{}

func (memUserAdmin) List(context.Context, list.Request) (list.Page[user.Summary], error) {
	return list.Page[user.Summary]{Items: []user.Summary{}}, nil
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
// authlogic.Service, so a posture case can compare route groups against each other
// on the same credential.
type postureFixture struct {
	h         http.Handler
	svc       *testService
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	inv       *spyInvitationService
	events    *spySecurityEvents
	// extra adjusts the mount beyond the shared surface (an eraser, the
	// security-event listing, passkeys, TOTP), on every remount too.
	extra []func(*mountDeps)

	mu sync.Mutex
	// adminChecks records every principal that reached WithUserAdminCheck.
	adminChecks []UserAdminCheckRequest
	// machineGateRuns counts how often the host's machine gate ran, so a case can
	// prove the authenticator refused BEFORE the host policy was consulted.
	machineGateRuns int
}

func newPostureFixture(t *testing.T, routeAuth RouteAuthentication, extra ...func(*mountDeps)) *postureFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(nopMailer{},
		delivery.WithMailFrom("noreply@example.com"))
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	f := &postureFixture{users: users, idents: idents, passwords: passwords, inv: &spyInvitationService{}, events: newSpySecurityEvents(), extra: extra}
	f.svc = newServiceWithFakes(authenticationFixture{
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
		TokenSigner:         newFakeSigner(),

		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},

		UserAdmin:      memUserAdmin{},
		SecurityEvents: f.events,

		OAuthAccounts:     &memOAuthAccounts{},
		OAuthStates:       &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:         []oauth.Provider{stubProvider{}},
		OAuthCallbackBase: "https://app.example.com",
		PublicAuthBaseURL: "https://auth.example.com",
	})
	deps := mountDeps{
		Auth:        f.svc,
		Invitations: f.inv, InvitationSync: f.svc,
		InviteCheck: allowInviteCheck, UserAdminCheck: f.recordAdminCheck,
		ListStrategy: list.StrategyCursor,
		MachineGate:  f.countingMachineGate,
		Views:        stubViews{},
		RouteAuth:    routeAuth,
	}
	for _, adjust := range f.extra {
		adjust(&deps)
	}
	h := web.NewWebHandler()
	mount(h, deps)
	f.h = h
	return f
}

// recordAdminCheck is the host user-administration policy: it records the
// principal the pocket resolved and authorizes it, so a case asserts WHO reached
// the seam rather than what the host decided.
func (f *postureFixture) recordAdminCheck(_ context.Context, req UserAdminCheckRequest) error {
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

func (f *postureFixture) checks() []UserAdminCheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]UserAdminCheckRequest(nil), f.adminChecks...)
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

// adminPasskeys and adminTOTP are the passkey and TOTP capabilities reduced to
// what mounts the two admin reset routes; any other method is never reached by
// the posture cases.
type adminPasskeys struct{ passkeyService }

func (adminPasskeys) PasskeysEnabled() bool { return true }
func (adminPasskeys) ResetUserPasskeys(context.Context, authlogic.Principal, string) (int, error) {
	return 0, nil
}

type adminTOTP struct{ totpService }

func (adminTOTP) TOTPEnabled() bool { return true }
func (adminTOTP) ResetUserTOTP(context.Context, authlogic.Principal, string) (bool, error) {
	return false, nil
}

// withFullUserAdministration mounts every optional UserAdministration route:
// erase, the security-event listing and the passkey and TOTP resets.
func withFullUserAdministration(eraser *fakeEraser, reader *fakeSecurityEventReader) func(*mountDeps) {
	return func(d *mountDeps) {
		d.Eraser = eraser
		d.SecurityEvents = reader
		d.Passkeys = adminPasskeys{}
		d.TOTP = adminTOTP{}
	}
}

// userAdministrationRoutes is every route the UserAdministration group gates.
var userAdministrationRoutes = []struct{ method, path, body string }{
	{"GET", "/auth/admin/users", ""},
	{"GET", "/auth/admin/users/u2", ""},
	{"POST", "/auth/admin/users/u2/deactivate", `{}`},
	{"POST", "/auth/admin/users/u2/reactivate", `{}`},
	{"POST", "/auth/admin/users/u2/verification/resend", `{}`},
	{"POST", "/auth/admin/users/u2/erase", `{}`},
	{"GET", "/auth/admin/security-events", ""},
	{"POST", "/auth/admin/users/u2/passkeys/reset", `{}`},
	{"POST", "/auth/admin/users/u2/totp/reset", `{}`},
}

// invitationRoutes is every authenticated route the Invitations group gates.
var invitationRoutes = []struct{ method, path, body string }{
	{"POST", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member"}`},
	{"GET", "/auth/invitations/project/p1", ""},
	{"GET", "/auth/invitations/mine", ""},
	{"POST", "/auth/invitations/accept", `{"token":"t"}`},
	{"POST", "/auth/invitations/sync", `{}`},
	{"POST", "/auth/invitations/inv-1/cancel", ""},
	{"POST", "/auth/invitations/inv-1/resend", ""},
}

// TestDefaultUserAdministrationRefusesActAsUserKeys pins the v0.39.0 default:
// an act-as-user key — whose principal is its human owner — is refused at the
// authenticator on every UserAdministration route before the host check runs,
// while a SELF-ACTING service account still reaches WithUserAdminCheck as
// itself and the host decides.
func TestDefaultUserAdministrationRefusesActAsUserKeys(t *testing.T) {
	eraser := &fakeEraser{}
	f := newPostureFixture(t, RouteAuthentication{}, withFullUserAdministration(eraser, &fakeSecurityEventReader{}))
	f.seedAccount("u1", "alice@example.com")
	actKey, _ := f.key(t, "personal", true, "u1")
	f.events.reset()

	for _, p := range userAdministrationRoutes {
		if rec := f.request(p.method, p.path, p.body, actKey, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("act-as-user key on %s %s = %d, want 401; body=%s", p.method, p.path, rec.Code, rec.Body)
		}
	}
	if got := f.refusalRows("UserAdministration"); got != len(userAdministrationRoutes) {
		t.Errorf("UserAdministration refusal rows = %d, want one per route (%d): the 401s must come from the authenticator", got, len(userAdministrationRoutes))
	}
	if checks := f.checks(); len(checks) != 0 {
		t.Errorf("host checks = %d for a refused key, want 0: %+v", len(checks), checks)
	}
	if len(eraser.calls) != 0 {
		t.Errorf("eraser calls = %v, want none", eraser.calls)
	}

	selfKey, saID := f.key(t, "bot", false, "")
	rec := f.request("GET", "/auth/admin/users", "", selfKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("self-acting key on /auth/admin/users = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	checks := f.checks()
	if len(checks) != 1 {
		t.Fatalf("host checks = %d, want 1", len(checks))
	}
	if checks[0].Principal.Type != authlogic.PrincipalServiceAccount || checks[0].Principal.ID != saID {
		t.Errorf("host check principal = %+v, want the service account %s", checks[0].Principal, saID)
	}
}

// TestDefaultSessionHydrationPreservesActAsUserKeys pins /auth/me, which keeps
// its default: an act-as-user key hydrates as its human owner, while a
// SELF-acting service account is refused by the handler's CurrentUser
// requirement.
func TestDefaultSessionHydrationPreservesActAsUserKeys(t *testing.T) {
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
}

// TestDefaultInvitationsRefuseActAsUserKeys pins the invitation surface: an
// act-as-user key is refused at the authenticator on all seven authenticated
// routes, so neither the invitation service nor the host's invite check is
// consulted; a self-acting key still fails the handlers' CurrentUser
// requirement, so no API key reaches an invitation handler by default.
func TestDefaultInvitationsRefuseActAsUserKeys(t *testing.T) {
	var inviteChecks int
	f := newPostureFixture(t, RouteAuthentication{}, func(d *mountDeps) {
		d.InviteCheck = func(context.Context, InviteCheckRequest) error {
			inviteChecks++
			return nil
		}
	})
	f.seedAccount("u1", "alice@example.com")
	actKey, _ := f.key(t, "personal", true, "u1")
	selfKey, _ := f.key(t, "bot", false, "")
	f.events.reset()

	for _, k := range []struct{ name, raw string }{{"act-as-user key", actKey}, {"self-acting key", selfKey}} {
		for _, p := range invitationRoutes {
			if rec := f.request(p.method, p.path, p.body, k.raw, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s %s = %d, want 401; body=%s", k.name, p.method, p.path, rec.Code, rec.Body)
			}
		}
	}
	inv := f.inv
	if inv.createCalled || inv.listCalled || inv.mineCalled || inv.acceptCalled || inv.cancelCalled || inv.resendCalled {
		t.Errorf("a refused key reached the invitation service: %+v", inv)
	}
	if inviteChecks != 0 {
		t.Errorf("invite checks = %d, want 0", inviteChecks)
	}
	if got := f.refusalRows("Invitations"); got != len(invitationRoutes) {
		t.Errorf("Invitations refusal rows = %d, want one per route (%d) for the act-as-user key alone", got, len(invitationRoutes))
	}
}

// refusalRows counts the blocked act-as-user refusal rows naming group.
func (f *postureFixture) refusalRows(group string) int {
	n := 0
	for _, e := range f.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyAuth && e.EventStatus == securityevent.StatusBlocked &&
			e.Details["reason"] == securityevent.ReasonActAsUserRefused && e.Details["group"] == group {
			n++
		}
	}
	return n
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
	if loc := denied.Header().Get("Location"); loc != "/auth/login?return_to=%2Fauth%2Faccount%3Ftab%3Dsecurity&recover=1" {
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
// audited default a live session cookie is ADMITTED on all nine (the status then
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
		{"CSRFBootstrap", "GET", "/auth/csrf", "", func(a *RouteAuthentication, mw web.Middleware) { a.CSRFBootstrap = mw }},
		{"SessionHydration", "GET", "/auth/me", "", func(a *RouteAuthentication, mw web.Middleware) { a.SessionHydration = mw }},
		{"CredentialManagement", "POST", "/auth/step-up/begin", `{}`, func(a *RouteAuthentication, mw web.Middleware) { a.CredentialManagement = mw }},
		{"MachineLifecycle", "GET", "/auth/service-accounts", "", func(a *RouteAuthentication, mw web.Middleware) { a.MachineLifecycle = mw }},
		{"UserAdministration", "GET", "/auth/admin/users", "", func(a *RouteAuthentication, mw web.Middleware) { a.UserAdministration = mw }},
		{"Invitations", "GET", "/auth/invitations/mine", "", func(a *RouteAuthentication, mw web.Middleware) { a.Invitations = mw }},
		{"BrowserAccount", "GET", "/auth/account", "", func(a *RouteAuthentication, mw web.Middleware) { a.BrowserAccount = mw }},
	}
}

// TestBundledRouteDefaultManifest pins which credential kinds each bundled
// group's DEFAULT authenticator admits, by slot name, on a host wiring every
// kind: a session (cookie), an external token, a self-acting key and an
// act-as-user key (bearers). It reads the authenticators themselves, so a
// handler's own CurrentUser refusal never masks an admission, and any future
// default change fails here by slot.
func TestBundledRouteDefaultManifest(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	selfKey, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	actKey, _, _ := h.mintKey(t, "personal", true, h.userID, time.Time{})
	auth := RouteAuthentication{}.withDefaults(h.svc, false)

	type admits struct{ session, external, selfKey, actKey bool }
	manifest := map[string]struct {
		mw   web.Middleware
		want admits
	}{
		"OAuthLinkStart":       {auth.OAuthLinkStart, admits{true, false, false, false}},
		"SessionSecurityReads": {auth.SessionSecurityReads, admits{true, false, false, false}},
		"CSRFBootstrap":        {auth.CSRFBootstrap, admits{true, true, true, true}},
		"SessionHydration":     {auth.SessionHydration, admits{true, true, true, true}},
		"CredentialManagement": {auth.CredentialManagement, admits{true, false, false, false}},
		"MachineLifecycle":     {auth.MachineLifecycle, admits{true, false, false, false}},
		"UserAdministration":   {auth.UserAdministration, admits{true, true, true, false}},
		"Invitations":          {auth.Invitations, admits{true, true, true, false}},
		"BrowserAccount":       {auth.BrowserAccount, admits{true, false, false, false}},
	}
	probes := slotProbes()
	if len(manifest) != len(probes) {
		t.Fatalf("manifest covers %d slots, slotProbes %d", len(manifest), len(probes))
	}
	for _, p := range probes {
		entry, ok := manifest[p.slot]
		if !ok {
			t.Errorf("slot %s has no manifest entry", p.slot)
			continue
		}
		for _, c := range []struct {
			name           string
			header, cookie string
			want           bool
		}{
			{"session", "", h.accessJWT, entry.want.session},
			{"external token", h.token, "", entry.want.external},
			{"self-acting key", selfKey, "", entry.want.selfKey},
			{"act-as-user key", actKey, "", entry.want.actKey},
		} {
			_, got := serveGuarded(h.credentialHarness, entry.mw, h.credentialRequest(c.header, c.cookie))
			if got.reached != c.want {
				t.Errorf("%s default with a %s: admitted = %v, want %v", p.slot, c.name, got.reached, c.want)
			}
		}
	}
}

// TestBundledRouteDefaultsAdmitALiveSession is the control the override matrix
// reads against: with every slot unset, one live session cookie is admitted by
// all nine authenticators.
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
// admits, and all EIGHT unconfigured groups keep their audited defaults.
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
// it freezes WithRouteAuthentication.
func newPostureFixtureWith(t *testing.T, base *postureFixture, routeAuth RouteAuthentication) *postureFixture {
	t.Helper()
	deps := mountDeps{
		Auth:        base.svc,
		Invitations: base.inv,
		InviteCheck: allowInviteCheck, UserAdminCheck: base.recordAdminCheck,
		ListStrategy: list.StrategyCursor,
		MachineGate:  base.countingMachineGate,
		Views:        stubViews{},
		RouteAuth:    routeAuth,
	}
	for _, adjust := range base.extra {
		adjust(&deps)
	}
	h := web.NewWebHandler()
	mount(h, deps)
	base.h = h
	return base
}
