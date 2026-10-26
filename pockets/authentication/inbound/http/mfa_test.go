package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const mfaChallenge = `Bearer error="insufficient_user_authentication", error_description="multi-factor authentication is required"`

// mfaCredentialKey stashes mfaService's credential on a request context.
type mfaCredentialKey struct{}

// mfaService is a host-shaped CredentialService admitting one external token
// whose credential the test fixes, so a gate's verdict comes only from what it
// reads. It records MFA denials.
type mfaService struct {
	cred    Credential
	denials []authlogic.MFADenial
}

// plainCredentialService hides mfaService's recorder: an interface-typed field
// promotes CredentialService's methods only.
type plainCredentialService struct{ CredentialService }

func (s *mfaService) Authenticate(ctx context.Context, kind CredentialKind, transport Transport, _ string) (context.Context, error) {
	cred := s.cred
	cred.Kind, cred.Transport = kind, transport
	ctx = sdk.WithPrincipal(ctx, Principal{Type: authlogic.PrincipalUser, ID: "host-u1"})
	return context.WithValue(ctx, mfaCredentialKey{}, cred), nil
}
func (s *mfaService) RequireLive(ctx context.Context, _ LivenessTier, _ bool) (context.Context, error) {
	return ctx, nil
}
func (s *mfaService) ClassifyBearer(string) (CredentialKind, bool) {
	return CredentialExternalToken, true
}
func (s *mfaService) ExternalEnabled() bool          { return true }
func (s *mfaService) MachineEnabled() bool           { return false }
func (s *mfaService) SessionLifetime() time.Duration { return 0 }
func (s *mfaService) CurrentCredential(ctx context.Context) (Credential, bool) {
	cred, ok := ctx.Value(mfaCredentialKey{}).(Credential)
	return cred, ok
}
func (s *mfaService) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return sdk.PrincipalFromContext(ctx)
}
func (s *mfaService) CurrentUser(ctx context.Context) (string, bool) {
	p, ok := sdk.PrincipalFromContext(ctx)
	return p.ID, ok
}
func (s *mfaService) CurrentSessionID(context.Context) (string, bool) { return "", false }
func (s *mfaService) RecordMFADenied(_ context.Context, d authlogic.MFADenial) {
	s.denials = append(s.denials, d)
}

func serveMFA(t *testing.T, mw web.Middleware) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	reached := false
	req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
	req.Header.Set("Authorization", "Bearer "+extJWT(extIssuer, extSubject))
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec, reached
}

// mfaRows returns the recorded mfa_required_denied rows.
func mfaRows(events *spySecurityEvents) []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, evt := range events.recorded() {
		if evt.EventType == securityevent.TypeMFARequiredDenied {
			out = append(out, evt)
		}
	}
	return out
}

// TestRequireMFAReadsAssurance proves the gate reads Credential.Assurance, not
// Identity.MFA: a host service that never stamps Assurance gets its MFA
// tokens denied (upgrade row 3), and each denial is recorded once.
func TestRequireMFAReadsAssurance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cred      Credential
		admitted  bool
		assurance session.AssuranceLevel
	}{
		{"stamped AAL2 without the flag", Credential{Assurance: session.AssuranceAAL2}, true, ""},
		{"stamped AAL1 beside the flag", Credential{Assurance: session.AssuranceAAL1, Identity: sdkauth.Identity{MFA: true}}, false, session.AssuranceAAL1},
		{"never stamped", Credential{Identity: sdkauth.Identity{MFA: true}}, false, session.AssuranceUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mfaService{cred: tc.cred}
			a, err := NewAuthenticator(svc, environment.ModeDevelopment)
			if err != nil {
				t.Fatal(err)
			}
			rec, reached := serveMFA(t, a.RequirePrincipal(Accept(CredentialExternalToken), RequireMFA()))
			if tc.admitted {
				if rec.Code != http.StatusOK || !reached || len(svc.denials) != 0 {
					t.Fatalf("admitted = %d reached=%v denials=%d", rec.Code, reached, len(svc.denials))
				}
				return
			}
			if rec.Code != http.StatusUnauthorized || reached || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
				t.Fatalf("denial = %d reached=%v challenge %q", rec.Code, reached, rec.Header().Get("WWW-Authenticate"))
			}
			if len(svc.denials) != 1 {
				t.Fatalf("recorder called %d times, want once", len(svc.denials))
			}
			d := svc.denials[0]
			if d.Gate != authlogic.MFAGateRoute || d.Principal.ID != "host-u1" || d.Credential.Kind != CredentialExternalToken || d.Credential.Assurance != tc.assurance {
				t.Fatalf("denial = %+v", d)
			}
		})
	}

	silent := &mfaService{cred: Credential{Assurance: session.AssuranceAAL1}}
	a, err := NewAuthenticator(plainCredentialService{silent}, environment.ModeDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := serveMFA(t, a.RequirePrincipal(Accept(CredentialExternalToken), RequireMFA())); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge || len(silent.denials) != 0 {
		t.Fatalf("a service without the recorder = %d %q denials=%d; want the denial unrecorded", rec.Code, rec.Header().Get("WWW-Authenticate"), len(silent.denials))
	}
}

