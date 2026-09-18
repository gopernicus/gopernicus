package authenticationhttp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	oauthHTTPClientID = "https://client.example.com/metadata.json"
	oauthHTTPRedirect = "https://client.example.com/callback?existing=yes"
	oauthHTTPSecret   = "test+secret:with spaces"
)

type oauthHTTPRepository struct {
	oauth2.Repository
	codes []oauth2.Code
}

func (s *oauthHTTPRepository) PutClient(context.Context, oauth2.Client) error { return nil }

func (s *oauthHTTPRepository) CreateCode(_ context.Context, code oauth2.Code, _ string) error {
	s.codes = append(s.codes, code)
	return nil
}
func (s *oauthHTTPRepository) GetCode(context.Context, string) (oauth2.Code, error) {
	return oauth2.Code{}, oauth2.ErrInvalidGrant
}

type oauthHTTPResolver struct{}

func (oauthHTTPResolver) Resolve(_ context.Context, id string) (oauth2.Client, error) {
	if id != oauthHTTPClientID {
		return oauth2.Client{}, oauth2.ErrInvalidClient
	}
	return oauth2.Client{ID: id, Name: "Trusted app", RedirectURIs: []string{oauthHTTPRedirect}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type oauthHTTPCapture struct{ consent OAuthConsentPage }

func (v *oauthHTTPCapture) OAuthConsent(m OAuthConsentPage) web.Renderer {
	v.consent = m
	return stubRenderer{"oauth_consent"}
}
func (v *oauthHTTPCapture) Sessions(SessionsPage) web.Renderer { return stubRenderer{"sessions"} }

type oauthHTTPFixture struct {
	*credentialHarness
	router  *web.WebHandler
	cfg     *OAuth2Config
	repos   *oauthHTTPRepository
	capture *oauthHTTPCapture
}

func newOAuthHTTPFixture(t *testing.T, issuer ...string) *oauthHTTPFixture {
	t.Helper()
	canonical := delegatedIssuer
	if len(issuer) > 0 {
		canonical = issuer[0]
	}
	harness := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: canonical})
	repos := &oauthHTTPRepository{}
	service, err := oauth2.New(oauth2.Repositories{OAuth2: repos, Users: harness.users, Sessions: harness.sess}, harness.signer, oauthHTTPResolver{}, harness.svc.Service, environment.ModeDevelopment, oauth2.Config{Issuer: canonical, MCPResource: mcpResource, APIResource: apiResource, ConfidentialClientID: "mcp-server", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(oauthHTTPSecret))}})
	if err != nil {
		t.Fatal(err)
	}
	capture := &oauthHTTPCapture{}
	cfg := &OAuth2Config{Service: service, Views: capture, Limiter: ratelimiter.NewMemory(), CapabilityDescription: "Read project summaries only."}
	router := web.NewWebHandler()
	mountOAuth2(clientInfoRegistrar{inner: router}, &handlers{svc: harness.svc, views: stubViews{}, oauth2: cfg, mutation: MutationSecurity{AllowedOrigins: []string{delegatedIssuer}, SessionCookieName: harness.svc.SessionCookieName()}})
	return &oauthHTTPFixture{credentialHarness: harness, router: router, cfg: cfg, repos: repos, capture: capture}
}
func (f *oauthHTTPFixture) request(method, path, body string, withWebCookie bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if withWebCookie {
		r.AddCookie(&http.Cookie{Name: f.svc.SessionCookieName(), Value: f.accessJWT})
	}
	return r
}
func (f *oauthHTTPFixture) serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}
func oauthHTTPBasic(r *http.Request, secret string) {
	r.SetBasicAuth(url.QueryEscape("mcp-server"), url.QueryEscape(secret))
}
func assertOAuthHTTPError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("non-protocol error: %d %s", w.Code, w.Body)
	}
	if w.Code != status || result.Error != code {
		t.Fatalf("response=%d %s, want %d %s", w.Code, w.Body, status, code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("OAuth error was cacheable")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("protocol error mutated cookies")
	}
}
func oauthHTTPAuthorizationQuery() url.Values {
	digest := sha256.Sum256([]byte(strings.Repeat("v", 43)))
	return url.Values{"response_type": {"code"}, "client_id": {oauthHTTPClientID}, "redirect_uri": {oauthHTTPRedirect}, "resource": {mcpResource}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "state": {"client-state"}}
}

