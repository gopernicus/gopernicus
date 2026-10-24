package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// downLimiter is a shared (durable) limiter whose backend is unreachable.
type downLimiter struct{ durableLimiter }

func (downLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, errors.New("limiter backend down")
}

// TestCookieDomainGuard pins the session-cookie Domain posture: production
// refuses a Domain unless acknowledged (then one posture_waiver WARN),
// development admits it, the acknowledgement is inert without a Domain, and a
// Domain beside an OriginPolicy namespace rule is refused in every mode,
// acknowledged or not.
func TestCookieDomainGuard(t *testing.T) {
	namespace, err := web.NewOriginPolicy(web.OriginPolicyConfig{Namespaces: []web.OriginNamespace{{Scheme: "https", Suffix: "clients.example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	exact, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{"https://app.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		mode    environment.Mode
		domain  string
		ack     bool
		policy  web.OriginPolicy
		want    error
		waivers int
	}{
		{"production Domain unacknowledged", environment.ModeProduction, "example.com", false, web.OriginPolicy{}, ErrCookieDomainRefused, 0},
		{"production Domain acknowledged", environment.ModeProduction, "example.com", true, web.OriginPolicy{}, nil, 1},
		{"production Domain acknowledged beside exact origins", environment.ModeProduction, "example.com", true, exact, nil, 1},
		{"production acknowledgement without a Domain", environment.ModeProduction, "", true, web.OriginPolicy{}, nil, 0},
		{"production namespace without a Domain", environment.ModeProduction, "", false, namespace, nil, 0},
		{"production Domain acknowledged beside a namespace", environment.ModeProduction, "example.com", true, namespace, ErrCookieDomainWithNamespaceRule, 0},
		{"development Domain", environment.ModeDevelopment, "example.com", false, web.OriginPolicy{}, nil, 0},
		{"development Domain beside a namespace", environment.ModeDevelopment, "example.com", false, namespace, ErrCookieDomainWithNamespaceRule, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := bufferLog()
			cfg := noPasswordConfig()
			if tc.mode == environment.ModeProduction {
				cfg = prodLimiterConfig()
				cfg.RateLimiter = durableLimiter{}
			}
			cfg.SessionCookie.Domain = tc.domain
			cfg.CookieDomainAcknowledged = tc.ack
			cfg.OriginPolicy = tc.policy
			cfg.Logger = log
			_, err := newFixture(testRepositories(Repositories{}), cfg)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if n := strings.Count(buf.String(), "posture_waiver=CookieDomainAcknowledged"); n != tc.waivers {
				t.Errorf("posture_waiver=CookieDomainAcknowledged logged %d times, want %d:\n%s", n, tc.waivers, buf.String())
			}
		})
	}
}

// TestPublicRouteLimiterFailOpenWaiver proves a true PublicRouteLimiterFailOpen
// is one posture_waiver WARN in production and silent in development.
func TestPublicRouteLimiterFailOpenWaiver(t *testing.T) {
	for _, tc := range []struct {
		mode     environment.Mode
		failOpen bool
		waivers  int
	}{
		{environment.ModeProduction, true, 1},
		{environment.ModeProduction, false, 0},
		{environment.ModeDevelopment, true, 0},
	} {
		log, buf := bufferLog()
		cfg := noPasswordConfig()
		if tc.mode == environment.ModeProduction {
			cfg = prodLimiterConfig()
			cfg.RateLimiter = durableLimiter{}
		}
		cfg.PublicRouteLimiterFailOpen = tc.failOpen
		cfg.Logger = log
		if _, err := newFixture(testRepositories(Repositories{}), cfg); err != nil {
			t.Fatalf("%s failOpen=%v: %v", tc.mode, tc.failOpen, err)
		}
		if n := strings.Count(buf.String(), "posture_waiver=PublicRouteLimiterFailOpen"); n != tc.waivers {
			t.Errorf("%s failOpen=%v: posture_waiver=PublicRouteLimiterFailOpen logged %d times, want %d:\n%s", tc.mode, tc.failOpen, n, tc.waivers, buf.String())
		}
	}
}

// TestPublicRouteLimitsReachNew proves New validates the route budgets and
// carries them, and the fail-closed policy, to the mounted routes and to
// Components.HTTP.RateLimitByIP.
func TestPublicRouteLimitsReachNew(t *testing.T) {
	cfg := noPasswordConfig()
	cfg.PublicRouteLimits = PublicRouteLimits{Refresh: ratelimiter.Limit{Requests: 1}}
	if _, err := newFixture(testRepositories(Repositories{}), cfg); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "PublicRouteLimits.Refresh") {
		t.Fatalf("invalid route budget: err = %v, want sdk.ErrInvalidInput naming the field", err)
	}

	build := func(failOpen bool, limits PublicRouteLimits) http.Handler {
		cfg := noPasswordConfig()
		cfg.RateLimiter = downLimiter{}
		cfg.PublicRouteLimiterFailOpen = failOpen
		cfg.PublicRouteLimits = limits
		components, err := newFixture(testRepositories(Repositories{}), cfg)
		if err != nil {
			t.Fatal(err)
		}
		h := web.NewWebHandler()
		if err := components.HTTP.Register(pockets.Mount{Router: h}); err != nil {
			t.Fatal(err)
		}
		h.Handle(http.MethodPost, "/host/limited", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, components.HTTP.RateLimitByIP("host", 10))
		return h
	}
	post := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		return rec
	}
	closed := build(false, PublicRouteLimits{})
	for _, path := range []string{"/auth/refresh", "/host/limited"} {
		if rec := post(closed, path); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
			t.Errorf("%s during a limiter outage = %d %v, want 503 + Retry-After: 5", path, rec.Code, rec.Header())
		}
	}
	open := build(true, PublicRouteLimits{})
	for _, path := range []string{"/auth/refresh", "/host/limited"} {
		if rec := post(open, path); rec.Code == http.StatusServiceUnavailable {
			t.Errorf("%s with PublicRouteLimiterFailOpen during a limiter outage = 503, want it admitted", path)
		}
	}
}

// TestEdgeAcknowledgementsHaveNoEnvTag pins CookieDomainAcknowledged and
// PublicRouteLimiterFailOpen as literals: ParseEnvTags never fills them.
func TestEdgeAcknowledgementsHaveNoEnvTag(t *testing.T) {
	for _, key := range []string{
		"AUTH_COOKIE_DOMAIN_ACKNOWLEDGED", "COOKIE_DOMAIN_ACKNOWLEDGED", "CookieDomainAcknowledged",
		"AUTH_PUBLIC_ROUTE_LIMITER_FAIL_OPEN", "PUBLIC_ROUTE_LIMITER_FAIL_OPEN", "PublicRouteLimiterFailOpen",
	} {
		t.Setenv(key, "true")
	}
	for _, namespace := range []string{"", "AUTH"} {
		var browser BrowserConfig
		var abuse AbuseProtectionConfig
		var cfg constructorConfig
		for _, target := range []any{&browser, &abuse, &cfg} {
			if err := environment.ParseEnvTags(namespace, target); err != nil {
				t.Fatalf("ParseEnvTags(%q, %T): %v", namespace, target, err)
			}
		}
		if browser.CookieDomainAcknowledged || cfg.CookieDomainAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled CookieDomainAcknowledged", namespace)
		}
		if abuse.PublicRouteLimiterFailOpen || cfg.PublicRouteLimiterFailOpen {
			t.Errorf("ParseEnvTags(%q) filled PublicRouteLimiterFailOpen", namespace)
		}
	}
}
