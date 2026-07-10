// Package authentication is the public surface of the authentication feature module: the
// registration entry point (Register), the cross-feature identity capability
// (Service / NewService / RequireUser / CurrentUser), the host-filled ports
// (Repositories), the feature-owned PasswordHasher port, and the customization
// config (Config). Implementation lives in internal/; the domain type and
// repository-interface packages (user, session, verification) are public
// because hosts and store adapters reference them, but the services and
// handlers stay internal.
//
// The feature is datastore-free and view-free: it depends on its repository
// ports and sdk facilities only, never on a concrete store, an integration, or
// a view library. v1 is JSON-API only (see internal/http).
//
// Host-facing surface, all in this file per the feature charter's "<name>.go is
// the feature's entire host-facing surface" rule:
//
//   - Repositories — the five outbound ports a store adapter or host fills.
//   - PasswordHasher — the feature-owned hashing port (integrations/cryptids/
//     bcrypt satisfies it structurally).
//   - Config — required Hasher + Mailer (nil errors at construction), optional
//     RateLimiter (nil → in-memory), MailFrom, SessionCookie.
//   - NewService / Service.RequireUser / Service.CurrentUser — the surface a
//     host wires into another feature (e.g. cms admin gating).
//   - Register — mounts the feature's own HTTP routes.
package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	inbound "github.com/gopernicus/gopernicus/features/authentication/internal/inbound/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/identity"
	"github.com/gopernicus/gopernicus/sdk/notify"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// ErrHasherRequired and ErrMailerRequired are returned by NewService/Register
// when the corresponding required Config field is nil. Unlike cms's safe
// silent defaults (nil Cache disables caching), a password feature with no
// hasher, or one that silently drops verification/reset mail, is a security
// foot-gun — so these degrade loudly at construction, never silently.
var (
	ErrHasherRequired = errors.New("auth: Config.Hasher is required")
	ErrMailerRequired = errors.New("auth: Config.Mailer is required")
)

// ErrOAuthReposRequired is returned by NewService/Register when Config.Providers
// is non-empty but Repositories.OAuthAccounts or Repositories.OAuthStates is nil.
// OAuth is deny-by-absence — no providers means no routes and the oauth repos may
// be nil — but wiring providers without their stores is a loud partial-wiring
// error (design §3, the Hasher/Mailer precedent), never a silent half-on state.
var ErrOAuthReposRequired = errors.New("auth: Config.Providers set but Repositories.OAuthAccounts/OAuthStates is nil")

// ErrMachineReposRequired is returned by NewService/Register when exactly one of
// Repositories.ServiceAccounts and Repositories.APIKeys is wired. The machine
// identity subsystem (API keys + service accounts, design §4.1) is both-or-
// neither: both nil → subsystem off (routes not registered, the bearer API-key
// path inert); both set → on; one without the other is a loud construction error
// (cut refinement 5), never a silent half-on state.
var ErrMachineReposRequired = errors.New("auth: Repositories.ServiceAccounts and Repositories.APIKeys must be wired together (both or neither)")

// ErrInvitationRepoRequired is returned by NewService/Register when Config.Granter
// is wired but Repositories.Invitations is nil. Invitations are deny-by-absence —
// no Granter means no routes and Invitations may be nil — but wiring a Granter
// without its store is a loud partial-wiring error (design §6), never a silent
// half-on state.
var ErrInvitationRepoRequired = errors.New("auth: Config.Granter set but Repositories.Invitations is nil")

// ErrInvalidListStrategy is returned by NewService/Register when
// Config.ListStrategy is set to a value other than "cursor" or "offset". Like
// the Hasher/Mailer requirements it degrades loudly at construction (the Config
// posture), never silently defaulting a typo.
var ErrInvalidListStrategy = errors.New(`auth: Config.ListStrategy must be "cursor" or "offset"`)

// ErrInvitationsDisabled is returned by the invitation use-cases (Create, Accept,
// …) on a Service built with no Config.Granter: the invitation subsystem is off,
// so — mirroring the transport, which registers no invitation routes and 404s
// the whole surface (design §6) — the driving surface wraps sdk.ErrNotFound.
var ErrInvitationsDisabled = fmt.Errorf("auth: invitations are disabled (no Config.Granter): %w", sdk.ErrNotFound)

