package authenticationhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestServiceOwnsOriginAllowlist(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	origins := []string{"https://app.example.com"}
	adapter, err := New(service.Service,
		environment.ModeDevelopment,
		WithBrowser(BrowserConfig{AllowedOrigins: origins}),
		WithAuthenticatorPolicy(AuthenticatorPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	origins[0] = "https://evil.example.com"
	if got := adapter.routes.Mutation.AllowedOrigins[0]; got != "https://app.example.com" {
		t.Fatalf("host mutation changed live origin policy: %q", got)
	}
}

func TestAdapterOwnsValidatedHTMLPolicy(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	policy, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"'self'"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(service.Service,
		environment.ModeDevelopment,
		WithBrowser(BrowserConfig{Views: stubViews{}, HTMLPolicy: policy}),
		WithAuthenticatorPolicy(AuthenticatorPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"https://changed.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	*policy = *replacement
	h := web.NewWebHandler()
	if err := adapter.Register(pockets.Mount{Router: h}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/login", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "changed.example.com") {
		t.Fatalf("host replacement changed mounted CSP: %q", csp)
	}
}

func TestMiddlewareOnlyAdapterDoesNotMountRoutes(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	adapter, err := NewAuthenticator(service.Service,
		environment.ModeDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Register(pockets.Mount{Router: web.NewWebHandler()}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("middleware-only Register: %v", err)
	}
	handler := adapter.RequireAccessToken()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("missing proof reached handler") }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing proof status=%d", rec.Code)
	}
}

func TestAdapterRejectsInvalidCookieAndTypedNilLimiter(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	for _, path := range []string{"/auth path", "/auth\"path", "/auth/", "relative", "/auth?x"} {
		if _, err := NewAuthenticator(service.Service,
			environment.ModeDevelopment,
			WithCookies(CookieConfig{RefreshPath: path})); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("path %q accepted: %v", path, err)
		}
	}
	var limiter *ratelimiter.Memory
	if _, err := NewAuthenticator(service.Service,
		environment.ModeDevelopment,
		WithLimiter(limiter)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil limiter: %v", err)
	}
}

func TestDirectHTTPConstructorEnforcesRuntimePosture(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	for _, tc := range []struct {
		name string
		cfg  authenticatorConfig
	}{
		{"missing mode", authenticatorConfig{}},
		{"invalid mode", authenticatorConfig{RuntimeMode: "prod"}},
		{"insecure production cookies", authenticatorConfig{RuntimeMode: environment.ModeProduction}},
		{"process-local production limiter", authenticatorConfig{RuntimeMode: environment.ModeProduction, Cookie: CookieConfig{Secure: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAuthenticator(service.Service,
				tc.cfg.RuntimeMode,
				WithCookies(tc.cfg.Cookie),
				WithBrowserLoginPath(tc.cfg.BrowserLoginPath),
				WithLimiter(tc.cfg.Limiter),
				WithAuthenticatorLogger(tc.cfg.Logger)); err == nil {
				t.Fatal("unsafe direct authenticator accepted")
			}
			if _, err := New(service.Service,
				tc.cfg.RuntimeMode,
				WithAuthenticatorPolicy(AuthenticatorPolicy{Cookie: tc.cfg.Cookie, BrowserLoginPath: tc.cfg.BrowserLoginPath, Limiter: tc.cfg.Limiter, Logger: tc.cfg.Logger})); err == nil {
				t.Fatal("unsafe direct route adapter accepted")
			}
		})
	}
	if _, err := NewAuthenticator(service.Service,
		environment.ModeProduction,
		WithCookies(CookieConfig{Secure: true}),
		WithLimiter(sharedHTTPLimiter{})); err != nil {
		t.Fatal(err)
	}
}

type sharedHTTPLimiter struct{ ratelimiter.Limiter }

func (sharedHTTPLimiter) RateLimiterDurability() authlogic.LimiterDurability {
	return authlogic.LimiterDurability{InProcessOnly: false}
}

func TestBrowserOptionReuseAndReplacement(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	origins := []string{"https://original.example.com"}
	option := WithBrowser(BrowserConfig{AllowedOrigins: origins})
	origins[0] = "https://changed.example.com"
	for range 8 {
		t.Run("reuse", func(t *testing.T) {
			t.Parallel()
			adapter, err := New(service.Service, environment.ModeDevelopment, option)
			if err != nil {
				t.Fatal(err)
			}
			if got := adapter.routes.Mutation.AllowedOrigins; len(got) != 1 || got[0] != "https://original.example.com" {
				t.Fatalf("origins = %v", got)
			}
		})
	}
	reset, err := New(service.Service, environment.ModeDevelopment, option, WithBrowser(BrowserConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(reset.routes.Mutation.AllowedOrigins) != 0 {
		t.Fatal("empty replacement retained earlier origins")
	}
	if _, err := New(service.Service, environment.ModeDevelopment, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("adapter nil option: %v", err)
	}
	if _, err := NewAuthenticator(service.Service, environment.ModeDevelopment, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("authenticator nil option: %v", err)
	}
}
