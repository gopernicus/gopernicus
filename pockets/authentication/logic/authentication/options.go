package authentication

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// Option configures New before construction. Nil options are invalid.
type Option func(*constructorConfig)

// DelegatedTokensConfig enables verification of delegated tokens from one exact
// issuer. It does not enable issuance. The session store must round-trip the
// immutable delegation binding; stores that omit it cannot admit these tokens.
type DelegatedTokensConfig struct {
	Issuer string
}

// WithDelegatedTokens replaces the trusted delegated-token issuer. An empty
// issuer disables delegated verification (the default).
func WithDelegatedTokens(value DelegatedTokensConfig) Option {
	return func(c *constructorConfig) { c.DelegatedTokens = value }
}

// Repositories supplies storage capabilities; enabled features require their matching ports.
type Repositories struct {
	Users user.UserRepository
	// Identifiers backs the v3 identity-discovery rail (design §2.2). Registration
	// creates an unverified primary email identifier atomically with the user
	// (CreateWithPrimaryIdentifier); login/token resolve identity through GetLogin;
	// Verify claims/verifies it through the atomic revision-CAS ApplyVerifiedChange.
	// Wired whenever the challenge-backed register/verify flow is active (the
	// register/verify/login path assumes it is non-nil, like Users/Passwords).
	Identifiers       identifier.IdentifierRepository
	Passwords         user.PasswordRepository
	Sessions          session.SessionRepository
	SessionManagement session.ManagementRepository
	// UserAdmin is the OPTIONAL user-administration repository (CHAU-1.1): the
	// paginated operator directory plus the atomic status/revocation transition.
	// Nil → the administration service methods fail closed with
	// ErrUserAdminUnavailable and the bundled admin routes are not registered.
	UserAdmin user.AdminRepository
	// UserErasure is the OPTIONAL erasure capability (SOC 2 S3), backed by the
	// same transaction-capable adapter as Users, Identifiers, Sessions and the
	// rest. Nil → EraseUser fails closed with ErrUserErasureUnavailable.
	UserErasure user.EraseRepository
	// PasswordlessRedeem is the OPTIONAL atomic magic-link redemption repository
	// (CHAU-6.1). Named apart from the Passwordless KIND list above, which is a
	// different thing entirely. Required only when ProvisionOnRedeem is enabled;
	// package auth enforces that pairing.
	PasswordlessRedeem passwordless.Repository
	ActiveSessions     session.ActiveUserRepository
	// Challenges backs the atomic secret rail (design §3.2): HMAC-protected OTP
	// codes and SHA-256 magic-link tokens with atomic replace/consume. Nil until a
	// host wires the challenge subsystem; the challenge service methods refuse
	// while it is nil (fail closed). Protector is required alongside it.
	Challenges challenge.Repository
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
	// SecurityEvents is the optional append-only audit rail (design §5.1,
	// ratified AV9). Nil → no audit trail: the synchronous recording site is a
	// documented no-op. When wired, every sensitive op records synchronously and
	// a write failure is logged at WARN, never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository
	// OAuth flow collaborators (design §3). Providers empty → the OAuth
	// subsystem is off and its routes are not registered (deny-by-absence); the
	// two oauth repositories and TokenEncrypter may then be nil. When Providers
	// is non-empty the auth package requires both oauth repositories at
	// construction (auth.ErrOAuthReposRequired).
	OAuthAccounts oauthaccount.OAuthAccountRepository
	OAuthStates   oauthstate.StateRepository
	// Machine-identity collaborators (design §4.1). Both nil → the API-key /
	// service-account subsystem is off (routes not registered, the bearer
	// API-key path is inert). The auth package enforces both-or-neither at
	// construction (auth.ErrMachineReposRequired).
	ServiceAccounts serviceaccount.ServiceAccountRepository
	APIKeys         apikey.APIKeyRepository
	// ExternalSubjects backs the external identity mirror. It is required when
	// WithExternal wires providers and unused otherwise.
	ExternalSubjects externalsubject.Repository
	// Passkeys stores registered passkeys. It is required when WithPasskeys wires
	// a relying party and unused otherwise; it must be backed by the same
	// transaction-capable adapter as Users and CredentialMutations, whose
	// Snapshot and Apply cover passkeys.
	Passkeys passkey.Repository
	// TOTP stores the second-factor aggregate: each user's authenticator-app
	// credential and recovery codes. It is required when WithTOTP wires an
	// encrypter and unused otherwise; it must be backed by the same
	// transaction-capable adapter as Users, Sessions, AuthenticationGrants and
	// Passkeys.
	TOTP totp.Repository
}

