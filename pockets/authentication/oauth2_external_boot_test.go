package authentication

import (
	"crypto/sha256"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestOAuth2BesideExternalMountsWithoutBrowserLoginPath pins #282: a host wiring
// WithOAuth2 (which mounts the authorize and sessions pages) beside WithExternal
// boots without naming a browser login path: both pages admit only first-party
// access tokens.
func TestOAuth2BesideExternalMountsWithoutBrowserLoginPath(t *testing.T) {
	cfg := browserBaseConfig()
	repos := testRepositories(Repositories{
		ExternalSubjects: cookieTestSubjects{},
		OAuth2:           recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{},
	})
	c, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
		WithBrowser(BrowserConfig{Views: recorderViews{}}),
		WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: cookieTestProvider{}, CookieName: "idp_session"}}}),
		WithOAuth2(OAuth2Config{
			Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
				ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
			Clients: recorderClients{}, Views: recorderOAuthViews{},
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register panicked: %v", r)
		}
	}()
	if err := c.HTTP.Register(pockets.Mount{Router: web.NewWebHandler()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
}
