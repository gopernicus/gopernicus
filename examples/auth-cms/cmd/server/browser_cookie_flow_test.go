package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// U7 cross-module acceptance: the whole same-site cross-origin cookie flow through
// EXPORTED host seams only. This host is a different module from the pocket, so
// nothing here can reach an internal package — it composes exactly what
// coordination-hub composes (web router + a globally installed CORS policy that opts
// in the pocket's echo header + the authentication pocket mounted under /api/v1
// with a matching RefreshCookiePath), and drives it over a real TLS server and a real
// cookie jar. The test NEVER reads a cookie value to build a request; like the browser
// it stands in for, it reads the CSRF token from the JSON body only.
//
// The pocket-internal siblings (pockets/authentication/internal/inbound/
// authentication) pin the same seams against fakes; this one pins that a HOST can
// wire them without the pocket's internals.

const (
	// apiPrefix is the host's pocket mount prefix — the PrefixRegistrar path that
	// makes refresh-cookie scoping non-trivial.
	apiPrefix = "/api/v1"

	// browserSPAOrigin is the allowlisted SPA: a sibling origin under the same
	// registrable domain (cross-origin, same-site), which is the only browser
	// topology the SameSite=Lax cookie posture supports.
	browserSPAOrigin = "https://spa.example.com"

	// csrfEchoHeader is the pocket's double-submit echo header. The sdk knows no
	// pocket header, so the HOST lists it in its CORS request-header policy.
	csrfEchoHeader = "X-CSRF-Token"

	flowEmail       = "spa-flow@example.com"
	flowPassword    = "password123456789"
	flowNewPassword = "newpassword456789"
)

// newBrowserFlowHost builds the coordination-hub-shaped composition and returns the
// running TLS server plus the built auth Service (for its exported cookie names).
func newBrowserFlowHost(t *testing.T) (*httptest.Server, *auth.Service) {
	t.Helper()

	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	// Prefixed mount: the refresh cookie must carry the FULL prefixed path or the
	// browser never sends it to /api/v1/auth/refresh (upstream evidence §2).
	cfg.RefreshCookiePath = apiPrefix + "/auth"
	// The pocket's own exact-match Origin allowlist for cookie-authenticated
	// mutations, independent of the sdk CORS allowlist below.
	cfg.AllowedOrigins = []string{browserSPAOrigin}
	// in_process delivery owns its bounded pool and needs no dispatcher; this flow
	// sends nothing, it only needs a constructible Service.
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	// A just-registered account has no verified identifier yet, and this flow proves
	// cookie/CORS/CSRF plumbing rather than the verification rail.
	cfg.RequireVerifiedEmail = false

	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	router := web.NewWebHandler(web.WithLogging(quietLog()))
	// Genuinely global CORS: one Use around the whole mux, so it also answers the
	// preflight to a method-qualified pocket route. AllowedHeaders is non-nil, so it
	// REPLACES the default list — the host opts X-CSRF-Token in explicitly.
	router.Use(web.CORSWithConfig(web.CORSConfig{
		AllowedOrigins: []string{browserSPAOrigin},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", csrfEchoHeader},
	}))
	if err := svc.Register(pocket.Mount{
		Router: pocket.PrefixRegistrar{Prefix: apiPrefix, Next: router},
		Logger: quietLog(),
	}); err != nil {
		t.Fatalf("authSvc.Register: %v", err)
	}

	srv := httptest.NewTLSServer(router)
	t.Cleanup(srv.Close)
	return srv, svc
}

// browserClient is the SPA stand-in: a TLS client with a real cookie jar that always
// sends the SPA's Origin and never inspects a stored cookie to build a request.
type browserClient struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
}

func newBrowserClient(t *testing.T, srv *httptest.Server) *browserClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := srv.Client()
	client.Jar = jar
	return &browserClient{t: t, srv: srv, client: client}
}

// do issues a credentialed request from the SPA origin and returns the response with
// its body already drained.
func (b *browserClient) do(method, path, body string, header http.Header) (*http.Response, []byte) {
	b.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, b.srv.URL+path, rdr)
	if err != nil {
		b.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Origin", browserSPAOrigin)
	req.Header.Set("Sec-Fetch-Site", "same-site")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatalf("read %s %s body: %v", method, path, err)
	}
	return resp, payload
}