// TOTPConfig enables the authenticator-app second factor and recovery codes.
type TOTPConfig struct {
	// Encrypter seals each TOTP secret at rest. Nil leaves TOTP off.
	Encrypter cryptids.KeyedEncrypter
	// Issuer is the name an authenticator app lists the account under. It is
	// required and must not contain a colon.
	Issuer string
}

// WithMFAMethods sets MFAConfig.Methods: the AAL2 sources the second-factor
// gate admits when it requires AAL2 (adding or removing a passkey or the
// authenticator app, regenerating recovery codes), and the factors it counts as
// held. Empty admits any source.
func WithMFAMethods(methods ...MFAMethod) Option {
	methods = slices.Clone(methods)
	return func(c *constructorConfig) {
		c.MFAMethods = slices.Clone(methods)
	}
}

// WithTOTP replaces the complete TOTPConfig group, including zero values.
func WithTOTP(value TOTPConfig) Option {
	return func(c *constructorConfig) {
		c.TOTPConfig = value
	}
}

// PasskeyConfig enables passkeys over a WebAuthn relying party.
type PasskeyConfig struct {
	// RelyingParty runs the ceremonies. Nil leaves passkeys off.
	RelyingParty webauthn.RelyingParty
	// UserVerification is requested of every ceremony: preferred (the zero
	// value; a presence-only passkey still signs in, at AAL1) or required.
	// Discouraged is refused: it would leave no native AAL2 path.
	UserVerification webauthn.Requirement
	// MaxPerUser caps the passkeys one user may hold. Zero selects 10; above 25
	// is refused.
	MaxPerUser int
}

func (c PasskeyConfig) userVerification() webauthn.Requirement {
	if c.UserVerification == "" {
		return webauthn.Preferred
	}
	return c.UserVerification
}

func (c PasskeyConfig) maxPerUser() int {
	if c.MaxPerUser == 0 {
		return defaultPasskeysPerUser
	}
	return c.MaxPerUser
}

// WithPasskeys replaces the complete PasskeyConfig group, including zero values.
func WithPasskeys(value PasskeyConfig) Option {
	return func(c *constructorConfig) {
		c.Passkey = value
	}
}

// PasswordConfig selects password availability, validation, and login requirements.
type PasswordConfig struct {
	Hasher Hasher
	// ValidatePassword replaces default new-password limits when non-nil.
	ValidatePassword func(context.Context, string) error
	// Compromised is the OPTIONAL host-injected breach/blocklist checker (design
	// §5.9). Nil → no breach check. When wired, every password entry point
	// (register/set/change/reset) consults it through the single validatePassword
	// path. CompromisedFailOpen selects the policy when the checker cannot complete.
	Compromised CompromisedPasswordChecker
	// CompromisedFailOpen selects the policy when a wired Compromised checker
	// returns an error (cannot complete a check). Default false = FAIL CLOSED: an
	// unavailable breach service rejects the password rather than silently becoming
	// a bypass — the required production posture (design §5.9, V15 fail-closed
	// profile). Set true only to trade breach coverage for availability (a
	// development/self-hosted convenience).
	CompromisedFailOpen bool
	// PasswordFlowsDisabled turns the password credential OFF as a posture: the
	// registration / password-login / verification / forgot-reset / change-set-
	// remove entry points refuse with ErrPasswordFlowsDisabled, and the inbound
	// layer registers none of their routes (deny-by-absence, like machine
	// identity). For hosts whose only way in is OAuth or passwordless.
	PasswordFlowsDisabled bool
	// RequireVerifiedEmail, when true, makes Login refuse an unverified user
	// with ErrEmailNotVerified (403). Default false (design §7.1, AV8).
	RequireVerifiedEmail bool
}

// WithPassword replaces the complete PasswordConfig group, including zero values.
func WithPassword(value PasswordConfig) Option {
	return func(c *constructorConfig) {
		c.Hasher = value.Hasher
		c.ValidatePassword = value.ValidatePassword
		c.Compromised = value.Compromised
		c.CompromisedFailOpen = value.CompromisedFailOpen
		c.PasswordFlowsDisabled = value.PasswordFlowsDisabled
		c.RequireVerifiedEmail = value.RequireVerifiedEmail
	}
}

// SessionsConfig sets the access-token and refresh-token lifetimes.
type SessionsConfig struct {
	AccessTokenTTL time.Duration
	RefreshTTL     time.Duration
}

// WithSessions replaces the complete SessionsConfig group, including zero values.
func WithSessions(value SessionsConfig) Option {
	return func(c *constructorConfig) {
		c.AccessTokenTTL = value.AccessTokenTTL
		c.RefreshTTL = value.RefreshTTL
	}
}

