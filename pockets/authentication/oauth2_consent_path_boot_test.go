package authentication

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func consentPathOAuth2(views bool, path string) OAuth2Config {
	cfg := OAuth2Config{
		Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
			ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
		Clients: recorderClients{}, ConsentPath: path,
	}
	if views {
		cfg.Views = recorderOAuthViews{}
	}
	return cfg
}

// TestOAuth2ConsentPathBootsWithoutBrowserViews pins #380: a host that owns
// consent (ConsentPath) boots WithOAuth2 with no browser Views, and the only
// browser-facing routes mounted are the OAuth2 protocol and JSON routes; no
// login, account or sessions page exists.
func TestOAuth2ConsentPathBootsWithoutBrowserViews(t *testing.T) {
	cfg := browserBaseConfig()
	repos := testRepositories(Repositories{OAuth2: recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{}})
	c, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
		WithOAuth2(consentPathOAuth2(false, "/connect/consent")))
	if err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	if err := c.HTTP.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	status := func(method, path string) int {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code
	}
	for _, path := range []string{"/auth/login", "/auth/account", "/auth/step-up", "/auth/sessions", "/auth/oauth/link"} {
		// 405 means only a JSON API route (POST /auth/login) shares the path.
		if got := status(http.MethodGet, path); got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 404 or 405 (no browser page on the ConsentPath lane)", path, got)
		}
	}
	if got := status(http.MethodPost, "/auth/oauth2/authorize"); got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
		t.Errorf("POST /auth/oauth2/authorize = %d, want the form lane unmounted", got)
	}
	for _, path := range []string{"/auth/oauth2/consent", "/auth/connections"} {
		if got := status(http.MethodGet, path); got != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401 (mounted, JSON denial)", path, got)
		}
	}
	if got := status(http.MethodGet, "/auth/oauth2/authorize"); got != http.StatusSeeOther {
		t.Errorf("GET /auth/oauth2/authorize = %d, want the 303 login redirect", got)
	}
}

// TestOAuth2ConsentLaneBootRefusals pins the exclusive consent lanes: neither
// or both of Views and ConsentPath, an unsafe ConsentPath, and Views without
// the browser Views are boot errors.
func TestOAuth2ConsentLaneBootRefusals(t *testing.T) {
	cfg := browserBaseConfig()
	repos := testRepositories(Repositories{OAuth2: recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{}})
	for name, tc := range map[string]struct {
		oauth   OAuth2Config
		browser bool
	}{
		"neither":                 {consentPathOAuth2(false, ""), true},
		"both":                    {consentPathOAuth2(true, "/connect/consent"), true},
		"views without browser":   {consentPathOAuth2(true, ""), false},
		"absolute consent path":   {consentPathOAuth2(false, "https://evil.example/consent"), false},
		"protocol-relative path":  {consentPathOAuth2(false, "//evil.example/consent"), false},
		"consent path with query": {consentPathOAuth2(false, "/consent?x=1"), false},
	} {
		t.Run(name, func(t *testing.T) {
			opts := []Option{WithPassword(PasswordConfig{Hasher: cfg.Hasher}), WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}), WithOAuth2(tc.oauth)}
			if tc.browser {
				opts = append(opts, WithBrowser(BrowserConfig{Views: recorderViews{}}))
			}
			if _, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
