package authenticationhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The external-token matrix: kinds × transports × liveness tiers × assurance
// options × denial modes, plus the bundled route audit for provider users.

const (
	extIssuer      = "https://idp.example.test"
	extCookie      = "__session"
	extSubject     = "s1"
	extUserID      = "ext-u1"
	extLoginPath   = "/signin"
	extEmail       = "ext@example.com"
	extOtherTenant = "tenant-b"
)

// --- fakes ---

type extProviderFake struct {
	mu         sync.Mutex
	identities map[string]sdkauth.Identity
	err        error
}

func (p *extProviderFake) Issuer() string { return extIssuer }

func (p *extProviderFake) Authenticate(_ context.Context, cred sdkauth.Credential) (sdkauth.Identity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return sdkauth.Identity{}, p.err
	}
	id, ok := p.identities[cred.Raw]
	if !ok {
		return sdkauth.Identity{}, sdkauth.ErrInvalidCredential
	}
	return id, nil
}

type extRevocationFake struct {
	mu    sync.Mutex
	facts sdkauth.RevocationFacts
	err   error
	calls atomic.Int64
}

func (r *extRevocationFake) CheckRevocation(context.Context, string, string) (sdkauth.RevocationFacts, error) {
	r.calls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.facts, r.err
}

func (r *extRevocationFake) set(facts sdkauth.RevocationFacts, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts, r.err = facts, err
}

type extSubjectsFake struct {
	mu sync.Mutex
	m  map[string]externalsubject.ExternalSubject
}

func (s *extSubjectsFake) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.m[provider+"|"+subject]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return row, nil
}

func (s *extSubjectsFake) Link(_ context.Context, row externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[row.Provider+"|"+row.Subject] = row
	return row, nil
}

func (s *extSubjectsFake) ListByUser(context.Context, string) ([]externalsubject.ExternalSubject, error) {
	return []externalsubject.ExternalSubject{}, nil
}

func (s *extSubjectsFake) Unlink(context.Context, string, string) error { return sdk.ErrNotFound }

// extJWT is a JWT-shaped token naming iss; the fake provider decides validity.
func extJWT(iss, sub string) string {
	body, _ := json.Marshal(map[string]any{"iss": iss, "sub": sub})
	return "e30." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

// --- harness ---

type externalHarness struct {
	*credentialHarness
	provider   *extProviderFake
	revocation *extRevocationFake
	idents     *memIdentifiers
	token      string
}

type externalSetup struct {
	noProviders bool
	cookies     []string
	loginPath   string
	requireMFA  bool
	failOpen    bool
}

// newExternalHarness wires a hybrid service: the native user rail and machine
// rail of newCredentialHarness plus one external provider with a mirrored,
// active local user holding a verified email.
func newExternalHarness(t *testing.T, setup externalSetup) *externalHarness {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	sess := &memSessions{m: map[string]session.Session{}}
	signer := newFakeSigner()
	sas := &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}}
	keys := &memAPIKeys{m: map[string]apikey.APIKey{}}
	events := newSpySecurityEvents()
	provider := &extProviderFake{identities: map[string]sdkauth.Identity{}}
	revocation := &extRevocationFake{}
	subjects := &extSubjectsFake{m: map[string]externalsubject.ExternalSubject{
		"idp|" + extSubject: {Provider: "idp", Subject: extSubject, UserID: extUserID},
	}}
	fixture := authenticationFixture{
		Users: users, Identifiers: idents, Passwords: &memPasswords{m: map[string]string{}}, Sessions: sess,
		Challenges: &memChallenges{byID: map[string]challenge.Challenge{}}, Protector: memProtector{},
		Hasher: &fakeHasher{}, Limiter: ratelimiter.NewMemory(), ServiceAccounts: sas, APIKeys: keys,
		SecurityEvents: events, TokenSigner: signer,
		DelegatedTokens: authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer},
		UserAdmin:       memUserAdmin{},
		OAuthAccounts:   &memOAuthAccounts{}, OAuthStates: &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers: []oauth.Provider{stubProvider{}}, OAuthCallbackBase: "https://app.example.com",
		PublicAuthBaseURL: "https://auth.example.com",
	}
	if !setup.noProviders {
		fixture.External = &authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{
			Name: "idp", Provider: provider, Revocation: revocation, CookieName: extCookie, RequireMFA: setup.requireMFA,
			RevocationFailOpen: setup.failOpen,
		}}}
		fixture.ExternalSubjects = subjects
	}
	svc := newServiceWithFakes(fixture, authenticatorConfig{ExternalCookies: setup.cookies, BrowserLoginPath: setup.loginPath})

	h := &credentialHarness{svc: svc, users: users, sess: sess, signer: signer, sas: sas, keys: keys, events: events}
	ctx := context.Background()
	u, err := svc.Register(ctx, "matrix@example.com", credentialPassword, "Matrix")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, _, err := svc.Login(ctx, "matrix@example.com", credentialPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	h.userID, h.accessJWT = u.ID, pair.AccessToken
	for id := range sess.m {
		h.sessionID = id
	}

	users.mu.Lock()
	users.byID[extUserID] = user.User{ID: extUserID, DisplayName: "External", Status: user.StatusActive}
	users.mu.Unlock()
	idents.insert(identifier.Identifier{
		UserID: extUserID, Kind: identifier.KindEmail, NormalizedValue: extEmail,
		VerifiedAt: time.Now(), NotificationEnabled: true, IsPrimary: true,
	})
	token := extJWT(extIssuer, extSubject)
	provider.identities[token] = sdkauth.Identity{
		Issuer: extIssuer, Subject: extSubject, Email: extEmail, EmailVerified: true,
		AuthenticatedAt: time.Now().Add(-time.Minute), Tenant: "tenant-a",
	}
	return &externalHarness{credentialHarness: h, provider: provider, revocation: revocation, idents: idents, token: token}
}