// TestRequireMFABesideAccessTokens pins the lift: RequireMFA reads an access
// token's signed aal claim — an AAL2 session passes, an AAL1 or claim-less one
// gets the challenge and a row naming access_token — while the options that
// read provider evidence still panic beside access tokens, naming themselves.
func TestRequireMFABesideAccessTokens(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	gate := h.svc.RequirePrincipal(Accept(CredentialAccessToken, CredentialExternalToken), RequireMFA())
	h.svc.RequirePrincipal(RequireMFA())
	token := func(aal string) string {
		claims := map[string]any{"user_id": h.userID, "session_id": "s-mfa", "session_profile": "first_party"}
		if aal != "" {
			claims["aal"] = aal
		}
		raw, err := h.signer.Sign(claims, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if rec, got := h.serve(gate, extReq{bearer: token("aal2")}); rec.Code != http.StatusOK || got.cred.Assurance != session.AssuranceAAL2 {
		t.Fatalf("AAL2 access token = %d assurance %q", rec.Code, got.cred.Assurance)
	}
	for _, aal := range []string{"aal1", ""} {
		rec, _ := h.serve(gate, extReq{bearer: token(aal)})
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
			t.Fatalf("access token aal=%q = %d %q, want the RFC 9470 401", aal, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
	rows := mfaRows(h.events)
	if len(rows) != 2 || rows[0].Details["credential"] != "access_token" || rows[0].Details["assurance"] != "aal1" || rows[1].Details["assurance"] != "unknown" || rows[0].Details["gate"] != "route" {
		t.Fatalf("rows = %+v", rows)
	}
	for name, opt := range map[string]PrincipalOption{"FreshAuth": FreshAuth(time.Minute), "VerifiedEmail": VerifiedEmail(), "Tenant": Tenant("t"), "Check": Check(func(context.Context, Credential) error { return nil })} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, "FreshAuth, VerifiedEmail, Tenant and Check") {
					t.Errorf("%s beside access tokens panic = %q, want one naming the external-evidence options", name, msg)
				}
			}()
			h.svc.RequirePrincipal(Accept(CredentialAccessToken, CredentialExternalToken), RequireMFA(), opt)
		}()
	}
}

