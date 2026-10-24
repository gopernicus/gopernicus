package authenticationhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestPublicJSONArmsCarryTheOriginGate proves the JSON arms of the public
// credential-establishment routes answer the JSON forbid-origin 403 to a
// browser-borne request outside the allowlist, while a non-browser client, a
// same-origin browser and an allowlisted origin reach the handler.
func TestPublicJSONArmsCarryTheOriginGate(t *testing.T) {
	const allowed = "https://app.example.com"
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{
		Users:             users,
		Identifiers:       newMemIdentifiers(users),
		Passwords:         &memPasswords{m: map[string]string{}},
		Sessions:          &memSessions{m: map[string]session.Session{}},
		Hasher:            fakeHasher{},
		Limiter:           ratelimiter.NewMemory(),
		OAuthAccounts:     &memOAuthAccounts{},
		OAuthStates:       &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:         []oauth.Provider{stubProvider{}},
		OAuthCallbackBase: allowed,
		TokenSigner:       newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Mutation: MutationSecurity{AllowedOrigins: []string{allowed}}})
	send := func(path, origin, fetchSite string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for _, path := range []string{"/auth/register", "/auth/verify", "/auth/password/forgot", "/auth/password/reset", "/auth/token", "/auth/oauth/verify-link"} {
		t.Run(path, func(t *testing.T) {
			for _, tc := range []struct{ origin, fetchSite string }{
				{"https://evil.example", "cross-site"},
				{"https://evil.example.com", "same-site"},
				{"https://evil.example", ""},
				{"", "cross-site"},
			} {
				rec := send(path, tc.origin, tc.fetchSite)
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), originRejectedCode) {
					t.Errorf("Origin %q Sec-Fetch-Site %q = %d %s, want the JSON forbid-origin 403", tc.origin, tc.fetchSite, rec.Code, rec.Body)
				}
			}
			for _, tc := range []struct{ origin, fetchSite string }{
				{"", ""},
				{"", "same-origin"},
				{allowed, "same-site"},
				{allowed, ""},
			} {
				if rec := send(path, tc.origin, tc.fetchSite); rec.Code == http.StatusForbidden {
					t.Errorf("Origin %q Sec-Fetch-Site %q = 403 %s, want it to reach the handler", tc.origin, tc.fetchSite, rec.Body)
				}
			}
		})
	}
}