// identity mints a token whose verified identity mutate adjusts.
func (h *externalHarness) identity(name string, mutate func(*sdkauth.Identity)) string {
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	id := h.provider.identities[h.token]
	mutate(&id)
	raw := extJWT(extIssuer, extSubject) + name
	h.provider.identities[raw] = id
	return raw
}

func (h *externalHarness) revoke() {
	h.revocation.set(sdkauth.RevocationFacts{ValidAfter: time.Now()}, nil)
}

func (h *externalHarness) deleteNativeSession(t *testing.T) {
	t.Helper()
	if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
		t.Fatal(err)
	}
}

type extReq struct {
	method       string
	bearer       string
	nativeCookie string
	extCookie    string
}

func (r extReq) build() *http.Request {
	method := r.method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, "/guarded", nil)
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.nativeCookie != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: r.nativeCookie})
	}
	if r.extCookie != "" {
		req.AddCookie(&http.Cookie{Name: extCookie, Value: r.extCookie})
	}
	return req
}

func (h *externalHarness) serve(mw web.Middleware, r extReq) (*httptest.ResponseRecorder, observed) {
	return serveGuarded(h.credentialHarness, mw, r.build())
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic at wiring time", name)
		}
	}()
	fn()
}

// --- admission ---

func TestExternalDefaultSetAdmitsEveryWiredKind(t *testing.T) {
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}})
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	for name, r := range map[string]extReq{
		"external header":     {bearer: h.token},
		"external cookie":     {extCookie: h.token},
		"native header":       {bearer: h.accessJWT},
		"native cookie":       {nativeCookie: h.accessJWT},
		"api key":             {bearer: key},
		"external header put": {method: http.MethodPut, bearer: h.token},
	} {
		rec, got := h.serve(h.svc.RequirePrincipal(), r)
		if rec.Code != http.StatusOK || !got.reached {
			t.Errorf("%s through the default set = %d, want 200", name, rec.Code)
		}
	}
	rec, got := h.serve(h.svc.RequirePrincipal(), extReq{bearer: h.token})
	if rec.Code != http.StatusOK || got.principal.ID != extUserID || got.cred.Kind != CredentialExternalToken || got.cred.Transport != TransportHeader || got.cred.Identity.Subject != extSubject {
		t.Fatalf("external stash = %+v / %+v", got.principal, got.cred)
	}

	plain := newExternalHarness(t, externalSetup{noProviders: true})
	if rec, _ := plain.serve(plain.svc.RequirePrincipal(), extReq{bearer: plain.token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("external token on a host without providers = %d, want 401", rec.Code)
	}
	if rec, _ := plain.serve(plain.svc.RequirePrincipal(), extReq{bearer: plain.accessJWT}); rec.Code != http.StatusOK {
		t.Errorf("native token on a host without providers = %d, want 200", rec.Code)
	}
}

