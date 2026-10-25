package authenticationhttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var (
	hostCSRF        = csrfCookieSpec{name: csrfCookieName, secure: true}
	developmentCSRF = csrfCookieSpec{name: developmentCSRFCookieName}
)

// TestCSRFCookieResolvesFromTheSessionCookie pins the resolution matrix: a
// Secure session cookie keeps the __Host- cookie in both modes, a non-Secure
// one gets the development cookie in development and is refused in production.
func TestCSRFCookieResolvesFromTheSessionCookie(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	for _, tc := range []struct {
		name   string
		mode   environment.Mode
		secure bool
		want   csrfCookieSpec
	}{
		{"secure development", environment.ModeDevelopment, true, hostCSRF},
		{"secure production", environment.ModeProduction, true, hostCSRF},
		{"insecure development", environment.ModeDevelopment, false, developmentCSRF},
		{"insecure production", environment.ModeProduction, false, csrfCookieSpec{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := AuthenticatorPolicy{Cookie: CookieConfig{Secure: tc.secure}, Limiter: sharedHTTPLimiter{}}
			adapter, err := New(service.Service, tc.mode, WithAuthenticatorPolicy(policy))
			direct, directErr := NewAuthenticator(service.Service, tc.mode, WithCookies(policy.Cookie), WithLimiter(policy.Limiter))
			if tc.want == (csrfCookieSpec{}) {
				for _, e := range []error{err, directErr} {
					if !errors.Is(e, sdk.ErrInvalidInput) || !strings.Contains(e.Error(), "production cookies require Secure") {
						t.Errorf("err = %v, want the Secure session-cookie refusal", e)
					}
				}
				return
			}
			if err != nil || directErr != nil {
				t.Fatalf("New = %v, NewAuthenticator = %v", err, directErr)
			}
			if adapter.csrfCookie != tc.want || direct.csrfCookie != tc.want || adapter.routes.Mutation.csrf().cookie != tc.want {
				t.Errorf("csrf cookie = %+v / %+v / %+v, want %+v", adapter.csrfCookie, direct.csrfCookie, adapter.routes.Mutation.csrf().cookie, tc.want)
			}
		})
	}
}

// TestProductionRefusesTheDevelopmentCSRFCookie exercises the second,
// independent production check on its own: a relaxed CSRF cookie never passes
// in production, whatever the session cookie check decided.
func TestProductionRefusesTheDevelopmentCSRFCookie(t *testing.T) {
	for _, spec := range []csrfCookieSpec{developmentCSRF, {name: csrfCookieName}, {name: developmentCSRFCookieName, secure: true}} {
		err := validateCSRFCookie(environment.ModeProduction, spec)
		if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), spec.name) {
			t.Errorf("production %+v = %v, want an ErrInvalidInput naming the cookie", spec, err)
		}
	}
	if err := validateCSRFCookie(environment.ModeProduction, hostCSRF); err != nil {
		t.Errorf("production __Host- cookie = %v", err)
	}
	if err := validateCSRFCookie(environment.ModeDevelopment, developmentCSRF); err != nil {
		t.Errorf("development cookie in development = %v", err)
	}
}

// TestDevelopmentCSRFCookieBootLine pins the development boot line and that a
// Secure host logs none.
func TestDevelopmentCSRFCookieBootLine(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	for _, secure := range []bool{false, true} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		if _, err := New(service.Service, environment.ModeDevelopment, WithAuthenticatorPolicy(AuthenticatorPolicy{Cookie: CookieConfig{Secure: secure}, Logger: logger})); err != nil {
			t.Fatal(err)
		}
		logged := strings.Contains(buf.String(), "csrf_cookie=auth_csrf secure=false")
		if logged == secure {
			t.Errorf("secure=%v: boot log %q", secure, buf.String())
		}
	}
}

