package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// refreshSessionOutageLimiter fails only the service's per-session refresh key,
// so the route's IP arm admits and the logic arm's outage reaches the handler.
type refreshSessionOutageLimiter struct{ inner ratelimiter.Limiter }

func (l refreshSessionOutageLimiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if strings.HasPrefix(key, "refresh:") && key != "refresh:192.0.2.1" {
		return ratelimiter.Result{}, errors.New("limiter backend down")
	}
	return l.inner.Allow(ctx, key, limit)
}
func (l refreshSessionOutageLimiter) Reset(ctx context.Context, key string) error {
	return l.inner.Reset(ctx, key)
}

// newNativeOAuthHandler mounts a native-OAuth-enabled service with the given
// adapter limiter and route budgets.
func newNativeOAuthHandler(t *testing.T, limiter ratelimiter.Limiter, limits PublicRouteLimits) http.Handler {
	t.Helper()
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{
		Users:                   users,
		Identifiers:             newMemIdentifiers(users),
		Passwords:               &memPasswords{m: map[string]string{}},
		Sessions:                &memSessions{m: map[string]session.Session{}},
		Hasher:                  fakeHasher{},
		Limiter:                 limiter,
		OAuthAccounts:           &memOAuthAccounts{},
		OAuthStates:             &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:               []oauth.Provider{stubProvider{}},
		OAuthCallbackBase:       "https://app.example.com",
		OAuthNativeRedirectURIs: []string{"com.example.app:/oauth"},
		TokenSigner:             newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, RouteLimits: limits})
	return h
}

func postJSON(h http.Handler, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestPublicRouteLimitsValidate(t *testing.T) {
	if err := (PublicRouteLimits{}).Validate(); err != nil {
		t.Fatalf("zero limits (every default) refused: %v", err)
	}
	if err := (PublicRouteLimits{Register: ratelimiter.PerMinute(1000)}).Validate(); err != nil {
		t.Fatalf("a large budget refused: %v", err)
	}
	for name, limits := range map[string]PublicRouteLimits{
		"Register":          {Register: ratelimiter.Limit{Requests: -1, Window: 60e9}},
		"Verify":            {Verify: ratelimiter.Limit{Requests: 5}},
		"PasswordReset":     {PasswordReset: ratelimiter.PerMinute(5).WithBurst(-1)},
		"VerifyLink":        {VerifyLink: ratelimiter.Limit{Window: 60e9}},
		"OAuthNative":       {OAuthNative: ratelimiter.Limit{Requests: -5, Window: 60e9}},
		"Refresh":           {Refresh: ratelimiter.Limit{Requests: 1, Window: -1}},
		"InvitationDecline": {InvitationDecline: ratelimiter.Limit{Requests: 1}},
	} {
		err := limits.Validate()
		if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "PublicRouteLimits."+name) {
			t.Errorf("%s: err = %v, want sdk.ErrInvalidInput naming the field", name, err)
		}
	}
	service := newServiceWithFakes(authenticationFixture{})
	if _, err := New(service.Service, environment.ModeDevelopment, WithPublicRouteLimits(PublicRouteLimits{Register: ratelimiter.Limit{Requests: 1}})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New accepted an invalid route budget: %v", err)
	}
}

// TestPublicRouteBudgetKeys pins each bundled public route to its edge key and
// proves forgot, token and login carry none (their per-IP budgets live in the
// service).
func TestPublicRouteBudgetKeys(t *testing.T) {
	limiter := &keyRecordingLimiter{}
	h := newTestHandler(t, limiter)
	edge := func() []string {
		var out []string
		for _, key := range limiter.take() {
			if !strings.HasPrefix(key, "authentication:") && !strings.HasPrefix(key, "verification_resend:") {
				out = append(out, key)
			}
		}
		return out
	}
	for _, tc := range []struct{ path, want string }{
		{"/auth/register", "register:192.0.2.1"},
		{"/auth/verify", "verify:192.0.2.1"},
		{"/auth/password/reset", "password_reset:192.0.2.1"},
		{"/auth/refresh", "refresh:192.0.2.1"},
		{"/auth/password/forgot", ""},
		{"/auth/token", ""},
		{"/auth/login", ""},
	} {
		postJSON(h, tc.path, `{}`)
		got := edge()
		var want []string
		if tc.want != "" {
			want = []string{tc.want}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s edge keys = %q, want %q", tc.path, got, want)
		}
	}

	native := &keyRecordingLimiter{}
	nh := newNativeOAuthHandler(t, native, PublicRouteLimits{})
	for _, tc := range []struct{ path, want string }{
		{"/auth/oauth/verify-link", "verify_link:192.0.2.1"},
		{"/auth/oauth/native/verify-link", "verify_link:192.0.2.1"},
		{"/auth/oauth/google/native/start", "oauth_native:192.0.2.1"},
		{"/auth/oauth/google/native/complete", "oauth_native:192.0.2.1"},
		{"/auth/oauth/google/native/link/start", "oauth_native:192.0.2.1"},
	} {
		postJSON(nh, tc.path, `{}`)
		var got []string
		for _, key := range native.take() {
			if !strings.HasPrefix(key, "authentication:") {
				got = append(got, key)
			}
		}
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("%s keys = %q, want first %q", tc.path, got, tc.want)
		}
	}
}

