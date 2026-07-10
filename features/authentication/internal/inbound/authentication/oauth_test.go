package authentication

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// --- oauth route-level fakes (package http) ---

type stubProvider struct{}

func (stubProvider) Name() string                 { return "google" }
func (stubProvider) SupportsOIDC() bool           { return false }
func (stubProvider) TrustEmailVerification() bool { return true }
func (stubProvider) GetAuthorizationURL(state, _, _, _ string) string {
	return "https://accounts.google.example/authorize?state=" + state
}
func (stubProvider) ExchangeCode(context.Context, string, string, string) (*oauth.TokenResponse, error) {
	return &oauth.TokenResponse{}, nil
}
func (stubProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{}, nil
}
func (stubProvider) ValidateIDToken(context.Context, string, string) (*oauth.IDTokenClaims, error) {
	return nil, errors.New("no oidc")
}
func (stubProvider) RefreshToken(context.Context, string) (*oauth.TokenResponse, error) {
	return nil, errors.New("unused")
}

type memOAuthAccounts struct {
	mu sync.Mutex
	m  []oauthaccount.OAuthAccount
}

func (r *memOAuthAccounts) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m = append(r.m, a)
	return a, nil
}
func (r *memOAuthAccounts) GetByProvider(_ context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.m {
		if a.Provider == provider && a.ProviderUserID == providerUserID {
			return a, nil
		}
	}
	return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
}
func (r *memOAuthAccounts) ListByUser(_ context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []oauthaccount.OAuthAccount{}
	for _, a := range r.m {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (r *memOAuthAccounts) Delete(context.Context, string, string) error { return nil }

type memOAuthStates struct {
	mu sync.Mutex
	m  map[string]oauthstate.State
}

func (r *memOAuthStates) Create(_ context.Context, s oauthstate.State) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[s.Token] = s
	return s, nil
}
func (r *memOAuthStates) Consume(_ context.Context, token string) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.m[token]
	if !ok {
		return oauthstate.State{}, sdk.ErrNotFound
	}
	delete(r.m, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, sdk.ErrExpired
	}
	return s, nil
}

// newOAuthTestHandler builds a real oauth-enabled authsvc.Service over in-memory
// fakes and mounts the routes.
func newOAuthTestHandler(t *testing.T) http.Handler {
	t.Helper()
	svc := authsvc.NewService(authsvc.Deps{
		Users:             &memUsers{byID: map[string]user.User{}},
		Passwords:         &memPasswords{m: map[string]string{}},
		Sessions:          &memSessions{m: map[string]session.Session{}},
		Codes:             &memCodes{m: map[string]verification.Code{}},
		Tokens:            &memTokens{m: map[string]verification.Token{}},
		Hasher:            fakeHasher{},
		Mailer:            nopMailer{},
		MailFrom:          "noreply@example.com",
		Limiter:           ratelimiter.NewMemory(),
		Cookie:            authsvc.CookieConfig{},
		OAuthAccounts:     &memOAuthAccounts{},
		OAuthStates:       &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:         []oauth.Provider{stubProvider{}},
		OAuthCallbackBase: "https://app.example.com",
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, "")
	return h
}

// TestOAuthRoutesDenyByAbsence proves the OAuth routes are NOT registered when no
// provider is wired: every one returns 404 (design §3 deny-by-absence).
func TestOAuthRoutesDenyByAbsence(t *testing.T) {
	h := newTestHandler(t, nil) // no providers
	cases := []struct {
		method, path string
	}{
		{"GET", "/auth/oauth/github/start"},
		{"GET", "/auth/oauth/github/callback?code=x&state=y"},
		{"POST", "/auth/oauth/verify-link"},
		{"GET", "/auth/oauth/linked"},
		{"GET", "/auth/oauth/github/link/start"},
		{"DELETE", "/auth/oauth/github/link"},
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s (oauth off) status = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

// TestOAuthStartRedirects proves a wired start route 302-redirects to the
// provider authorization URL.
func TestOAuthStartRedirects(t *testing.T) {
	h := newOAuthTestHandler(t)
	rec := do(t, h, "GET", "/auth/oauth/google/start", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("start status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc == "" {
		t.Error("start did not set a Location header")
	}
}

// TestOAuthStartUnknownProvider404 proves an unknown provider on a wired host is
// a 404.
func TestOAuthStartUnknownProvider404(t *testing.T) {
	h := newOAuthTestHandler(t)
	rec := do(t, h, "GET", "/auth/oauth/facebook/start", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider start status = %d, want 404", rec.Code)
	}
}

// TestOAuthSessionGatedRoutesRequireSession proves the linked/link-start/unlink
// routes are session-gated (401 without a session) when wired.
func TestOAuthSessionGatedRoutesRequireSession(t *testing.T) {
	h := newOAuthTestHandler(t)
	cases := []struct {
		method, path string
	}{
		{"GET", "/auth/oauth/linked"},
		{"GET", "/auth/oauth/google/link/start"},
		{"DELETE", "/auth/oauth/google/link"},
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session status = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

// TestOAuthVerifyLinkStrictDecode proves the verify-link body is strictly
// decoded (unknown fields → 400).
func TestOAuthVerifyLinkStrictDecode(t *testing.T) {
	h := newOAuthTestHandler(t)
	rec := do(t, h, "POST", "/auth/oauth/verify-link", `{"token":"x","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("verify-link unknown-field status = %d, want 400", rec.Code)
	}
}
