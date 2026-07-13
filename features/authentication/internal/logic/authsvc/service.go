// Package authsvc holds the auth feature's domain services: registration, email
// verification, login (rate-limited), logout, password change, password
// forgot/reset, and session validation — business rules over the repository
// ports, with no SQL. It is internal so it is not part of the feature's public
// SemVer surface; the host-facing surface is package auth (auth.go).
//
// Secret hygiene: passwords are only ever compared through the Hasher (bcrypt's
// compare is constant-time by construction); verification codes and reset
// tokens are opaque values matched by keyed lookup, so no secret is branched on
// byte-by-byte here. Secrets are never logged, and forgot-password never
// reveals whether an email is registered.
package authsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

const (
	// passwordResetTTL is how long a password-reset token stays valid.
	passwordResetTTL = time.Hour
	// minPasswordCodePoints is the minimum length of a single-factor password in
	// Unicode code points (design §5.9): the v3 floor replaces the old eight-byte
	// minimum. Length is the only strength rule — no arbitrary composition or
	// periodic-rotation requirements are imposed.
	minPasswordCodePoints = 15
	// maxPasswordCodePoints is the maximum accepted password length in Unicode
	// code points (design §5.9: "at least 64"). A generous ceiling so passphrases
	// are welcome; it exists only to bound work, not to weaken long secrets.
	maxPasswordCodePoints = 64
	// maxPasswordInputBytes is the finite pre-hash input cap (design §5.9: inputs
	// are length-bounded before expensive hashing). A 64-code-point password is at
	// most 256 UTF-8 bytes, so this rejects only pathological over-long input
	// before it reaches the counter or the hasher; over-cap input is REJECTED, never
	// silently truncated (the bcrypt integration also errors past its 72-byte limit
	// rather than truncating — the no-silent-truncation contract holds end to end).
	maxPasswordInputBytes = 256
	// refreshCookiePath scopes the refresh cookie to /auth (D4): it covers
	// /auth/refresh AND /auth/logout, never riding on unrelated requests.
	refreshCookiePath = "/auth"
	// loginAttemptsPerMinute caps failed+successful login attempts per
	// (email, client-IP) window before Login refuses with ErrRateLimited.
	loginAttemptsPerMinute = 5
	// defaultAccessTokenTTL is the access-JWT lifetime when Config.AccessTokenTTL
	// is unset (§1.1, D8). Kept short: it bounds the revocation-asymmetry window
	// on stateless routes (a deleted session still honors an outstanding access
	// JWT for ≤ this).
	defaultAccessTokenTTL = 15 * time.Minute
	// defaultRefreshTTL is the refresh-token / session horizon when
	// Config.RefreshTTL is unset (§1.1, D8). Fixed at mint; rotation never
	// extends it (D2).
	defaultRefreshTTL = 7 * 24 * time.Hour
	// refreshAttemptsPerMinute caps per-session refresh attempts (the by-session
	// arm of the §6 refresh rate limit); the by-IP arm is a route middleware.
	refreshAttemptsPerMinute = 30
)

// TokenPair is the credential pair a session mint produces (§1.1): a
// self-validating access JWT and its absolute expiry, plus the opaque refresh
// token. RefreshToken is empty on the grace lane (§1.3 branch 4), where only a
// new access token is issued and the client keeps its existing refresh token.
type TokenPair struct {
	AccessToken     string
	AccessExpiresAt time.Time
	RefreshToken    string
}

// ErrRateLimited is returned by Login when the per-(email, IP) attempt budget is
// exhausted. It is deliberately distinct from the invalid-credentials error
// (sdk.ErrUnauthorized) so the transport can map it to 429 and clients can back
// off. Checked with errors.Is.
var ErrRateLimited = errors.New("too many login attempts")

// ErrEmailNotVerified is returned by Login when Config.RequireVerifiedEmail is
// set and the caller's email is unverified. It wraps sdk.ErrForbidden so the
// transport maps it to 403 (design §7.1). The public auth package re-exports it
// as auth.ErrEmailNotVerified. Checked with errors.Is.
var ErrEmailNotVerified = fmt.Errorf("email not verified: %w", sdk.ErrForbidden)

// ErrRegistrationVerificationConflict is returned by Verify when the
// verify_registration code was consumed but the atomic identifier apply lost a
// revision-CAS (design §5.6): the code is spent, but no partial state was written,
// so the flow is safely restartable — the caller reissues a registration code and
// tries again. It wraps sdk.ErrConflict so the transport maps it to 409. Checked
// with errors.Is.
var ErrRegistrationVerificationConflict = fmt.Errorf("registration verification could not be applied, please request a new code: %w", sdk.ErrConflict)

// ErrPasswordResetInvalid is the single generic failure ResetPassword returns for
// an unknown, expired, or already-used reset token (design §5.8 enumeration/anti-
// probing): every non-live token collapses to one error so a response can never
// distinguish "no such token" from "expired" from "already used", and the secret
// is never named. It wraps sdk.ErrNotFound so the transport maps it to 404,
// preserving the existing external reset contract. Checked with errors.Is.
var ErrPasswordResetInvalid = fmt.Errorf("password reset token is invalid or expired: %w", sdk.ErrNotFound)

// ErrPasswordCompromised is returned by the password policy when a wired
// CompromisedPasswordChecker reports the candidate as breached/blocklisted
// (design §5.9). It wraps sdk.ErrInvalidInput so the transport maps it to 400 and
// the caller is asked to choose a different password. Checked with errors.Is. A
// checker that cannot COMPLETE (an infrastructure error) is governed separately by
// the fail-closed/fail-open policy, not this error.
var ErrPasswordCompromised = fmt.Errorf("password is known to be compromised: %w", sdk.ErrInvalidInput)

// passwordResetPurgePurposes are the outstanding challenge purposes a successful
// reset purges for the user in the same transaction (design §5.9): the just-
// consumed reset row (idempotent) plus any live remove-password challenge, so a
// pending credential-mutation flow cannot survive a reset.
var passwordResetPurgePurposes = []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword}

