package authsvc

import "github.com/gopernicus/gopernicus/sdk/foundation/web"

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
	// SessionID is the access JWT's session_id claim. It is PROVEN live only
	// after a Live() gate; a stateless gate leaves it merely claimed.
	SessionID string
	// APIKeyID is the resolved key's id (api_key only).
	APIKeyID string
	// ServiceAccountID is the owning account (api_key only), act-as-user or not.
	ServiceAccountID string
	// ActAsUser reports whether the key's account resolves to a human owner
	// (api_key only).
	ActAsUser bool
}

// PrincipalOption narrows the credential set an authenticator admits. The
// options are OR-sets over credentials and transports plus a liveness tier and a
// browser denial mode; see Service.RequirePrincipal.
type PrincipalOption func(*principalSet)

// principalSet is the resolved posture of one RequirePrincipal instance: which
// credentials it admits, which transports it reads, whether it pays the live
// session lookup, and how it denies. It is built once at construction and never
// mutated while serving.
type principalSet struct {
	accessToken bool
	apiKey      bool
	header      bool
	cookie      bool
	live        bool
	browser     bool
}

// Accept is the OR-set of credential kinds the authenticator admits. The default
// is every wired kind (access token always; API key when MachineEnabled). Zero
// arguments panic: a set that admits nothing is a programming error, not a
// posture.
func Accept(kinds ...CredentialKind) PrincipalOption {
	if len(kinds) == 0 {
		panic("authsvc: Accept requires at least one credential kind")
	}
	for _, k := range kinds {
		if k != CredentialAccessToken && k != CredentialAPIKey {
			panic("authsvc: Accept: unknown credential kind " + string(k))
		}
	}
	return func(set *principalSet) {
		set.accessToken, set.apiKey = false, false
		for _, k := range kinds {
			switch k {
			case CredentialAccessToken:
				set.accessToken = true
			case CredentialAPIKey:
				set.apiKey = true
			}
		}
	}
}

// Transports is the OR-set of transports the authenticator reads. The default is
// both, with the header authoritative. A credential arriving on a transport
// outside the set is IGNORED, never denied: the set says what the surface reads,
// so a never-consulted header is not a bypass. Zero arguments panic, like
// Accept.
func Transports(ts ...Transport) PrincipalOption {
	if len(ts) == 0 {
		panic("authsvc: Transports requires at least one transport")
	}
	for _, t := range ts {
		if t != TransportHeader && t != TransportCookie {
			panic("authsvc: Transports: unknown transport " + string(t))
		}
	}
	return func(set *principalSet) {
		set.header, set.cookie = false, false
		for _, t := range ts {
			switch t {
			case TransportHeader:
				set.header = true
			case TransportCookie:
				set.cookie = true
			}
		}
	}
}

// Live raises the authenticator to the immediate-revocation tier: an access
// token's session row must exist (one PK lookup, failing CLOSED on a missing,
// expired, or unreadable row), and the proven session id is stashed for
// CurrentSessionID. An API key passes without another lookup — it was fully
// DB-checked during resolution and owns no session row.
func Live() PrincipalOption {
	return func(set *principalSet) { set.live = true }
}

// Browser switches the denial from a JSON 401 to a 303 toward
// Config.BrowserLoginPath, carrying a validated return_to on GET/HEAD (design
// §9.2). Mount it deliberately on HTML routes; it never sniffs Accept or Fetch
// Metadata.
func Browser() PrincipalOption {
	return func(set *principalSet) { set.browser = true }
}

// defaultSet is the posture of an option-free RequirePrincipal: every WIRED
// credential kind — the access token always (a TokenSigner is required), the API
// key only when the machine subsystem is wired — over both transports,
// stateless, denying with JSON.
func defaultSet(s *Service) principalSet {
	return principalSet{
		accessToken: true,
		apiKey:      s.MachineEnabled(),
		header:      true,
		cookie:      true,
	}
}

// resolveSet applies opts over the service's default set. It runs at middleware
// CONSTRUCTION, so an empty Accept/Transports panics at wiring time rather than
// on a request.
func (s *Service) resolveSet(opts []PrincipalOption) principalSet {
	set := defaultSet(s)
	for _, opt := range opts {
		opt(&set)
	}
	return set
}

// admits reports whether an already-resolved credential falls inside this set —
// the nested-narrowing check, which never re-resolves.
func (set principalSet) admits(kind CredentialKind, transport Transport) bool {
	switch kind {
	case CredentialAccessToken:
		if !set.accessToken {
			return false
		}
	case CredentialAPIKey:
		if !set.apiKey {
			return false
		}
	default:
		return false
	}
	switch transport {
	case TransportHeader:
		return set.header
	case TransportCookie:
		return set.cookie
	}
	return false
}

// The named helpers below are one-line pre-compositions of RequirePrincipal, so
// the common postures read as vocabulary at the call site instead of an option
// list. Each is LITERALLY the call its name describes; a host that needs a set
// none of them spells composes RequirePrincipal directly.

// RequireAccessTokenOrAPIKey admits every wired credential over both transports,
// statelessly — the posture most read routes want.
func (s *Service) RequireAccessTokenOrAPIKey() web.Middleware {
	return s.RequirePrincipal()
}

// RequireAccessTokenOrAPIKeyLive is the same OR-set at the immediate-revocation
// tier: a revoked session denies within one round-trip, while an API key (already
// DB-checked at resolution) still passes.
func (s *Service) RequireAccessTokenOrAPIKeyLive() web.Middleware {
	return s.RequirePrincipal(Live())
}

// RequireAccessToken admits a person's access token over either transport — an
// API key is refused even when it is otherwise valid.
func (s *Service) RequireAccessToken() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken))
}

// RequireAccessTokenLive is the access-token-only gate at the
// immediate-revocation tier — the "a key never mints a key" posture.
func (s *Service) RequireAccessTokenLive() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken), Live())
}

// RequireAccessTokenCookie reads the session cookie ONLY: a header credential is
// never consulted, the browser-app posture.
func (s *Service) RequireAccessTokenCookie() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie))
}

// RequireAPIKey admits machines only.
func (s *Service) RequireAPIKey() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAPIKey))
}
