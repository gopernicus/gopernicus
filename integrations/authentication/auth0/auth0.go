// Package auth0 is the Auth0 preset for sdk/capabilities/authentication. It
// imports no library: like integrations/oauth/github, it isolates a vendor
// contract — Auth0's issuer format and claim names — which changes on Auth0's
// schedule, not sdk's. Hosts compose it in cmd with a generic OIDC verifier
// (integrations/cryptids/go-oidc):
//
//	verifier, err := gooidc.New(ctx, auth0.Issuer(domain), gooidc.WithAudiences(apiAudience))
//	provider := sdkauth.NewOIDC(auth0.Issuer(domain), verifier, auth0.Profile(domain))
//
// Auth0 has no server-minted application session and no revocation endpoint
// for access tokens, so the preset is header-only and implements no
// RevocationChecker; a token is valid until it expires.
package auth0

import (
	"strings"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

const (
	// ClaimOrganization is the Auth0 Organizations claim mapped to Identity.Tenant.
	ClaimOrganization = "org_id"

	// ClaimGrantType names how an access token was obtained ("client-credentials"
	// for machine-to-machine tokens).
	ClaimGrantType = "gty"

	// GrantClientCredentials is the gty of a machine-to-machine access token.
	GrantClientCredentials = "client-credentials"

	// AttributeAuthorizedParty is the Identity.Attributes key carrying azp, the
	// client the token was issued to.
	AttributeAuthorizedParty = "azp"
)

// Issuer returns the iss Auth0 puts in a tenant's tokens: https://<domain>/,
// trailing slash included, exactly as discovery publishes it. domain is the
// tenant or custom domain ("example.us.auth0.com"); a scheme and trailing
// slash are tolerated. It panics on an empty domain or one carrying a path,
// which would otherwise pin an issuer no token carries.
func Issuer(domain string) string {
	d := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(domain), "https://"), "/")
	if d == "" || strings.ContainsAny(d, "/ ") {
		panic("auth0.Issuer: domain must be a bare host such as example.us.auth0.com")
	}
	return "https://" + d + "/"
}

// DefaultDenySignIn returns the grant types refused by default:
// client-credentials, because machine credentials stay native API keys and a
// machine token has no user to map.
func DefaultDenySignIn() []string { return []string{GrantClientCredentials} }

// Profile maps Auth0 tokens for authentication.NewOIDC. Standard OIDC claims
// keep the defaults (sub, email, email_verified, name, phone_number,
// phone_number_verified, auth_time else iat, amr — Auth0 puts "mfa" in amr
// after a second factor). org_id is the tenant; gty names the grant so
// machine tokens are refused; azp is copied into Identity.Attributes. Access
// tokens carry email and name only when an Auth0 Action adds them, typically
// under a namespaced claim the host maps itself. The mapping is the same for
// every tenant; domain is taken for symmetry with Issuer.
func Profile(domain string) sdkauth.Profile {
	return sdkauth.Profile{
		Claims: sdkauth.ClaimMapping{
			Tenant:       ClaimOrganization,
			SignInMethod: ClaimGrantType,
			Extra:        []string{AttributeAuthorizedParty},
		},
		DenySignIn: DefaultDenySignIn(),
	}
}
