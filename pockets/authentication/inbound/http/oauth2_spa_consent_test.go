package authenticationhttp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const spaConsentPath = "/connect/consent"

type spaSessions struct {
	items     []session.Session
	revoked   []string
	revokeAll int
	delegated []string
}

func (s *spaSessions) ListUserSessions(_ context.Context, userID string, _ list.Request) (list.Page[session.Session], error) {
	var out []session.Session
	for _, sess := range s.items {
		if sess.UserID == userID {
			out = append(out, sess)
		}
	}
	return list.Page[session.Session]{Items: out}, nil
}

func (s *spaSessions) RevokeUserSession(_ context.Context, userID, id string) error {
	for _, sess := range s.items {
		if sess.ID == id && sess.UserID == userID {
			s.revoked = append(s.revoked, id)
			return nil
		}
	}
	return sdk.ErrNotFound
}

func (s *spaSessions) RevokeAllUserSessions(context.Context, string) error {
	s.revokeAll++
	return nil
}

func (s *spaSessions) RevokeDelegatedUserSessions(_ context.Context, userID string) error {
	s.delegated = append(s.delegated, userID)
	return nil
}

// spaRepository adds the client snapshot read the sessions list uses for
// display names.
type spaRepository struct{ *oauthHTTPRepository }

func (spaRepository) GetClient(_ context.Context, id string) (oauth2.Client, error) {
	return oauth2.Client{ID: id, Name: "Trusted app", RedirectURIs: []string{oauthHTTPRedirect}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type spaFixture struct {
	*credentialHarness
	router   *web.WebHandler
	repos    *oauthHTTPRepository
	sessions *spaSessions
}

// newSPAFixture mounts the OAuth2 and session-management routes on the
// ConsentPath lane: no OAuthViews and no browser Views.
func newSPAFixture(t *testing.T) *spaFixture {
	t.Helper()
	harness := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	repos := &oauthHTTPRepository{}
	service, err := oauth2.New(oauth2.Repositories{OAuth2: spaRepository{repos}, Users: harness.users, Sessions: harness.sess}, harness.signer, oauthHTTPResolver{}, harness.svc.Service, environment.ModeDevelopment, oauth2.Config{Issuer: delegatedIssuer, MCPResource: mcpResource, APIResource: apiResource, ConfidentialClientID: "mcp-server", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(oauthHTTPSecret))}})
	if err != nil {
		t.Fatal(err)
	}
	sessions := &spaSessions{items: []session.Session{
		{ID: harness.sessionID, UserID: harness.userID, Profile: session.ProfileFirstParty, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "delegated-1", UserID: harness.userID, Profile: session.ProfileDelegated, Delegation: session.Delegation{ClientID: oauthHTTPClientID, Resource: mcpResource}, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
	}}
	cfg := &OAuth2Config{Service: service, Sessions: sessions, ConsentPath: spaConsentPath, Limiter: ratelimiter.NewMemory(), CapabilityDescription: "Read project summaries only."}
	if err := validateOAuth2Consent(cfg, nil); err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	h := &handlers{svc: harness.svc, oauth2: cfg, mutation: MutationSecurity{AllowedOrigins: []string{delegatedIssuer}, SessionCookieName: harness.svc.SessionCookieName()}}
	mountOAuth2(clientInfoRegistrar{inner: router}, h)
	mountSessionManagement(clientInfoRegistrar{inner: router}, h)
	return &spaFixture{credentialHarness: harness, router: router, repos: repos, sessions: sessions}
}

func (f *spaFixture) serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func (f *spaFixture) get(path string, signedIn bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if signedIn {
		r.AddCookie(&http.Cookie{Name: f.svc.SessionCookieName(), Value: f.accessJWT})
	}
	return f.serve(r)
}

// post sends a signed-in JSON mutation. csrf adds the double-submit cookie and
// header; origin sets Origin.
func (f *spaFixture) post(path, body, origin string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: f.svc.SessionCookieName(), Value: f.accessJWT})
	if csrf {
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-value"})
		r.Header.Set(csrfHeaderName, "csrf-token-value")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return f.serve(r)
}

// authorize runs GET /auth/oauth2/authorize signed in and returns the consent
// request token from the ConsentPath redirect.
func (f *spaFixture) authorize(t *testing.T) string {
	t.Helper()
	w := f.get("/auth/oauth2/authorize?"+oauthHTTPAuthorizationQuery().Encode(), true)
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusSeeOther || loc.Path != spaConsentPath {
		t.Fatalf("authorize = %d %q, want 303 to %s", w.Code, w.Header().Get("Location"), spaConsentPath)
	}
	q := loc.Query()
	if len(q) != 1 || q.Get("request") == "" {
		t.Fatalf("consent redirect carries %v, want only the request token", q)
	}
	return q.Get("request")
}

