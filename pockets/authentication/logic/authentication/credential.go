package authentication

import (
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
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
)

// Transport names how a credential reached the server. It is the axis
// orthogonal to CredentialKind: an access token is the same credential whether
// it rode the header or the cookie, and a surface may deliberately read only one
// of them (a browser page reads its cookie; an API host reads headers).
type Transport string

const (
	// TransportHeader is `Authorization: Bearer <token>`.
	TransportHeader Transport = "header"
	// TransportCookie is the access-JWT session cookie.
	TransportCookie Transport = "cookie"
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
}

// The named helpers below are one-line pre-compositions of RequirePrincipal, so
// the common postures read as vocabulary at the call site instead of an option
// list. Each is LITERALLY the call its name describes; a host that needs a set
// none of them spells composes RequirePrincipal directly.