// TestPublicRouteBudgetsDeny exhausts each default budget in turn on one
// handler: the denial is the shared JSON 429 with Retry-After and no-store, and
// exhausting one route leaves the next route's budget intact.
func TestPublicRouteBudgetsDeny(t *testing.T) {
	h := newTestHandler(t, nil)
	for _, tc := range []struct {
		path   string
		budget int
	}{
		{"/auth/register", 10},
		{"/auth/verify", 20},
		{"/auth/password/reset", 20},
		{"/auth/refresh", 30},
	} {
		for i := range tc.budget {
			if rec := postJSON(h, tc.path, `{}`); rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s request %d = 429 inside the %d/min budget", tc.path, i+1, tc.budget)
			}
		}
		rec := postJSON(h, tc.path, `{}`)
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "rate_limited") {
			t.Fatalf("%s past its budget = %d %v %s; want 429 + Retry-After + no-store", tc.path, rec.Code, rec.Header(), rec.Body)
		}
	}
}

// TestPublicRouteLimitsOverride proves a host budget replaces the default, and
// that the three native OAuth routes share one bucket as do the two verify-link
// routes.
func TestPublicRouteLimitsOverride(t *testing.T) {
	limiter := ratelimiter.NewMemory()
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{Users: users, Identifiers: newMemIdentifiers(users), Hasher: fakeHasher{}, Limiter: limiter, TokenSigner: newFakeSigner()})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, RouteLimits: PublicRouteLimits{Register: ratelimiter.PerMinute(2)}})
	for i := range 2 {
		if rec := postJSON(h, "/auth/register", `{}`); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("register %d = 429 inside a 2/min budget", i+1)
		}
	}
	if rec := postJSON(h, "/auth/register", `{}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third register = %d, want 429 under a 2/min budget", rec.Code)
	}

	nh := newNativeOAuthHandler(t, ratelimiter.NewMemory(), PublicRouteLimits{OAuthNative: ratelimiter.PerMinute(2), VerifyLink: ratelimiter.PerMinute(1)})
	postJSON(nh, "/auth/oauth/google/native/start", `{}`)
	postJSON(nh, "/auth/oauth/google/native/complete", `{}`)
	if rec := postJSON(nh, "/auth/oauth/google/native/link/start", `{}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("native link/start after start+complete = %d, want 429 (one shared key)", rec.Code)
	}
	postJSON(nh, "/auth/oauth/verify-link", `{}`)
	if rec := postJSON(nh, "/auth/oauth/native/verify-link", `{}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("native verify-link after verify-link = %d, want 429 (one shared key)", rec.Code)
	}
}

// TestBundledBudgetsFailClosed proves a limiter outage answers 503 +
// Retry-After: 5 on a bundled public route by default and admits under
// WithLimiterFailOpen.
func TestBundledBudgetsFailClosed(t *testing.T) {
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{Users: users, Identifiers: newMemIdentifiers(users), Hasher: fakeHasher{}, TokenSigner: newFakeSigner()})
	svc.limiter = failedLimiter{errors.New("limiter backend down")}
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc})
	rec := postJSON(h, "/auth/register", `{}`)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("register during a limiter outage = %d %v; want 503 + Retry-After: 5 + no-store", rec.Code, rec.Header())
	}
	svc.limiterFailOpen = true
	open := web.NewWebHandler()
	mount(open, mountDeps{Auth: svc})
	if rec := postJSON(open, "/auth/register", `{}`); rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("fail-open register during a limiter outage = %d, want it admitted", rec.Code)
	}
}

// TestRefreshSessionBudgetOutageAnswers503 drives the logic per-session arm's
// outage through the route: the IP arm admits, the service refuses with
// sdk.ErrUnavailable, and the response is 503 + Retry-After: 5.
func TestRefreshSessionBudgetOutageAnswers503(t *testing.T) {
	h := newTestHandler(t, refreshSessionOutageLimiter{inner: ratelimiter.NewMemory()})
	do(t, h, "POST", "/auth/register", `{"email":"outage@example.com","password":"password123456789","display_name":"O"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"outage@example.com","password":"password123456789"}`)
	rc := refreshCookie(login)
	if rc == nil {
		t.Fatalf("no refresh cookie from login: %d %s", login.Code, login.Body)
	}
	rec := do(t, h, "POST", "/auth/refresh", "", &http.Cookie{Name: rc.Name, Value: rc.Value})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("refresh during a session-budget outage = %d %v %s; want 503 + Retry-After: 5", rec.Code, rec.Header(), rec.Body)
	}
}
