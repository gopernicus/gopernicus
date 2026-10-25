package authenticationhttp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var (
	errEmailUnverified = fmt.Errorf("authentication HTTP: verified email required: %w", sdk.ErrUnauthorized)
	errTenantRefused   = fmt.Errorf("authentication HTTP: tenant not admitted: %w", sdk.ErrUnauthorized)
	errCheckRefused    = fmt.Errorf("authentication HTTP: credential check failed: %w", sdk.ErrUnauthorized)
)

// PrincipalOption configures the credential set an authenticator admits. The
// options configure credentials, transports, profile, audience, liveness and
// denial behavior; see Adapter.RequirePrincipal.
type PrincipalOption func(*principalSet)

// principalSet is the resolved posture of one RequirePrincipal instance: which
// credentials it admits, which transports it reads, its liveness tier, the
// assurance it demands of external tokens, and how it denies. It is built once
// at construction and never mutated while serving.
type principalSet struct {
	accessToken        bool
	apiKey             bool
	externalToken      bool
	externalWired      bool
	header             bool
	cookie             bool
	liveness           LivenessTier
	browser            bool
	optional           bool
	firstParty         bool
	audiences          []string
	delegatedAudiences []string
	assurances         []assurance
}

// assurance is one assurance option. RequireMFA reads Credential.Assurance,
// which access tokens carry too, so it evaluates them (accessTokens); every
// other option reads external provider evidence only.
type assurance struct {
	check        func(context.Context, Credential) error
	accessTokens bool
}

// insufficientAuthentication is an assurance denial a re-authentication can
// cure; JSON denials answer it with the RFC 9470 challenge.
type insufficientAuthentication struct {
	description string
	maxAge      time.Duration
}

func (e *insufficientAuthentication) Error() string {
	return "authentication HTTP: insufficient user authentication: " + e.description
}

func (e *insufficientAuthentication) Unwrap() error { return sdk.ErrUnauthorized }

// challenge is the RFC 9470 WWW-Authenticate value.
func (e *insufficientAuthentication) challenge() string {
	value := `Bearer error="insufficient_user_authentication", error_description="` + e.description + `"`
	if e.maxAge > 0 {
		value += `, max_age="` + strconv.FormatInt(int64(e.maxAge/time.Second), 10) + `"`
	}
	return value
}

// Audience admits tokens issued for one of the exact resource identifiers.
// Audience-less first-party tokens and API keys cannot satisfy this gate. Use
// DelegatedAudience to opt delegated tokens into a shared first-party/API-key
// route. Repeated Audience options replace this gate's set.
func Audience(resources ...string) PrincipalOption {
	if len(resources) == 0 {
		panic("authsvc: Audience requires at least one resource")
	}
	for _, resource := range resources {
		if strings.TrimSpace(resource) == "" || resource != strings.TrimSpace(resource) {
			panic("authsvc: Audience requires nonempty resource identifiers without surrounding whitespace")
		}
	}
	resources = slices.Clone(resources)
	return func(set *principalSet) { set.audiences = resources }
}

// DelegatedAudience admits delegated access tokens issued for one of the exact
// resources without changing first-party or API-key admission. Accept,
// Transports and FirstParty still apply; delegated tokens remain header-only
// and always require live verification. When Audience is also present, both
// audience gates must match, regardless of option order. Repeated
// DelegatedAudience options replace this gate's set.
func DelegatedAudience(resources ...string) PrincipalOption {
	if len(resources) == 0 {
		panic("authsvc: DelegatedAudience requires at least one resource")
	}
	for _, resource := range resources {
		if strings.TrimSpace(resource) == "" || resource != strings.TrimSpace(resource) {
			panic("authsvc: DelegatedAudience requires nonempty resource identifiers without surrounding whitespace")
		}
	}
	resources = slices.Clone(resources)
	return func(set *principalSet) { set.delegatedAudiences = resources }
}

// FirstParty requires a first-party access token, regardless of transport.
// No API key, delegated token or external token satisfies this gate.
func FirstParty() PrincipalOption {
	return func(set *principalSet) { set.firstParty = true }
}