func TestExternalAdmittedOnlyBySetsAcceptingIt(t *testing.T) {
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}})
	deny := map[string]web.Middleware{
		"RequireAccessToken":             h.svc.RequireAccessToken(),
		"RequireAccessTokenLive":         h.svc.RequireAccessTokenLive(),
		"RequireAccessTokenOrAPIKey":     h.svc.RequireAccessTokenOrAPIKey(),
		"RequireAccessTokenOrAPIKeyLive": h.svc.RequireAccessTokenOrAPIKeyLive(),
		"RequireAPIKey":                  h.svc.RequireAPIKey(),
		"FirstParty":                     h.svc.RequirePrincipal(FirstParty()),
		"Audience":                       h.svc.RequirePrincipal(Audience(apiResource)),
		"DelegatedAudience":              h.svc.RequirePrincipal(DelegatedAudience(apiResource)),
	}
	for name, mw := range deny {
		if rec, got := h.serve(mw, extReq{bearer: h.token}); rec.Code != http.StatusUnauthorized || got.reached {
			t.Errorf("%s admitted an external token: %d", name, rec.Code)
		}
	}
	for name, mw := range map[string]web.Middleware{
		"RequireExternalToken":         h.svc.RequireExternalToken(),
		"RequireExternalTokenOrAPIKey": h.svc.RequireExternalTokenOrAPIKey(),
	} {
		if rec, _ := h.serve(mw, extReq{bearer: h.token}); rec.Code != http.StatusOK {
			t.Errorf("%s refused an external token: %d", name, rec.Code)
		}
		if rec, _ := h.serve(mw, extReq{bearer: h.accessJWT}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s admitted a native access token: %d", name, rec.Code)
		}
		if rec, _ := h.serve(mw, extReq{nativeCookie: h.accessJWT}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s admitted a native session cookie: %d", name, rec.Code)
		}
	}
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	if rec, _ := h.serve(h.svc.RequireExternalTokenOrAPIKey(), extReq{bearer: key}); rec.Code != http.StatusOK {
		t.Errorf("RequireExternalTokenOrAPIKey refused an API key: %d", rec.Code)
	}
	if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{bearer: extJWT("https://unwired.example.test", extSubject)}); rec.Code != http.StatusUnauthorized {
		t.Errorf("unwired issuer = %d, want 401", rec.Code)
	}
}