// ErrDuplicateNotifierKind is returned by NewService/Register when Config.Notifiers
// contains more than one notifier declaring the same kind. Unlike auth's OAuth
// provider map, which silently last-wins, the notifier set degrades LOUDLY at
// construction (the ErrOAuthReposRequired posture): a duplicate kind is an
// ambiguous delivery route, never a silent pick.
var ErrDuplicateNotifierKind = errors.New("auth: Config.Notifiers has more than one notifier for the same kind")

// ErrKindNotSupported is returned (as the wrapped cause) by Service.Create for an
// invitation identifier kind the host is not set up to deliver to
// (deny-by-absence, ruling 6): a kind is supported iff it is identity.KindEmail
// with the Mailer wired, OR a notifier of that kind is wired in Config.Notifiers.
// It wraps sdk.ErrInvalidInput, so the transport maps it to 400, and the
// invitation is NOT created. Hosts detect it with errors.Is(err,
// auth.ErrKindNotSupported).
var ErrKindNotSupported = invitationsvc.ErrKindNotSupported

// Granter is the ReBAC-decoupled grant-on-accept seam for invitations (design
// §2.2/§6, ratified AV4): Grant(ctx, resourceType, resourceID, relation,
// subjectType, subjectID). A host adapts it to whatever authorizer it runs — a
// ReBAC CreateRelationships, a role-column write, or the proof host's toy
// membership map — or wires none. The grant must be idempotent in the Granter's
// world (a duplicate accept must not error). Aliased from invitationsvc so the
// sibling service can call it without an import cycle.
type Granter = invitationsvc.Granter

// MemberCheck is the optional duplicate-membership predicate consulted before a
// direct-add grant (design §6). Nil → no dup check. Aliased from invitationsvc.
type MemberCheck = invitationsvc.MemberCheck

// CreateInput is the input to Service.Create (an invitation). Aliased from
// invitationsvc per the Principal precedent so a host wiring its own invitation
// handler names one type across the public and internal packages.
type CreateInput = invitationsvc.CreateInput

// CreateResult reports the outcome of Service.Create: DirectlyAdded true when a
// known invitee was granted immediately, else Invitation is the pending record.
type CreateResult = invitationsvc.CreateResult

// AcceptInput is the input to Service.Accept: the mailed Token plus the accepting
// caller's SubjectType/SubjectID and Identifier (email). Aliased from invitationsvc.
type AcceptInput = invitationsvc.AcceptInput

// AcceptResult reports the granted tuple's resource/relation. Aliased from invitationsvc.
type AcceptResult = invitationsvc.AcceptResult

// ErrOAuthLastMethod is returned (as the wrapped cause) by Service.Unlink when the
// target link is the user's only authentication method and no password is set —
// unlinking it would lock the account out. It wraps sdk.ErrConflict, so the
// transport maps it to 409. Hosts detect it with errors.Is(err,
// auth.ErrOAuthLastMethod).
var ErrOAuthLastMethod = authsvc.ErrLastAuthMethod

// ErrEmailNotVerified is returned (as the wrapped cause) by login when
// Config.RequireVerifiedEmail is set and the caller's email is unverified. It
// wraps sdk.ErrForbidden, so the transport maps it to 403. Hosts detect it with
// errors.Is(err, auth.ErrEmailNotVerified).
var ErrEmailNotVerified = authsvc.ErrEmailNotVerified

// Principal is the effective caller resolved from a credential (a session, an
// API key, or — when Config.TokenSigner is wired — a bearer JWT). AV5
// pins it as the one value type: actor references are (subject_type, subject_id)
// string pairs everywhere, with no principals registry table. Type is a string
// convention (Service.AuthenticateAPIKey yields "user" for an act-as-user key or
// "service_account" otherwise); the alias keeps exactly one type across the
// public and internal packages.
type Principal = authsvc.Principal

// OAuthResult is the outcome of Service.OAuthCallback / Service.VerifyLink: the
// Action taken, the session Token (empty for a pending link), the resolved User,
// and the validated RedirectTo. Aliased from authsvc per the Principal precedent.
type OAuthResult = authsvc.OAuthResult