// TestRequireMFAOverTheRealEngine runs the gate over the pocket's own service:
// an MFA token passes, an AAL1 token gets the challenge and one row, an API key
// gets the 403 mfa_credential_not_eligible with no challenge and an api_key row,
// and a nested gate narrows the outer credential without re-resolving.
func TestRequireMFAOverTheRealEngine(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	mfa := h.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true })
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	gate := h.svc.RequirePrincipal(Accept(CredentialExternalToken, CredentialAPIKey), RequireMFA())

	if rec, got := h.serve(gate, extReq{bearer: mfa}); rec.Code != http.StatusOK || got.cred.Assurance != session.AssuranceAAL2 {
		t.Fatalf("MFA token = %d assurance %q", rec.Code, got.cred.Assurance)
	}
	rec, got := h.serve(gate, extReq{bearer: key})
	if rec.Code != http.StatusForbidden || got.reached || rec.Header().Get("WWW-Authenticate") != "" || !strings.Contains(rec.Body.String(), `"code":"mfa_credential_not_eligible"`) {
		t.Fatalf("API key under RequireMFA = %d reached=%v %q %s, want the 403 without a challenge", rec.Code, got.reached, rec.Header().Get("WWW-Authenticate"), rec.Body)
	}
	if rows := mfaRows(h.events); len(rows) != 1 || rows[0].Details["reason"] != "api_key" || rows[0].Details["credential"] != "api_key" || rows[0].Details["gate"] != "route" {
		t.Fatalf("API key denial rows = %+v, want one route/api_key row", rows)
	}
	h.events.reset()

	outer := h.svc.RequirePrincipal(Accept(CredentialExternalToken))
	inner := h.svc.RequirePrincipal(Accept(CredentialExternalToken), RequireMFA())
	nested := func(next http.Handler) http.Handler { return outer(inner(next)) }
	rec, got = h.serve(nested, extReq{bearer: h.token})
	if rec.Code != http.StatusUnauthorized || got.reached || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("nested AAL1 = %d reached=%v %q", rec.Code, got.reached, rec.Header().Get("WWW-Authenticate"))
	}
	rows := mfaRows(h.events)
	if len(rows) != 1 || rows[0].UserID != extUserID || rows[0].EventStatus != securityevent.StatusBlocked {
		t.Fatalf("nested denial rows = %+v, want one attributed row", rows)
	}
	for key, want := range map[string]any{"gate": "route", "credential": "external_token", "provider": "idp", "assurance": "aal1", "required": "aal2"} {
		if rows[0].Details[key] != want {
			t.Fatalf("row Details = %v, want %s=%v", rows[0].Details, key, want)
		}
	}
	if rec, _ := h.serve(nested, extReq{bearer: mfa}); rec.Code != http.StatusOK {
		t.Fatalf("nested MFA token = %d, want 200", rec.Code)
	}
}

// TestProviderMFAFloorAnswersTheChallenge proves a provider's RequireMFA floor
// reaches the client as the RFC 9470 challenge on every route, recorded once
// by the engine with no UserID.
func TestProviderMFAFloorAnswersTheChallenge(t *testing.T) {
	h := newExternalHarness(t, externalSetup{requireMFA: true})
	rec, got := h.serve(h.svc.RequirePrincipal(), extReq{bearer: h.token})
	if rec.Code != http.StatusUnauthorized || got.reached || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("floor denial = %d reached=%v %q", rec.Code, got.reached, rec.Header().Get("WWW-Authenticate"))
	}
	rows := mfaRows(h.events)
	if len(rows) != 1 || rows[0].UserID != "" || rows[0].Details["gate"] != "provider" {
		t.Fatalf("floor rows = %+v, want one provider row without a user", rows)
	}
	mfa := h.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true })
	if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{bearer: mfa}); rec.Code != http.StatusOK {
		t.Fatalf("MFA token over the floor = %d, want 200", rec.Code)
	}
}

