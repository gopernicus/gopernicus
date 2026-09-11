package authenticationhttp

import (
	"slices"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

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
	optional    bool
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
	kinds = slices.Clone(kinds)
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
	ts = slices.Clone(ts)
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
// the WithBrowserLoginPath setting, carrying a validated return_to on GET/HEAD (design
// §9.2). Mount it deliberately on HTML routes; it never sniffs Accept or Fetch
// Metadata.
func Browser() PrincipalOption {
	return func(set *principalSet) { set.browser = true }
}

// Optional switches the OUTERMOST authenticator from deny-by-absence to
// pass-by-absence: no credential presented within the set continues the
// request with NO principal and NO credential stashed (CurrentPrincipal /
// CurrentCredential report false), skipping the Live() tier and the Browser()
// redirect. A credential presented within the set is resolved exactly as
// today — denied when invalid, expired, revoked, or of a kind outside the
// set — so a stale or wrong-kind credential is still a 401 (or 303), never
// laundered into anonymity. A credential arriving on a transport outside the
// set is IGNORED, as for a required posture, so an anonymous pass there is
// correct.
func Optional() PrincipalOption {
	return func(set *principalSet) { set.optional = true }
}

// defaultSet is the posture of an option-free RequirePrincipal: every WIRED
// credential kind — the access token always (a TokenSigner is required), the API
// key only when the machine subsystem is wired — over both transports,
// stateless, denying with JSON.
func defaultSet(s *Adapter) principalSet {
	return principalSet{
		accessToken: true,
		apiKey:      s.service.MachineEnabled(),
		header:      true,
		cookie:      true,
	}
}

// resolveSet applies opts over the service's default set. It runs at middleware
// CONSTRUCTION, so an empty Accept/Transports panics at wiring time rather than
// on a request.
func (s *Adapter) resolveSet(opts []PrincipalOption) principalSet {
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

// RequireAccessTokenOrAPIKey admits every wired credential over both transports,
// statelessly — the posture most read routes want.
func (s *Adapter) RequireAccessTokenOrAPIKey() web.Middleware {
	return s.RequirePrincipal()
}

// RequireAccessTokenOrAPIKeyLive is the same OR-set at the immediate-revocation
// tier: a revoked session denies within one round-trip, while an API key (already
// DB-checked at resolution) still passes.
func (s *Adapter) RequireAccessTokenOrAPIKeyLive() web.Middleware {
	return s.RequirePrincipal(Live())
}

// RequireAccessToken admits a person's access token over either transport — an
// API key is refused even when it is otherwise valid.
func (s *Adapter) RequireAccessToken() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken))
}

// RequireAccessTokenLive is the access-token-only gate at the
// immediate-revocation tier — the "a key never mints a key" posture.
func (s *Adapter) RequireAccessTokenLive() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken), Live())
}

// RequireAccessTokenCookie reads the session cookie ONLY: a header credential is
// never consulted, the browser-app posture.
func (s *Adapter) RequireAccessTokenCookie() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie))
}

// RequireAPIKey admits machines only.
func (s *Adapter) RequireAPIKey() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAPIKey))
}