func TestExternalNestedNarrowingReadsTheStash(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	chain := func(outer, inner web.Middleware) web.Middleware {
		return func(next http.Handler) http.Handler { return outer(inner(next)) }
	}
	if rec, _ := h.serve(chain(h.svc.RequirePrincipal(), h.svc.RequireAccessToken()), extReq{bearer: h.token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("nested access-token gate admitted the stashed external token: %d", rec.Code)
	}
	if rec, _ := h.serve(chain(h.svc.RequirePrincipal(), h.svc.RequireExternalToken()), extReq{bearer: h.token}); rec.Code != http.StatusOK {
		t.Errorf("nested external gate refused the stashed external token: %d", rec.Code)
	}
	if rec, _ := h.serve(chain(h.svc.RequirePrincipal(), h.svc.RequirePrincipal(Accept(CredentialExternalToken), Transports(TransportCookie))), extReq{bearer: h.token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("nested cookie-only gate admitted a header-borne external token: %d", rec.Code)
	}
}

// --- cookies ---

func TestExternalCookieTransport(t *testing.T) {
	t.Run("header-only without a configured cookie name", func(t *testing.T) {
		h := newExternalHarness(t, externalSetup{})
		if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{extCookie: h.token}); rec.Code != http.StatusUnauthorized {
			t.Errorf("unconfigured provider cookie = %d, want 401 (never read)", rec.Code)
		}
	})
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}})
	t.Run("admitted on a cookie route", func(t *testing.T) {
		rec, got := h.serve(h.svc.RequirePrincipal(Accept(CredentialExternalToken), Transports(TransportCookie)), extReq{extCookie: h.token})
		if rec.Code != http.StatusOK || got.cred.Transport != TransportCookie {
			t.Errorf("provider cookie = %d transport=%q, want 200 cookie", rec.Code, got.cred.Transport)
		}
	})
	t.Run("ignored on a header-only route", func(t *testing.T) {
		if rec, _ := h.serve(h.svc.RequirePrincipal(Transports(TransportHeader)), extReq{extCookie: h.token}); rec.Code != http.StatusUnauthorized {
			t.Errorf("header-only route read the provider cookie: %d", rec.Code)
		}
	})
	t.Run("never read by a set not admitting external tokens", func(t *testing.T) {
		rec, got := h.serve(h.svc.RequirePrincipal(Accept(CredentialAccessToken), Optional()), extReq{extCookie: h.token})
		if rec.Code != http.StatusOK || got.principal.ID != "" {
			t.Errorf("native-only Optional() with a provider cookie = %d principal=%q, want anonymous pass", rec.Code, got.principal.ID)
		}
	})
	t.Run("native cookie first, first present wins", func(t *testing.T) {
		rec, got := h.serve(h.svc.RequirePrincipal(), extReq{nativeCookie: h.accessJWT, extCookie: h.token})
		if rec.Code != http.StatusOK || got.cred.Kind != CredentialAccessToken {
			t.Errorf("both cookies = %d kind=%q, want the native one", rec.Code, got.cred.Kind)
		}
		if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{nativeCookie: "not.a.token", extCookie: h.token}); rec.Code != http.StatusUnauthorized {
			t.Errorf("invalid native cookie fell through to the provider cookie: %d", rec.Code)
		}
	})
	t.Run("a native token in the provider cookie denies", func(t *testing.T) {
		if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{extCookie: h.accessJWT}); rec.Code != http.StatusUnauthorized {
			t.Errorf("native token under the provider cookie name = %d, want 401", rec.Code)
		}
	})
	t.Run("Optional never launders a bad provider cookie", func(t *testing.T) {
		if rec, got := h.serve(h.svc.RequirePrincipal(Optional()), extReq{extCookie: extJWT(extIssuer, "unknown")}); rec.Code != http.StatusUnauthorized || got.reached {
			t.Errorf("Optional() with an invalid provider cookie = %d", rec.Code)
		}
		h.revoke()
		defer h.revocation.set(sdkauth.RevocationFacts{}, nil)
		if rec, got := h.serve(h.svc.RequirePrincipal(Optional(), Live()), extReq{extCookie: h.token}); rec.Code != http.StatusUnauthorized || got.reached {
			t.Errorf("Optional() with a revoked provider cookie = %d", rec.Code)
		}
		if rec, got := h.serve(h.svc.RequirePrincipal(Optional()), extReq{}); rec.Code != http.StatusOK || got.principal.ID != "" {
			t.Errorf("Optional() with nothing presented = %d", rec.Code)
		}
	})
}

func TestExternalCookieNameValidation(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	for _, names := range [][]string{
		{"session"}, {"session_refresh"}, {csrfCookieName}, {extCookie, extCookie}, {"bad name"}, {""},
	} {
		if _, err := NewAuthenticator(h.svc.Service, environment.ModeDevelopment, WithExternalCookies(names...)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("WithExternalCookies(%q) = %v, want ErrInvalidInput", names, err)
		}
	}
	if _, err := NewAuthenticator(h.svc.Service, environment.ModeDevelopment, WithCookies(CookieConfig{Name: "app"}), WithExternalCookies("app_refresh")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("provider cookie sharing a custom refresh name = %v, want ErrInvalidInput", err)
	}
	if _, err := NewAuthenticator(h.svc.Service, environment.ModeDevelopment, WithExternalCookies(extCookie, "__clerk")); err != nil {
		t.Errorf("valid provider cookies: %v", err)
	}
	plain := newExternalHarness(t, externalSetup{noProviders: true})
	if _, err := NewAuthenticator(plain.svc.Service, environment.ModeDevelopment, WithExternalCookies(extCookie)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("provider cookies without providers = %v, want ErrInvalidInput", err)
	}
}

// --- wiring-time panics ---