// TestCSRFCookieAttributes pins the Set-Cookie bytes: the __Host- cookie is
// unchanged, and the development cookie differs only in name and Secure.
func TestCSRFCookieAttributes(t *testing.T) {
	for _, tc := range []struct {
		spec csrfCookieSpec
		want string
	}{
		{hostCSRF, "__Host-auth_csrf=tok; Path=/; Secure; SameSite=Lax"},
		{MutationSecurity{}.csrf().cookie, "__Host-auth_csrf=tok; Path=/; Secure; SameSite=Lax"},
		{developmentCSRF, "auth_csrf=tok; Path=/; SameSite=Lax"},
	} {
		rec := httptest.NewRecorder()
		tc.spec.set(rec, "tok")
		if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%+v Set-Cookie = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TestDevelopmentCSRFCookieReaders drives every CSRF reader on a development
// host: each accepts the development cookie and ignores a __Host- cookie.
func TestDevelopmentCSRFCookieReaders(t *testing.T) {
	security := MutationSecurity{AllowedOrigins: []string{spaOrigin}, SessionCookieName: "session", csrfCookie: developmentCSRF}
	h := &handlers{mutation: security, views: stubViews{}}
	session := &http.Cookie{Name: "session", Value: "opaque"}

	t.Run("JSON lane", func(t *testing.T) {
		for _, form := range []bool{false, true} {
			gate := requireBrowserSafeMutation(security.csrf())
			if form {
				gate = requireBrowserSafeFormMutation(security.csrf())
			}
			for _, name := range []string{developmentCSRFCookieName, csrfCookieName} {
				reached := false
				next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
				r := mutationGateRequest(http.MethodPost, spaOrigin, "same-origin", session, &http.Cookie{Name: name, Value: "tok"})
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set(csrfHeaderName, "tok")
				gate(next).ServeHTTP(httptest.NewRecorder(), r)
				if reached != (name == developmentCSRFCookieName) {
					t.Errorf("form lane %v, cookie %s: reached = %v", form, name, reached)
				}
			}
		}
	})

	t.Run("form lane", func(t *testing.T) {
		for _, name := range []string{developmentCSRFCookieName, csrfCookieName} {
			r := httptest.NewRequest(http.MethodPost, "/auth/password/change", nil)
			r.AddCookie(&http.Cookie{Name: name, Value: "tok"})
			rec := httptest.NewRecorder()
			ok := h.formCSRFOK(rec, r, url.Values{"csrf_token": {"tok"}})
			if ok != (name == developmentCSRFCookieName) {
				t.Errorf("cookie %s: formCSRFOK = %v", name, ok)
			}
			if !ok && (rec.Code != http.StatusForbidden || csrfCookieNamed(rec, developmentCSRFCookieName) == nil) {
				t.Errorf("cookie %s: refusal = %d, want 403 re-issuing the development cookie", name, rec.Code)
			}
		}
	})

	t.Run("page context", func(t *testing.T) {
		rec := httptest.NewRecorder()
		pc := h.newPageContext(rec)
		if c := csrfCookieNamed(rec, developmentCSRFCookieName); c == nil || c.Value != pc.CSRFToken || c.Secure || csrfCookieNamed(rec, csrfCookieName) != nil {
			t.Errorf("page context cookies = %q", rec.Header().Values("Set-Cookie"))
		}
	})

	t.Run("bootstrap reuse", func(t *testing.T) {
		var raw [csrfTokenBytes]byte
		held := base64.RawURLEncoding.EncodeToString(raw[:])
		for _, tc := range []struct {
			name  string
			reuse bool
		}{{developmentCSRFCookieName, true}, {csrfCookieName, false}} {
			r := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
			r.AddCookie(&http.Cookie{Name: tc.name, Value: held})
			rec := httptest.NewRecorder()
			h.csrfBootstrap(rec, r)
			var body csrfResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			c := csrfCookieNamed(rec, developmentCSRFCookieName)
			if c == nil || c.Value != body.Token || c.Secure || (body.Token == held) != tc.reuse {
				t.Errorf("held %s: token reused = %v, cookies %q", tc.name, body.Token == held, rec.Header().Values("Set-Cookie"))
			}
		}
	})

	t.Run("oauth2 consent", func(t *testing.T) {
		f := newOAuthHTTPFixture(t)
		repos := &oauthHTTPRepository{}
		service, err := oauth2.New(oauth2.Repositories{OAuth2: repos, Users: f.users, Sessions: f.sess}, f.signer, oauthHTTPResolver{}, f.svc.Service, environment.ModeDevelopment, oauth2.Config{Issuer: delegatedIssuer, MCPResource: mcpResource, APIResource: apiResource, ConfidentialClientID: "mcp-server", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(oauthHTTPSecret))}})
		if err != nil {
			t.Fatal(err)
		}
		capture := &oauthHTTPCapture{}
		router := web.NewWebHandler()
		mountOAuth2(clientInfoRegistrar{inner: router}, &handlers{svc: f.svc, views: stubViews{}, oauth2: &OAuth2Config{Service: service, Views: capture, Limiter: ratelimiter.NewMemory()},
			mutation: MutationSecurity{AllowedOrigins: []string{delegatedIssuer}, SessionCookieName: f.svc.SessionCookieName(), csrfCookie: developmentCSRF}})
		f.router = router
		w := f.serve(f.request("GET", "/auth/oauth2/authorize?"+oauthHTTPAuthorizationQuery().Encode(), "", true))
		csrf := csrfCookieNamed(w, developmentCSRFCookieName)
		if w.Code != http.StatusOK || csrf == nil || csrf.Secure {
			t.Fatalf("consent page = %d, cookies %q", w.Code, w.Header().Values("Set-Cookie"))
		}
		form := url.Values{"request": {capture.consent.RequestToken}, "decision": {"allow"}, "csrf_token": {csrf.Value}}
		r := f.request("POST", "/auth/oauth2/authorize", form.Encode(), true)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf.Value})
		r.Header.Set("Origin", delegatedIssuer)
		assertOAuthHTTPError(t, f.serve(r), http.StatusForbidden, "access_denied")
		r = f.request("POST", "/auth/oauth2/authorize", form.Encode(), true)
		r.AddCookie(csrf)
		r.Header.Set("Origin", delegatedIssuer)
		w = f.serve(r)
		if location, err := url.Parse(w.Header().Get("Location")); err != nil || w.Code != http.StatusSeeOther || location.Query().Get("code") == "" || len(repos.codes) != 1 {
			t.Fatalf("approval with the development cookie = %d %s", w.Code, w.Header().Get("Location"))
		}
	})
}

// TestExternalCookiesCollideWithEitherCSRFCookie pins that a provider cookie
// may share neither CSRF cookie name, whichever the session cookie resolves to.
func TestExternalCookiesCollideWithEitherCSRFCookie(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	for _, secure := range []bool{false, true} {
		for _, name := range []string{csrfCookieName, developmentCSRFCookieName} {
			if _, err := NewAuthenticator(h.svc.Service, environment.ModeDevelopment, WithCookies(CookieConfig{Secure: secure}), WithExternalCookies(name)); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("secure=%v WithExternalCookies(%q) = %v, want ErrInvalidInput", secure, name, err)
			}
		}
	}
}

func csrfCookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