// Hasher hashes and verifies passwords. auth.PasswordHasher satisfies it
// structurally; it is declared here rather than imported from the auth package
// so the internal service carries no import cycle with its own host-facing
// package. Accept interfaces, return structs.
type Hasher interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// compromisedChecker reports whether a candidate password is known-compromised
// (present in a breach corpus or a host blocklist, design §5.9). It is the
// internal structural twin of auth.CompromisedPasswordChecker, declared here so
// the service carries no import cycle with its host-facing package. It is
// OPTIONAL — nil disables the breach check — and the feature core ships none, so
// the core adds no network dependency; a local blocklist or a future remote
// breach-check adapter both satisfy it.
type compromisedChecker interface {
	// IsCompromised reports whether password is known-compromised. A non-nil error
	// means the check could not complete (e.g. an unreachable remote corpus); the
	// service's fail-closed/fail-open policy then decides the outcome.
	IsCompromised(ctx context.Context, password string) (bool, error)
}

// invitationResolver is the SINGLE narrow port authsvc holds on the sibling
// invitationsvc (design §6 pin): the register/verify flow calls it to grant a
// just-registered/verified email's pending auto-accept invitations. It is
// declared here (structural) so authsvc carries NO import edge to invitationsvc
// and never holds the Granter. Nil → invitations are off (a no-op).
type invitationResolver interface {
	ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error)
}

// CookieConfig is the resolved session-cookie policy. The auth package fills it
// from auth.Config.SessionCookie (applying name/path defaults).
type CookieConfig struct {
	Name   string
	Path   string
	Domain string
	Secure bool
	MaxAge int // seconds; also the session lifetime when > 0
}

// Deps are the collaborators the Service needs. The auth package builds this
// after validating the required fields and applying defaults.
type Deps struct {
	Users user.UserRepository
	// Identifiers backs the v3 identity-discovery rail (design §2.2). Registration
	// creates an unverified primary email identifier atomically with the user
	// (CreateWithPrimaryIdentifier); login/token resolve identity through GetLogin;
	// Verify claims/verifies it through the atomic revision-CAS ApplyVerifiedChange.
	// Wired whenever the challenge-backed register/verify flow is active (the
	// register/verify/login path assumes it is non-nil, like Users/Passwords).
	Identifiers identifier.IdentifierRepository
	// Normalizer canonicalizes identifier values for persistence, lookup, and
	// rate-limit keys (design §2.2): one injected policy so a stored identifier and
	// a login/verify/recovery lookup speak the same normalized string. Nil selects
	// the bundled strict identifier.DefaultNormalizer.
	Normalizer identifier.Normalizer
	Passwords  user.PasswordRepository
	Sessions   session.SessionRepository
	// Challenges backs the atomic secret rail (design §3.2): HMAC-protected OTP
	// codes and SHA-256 magic-link tokens with atomic replace/consume. Nil until a
	// host wires the challenge subsystem; the challenge service methods refuse
	// while it is nil (fail closed). Protector is required alongside it.
	Challenges challenge.Repository
	// Protector protects short codes with a keyed HMAC and digests tokens
	// (design §3.3). Required whenever Challenges is wired.
	Protector challengeProtector
	// PasswordResets backs the atomic password-reset composition (design §5.9):
	// redeem the reset challenge, set the password, and revoke all sessions/grants
	// in one transaction. Wired whenever the challenge-backed forgot/reset flow is
	// active; ResetPassword refuses while it is nil (fail closed).
	PasswordResets passwordreset.Repository
	// ContactChanges backs the pending-value flow state of an identifier add/change
	// (design §2.4): an atomic replace-per-(user, kind) PendingChange holding the new
	// normalized value and requested uses between a change flow's start and confirm.
	// REQUIRED once the identifier-management flows are wired (phase 6); the start/
	// confirm methods fail closed (ErrIdentifierChangeUnavailable) while it is nil.
	ContactChanges contactchange.Repository
	// CredentialMutations backs the revision-serialized credential-mutation rail
	// (design §5.6). The OAuth adoption-revocation path (design §5.7/V5) uses it to
	// remove a squatter's password atomically before an adopting link is created; the
	// password repository exposes no delete, so the typed RemovePassword mutation is
	// the only removal seam. Nil until the credential rail is wired; the adoption path
	// fails closed while it is nil.
	CredentialMutations credential.MutationRepository
	// AuthenticationGrants backs recent-authentication / step-up grants (design
	// §5.0): the single-use, session-bound proof a sensitive mutation consumes
	// immediately before it runs. Nil until the credential suite is wired; the step-up
	// service methods refuse (fail closed) while it is nil.
	AuthenticationGrants authgrant.Repository
	// CredentialPolicy evaluates a proposed credential/identifier mutation against
	// the current and proposed MethodSet (design §5.6): the /auth/methods removable
	// hints and every sensitive mutation route it before their revision-CAS Apply.
	// Nil selects the bundled safe credential.NewDefaultPolicy default.
	CredentialPolicy credential.Policy
	Hasher           Hasher
	// Compromised is the OPTIONAL host-injected breach/blocklist checker (design
	// §5.9). Nil → no breach check. When wired, every password entry point
	// (register/set/change/reset) consults it through the single validatePassword
	// path. CompromisedFailOpen selects the policy when the checker cannot complete.
	Compromised compromisedChecker
	// CompromisedFailOpen selects the policy when a wired Compromised checker
	// returns an error (cannot complete a check). Default false = FAIL CLOSED: an
	// unavailable breach service rejects the password rather than silently becoming
	// a bypass — the required production posture (design §5.9, V15 fail-closed
	// profile). Set true only to trade breach coverage for availability (a
	// development/self-hosted convenience).
	CompromisedFailOpen bool
	Mailer              email.Sender
	MailFrom            string
	// Deliver is the shared kind-aware delivery renderer/router (design §6.1),
	// constructor-injected by package auth and shared with invitationsvc. It renders
	// an encrypted-job-ready Envelope and routes a send through the email/notify kind
	// fork; the durable worker (phase 4) consumes it. Wired whenever the Mailer is
	// (the router requires an email Sender). The request-time send sites enqueue
	// rendered/opaque commands through Queue (AV3-4.3).
	Deliver *delivery.Router
	// Queue is the durable delivery outbox (design §6.1.1) every send site enqueues
	// through instead of a request-time provider call. Wired whenever DeliveryJobs is
	// (package auth builds it); nil → outbound disabled (the send sites fail loudly).
	Queue deliveryQueue
	// IdentifierKeyer derives PII-free outbox idempotency keys (design §4.4). Nil → a
	// SHA-256 fallback keeps keys PII-free without the host keyer.
	IdentifierKeyer identifierKeyer
	Limiter         ratelimiter.Limiter
	Cookie          CookieConfig
	Clock           func() time.Time // nil → time.Now
	Logger          *slog.Logger     // nil → slog.Default(); used only for best-effort WARN lines
	// IDs is the app-chosen entity-ID strategy (amended D9): it mints the keys
	// of users, service accounts, API-key records, and security events. The
	// zero value generates default nanoids; cryptids.Database delegates to the
	// store. It NEVER mints secrets — session tokens, verification codes, API
	// key material, and PKCE/nonce values keep their own unconditional random
	// generator regardless of this strategy.
	IDs cryptids.IDGenerator
	// RequireVerifiedEmail, when true, makes Login refuse an unverified user
	// with ErrEmailNotVerified (403). Default false (design §7.1, AV8).
	RequireVerifiedEmail bool

	// SecurityEvents is the optional append-only audit rail (design §5.1,
	// ratified AV9). Nil → no audit trail: the synchronous recording site is a
	// documented no-op. When wired, every sensitive op records synchronously and
	// a write failure is logged at WARN, never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository

	// Invitations is the optional resolve-on-registration collaborator (design
	// §6). Nil when invitations are off; when wired (by package auth), Register
	// and Verify grant a just-registered/verified email's pending auto-accept
	// invitations best-effort. It is the ONE port authsvc holds on invitationsvc.
	Invitations invitationResolver

	// OAuth flow collaborators (design §3). Providers empty → the OAuth
	// subsystem is off and its routes are not registered (deny-by-absence); the
	// two oauth repositories and TokenEncrypter may then be nil. When Providers
	// is non-empty the auth package requires both oauth repositories at
	// construction (auth.ErrOAuthReposRequired).
	OAuthAccounts     oauthaccount.OAuthAccountRepository
	OAuthStates       oauthstate.StateRepository
	Providers         []oauth.Provider
	TokenEncrypter    cryptids.Encrypter // nil → provider tokens are dropped (not persisted)
	OAuthCallbackBase string
	RedirectAllowlist []string

	// Machine-identity collaborators (design §4.1). Both nil → the API-key /
	// service-account subsystem is off (routes not registered, the bearer
	// API-key path is inert). The auth package enforces both-or-neither at
	// construction (auth.ErrMachineReposRequired).
	ServiceAccounts serviceaccount.ServiceAccountRepository
	APIKeys         apikey.APIKeyRepository

	// TokenSigner mints and verifies the access JWT (§1.1). It is REQUIRED — the
	// public auth.NewService rejects a nil signer with ErrTokenSignerRequired
	// (D3); this internal Service assumes it is non-nil. AccessTokenTTL is the
	// access-JWT lifetime (≤0 → defaultAccessTokenTTL, 15m); RefreshTTL is the
	// fixed refresh/session horizon (≤0 → defaultRefreshTTL, 7d).
	TokenSigner    cryptids.JWTSigner
	AccessTokenTTL time.Duration
	RefreshTTL     time.Duration

	// Passwordless is the host-enabled passwordless kind set (design §4.2), resolved
	// and validated by package auth (auth.Config.Passwordless) before it reaches here.
	// Empty → passwordless is off and its routes are not registered (deny-by-absence);
	// a non-empty set lists the {email, phone} kinds whose active verified
	// login-enabled identifiers are permitted as direct passwordless login methods.
	Passwordless []string
	// PublicAuthBaseURL is the absolute base URL passwordless magic links are built
	// from (design §6.4): the worker composes the sign-in link from this base only,
	// never from a request Host/forwarded header. Package auth validates it (absolute
	// http(s), HTTPS in production) whenever a passwordless kind is enabled.
	PublicAuthBaseURL string
}