func TestExternalWiringPanics(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	for name, opts := range map[string][]PrincipalOption{
		"FreshAuth with access tokens":    {Accept(CredentialAccessToken, CredentialExternalToken), FreshAuth(time.Minute)},
		"Tenant on the default set":       {Tenant("t")},
		"VerifiedEmail with access token": {Accept(CredentialAccessToken), VerifiedEmail()},
		"Browser with external, no path":  {Browser()},
		"Browser on RequireExternalToken": {Accept(CredentialExternalToken), Browser()},
	} {
		mustPanic(t, name, func() { h.svc.RequirePrincipal(opts...) })
	}
	mustPanic(t, "FreshAuth(0)", func() { FreshAuth(0) })
	mustPanic(t, "Tenant()", func() { Tenant() })
	mustPanic(t, "Check(nil)", func() { Check(nil) })

	h.svc.RequirePrincipal(Accept(CredentialExternalToken, CredentialAPIKey), RequireMFA(), FreshAuth(time.Minute), VerifiedEmail(), Tenant("t"))
	h.svc.RequirePrincipal(RequireMFA())
	h.svc.RequirePrincipal(Accept(CredentialAccessToken), Browser())
	withPath := newExternalHarness(t, externalSetup{loginPath: extLoginPath})
	withPath.svc.RequirePrincipal(Browser())
	plain := newExternalHarness(t, externalSetup{noProviders: true})
	plain.svc.RequirePrincipal(Browser())
}

// --- assurance ---