// IdentityConfig selects identifier normalization and credential security policies.
type IdentityConfig struct {
	// Normalizer canonicalizes identifier values for persistence, lookup, and
	// rate-limit keys (design §2.2): one injected policy so a stored identifier and
	// a login/verify/recovery lookup speak the same normalized string. Nil selects
	// the bundled strict identifier.DefaultNormalizer.
	Normalizer identifier.Normalizer
	// IdentifierKeyer derives PII-free outbox idempotency keys (design §4.4). Nil → a
	// SHA-256 fallback keeps keys PII-free without the host keyer.
	IdentifierKeyer identifierKeyer
	// CredentialPolicy evaluates a proposed credential/identifier mutation against
	// the current and proposed MethodSet (design §5.6): the /auth/methods removable
	// hints and every sensitive mutation route it before their revision-CAS Apply.
	// Nil selects the bundled safe credential.NewDefaultPolicy default.
	CredentialPolicy credential.Policy
}

// WithIdentity replaces the complete IdentityConfig group, including zero values.
func WithIdentity(value IdentityConfig) Option {
	return func(c *constructorConfig) {
		c.Normalizer = value.Normalizer
		c.IdentifierKeyer = value.IdentifierKeyer
		c.CredentialPolicy = value.CredentialPolicy
	}
}

// DeliveryConfig supplies message rendering and delivery dependencies.
type DeliveryConfig struct {
	// Deliver is the shared kind-aware delivery renderer/router (design §6.1),
	// constructor-injected by package auth (which builds it from Mailer) and
	// shared with invitations. It renders an encrypted-job-ready Envelope and routes
	// a send through the email/notify kind fork; the durable worker consumes it. The
	// request-time send sites enqueue rendered/opaque commands through Queue (AV3-4.3).
	Deliver *delivery.Router
	// Queue is the delivery dispatch seam every send site enqueues through instead of
	// a request-time provider call. Wired whenever a delivery dispatcher is (package
	// auth builds it from the jobs-mode DeliveryDispatcher or the in_process
	// queue); nil → outbound disabled (the send sites fail loudly).
	Queue deliveryQueue
}

// WithDelivery replaces the complete DeliveryConfig group, including zero values.
func WithDelivery(value DeliveryConfig) Option {
	return func(c *constructorConfig) {
		c.Deliver = value.Deliver
		c.Queue = value.Queue
	}
}

// OAuthConfig selects providers, callback destinations, and identity trust policy.
type OAuthConfig struct {
	Providers               []oauth.Provider
	TokenEncrypter          cryptids.Encrypter
	OAuthCallbackBase       string
	OAuthNativeRedirectURIs []string
	TrustOAuthEmail         func(string, oauth.UserInfo) bool
}

// WithOAuth replaces the complete OAuthConfig group, including zero values.
func WithOAuth(value OAuthConfig) Option {
	value = cloneOAuthConfig(value)
	return func(c *constructorConfig) {
		value := cloneOAuthConfig(value)
		c.Providers = value.Providers
		c.TokenEncrypter = value.TokenEncrypter
		c.OAuthCallbackBase = value.OAuthCallbackBase
		c.OAuthNativeRedirectURIs = value.OAuthNativeRedirectURIs
		c.TrustOAuthEmail = value.TrustOAuthEmail
	}
}

func cloneOAuthConfig(value OAuthConfig) OAuthConfig {
	value.Providers = slices.Clone(value.Providers)
	value.OAuthNativeRedirectURIs = slices.Clone(value.OAuthNativeRedirectURIs)
	return value
}

// PasswordlessConfig enables identifier kinds and optional account provisioning.
type PasswordlessConfig struct {
	// Passwordless is the host-enabled passwordless kind set (design §4.2), resolved
	// and validated by package auth (auth.Passwordless) before it reaches here.
	// Empty → passwordless is off and its routes are not registered (deny-by-absence);
	// a non-empty set lists the {email, phone} kinds whose active verified
	// login-enabled identifiers are permitted as direct passwordless login methods.
	Passwordless []string
	// ProvisionOnRedeem enables account creation from an email magic link at
	// CONSUME time (PasswordlessProvisionOnRedeem).
	ProvisionOnRedeem bool
}

// WithPasswordless replaces the complete PasswordlessConfig group, including zero values.
func WithPasswordless(value PasswordlessConfig) Option {
	value = clonePasswordlessConfig(value)
	return func(c *constructorConfig) {
		value := clonePasswordlessConfig(value)
		c.Passwordless = value.Passwordless
		c.ProvisionOnRedeem = value.ProvisionOnRedeem
	}
}

func clonePasswordlessConfig(value PasswordlessConfig) PasswordlessConfig {
	value.Passwordless = slices.Clone(value.Passwordless)
	return value
}