// TestAdministrationMFAGate proves the admin gate runs after the host check:
// a non-admin keeps the 403 and records nothing; an admitted caller below AAL2
// gets the challenge and one admin row; AAL2 passes and an admitted API key
// gets the 403 mfa_credential_not_eligible (S4d).
func TestAdministrationMFAGate(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	mfa := h.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true })
	key, _, saID := h.mintKey(t, "operator", false, "", time.Time{})
	checks := 0
	check := func(_ context.Context, req UserAdminCheckRequest) error {
		checks++
		if req.Principal.ID == extUserID || req.Principal.ID == saID {
			return nil
		}
		return sdk.ErrForbidden
	}
	router := web.NewWebHandler()
	mount(router, mountDeps{Auth: h.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: check, AdministrationMFA: true})
	listUsers := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/auth/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := listUsers(h.accessJWT); rec.Code != http.StatusForbidden || rec.Header().Get("WWW-Authenticate") != "" || len(mfaRows(h.events)) != 0 {
		t.Fatalf("non-admin = %d %q rows=%d; want the host's 403 and no MFA row", rec.Code, rec.Header().Get("WWW-Authenticate"), len(mfaRows(h.events)))
	}
	if checks != 1 {
		t.Fatalf("checks = %d, want the host check to run first", checks)
	}
	rec := listUsers(h.token)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("admin at AAL1 = %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	rows := mfaRows(h.events)
	if len(rows) != 1 || rows[0].UserID != extUserID {
		t.Fatalf("admin rows = %+v", rows)
	}
	for k, want := range map[string]any{"gate": "admin", "credential": "external_token", "provider": "idp", "assurance": "aal1", "required": "aal2"} {
		if rows[0].Details[k] != want {
			t.Fatalf("admin row Details = %v, want %s=%v", rows[0].Details, k, want)
		}
	}
	if rec := listUsers(mfa); rec.Code != http.StatusOK {
		t.Fatalf("admin at AAL2 = %d, want 200", rec.Code)
	}
	if rec := listUsers(key); rec.Code != http.StatusForbidden || rec.Header().Get("WWW-Authenticate") != "" || !strings.Contains(rec.Body.String(), "mfa_credential_not_eligible") {
		t.Fatalf("admin API key = %d %q %s, want the 403 without a challenge", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body)
	}
	rows = mfaRows(h.events)
	if len(rows) != 2 || rows[1].Details["reason"] != "api_key" || rows[1].Details["gate"] != "admin" {
		t.Fatalf("admin API key rows = %+v, want an admin/api_key row", rows)
	}

	native := web.NewWebHandler()
	mount(native, mountDeps{Auth: h.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }, AdministrationMFA: true})
	nativeReq := httptest.NewRequest(http.MethodGet, "/auth/admin/users", nil)
	nativeReq.Header.Set("Authorization", "Bearer "+h.accessJWT)
	nativeRec := httptest.NewRecorder()
	native.ServeHTTP(nativeRec, nativeReq)
	rows = mfaRows(h.events)
	if nativeRec.Code != http.StatusUnauthorized || len(rows) != 3 || rows[2].UserID != h.userID || rows[2].Details["credential"] != "access_token" || rows[2].Details["assurance"] != "aal1" {
		t.Fatalf("native admin session = %d rows %+v; want 401 and an access_token/aal1 row", nativeRec.Code, rows)
	}

	plain := web.NewWebHandler()
	mount(plain, mountDeps{Auth: h.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: check})
	req := httptest.NewRequest(http.MethodGet, "/auth/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	without := httptest.NewRecorder()
	plain.ServeHTTP(without, req)
	if without.Code != http.StatusOK {
		t.Fatalf("admin at AAL1 without the gate = %d, want 200", without.Code)
	}
}

func TestAdministrationMFARequiresUserAdminCheck(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	if _, err := New(h.svc.Service, environment.ModeDevelopment, WithAdministrationMFA()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("WithAdministrationMFA without WithUserAdminCheck = %v, want ErrInvalidInput", err)
	}
	if _, err := New(h.svc.Service, environment.ModeDevelopment, WithAdministrationMFA(), WithUserAdminCheck(func(context.Context, UserAdminCheckRequest) error { return nil })); err != nil {
		t.Fatalf("WithAdministrationMFA beside a check: %v", err)
	}
}