func TestSPAAuthorizeRedirectsToConsentPath(t *testing.T) {
	f := newSPAFixture(t)
	token := f.authorize(t)
	if strings.Contains(token, "client-state") {
		t.Fatal("consent redirect exposes raw authorize parameters")
	}
	path := "/auth/oauth2/authorize?" + oauthHTTPAuthorizationQuery().Encode()
	w := f.get(path, false)
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusSeeOther || loc.Path != "/auth/login" || loc.Query().Get("return_to") != path {
		t.Fatalf("signed out = %d %q, want the login redirect returning to authorize", w.Code, w.Header().Get("Location"))
	}
	bad := oauthHTTPAuthorizationQuery()
	bad.Set("redirect_uri", "https://attacker.example")
	if w := f.get("/auth/oauth2/authorize?"+bad.Encode(), true); w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
		t.Fatalf("untrusted redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := f.get("/auth/oauth2/consent?request="+url.QueryEscape(token), false); w.Code != http.StatusUnauthorized || w.Header().Get("Location") != "" {
		t.Fatalf("signed-out consent read = %d %q, want a JSON 401", w.Code, w.Header().Get("Location"))
	}
}

func TestSPAConsentReadAndDecide(t *testing.T) {
	f := newSPAFixture(t)
	token := f.authorize(t)

	w := f.get("/auth/oauth2/consent?request="+url.QueryEscape(token), true)
	var model oauth2ConsentResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &model) != nil {
		t.Fatalf("consent read = %d %s", w.Code, w.Body)
	}
	want := oauth2ConsentResponse{ClientName: "Trusted app", ClientID: oauthHTTPClientID, ClientHost: "client.example.com", RedirectHost: "client.example.com", Resource: mcpResource, CapabilityDescription: "Read project summaries only.", Request: token}
	if model != want {
		t.Fatalf("consent model = %+v, want %+v", model, want)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("consent read is cacheable or leaks a referrer")
	}
	if w := f.get("/auth/oauth2/consent?request="+url.QueryEscape(token+"x"), true); w.Code != http.StatusBadRequest {
		t.Fatalf("tampered token read = %d", w.Code)
	}

	allow := `{"request":"` + token + `","decision":"allow"}`
	if w := f.post("/auth/oauth2/consent", allow, delegatedIssuer, false); w.Code != http.StatusForbidden || len(f.repos.codes) != 0 {
		t.Fatalf("approve without CSRF = %d, codes=%d", w.Code, len(f.repos.codes))
	}
	if w := f.post("/auth/oauth2/consent", allow, "https://evil.example", true); w.Code != http.StatusForbidden || len(f.repos.codes) != 0 {
		t.Fatalf("approve from a foreign origin = %d, codes=%d", w.Code, len(f.repos.codes))
	}
	if w := f.post("/auth/oauth2/consent", `{"request":"`+token+`","decision":"allow","redirect_uri":"https://attacker.example"}`, delegatedIssuer, true); w.Code != http.StatusBadRequest || len(f.repos.codes) != 0 {
		t.Fatalf("body redirect_uri = %d, want the strict body refusal", w.Code)
	}

	w = f.post("/auth/oauth2/consent", `{"request":"`+token+`","decision":"deny"}`, delegatedIssuer, true)
	var decision oauth2DecisionResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &decision) != nil {
		t.Fatalf("deny = %d %s", w.Code, w.Body)
	}
	denied, _ := url.Parse(decision.RedirectURI)
	if denied.Host != "client.example.com" || denied.Query().Get("error") != "access_denied" || denied.Query().Get("state") != "client-state" || denied.Query().Get("iss") != delegatedIssuer || denied.Query().Get("existing") != "yes" || len(f.repos.codes) != 0 {
		t.Fatalf("deny redirect = %s", decision.RedirectURI)
	}

	w = f.post("/auth/oauth2/consent", allow, delegatedIssuer, true)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &decision) != nil {
		t.Fatalf("approve = %d %s", w.Code, w.Body)
	}
	approved, _ := url.Parse(decision.RedirectURI)
	if approved.Host != "client.example.com" || approved.Path != "/callback" || approved.Query().Get("code") == "" || approved.Query().Get("state") != "client-state" || approved.Query().Get("iss") != delegatedIssuer || len(f.repos.codes) != 1 {
		t.Fatalf("approve redirect = %s", decision.RedirectURI)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("approval changed browser cookies")
	}
}

// TestSPAAndFormLanesShareTheDecisionRedirect pins that the JSON decision is
// the same URL the form lane 303s to for the same request.
func TestSPAAndFormLanesShareTheDecisionRedirect(t *testing.T) {
	spa := newSPAFixture(t)
	token := spa.authorize(t)
	var decision oauth2DecisionResponse
	w := spa.post("/auth/oauth2/consent", `{"request":"`+token+`","decision":"deny"}`, delegatedIssuer, true)
	if json.Unmarshal(w.Body.Bytes(), &decision) != nil {
		t.Fatalf("deny = %d %s", w.Code, w.Body)
	}

	form := newOAuthHTTPFixture(t)
	page := form.serve(form.request("GET", "/auth/oauth2/authorize?"+oauthHTTPAuthorizationQuery().Encode(), "", true))
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrf = c
		}
	}
	body := url.Values{"request": {form.capture.consent.RequestToken}, "decision": {"deny"}, "csrf_token": {csrf.Value}}
	r := form.request("POST", "/auth/oauth2/authorize", body.Encode(), true)
	r.AddCookie(csrf)
	r.Header.Set("Origin", delegatedIssuer)
	if got := form.serve(r).Header().Get("Location"); got != decision.RedirectURI {
		t.Fatalf("form lane %q != JSON lane %q", got, decision.RedirectURI)
	}
}

