// Package auth is the public surface of the auth feature module: the
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
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	internalhttp "github.com/gopernicus/gopernicus/features/auth/internal/inbound/http"
	"github.com/gopernicus/gopernicus/features/auth/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/auth/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/features/auth/internal/redirect"
	"github.com/gopernicus/gopernicus/features/auth/logic/apikey"
	"github.com/gopernicus/gopernicus/features/auth/logic/invitation"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/auth/logic/securityevent"
	"github.com/gopernicus/gopernicus/features/auth/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
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

// ErrOAuthLastMethod is returned (as the wrapped cause) by Service.Unlink when the
// target link is the user's only authentication method and no password is set —
// unlinking it would lock the account out. It wraps errs.ErrConflict, so the
// transport maps it to 409. Hosts detect it with errors.Is(err,
// auth.ErrOAuthLastMethod).
var ErrOAuthLastMethod = authsvc.ErrLastAuthMethod

// ErrEmailNotVerified is returned (as the wrapped cause) by login when
// Config.RequireVerifiedEmail is set and the caller's email is unverified. It
// wraps errs.ErrForbidden, so the transport maps it to 403. Hosts detect it with
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
// (e.g. features/auth/stores/turso) or a host fills it; the feature stays
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

	// Logger receives the best-effort WARN line when a security-event audit write
	// fails (design §5.1 — audit-write failures never fail the auth flow). Nil →
	// slog.Default(); Register defaults it to the Mount's logger when unset.
	Logger *slog.Logger
}

// Service is the auth feature's identity capability without HTTP routes — the
// surface a host wires into another feature (its RequireUser middleware, its
// CurrentUser port). It holds no mutable state beyond the shared Repositories/
// Config values.
type Service struct {
	svc *authsvc.Service
	// inv is the invitation service, nil when Config.Granter is unset. Register
	// mounts its routes and NewService injects it into authsvc as the
	// resolve-on-registration collaborator; both use this single instance.
	inv *invitationsvc.Service
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
	}
	// Set the resolve-on-registration collaborator only when built, so the
	// authsvc field stays a genuine nil interface when invitations are off.
	if invSvc != nil {
		deps.Invitations = invSvc
	}
	return &Service{svc: authsvc.NewService(deps), inv: invSvc}, nil
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
			if errors.Is(err, errs.ErrNotFound) {
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
// generic errs.ErrUnauthorized.
func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey string) (Principal, error) {
	return s.svc.AuthenticateAPIKey(ctx, rawKey)
}

// CurrentPrincipal returns the effective Principal stashed by
// RequireServiceAccount / RequirePrincipal, if any — the machine-or-human
// identity port a consuming feature reads alongside CurrentUser.
func (s *Service) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return s.svc.CurrentPrincipal(ctx)
}

// Register wires the auth feature's own HTTP routes onto the host's mount. It
// builds a Service internally (validating the required Config fields) and mounts
// the route table. A host that also needs cross-feature identity builds a second
// Service via NewService — the two point at the same Repositories/Config and
// hold no independent state, an accepted, documented duplication (see the
// auth-feature-design doc, §3). Migrations are registered by the store adapter
// (features/auth/stores/turso), not here — the core is dialect-blind.
func Register(m feature.Mount, repos Repositories, cfg Config) error {
	// The audit rail's best-effort WARN line rides the host's Mount logger unless
	// the host set an explicit Config.Logger (design §5.1).
	if cfg.Logger == nil {
		cfg.Logger = m.Logger
	}
	svc, err := NewService(repos, cfg)
	if err != nil {
		return err
	}
	// Pass the invitation service as a GENUINE nil interface when it is off, so
	// Mount's deny-by-absence check (design §6) is not fooled by a typed nil.
	var inv internalhttp.InvitationService
	if svc.inv != nil {
		inv = svc.inv
	}
	internalhttp.Mount(m.Router, svc.svc, inv)
	return nil
}