// PasswordHasher hashes and verifies passwords. It is feature-owned (not an sdk
// facility) because it has one consumer today and none genuinely foreseen
// elsewhere. integrations/cryptids/bcrypt satisfies it structurally, with zero
// import in either direction.
type PasswordHasher interface {
	// HashPassword returns a self-describing hash of password.
	HashPassword(password string) (string, error)
	// VerifyPassword reports whether password matches hash; a mismatch returns
	// a non-nil error. Implementations must compare in constant time.
	VerifyPassword(hash, password string) error
}

// Repositories is the set of outbound ports the feature needs. A store adapter
// (e.g. features/authentication/stores/turso) or a host fills it; the feature stays
// dialect-blind. Passwords is split from Users on purpose — credential material
// is stored and access-controlled independently of general user reads.
type Repositories struct {
	Users              user.UserRepository
	Passwords          user.PasswordRepository
	Sessions           session.SessionRepository
	VerificationCodes  verification.CodeRepository
	VerificationTokens verification.TokenRepository
	// OAuthAccounts and OAuthStates back the OAuth flow (design §3). They may be
	// nil when Config.Providers is empty (OAuth off); wiring providers without
	// them is ErrOAuthReposRequired at construction.
	OAuthAccounts oauthaccount.OAuthAccountRepository
	OAuthStates   oauthstate.StateRepository
	// ServiceAccounts and APIKeys back machine identity (design §4.1). They are
	// both-or-neither: both nil → the subsystem is off (routes not registered,
	// the bearer API-key path inert); one without the other →
	// ErrMachineReposRequired at construction.
	ServiceAccounts serviceaccount.ServiceAccountRepository
	APIKeys         apikey.APIKeyRepository
	// SecurityEvents backs the append-only audit rail (design §5.1). It is
	// OPTIONAL (ratified AV9), independently of every other port: nil → the
	// feature keeps NO audit trail (the synchronous recording site is a no-op),
	// and no construction error is raised. When wired, every sensitive op records
	// a security event synchronously and a write failure is logged at WARN,
	// never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository
	// Invitations backs the resource-invitation flow (design §6). It may be nil
	// when Config.Granter is nil (invitations off); wiring a Granter without it is
	// ErrInvitationRepoRequired at construction.
	Invitations invitation.InvitationRepository
}

// CookieConfig is the session-cookie policy. Zero values are safe: an empty Name
// defaults to "session" and an empty Path to "/". MaxAge is in seconds and also
// sets the session lifetime; a non-positive MaxAge yields a browser session
// cookie backed by a 7-day server session. Secure/Domain are host deployment
// choices (Secure should be true behind TLS). Cookies are always HttpOnly with
// SameSite=Lax.
type CookieConfig struct {
	Name   string
	Path   string
	Domain string
	Secure bool
	MaxAge int
}