// Service implements the auth use cases over the repository ports.
type Service struct {
	users user.UserRepository
	// identifiers backs the v3 identity-discovery rail (design §2.2): registration
	// creates the unverified primary email identifier, login/token resolve through
	// GetLogin, and verification claims/verifies via the atomic ApplyVerifiedChange.
	identifiers identifier.IdentifierRepository
	// normalizer is the single injected identifier-value canonicalizer (design
	// §2.2); nil-defaulted to identifier.DefaultNormalizer in NewService.
	normalizer identifier.Normalizer
	passwords  user.PasswordRepository
	sessions   session.SessionRepository
	// challenges backs the atomic secret rail (design §3.2); protector protects
	// its codes/tokens (design §3.3). Both nil → the challenge service methods
	// refuse (the subsystem is off).
	challenges challenge.Repository
	protector  challengeProtector
	// passwordResets backs the atomic password-reset composition (design §5.9).
	// Nil → ResetPassword refuses (the forgot/reset rail is off, fail closed).
	passwordResets passwordreset.Repository
	// contactChanges backs the pending-value flow state of an identifier add/change
	// (design §2.4). Nil → the identifier add/change flows fail closed.
	contactChanges contactchange.Repository
	// credentialMutations backs the revision-serialized credential-mutation rail
	// (design §5.6); the OAuth adoption-revocation path removes a squatter password
	// through it (design §5.7/V5). Nil → the adoption path fails closed.
	credentialMutations credential.MutationRepository
	// authGrants backs recent-authentication / step-up grants (design §5.0). Nil →
	// the step-up service methods fail closed (ErrStepUpUnavailable).
	authGrants authgrant.Repository
	// credentialPolicy evaluates a proposed credential/identifier mutation (design
	// §5.6): the /auth/methods removable hints and the sensitive mutations consult
	// it. NewService defaults a nil policy to the bundled credential.DefaultPolicy.
	credentialPolicy credential.Policy
	hasher           Hasher
	// compromised is the optional breach/blocklist checker (design §5.9); nil →
	// no breach check. compromisedFailOpen selects the outcome when it errors.
	compromised         compromisedChecker
	compromisedFailOpen bool
	mailer              email.Sender
	mailFrom            string
	// deliver is the shared kind-aware delivery renderer/router (Deps.Deliver): send
	// sites render an envelope through it and enqueue it on queue. Nil until wired.
	deliver *delivery.Router
	// queue is the durable delivery outbox (Deps.Queue) send sites enqueue through;
	// identifierKeyer derives its PII-free idempotency keys. Both nil → outbound off.
	queue                deliveryQueue
	identifierKeyer      identifierKeyer
	limiter              ratelimiter.Limiter
	cookie               CookieConfig
	now                  func() time.Time
	logger               *slog.Logger
	requireVerifiedEmail bool
	// ids is the app-chosen entity-ID strategy (Deps.IDs); zero value → default
	// nanoids. Entity keys only, never secrets.
	ids cryptids.IDGenerator
	// securityEvents is the optional append-only audit rail (design §5.1). Nil →
	// the recordSecurityEvent helper is a no-op (ratified AV9).
	securityEvents securityevent.SecurityEventRepository
	// invitations is the optional resolve-on-registration collaborator (design
	// §6). Nil → resolvePendingInvitations is a no-op.
	invitations invitationResolver
	// tokenHasher is the fixed SHA-256 hasher for session cookie tokens. It is
	// an internal implementation detail, not a host-provided port: the stored
	// session value is always the SHA-256 hash of the cookie (design §7.3).
	tokenHasher *cryptids.SHA256Hasher

	// OAuth flow state (design §3). providers is keyed by Provider.Name();
	// oauthAccounts/oauthStates are the flow's repositories; tokenEncrypter is
	// nil when provider tokens are dropped; redirects guards post-flow
	// destinations. All are zero/empty when the subsystem is off.
	oauthAccounts  oauthaccount.OAuthAccountRepository
	oauthStates    oauthstate.StateRepository
	providers      map[string]oauth.Provider
	tokenEncrypter cryptids.Encrypter
	callbackBase   string
	redirects      redirect.Allowlist

	// Machine-identity state (design §4.1). Both nil when the subsystem is off.
	serviceAccounts serviceaccount.ServiceAccountRepository
	apiKeys         apikey.APIKeyRepository

	// Access-JWT signer and TTLs (§1.1). tokenSigner is always wired (the public
	// constructor requires it, D3). accessTTL is the access-JWT lifetime;
	// refreshTTL is the fixed refresh/session horizon (rotation never extends it).
	tokenSigner cryptids.JWTSigner
	accessTTL   time.Duration
	refreshTTL  time.Duration

	// passwordless is the resolved set of enabled passwordless kinds (Deps.Passwordless),
	// keyed by kind for O(1) lookups. Empty when passwordless is off; the transport
	// registers the passwordless routes only when PasswordlessEnabled reports true
	// (deny-by-absence, design §4.2).
	passwordless map[string]bool
	// publicBaseURL is the absolute base URL magic links are built from (Deps.PublicAuthBaseURL,
	// design §6.4). Empty unless passwordless is enabled; package auth validates it.
	publicBaseURL string
}