func TestExternalAssuranceOptions(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	mfa := h.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true })
	stale := h.identity("stale", func(id *sdkauth.Identity) { id.AuthenticatedAt = time.Now().Add(-time.Hour) })
	unverified := h.identity("unverified", func(id *sdkauth.Identity) { id.EmailVerified = false })
	otherTenant := h.identity("tenant", func(id *sdkauth.Identity) { id.Tenant = extOtherTenant })
	ext := Accept(CredentialExternalToken, CredentialAPIKey)

	cases := []struct {
		name      string
		opt       PrincipalOption
		pass      string
		fail      string
		challenge string
	}{
		{"RequireMFA", RequireMFA(), mfa, h.token, `Bearer error="insufficient_user_authentication"`},
		{"FreshAuth", FreshAuth(5 * time.Minute), h.token, stale, `max_age="300"`},
		{"VerifiedEmail", VerifiedEmail(), h.token, unverified, ""},
		{"Tenant", Tenant("tenant-a"), h.token, otherTenant, ""},
		{"Check", Check(func(_ context.Context, cred Credential) error {
			if cred.Identity.Tenant != "tenant-a" {
				return errors.New("host rule")
			}
			return nil
		}), h.token, otherTenant, ""},
	}
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	for _, c := range cases {
		mw := h.svc.RequirePrincipal(ext, c.opt)
		if rec, _ := h.serve(mw, extReq{bearer: c.pass}); rec.Code != http.StatusOK {
			t.Errorf("%s refused a satisfying token: %d", c.name, rec.Code)
		}
		rec, got := h.serve(mw, extReq{bearer: c.fail})
		header := rec.Header().Get("WWW-Authenticate")
		if rec.Code != http.StatusUnauthorized || got.reached {
			t.Errorf("%s admitted a failing token: %d", c.name, rec.Code)
		}
		if c.challenge != "" && !strings.Contains(header, c.challenge) {
			t.Errorf("%s WWW-Authenticate = %q, want %q (RFC 9470)", c.name, header, c.challenge)
		}
		if c.challenge == "" && header != "" {
			t.Errorf("%s carried a re-authentication challenge it cannot cure: %q", c.name, header)
		}
		// The external-evidence options never apply to a key; RequireMFA refuses
		// one outright (S4d), with a 403 and no challenge.
		want := http.StatusOK
		if c.name == "RequireMFA" {
			want = http.StatusForbidden
		}
		if rec, _ := h.serve(mw, extReq{bearer: key}); rec.Code != want || rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s over an API key = %d %q, want %d and no challenge", c.name, rec.Code, rec.Header().Get("WWW-Authenticate"), want)
		}
	}

	if rec, _ := h.serve(h.svc.RequirePrincipal(ext, Check(func(context.Context, Credential) error { return sdk.ErrUnavailable })), extReq{bearer: h.token}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Check reporting unavailable = %d, want 503", rec.Code)
	}

	browser := newExternalHarness(t, externalSetup{loginPath: extLoginPath})
	rec, _ := browser.serve(browser.svc.RequirePrincipal(Accept(CredentialExternalToken), RequireMFA(), Browser()), extReq{bearer: browser.token})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("Browser() assurance denial = %d %q, want a plain 303", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

// --- liveness ---

// TestLivenessTiersAcrossKinds is the tier × kind table: a revoked native
// session, an API key, a revoked external subject and a revoked delegated grant,
// each on a safe and an unsafe method under Live, LiveOnMutation (the default)
// and Stateless.
func TestLivenessTiersAcrossKinds(t *testing.T) {
	type outcome struct{ get, post int }
	const ok, no = http.StatusOK, http.StatusUnauthorized
	tiers := []struct {
		name string
		opts []PrincipalOption
	}{
		{"Live", []PrincipalOption{Live()}},
		{"LiveOnMutation (default)", nil},
		{"LiveOnMutation (explicit)", []PrincipalOption{LiveOnMutation()}},
		{"Stateless", []PrincipalOption{Stateless()}},
	}
	want := map[string]map[string]outcome{
		"native access token": {"Live": {no, no}, "LiveOnMutation (default)": {ok, no}, "LiveOnMutation (explicit)": {ok, no}, "Stateless": {ok, ok}},
		"api key":             {"Live": {ok, ok}, "LiveOnMutation (default)": {ok, ok}, "LiveOnMutation (explicit)": {ok, ok}, "Stateless": {ok, ok}},
		"external token":      {"Live": {no, no}, "LiveOnMutation (default)": {ok, no}, "LiveOnMutation (explicit)": {ok, no}, "Stateless": {ok, ok}},
		"delegated token":     {"Live": {no, no}, "LiveOnMutation (default)": {no, no}, "LiveOnMutation (explicit)": {no, no}, "Stateless": {no, no}},
	}
	h := newExternalHarness(t, externalSetup{})
	key, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	delegated, _ := h.delegatedToken(t, "grant-1")
	h.sess.mu.Lock()
	delete(h.sess.m, "grant-1")
	h.sess.mu.Unlock()
	h.deleteNativeSession(t)
	h.revoke()

	creds := map[string]string{"native access token": h.accessJWT, "api key": key, "external token": h.token, "delegated token": delegated}
	for kind, raw := range creds {
		for _, tier := range tiers {
			opts := tier.opts
			if kind == "delegated token" {
				opts = append([]PrincipalOption{DelegatedAudience(mcpResource)}, opts...)
			}
			mw := h.svc.RequirePrincipal(opts...)
			got := outcome{}
			rec, _ := h.serve(mw, extReq{bearer: raw})
			got.get = rec.Code
			rec, _ = h.serve(mw, extReq{method: http.MethodPost, bearer: raw})
			got.post = rec.Code
			if got != want[kind][tier.name] {
				t.Errorf("%s × %s: GET/POST = %d/%d, want %d/%d", kind, tier.name, got.get, got.post, want[kind][tier.name].get, want[kind][tier.name].post)
			}
		}
	}
}

// TestNativeDefaultDeniesRevokedSessionOnMutation pins R-LIVE: the default tier
// re-checks a NATIVE access token's session on unsafe methods, and Stateless()
// restores the prior signature-only behavior.
func TestNativeDefaultDeniesRevokedSessionOnMutation(t *testing.T) {
	h := newCredentialHarness(t)
	if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
		t.Fatal(err)
	}
	post := func(mw web.Middleware) int {
		r := h.credentialRequest(h.accessJWT, "")
		r.Method = http.MethodPost
		rec, _ := serveGuarded(h, mw, r)
		return rec.Code
	}
	for name, mw := range map[string]web.Middleware{
		"RequirePrincipal()":           h.svc.RequirePrincipal(),
		"RequireAccessToken()":         h.svc.RequireAccessToken(),
		"RequireAccessTokenOrAPIKey()": h.svc.RequireAccessTokenOrAPIKey(),
		"RequireAccessTokenCookie()":   h.svc.RequirePrincipal(Accept(CredentialAccessToken)),
	} {
		if code := post(mw); code != http.StatusUnauthorized {
			t.Errorf("%s POST with a revoked session = %d, want 401 (R-LIVE)", name, code)
		}
	}
	if code := post(h.svc.RequirePrincipal(Stateless())); code != http.StatusOK {
		t.Errorf("Stateless() POST with a revoked session = %d, want 200", code)
	}
	if rec, _ := serveGuarded(h, h.svc.RequirePrincipal(), h.credentialRequest(h.accessJWT, "")); rec.Code != http.StatusOK {
		t.Errorf("default GET with a revoked session = %d, want 200 (safe reads pass on validity)", rec.Code)
	}
}

// --- 503 ---

