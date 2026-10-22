package authenticationhttp

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const hostileOrigin = "https://evil.example"

// mutationGateRequest builds a request for the exported gate: cookie marks a
// browser session, origin and site set the browser's fetch metadata.
func mutationGateRequest(method, origin, site string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(method, "/host/resource", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

// TestRequireBrowserSafeMutationGate pins the exported gate's method and
// credential matrix under the adapter's own browser policy.
func TestRequireBrowserSafeMutationGate(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	adapter, err := New(service.Service, environment.ModeDevelopment,
		WithBrowser(BrowserConfig{AllowedOrigins: []string{spaOrigin}}),
		WithAuthenticatorPolicy(AuthenticatorPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: adapter.SessionCookieName(), Value: "opaque"}
	csrfCookie := &http.Cookie{Name: csrfCookieName, Value: "double-submit"}

	cases := []struct {
		name     string
		request  *http.Request
		token    string
		bearer   bool
		reached  bool
		wantCode string
	}{
		{name: "cross-site cookie POST", request: mutationGateRequest(http.MethodPost, hostileOrigin, "cross-site", sessionCookie, csrfCookie), token: "double-submit", wantCode: originRejectedCode},
		{name: "same-origin cookie POST without token", request: mutationGateRequest(http.MethodPost, spaOrigin, "same-site", sessionCookie, csrfCookie), wantCode: "permission_denied"},
		{name: "same-origin cookie POST with token", request: mutationGateRequest(http.MethodPost, spaOrigin, "same-site", sessionCookie, csrfCookie), token: "double-submit", reached: true},
		{name: "cookie DELETE without token", request: mutationGateRequest(http.MethodDelete, spaOrigin, "same-site", sessionCookie), wantCode: "permission_denied"},
		{name: "bearer-only cross-origin POST", request: mutationGateRequest(http.MethodPost, hostileOrigin, "cross-site"), bearer: true, reached: true},
		{name: "cookie GET", request: mutationGateRequest(http.MethodGet, hostileOrigin, "cross-site", sessionCookie), reached: true},
		{name: "cookie HEAD", request: mutationGateRequest(http.MethodHead, hostileOrigin, "cross-site", sessionCookie), reached: true},
		{name: "cookie OPTIONS", request: mutationGateRequest(http.MethodOptions, hostileOrigin, "cross-site", sessionCookie), reached: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			handler := adapter.RequireBrowserSafeMutation()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			}))
			if tc.token != "" {
				tc.request.Header.Set(csrfHeaderName, tc.token)
			}
			if tc.bearer {
				tc.request.Header.Set("Authorization", "Bearer opaque")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tc.request)
			if reached != tc.reached {
				t.Fatalf("reached = %v, want %v (status %d)", reached, tc.reached, rec.Code)
			}
			if tc.reached {
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if code := errorCode(t, rec); code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// TestRequireBrowserSafeMutationFailsClosedWithoutPolicy proves an adapter built
// by NewAuthenticator, which has no browser policy, refuses every unsafe method
// and never panics; a nil adapter does the same.
func TestRequireBrowserSafeMutationFailsClosedWithoutPolicy(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	var logs bytes.Buffer
	middlewareOnly, err := NewAuthenticator(service.Service, environment.ModeDevelopment,
		WithAuthenticatorLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	var absent *Adapter
	for name, adapter := range map[string]*Adapter{"NewAuthenticator": middlewareOnly, "nil": absent} {
		reached := false
		handler := adapter.RequireBrowserSafeMutation()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, mutationGateRequest(http.MethodPost, spaOrigin, "same-origin"))
		if rec.Code != http.StatusForbidden || reached {
			t.Errorf("%s: POST status = %d reached = %v, want 403 and not reached", name, rec.Code, reached)
		}
		handler.ServeHTTP(httptest.NewRecorder(), mutationGateRequest(http.MethodGet, spaOrigin, "same-origin"))
		if !reached {
			t.Errorf("%s: GET was not passed through", name)
		}
	}
	if !strings.Contains(logs.String(), "RequireBrowserSafeMutation on an adapter without a browser policy") {
		t.Errorf("composing the gate on a policy-less adapter logged no WARN:\n%s", logs.String())
	}
}

// TestRequireBrowserSafeMutationOnHostRoute drives the gate end to end on a host
// route behind the adapter's authenticator: a real cookie session, the token
// from GET /auth/csrf, and the mutation it unlocks.
func TestRequireBrowserSafeMutationOnHostRoute(t *testing.T) {
	users := newMemUsers()
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	svc := newServiceWithFakes(authenticationFixture{
		Users:          users,
		Identifiers:    newMemIdentifiers(users),
		Passwords:      passwords,
		Sessions:       sessions,
		Challenges:     challenges,
		Protector:      memProtector{},
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:         fakeHasher{},
		Limiter:        ratelimiter.NewMemory(),
		TokenSigner:    newFakeSigner(),
	})
	adapter, err := New(svc.Service, environment.ModeDevelopment,
		WithBrowser(BrowserConfig{AllowedOrigins: []string{spaOrigin}}),
		WithAuthenticatorPolicy(AuthenticatorPolicy{Limiter: ratelimiter.NewMemory()}))
	if err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	if err := adapter.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatal(err)
	}
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	router.Handle(http.MethodPost, "/host/mutate", ok, adapter.RequireAccessTokenLive(), adapter.RequireBrowserSafeMutation())
	router.Handle(http.MethodGet, "/host/read", ok, adapter.RequireAccessTokenLive(), adapter.RequireBrowserSafeMutation())

	srv := httptest.NewTLSServer(router)
	defer srv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := srv.Client()
	client.Jar = jar
	send := func(method, path, body, origin, site string, header http.Header) (*http.Response, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", site)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, vs := range header {
			req.Header[k] = vs
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		return resp, payload
	}

	send(http.MethodPost, "/auth/register", `{"email":"host@example.com","password":"password123456789","display_name":"H"}`, spaOrigin, "same-site", nil)
	if resp, body := send(http.MethodPost, "/auth/login", `{"email":"host@example.com","password":"password123456789"}`, spaOrigin, "same-site", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d; body=%s", resp.StatusCode, body)
	}
	boot, bootBody := send(http.MethodGet, "/auth/csrf", "", spaOrigin, "same-site", nil)
	if boot.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/csrf = %d; body=%s", boot.StatusCode, bootBody)
	}
	token := bootstrapToken(t, bootBody)

	if resp, body := send(http.MethodPost, "/host/mutate", "", hostileOrigin, "cross-site", http.Header{csrfHeaderName: {token}}); resp.StatusCode != http.StatusForbidden || bodyErrorCode(t, body) != originRejectedCode {
		t.Errorf("cross-site cookie POST = %d %s, want 403 origin_rejected", resp.StatusCode, body)
	}
	if resp, body := send(http.MethodPost, "/host/mutate", "", spaOrigin, "same-site", nil); resp.StatusCode != http.StatusForbidden || bodyErrorCode(t, body) != "permission_denied" {
		t.Errorf("cookie POST without token = %d %s, want 403 permission_denied", resp.StatusCode, body)
	}
	if resp, body := send(http.MethodPost, "/host/mutate", "", spaOrigin, "same-site", http.Header{csrfHeaderName: {token}}); resp.StatusCode != http.StatusNoContent {
		t.Errorf("cookie POST with the bootstrapped token = %d %s, want 204", resp.StatusCode, body)
	}
	if resp, body := send(http.MethodGet, "/host/read", "", hostileOrigin, "cross-site", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("cookie GET without token = %d %s, want 204", resp.StatusCode, body)
	}
}

// TestProxyPostureWarnsOnceInProduction pins the trusted-proxy WARN: absent
// TrustProxies, or a forwarded request resolved to its peer, logs once in
// production; a correctly resolved request and development log nothing.
func TestProxyPostureWarnsOnceInProduction(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	request := func(forwarded string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = "10.0.0.2:4000"
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	cases := []struct {
		name    string
		mode    environment.Mode
		trusted int // -1: TrustProxies not wired
		forward string
		want    string
	}{
		{"no TrustProxies", environment.ModeProduction, -1, "", "web.TrustProxies is not wired"},
		{"TrustProxies(0) behind a proxy", environment.ModeProduction, 0, "203.0.113.9", "X-Forwarded-For"},
		{"TrustProxies(1) behind a proxy", environment.ModeProduction, 1, "203.0.113.9", ""},
		{"TrustProxies(0) direct", environment.ModeProduction, 0, "", ""},
		{"development without TrustProxies", environment.ModeDevelopment, -1, "203.0.113.9", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := clientInfoMiddleware(newProxyPosture(tc.mode, slog.New(slog.NewTextHandler(&logs, nil))))(ok)
			if tc.trusted >= 0 {
				handler = web.TrustProxies(tc.trusted)(handler)
			}
			for range 3 {
				handler.ServeHTTP(httptest.NewRecorder(), request(tc.forward))
			}
			got := strings.Count(logs.String(), "level=WARN")
			switch {
			case tc.want == "" && got != 0:
				t.Errorf("logged %d WARNs, want none:\n%s", got, logs.String())
			case tc.want != "" && (got != 1 || !strings.Contains(logs.String(), tc.want)):
				t.Errorf("want one WARN naming %q, got %d:\n%s", tc.want, got, logs.String())
			}
		})
	}
}

// TestNewWiresProxyPostureInProductionOnly proves the bundled route table gets
// the observer from the adapter's mode.
func TestNewWiresProxyPostureInProductionOnly(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	dev, err := New(service.Service, environment.ModeDevelopment, WithAuthenticatorPolicy(AuthenticatorPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	if dev.routes.Proxies != nil {
		t.Error("development adapter carries a proxy observer")
	}
	prod, err := New(service.Service, environment.ModeProduction,
		WithAuthenticatorPolicy(AuthenticatorPolicy{Cookie: CookieConfig{Secure: true}, Limiter: sharedHTTPLimiter{}}))
	if err != nil {
		t.Fatal(err)
	}
	if prod.routes.Proxies == nil {
		t.Error("production adapter carries no proxy observer")
	}
}
