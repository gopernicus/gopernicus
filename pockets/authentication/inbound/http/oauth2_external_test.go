package authenticationhttp

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// newOAuth2ExternalRouter mounts the OAuth2 and session-management pages over a
// hybrid service whose provider reads a cookie, with or without an explicit
// browser login path (#282).
func newOAuth2ExternalRouter(t *testing.T, loginPath string) (*externalHarness, *web.WebHandler) {
	t.Helper()
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}, loginPath: loginPath})
	service, err := oauth2.New(oauth2.Repositories{OAuth2: &oauthHTTPRepository{}, Users: h.users, Sessions: h.sess}, h.signer, oauthHTTPResolver{}, h.svc.Service, environment.ModeDevelopment, oauth2.Config{Issuer: delegatedIssuer, MCPResource: mcpResource, APIResource: apiResource, ConfidentialClientID: "mcp-server", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(oauthHTTPSecret))}})
	if err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	hs := &handlers{svc: h.svc, views: stubViews{}, oauth2: &OAuth2Config{Service: service, Views: &oauthHTTPCapture{}, Limiter: ratelimiter.NewMemory()}, mutation: MutationSecurity{AllowedOrigins: []string{delegatedIssuer}, SessionCookieName: h.svc.SessionCookieName()}}
	mountOAuth2(clientInfoRegistrar{inner: router}, hs)
	mountSessionManagement(clientInfoRegistrar{inner: router}, hs)
	return h, router
}

// TestOAuth2BrowserPagesAdmitOnlyFirstPartyTokens pins #282's one behaviour
// change: the authorize and sessions pages never read a provider cookie, so a
// provider-cookie-only browser is sent to native session recovery (303 with
// recover=1) without a provider round-trip or its side effects, and a provider
// outage there no longer answers 503. Every other status is unchanged.
func TestOAuth2BrowserPagesAdmitOnlyFirstPartyTokens(t *testing.T) {
	authorize := "/auth/oauth2/authorize?" + oauthHTTPAuthorizationQuery().Encode()
	for name, loginPath := range map[string]string{"implicit login path": "", "explicit login path": "/auth/login"} {
		t.Run(name, func(t *testing.T) {
			h, router := newOAuth2ExternalRouter(t, loginPath)
			serve := func(path string, build func(*http.Request)) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				build(r)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w
			}
			providerCookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: extCookie, Value: h.token}) }
			assertRecover := func(name, path string, w *httptest.ResponseRecorder) {
				t.Helper()
				loc, err := url.Parse(w.Header().Get("Location"))
				if err != nil || w.Code != http.StatusSeeOther || loc.Path != "/auth/login" || loc.Query().Get("recover") != "1" || loc.Query().Get("return_to") != path {
					t.Errorf("%s %s = %d %q, want 303 to /auth/login with recover=1 and return_to", name, path, w.Code, w.Header().Get("Location"))
				}
			}

			for _, path := range []string{authorize, "/auth/sessions"} {
				events := len(h.events.events)
				assertRecover("provider cookie only", path, serve(path, providerCookie))
				if got := len(h.events.events); got != events {
					t.Errorf("provider cookie on %s recorded %d security events, want none", path, got-events)
				}

				h.provider.err = &sdkauth.UnavailableError{Kind: sdkauth.UnavailableNetwork, Err: errors.New("jwks down")}
				assertRecover("provider cookie during an outage", path, serve(path, providerCookie))
				h.provider.err = nil

				assertRecover("anonymous", path, serve(path, func(*http.Request) {}))
				assertRecover("provider bearer", path, serve(path, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+h.token) }))
			}

			w := serve(authorize, func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: h.accessJWT})
				providerCookie(r)
			})
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "oauth_consent") {
				t.Errorf("native session on the authorize page = %d %s, want 200 consent", w.Code, w.Body)
			}
		})
	}
}