// NewService builds a Service from its dependencies, applying cookie name/path
// defaults and a time.Now clock when unset.
func NewService(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cookie := d.Cookie
	if cookie.Name == "" {
		cookie.Name = "session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	providers := make(map[string]oauth.Provider, len(d.Providers))
	for _, p := range d.Providers {
		providers[p.Name()] = p
	}
	passwordless := make(map[string]bool, len(d.Passwordless))
	for _, k := range d.Passwordless {
		passwordless[k] = true
	}
	accessTTL := d.AccessTokenTTL
	if accessTTL <= 0 {
		accessTTL = defaultAccessTokenTTL
	}
	refreshTTL := d.RefreshTTL
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	normalizer := d.Normalizer
	if normalizer == nil {
		normalizer = identifier.DefaultNormalizer{}
	}
	credentialPolicy := d.CredentialPolicy
	if credentialPolicy == nil {
		credentialPolicy = credential.NewDefaultPolicy(credential.PolicyConfig{})
	}
	return &Service{
		users:                d.Users,
		identifiers:          d.Identifiers,
		normalizer:           normalizer,
		passwords:            d.Passwords,
		sessions:             d.Sessions,
		challenges:           d.Challenges,
		protector:            d.Protector,
		passwordResets:       d.PasswordResets,
		contactChanges:       d.ContactChanges,
		credentialMutations:  d.CredentialMutations,
		authGrants:           d.AuthenticationGrants,
		credentialPolicy:     credentialPolicy,
		hasher:               d.Hasher,
		compromised:          d.Compromised,
		compromisedFailOpen:  d.CompromisedFailOpen,
		mailer:               d.Mailer,
		mailFrom:             d.MailFrom,
		deliver:              d.Deliver,
		queue:                d.Queue,
		identifierKeyer:      d.IdentifierKeyer,
		limiter:              d.Limiter,
		cookie:               cookie,
		now:                  clock,
		logger:               logger,
		requireVerifiedEmail: d.RequireVerifiedEmail,
		ids:                  d.IDs,
		securityEvents:       d.SecurityEvents,
		invitations:          d.Invitations,
		tokenHasher:          cryptids.NewSHA256Hasher(),
		oauthAccounts:        d.OAuthAccounts,
		oauthStates:          d.OAuthStates,
		providers:            providers,
		tokenEncrypter:       d.TokenEncrypter,
		callbackBase:         d.OAuthCallbackBase,
		redirects:            redirect.New(d.RedirectAllowlist),
		serviceAccounts:      d.ServiceAccounts,
		apiKeys:              d.APIKeys,
		tokenSigner:          d.TokenSigner,
		accessTTL:            accessTTL,
		refreshTTL:           refreshTTL,
		passwordless:         passwordless,
		publicBaseURL:        d.PublicAuthBaseURL,
	}
}

// Register creates an unverified user together with its primary email identifier
// in one atomic operation (CreateWithPrimaryIdentifier, design §2.2), stores the
// password hash, issues a verify_registration challenge code on the atomic secret
// rail (design §3.2), and mails it. The primary identifier is login-, recovery-,
// and notification-enabled but UNVERIFIED while the registration challenge is
// pending (design §2.3): identity lives in user_identifiers from account creation,
// so Verify records the proof time rather than adding the identifier. A mail
// failure still leaves an account the user can later verify (the error is returned
// so the caller knows delivery failed). A duplicate email — a lost authentication
// claim on the identifier value — surfaces as sdk.ErrAlreadyExists from the store.
func (s *Service) Register(ctx context.Context, emailAddr, password, displayName string) (user.User, error) {
	if err := s.validatePassword(ctx, password); err != nil {
		return user.User{}, err
	}
	now := s.now()
	// The primary identifier is normalized through the single injected policy so a
	// later GetLogin/GetRecovery lookup resolves the same stored value; it also
	// validates the address, failing before any user row is minted.
	ident, err := identifier.NewRegistrationEmail(s.ids, s.normalizer, "", emailAddr, now)
	if err != nil {
		return user.User{}, err
	}
	u := user.NewUser(s.ids, displayName, now)
	hash, err := s.hasher.HashPassword(password)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}

	created, createdIdent, err := s.users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		return user.User{}, err
	}
	primaryEmail := createdIdent.NormalizedValue
	if err := s.passwords.Set(ctx, created.ID, hash); err != nil {
		return user.User{}, err
	}

	code, err := s.IssueChallenge(ctx, created.ID, challenge.PurposeVerifyRegistration)
	if err != nil {
		return user.User{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: created.ID,
		Type:   securityevent.TypeRegister,
		Status: securityevent.StatusSuccess,
	})
	s.resolvePendingInvitations(ctx, primaryEmail, created.ID)
	// The account is resolved (just created), so registration verification is not
	// enumeration-sensitive: render the code on the request path and enqueue the
	// sealed message on the durable outbox. The worker delivers it; a provider outage
	// no longer blocks or fails registration beyond the enqueue itself. A failed
	// enqueue is returned (like the prior mail-failure contract) with the created
	// account, which the user can verify with a later resend.
	key := s.idempotencyKey(identity.KindEmail, primaryEmail, delivery.PurposeRegistrationVerification)
	if err := s.enqueueRendered(ctx, delivery.PurposeRegistrationVerification, key, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposeRegistrationVerification,
		Destination:     primaryEmail,
		ResolutionInput: primaryEmail,
		Secret:          code,
	}); err != nil {
		return created, err
	}
	return created, nil
}