// Config carries host-provided collaborators. The Hasher and Mailer are
// REQUIRED (nil → ErrHasherRequired / ErrMailerRequired at construction);
// everything else is optional with a safe default.
type Config struct {
	// Hasher is REQUIRED; nil → ErrHasherRequired.
	Hasher PasswordHasher
	// Mailer is REQUIRED; nil → ErrMailerRequired. Delivers verification and
	// password-reset messages.
	Mailer email.Sender
	// MailFrom is the From address on verification/reset mail.
	MailFrom string
	// RateLimiter throttles login attempts; nil → ratelimiter.NewMemory()
	// (safe-by-default: an in-process limiter, not "unlimited").
	RateLimiter ratelimiter.Limiter
	// SessionCookie configures the session cookie; the zero value is usable.
	SessionCookie CookieConfig
	// RequireVerifiedEmail, when true, makes login refuse an unverified user
	// with a 403 (ErrEmailNotVerified). Default false (design §7.1, AV8):
	// flipping it on requires a working Mailer so users can verify.
	RequireVerifiedEmail bool

	// IDs is the app's entity-ID strategy, decided once at wiring (amended D9):
	// it mints the keys of users, service accounts, API-key records, security
	// events, and invitations. The zero value generates default nanoids;
	// cryptids.Database delegates key generation to the database (the bundled
	// stores omit the id column and read it back with RETURNING); an
	// integration's GenerateFunc (e.g. google-uuid) chooses another shape. It
	// never mints SECRETS — session tokens, verification codes/reset tokens,
	// OAuth state, API-key material, and invitation secrets keep their own
	// unconditional high-entropy generator regardless of this strategy.
	IDs cryptids.IDGenerator

	// ListStrategy is the DEFAULT pagination strategy the feature's JSON list
	// endpoints (service accounts, API keys, invitations) apply when a request
	// names neither a cursor nor an offset param (sdk/crud ParseListRequest).
	// "cursor" (the default) or "offset"; empty is treated as "cursor". A host
	// populates it from an env-tagged config field
	// (`env:"AUTH_LIST_STRATEGY" default:"cursor"` via sdk/config ParseEnvTags),
	// never from os.Getenv inside the feature. Any other value is
	// ErrInvalidListStrategy at construction (the loud-Config posture).
	ListStrategy string `env:"AUTH_LIST_STRATEGY" default:"cursor"`

	// Providers are the wired OAuth/OIDC providers (integrations/oauth/* satisfy
	// oauth.Provider). Empty/nil → the OAuth subsystem is OFF and its routes are
	// NOT registered (deny-by-absence); Repositories.OAuthAccounts/OAuthStates
	// may then be nil. Non-empty → both oauth repositories are required
	// (ErrOAuthReposRequired).
	Providers []oauth.Provider
	// TokenEncrypter encrypts provider access/refresh tokens at rest. Nil →
	// provider tokens are NOT persisted (login and linking still work; there is
	// no offline provider-API access) — a safe, documented silent degradation.
	// Wire cryptids.AESGCM to store them.
	TokenEncrypter cryptids.Encrypter
	// OAuthCallbackBase is the absolute origin (e.g. "https://app.example.com")
	// the provider callback URL is built from. Only meaningful when Providers is
	// set.
	OAuthCallbackBase string
	// RedirectAllowlist is the exact-match allowlist of post-flow redirect
	// destinations (open-redirect guard). The same-origin default ("/") is always
	// allowed; any other requested target must appear verbatim here or it falls
	// back to "/".
	RedirectAllowlist []string

	// TokenSigner enables stateless bearer-JWT mode (design §4.4, AV6). Nil → the
	// mode is OFF: bearer JWTs are NEVER parsed and POST /auth/token is not
	// registered (deny-by-absence). When wired (integrations/cryptids/golang-jwt
	// satisfies it structurally), POST /auth/token issues short-TTL user tokens
	// and RequireUser/RequirePrincipal accept an Authorization: Bearer <jwt>,
	// resolving it to the same user identity the session path produces.
	//
	// The revocation asymmetry (a JWT outlives a password change until expiry) is
	// documented on the Config field — short TTL is the mitigation, mirroring the
	// events design's MaxConnAge posture. There are NO refresh tokens (AV6):
	// sessions remain the revocable long-lived identity; JWTs are short-lived API
	// conveniences, and machine clients authenticate via API keys, not JWTs.
	TokenSigner cryptids.JWTSigner
	// TokenTTL is the lifetime of a bearer JWT minted by POST /auth/token. Zero →
	// 1h (design §4.4). Keep it short: it bounds the revocation-asymmetry window
	// documented on TokenSigner above. Meaningful only when TokenSigner is wired.
	TokenTTL time.Duration

	// Granter is the ReBAC-decoupled grant-on-accept seam for invitations (design
	// §6, ratified AV4). Nil → the invitation subsystem is OFF and its routes are
	// NOT registered (deny-by-absence); Repositories.Invitations may then be nil.
	// Non-nil → Repositories.Invitations is required (ErrInvitationRepoRequired),
	// and Register/verify resolve pending auto-accept invitations for the invitee.
	Granter Granter
	// MemberCheck is the optional duplicate-membership predicate for the direct-add
	// path (known invitee + AutoAccept). Nil → no dup check (idempotent grants
	// absorb duplicates). Meaningful only when Granter is wired.
	MemberCheck MemberCheck
	// Notifiers is the host's wired delivery set for invitation delivery (ruling
	// 6). The email kind is always deliverable via the required Mailer with zero
	// entries here; each additional wired kind (identity.KindPhone, "slack", …)
	// enables invitations of that kind (deny-by-absence — an unwired kind is
	// ErrKindNotSupported at Create). A wired email-kind notifier also routes
	// invitation mail through notify instead of the Mailer directly
	// (verification/reset mail stays on the Mailer). Duplicate kinds →
	// ErrDuplicateNotifierKind at construction. Meaningful only when Granter is
	// wired; sdk/notify ships Console (any kind) and MailerBridge (email).
	Notifiers []notify.Notifier

	// Logger receives the best-effort WARN line when a security-event audit write
	// fails (design §5.1 — audit-write failures never fail the auth flow). Nil →
	// slog.Default(); Register defaults it to the Mount's logger when unset.
	Logger *slog.Logger
}