func TestSPAConnections(t *testing.T) {
	f := newSPAFixture(t)
	w := f.get("/auth/connections", true)
	var page struct {
		Items []connectionResponse `json:"items"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 2 {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	first, delegated := page.Items[0], page.Items[1]
	if first.Profile != string(session.ProfileFirstParty) || !first.Current || delegated.Profile != string(session.ProfileDelegated) || delegated.ClientID != oauthHTTPClientID || delegated.ClientName != "Trusted app" || delegated.Resource != mcpResource || delegated.Current {
		t.Fatalf("connections = %+v", page.Items)
	}
	if w := f.get("/auth/connections", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out list = %d", w.Code)
	}

	if w := f.post("/auth/connections/delegated-1/revoke", "", delegatedIssuer, false); w.Code != http.StatusForbidden || len(f.sessions.revoked) != 0 {
		t.Fatalf("revoke without CSRF = %d", w.Code)
	}
	w = f.post("/auth/connections/delegated-1/revoke", "", delegatedIssuer, true)
	if w.Code != http.StatusNoContent || len(f.sessions.revoked) != 1 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("revoke = %d revoked=%v cookies=%v", w.Code, f.sessions.revoked, w.Result().Cookies())
	}
	if w := f.post("/auth/connections/unknown/revoke", "", delegatedIssuer, true); w.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown = %d", w.Code)
	}
	if w := f.post("/auth/connections/revoke-all", "", "https://evil.example", true); w.Code != http.StatusForbidden || f.sessions.revokeAll != 0 {
		t.Fatalf("revoke-all from a foreign origin = %d", w.Code)
	}
	w = f.post("/auth/connections/revoke-all", "", delegatedIssuer, true)
	if w.Code != http.StatusNoContent || f.sessions.revokeAll != 1 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("revoke-all = %d, want 204 with the session cookies cleared", w.Code)
	}

	if w := f.post("/auth/connections/revoke-delegated", "", "https://evil.example", true); w.Code != http.StatusForbidden || len(f.sessions.delegated) != 0 {
		t.Fatalf("revoke-delegated from a foreign origin = %d", w.Code)
	}
	if w := f.post("/auth/connections/revoke-delegated", "", delegatedIssuer, false); w.Code != http.StatusForbidden || len(f.sessions.delegated) != 0 {
		t.Fatalf("revoke-delegated without CSRF = %d", w.Code)
	}
	w = f.post("/auth/connections/revoke-delegated", "", delegatedIssuer, true)
	if w.Code != http.StatusNoContent || len(f.sessions.delegated) != 1 || f.sessions.delegated[0] != f.userID || len(w.Result().Cookies()) != 0 {
		t.Fatalf("revoke-delegated = %d delegated=%v cookies=%v, want 204 for the caller with no cookie change", w.Code, f.sessions.delegated, w.Result().Cookies())
	}
}

func TestOAuth2ConsentPageHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	w := httptest.NewRecorder()
	OAuth2ConsentPageHeaders(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, spaConsentPath, nil))
	for header, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY", "Content-Security-Policy": "frame-ancestors 'none'"} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	w = httptest.NewRecorder()
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	OAuth2ConsentPageHeaders(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, spaConsentPath, nil))
	if w.Header().Get("Content-Security-Policy") != "default-src 'self'; frame-ancestors 'none'" {
		t.Fatal("helper replaced the host's own policy")
	}
}

func TestValidateOAuth2Consent(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg   OAuth2Config
		views Views
		ok    bool
	}{
		"path only":             {OAuth2Config{ConsentPath: "/consent"}, nil, true},
		"views and browser":     {OAuth2Config{Views: &oauthHTTPCapture{}}, stubViews{}, true},
		"neither":               {OAuth2Config{}, stubViews{}, false},
		"both":                  {OAuth2Config{Views: &oauthHTTPCapture{}, ConsentPath: "/consent"}, stubViews{}, false},
		"views without browser": {OAuth2Config{Views: &oauthHTTPCapture{}}, nil, false},
		"absolute path":         {OAuth2Config{ConsentPath: "https://evil.example/c"}, nil, false},
		"query":                 {OAuth2Config{ConsentPath: "/c?x=1"}, nil, false},
		"fragment":              {OAuth2Config{ConsentPath: "/c#x"}, nil, false},
		"relative":              {OAuth2Config{ConsentPath: "consent"}, nil, false},
	} {
		if err := validateOAuth2Consent(&tc.cfg, tc.views); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}