// Verify consumes a verify_registration challenge code for the account behind
// emailAddr, then claims and verifies its primary email identifier under the
// atomic revision-CAS ApplyVerifiedChange (design §2.3, §5.9). A wrong code counts
// an attempt and eventually locks out (ErrTooManyAttempts); an expired code is
// ErrChallengeExpired; every other non-match — an unknown account included — is the
// single generic ErrChallengeInvalid, so the response cannot enumerate accounts
// (design §5.8). A post-consume apply conflict is ErrRegistrationVerificationConflict
// (409): the code is spent but no partial state was written, so the caller reissues
// a code and retries (design §5.6).
func (s *Service) Verify(ctx context.Context, emailAddr, code string) error {
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return ErrChallengeInvalid // uniform: never reveal a malformed/unknown identity
	}
	// Resolve the account through its login-enabled primary identifier — the
	// unverified registration email created atomically at Register (design §2.3).
	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return ErrChallengeInvalid // uniform: no such account is indistinguishable from a wrong code
		}
		return err
	}
	if _, err := s.ConsumeChallenge(ctx, ident.UserID, challenge.PurposeVerifyRegistration, code); err != nil {
		return err // stable, already-uniform challenge errors (expired / invalid / too-many)
	}
	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		return err
	}
	now := s.now()
	// Retire the unverified registration identifier and claim its verified
	// replacement in one revision-CAS operation (design §2.2). ReplacesIdentifierID
	// frees the partial authentication-claim index before the verified row claims it.
	input := identifier.ApplyVerifiedChangeInput{
		UserID:               ident.UserID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      normalized,
		LoginEnabled:         true,
		RecoveryEnabled:      true,
		NotificationEnabled:  true,
		MakePrimary:          true,
		ReplacesIdentifierID: ident.ID,
	}
	if _, err := s.identifiers.ApplyVerifiedChange(ctx, input, u.AuthRevision, now); err != nil {
		if errors.Is(err, sdk.ErrConflict) || errors.Is(err, sdk.ErrAlreadyExists) {
			return ErrRegistrationVerificationConflict
		}
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeEmailVerified,
		Status: securityevent.StatusSuccess,
	})
	// The verified identifier is now the authoritative claim; resolve the invitee's
	// pending auto-accept invitations against its normalized value.
	s.resolvePendingInvitations(ctx, normalized, u.ID)
	return nil
}

// resolvePendingInvitations grants a just-registered/verified email's pending
// auto-accept invitations, best-effort (design §6): a nil collaborator (off) or
// any error never affects the register/verify outcome — one failed grant never
// aborts registration. The invitation service audits each grant/failure itself;
// here a resolve error is a coarse WARN line. Called from both Register (so a
// no-verify host still resolves) and Verify; a second pass is a no-op because
// resolved invitations move off pending.
func (s *Service) resolvePendingInvitations(ctx context.Context, email, userID string) {
	if s.invitations == nil {
		return
	}
	if _, err := s.invitations.ResolveInvitations(ctx, email, PrincipalUser, userID); err != nil {
		s.logger.Warn("resolve invitations failed", "error_kind", errKind(err))
	}
}

// ActiveVerifiedIdentifier returns the normalized value of userID's active,
// VERIFIED identifier of kind — primary first, then oldest — resolved through the
// v3 identifier rail (design §7). It is the single kind-aware accessor that
// replaced the EmailForUser/VerifiedPhoneForUser proliferation: the invitation
// HTTP handlers key "mine" and the accept-time identifier match on it, and the
// accept-time phone match resolves the caller's verified phone through it, so
// invitationsvc stays decoupled from the identifier store (the auth feature owns
// user identity). No active verified identifier of that kind → sdk.ErrNotFound.
func (s *Service) ActiveVerifiedIdentifier(ctx context.Context, userID, kind string) (string, error) {
	addresses, err := s.projectAddresses(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, a := range addresses {
		if a.Kind == kind {
			return a.Value, nil
		}
	}
	return "", fmt.Errorf("no active verified %s identifier for user: %w", kind, sdk.ErrNotFound)
}

// Login rate-limits FIRST on (email, client-IP), then resolves the login-enabled
// email identifier (GetLogin, design §2.2), verifies the password, and mints a
// session, returning the access/refresh TokenPair (the session row holds only the
// refresh token's hash — see mintSession). Password login stays email-only in v3
// (design §4.1); identity is resolved through user_identifiers, not the legacy
// email column. Rate-limit exhaustion returns ErrRateLimited; any credential
// mismatch (unknown identifier, missing password, wrong password) returns the same
// generic sdk.ErrUnauthorized so the response cannot distinguish them.
//
// When Config.RequireVerifiedEmail is set, a caller with correct credentials but an
// unverified identifier is refused with ErrEmailNotVerified (403) — the check runs
// AFTER password verification, on the identifier's proof state, so it never leaks a
// verified/unverified signal to an unauthenticated attacker. Default off (design §7.1).
//
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo,
// set by the feature middleware) — the single source of truth for IP (design §5.1
// WI4); there is no clientIP parameter. Every exit records a security event: a
// rate-limited attempt is `blocked`, a credential/verification denial is
// `failure`, and a minted session is `success`.
func (s *Service) Login(ctx context.Context, emailAddr, password string) (TokenPair, user.User, error) {
	clientIP := clientInfoFromContext(ctx).ip
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		s.recordLogin(ctx, "", emailAddr, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}

	res, err := s.limiter.Allow(ctx, s.loginKey(string(identifier.KindEmail), normalized, clientIP), ratelimiter.PerMinute(loginAttemptsPerMinute))
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	if !res.Allowed {
		s.recordLogin(ctx, "", normalized, securityevent.StatusBlocked)
		return TokenPair{}, user.User{}, ErrRateLimited
	}

	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		s.recordLogin(ctx, "", normalized, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}
	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		s.recordLogin(ctx, ident.UserID, normalized, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}
	if s.requireVerifiedEmail && !ident.Verified() {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, ErrEmailNotVerified
	}

	pair, err := s.mintSession(ctx, u.ID, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	s.recordLogin(ctx, u.ID, normalized, securityevent.StatusSuccess)
	return pair, u, nil
}

// recordLogin appends a `login` audit row. The attempted email is an identifier
// (never a secret), so it rides Details to make failed-login auditing useful; a
// resolved userID is attributed when one is known.
func (s *Service) recordLogin(ctx context.Context, userID, email, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeLogin,
		Status:  status,
		Details: map[string]any{"email": email},
	})
}