// Service is the auth feature's driving surface — every use-case as a method
// (session lifecycle, passwords, OAuth, machine identity, tokens, invitations),
// plus the cross-feature identity seams (RequireUser middleware, CurrentUser
// port) a host wires into another feature. It holds no mutable state beyond the
// shared Repositories/Config values. The shipped HTTP layer is an optional
// adapter over exactly this surface (FS2): a host may mount it (Register), mount
// part of it (subsystem deny-by-absence), or skip it and call these methods from
// its own handlers.
type Service struct {
	svc *authsvc.Service
	// inv is the invitation service, nil when Config.Granter is unset. Register
	// mounts its routes and NewService injects it into authsvc as the
	// resolve-on-registration collaborator; both use this single instance.
	inv *invitationsvc.Service
	// listStrategy is the resolved Config.ListStrategy the shipped HTTP adapter
	// passes as the transport-edge DefaultStrategy (Register → Mount → handlers).
	listStrategy crud.Strategy
}

// resolveListStrategy validates Config.ListStrategy and maps it to a
// crud.Strategy. Empty (the zero value of a literally-built Config) resolves to
// the cursor default; "cursor"/"offset" pass through; anything else is
// ErrInvalidListStrategy.
func resolveListStrategy(s string) (crud.Strategy, error) {
	switch s {
	case "", string(crud.StrategyCursor):
		return crud.StrategyCursor, nil
	case string(crud.StrategyOffset):
		return crud.StrategyOffset, nil
	default:
		return "", ErrInvalidListStrategy
	}
}

