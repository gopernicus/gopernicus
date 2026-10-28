package authentication

import (
	"crypto/sha256"
	"errors"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
)

// TestTokenIssuerCollisionsAtNew is L6 and MED-2 through New with
// OAuth2Config (and an external provider) wired: an Issuer equal to the OAuth2
// issuer or to the external provider's issuer, and an Audience equal to the
// MCP or API resource, each refuse with ErrTokenAudienceCollides; distinct
// values construct.
func TestTokenIssuerCollisionsAtNew(t *testing.T) {
	build := func(sessions SessionsConfig) error {
		cfg := browserBaseConfig()
		repos := testRepositories(Repositories{
			SecurityEvents: stubSecurityEvents{}, ExternalSubjects: cookieTestSubjects{},
			OAuth2: recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{},
		})
		_, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
			WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
			WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
			WithBrowser(BrowserConfig{Views: recorderViews{}}),
			WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: cookieTestProvider{}}}}),
			WithOAuth2(OAuth2Config{
				Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
					ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
				Clients: recorderClients{}, Views: recorderOAuthViews{},
			}),
			WithSessions(sessions))
		return err
	}
	for _, tc := range []struct {
		name     string
		sessions SessionsConfig
		collides bool
	}{
		{"oauth2 issuer", SessionsConfig{Issuer: "https://issuer.example"}, true},
		{"external provider issuer", SessionsConfig{Issuer: "https://idp.example.test"}, true},
		{"mcp audience", SessionsConfig{Issuer: "https://auth.example", Audience: "https://mcp.example"}, true},
		{"api audience", SessionsConfig{Audience: "https://api.example"}, true},
		{"distinct", SessionsConfig{Issuer: "https://auth.example", Audience: "app"}, false},
	} {
		err := build(tc.sessions)
		if tc.collides != errors.Is(err, ErrTokenAudienceCollides) || (!tc.collides && err != nil) {
			t.Errorf("%s: err = %v, collides want %v", tc.name, err, tc.collides)
		}
	}
}