// Logout revokes the session behind the caller's credentials and is idempotent
// (a missing/already-gone session is not an error). It resolves the session id
// through two lanes (§1.5):
//
//   - Primary: the refresh token (browser refresh cookie, or an API body). Hashed
//     and resolved via GetByRefreshHash; the matched row's id is deleted.
//   - Fallback: the access JWT (API bearer, no refresh token). Its session_id is
//     read IGNORING EXPIRY — an expired access JWT must never make logout a no-op
//     (shared-computer hazard). This read parses the JWT payload directly WITHOUT
//     signature verification, so it is used SOLELY to target a Delete; deleting a
//     session you cannot otherwise prove ownership of is at worst a self-inflicted
//     or low-value revoke, and the access credential is never trusted from it.
//
// A blank refresh token and a blank access token together are a no-op success.
func (s *Service) Logout(ctx context.Context, refreshToken, accessToken string) error {
	sessionID := ""
	if refreshToken != "" {
		if hash, err := s.hashSessionToken(refreshToken); err == nil {
			if sess, _, err := s.sessions.GetByRefreshHash(ctx, hash); err == nil {
				sessionID = sess.ID
			}
		}
	}
	if sessionID == "" && accessToken != "" {
		sessionID = sessionIDIgnoringExpiry(accessToken)
	}
	if sessionID != "" {
		if err := s.sessions.Delete(ctx, sessionID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return err
		}
		// Revoking a session invalidates its recent-authentication grants (design
		// §5.0): the DeleteBySession cascade is best-effort defense-in-depth — a live
		// session is already required to consume a grant, so a leftover grant on a
		// deleted session is unusable regardless.
		if s.authGrants != nil {
			if err := s.authGrants.DeleteBySession(ctx, sessionID); err != nil {
				s.logger.Warn("delete session grants failed", "error_kind", errKind(err))
			}
		}
	}
	// The user id is best-effort: a principal stashed on ctx attributes the row.
	uid, _ := s.CurrentUser(ctx)
	details := map[string]any(nil)
	if sessionID != "" {
		details = map[string]any{"session_id": sessionID}
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  uid,
		Type:    securityevent.TypeLogout,
		Status:  securityevent.StatusSuccess,
		Details: details,
	})
	return nil
}

// ChangePassword verifies the current password, stores the new hash, then
// revokes ALL of the user's sessions and mints a fresh one for the caller,
// returning the new access/refresh TokenPair (design §7.2, amended 2026-07-07). A
// wrong current password returns sdk.ErrUnauthorized; a too-short new password
// returns sdk.ErrInvalidInput.
//
// Atomicity pin (design §7.2): once the new hash is stored, a DeleteByUser
// failure is RETURNED, never best-effort-logged — the password changed but stale
// sessions may survive, and that must surface to the operator.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (TokenPair, error) {
	hash, err := s.passwords.Get(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.hasher.VerifyPassword(hash, currentPassword); err != nil {
		return TokenPair{}, fmt.Errorf("current password is incorrect: %w", sdk.ErrUnauthorized)
	}
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return TokenPair{}, err
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return TokenPair{}, fmt.Errorf("hash password: %w", err)
	}
	if err := s.passwords.Set(ctx, userID, newHash); err != nil {
		return TokenPair{}, err
	}
	// Route the session revocation + fresh caller mint through the shared password-
	// mutation tail (design §5.2/§5.3/§7.2): the caller just proved the current
	// password, so the reminted session records a fresh password authentication and
	// the recent-primary-login shortcut then covers an immediately following mutation.
	pair, err := s.revokeAndRemintForPassword(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordChange,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// ForgotPassword is the enumeration-safe unauthenticated start (design §4.1/§6.1.1):
// it normalizes the address and enqueues an OPAQUE delivery command carrying only the
// normalized identifier — it never resolves the account, issues a challenge, or calls
// a provider on the request path. The worker (Service.Initialize) later resolves the
// active VERIFIED recovery identifier, issues the password_reset token, renders, and
// delivers; an unknown or unverified address resolves nothing there, so known and
// unknown addresses share one bounded request path with identical repository calls.
// A malformed address returns nil (uniform). Recovery stays email-only in v3.
func (s *Service) ForgotPassword(ctx context.Context, emailAddr string) error {
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return nil // never reveal validity/existence
	}
	if s.queue == nil {
		return ErrDeliveryDisabled
	}
	key := s.idempotencyKey(identity.KindEmail, normalized, delivery.PurposePasswordReset)
	_, err = s.queue.Enqueue(ctx, delivery.Command{
		Kind:           identity.KindEmail,
		Purpose:        delivery.PurposePasswordReset,
		IdempotencyKey: key,
		Envelope:       delivery.Envelope{ResolutionInput: normalized},
	})
	return err
}