// jarCookieNames reports the cookie names the jar would send to path — the browser's
// own Path-matching, not a string comparison of our own.
func (b *browserClient) jarCookieNames(path string) []string {
	b.t.Helper()
	u, err := url.Parse(b.srv.URL + path)
	if err != nil {
		b.t.Fatalf("parse %s: %v", path, err)
	}
	var names []string
	for _, c := range b.client.Jar.Cookies(u) {
		names = append(names, c.Name)
	}
	return names
}

// TestBrowserCookieFlowThroughHostSeams is the U7 definition-of-done walk: an
// allowlisted cross-origin SPA logs in over cookies, bootstraps a CSRF token from a
// no-store JSON body, clears a real preflight carrying the echo header, completes a
// browser-safe mutation without ever reading the API cookie, refreshes and hydrates
// over cookies at the PREFIXED paths, and logs out with the refresh cookie cleared at
// the same Path it was issued under.
func TestBrowserCookieFlowThroughHostSeams(t *testing.T) {
	srv, svc := newBrowserFlowHost(t)
	spa := newBrowserClient(t, srv)

	// 1. Register + login. The credentialed cross-origin response must be readable,
	//    and the refresh cookie must be scoped to the PREFIXED auth path.
	if resp, body := spa.do("POST", apiPrefix+"/auth/register",
		`{"email":"`+flowEmail+`","password":"`+flowPassword+`","display_name":"SPA"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d, want 201; body=%s", resp.StatusCode, body)
	}
	login, loginBody := spa.do("POST", apiPrefix+"/auth/login",
		`{"email":"`+flowEmail+`","password":"`+flowPassword+`"}`, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200; body=%s", login.StatusCode, loginBody)
	}
	if got := login.Header.Get("Access-Control-Allow-Origin"); got != browserSPAOrigin {
		t.Errorf("login Allow-Origin = %q, want %q", got, browserSPAOrigin)
	}
	if got := login.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("login Allow-Credentials = %q, want true", got)
	}
	if got := login.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("login Vary = %q, want it to include Origin", got)
	}
	issued := setCookie(t, login, svc.RefreshCookieName())
	if issued.Path != apiPrefix+"/auth" {
		t.Fatalf("issued refresh cookie Path = %q, want %q", issued.Path, apiPrefix+"/auth")
	}

	// The jar's own Path matching: the refresh cookie rides the prefixed auth paths
	// and nothing else.
	if !contains(spa.jarCookieNames(apiPrefix+"/auth/refresh"), svc.RefreshCookieName()) {
		t.Fatalf("refresh cookie is not sent to %s/auth/refresh; jar has %v",
			apiPrefix, spa.jarCookieNames(apiPrefix+"/auth/refresh"))
	}
	if contains(spa.jarCookieNames("/healthz"), svc.RefreshCookieName()) {
		t.Errorf("refresh cookie rides /healthz; it must be scoped to %s/auth", apiPrefix)
	}
	if contains(spa.jarCookieNames(apiPrefix+"/cms/entries"), svc.RefreshCookieName()) {
		t.Errorf("refresh cookie rides an unrelated prefixed path; it must be scoped to %s/auth", apiPrefix)
	}

	// 2. CSRF bootstrap: the SPA reads the token from the BODY (it cannot read the
	//    API-origin cookie), and the response must be uncacheable and readable.
	boot, bootBody := spa.do("GET", apiPrefix+"/auth/csrf", "", nil)
	if boot.StatusCode != http.StatusOK {
		t.Fatalf("csrf bootstrap = %d, want 200; body=%s", boot.StatusCode, bootBody)
	}
	if got := boot.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("csrf bootstrap Cache-Control = %q, want no-store", got)
	}
	if got := boot.Header.Get("Access-Control-Allow-Origin"); got != browserSPAOrigin {
		t.Errorf("csrf bootstrap Allow-Origin = %q, want %q (the SPA must read the body)", got, browserSPAOrigin)
	}
	var bootstrap struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(bootBody, &bootstrap); err != nil || bootstrap.Token == "" {
		t.Fatalf("csrf bootstrap body %q: token=%q err=%v", bootBody, bootstrap.Token, err)
	}

	// 3. The mutation's preflight, answered by the globally installed sdk CORS
	//    middleware on a route registered POST-only (the 405 that used to escape
	//    per-pattern "global" middleware).
	preflight, _ := spa.do("OPTIONS", apiPrefix+"/auth/password/change", "", http.Header{
		"Access-Control-Request-Method":  {"POST"},
		"Access-Control-Request-Headers": {"content-type, x-csrf-token"},
	})
	if preflight.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight = %d, want 204", preflight.StatusCode)
	}
	if got := preflight.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), strings.ToLower(csrfEchoHeader)) {
		t.Fatalf("preflight Allow-Headers = %q, want it to include %s", got, csrfEchoHeader)
	}
	if got := preflight.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("preflight Allow-Credentials = %q, want true", got)
	}

	// 4. The browser-safe mutation itself: jar cookies the SPA cannot read plus the
	//    echo header carrying the body token.
	change, changeBody := spa.do("POST", apiPrefix+"/auth/password/change",
		`{"current_password":"`+flowPassword+`","new_password":"`+flowNewPassword+`"}`,
		http.Header{csrfEchoHeader: {bootstrap.Token}})
	if change.StatusCode != http.StatusOK {
		t.Fatalf("password change = %d, want 200; body=%s", change.StatusCode, changeBody)
	}

	// 5. Cookie-driven refresh at the prefixed path — the flow that is dead without
	//    RefreshCookiePath. No body: the presented credential is the cookie.
	refresh, refreshBody := spa.do("POST", apiPrefix+"/auth/refresh", "", nil)
	if refresh.StatusCode != http.StatusOK {
		t.Fatalf("cookie refresh = %d, want 200; body=%s", refresh.StatusCode, refreshBody)
	}
	if rotated := setCookie(t, refresh, svc.RefreshCookieName()); rotated.Path != apiPrefix+"/auth" {
		t.Fatalf("rotated refresh cookie Path = %q, want %q", rotated.Path, apiPrefix+"/auth")
	}

	// 6. Session hydration: the cookie client asks who it is.
	me, meBody := spa.do("GET", apiPrefix+"/auth/me", "", nil)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me = %d, want 200; body=%s", me.StatusCode, meBody)
	}
	if got := me.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("me Cache-Control = %q, want no-store", got)
	}
	var profile struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(meBody, &profile); err != nil {
		t.Fatalf("decode me body %q: %v", meBody, err)
	}
	if profile.Email != flowEmail || profile.ID == "" {
		t.Fatalf("me body = %+v, want the signed-in %s with an id", profile, flowEmail)
	}

	// 7. Logout clears BOTH cookies, and the refresh cookie is cleared at exactly the
	//    Path it was issued under — a mismatch would leave a live refresh token in the
	//    browser after sign-out.
	out, outBody := spa.do("POST", apiPrefix+"/auth/logout", "", nil)
	if out.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d, want 200; body=%s", out.StatusCode, outBody)
	}
	cleared := setCookie(t, out, svc.RefreshCookieName())
	if cleared.Path != apiPrefix+"/auth" {
		t.Fatalf("cleared refresh cookie Path = %q, want %q (issue and clear must match)", cleared.Path, apiPrefix+"/auth")
	}
	if cleared.MaxAge >= 0 && cleared.Value != "" {
		t.Fatalf("logout did not expire the refresh cookie: %+v", cleared)
	}
	if contains(spa.jarCookieNames(apiPrefix+"/auth/refresh"), svc.RefreshCookieName()) {
		t.Error("the refresh cookie survived logout in the jar")
	}
	if after, body := spa.do("GET", apiPrefix+"/auth/me", "", nil); after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401; body=%s", after.StatusCode, body)
	}
}

// TestBrowserFlowRejectsUnallowlistedOrigin pins the diagnosable half of the flow: an
// origin the host's CORS policy does NOT allow cannot bootstrap, and the sdk writes no
// CORS headers for it, so the browser refuses to expose the response at all.
func TestBrowserFlowRejectsUnallowlistedOrigin(t *testing.T) {
	srv, _ := newBrowserFlowHost(t)
	spa := newBrowserClient(t, srv)

	if resp, body := spa.do("POST", apiPrefix+"/auth/register",
		`{"email":"other-`+flowEmail+`","password":"`+flowPassword+`","display_name":"SPA"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d, want 201; body=%s", resp.StatusCode, body)
	}
	if resp, body := spa.do("POST", apiPrefix+"/auth/login",
		`{"email":"other-`+flowEmail+`","password":"`+flowPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200; body=%s", resp.StatusCode, body)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+apiPrefix+"/auth/csrf", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := spa.client.Do(req)
	if err != nil {
		t.Fatalf("csrf from an unallowlisted origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf from an unallowlisted origin = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for an origin the host does not allow", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin even on a rejected origin", got)
	}
}

// setCookie returns the named cookie a response set, failing the test when absent.
func setCookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("response set no %q cookie (set: %v)", name, resp.Cookies())
	return nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