// TestMFAMethodsOnEveryGate pins MFAConfig.Methods (S4c): a TOTP AAL2 session is
// admitted by RequireMFA(), the admin gate and the machine gate when the
// restriction admits totp (or there is none) and refused with
// mfa_required_denied reason=method method=totp when it names only passkey;
// a first-party AAL2 token without a methods claim fails closed under a
// restriction and passes without one.
func TestMFAMethodsOnEveryGate(t *testing.T) {
	allow := func(context.Context, UserAdminCheckRequest) error { return nil }
	build := func(methods ...authlogic.MFAMethod) (*passkeyFixture, *http.Cookie) {
		f := newPasskeyFixture(t, passkeyOptions{totp: true, machine: true, adminMFA: true, adminCheck: allow, mfaMethods: methods})
		cookie := f.login(t)
		secret, _ := f.enrollTOTP(t, cookie)
		return f, f.totpStepUp(t, cookie, secret)
	}
	route := func(f *passkeyFixture, c *http.Cookie, bearer string) int {
		h := f.svc.RequirePrincipal(RequireMFA())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		r := httptest.NewRequest("GET", "/guarded", nil)
		if c != nil {
			r.AddCookie(c)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	gates := func(f *passkeyFixture, c *http.Cookie) []int {
		return []int{
			route(f, c, ""),
			f.do(t, call{method: "GET", path: "/auth/admin/users", cookie: c}).Code,
			f.do(t, call{method: "GET", path: "/auth/service-accounts", cookie: c}).Code,
		}
	}
	for _, methods := range [][]authlogic.MFAMethod{nil, {authlogic.MFAMethodTOTP}, {authlogic.MFAMethodPasskey, authlogic.MFAMethodTOTP}} {
		f, aal2 := build(methods...)
		if got := gates(f, aal2); !slices.Equal(got, []int{200, 200, 200}) {
			t.Errorf("Methods %v: gates = %v, want all 200", methods, got)
		}
	}
	f, aal2 := build(authlogic.MFAMethodPasskey)
	if got := gates(f, aal2); !slices.Equal(got, []int{401, 401, 401}) {
		t.Fatalf("Methods [passkey]: gates = %v, want all 401", got)
	}
	rows := mfaRows(f.events)
	if len(rows) != 3 {
		t.Fatalf("mfa_required_denied rows = %d, want 3", len(rows))
	}
	for i, gate := range []string{"route", "admin", "machine"} {
		d := rows[i].Details
		if d["gate"] != gate || d["reason"] != "method" || d["method"] != "totp" || d["assurance"] != "aal2" {
			t.Errorf("row %d = %v", i, d)
		}
	}

	legacy, err := f.signer.Sign(map[string]any{"user_id": f.userID, "session_id": "legacy", "session_profile": "first_party", "aal": "aal2"}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if code := route(f, nil, legacy); code != http.StatusUnauthorized {
		t.Fatalf("a token without methods under a restriction = %d, want 401", code)
	}
	if d := mfaRows(f.events)[3].Details; d["reason"] != "method" || d["method"] != "none" {
		t.Fatalf("legacy denial = %v", d)
	}
	open := newPasskeyFixture(t, passkeyOptions{totp: true})
	legacy, _ = open.signer.Sign(map[string]any{"user_id": open.userID, "session_id": "legacy", "session_profile": "first_party", "aal": "aal2"}, time.Now().Add(time.Minute))
	if code := route(open, nil, legacy); code != http.StatusOK {
		t.Fatalf("a token without methods and no restriction = %d, want 200", code)
	}
	if d := mfaRows(open.events); len(d) != 0 {
		t.Fatalf("unexpected denials %v", d)
	}
	aal1 := open.login(t)
	if code := route(open, aal1, ""); code != http.StatusUnauthorized || mfaRows(open.events)[0].Details["reason"] != "assurance" {
		t.Fatalf("AAL1 = %d %v", code, mfaRows(open.events))
	}
}

// TestAPIKeysExemptRestoresThePass pins the MFAConfig.APIKeysExempt hatch: a
// key passes RequireMFA() and the admin gate again, recording no denial.
func TestAPIKeysExemptRestoresThePass(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	h.svc.Adapter.apiKeysExempt = true
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	gate := h.svc.RequirePrincipal(Accept(CredentialExternalToken, CredentialAPIKey), RequireMFA())
	if rec, _ := h.serve(gate, extReq{bearer: key}); rec.Code != http.StatusOK {
		t.Fatalf("exempt API key under RequireMFA = %d, want 200", rec.Code)
	}
	router := web.NewWebHandler()
	mount(router, mountDeps{Auth: h.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }, AdministrationMFA: true})
	req := httptest.NewRequest(http.MethodGet, "/auth/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("exempt admin API key = %d, want 200", rec.Code)
	}
	if rows := mfaRows(h.events); len(rows) != 0 {
		t.Fatalf("an exempt key recorded %+v", rows)
	}
}

// TestAPIKeyMFADenialUnderBrowser proves a key under a Browser() RequireMFA()
// gate gets the JSON 403, never the login or step-up redirect.
func TestAPIKeyMFADenialUnderBrowser(t *testing.T) {
	h := newExternalHarness(t, externalSetup{loginPath: extLoginPath})
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	gate := h.svc.RequirePrincipal(Accept(CredentialExternalToken, CredentialAPIKey), RequireMFA(), Browser())
	rec, _ := h.serve(gate, extReq{bearer: key})
	if rec.Code != http.StatusForbidden || rec.Header().Get("Location") != "" || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("API key under Browser() RequireMFA() = %d %q %q, want the JSON 403", rec.Code, rec.Header().Get("Location"), rec.Header().Get("WWW-Authenticate"))
	}
}