// ResetPassword redeems a reset token through the atomic passwordreset
// composition (design §5.9): in one transaction it consumes the live
// password_reset challenge, sets the new password hash, revokes every session,
// and revokes the user's outstanding recent-authentication grants and
// password/reset challenges. It NEVER logs the caller in — no session is minted —
// so the next sensitive action requires fresh step-up. A too-short password
// returns sdk.ErrInvalidInput; an unknown, expired, or already-used token is the
// single generic ErrPasswordResetInvalid (enumeration/anti-probing).
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return err
	}
	if s.passwordResets == nil || s.protector == nil {
		return fmt.Errorf("password reset subsystem not wired: %w", sdk.ErrForbidden)
	}
	if token == "" {
		return ErrPasswordResetInvalid // an empty token never matches a live challenge
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	res, err := s.passwordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose:                challenge.PurposePasswordReset,
		TokenDigest:            s.protector.DigestToken(token),
		NewPasswordHash:        newHash,
		PurgeChallengePurposes: passwordResetPurgePurposes,
		Now:                    s.now(),
	})
	if err != nil {
		// A non-live token (unknown/expired/used) is the single generic failure; no
		// state was changed. Anything else is infrastructure.
		if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired) {
			return ErrPasswordResetInvalid
		}
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: res.UserID,
		Type:   securityevent.TypePasswordReset,
		Status: securityevent.StatusSuccess,
	})
	return nil
}

// ValidateSession returns the live session for sessionID — the RequireLiveSession
// lookup (§1.4). A blank id returns sdk.ErrUnauthorized; unknown/expired sessions
// surface sdk.ErrNotFound / sdk.ErrExpired from the store (Get is keyed by the
// app-minted id now, so no hashing is involved).
func (s *Service) ValidateSession(ctx context.Context, sessionID string) (session.Session, error) {
	if sessionID == "" {
		return session.Session{}, fmt.Errorf("no session: %w", sdk.ErrUnauthorized)
	}
	return s.sessions.Get(ctx, sessionID)
}

// RequireUser is HTTP middleware that gates next on a valid user credential. On
// a missing/invalid/expired credential it writes a 401 JSON error; on success it
// stashes the user id on the request context (read via CurrentUser) and calls
// next. It satisfies web.Middleware via the method value s.RequireUser.
//
// It verifies the access JWT statelessly — signature + expiry only, zero DB
// (§1.2). The credential is either an Authorization: Bearer JWT (API flows) or
// the access-JWT session cookie (browser flows); both resolve to the same user
// identity. Revocation is honored within ≤ AccessTokenTTL on this tier; route
// RequireLiveSession for immediate revocation.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.resolveUserID(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), identity.Principal{Type: identity.User, ID: userID})))
	})
}

// resolveUserID resolves the request's user identity for RequireUser, stateless
// and DB-free (§1.2). A JWT-shaped bearer is authoritative (a bad one denies,
// never falling through to the cookie); otherwise the access-JWT session cookie
// is verified. Both paths verify signature + expiry only.
func (s *Service) resolveUserID(r *http.Request) (string, bool) {
	if raw, ok := bearerToken(r); ok && isJWTToken(raw) {
		return s.verifyBearer(raw)
	}
	c, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return "", false
	}
	return s.verifyBearer(c.Value)
}

// CurrentUser returns the authenticated user id stashed by RequireUser, if any.
// It is the cross-feature identity port other features consume structurally
// (features/README.md §5's CurrentUser).
func (s *Service) CurrentUser(ctx context.Context) (string, bool) {
	p, ok := identity.FromContext(ctx)
	if !ok || p.Type != identity.User {
		return "", false
	}
	return p.ID, true
}

// SetSessionCookies writes the browser credential cookies for a mint (§1.1, D4).
// It always sets the access cookie (the access JWT, existing policy: HttpOnly +
// SameSite=Lax, Secure/Domain/MaxAge from config). It sets the refresh cookie
// only when pair.RefreshToken is non-empty (so the grace lane, which issues no
// new refresh token, leaves the client's refresh cookie intact); the refresh
// cookie is HttpOnly, Path=/auth (covers /auth/refresh AND /auth/logout),
// SameSite=Lax explicit (CSRF posture for the cookie-driven refresh endpoint),
// with MaxAge tracking the fixed refresh horizon.
func (s *Service) SetSessionCookies(w http.ResponseWriter, pair TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    pair.AccessToken,
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.cookie.MaxAge,
	})
	if pair.RefreshToken != "" {
		http.SetCookie(w, s.refreshCookie(pair.RefreshToken, int(s.refreshTTL/time.Second)))
	}
}

// ClearSessionCookies expires BOTH the access and refresh cookies on the client
// (§1.5): logging out must not leave a live access JWT or refresh token behind.
func (s *Service) ClearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    "",
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, s.refreshCookie("", -1))
}

// refreshCookie builds the refresh cookie with the fixed Path=/auth scope and the
// explicit SameSite=Lax policy (D4). maxAge < 0 expires it.
func (s *Service) refreshCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     s.refreshCookieName(),
		Value:    value,
		Path:     refreshCookiePath,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// SessionCookieName returns the configured access (session) cookie name.
func (s *Service) SessionCookieName() string { return s.cookie.Name }

// RefreshCookieName returns the refresh cookie name (the access cookie name with
// a "_refresh" suffix). The refresh endpoint and logout read it to recover the
// refresh token from a browser client.
func (s *Service) RefreshCookieName() string { return s.refreshCookieName() }

func (s *Service) refreshCookieName() string { return s.cookie.Name + "_refresh" }

// mintSession creates a fresh session for userID and returns its access/refresh
// TokenPair (§1.1). It app-mints the session (id + raw refresh token), stores the
// row under the refresh token's SHA-256 HASH (the raw token is never persisted),
// and signs an access JWT carrying {user_id, session_id}. It is the single mint
// path Login, ChangePassword, IssueToken, and the OAuth flows share, so no call
// site persists a raw token or hand-rolls the pair.
//
// auth records how and when the primary authentication that minted the session
// happened (design §5.0): a successful login stamps its method/time/assurance so a
// sufficiently recent login can satisfy a recent-authentication grant without an
// extra step-up prompt. A zero value means none recorded (the shortcut never fires
// for that session).
func (s *Service) mintSession(ctx context.Context, userID string, auth session.AuthenticationMetadata) (TokenPair, error) {
	sess, rawRefresh := session.NewSession(userID, s.refreshTTL, s.now())
	refreshHash, err := s.hashSessionToken(rawRefresh)
	if err != nil {
		return TokenPair{}, err
	}
	sess.RefreshTokenHash = refreshHash
	sess.Authentication = auth
	created, err := s.sessions.Create(ctx, sess)
	if err != nil {
		return TokenPair{}, err
	}
	access, expiresAt, err := s.signAccessToken(userID, created.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt, RefreshToken: rawRefresh}, nil
}

