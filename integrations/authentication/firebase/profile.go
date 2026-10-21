package firebase

import (
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// AttributeSignInProvider is the Identity.Attributes key carrying
// firebase.sign_in_provider ("password", "google.com", "phone", …).
const AttributeSignInProvider = "firebase.sign_in_provider"

// DefaultDenySignIn returns the sign-in providers refused by default: anonymous
// users, which carry no identifier a consumer can provision.
func DefaultDenySignIn() []string { return []string{SignInAnonymous} }

// IDTokenIssuer is the iss of a project's ID tokens, also the issuer a
// generic OIDC verifier discovers keys from. The audience is the project id.
func IDTokenIssuer(projectID string) string { return idTokenIssuerPrefix + projectID }

// SessionCookieIssuer is the iss of a project's session cookies.
func SessionCookieIssuer(projectID string) string { return sessionCookieIssuerPrefix + projectID }

// Profile maps Firebase ID-token claims for authentication.NewOIDC over a
// generic OIDC verifier pinned to IDTokenIssuer(projectID) with audience
// projectID. That path is header-only: no revocation facts, no session
// cookies. The resulting Identity matches what New produces. The mapping is
// the same for every project; projectID is taken for symmetry with the issuer
// helpers and future project-scoped claims.
func Profile(projectID string) sdkauth.Profile {
	return sdkauth.Profile{
		Claims: sdkauth.ClaimMapping{
			SignInMethod: "firebase.sign_in_provider",
			Tenant:       "firebase.tenant",
			MFAFlag:      "firebase.sign_in_second_factor",
			Extra:        []string{AttributeSignInProvider},
		},
		TrustPhone: true,
		DenySignIn: DefaultDenySignIn(),
	}
}