func TestOAuthHTTPMetadataUsesCanonicalIssuerAndOnlyImplementedFeatures(t *testing.T) {
	f := newOAuthHTTPFixture(t, delegatedIssuer+"/tenant")
	r := f.request("GET", "/.well-known/oauth-authorization-server/tenant", "", false)
	r.Host = "attacker.example"
	w := f.serve(r)
	var metadata map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || metadata["issuer"] != delegatedIssuer+"/tenant" || metadata["token_endpoint"] != delegatedIssuer+"/auth/oauth2/token" || metadata["client_id_metadata_document_supported"] != true {
		t.Fatalf("metadata: %d %s", w.Code, w.Body)
	}
	for _, field := range []string{"registration_endpoint", "scopes_supported", "jwks_uri"} {
		if _, ok := metadata[field]; ok {
			t.Fatalf("advertised unsupported %s", field)
		}
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("discovery minted cookies")
	}
	if w := f.serve(f.request("POST", "/auth/oauth2/register", "", false)); w.Code != 404 {
		t.Fatalf("DCR unexpectedly mounted: %d", w.Code)
	}
}

func TestOAuthHTTPStrictTokenFormAndPublicClientBoundary(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	base := "grant_type=authorization_code&client_id=" + url.QueryEscape(oauthHTTPClientID) + "&resource=" + url.QueryEscape(mcpResource)
	for _, tc := range []struct {
		name, path, body, content, auth string
		status                          int
		code                            string
	}{
		{"json", "/auth/oauth2/token", `{"grant_type":"authorization_code"}`, "application/json", "", 400, "invalid_request"},
		{"duplicate-resource", "/auth/oauth2/token", base + "&resource=other", "", "", 400, "invalid_request"},
		{"query-smuggling", "/auth/oauth2/token?client_id=other", base, "", "", 400, "invalid_request"},
		{"body-secret", "/auth/oauth2/token", base + "&client_secret=", "", "", 400, "invalid_client"},
		{"bearer-client-auth", "/auth/oauth2/token", base, "", "Bearer invalid", 401, "invalid_client"},
		{"scope", "/auth/oauth2/token", base + "&scope=admin", "", "", 400, "invalid_scope"},
		{"audience", "/auth/oauth2/token", base + "&audience=other", "", "", 400, "invalid_target"},
		{"unknown-grant", "/auth/oauth2/token", "grant_type=client_credentials", "", "", 400, "unsupported_grant_type"},
		{"body-cap", "/auth/oauth2/token", "token=" + strings.Repeat("x", maxOAuth2BodyBytes), "", "", 413, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.request("POST", tc.path, tc.body, true)
			if tc.content != "" {
				r.Header.Set("Content-Type", tc.content)
			}
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			assertOAuthHTTPError(t, f.serve(r), tc.status, tc.code)
		})
	}
}

