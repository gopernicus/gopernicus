package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const proofClientID = "https://client.example.test/oauth.json"
const proofCallback = "https://client.example.test/callback"
const proofSecret = "local integration test confidential secret"
const proofVerifier = "01234567890123456789012345678901234567890123456789"

type proofClients struct{}

func (proofClients) Resolve(_ context.Context, id string) (oauth2.Client, error) {
	if id != proofClientID {
		return oauth2.Client{}, oauth2.ErrInvalidClient
	}
	return oauth2.Client{ID: id, Name: "OAuth proof client", RedirectURIs: []string{proofCallback}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type oauthHTTPProof struct {
	t               *testing.T
	server          *httptest.Server
	browser, client *http.Client
	svc             *auth.Components
	repos           auth.Repositories
	userID          string
}

func newOAuthHTTPProof(t *testing.T, enabled bool) *oauthHTTPProof {
	t.Helper()
	router := web.NewWebHandler()
	srv := httptest.NewTLSServer(router)
	t.Cleanup(srv.Close)
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.RequireVerifiedEmail = false
	cfg.SessionCookie.Secure = true
	cfg.AllowedOrigins = []string{srv.URL}
	repos := authmem.New().Repositories()
	opts := cfg.options()
	if enabled {
		opts = append(opts, auth.WithOAuth2(auth.OAuth2Config{Server: oauth2.Config{Issuer: srv.URL, MCPResource: srv.URL + "/mcp", APIResource: srv.URL + "/api", ConfidentialClientID: "proof-mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(proofSecret))}}, Clients: proofClients{}, Views: cfg.Views.(inbound.OAuthViews)}))
	}
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog()}); err != nil {
		t.Fatal(err)
	}
	who := func(w http.ResponseWriter, r *http.Request) {
		id, ok := svc.Authentication.CurrentUser(r.Context())
		if !ok {
			w.WriteHeader(401)
			return
		}
		sid, _ := svc.Authentication.CurrentSessionID(r.Context())
		web.RespondJSON(w, 200, map[string]string{"user_id": id, "session_id": sid})
	}
	router.Handle("GET", "/mcp", who, svc.HTTP.RequirePrincipal(inbound.Audience(srv.URL+"/mcp")))
	router.Handle("GET", "/api", who, svc.HTTP.RequirePrincipal(inbound.Audience(srv.URL+"/api")))
	browser := srv.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	browser.Jar, _ = cookiejar.New(nil)
	client := srv.Client()
	client.CheckRedirect = browser.CheckRedirect
	h := &oauthHTTPProof{t: t, server: srv, browser: browser, client: client, svc: svc, repos: repos}
	u, err := svc.Authentication.Register(context.Background(), "oauth-proof@example.test", flowPassword, "OAuth proof")
	if err != nil {
		t.Fatal(err)
	}
	h.userID = u.ID
	return h
}

func (h *oauthHTTPProof) do(client *http.Client, method, path, body, contentType, bearer string, basic bool) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if client == h.browser {
		req.Header.Set("Origin", h.server.URL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if basic {
		req.SetBasicAuth("proof-mcp", proofSecret)
	}
	res, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return res, data
}
func (h *oauthHTTPProof) login() {
	h.t.Helper()
	res, _ := h.do(h.browser, "POST", "/auth/login", `{"email":"oauth-proof@example.test","password":"`+flowPassword+`"}`, "application/json", "", false)
	if res.StatusCode != 200 {
		h.t.Fatalf("web login status %d", res.StatusCode)
	}
}
func hidden(t *testing.T, body []byte, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"[^>]*value="([^"]*)"`)
	matches := pattern.FindSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("missing form field %s", name)
	}
	return html.UnescapeString(string(matches[1]))
}
func (h *oauthHTTPProof) authorize() string {
	h.t.Helper()
	digest := sha256.Sum256([]byte(proofVerifier))
	q := url.Values{"client_id": {proofClientID}, "redirect_uri": {proofCallback}, "resource": {h.server.URL + "/mcp"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "state": {"original-state"}}
	res, body := h.do(h.browser, "GET", "/auth/oauth2/authorize?"+q.Encode(), "", "", "", false)
	if res.StatusCode != 200 {
		h.t.Fatalf("consent status %d", res.StatusCode)
	}
	form := url.Values{"request": {hidden(h.t, body, "request")}, "decision": {"allow"}, "csrf_token": {hidden(h.t, body, "csrf_token")}}
	res, _ = h.do(h.browser, "POST", "/auth/oauth2/authorize", form.Encode(), "application/x-www-form-urlencoded", "", false)
	if res.StatusCode != 303 && res.StatusCode != 302 {
		h.t.Fatalf("approve status %d", res.StatusCode)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != proofCallback || loc.Query().Get("state") != "original-state" || loc.Query().Get("iss") != h.server.URL {
		h.t.Fatal("authorization response lost its binding")
	}
	code := loc.Query().Get("code")
	if code == "" {
		h.t.Fatal("missing code")
	}
	return code
}
func (h *oauthHTTPProof) token(form url.Values, basic bool) (oauth2.TokenResponse, int) {
	h.t.Helper()
	res, body := h.do(h.client, "POST", "/auth/oauth2/token", form.Encode(), "application/x-www-form-urlencoded", "", basic)
	if len(res.Cookies()) != 0 {
		h.t.Fatal("OAuth token response wrote browser cookies")
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		h.t.Fatal("token response may be cached")
	}
	var pair oauth2.TokenResponse
	if res.StatusCode == 200 {
		if err := json.Unmarshal(body, &pair); err != nil {
			h.t.Fatal(err)
		}
	}
	return pair, res.StatusCode
}
func (h *oauthHTTPProof) redeem(code, verifier string) (oauth2.TokenResponse, int) {
	return h.token(url.Values{"grant_type": {oauth2.GrantAuthorizationCode}, "client_id": {proofClientID}, "redirect_uri": {proofCallback}, "resource": {h.server.URL + "/mcp"}, "code": {code}, "code_verifier": {verifier}}, false)
}
func (h *oauthHTTPProof) connect() oauth2.TokenResponse {
	h.t.Helper()
	pair, status := h.redeem(h.authorize(), proofVerifier)
	if status != 200 {
		h.t.Fatalf("redeem status %d", status)
	}
	return pair
}
func (h *oauthHTTPProof) refresh(pair oauth2.TokenResponse) (oauth2.TokenResponse, int) {
	return h.token(url.Values{"grant_type": {oauth2.GrantRefreshToken}, "client_id": {proofClientID}, "resource": {h.server.URL + "/mcp"}, "refresh_token": {pair.RefreshToken}}, false)
}
func (h *oauthHTTPProof) resource(path, token string, want int) {
	h.t.Helper()
	res, _ := h.do(h.client, "GET", path, "", "", token, false)
	if res.StatusCode != want {
		h.t.Fatalf("resource %s status %d, want %d", path, res.StatusCode, want)
	}
}
func (h *oauthHTTPProof) identity(pair oauth2.TokenResponse) oauth2.AccessToken {
	h.t.Helper()
	token, err := h.svc.Authentication.VerifyOAuth2AccessToken(context.Background(), pair.AccessToken)
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}
func (h *oauthHTTPProof) inventory() []session.Session {
	h.t.Helper()
	page, err := h.repos.SessionManagement.ListByUser(context.Background(), h.userID, list.Request{})
	if err != nil {
		h.t.Fatal(err)
	}
	return page.Items
}
func (h *oauthHTTPProof) revokeFromBrowser(path string) {
	h.t.Helper()
	res, body := h.do(h.browser, "GET", "/auth/sessions", "", "", "", false)
	if res.StatusCode != 200 {
		h.t.Fatalf("sessions page status %d", res.StatusCode)
	}
	form := url.Values{"csrf_token": {hidden(h.t, body, "csrf_token")}}
	res, _ = h.do(h.browser, "POST", path, form.Encode(), "application/x-www-form-urlencoded", "", false)
	if res.StatusCode != 303 {
		h.t.Fatalf("session revoke status %d", res.StatusCode)
	}
}

func TestOAuth2HTTPIndependentSessions(t *testing.T) {
	h := newOAuthHTTPProof(t, true)
	h.login()
	initial := h.inventory()
	if len(initial) != 1 || !initial[0].FirstParty() {
		t.Fatal("web login did not create W")
	}
	webID := initial[0].ID
	code := h.authorize()
	if _, status := h.redeem(code, strings.Repeat("x", 43)); status != 400 {
		t.Fatalf("wrong verifier status %d", status)
	}
	a, status := h.redeem(code, proofVerifier)
	if status != 200 {
		t.Fatalf("code status %d", status)
	}
	if _, status = h.redeem(code, proofVerifier); status != 400 {
		t.Fatal("authorization code reused")
	}
	b := h.connect()
	aid, bid := h.identity(a).SessionID, h.identity(b).SessionID
	if aid == bid || aid == webID || bid == webID || len(h.inventory()) != 3 {
		t.Fatal("connections reused browser or each other")
	}
	h.resource("/mcp", a.AccessToken, 200)
	h.resource("/api", a.AccessToken, 401)
	api, status := h.token(url.Values{"grant_type": {oauth2.GrantTokenExchange}, "subject_token": {a.AccessToken}, "subject_token_type": {oauth2.AccessTokenType}, "resource": {h.server.URL + "/api"}}, true)
	if status != 200 {
		t.Fatalf("exchange status %d", status)
	}
	apiID := h.identity(api)
	if apiID.SessionID != aid || apiID.OriginClientID != proofClientID || apiID.ActorID != "proof-mcp" {
		t.Fatal("exchange lost grant/actor")
	}
	h.resource("/api", api.AccessToken, 200)
	h.resource("/mcp", api.AccessToken, 401)
	h.resource("/auth/methods", a.AccessToken, 401)
	for _, pair := range []oauth2.TokenResponse{a, api} {
		res, body := h.do(h.client, "POST", "/auth/oauth2/introspect", url.Values{"token": {pair.AccessToken}}.Encode(), "application/x-www-form-urlencoded", "", true)
		var info oauth2.Introspection
		if err := json.Unmarshal(body, &info); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || info.Active != (pair.AccessToken == a.AccessToken) {
			t.Fatal("introspection crossed audience")
		}
	}
	h.revokeFromBrowser("/auth/sessions/" + aid + "/revoke")
	h.resource("/mcp", a.AccessToken, 401)
	h.resource("/api", api.AccessToken, 401)
	h.resource("/mcp", b.AccessToken, 200)
	if _, status = h.refresh(a); status != 400 {
		t.Fatal("revoked A refreshed")
	}
	if len(h.inventory()) != 2 {
		t.Fatal("disconnect changed W or B")
	}
	// Logging out the browser must not revoke B, which can still rotate.
	res, _ := h.do(h.browser, "POST", "/auth/logout", `{}`, "application/json", "", false)
	if res.StatusCode != 200 && res.StatusCode != 204 {
		t.Fatalf("logout status %d", res.StatusCode)
	}
	b, status = h.refresh(b)
	if status != 200 {
		t.Fatalf("B refresh after W logout status %d", status)
	}
	h.resource("/mcp", b.AccessToken, 200)
	if len(h.inventory()) != 1 {
		t.Fatal("web logout changed independent B")
	}
	h.login()
	pending := h.authorize()
	h.revokeFromBrowser("/auth/sessions/revoke-all")
	if len(h.inventory()) != 0 {
		t.Fatal("global revoke left sessions")
	}
	if _, status = h.redeem(pending, proofVerifier); status != 400 {
		t.Fatal("pending approval resurrected session after global revoke")
	}
	h.resource("/mcp", b.AccessToken, 401)
}

func TestOAuth2HTTPRefreshReplayRevokesOnlyConnection(t *testing.T) {
	h := newOAuthHTTPProof(t, true)
	h.login()
	a, b := h.connect(), h.connect()
	old := a
	for range 3 {
		var status int
		a, status = h.refresh(a)
		if status != 200 {
			t.Fatalf("refresh status %d", status)
		}
	}
	if _, status := h.refresh(old); status != 400 {
		t.Fatal("spent refresh replay accepted")
	}
	h.resource("/mcp", a.AccessToken, 401)
	h.resource("/mcp", b.AccessToken, 200)
	if len(h.inventory()) != 2 {
		t.Fatal("replay revoked sibling or browser")
	}
}

func TestOAuth2DisabledSurface(t *testing.T) {
	h := newOAuthHTTPProof(t, false)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/auth/oauth2/authorize", "/auth/sessions"} {
		res, _ := h.do(h.client, "GET", path, "", "", "", false)
		if res.StatusCode != 404 {
			t.Fatalf("disabled %s status %d", path, res.StatusCode)
		}
	}
	for _, path := range []string{"/auth/oauth2/token", "/auth/oauth2/revoke", "/auth/oauth2/introspect"} {
		res, _ := h.do(h.client, "POST", path, "", "", "", false)
		if res.StatusCode != 404 {
			t.Fatalf("disabled %s status %d", path, res.StatusCode)
		}
	}
}

func TestOAuth2IncompleteWiringFailsBeforeServing(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	for _, missing := range []string{"oauth_store", "session_management", "clients", "consent_views", "browser_views", "confidential_secret"} {
		t.Run(missing, func(t *testing.T) {
			repos := authmem.New().Repositories()
			feature := auth.OAuth2Config{Server: oauth2.Config{Issuer: "https://host.example", MCPResource: "https://host.example/mcp", APIResource: "https://host.example/api", ConfidentialClientID: "proof-mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(proofSecret))}}, Clients: proofClients{}, Views: cfg.Views.(inbound.OAuthViews)}
			local := cfg
			switch missing {
			case "oauth_store":
				repos.OAuth2 = nil
			case "session_management":
				repos.SessionManagement = nil
			case "clients":
				var nilClient *proofClients
				feature.Clients = nilClient
			case "consent_views":
				feature.Views = nil
			case "browser_views":
				local.Views = nil
				local.HTMLPolicy = nil
			case "confidential_secret":
				feature.Server.ConfidentialSecretHashes = nil
			}
			_, err := auth.New(repos, local.TokenSigner, local.RuntimeMode, local.DeliveryMode, append(local.options(), auth.WithOAuth2(feature))...)
			if err == nil {
				t.Fatal("incomplete OAuth host admitted")
			}
		})
	}
}