// primaryAuthentication builds the session authentication metadata for a primary
// login performed with kind (design §5.0). It stamps the honest method descriptor
// and its assurance as of now so the recent-primary-login shortcut can later judge
// method/age/assurance against an operation's policy. An unknown method kind
// records no descriptor and the zero assurance, so the shortcut never treats an
// unrecognized method as sufficient.
func (s *Service) primaryAuthentication(kind session.MethodKind) session.AuthenticationMetadata {
	meta := session.AuthenticationMetadata{AuthenticatedAt: s.now().UTC()}
	if d, ok := session.DescribeMethod(kind); ok {
		meta.Methods = []session.AuthenticationMethod{d}
		meta.Assurance = d.Assurance
	}
	return meta
}

// signAccessToken signs an access JWT carrying {user_id, session_id} at
// AccessTokenTTL (§1.1). The signer stamps exp (from the returned expiry) and
// iat; this call sets the identity claims. session_id backs RequireLiveSession
// and the logout fallback.
func (s *Service) signAccessToken(userID, sessionID string) (string, time.Time, error) {
	expiresAt := s.now().Add(s.accessTTL)
	token, err := s.tokenSigner.Sign(map[string]any{
		tokenClaimUserID:    userID,
		tokenClaimSessionID: sessionID,
	}, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return token, expiresAt, nil
}

// hashSessionToken returns the stored form of a refresh token — its SHA-256 hex
// digest. This is the ONLY place a refresh token is hashed (design §7.3):
// mintSession's create, Rotate's new-hash, Refresh's resolve, and Logout's
// primary lane all route through it, so the raw token never reaches a repository
// and no second call site can drift.
func (s *Service) hashSessionToken(token string) (string, error) {
	return s.tokenHasher.Hash(token)
}

// normalizeEmail canonicalizes an email address through the single injected
// identifier normalizer (design §2.2) so a login/verify/recovery lookup resolves
// the same stored value registration claimed. It is the one email-normalization
// path the identity-bearing flows share; a rejected value wraps sdk.ErrInvalidInput.
func (s *Service) normalizeEmail(value string) (string, error) {
	return s.normalizer.Normalize(string(identifier.KindEmail), value)
}

// loginKey derives the PII-free rate-limit key for a login attempt: a
// non-reversible identifier digest (design §4.4 — a raw email/phone never enters a
// limiter key) combined with the trusted client IP. The IP comes from the
// client-info carrier, which routes.go resolves from web.TrustProxies or
// RemoteAddr and NEVER from a raw X-Forwarded-For header, so a spoofed forwarding
// header cannot rotate an attacker off a victim's bucket. Equivalent normalized
// values digest to one bucket, and an unknown identifier keys the same shape as a
// known one, so the limiter leaks no existence signal before account resolution.
func (s *Service) loginKey(kind, normalizedValue, clientIP string) string {
	return "login:" + s.identifierDigest(kind, normalizedValue) + "|" + clientIP
}

// invalidCredentials is the single generic error returned for every credential
// mismatch so the response cannot distinguish "no such user" from "wrong
// password".
func invalidCredentials() error {
	return fmt.Errorf("invalid email or password: %w", sdk.ErrUnauthorized)
}

// validatePassword enforces the single-factor password policy (design §5.9) and
// is the SINGLE validator every password entry point — register, set, change,
// reset — routes through, so the policy can never drift between flows. The rules
// are length-only, with no arbitrary composition or periodic-rotation
// requirements:
//
//   - a finite pre-hash byte cap (maxPasswordInputBytes), checked first so a
//     pathological megabyte input never reaches the rune counter or the hasher;
//   - at least minPasswordCodePoints Unicode code points;
//   - at most maxPasswordCodePoints Unicode code points; and
//   - when a compromised-password checker is wired, a breach/blocklist check.
//
// The breach check runs LAST — after the cheap length gates — so a wired remote
// checker is never consulted for input that is already rejected. When the checker
// cannot complete, the fail-closed default rejects the password (an unavailable
// breach service must not become a bypass); compromisedFailOpen trades that for
// availability, logging a WARN and accepting the password.
func (s *Service) validatePassword(ctx context.Context, pw string) error {
	if len(pw) > maxPasswordInputBytes {
		return fmt.Errorf("password must be at most %d bytes: %w", maxPasswordInputBytes, sdk.ErrInvalidInput)
	}
	n := utf8.RuneCountInString(pw)
	if n < minPasswordCodePoints {
		return fmt.Errorf("password must be at least %d characters: %w", minPasswordCodePoints, sdk.ErrInvalidInput)
	}
	if n > maxPasswordCodePoints {
		return fmt.Errorf("password must be at most %d characters: %w", maxPasswordCodePoints, sdk.ErrInvalidInput)
	}
	if s.compromised == nil {
		return nil
	}
	bad, err := s.compromised.IsCompromised(ctx, pw)
	if err != nil {
		if s.compromisedFailOpen {
			s.logger.Warn("compromised-password check failed open", "error_kind", errKind(err))
			return nil
		}
		return fmt.Errorf("compromised-password check unavailable: %w", err)
	}
	if bad {
		return ErrPasswordCompromised
	}
	return nil
}

// RateLimitByIP returns middleware that throttles a PUBLIC route on the client
// IP, refusing with a 429 once the per-minute budget is spent. It reuses the
// feature's configured RateLimiter (the one Login uses) and reads the IP from
// the client-info carrier, so an unauthenticated route (invitation decline, the
// design §6 case) is protected with no new plumbing. A limiter error fails OPEN
// (the request proceeds) — the in-memory default never errors, and a public
// decline must not be blocked by a limiter outage.
func (s *Service) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	keyFunc := func(r *http.Request) string {
		return keyPrefix + ":" + clientInfoFromContext(r.Context()).ip
	}
	rejectFunc := func(w http.ResponseWriter, _ *http.Request, _ ratelimiter.Result) {
		writeTooManyRequests(w)
	}
	return ratelimiter.Middleware(s.limiter, ratelimiter.PerMinute(perMinute), keyFunc, rejectFunc)
}

// writeUnauthorized writes a 401 JSON error via the shared sdk responder, so
// RequireUser's rejection matches the feature's other error responses (FS9).
func writeUnauthorized(w http.ResponseWriter) {
	web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
}

// writeTooManyRequests writes a 429 JSON error via the shared sdk responder, so
// a rate-limited public route matches the feature's error shape (FS9).
func writeTooManyRequests(w http.ResponseWriter) {
	web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
}
