package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// floorRoutes are a host's floor probes mounted beside the bundled routes: a
// plain RequirePrincipal() JSON route and a Browser() HTML route.
func floorRoutes(f *passkeyFixture) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	f.web.Handle("GET", "/demo/json", ok, f.svc.RequirePrincipal())
	f.web.Handle("GET", "/demo/page", ok, f.svc.RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie), Browser()))
}

// open is the enrollment window of a fixture whose user (zero CreatedAt)
// is inside its grace; closed one whose grace has passed.
func openWindow() *authlogic.MFAEnrollmentConfig {
	return &authlogic.MFAEnrollmentConfig{Start: time.Now().Add(-time.Minute), Grace: time.Hour}
}

func closedWindow() *authlogic.MFAEnrollmentConfig {
	return &authlogic.MFAEnrollmentConfig{Start: time.Now().Add(-2 * time.Hour), Grace: time.Hour}
}

// get serves a GET carrying cookie.
func (f *passkeyFixture) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

// loginPair signs the fixture user in and returns the session and refresh
// cookies.
func (f *passkeyFixture) loginPair(t *testing.T) (*http.Cookie, *http.Cookie) {
	t.Helper()
	rec := f.do(t, call{method: "POST", path: "/auth/login", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`})
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil || refreshCookie(rec) == nil {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(rec), refreshCookie(rec)
}

// refresh rotates refresh and returns the new pair.
func (f *passkeyFixture) refresh(t *testing.T, refresh *http.Cookie) (*http.Cookie, *http.Cookie) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.Header.Set("Origin", pkOrigin)
	r.AddCookie(refresh)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(rec), refreshCookie(rec)
}

// enrollmentRows counts the mfa_required_denied rows of gate enrollment with
// reason.
func (f *passkeyFixture) enrollmentRows(reason string) int {
	n := 0
	for _, evt := range f.rows(securityevent.TypeMFARequiredDenied) {
		if evt.Details["gate"] == string(authlogic.MFAGateEnrollment) && evt.Details["reason"] == reason {
			n++
		}
	}
	return n
}

// TestEnrollmentFloorInGrace proves an unenrolled AAL1 session passes every
// plain RequirePrincipal route inside its grace, /auth/me and /auth/methods
// report the window, and the explicit gates still deny: RequireMFA, the admin
// gate and the machine lifecycle.
func TestEnrollmentFloorInGrace(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true, totpViews: &captureTOTPViews{}, enrollment: openWindow(), adminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }, adminMFA: true, machine: true})
	floorRoutes(f)
	cookie := f.login(t)

	for _, path := range []string{"/demo/json", "/demo/page"} {
		if rec := f.get(path, cookie); rec.Code != http.StatusOK {
			t.Errorf("%s in grace = %d, want 200", path, rec.Code)
		}
	}
	for _, path := range []string{"/auth/me", "/auth/methods"} {
		rec := f.get(path, cookie)
		enrollment, _ := decode(t, rec)["mfa_enrollment"].(map[string]any)
		by, err := time.Parse(time.RFC3339, enrollment["enroll_by"].(string))
		if rec.Code != http.StatusOK || enrollment["required"] != true || err != nil || by.Before(time.Now()) {
			t.Errorf("%s = %d %s, want mfa_enrollment with a future enroll_by", path, rec.Code, rec.Body)
		}
	}
	gated := f.svc.RequirePrincipal(RequireMFA())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	r := httptest.NewRequest(http.MethodGet, "/demo/mfa", nil)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Errorf("RequireMFA in grace = %d %q, want the RFC 9470 401", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := f.get("/auth/admin/users", cookie); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Errorf("/auth/admin/users in grace = %d %q, want the RFC 9470 401", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/service-accounts", body: `{"name":"bot"}`, cookie: cookie, origin: pkOrigin}); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /auth/service-accounts in grace = %d %s, want 401", rec.Code, rec.Body)
	}
	if n := f.enrollmentRows(authlogic.MFADenialEnrollment); n != 0 {
		t.Errorf("in grace recorded %d enrollment denials, want none", n)
	}
}

// TestEnrollmentFloorPastTheWindow proves a session past its grace gets the
// JSON 403 mfa_enrollment_required with enroll_by and, under Browser(), the
// enrollment page redirect with return_to; that the bundled enrollment surface
// stays reachable at AAL1; and that retries record one denial per session.
func TestEnrollmentFloorPastTheWindow(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true, totpViews: &captureTOTPViews{}, enrollment: closedWindow()})
	floorRoutes(f)
	cookie := f.login(t)

	for range 3 {
		rec := f.get("/demo/json", cookie)
		body := decode(t, rec)
		if _, err := time.Parse(time.RFC3339, body["enroll_by"].(string)); rec.Code != http.StatusForbidden || body["code"] != codeMFAEnrollmentRequired || err != nil {
			t.Fatalf("JSON past the window = %d %s", rec.Code, rec.Body)
		}
	}
	rec := f.get("/demo/page?tab=1", cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/account/totp/enroll?return_to="+url.QueryEscape("/demo/page?tab=1") {
		t.Fatalf("Browser() past the window = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if n := f.enrollmentRows(authlogic.MFADenialEnrollment); n != 1 {
		t.Errorf("four refusals recorded %d enrollment denials, want 1 per session", n)
	}

	for _, path := range []string{"/auth/me", "/auth/methods", "/auth/csrf", "/auth/totp", "/auth/recovery-codes", "/auth/account/totp", "/auth/step-up/mfa"} {
		if rec := f.get(path, cookie); rec.Code != http.StatusOK {
			t.Errorf("%s past the window = %d %s, want 200 (the enrollment surface)", path, rec.Code, rec.Body)
		}
	}
	secret, _ := f.enrollTOTP(t, cookie)
	stepped := f.totpStepUp(t, cookie, secret)
	if rec := f.get("/demo/json", stepped); rec.Code != http.StatusOK {
		t.Fatalf("the AAL2 session after enrolment = %d %s", rec.Code, rec.Body)
	}
	if rec := f.get("/auth/me", stepped); strings.Contains(rec.Body.String(), "mfa_enrollment") {
		t.Errorf("/auth/me at AAL2 still reports a window: %s", rec.Body)
	}
}

// TestEnrollmentFloorBrowserTargets pins the Browser() target: the passkeys
// page when only passkey views are wired, and the JSON 403 when no enrollment
// page is.
func TestEnrollmentFloorBrowserTargets(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{views: &capturePasskeyViews{}, enrollment: closedWindow()})
	floorRoutes(f)
	if rec := f.get("/demo/page", f.login(t)); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/account/passkeys?return_to=") {
		t.Errorf("passkey views only = %d %q, want the passkeys page", rec.Code, rec.Header().Get("Location"))
	}
	f = newPasskeyFixture(t, passkeyOptions{totp: true, enrollment: closedWindow()})
	floorRoutes(f)
	if rec := f.get("/demo/page", f.login(t)); rec.Code != http.StatusForbidden || decode(t, rec)["code"] != codeMFAEnrollmentRequired {
		t.Errorf("no enrollment views = %d %s, want the JSON 403", rec.Code, rec.Body)
	}
}

// TestEnrollmentFloorEnrolledStepsUp proves an enrolled user's AAL1 session
// gets the step-up answer, not a 401 refresh loop: its refresh carries
// mfa_enrolled again, and the step-up reaches the route.
func TestEnrollmentFloorEnrolledStepsUp(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true, totpViews: &captureTOTPViews{}, enrollment: openWindow()})
	floorRoutes(f)
	secret, _ := f.enrollTOTP(t, f.login(t))

	cookie, refresh := f.loginPair(t)
	for range 2 {
		rec := f.get("/demo/json", cookie)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
			t.Fatalf("enrolled AAL1 JSON = %d %q, want the RFC 9470 401", rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
		rec = f.get("/demo/page", cookie)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/step-up/mfa?return_to=") || strings.Contains(rec.Header().Get("Location"), "recover=1") {
			t.Fatalf("enrolled AAL1 page = %d %q, want the step-up page", rec.Code, rec.Header().Get("Location"))
		}
		cookie, refresh = f.refresh(t, refresh)
	}
	if n := f.enrollmentRows(authlogic.MFADenialAssurance); n != 1 {
		t.Errorf("enrolled refusals recorded %d rows, want 1 per session", n)
	}
	stepped := f.totpStepUp(t, cookie, secret)
	for _, path := range []string{"/demo/json", "/demo/page"} {
		if rec := f.get(path, stepped); rec.Code != http.StatusOK {
			t.Errorf("%s after step-up = %d", path, rec.Code)
		}
	}
}

// TestEnrollmentFloorClaimlessToken proves a first-party AAL1 token without an
// enrollment claim (minted before the flip) gets the plain JSON 401 — no
// challenge — and, under Browser(), the login redirect with recover=1; its
// refresh mints the claim.
func TestEnrollmentFloorClaimlessToken(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true, totpViews: &captureTOTPViews{}, enrollment: openWindow()})
	floorRoutes(f)
	cookie := f.login(t)
	sid := ""
	f.sessions.mu.Lock()
	for id := range f.sessions.m {
		sid = id
	}
	f.sessions.mu.Unlock()
	raw, err := f.signer.Sign(map[string]any{"user_id": f.userID, "session_id": sid, "session_profile": "first_party", "aal": "aal1", "methods": []string{"password"}}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimless := &http.Cookie{Name: cookie.Name, Value: raw}

	rec := f.get("/demo/json", claimless)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("claimless JSON = %d %q, want the plain 401", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	rec = f.get("/demo/page", claimless)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/login?return_to="+url.QueryEscape("/demo/page")+"&recover=1" {
		t.Errorf("claimless page = %d %q, want the login recovery redirect", rec.Code, rec.Header().Get("Location"))
	}
	if rec := f.get("/auth/me", claimless); rec.Code != http.StatusOK {
		t.Errorf("claimless /auth/me = %d, want 200 (allowlisted)", rec.Code)
	}
}

// TestEnrollmentFloorHostOverrideInherits proves a host BundledRouteAuth
// override of an enrollment-surface group inherits AllowMFAEnrollment().
func TestEnrollmentFloorHostOverrideInherits(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true, enrollment: closedWindow(),
		routeAuth: BundledRouteAuthentication{SessionHydration: PrincipalStrategy(Live()), SessionSecurityReads: PrincipalStrategy(Accept(CredentialAccessToken), Live())}})
	cookie := f.login(t)
	for _, path := range []string{"/auth/me", "/auth/methods"} {
		if rec := f.get(path, cookie); rec.Code != http.StatusOK {
			t.Errorf("%s under a host override past the window = %d %s, want 200", path, rec.Code, rec.Body)
		}
	}
}

// TestEnrollmentFloorOAuth2Pages proves native all gates the OAuth2 authorize
// page at AAL2 with no grace, while /auth/sessions stays on the enrollment
// surface past the window.
func TestEnrollmentFloorOAuth2Pages(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	f.svc.Adapter.enrollmentFloor, f.svc.Adapter.enrollmentDenials = true, cacher.NewMemory()
	f.cfg.Sessions = f.svc.Service
	h := &handlers{listStrategy: list.StrategyCursor, svc: f.svc, views: stubViews{}, oauth2: f.cfg, oauth2MFA: true, mutation: MutationSecurity{AllowedOrigins: []string{delegatedIssuer}, SessionCookieName: f.svc.SessionCookieName()}}
	router := web.NewWebHandler()
	mountOAuth2(clientInfoRegistrar{inner: router}, h)
	mountSessionManagement(clientInfoRegistrar{inner: router}, h)
	token := func(enrollBy time.Time) *http.Cookie {
		raw, err := f.signer.Sign(map[string]any{"user_id": f.userID, "session_id": f.sessionID, "session_profile": "first_party", "aal": "aal1", "methods": []string{"password"}, "mfa_enroll_by": enrollBy.Unix()}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: f.svc.SessionCookieName(), Value: raw}
	}
	serve := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	authorize := "/auth/oauth2/authorize?" + oauthHTTPAuthorizationQuery().Encode()
	if w := serve(authorize, token(time.Now().Add(time.Hour))); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Errorf("authorize in grace = %d %q, want the RFC 9470 401", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	past := token(time.Now().Add(-time.Minute))
	if w := serve(authorize, past); w.Code != http.StatusForbidden {
		t.Errorf("authorize past the window = %d, want 403", w.Code)
	}
	// The fixture's store lists no sessions, so the page itself answers its
	// unavailable error; what matters is that the floor let the request reach it.
	if w := serve("/auth/sessions", past); w.Code == http.StatusForbidden || w.Code == http.StatusSeeOther || w.Body.String() != "PAGE:error" {
		t.Errorf("/auth/sessions past the window = %d %s, want the page handler reached", w.Code, w.Body)
	}
}

// TestEnrollmentFloorScope proves the floor judges only first-party access
// tokens below AAL2: API keys, delegated and external tokens and AAL2 tokens
// pass a plain RequirePrincipal untouched, and AllowMFAEnrollment() lifts it.
func TestEnrollmentFloorScope(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	for name, tc := range map[string]struct {
		cred Credential
		opts []PrincipalOption
		want int
	}{
		"first-party past":      {Credential{Profile: session.ProfileFirstParty, Assurance: session.AssuranceAAL1, MFAEnrollBy: past}, nil, http.StatusForbidden},
		"first-party allowed":   {Credential{Profile: session.ProfileFirstParty, Assurance: session.AssuranceAAL1, MFAEnrollBy: past}, []PrincipalOption{AllowMFAEnrollment()}, http.StatusOK},
		"first-party AAL2":      {Credential{Profile: session.ProfileFirstParty, Assurance: session.AssuranceAAL2}, nil, http.StatusOK},
		"delegated":             {Credential{Profile: session.ProfileDelegated, Assurance: session.AssuranceAAL1, Audiences: []string{"https://rs"}}, []PrincipalOption{DelegatedAudience("https://rs")}, http.StatusOK},
		"external AAL1":         {Credential{Assurance: session.AssuranceAAL1}, []PrincipalOption{Accept(CredentialExternalToken)}, http.StatusOK},
		"api key":               {Credential{}, []PrincipalOption{Accept(CredentialAPIKey)}, http.StatusOK},
		"first-party claimless": {Credential{Profile: session.ProfileFirstParty, Assurance: session.AssuranceAAL1}, nil, http.StatusUnauthorized},
	} {
		svc := &kindService{mfaService: mfaService{cred: tc.cred}, kind: CredentialAccessToken}
		switch {
		case tc.cred.Profile == "" && len(tc.opts) > 0 && name == "api key":
			svc.kind = CredentialAPIKey
		case tc.cred.Profile == "":
			svc.kind = CredentialExternalToken
		}
		a, err := NewAuthenticator(svc, environment.ModeDevelopment)
		if err != nil {
			t.Fatal(err)
		}
		a.enrollmentFloor, a.enrollmentDenials = true, cacher.NewMemory()
		reached := false
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Header.Set("Authorization", "Bearer "+extJWT(extIssuer, extSubject))
		if svc.kind == CredentialAPIKey {
			r.Header.Set("Authorization", "Bearer gpk_dotless")
		}
		rec := httptest.NewRecorder()
		a.RequirePrincipal(tc.opts...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, r)
		if rec.Code != tc.want || reached != (tc.want == http.StatusOK) {
			t.Errorf("%s: %d reached=%v, want %d", name, rec.Code, reached, tc.want)
		}
	}
}

// kindService is mfaService resolving bearers as one kind, with the machine
// subsystem on so an API key resolves.
type kindService struct {
	mfaService
	kind CredentialKind
}

func (s *kindService) ClassifyBearer(string) (CredentialKind, bool) { return s.kind, true }
func (s *kindService) MachineEnabled() bool                         { return true }
func (s *kindService) Authenticate(ctx context.Context, _ CredentialKind, transport Transport, raw string) (context.Context, error) {
	return s.mfaService.Authenticate(ctx, s.kind, transport, raw)
}