func TestOAuthHTTPIntrospectionRequiresFreshConfidentialProof(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	token, _ := f.delegatedToken(t, "introspection-connection")
	body := url.Values{"token": {token}}.Encode()
	for _, tc := range []struct{ name, secret, extra string }{
		{"missing", "", ""}, {"wrong", "bad-secret", ""}, {"mixed", oauthHTTPSecret, "&client_id=mcp-server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.request("POST", "/auth/oauth2/introspect", body+tc.extra, true)
			if tc.secret != "" {
				oauthHTTPBasic(r, tc.secret)
			}
			w := f.serve(r)
			assertOAuthHTTPError(t, w, 401, "invalid_client")
			if w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing client auth challenge")
			}
		})
	}
	r := f.request("POST", "/auth/oauth2/introspect", body, true)
	oauthHTTPBasic(r, oauthHTTPSecret)
	w := f.serve(r)
	var active oauth2.Introspection
	if err := json.Unmarshal(w.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !active.Active || active.SessionID != "introspection-connection" || active.Audience != mcpResource {
		t.Fatalf("introspection: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("introspection changed web cookies")
	}
	if err := f.sess.Delete(t.Context(), "introspection-connection"); err != nil {
		t.Fatal(err)
	}
	r = f.request("POST", "/auth/oauth2/introspect", body, false)
	oauthHTTPBasic(r, oauthHTTPSecret)
	w = f.serve(r)
	if w.Code != 200 || w.Body.String() != `{"active":false}` {
		t.Fatalf("revocation not immediate: %d %s", w.Code, w.Body)
	}
}

func TestOAuthHTTPAuthorizationRequiresWebSessionAndBoundCSRFConsent(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	path := "/auth/oauth2/authorize?" + oauthHTTPAuthorizationQuery().Encode()
	w := f.serve(f.request("GET", path, "", false))
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != 303 || location.Query().Get("return_to") != path {
		t.Fatalf("login did not retain OAuth request: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = f.serve(f.request("GET", path, "", true))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "oauth_consent") || f.capture.consent.RequestToken == "" || f.capture.consent.CapabilityDescription != "Read project summaries only." {
		t.Fatalf("consent page: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self' https://client.example.com;") {
		t.Fatal("consent page cannot return to registered client")
	}
	var csrf *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		} else {
			t.Fatalf("consent created credential cookie %s", cookie.Name)
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF cookie")
	}
	form := url.Values{"request": {f.capture.consent.RequestToken}, "decision": {"allow"}, "csrf_token": {csrf.Value}}
	r := f.request("POST", "/auth/oauth2/authorize", form.Encode(), true)
	r.Header.Set("Origin", delegatedIssuer)
	w = f.serve(r)
	assertOAuthHTTPError(t, w, 403, "access_denied")
	if len(f.repos.codes) != 0 {
		t.Fatal("missing CSRF created a code")
	}
	form.Set("decision", "deny")
	r = f.request("POST", "/auth/oauth2/authorize", form.Encode(), true)
	r.AddCookie(csrf)
	r.Header.Set("Origin", delegatedIssuer)
	w = f.serve(r)
	location, err = url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != 303 || location.Query().Get("error") != "access_denied" || location.Query().Get("state") != "client-state" || location.Query().Get("iss") != delegatedIssuer || location.Query().Get("existing") != "yes" {
		t.Fatalf("denial redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	if len(f.repos.codes) != 0 || len(w.Result().Cookies()) != 0 {
		t.Fatal("denial created grant or cookies")
	}
	form.Set("decision", "allow")
	r = f.request("POST", "/auth/oauth2/authorize", form.Encode(), true)
	r.AddCookie(csrf)
	r.Header.Set("Origin", delegatedIssuer)
	w = f.serve(r)
	location, err = url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != 303 || location.Query().Get("code") == "" || len(f.repos.codes) != 1 {
		t.Fatalf("approval redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("approval changed browser session")
	}
	bad := oauthHTTPAuthorizationQuery()
	bad.Set("redirect_uri", "https://attacker.example")
	w = f.serve(f.request("GET", "/auth/oauth2/authorize?"+bad.Encode(), "", true))
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatalf("untrusted redirect followed: %d %s", w.Code, w.Header())
	}
}

func TestOAuthHTTPLimiterFailsClosed(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	f.cfg.Limiter = failedLimiter{err: errors.New("limiter unavailable")}
	w := f.serve(f.request("POST", "/auth/oauth2/token", "grant_type=client_credentials", false))
	assertOAuthHTTPError(t, w, 503, "temporarily_unavailable")
	f.cfg.Limiter = denyLimiter{}
	w = f.serve(f.request("POST", "/auth/oauth2/token", "grant_type=client_credentials", false))
	assertOAuthHTTPError(t, w, 429, "temporarily_unavailable")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("rate denial omitted retry guidance")
	}
}

func TestOAuthHTTPExchangeIssuesOnlyAPIProofAndRejectsExtraAudience(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	token, _ := f.delegatedToken(t, "exchange-connection")
	form := url.Values{"grant_type": {oauth2.GrantTokenExchange}, "subject_token": {token}, "subject_token_type": {oauth2.AccessTokenType}, "resource": {apiResource}}
	r := f.request("POST", "/auth/oauth2/token", form.Encode(), true)
	w := f.serve(r)
	assertOAuthHTTPError(t, w, 401, "invalid_client")
	r = f.request("POST", "/auth/oauth2/token", form.Encode(), true)
	oauthHTTPBasic(r, oauthHTTPSecret)
	w = f.serve(r)
	var response oauth2.TokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.AccessToken == "" || response.RefreshToken != "" || response.IssuedTokenType != oauth2.AccessTokenType {
		t.Fatalf("exchange response: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("exchange modified web cookies")
	}
	claims, err := f.signer.Verify(response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	audiences, _ := claims["aud"].([]any)
	actor, _ := claims["act"].(map[string]any)
	if len(audiences) != 1 || audiences[0] != apiResource || claims["session_id"] != "exchange-connection" || claims["client_id"] != "mcp-server" || claims["origin_client_id"] != oauthHTTPClientID || actor["sub"] != "mcp-server" {
		t.Fatalf("exchanged bindings: %+v", claims)
	}
	r = f.request("POST", "/auth/oauth2/introspect", url.Values{"token": {response.AccessToken}}.Encode(), false)
	oauthHTTPBasic(r, oauthHTTPSecret)
	w = f.serve(r)
	if w.Code != 200 || w.Body.String() != `{"active":false}` {
		t.Fatalf("MCP introspected API audience: %d %s", w.Code, w.Body)
	}
	form.Set("audience", mcpResource)
	r = f.request("POST", "/auth/oauth2/token", form.Encode(), false)
	oauthHTTPBasic(r, oauthHTTPSecret)
	assertOAuthHTTPError(t, f.serve(r), 400, "invalid_target")
}

func TestOAuthHTTPRejectsUnsupportedAuthorizationSemantics(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	for _, tc := range []struct{ name, value, code string }{
		{"response_mode", "fragment", "invalid_request"}, {"request", "signed-request-object", "invalid_request"}, {"request_uri", "https://attacker.example/request", "invalid_request"}, {"authorization_details", "[]", "invalid_request"}, {"audience", apiResource, "invalid_target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := oauthHTTPAuthorizationQuery()
			form.Set(tc.name, tc.value)
			w := f.serve(f.request("GET", "/auth/oauth2/authorize?"+form.Encode(), "", true))
			assertOAuthHTTPError(t, w, 400, tc.code)
			if w.Header().Get("Location") != "" {
				t.Fatal("unsupported request redirected")
			}
		})
	}
}

func TestOAuthHTTPPublicRevokeUnknownTokenIsIdempotentAndPreservesCookies(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	form := url.Values{"client_id": {oauthHTTPClientID}, "token": {"unknown-access"}, "token_type_hint": {"access_token"}}
	for range 2 {
		w := f.serve(f.request("POST", "/auth/oauth2/revoke", form.Encode(), true))
		if w.Code != 200 || len(w.Result().Cookies()) != 0 {
			t.Fatalf("unknown revoke changed browser: %d %s", w.Code, w.Body)
		}
	}
	if _, err := f.sess.Get(t.Context(), f.sessionID); err != nil {
		t.Fatalf("OAuth revoke affected web session: %v", err)
	}
}

func TestOAuthConsentPolicyAllowsOnlyValidatedCallbackOrigin(t *testing.T) {
	for _, callback := range []string{"https://client.example/callback?state=hidden", "http://localhost:8082/callback", "http://[::1]:8082/callback"} {
		w := httptest.NewRecorder()
		if !writeOAuth2ConsentSecurity(w, nil, "test-nonce", callback) {
			t.Fatal("valid callback refused")
		}
		u, _ := url.Parse(callback)
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "form-action 'self' "+u.Scheme+"://"+u.Host+";") || strings.Contains(csp, "state=hidden") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "base-uri 'none'") || !strings.Contains(csp, "'nonce-test-nonce'") {
			t.Fatalf("unsafe consent policy: %s", csp)
		}
		if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("consent lost privacy headers")
		}
	}
	for _, callback := range []string{"https://*.example/callback", "https://example;script-src:80/callback", "https://user@example/callback", "javascript:alert(1)"} {
		if writeOAuth2ConsentSecurity(httptest.NewRecorder(), nil, "", callback) {
			t.Fatalf("unsafe CSP source admitted: %s", callback)
		}
	}
	w := httptest.NewRecorder()
	writeHTMLSecurity(w, nil, "")
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self';") {
		t.Fatal("ordinary pages changed")
	}
}