// LinksConfig sets authentication landing URLs and permitted redirect destinations.
type LinksConfig struct {
	// PublicAuthBaseURL is the absolute base URL passwordless magic links are built
	// from (design §6.4): the worker composes the sign-in link from this base only,
	// never from a request Host/forwarded header. Package auth validates it (absolute
	// http(s), HTTPS in production) whenever a passwordless kind is enabled.
	PublicAuthBaseURL string
	// PasswordResetURL is the absolute public reset landing route, BEFORE the token
	// query parameter is appended (CHAU-5.1). Empty → the legacy raw-token reset
	// template is rendered instead; package auth rejects an empty value in
	// production and warns in development. The link is built from THIS value only —
	// never from a request header.
	PasswordResetURL string
	// OAuthLinkBaseURL is the absolute SPA landing URL the OAuth pending-link email
	// links to, BEFORE the "#token=<token>" fragment is appended (oauth-pending-link
	// plan D1/D2). Empty → the legacy bare-token line is rendered instead; package
	// auth validates a non-empty value (absolute http(s), no fragment, HTTPS in
	// production) at construction. The link is built from THIS value only — never
	// from a request header.
	OAuthLinkBaseURL  string
	RedirectAllowlist []string
}

// WithLinks replaces the complete LinksConfig group, including zero values.
func WithLinks(value LinksConfig) Option {
	value = cloneLinksConfig(value)
	return func(c *constructorConfig) {
		value := cloneLinksConfig(value)
		c.PublicAuthBaseURL = value.PublicAuthBaseURL
		c.PasswordResetURL = value.PasswordResetURL
		c.OAuthLinkBaseURL = value.OAuthLinkBaseURL
		c.RedirectAllowlist = value.RedirectAllowlist
	}
}

func cloneLinksConfig(value LinksConfig) LinksConfig {
	value.RedirectAllowlist = slices.Clone(value.RedirectAllowlist)
	return value
}

// WithChallengeProtector replaces Protector for this constructor.
func WithChallengeProtector(value challengeProtector) Option {
	return func(c *constructorConfig) {
		c.Protector = value
	}
}

// WithSecurityEventRecorder supplies the recorder the service — and the external
// engine WithExternal builds — records through, so every recording site of a
// host shares one recorder and one failure count. It wins over
// Repositories.SecurityEvents. Without it New builds a recorder over
// Repositories.SecurityEvents that attributes rows through the client-info
// carrier.
func WithSecurityEventRecorder(value *securityevent.Recorder) Option {
	return func(c *constructorConfig) {
		c.SecurityEventRecorder = value
	}
}

// WithAuditWriteObserver sets the observer told about every failed audit write
// of the recorder New builds. A supplied recorder carries its own observer
// (securityevent.WithWriteObserver), so passing both options is
// sdk.ErrInvalidInput.
func WithAuditWriteObserver(value securityevent.WriteObserver) Option {
	return func(c *constructorConfig) {
		c.AuditWriteObserver = value
	}
}

// WithInvitations replaces Invitations for this constructor.
func WithInvitations(value invitationResolver) Option {
	return func(c *constructorConfig) {
		c.Invitations = value
	}
}

// WithLimits replaces the authentication budgets; zero fields select their documented defaults.
func WithLimits(value AuthenticationLimits) Option {
	return func(c *constructorConfig) {
		c.AuthenticationLimits = value
	}
}

// AbuseProtectionConfig selects the limiter outage policy. Budgets stay on
// WithLimits.
type AbuseProtectionConfig struct {
	// LimiterFailOpen admits a refresh when the per-session refresh budget's
	// limiter returns an error (WARN-logged). The zero value fails CLOSED in
	// every mode: Refresh returns an error matching sdk.ErrUnavailable (503).
	// Every other budget fails closed regardless.
	LimiterFailOpen bool
}

// WithAbuseProtection replaces the complete AbuseProtectionConfig group,
// including zero values.
func WithAbuseProtection(value AbuseProtectionConfig) Option {
	return func(c *constructorConfig) {
		c.LimiterFailOpen = value.LimiterFailOpen
	}
}

// WithClock supplies the clock; nil selects time.Now.
func WithClock(value func() time.Time) Option {
	return func(c *constructorConfig) {
		c.Clock = value
	}
}

// WithLogger supplies the logger; nil selects slog.Default().
func WithLogger(value *slog.Logger) Option {
	return func(c *constructorConfig) {
		c.Logger = value
	}
}

// WithIDs replaces IDs for this constructor.
func WithIDs(value sdk.IDGenerator) Option {
	return func(c *constructorConfig) {
		c.IDs = value
	}
}