// NewService builds the auth Service, validating the required Config fields and
// defaulting a nil RateLimiter to an in-memory one. It does not mount HTTP
// routes (see Register for that).
func NewService(repos Repositories, cfg Config) (*Service, error) {
	if cfg.Hasher == nil {
		return nil, ErrHasherRequired
	}
	if cfg.Mailer == nil {
		return nil, ErrMailerRequired
	}
	if len(cfg.Providers) > 0 && (repos.OAuthAccounts == nil || repos.OAuthStates == nil) {
		return nil, ErrOAuthReposRequired
	}
	if (repos.ServiceAccounts == nil) != (repos.APIKeys == nil) {
		return nil, ErrMachineReposRequired
	}
	if cfg.Granter != nil && repos.Invitations == nil {
		return nil, ErrInvitationRepoRequired
	}
	listStrategy, err := resolveListStrategy(cfg.ListStrategy)
	if err != nil {
		return nil, err
	}
	// Build the notifier lookup keyed by kind, rejecting duplicate kinds LOUDLY
	// (the ErrOAuthReposRequired posture — NOT the OAuth provider map's silent
	// last-wins). A duplicate delivery route is a wiring bug, never a silent pick.
	notifiers := make(map[string]notify.Notifier, len(cfg.Notifiers))
	for _, n := range cfg.Notifiers {
		kind := n.Kind()
		if _, dup := notifiers[kind]; dup {
			return nil, ErrDuplicateNotifierKind
		}
		notifiers[kind] = n
	}
	limiter := cfg.RateLimiter
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}

	// The invitation service is built only when a Granter is wired (deny-by-
	// absence). Its Granter is injected HERE, never into authsvc (design §6 pin).
	var invSvc *invitationsvc.Service
	if cfg.Granter != nil {
		invSvc = invitationsvc.New(invitationsvc.Deps{
			Invitations:    repos.Invitations,
			Granter:        cfg.Granter,
			MemberCheck:    cfg.MemberCheck,
			UserLookup:     userLookup(repos.Users),
			Mailer:         cfg.Mailer,
			MailFrom:       cfg.MailFrom,
			Redirects:      redirect.New(cfg.RedirectAllowlist),
			SecurityEvents: repos.SecurityEvents,
			Logger:         cfg.Logger,
			IDs:            cfg.IDs,
			Notifiers:      notifiers,
		})
	}

	deps := authsvc.Deps{
		Users:     repos.Users,
		Passwords: repos.Passwords,
		Sessions:  repos.Sessions,
		Codes:     repos.VerificationCodes,
		Tokens:    repos.VerificationTokens,
		Hasher:    cfg.Hasher,
		Mailer:    cfg.Mailer,
		MailFrom:  cfg.MailFrom,
		Limiter:   limiter,
		Cookie: authsvc.CookieConfig{
			Name:   cfg.SessionCookie.Name,
			Path:   cfg.SessionCookie.Path,
			Domain: cfg.SessionCookie.Domain,
			Secure: cfg.SessionCookie.Secure,
			MaxAge: cfg.SessionCookie.MaxAge,
		},
		RequireVerifiedEmail: cfg.RequireVerifiedEmail,
		OAuthAccounts:        repos.OAuthAccounts,
		OAuthStates:          repos.OAuthStates,
		Providers:            cfg.Providers,
		TokenEncrypter:       cfg.TokenEncrypter,
		OAuthCallbackBase:    cfg.OAuthCallbackBase,
		RedirectAllowlist:    cfg.RedirectAllowlist,
		ServiceAccounts:      repos.ServiceAccounts,
		APIKeys:              repos.APIKeys,
		SecurityEvents:       repos.SecurityEvents,
		TokenSigner:          cfg.TokenSigner,
		TokenTTL:             cfg.TokenTTL,
		Logger:               cfg.Logger,
		IDs:                  cfg.IDs,
	}
	// Set the resolve-on-registration collaborator only when built, so the
	// authsvc field stays a genuine nil interface when invitations are off.
	if invSvc != nil {
		deps.Invitations = invSvc
	}
	return &Service{svc: authsvc.NewService(deps), inv: invSvc, listStrategy: listStrategy}, nil
}

// userLookup builds the internal email→subject resolver invitationsvc uses for
// the direct-add path, backed by the Users repository. It is wired here (package
// auth has the repos) so invitationsvc stays decoupled from the user store; an
// invalid or unknown email resolves to no user (found=false), never an error.
func userLookup(users user.UserRepository) invitationsvc.UserLookup {
	return func(ctx context.Context, emailAddr string) (string, bool, error) {
		normalized, err := user.NormalizeEmail(emailAddr)
		if err != nil {
			return "", false, nil
		}
		u, err := users.GetByEmail(ctx, normalized)
		if err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				return "", false, nil
			}
			return "", false, err
		}
		return u.ID, true, nil
	}
}

// RequireUser is HTTP middleware gating a route on a valid session. It satisfies
// sdk/web.Middleware via the method value authSvc.RequireUser, so a host passes
// it to another feature (e.g. cms.Config.AdminMiddleware) without either feature
// importing the other.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return s.svc.RequireUser(next)
}

// CurrentUser returns the authenticated user id on ctx, if any. It structurally
// satisfies a consuming feature's identity port (features/README.md §5's
// CurrentUser) with zero import in either direction.
func (s *Service) CurrentUser(ctx context.Context) (userID string, ok bool) {
	return s.svc.CurrentUser(ctx)
}

// RequireServiceAccount is HTTP middleware gating a route on an API-key bearer
// credential (design §4.3). A host wires it like RequireUser; it stashes the
// resolved Principal, read via CurrentPrincipal.
func (s *Service) RequireServiceAccount(next http.Handler) http.Handler {
	return s.svc.RequireServiceAccount(next)
}

// RequirePrincipal is HTTP middleware gating a route on either credential class
// (session or API-key bearer, plus a bearer JWT when Config.TokenSigner is
// wired). It stashes the resolved Principal, read via CurrentPrincipal.
func (s *Service) RequirePrincipal(next http.Handler) http.Handler {
	return s.svc.RequirePrincipal(next)
}