// Accept is the OR-set of credential kinds the authenticator admits. The default
// is every wired kind (access token always; API key when MachineEnabled;
// external token when ExternalEnabled). A kind the service has not wired is
// never resolved. Zero arguments panic: a set that admits nothing is a
// programming error, not a posture.
func Accept(kinds ...CredentialKind) PrincipalOption {
	if len(kinds) == 0 {
		panic("authsvc: Accept requires at least one credential kind")
	}
	for _, k := range kinds {
		if k != CredentialAccessToken && k != CredentialAPIKey && k != CredentialExternalToken {
			panic("authsvc: Accept: unknown credential kind " + string(k))
		}
	}
	kinds = slices.Clone(kinds)
	return func(set *principalSet) {
		set.accessToken, set.apiKey, set.externalToken = false, false, false
		for _, k := range kinds {
			switch k {
			case CredentialAccessToken:
				set.accessToken = true
			case CredentialAPIKey:
				set.apiKey = true
			case CredentialExternalToken:
				set.externalToken = true
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

// Live re-checks on EVERY request, safe reads included: an access token's
// session row must exist (one PK lookup; a missing or expired row denies, an
// unreadable one answers 503), and the proven session id is stashed for
// CurrentSessionID. An external token re-reads local user status and the
// provider's revocation facts and always fails closed, whatever the provider's
// RevocationFailOpen. An API key passes without another lookup — it was fully
// DB-checked during resolution and owns no session row. Delegated tokens always
// require a fresh live check whatever the tier.
func Live() PrincipalOption {
	return func(set *principalSet) { set.liveness = LivenessLive }
}

// LiveOnMutation is the default tier: unsafe methods (anything but GET, HEAD and
// OPTIONS) re-check exactly as Live does, while safe reads pass on credential
// validity alone. A provider's RevocationFailOpen relaxes only this tier.
func LiveOnMutation() PrincipalOption {
	return func(set *principalSet) { set.liveness = LivenessOnMutation }
}

// Stateless never re-checks: an access token passes on its signature and expiry
// until it expires, an external token on its validity and the local user status
// read at resolution. It is the per-route escape from the LiveOnMutation
// default. Delegated tokens still check their live grant.
func Stateless() PrincipalOption {
	return func(set *principalSet) { set.liveness = LivenessStateless }
}

// RequireMFA requires an access or external token at AAL2
// (Credential.Assurance): an access token whose session a user-verified passkey
// signed in or stepped up, or an external token whose provider asserted
// multi-factor authentication. An API key passes (no key can reach AAL2). A
// JSON denial carries the RFC 9470 insufficient_user_authentication challenge;
// under Browser() an access-token cookie below AAL2 is redirected to the
// passkey step-up page when passkeys and their views are wired, and otherwise
// gets that JSON 401. A credential service that implements MFADenialRecorder
// records one mfa_required_denied row (gate route).
func RequireMFA() PrincipalOption {
	return func(set *principalSet) {
		set.assurances = append(slices.Clip(set.assurances), assurance{accessTokens: true, check: func(_ context.Context, cred Credential) error {
			if !cred.Assurance.AtLeast(session.AssuranceAAL2) {
				return authlogic.ErrMFARequired
			}
			return nil
		}})
	}
}

// FreshAuth requires an external token whose subject authenticated at the
// provider no longer than within ago. A JSON denial carries the RFC 9470
// challenge with max_age. A non-positive within panics.
func FreshAuth(within time.Duration) PrincipalOption {
	if within <= 0 {
		panic("authsvc: FreshAuth requires a positive duration")
	}
	return withAssurance(func(_ context.Context, cred Credential) error {
		at := cred.Identity.AuthenticatedAt
		if at.IsZero() || time.Since(at) > within {
			return &insufficientAuthentication{description: "more recent authentication is required", maxAge: within}
		}
		return nil
	})
}

// VerifiedEmail requires an external token whose provider verified its email.
func VerifiedEmail() PrincipalOption {
	return withAssurance(func(_ context.Context, cred Credential) error {
		if cred.Identity.Email == "" || !cred.Identity.EmailVerified {
			return errEmailUnverified
		}
		return nil
	})
}

// Tenant requires an external token whose provider tenant is exactly one of ids.
func Tenant(ids ...string) PrincipalOption {
	if len(ids) == 0 || slices.Contains(ids, "") {
		panic("authsvc: Tenant requires nonempty tenant ids")
	}
	ids = slices.Clone(ids)
	return withAssurance(func(_ context.Context, cred Credential) error {
		if !slices.Contains(ids, cred.Identity.Tenant) {
			return errTenantRefused
		}
		return nil
	})
}

// Check runs fn over an external token's credential; an error denies (an
// sdk.ErrUnavailable one answers 503). fn reads the verified Identity,
// including the host-mapped Attributes; no raw claims are carried. Provider
// claims are identity data, never permissions. A nil fn panics.
func Check(fn func(ctx context.Context, cred Credential) error) PrincipalOption {
	if fn == nil {
		panic("authsvc: Check requires a function")
	}
	return withAssurance(func(ctx context.Context, cred Credential) error {
		if err := fn(ctx, cred); err != nil {
			if errors.Is(err, sdk.ErrUnavailable) {
				return err
			}
			return fmt.Errorf("%w: %w", errCheckRefused, err)
		}
		return nil
	})
}

// withAssurance adds an option that reads external provider evidence.
func withAssurance(check func(context.Context, Credential) error) PrincipalOption {
	return func(set *principalSet) { set.assurances = append(slices.Clip(set.assurances), assurance{check: check}) }
}

// Browser switches the denial from a JSON 401 to a 303 toward
// the WithBrowserLoginPath setting, carrying a validated return_to on GET/HEAD (design
// §9.2). Mount it deliberately on HTML routes; it never sniffs Accept or Fetch
// Metadata.
// An outermost missing/invalid access-cookie proof adds recover=1 on GET/HEAD
// when this posture admits first-party cookies, allowing the bundled login page
// to renew a still-live refresh session. Authoritative bearers, provider
// cookies, resolved-proof denials, and unsafe methods never request recovery. A
// service outage still answers the JSON 503, never the redirect.
func Browser() PrincipalOption {
	return func(set *principalSet) { set.browser = true }
}

// Optional switches the OUTERMOST authenticator from deny-by-absence to
// pass-by-absence: no credential presented within the set continues the
// request with NO principal and NO credential stashed (CurrentPrincipal /
// CurrentCredential report false), skipping the Live() tier and the Browser()
// redirect. A credential presented within the set is resolved exactly as
// today — denied when invalid, expired, revoked, or of a kind outside the
// set — so a stale or wrong-kind credential is still a 401 (or 303), and an
// outage a 503, never laundered into anonymity. A credential arriving on a transport outside the
// set is IGNORED, as for a required posture, so an anonymous pass there is
// correct.
func Optional() PrincipalOption {
	return func(set *principalSet) { set.optional = true }
}

// defaultSet is the posture of an option-free RequirePrincipal: every WIRED
// credential kind — the access token always (a TokenSigner is required), the API
// key only when the machine subsystem is wired, the external token only when
// identity providers are wired — over both transports, at the LiveOnMutation
// tier, denying with JSON. Delegated tokens require an explicit Audience or
// DelegatedAudience.
func defaultSet(s *Adapter) principalSet {
	external := s.service.ExternalEnabled()
	return principalSet{
		accessToken:   true,
		apiKey:        s.service.MachineEnabled(),
		externalToken: external,
		externalWired: external,
		header:        true,
		cookie:        true,
		liveness:      LivenessOnMutation,
	}
}

// resolveSet applies opts over the service's default set. It runs at middleware
// CONSTRUCTION, so a malformed posture panics at wiring time rather than on a
// request: an empty Accept/Transports, FreshAuth, VerifiedEmail, Tenant or
// Check on a set that also admits access tokens (which carry no provider
// evidence; RequireMFA does not panic), or Browser() on a set admitting
// external tokens without an explicit WithBrowserLoginPath (the default
// /auth/login is not mounted on an external-only host).
func (s *Adapter) resolveSet(opts []PrincipalOption) principalSet {
	set := defaultSet(s)
	for _, opt := range opts {
		opt(&set)
	}
	if set.accessToken && slices.ContainsFunc(set.assurances, func(a assurance) bool { return !a.accessTokens }) {
		panic("authsvc: FreshAuth, VerifiedEmail, Tenant and Check read external provider evidence and require a set that does not admit access tokens; add Accept(CredentialExternalToken, ...)")
	}
	if set.browser && set.resolves(CredentialExternalToken) && !s.browserLoginSet {
		panic("authsvc: Browser() on a set admitting external tokens requires WithBrowserLoginPath")
	}
	return set
}

// resolves reports whether the resolver may verify kind for this set: admitted,
// and for external tokens also wired.
func (set principalSet) resolves(kind CredentialKind) bool {
	switch kind {
	case CredentialAccessToken:
		return set.accessToken
	case CredentialExternalToken:
		return set.externalToken && set.externalWired
	}
	return false
}

// assure applies the set's assurance options in option order: every option to
// an external token, RequireMFA alone to an access token (a set admitting
// access tokens carries no other), none to an API key.
func (set principalSet) assure(ctx context.Context, cred Credential) error {
	switch cred.Kind {
	case CredentialExternalToken, CredentialAccessToken:
	default:
		return nil
	}
	for _, a := range set.assurances {
		if cred.Kind == CredentialAccessToken && !a.accessTokens {
			continue
		}
		if err := a.check(ctx, cred); err != nil {
			return err
		}
	}
	return nil
}

// admits reports whether an already-resolved credential falls inside this set —
// the nested-narrowing check, which never re-resolves.
func (set principalSet) admits(cred Credential) bool {
	switch cred.Kind {
	case CredentialAccessToken:
		if !set.accessToken {
			return false
		}
		switch cred.Profile {
		case session.ProfileFirstParty:
		case session.ProfileDelegated:
			if set.firstParty || cred.Transport != TransportHeader || (len(set.audiences) == 0 && len(set.delegatedAudiences) == 0) {
				return false
			}
			if len(set.delegatedAudiences) != 0 && !slices.ContainsFunc(cred.Audiences, func(audience string) bool {
				return slices.Contains(set.delegatedAudiences, audience)
			}) {
				return false
			}
		default:
			return false
		}
	case CredentialAPIKey:
		if !set.apiKey || set.firstParty || len(set.audiences) != 0 {
			return false
		}
	case CredentialExternalToken:
		if !set.externalToken || set.firstParty || len(set.audiences) != 0 || len(set.delegatedAudiences) != 0 {
			return false
		}
	default:
		return false
	}
	if len(set.audiences) != 0 && !slices.ContainsFunc(cred.Audiences, func(audience string) bool {
		return slices.Contains(set.audiences, audience)
	}) {
		return false
	}
	switch cred.Transport {
	case TransportHeader:
		return set.header
	case TransportCookie:
		return set.cookie
	}
	return false
}

// RequireAccessTokenOrAPIKey admits a person's access token or an API key over
// both transports at the default LiveOnMutation tier. It is literal: external
// tokens are not admitted even when providers are wired.
func (s *Adapter) RequireAccessTokenOrAPIKey() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken, CredentialAPIKey))
}

// RequireAccessTokenOrAPIKeyLive is the same OR-set at the Live tier: a revoked
// session denies within one round-trip on every method, while an API key
// (already DB-checked at resolution) still passes.
func (s *Adapter) RequireAccessTokenOrAPIKeyLive() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken, CredentialAPIKey), Live())
}

// RequireAccessToken admits a person's access token over either transport — an
// API key is refused even when it is otherwise valid.
func (s *Adapter) RequireAccessToken() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialAccessToken))
}

// RequireAccessTokenLive is the access-token-only gate at the Live tier — the
// "a key never mints a key" posture.
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

// RequireExternalToken admits an external identity provider's token, by header
// or provider cookie, at the default LiveOnMutation tier.
func (s *Adapter) RequireExternalToken() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialExternalToken))
}

// RequireExternalTokenOrAPIKey admits an external token or an API key — the
// hybrid chain of a host whose people sign in at a provider and whose machines
// stay native.
func (s *Adapter) RequireExternalTokenOrAPIKey() web.Middleware {
	return s.RequirePrincipal(Accept(CredentialExternalToken, CredentialAPIKey))
}