func TestUnavailableAnswers503WithRetryAfter(t *testing.T) {
	assert503 := func(t *testing.T, name string, rec *httptest.ResponseRecorder, got observed) {
		t.Helper()
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || got.reached {
			t.Errorf("%s = %d Retry-After=%q reached=%v, want 503 + Retry-After: 5", name, rec.Code, rec.Header().Get("Retry-After"), got.reached)
		}
		if !strings.Contains(rec.Body.String(), `"unavailable"`) {
			t.Errorf("%s body = %s, want the JSON unavailable error", name, rec.Body)
		}
	}

	t.Run("native session store outage on a mutation", func(t *testing.T) {
		h := newExternalHarness(t, externalSetup{loginPath: extLoginPath})
		h.sess.getErr = errors.New("store down")
		rec, got := h.serve(h.svc.RequirePrincipal(), extReq{method: http.MethodPost, bearer: h.accessJWT})
		assert503(t, "native POST", rec, got)
		rec, got = h.serve(h.svc.RequirePrincipal(Browser()), extReq{method: http.MethodPost, nativeCookie: h.accessJWT})
		assert503(t, "native Browser() POST", rec, got)
	})

	t.Run("provider revocation outage", func(t *testing.T) {
		h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}, loginPath: extLoginPath})
		h.revocation.set(sdkauth.RevocationFacts{}, sdkauth.ErrProviderUnavailable)
		rec, got := h.serve(h.svc.RequirePrincipal(), extReq{method: http.MethodPost, bearer: h.token})
		assert503(t, "external POST", rec, got)
		if rec, _ := h.serve(h.svc.RequirePrincipal(), extReq{bearer: h.token}); rec.Code != http.StatusOK {
			t.Errorf("external GET during an outage = %d, want 200 (default tier)", rec.Code)
		}
		rec, got = h.serve(h.svc.RequirePrincipal(Live()), extReq{bearer: h.token})
		assert503(t, "external Live() GET", rec, got)
		rec, got = h.serve(h.svc.RequirePrincipal(Browser(), Live()), extReq{extCookie: h.token})
		assert503(t, "external Browser() GET", rec, got)
		rec, got = h.serve(h.svc.RequirePrincipal(Optional(), Live()), extReq{extCookie: h.token})
		assert503(t, "external Optional() GET", rec, got)
	})

	t.Run("provider verification outage", func(t *testing.T) {
		h := newExternalHarness(t, externalSetup{})
		h.provider.err = &sdkauth.UnavailableError{Kind: sdkauth.UnavailableNetwork, Err: errors.New("jwks down")}
		rec, got := h.serve(h.svc.RequirePrincipal(), extReq{bearer: h.token})
		assert503(t, "external verification", rec, got)
	})
}

// --- Browser() denial ---

func TestExternalBrowserDenial(t *testing.T) {
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}, loginPath: extLoginPath})
	mw := h.svc.RequirePrincipal(Browser())
	rec, _ := h.serve(mw, extReq{extCookie: extJWT(extIssuer, "unknown")})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, extLoginPath+"?") || strings.Contains(loc, "recover=1") {
		t.Errorf("invalid provider cookie = %d %q, want 303 to the login path without recover", rec.Code, loc)
	}
	rec, _ = h.serve(mw, extReq{})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "recover=1") {
		t.Errorf("no credential = %d %q, want 303 with native recover", rec.Code, loc)
	}
}

// --- bundled route audit ---

func newExternalRoutes(t *testing.T, h *externalHarness, routeAuth RouteAuthentication) (http.Handler, *spyInvitationService) {
	t.Helper()
	inv := &spyInvitationService{}
	mux := web.NewWebHandler()
	mount(mux, mountDeps{
		Auth: h.svc, Invitations: inv, InviteCheck: allowInviteCheck,
		UserAdminCheck: func(context.Context, UserAdminCheckRequest) error { return nil },
		ListStrategy:   list.StrategyCursor,
		MachineGate:    func(next http.Handler) http.Handler { return next },
		Views:          stubViews{},
		RouteAuth:      routeAuth,
		Mutation:       MutationSecurity{SessionCookieName: h.svc.SessionCookieName()},
	})
	return mux, inv
}

func routeRequest(method, path, body string, mutate func(*http.Request)) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