// AuthenticateAPIKey resolves the effective Principal for a raw API key (design
// §4.1): a personal act-as-user key yields Principal{Type: "user"}, otherwise
// Principal{Type: "service_account"}. Revoked, expired, or unknown keys return a
// generic sdk.ErrUnauthorized.
func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey string) (Principal, error) {
	return s.svc.AuthenticateAPIKey(ctx, rawKey)
}

// CurrentPrincipal returns the effective Principal stashed by
// RequireServiceAccount / RequirePrincipal, if any — the machine-or-human
// identity port a consuming feature reads alongside CurrentUser.
func (s *Service) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return s.svc.CurrentPrincipal(ctx)
}

// resolverAssertion is the compile-time proof that the auth feature satisfies the
// generic sdk/identity Resolver port: a host wires this Service anywhere a
// Resolver is expected, unadapted.
var _ identity.Resolver = (*Service)(nil)

// Resolve implements identity.Resolver: it turns a Principal into its display and
// contact Info. A user principal resolves to its DisplayName (else the email
// local part) with the email carried as an identity.KindEmail address; a
// service-account principal resolves to its Name. An unknown principal type, a
// missing record, or an off machine subsystem (nil ServiceAccounts) returns an
// error satisfying sdk.ErrNotFound — fail-closed, nil-guarded, never a panic.
func (s *Service) Resolve(ctx context.Context, p identity.Principal) (identity.Info, error) {
	return s.svc.Resolve(ctx, p)
}

// RegisterUser creates an account and dispatches the email-verification code.
func (s *Service) RegisterUser(ctx context.Context, email, password, displayName string) (user.User, error) {
	return s.svc.Register(ctx, email, password, displayName)
}

// Login verifies credentials and returns the plaintext session cookie token to set.
func (s *Service) Login(ctx context.Context, email, password string) (token string, u user.User, err error) {
	return s.svc.Login(ctx, email, password)
}

// Logout revokes the session backing the cookie token (idempotent).
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.svc.Logout(ctx, token)
}

// Verify redeems an email-verification code, marking the user verified.
func (s *Service) Verify(ctx context.Context, code string) error {
	return s.svc.Verify(ctx, code)
}

// ChangePassword verifies the current password, sets the new one, revokes all sessions, and returns a fresh cookie token.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (token string, err error) {
	return s.svc.ChangePassword(ctx, userID, currentPassword, newPassword)
}

// ForgotPassword mails a reset token; an unknown email is a silent no-op.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	return s.svc.ForgotPassword(ctx, email)
}

// ResetPassword redeems a reset token and sets the new password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	return s.svc.ResetPassword(ctx, token, newPassword)
}

// StartOAuth returns the provider authorization URL for an unauthenticated login/register flow.
func (s *Service) StartOAuth(ctx context.Context, provider, redirectTo string) (authURL string, err error) {
	return s.svc.StartOAuth(ctx, provider, redirectTo)
}

// StartLink returns the provider authorization URL for linking a provider to the signed-in user.
func (s *Service) StartLink(ctx context.Context, userID, provider, redirectTo string) (authURL string, err error) {
	return s.svc.StartLink(ctx, userID, provider, redirectTo)
}

// OAuthCallback processes a provider callback (code + state) into an OAuthResult.
func (s *Service) OAuthCallback(ctx context.Context, provider, code, state string) (OAuthResult, error) {
	return s.svc.OAuthCallback(ctx, provider, code, state)
}

// VerifyLink completes a pending account link from its emailed token.
func (s *Service) VerifyLink(ctx context.Context, token string) (OAuthResult, error) {
	return s.svc.VerifyLink(ctx, token)
}

// ListLinked returns the user's linked provider accounts.
func (s *Service) ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	return s.svc.ListLinked(ctx, userID)
}

// Unlink removes a provider link, refusing if it is the account's last auth method.
func (s *Service) Unlink(ctx context.Context, userID, provider string) error {
	return s.svc.Unlink(ctx, userID, provider)
}

// CreateServiceAccount creates a machine identity, optionally acting as ownerUserID.
func (s *Service) CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error) {
	return s.svc.CreateServiceAccount(ctx, createdBy, name, description, actAsUser, ownerUserID)
}

// ListServiceAccounts pages the service accounts.
func (s *Service) ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return s.svc.ListServiceAccounts(ctx, req)
}

