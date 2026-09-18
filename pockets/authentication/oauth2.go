package authentication

import (
	"slices"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
)

// OAuth2Config enables the authorization server for this host's MCP resource.
// Clients must enforce the host's trust allowlist on every resolution. Production
// hosts can use outbound/oauth2.NewCIMD; the pocket never infers client trust.
type OAuth2Config struct {
	Server  oauth2.Config
	Clients oauth2.ClientResolver
	Views   inbound.OAuthViews
	// CapabilityDescription is trusted host copy describing the exposed tools.
	CapabilityDescription string
}

// WithOAuth2 opts into a separate delegated session lifecycle. Omitting this
// option keeps all authorization-server and connection-management routes absent.
func WithOAuth2(value OAuth2Config) Option {
	value.Server.ConfidentialSecretHashes = slices.Clone(value.Server.ConfidentialSecretHashes)
	return func(c *constructorConfig) {
		copy := value
		copy.Server.ConfidentialSecretHashes = slices.Clone(value.Server.ConfidentialSecretHashes)
		c.OAuth2 = &copy
	}
}
