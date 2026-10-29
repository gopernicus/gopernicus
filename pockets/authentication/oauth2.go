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
	// DelegatedAssurance (opt-in, SOC 2 S4d part 4) signs the approving
	// session's aal and methods on delegated access tokens, so a grant approved
	// at AAL2 passes RequireMFA on a route that admits its audience; the
	// bundled admin and machine routes never admit a delegated token. New
	// refuses it unless Repositories.OAuth2 implements
	// oauth2.CodeAuthenticationRepository (ErrDelegatedAssuranceRequires) over
	// migration 0030 (ErrDelegatedAssuranceSchemaMissing). It sets
	// Server.DelegatedAssurance; see that field for the rollout and rollback.
	// Every process that verifies delegated tokens must run a core that admits
	// the claims before it is set.
	DelegatedAssurance bool
}

// WithOAuth2 opts into a separate delegated session lifecycle. Omitting this
// option keeps all authorization-server and connection-management routes absent.
func WithOAuth2(value OAuth2Config) Option {
	value.Server.ConfidentialSecretHashes = slices.Clone(value.Server.ConfidentialSecretHashes)
	return func(c *constructorConfig) {
		copy := value
		copy.Server.ConfidentialSecretHashes = slices.Clone(value.Server.ConfidentialSecretHashes)
		copy.Server.DelegatedAssurance = copy.Server.DelegatedAssurance || copy.DelegatedAssurance
		c.OAuth2 = &copy
	}
}