// TestBundledEstablishedSessionSurfacesRefuseExternalTokens pins the route
// audit: surfaces needing a native session (step-up included) deny a provider
// user, while hydration, CSRF bootstrap, invitations and user administration
// admit every wired kind.
func TestBundledEstablishedSessionSurfacesRefuseExternalTokens(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	mux, _ := newExternalRoutes(t, h, RouteAuthentication{})
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+h.token) }
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/auth/methods", ""},
		{"GET", "/auth/delivery/status?receipt=r1", ""},
		{"POST", "/auth/step-up/begin", `{}`},
		{"POST", "/auth/password/change", `{}`},
		{"POST", "/auth/identifiers/email", `{}`},
		{"POST", "/auth/service-accounts", `{}`},
		{"GET", "/auth/oauth/google/link/start", ""},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, routeRequest(c.method, c.path, c.body, bearer))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("external token on %s %s = %d, want 401; body=%s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/auth/me"},
		{"GET", "/auth/csrf"},
		{"GET", "/auth/invitations/mine"},
		{"GET", "/auth/admin/users"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, routeRequest(c.method, c.path, "", bearer))
		if rec.Code != http.StatusOK {
			t.Errorf("external token on %s %s = %d, want 200; body=%s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// TestBundledFloorAdmitsExternalBecauseTheDefaultSetDoes pins the coupling: the
// floor every bundled group runs inside is the default set, so a host override
// admitting external tokens works, and delegated tokens stay refused.
func TestBundledFloorAdmitsExternalBecauseTheDefaultSetDoes(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	routes := RouteAuthentication{SessionSecurityReads: h.svc.RequireExternalToken()}.withDefaults(h.svc, false)
	if rec, _ := h.serve(routes.SessionSecurityReads, extReq{bearer: h.token}); rec.Code != http.StatusOK {
		t.Errorf("external override through the floor = %d, want 200", rec.Code)
	}
	if !h.svc.resolveSet(nil).externalToken {
		t.Error("the default set does not admit wired external tokens")
	}
}

// TestBundledPOSTRunsOneExternalRecheck: the group's Live() gate and the floor
// share the proof stamp, so a bundled mutation pays ONE local status read and
// ONE provider revocation call.
func TestBundledPOSTRunsOneExternalRecheck(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	routes := RouteAuthentication{}.withDefaults(h.svc, false)
	h.users.mu.Lock()
	h.users.gets = 0
	h.users.mu.Unlock()
	rec, got := h.serve(routes.Invitations, extReq{method: http.MethodPost, bearer: h.token})
	if rec.Code != http.StatusOK || !got.reached {
		t.Fatalf("bundled POST = %d", rec.Code)
	}
	h.users.mu.Lock()
	gets := h.users.gets
	h.users.mu.Unlock()
	if calls := h.revocation.calls.Load(); calls != 1 || gets != 1 {
		t.Errorf("CheckRevocation = %d, users.Get = %d; want 1 and 1", calls, gets)
	}
}

// TestCookieBorneExternalUserBootstrapsCSRFAndAcceptsInvitation drives the
// provider-cookie browser path end to end: /auth/csrf hands a provider-cookie
// user the double-submit token, the double-submit gate (security.go, unchanged)
// still guards a bundled cookie mutation, and the invitation accept completes.
func TestCookieBorneExternalUserBootstrapsCSRFAndAcceptsInvitation(t *testing.T) {
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}})
	mux, inv := newExternalRoutes(t, h, RouteAuthentication{})
	cookie := &http.Cookie{Name: extCookie, Value: h.token}
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, routeRequest("POST", "/auth/admin/users/"+h.userID+"/deactivate", `{}`, withCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("provider-cookie bundled mutation without CSRF = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, routeRequest("GET", "/auth/csrf", "", withCookie))
	var body struct {
		Token string `json:"csrf_token"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Token == "" {
		t.Fatalf("provider-cookie /auth/csrf = %d %s", rec.Code, rec.Body)
	}
	withCSRF := func(r *http.Request) {
		r.AddCookie(cookie)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: body.Token})
		r.Header.Set(csrfHeaderName, body.Token)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, routeRequest("POST", "/auth/admin/users/"+h.userID+"/deactivate", `{}`, withCSRF))
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("provider-cookie bundled mutation with CSRF = %d, want past the gates; body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, routeRequest("POST", "/auth/invitations/accept", `{"token":"t"}`, withCSRF))
	if rec.Code != http.StatusOK || !inv.acceptCalled {
		t.Fatalf("provider-cookie invitation accept = %d %s, want 200", rec.Code, rec.Body)
	}
}
