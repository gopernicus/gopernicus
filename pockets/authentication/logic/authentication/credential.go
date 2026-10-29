package authentication

import (
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// CredentialKind names the class of credential a request authenticated with. The
// two kinds are the pocket's whole authentication surface (design §4.3): the
// session-backed access JWT, and a service account's API key.
type CredentialKind string

const (
	// CredentialAccessToken is the session-backed JWT (claims user_id +
	// session_id). It arrives by either transport: the Authorization header, or
	// the session cookie whose VALUE is that same access JWT.
	CredentialAccessToken CredentialKind = "access_token"
	// CredentialAPIKey is a service account's key. It resolves to its own
	// service-account principal, or — for an act-as-user account — to the human
	// owner it acts as.
	CredentialAPIKey CredentialKind = "api_key"
	// CredentialExternalToken is a token issued by a wired external identity
	// provider. It resolves through the subject mirror to a local user and carries
	// the verified sdk identity in Credential.Identity.
	CredentialExternalToken CredentialKind = "external_token"
)

// Transport names how a credential reached the server. It is the axis
// orthogonal to CredentialKind: an access token is the same credential whether
// it rode the header or the cookie, and a surface may deliberately read only one
// of them (a browser page reads its cookie; an API host reads headers).
type Transport string

const (
	// TransportHeader is `Authorization: Bearer <token>`.
	TransportHeader Transport = "header"
	// TransportCookie is the access-JWT session cookie, or a provider's own
	// session cookie for an external token.
	TransportCookie Transport = "cookie"
)

// LivenessTier selects how much a request re-checks beyond credential validity.
// The zero value is LivenessOnMutation, the default.
type LivenessTier int

const (
	// LivenessOnMutation re-checks on unsafe methods and lets safe reads pass on
	// validity alone. A per-provider RevocationFailOpen relaxes only this tier.
	LivenessOnMutation LivenessTier = iota
	// LivenessLive re-checks on every request and always fails closed.
	LivenessLive
	// LivenessStateless never re-checks. Delegated tokens still check their grant.
	LivenessStateless
)

// Credential is what the authenticator stashes beside the Principal: the proof
// the request actually presented, so a handler can tell an act-as-user API key
// from a person's session rather than seeing one indistinguishable Principal.
// The zero value means unauthenticated. It is pocket-owned (never sdk identity
// vocabulary) and read through Service.CurrentCredential.
type Credential struct {
	Kind      CredentialKind
	Transport Transport
	// SessionID is the access JWT's session_id claim. First-party stateless
	// authentication leaves it merely claimed; delegated authentication always
	// checks its live session before returning a credential.
	SessionID string
	// Profile is verified independently of transport. Delegated credentials always
	// require a live session; first-party access tokens retain their stateless tier.
	Profile session.Profile
	// Issuer, Audiences and client attribution are verified delegated claims.
	// First-party credentials and API keys leave them empty.
	Issuer          string
	Audiences       []string
	ClientID        string
	OriginClientID  string
	ActorID         string
	accessExpiresAt time.Time
	// APIKeyID is the resolved key's id (api_key only).
	APIKeyID string
	// ServiceAccountID is the owning account (api_key only), act-as-user or not.
	ServiceAccountID string
	// ActAsUser reports whether the key's account resolves to a human owner
	// (api_key only).
	ActAsUser bool
	// Identity is the provider-verified identity (external_token only). It is a
	// named field because an embedded one would shadow Issuer. Native kinds leave
	// it zero, and external tokens leave Issuer, Audiences and Profile zero.
	Identity sdkauth.Identity
	// Assurance is the authenticator assurance the credential proves. A
	// first-party access token carries its session's assurance from the signed
	// aal claim — AAL2 after a user-verified passkey, AAL1 otherwise — and a
	// token minted before the claim existed reads AssuranceUnknown until its
	// next refresh. A delegated token carries its grant's approving-session
	// assurance when the issuer set OAuth2 DelegatedAssurance, and
	// AssuranceUnknown otherwise. An external token is AAL2 when its provider
	// asserted multi-factor authentication (Identity.MFA) and AAL1 otherwise.
	// API keys leave it AssuranceUnknown. RequireMFA and the MFA gates read it,
	// so a host-implemented CredentialService must stamp it.
	Assurance session.AssuranceLevel
	// Methods are the method kinds behind Assurance, which an MFAConfig.Methods
	// gate reads to tell which source produced AAL2. A first-party access token
	// carries its session's kinds from the signed methods claim (a token minted
	// before the claim existed carries none until its next refresh); a delegated
	// token carries its approving session's kinds under the same condition as
	// Assurance; an external token carries session.MethodExternal. API keys
	// leave it empty.
	Methods []session.MethodKind
	// MFAEnrollBy is when the holder's enrollment grace closes under
	// MFAConfig.Required all beside passkeys or TOTP: a first-party access
	// token below AAL2 whose holder holds no AAL2 factor carries it from the
	// signed mfa_enroll_by claim. Every other credential — AAL2, enrolled,
	// delegated, an API key, an external token, the floor off — leaves it zero.
	MFAEnrollBy time.Time
	// MFAEnrolled reports, under the same policy, a first-party access token
	// below AAL2 whose holder holds an AAL2 factor (the signed mfa_enrolled
	// claim): the enrollment floor asks it to step up.
	MFAEnrolled bool
}

// The named helpers below are one-line pre-compositions of RequirePrincipal, so
// the common postures read as vocabulary at the call site instead of an option
// list. Each is LITERALLY the call its name describes; a host that needs a set
// none of them spells composes RequirePrincipal directly.