// MintAPIKey issues a key for a service account, returning the record and the one-time plaintext key.
func (s *Service) MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error) {
	return s.svc.MintAPIKey(ctx, serviceAccountID, name, expiresAt)
}

// ListAPIKeys pages a service account's API keys.
func (s *Service) ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return s.svc.ListAPIKeys(ctx, serviceAccountID, req)
}

// RevokeAPIKey revokes an API key by id (idempotent).
func (s *Service) RevokeAPIKey(ctx context.Context, keyID string) error {
	return s.svc.RevokeAPIKey(ctx, keyID)
}

// IssueToken mints a short-TTL bearer JWT for login-shaped credentials, returning the token and its expiry.
func (s *Service) IssueToken(ctx context.Context, email, password string) (token string, expiresAt time.Time, err error) {
	return s.svc.IssueToken(ctx, email, password)
}

// Create invites an identifier to a resource; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if s.inv == nil {
		return CreateResult{}, ErrInvitationsDisabled
	}
	return s.inv.Create(ctx, in)
}

// ListByResource pages a resource's invitations; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if s.inv == nil {
		return crud.Page[invitation.Invitation]{}, ErrInvitationsDisabled
	}
	return s.inv.ListByResource(ctx, resourceType, resourceID, req)
}

// Mine pages the caller's own invitations by identifier; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Mine(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if s.inv == nil {
		return crud.Page[invitation.Invitation]{}, ErrInvitationsDisabled
	}
	return s.inv.Mine(ctx, identifier, req)
}

// Accept redeems an invitation token for the calling subject; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Accept(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	if s.inv == nil {
		return AcceptResult{}, ErrInvitationsDisabled
	}
	return s.inv.Accept(ctx, in)
}

// Decline declines a pending invitation by id + token; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Decline(ctx context.Context, id, token string) error {
	if s.inv == nil {
		return ErrInvitationsDisabled
	}
	return s.inv.Decline(ctx, id, token)
}

// Cancel cancels a pending invitation the caller owns; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Cancel(ctx context.Context, id, currentUserID string) error {
	if s.inv == nil {
		return ErrInvitationsDisabled
	}
	return s.inv.Cancel(ctx, id, currentUserID)
}

// Resend regenerates and re-mails a pending invitation the caller owns; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Resend(ctx context.Context, id, currentUserID, redirectTo string) (invitation.Invitation, error) {
	if s.inv == nil {
		return invitation.Invitation{}, ErrInvitationsDisabled
	}
	return s.inv.Resend(ctx, id, currentUserID, redirectTo)
}

// SetSessionCookie writes the session cookie carrying token, per the configured policy.
func (s *Service) SetSessionCookie(w http.ResponseWriter, token string) {
	s.svc.SetSessionCookie(w, token)
}

// ClearSessionCookie expires the session cookie.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	s.svc.ClearSessionCookie(w)
}

// SessionCookieName returns the configured session cookie name.
func (s *Service) SessionCookieName() string {
	return s.svc.SessionCookieName()
}

// RateLimitByIP returns middleware throttling a public route on the client IP.
func (s *Service) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	return s.svc.RateLimitByIP(keyPrefix, perMinute)
}

// Register mounts the auth feature's shipped HTTP adapter — the /auth/* route
// surface — onto the host's Mount, over this already-built Service (FS2: build
// once via NewService, mount once). It is the optional convenience adapter over
// the Service's use-case methods: subsystems the Service was built without
// register no routes (deny-by-absence), and a host may skip Register entirely
// and drive the methods from its own handlers. Migrations are registered by the
// store adapter (features/authentication/stores/turso), not here — the core is
// dialect-blind. The audit rail's best-effort WARN sink (Config.Logger) is
// captured at NewService time; set it there — it defaults to slog.Default() when
// unset, no longer to the Mount logger, since the Service is built before Mount.
func (s *Service) Register(m feature.Mount) error {
	// Pass the invitation service as a GENUINE nil interface when it is off, so
	// Mount's deny-by-absence check (design §6) is not fooled by a typed nil.
	var inv inbound.InvitationService
	if s.inv != nil {
		inv = s.inv
	}
	inbound.Mount(m.Router, s.svc, inv, s.listStrategy)
	return nil
}
