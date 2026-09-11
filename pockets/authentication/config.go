package authentication

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	credential "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	identifier "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	protection "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// constructorConfig holds resolved pocket construction settings.
// RuntimeMode and DeliveryMode are explicit; New validates dependencies for each
// enabled feature. Individual logic and HTTP packages also offer constructors.
type constructorConfig struct {
	AuthenticationLimits          authlogic.AuthenticationLimits
	Hasher                        authlogic.Hasher
	ValidatePassword              func(context.Context, string) error
	CompromisedPasswordChecker    authlogic.CompromisedPasswordChecker
	CompromisedPasswordFailOpen   bool
	Mailer                        email.Sender
	MailFrom                      string `env:"AUTH_MAIL_FROM"`
	RateLimiter                   ratelimiter.Limiter
	SessionCookie                 inbound.CookieConfig
	RefreshCookiePath             string   `env:"AUTH_REFRESH_COOKIE_PATH"`
	AllowedOrigins                []string `env:"AUTH_ALLOWED_ORIGINS"`
	BrowserLoginPath              string   `env:"AUTH_BROWSER_LOGIN_PATH"`
	RequireVerifiedEmail          bool     `env:"AUTH_REQUIRE_VERIFIED_EMAIL"`
	PasswordFlowsDisabled         bool     `env:"AUTH_PASSWORD_FLOWS_DISABLED"`
	MachineRoutesGate             web.Middleware
	BundledRouteAuth              inbound.BundledRouteAuthentication
	RuntimeMode                   environment.Mode `env:"AUTH_RUNTIME_MODE"`
	DeliveryMode                  delivery.Mode    `env:"AUTH_DELIVERY_MODE"`
	ChallengeProtector            protection.ChallengeProtector
	IdentifierNormalizer          identifier.Normalizer
	IdentifierKeyer               protection.IdentifierKeyer
	CredentialPolicy              credential.Policy
	DeliveryEncrypter             cryptids.Encrypter
	DeliveryDispatcher            delivery.Dispatcher
	DeliveryEventsEmitter         sdkevents.Emitter
	DeliveryJobsAcknowledged      bool
	DeliveryEphemeralAcknowledged bool
	InProcessDelivery             delivery.InProcessConfig
	PublicAuthBaseURL             string   `env:"AUTH_PUBLIC_BASE_URL"`
	PasswordResetURL              string   `env:"AUTH_PASSWORD_RESET_URL"`
	OAuthLinkBaseURL              string   `env:"AUTH_OAUTH_LINK_URL"`
	PasswordlessProvisionOnRedeem bool     `env:"AUTH_PASSWORDLESS_PROVISION_ON_REDEEM"`
	Passwordless                  []string `env:"AUTH_PASSWORDLESS"`
	IDs                           sdk.IDGenerator
	ListStrategy                  string `env:"AUTH_LIST_STRATEGY" default:"cursor"`
	Providers                     []oauth.Provider
	TokenEncrypter                cryptids.Encrypter
	OAuthCallbackBase             string `env:"AUTH_OAUTH_CALLBACK_BASE"`
	OAuthNativeRedirectURIs       []string
	TrustOAuthEmail               func(provider string, identity oauth.UserInfo) bool
	RedirectAllowlist             []string `env:"AUTH_REDIRECT_ALLOWLIST"`
	TokenSigner                   cryptids.JWTSigner
	AccessTokenTTL                time.Duration `env:"AUTH_ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTTL                    time.Duration `env:"AUTH_REFRESH_TTL" default:"168h"`
	Granter                       invitations.Granter
	InviteCheck                   invitations.InviteCheck
	UserAdminCheck                authlogic.UserAdminCheck
	MemberCheck                   invitations.MemberCheck
	BodySenders                   map[string]delivery.BodySender
	Views                         inbound.Views
	HTMLPolicy                    *inbound.HTMLResourcePolicy
	EmailContentTemplates         []delivery.TemplateOverride
	EmailLayouts                  []delivery.LayoutOverride
	EmailBranding                 *email.Branding
	DeliveryData                  delivery.DataHook
	EmailSubjects                 map[string]string
	SMSBodies                     map[string]string
	Logger                        *slog.Logger
}

// Option configures New before construction. Nil options are invalid.
type Option func(*constructorConfig)

// PasswordConfig selects password availability, validation, and login requirements.
type PasswordConfig struct {
	// Hasher is required while password flows are enabled.
	Hasher authlogic.Hasher
	// ValidatePassword checks newly chosen passwords in register/set/change/reset.
	// Nil uses the default limits: 15–64 Unicode code points and at most 256 bytes.
	// A callback replaces all those limits; the host owns its length and complexity
	// rules. The configured breach check still runs after successful validation,
	// and the hasher may impose an algorithm-specific input limit.
	// Return sdk.ValidationError or wrap sdk.ErrInvalidInput for a policy refusal;
	// errors are preserved, including dependency failures. Existing-password login
	// and step-up verification do not apply this policy.
	ValidatePassword func(context.Context, string) error
	// CompromisedPasswordChecker is the OPTIONAL breach/blocklist checker consulted
	// by the shared password policy (design §5.9). Nil → no breach check (the
	// length policy still applies). When wired, register/set/change/reset all
	// consult it identically; the pocket core ships none, so wiring it never adds
	// a network dependency to the core.
	CompromisedPasswordChecker authlogic.CompromisedPasswordChecker
	// CompromisedPasswordFailOpen selects the policy when a wired
	// CompromisedPasswordChecker cannot complete a check (returns an error).
	// Default false = FAIL CLOSED: an unavailable breach service rejects the
	// password rather than becoming a silent bypass — the documented production
	// posture (design §5.9, V15 fail-closed profile). Set true only to trade breach
	// coverage for availability (a development/self-hosted convenience); the WARN is
	// logged on Logger.
	CompromisedPasswordFailOpen bool
	// PasswordFlowsDisabled turns the password credential OFF as a posture, for a
	// host whose only way in is OAuth (or passwordless): Register mounts NONE of
	// the registration / password-login / verification / forgot-reset /
	// change-set-remove / step-up-password routes (JSON and HTML —
	// deny-by-absence, like machine identity), and the corresponding Service
	// use-cases refuse with ErrPasswordFlowsDisabled. Default false keeps every
	// route. Hasher is required only while password flows are enabled.
	PasswordFlowsDisabled bool `env:"AUTH_PASSWORD_FLOWS_DISABLED"`
	// RequireVerifiedEmail, when true, makes login refuse an unverified user
	// with a 403 (ErrEmailNotVerified). Default false (design §7.1, AV8):
	// flipping it on requires a working Mailer so users can verify.
	// (env: AUTH_REQUIRE_VERIFIED_EMAIL)
	RequireVerifiedEmail bool `env:"AUTH_REQUIRE_VERIFIED_EMAIL"`
}

// WithPassword replaces the complete PasswordConfig group, including zero values.
func WithPassword(value PasswordConfig) Option {
	return func(c *constructorConfig) {
		c.Hasher = value.Hasher
		c.ValidatePassword = value.ValidatePassword
		c.CompromisedPasswordChecker = value.CompromisedPasswordChecker
		c.CompromisedPasswordFailOpen = value.CompromisedPasswordFailOpen
		c.PasswordFlowsDisabled = value.PasswordFlowsDisabled
		c.RequireVerifiedEmail = value.RequireVerifiedEmail
	}
}

// SessionsConfig sets the access-token and refresh-token lifetimes.
type SessionsConfig struct {
	// AccessTokenTTL is the access-JWT lifetime (§1.1, D8). Zero → 15m. Keep it
	// short: it bounds the revocation-asymmetry window on stateless routes.
	AccessTokenTTL time.Duration `env:"AUTH_ACCESS_TOKEN_TTL" default:"15m"`
	// RefreshTTL is the fixed refresh-token / session horizon (§1.1, D2/D8). Zero →
	// 7d. It is set at mint and NEVER extended by rotation (fixed horizon); a
	// stolen refresh token therefore cannot outlive it.
	RefreshTTL time.Duration `env:"AUTH_REFRESH_TTL" default:"168h"`
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
	// ChallengeProtector protects short codes (HMAC pepper) and digests tokens
	// (auth v3 §3.3). The slot is frozen here; it becomes REQUIRED when the
	// challenge subsystem is enabled (phase 3, ErrChallengeProtectorRequired).
	// AV3-0.2 ships the bundled HMACChallengeProtector.
	ChallengeProtector protection.ChallengeProtector
	// IdentifierNormalizer canonicalizes identifier values everywhere (auth v3
	// §2.2). Nil selects the bundled strict default (AV3-1.1).
	IdentifierNormalizer identifier.Normalizer
	// IdentifierKeyer derives PII-free rate-limit/idempotency keys under a key
	// distinct from the challenge pepper, JWT, and encryption keys (auth v3 §4.4).
	// Production-required once privacy-keyed limits are wired (phase 5,
	// ErrIdentifierKeyerRequired).
	IdentifierKeyer protection.IdentifierKeyer
	// CredentialPolicy evaluates a proposed credential/identifier mutation against
	// the current and proposed MethodSet (auth v3 §5.6). Nil selects the bundled
	// safe default (credential.NewDefaultPolicy: one direct login method + one
	// verified recovery method, PSTN restricted) when the credential suite is
	// enabled (phase 6); a host may supply stronger rules. The slot is frozen here
	// (AV3-0.4); ErrCredentialPolicyRequired covers the strict-production posture
	// that disables the default without a replacement.
	CredentialPolicy credential.Policy
}

// WithIdentity replaces the complete IdentityConfig group, including zero values.
func WithIdentity(value IdentityConfig) Option {
	return func(c *constructorConfig) {
		c.ChallengeProtector = value.ChallengeProtector
		c.IdentifierNormalizer = value.IdentifierNormalizer
		c.IdentifierKeyer = value.IdentifierKeyer
		c.CredentialPolicy = value.CredentialPolicy
	}
}

// AbuseProtectionConfig selects the rate limiter and authentication budgets.
type AbuseProtectionConfig struct {
	// AuthenticationLimits configures per-subject and per-IP abuse budgets.
	// Zero values select documented defaults.
	AuthenticationLimits authlogic.AuthenticationLimits
	// RateLimiter throttles login attempts; nil → ratelimiter.NewMemory()
	// (safe-by-default: an in-process limiter, not "unlimited").
	RateLimiter ratelimiter.Limiter
}

// WithAbuseProtection replaces the complete AbuseProtectionConfig group, including zero values.
func WithAbuseProtection(value AbuseProtectionConfig) Option {
	return func(c *constructorConfig) {
		c.AuthenticationLimits = value.AuthenticationLimits
		c.RateLimiter = value.RateLimiter
	}
}

// DeliveryConfig supplies message rendering and delivery dependencies.
type DeliveryConfig struct {
	// Mailer is required while delivery is enabled. It delivers verification
	// and password-reset messages.
	Mailer email.Sender
	// MailFrom is the From address on verification/reset mail. (env: AUTH_MAIL_FROM)
	MailFrom string `env:"AUTH_MAIL_FROM"`
	// BodySenders chooses a plain-text transport for each supported non-email
	// identifier kind (for example "phone"). The pocket resolves eligibility and
	// selects one route per queued command. Email always uses Mailer with full
	// HTML/text content; an "email" entry is invalid. The map is copied at setup.
	BodySenders map[string]delivery.BodySender
	// DeliveryEncrypter encrypts the delivery-outbox payload envelope (auth v3
	// §6.1.1). REQUIRED once the outbox is enabled (phase 4,
	// ErrDeliveryEncrypterRequired); bundled cryptids.AESGCM satisfies it with a
	// distinct key.
	DeliveryEncrypter cryptids.Encrypter
	// DeliveryDispatcher is the generic-jobs delivery transport for DeliveryMode
	// "jobs" (authv3-delivery-refactor AV3D-3.1). It is the delivery queue: producers
	// submit sealed command envelopes through it and the host runs the generic jobs
	// runtime that invokes DeliveryJobRuntime().Handle. A jobs-mode host wires this
	// stdlib-typed seam, keeping the authentication core free of any jobs import.
	// REQUIRED for DeliveryMode "jobs" (ErrDeliveryQueueRequired); requires
	// DeliveryEncrypter (the payload is always sealed) and, in production,
	// DeliveryJobsAcknowledged.
	DeliveryDispatcher delivery.Dispatcher
	// DeliveryEventsEmitter is the OPTIONAL, secret-free delivery lifecycle observer's
	// event rail for DeliveryMode "jobs" (authv3-delivery-refactor AV3D-3.4). When set,
	// the jobs-mode transport emits a bounded lifecycle event (delivered, skipped,
	// retried, dead_lettered, purged) per transition onto this emitter. Emission is
	// strictly best-effort and observation-only: it is never on the path that records
	// delivery state, so a nil emitter, an emit error, or a panic never loses, retries,
	// duplicates, or fails accepted delivery work. Leave nil to run delivery with no
	// observation. It is meaningful only for DeliveryMode "jobs".
	DeliveryEventsEmitter sdkevents.Emitter
	// DeliveryJobsAcknowledged affirms that the host runs the durable jobs delivery
	// runtime in its process lifecycle (authv3-delivery-refactor AV3D-0.1). It is
	// meaningful only for DeliveryMode "jobs". The queue is the ONLY send path, so a
	// jobs-mode host that enqueues without running the runtime silently swallows every
	// verification, reset, and magic-link message. The pocket cannot observe the host
	// lifecycle, so production REQUIRES this explicit acknowledgment
	// (ErrDeliveryJobsUnacknowledged) rather than failing open on a stalled queue.
	// Development tolerates the zero value (a test or manual drain may run the runtime).
	// It is a wiring assertion set in the composition root, not an env knob.
	DeliveryJobsAcknowledged bool
	// DeliveryEphemeralAcknowledged affirms that the host accepts the crash-loss
	// guarantee of DeliveryMode "in_process" (authv3-delivery-refactor AV3D-0.1). The
	// in-process pool is process-local: accepted, in-flight delivery work is lost on a
	// crash. The pocket cannot make an ephemeral send path durable, so production
	// REFUSES in_process without this explicit acknowledgment
	// (ErrDeliveryEphemeralUnacknowledged) — the recommended production posture is
	// "jobs". Development tolerates the zero value. It is a wiring assertion set in the
	// composition root, not an env knob.
	DeliveryEphemeralAcknowledged bool
	// InProcessDelivery tunes the bounded, EPHEMERAL in-process delivery runtime
	// (authv3-delivery-refactor AV3D-4.5). Meaningful ONLY for DeliveryMode
	// "in_process"; the zero value is a valid, fully-defaulted configuration. Every knob
	// is nil-safe (zero → default) and every invalid bound (a negative value, or a
	// StatusMaxEntries below QueueCapacity) fails LOUDLY at construction. See
	// InProcessDeliveryConfig for the per-process (NOT cross-instance) semantics: two
	// instances each keep their own queue, de-duplication, and status, so both can send.
	InProcessDelivery delivery.InProcessConfig
}

// WithDelivery replaces the complete DeliveryConfig group, including zero values.
func WithDelivery(value DeliveryConfig) Option {
	value = cloneDeliveryConfig(value)
	return func(c *constructorConfig) {
		value := cloneDeliveryConfig(value)
		c.Mailer = value.Mailer
		c.MailFrom = value.MailFrom
		c.BodySenders = value.BodySenders
		c.DeliveryEncrypter = value.DeliveryEncrypter
		c.DeliveryDispatcher = value.DeliveryDispatcher
		c.DeliveryEventsEmitter = value.DeliveryEventsEmitter
		c.DeliveryJobsAcknowledged = value.DeliveryJobsAcknowledged
		c.DeliveryEphemeralAcknowledged = value.DeliveryEphemeralAcknowledged
		c.InProcessDelivery = value.InProcessDelivery
	}
}

func cloneDeliveryConfig(value DeliveryConfig) DeliveryConfig {
	value.BodySenders = maps.Clone(value.BodySenders)
	return value
}

// MessagesConfig customizes authentication messages and their shared branding.
type MessagesConfig struct {
	// EmailContentTemplates registers host overrides of the pocket's default email
	// content at email.LayerApp (design §6.2) — the DISTINCT second override system
	// alongside Views (which overrides HTML pages). Empty (default) → the bundled
	// LayerCore templates render unchanged. Each entry's Namespace must be
	// EmailContentNamespace to override a bundled template; its embed.FS is walked
	// from "templates/", each "<name>.html" replacing the core "<name>". This changes
	// email BODIES only — never a page, route, service policy, or the JSON API.
	EmailContentTemplates []delivery.TemplateOverride
	// EmailLayouts registers host overrides of the email LAYOUTS at email.LayerApp
	// — the frame around the bodies EmailContentTemplates overrides. Empty
	// (default) → the sdk's bundled layouts render unchanged. Every delivery
	// purpose renders with email.LayoutTransactional, so ONE entry shipping
	// "transactional.html" (and optionally "transactional.txt") re-frames all auth
	// mail:
	//
	//	//go:embed layouts/*
	//	var layoutsFS embed.FS
	//	cfg.EmailLayouts = []auth.EmailLayoutOverride{{FS: layoutsFS}}
	//
	// The embed.FS is walked from "layouts/" unless the entry names another Dir;
	// each file's base name is the layout type it replaces. Ship BOTH halves:
	// resolution picks the winning layer's html/text pair, so an ".html"-only
	// override renders the text half with no layout rather than the sdk's. Changes
	// the email FRAME only — never a page, route, service policy, or the JSON API.
	EmailLayouts []delivery.LayoutOverride
	// EmailBranding sets the brand values the bundled email LAYOUTS render
	// ({{.Brand.Name}}, .Tagline, .Address, .LogoURL — "Your Company" is the
	// unset fallback). It composes with EmailContentTemplates (bodies) without
	// overlap: branding fills the shared layout frame, content templates replace
	// bodies. Nil (default) → today's fallback branding.
	//
	// Every auth delivery purpose renders with email.LayoutTransactional, which
	// renders LogoURL, Name, Tagline, and Address. A non-empty LogoURL emits an
	// <img> above the brand name; an empty one emits no image element. Name and
	// Tagline stay visible either way, because mail clients block external images
	// by default. Set an absolute, publicly fetchable HTTPS URL: the renderer
	// never fetches or validates it, and html/template is the escaping boundary.
	// A host that overrides the transactional layout through EmailLayouts owns
	// its own logo markup — the override replaces the bundled block.
	EmailBranding *email.Branding
	// DeliveryData is the host's per-render DATA enrichment for every delivery
	// purpose — the third mail customization seam alongside EmailContentTemplates
	// (bodies) and EmailLayouts (the frame). The pocket builds the data a template
	// renders (for an invitation: the resource tuple, relation, inviter, metadata,
	// link — see the README's per-purpose table) but has no source for a resource's
	// NAME or a relation's label; the hook is where a host looks those up. It
	// receives the public purpose (Purpose*) and a fresh, secret-free defensive
	// copy of the data, and returns additions/replacements that are merged before
	// the subject and bodies render. Secret, Link, and Subject are reserved:
	// returning any of them fails the render with an invalid-input error, so a
	// hook can never alter the delivered credential or push a secret into a
	// subject. Nil (default) → the pocket-built data renders as-is, byte-for-byte
	// today's output. The bundled invitation/member-added bodies and SMS bodies
	// render {{or .ResourceName .ResourceID}}, so a hook that sets ResourceName
	// alone already changes "invited to project p1" into "invited to project
	// Apollo".
	//
	// The hook may run concurrently and must not retain or mutate its input after
	// returning. A hook error aborts that render before anything is queued; what
	// the CALLER sees depends on the flow: invitation create/resend return the
	// error (create may already have persisted the invitation, so a resend can
	// recover); the member-added notice is best-effort after an already-committed
	// grant, so the error is logged and never surfaced; and the opaque
	// password-reset/passwordless/verification starts were already accepted and
	// queued, so a worker-side hook error follows the delivery runtime's bounded
	// retry/dead-letter policy and is never reported to the original caller.
	DeliveryData delivery.DataHook
	// EmailSubjects overrides the bundled email SUBJECT per delivery purpose
	// (key = a Purpose* constant, value = Go text/template source rendered against
	// the same data as the body, DeliveryData enrichment included). Validated at
	// New: an unknown purpose key or an empty/whitespace-only
	// source is ErrDeliveryOverrideInvalid, as is a source that fails to parse.
	// Overrides parse with missing-key errors enabled, so a template naming a
	// field the purpose's data does not carry fails that render loudly instead of
	// shipping "<no value>". A rendered subject must be non-empty and free of CR/LF
	// (ErrDeliverySubjectInvalid) — the pocket rejects it before any envelope is
	// queued. Empty (default) → the bundled subjects.
	EmailSubjects map[string]string
	// SMSBodies overrides the bundled body-only SMS text per delivery purpose,
	// with the same keys, data, validation, and missing-key rules as EmailSubjects.
	// Only a purpose that already has an SMS rail can be overridden: an entry for
	// an email-only purpose (registration verification, password reset, OAuth
	// pending link) is ErrDeliveryOverrideInvalid — an override customizes an
	// existing rail, it never enables a new kind. Empty (default) → the bundled
	// SMS bodies.
	SMSBodies map[string]string
}

// WithMessages replaces the complete MessagesConfig group, including zero values.
func WithMessages(value MessagesConfig) Option {
	value = cloneMessagesConfig(value)
	return func(c *constructorConfig) {
		value := cloneMessagesConfig(value)
		c.EmailContentTemplates = value.EmailContentTemplates
		c.EmailLayouts = value.EmailLayouts
		c.EmailBranding = value.EmailBranding
		c.DeliveryData = value.DeliveryData
		c.EmailSubjects = value.EmailSubjects
		c.SMSBodies = value.SMSBodies
	}
}

func cloneMessagesConfig(value MessagesConfig) MessagesConfig {
	value.EmailContentTemplates = slices.Clone(value.EmailContentTemplates)
	value.EmailLayouts = slices.Clone(value.EmailLayouts)
	if value.EmailBranding != nil {
		copy := *value.EmailBranding
		copy.SocialLinks = slices.Clone(copy.SocialLinks)
		value.EmailBranding = &copy
	}
	value.EmailSubjects = maps.Clone(value.EmailSubjects)
	value.SMSBodies = maps.Clone(value.SMSBodies)
	return value
}

// OAuthConfig selects providers, callback destinations, and identity trust policy.
type OAuthConfig struct {
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
	// set. (env: AUTH_OAUTH_CALLBACK_BASE)
	OAuthCallbackBase string `env:"AUTH_OAUTH_CALLBACK_BASE"`
	// OAuthNativeRedirectURIs opts in to native JSON routes. Every URI must be
	// registered with the provider and is matched exactly (including loopback port).
	OAuthNativeRedirectURIs []string
	// TrustOAuthEmail is host policy for new email-based registration/adoption.
	// The provider must also assert EmailVerified. Nil denies those paths; login
	// through an existing provider-ID link and explicit session-gated linking work.
	TrustOAuthEmail func(provider string, identity oauth.UserInfo) bool
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
	// Passwordless enables login-only passwordless authentication for the listed
	// identifier kinds (auth v3 §4.2). Empty (default) → the passwordless routes are
	// NOT registered (deny-by-absence — there is no natural nil collaborator, so the
	// knob is explicit). Allowed v3 kinds are "email" and "phone" (sdk.AddressKindEmail
	// / KindPhone); any other value is ErrPasswordlessKindInvalid at construction.
	// Each listed kind must have a wired delivery channel — email via the required
	// Mailer, phone via BodySenders["phone"] — else
	// ErrPasswordlessKindUnsupported; in production the wired transport must also be
	// production-capable. Enabling passwordless requires the atomic challenge rail
	// (Repositories.Challenges + ChallengeProtector) and a delivery runtime — a
	// DeliveryMode of "jobs" (DeliveryDispatcher) or "in_process" — since starts
	// issue challenges and enqueue asynchronously (V14), plus a valid
	// PublicAuthBaseURL for magic links (HTTPS in production). Listing a kind permits its active verified login-enabled
	// identifiers as direct methods under the §5.6 credential policy. It NEVER
	// auto-provisions and NEVER enables phone+password login (phone stays
	// passwordless-only, V10). (env: AUTH_PASSWORDLESS, comma-separated)
	Passwordless []string `env:"AUTH_PASSWORDLESS"`
	// PasswordlessProvisionOnRedeem enables account creation from an EMAIL MAGIC
	// LINK sent to an address with no account — created only when the link is
	// successfully CONSUMED, never when it is sent (CHAU-6.1).
	//
	// The zero value is the historical login-only behavior: a link to an unknown
	// address delivers nothing, and nothing is created. Turning it on is a real
	// security decision, so read the README's threat model before you do.
	//
	// Scope is deliberately narrow. It applies to the EMAIL LINK rail only — never
	// phone, never OTP codes, never OAuth, never an arbitrary identifier kind —
	// because a link is the only one of those whose delivery to an address is
	// itself proof of possession.
	//
	// Enabling it REQUIRES, and fails loudly at construction otherwise
	// (ErrPasswordlessProvisionWiring): the email passwordless kind enabled, the
	// atomic challenge rail, a ChallengeProtector, an IdentifierKeyer (the stable
	// PII-free subject key an unknown address is keyed under), a delivery runtime,
	// a valid PublicAuthBaseURL, Repositories.Passwordless, and
	// Repositories.ActiveSessions.
	//
	// The intent is captured in the link's binding AT ISSUE: flipping this flag
	// does not change what an already-mailed link does.
	PasswordlessProvisionOnRedeem bool `env:"AUTH_PASSWORDLESS_PROVISION_ON_REDEEM"`
}

// WithPasswordless replaces the complete PasswordlessConfig group, including zero values.
func WithPasswordless(value PasswordlessConfig) Option {
	value = clonePasswordlessConfig(value)
	return func(c *constructorConfig) {
		value := clonePasswordlessConfig(value)
		c.Passwordless = value.Passwordless
		c.PasswordlessProvisionOnRedeem = value.PasswordlessProvisionOnRedeem
	}
}

func clonePasswordlessConfig(value PasswordlessConfig) PasswordlessConfig {
	value.Passwordless = slices.Clone(value.Passwordless)
	return value
}

// LinksConfig sets authentication landing URLs and permitted redirect destinations.
type LinksConfig struct {
	// PublicAuthBaseURL is the absolute base URL magic links and redemption pages
	// are built from (auth v3 §6.4). REQUIRED when a link flow is enabled
	// (phase 7, ErrPublicAuthBaseURLRequired); production requires HTTPS. Request
	// Host/forwarded headers never participate.
	PublicAuthBaseURL string `env:"AUTH_PUBLIC_BASE_URL"`
	// PasswordResetURL is the absolute public landing route a password-reset email
	// links to, BEFORE the token query parameter is appended (CHAU-5.1). It is
	// deliberately a SEPARATE field from PublicAuthBaseURL: that one is the full
	// passwordless landing URL (e.g. ".../login/link"), not an application origin,
	// so a reset route cannot be derived from it without guessing.
	//
	// Validation at construction: an absolute http(s) URL with a host; HTTPS is
	// REQUIRED in production RuntimeMode (ErrPasswordResetURLInsecure) because the
	// link carries a single-use credential; a fragment is rejected
	// (ErrPasswordResetURLInvalid) because the builder appends a QUERY parameter and
	// a fragment would silently swallow it; and an existing `token` query parameter
	// is rejected so the builder never overwrites ambiguous host input. Other
	// non-secret query parameters are preserved.
	//
	// Compatibility posture (ratified CHAU-5.1):
	//
	//   - PRODUCTION: this field is REQUIRED whenever the challenge-backed
	//     forgot/reset rail is wired — raw-token-only reset mail is no longer an
	//     acceptable production experience (ErrPasswordResetURLRequired).
	//   - DEVELOPMENT: empty is permitted with ONE startup WARN, and reset mail
	//     falls back to the historical raw-token template so console and local
	//     flows keep working during migration.
	//
	// Once a URL is present, ALL modes render link-only mail. Request Host,
	// Forwarded, and X-Forwarded-* headers never participate: the link is built from
	// this configured value alone.
	PasswordResetURL string `env:"AUTH_PASSWORD_RESET_URL"`
	// OAuthLinkBaseURL is the absolute SPA landing URL the anti-takeover OAuth
	// pending-link email links to, BEFORE the "#token=<token>" fragment is appended
	// (oauth-pending-link plan D1/D2). It follows the same "separate full-URL field
	// per link type" precedent as PasswordResetURL, and is deliberately NOT a reuse
	// of PublicAuthBaseURL: that landing route POSTs magic-link redeem, while this
	// one POSTs the pending-link verify-link. The host supplies a full landing URL
	// (no fragment — the token owns the fragment) and wires the SPA route.
	//
	// Validation at construction (D5): EMPTY is allowed — the pocket degrades to the
	// historical bare-token email line rather than failing to boot; when OAuth
	// providers are wired but this field is empty, ONE startup WARN names
	// AUTH_OAUTH_LINK_URL. A NON-EMPTY value is validated in every mode: it must be an
	// absolute http(s) URL with a host and NO fragment (a fragment would swallow the
	// appended "#token="), and HTTPS is REQUIRED in production RuntimeMode
	// (ErrOAuthLinkURLInsecure) because the link carries a single-use credential.
	// Existing non-secret query parameters are preserved. Unlike PasswordResetURL this
	// is never a production boot requirement: it changes presentation, not the
	// anti-takeover guarantee (the emailed secret remains proof of inbox control).
	//
	// The token rides the URL FRAGMENT (mirroring the magic link), so it never
	// reaches the server on the landing-page GET (verify-link is POST-only) and the
	// landing page can scrub it from history. Request Host/forwarded headers never
	// participate: the link is built from this configured value alone.
	OAuthLinkBaseURL string `env:"AUTH_OAUTH_LINK_URL"`
	// RedirectAllowlist is the exact-match allowlist of ABSOLUTE post-flow
	// redirect destinations (open-redirect guard). The same-origin default ("/")
	// is always allowed, and the browser lanes (OAuth flows and HTML form
	// return-to) honor any safe same-origin relative path without allowlisting —
	// a relative path is never an off-site vector. Any other target must appear
	// verbatim here or it falls back to "/". The invitation lane resolves its
	// mailed-link destination against this list only (exact match, no relative
	// pass): a relative path is not a meaningful target inside an email.
	// (env: AUTH_REDIRECT_ALLOWLIST, comma-separated)
	RedirectAllowlist []string `env:"AUTH_REDIRECT_ALLOWLIST"`
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

// BrowserConfig configures browser routes, cookies, origins, and views.
type BrowserConfig struct {
	// SessionCookie configures the session cookie; the zero value is usable.
	SessionCookie inbound.CookieConfig
	// RefreshCookiePath scopes the refresh cookie. Empty (default) → "/auth", which
	// covers /auth/refresh AND /auth/logout on a host that mounts the pocket at the
	// root. A host that mounts the pocket under a path prefix (pockets.PrefixRegistrar,
	// e.g. "/api/v1") MUST set the FULL prefixed path — "/api/v1/auth" — or the browser
	// never sends the refresh cookie to the endpoints that need it and cookie-driven
	// refresh silently dies. The registrar deliberately exposes registration, not its
	// mount prefix, so the pocket cannot derive this.
	//
	// A non-empty value must be a valid absolute cookie path (leading "/", no query,
	// fragment, control, or header-delimiter character, no trailing slash except "/"
	// itself) or construction fails with ErrRefreshCookiePathInvalid. The SAME resolved
	// path is used for every refresh-cookie issue (login, rotation) and deletion
	// (logout). It configures ONLY the refresh cookie: the access cookie keeps
	// SessionCookie.Path and both cookies keep the SameSite=Lax posture.
	RefreshCookiePath string `env:"AUTH_REFRESH_COOKIE_PATH"`
	// AllowedOrigins is the exact-match Origin allowlist that the browser-safe
	// mutation gate on cookie-authenticated sensitive routes (step-up, credential and
	// identifier management) validates against (design §9.1). A "*" entry never
	// authorizes a credentialed cross-origin mutation. Empty leaves the gate to
	// reject every cross-site cookie mutation and any request carrying a
	// disallowed Origin; bearer-only (API) callers skip the gate entirely.
	// (env: AUTH_ALLOWED_ORIGINS, comma-separated)
	AllowedOrigins []string `env:"AUTH_ALLOWED_ORIGINS"`
	// BrowserLoginPath is the login destination the browser identity gates
	// (any authenticator carrying Browser()) 303 to on an
	// authentication denial (design §9.2). Empty (default) → "/auth/login". A non-empty
	// value MUST be a safe root-relative path (leading "/", no "//" prefix, no scheme,
	// no backslash, no control character) or construction fails with
	// ErrBrowserLoginPathInvalid — so a browser gate can never be pointed off-site. It
	// configures ONLY the Browser() denial mode: an authenticator without it keeps
	// its byte-stable JSON 401 regardless.
	BrowserLoginPath string `env:"AUTH_BROWSER_LOGIN_PATH"`
	// BundledRouteAuth overrides the AUTHENTICATION posture of the bundled route
	// groups, one semantic surface at a time. Every zero field keeps that group's
	// audited default (see BundledRouteAuthentication), so a host replaces one
	// group without restating the others and without a half-constructed Service:
	// NewService resolves each configured strategy into a concrete middleware and
	// holds the resolved set immutable for the process's life.
	//
	// It is authentication only. It cannot unmount the authenticator from a
	// protected bundled surface, and it never substitutes for the host
	// authorization seams (MachineRoutesGate, UserAdminCheck, InviteCheck).
	BundledRouteAuth inbound.BundledRouteAuthentication
	// Views is the OPTIONAL HTML rendering port (design §9.2, R12/V16). Nil (default)
	// → the HTML surface is absent: the HTML GET pages and form decoding are NOT
	// registered and the shared POST routes accept JSON only, so the pocket is
	// API-only with no view technology in the host's module graph. Non-nil → the
	// bundled/overridden HTML GET pages mount alongside the UNCHANGED JSON API (the
	// JSON DTO/status/body/cookie contracts are byte-compatible either way). The
	// pocket core never imports templ: this is a technology-neutral web.Renderer
	// port. The bundled default lives in the sibling module
	// pockets/authentication/views/templ (authtempl.New()); the blessed override
	// path is embedding that default and overriding individual methods. A host may
	// instead satisfy the port with html/template via sdk/pkg/web.Template.
	Views inbound.Views
	// HTMLPolicy is the OPTIONAL, technology-neutral HTML resource policy (design
	// §9.2, GOTH-0.4). Nil (default) → the historical asset-free CSP: every auth HTML
	// page and redirect keeps script-src nonce-only with no external asset origins,
	// the secure default. Non-nil → the SAME fixed protections plus the policy's
	// validated widening resource directives (script/style/image/font/connect/media/
	// worker), so a selected HTML view can load the styles, scripts, fonts, and images
	// it declares. A policy only WIDENS; it can never remove a fixed protection
	// (no-store, no-referrer, X-Frame-Options: DENY, X-Content-Type-Options: nosniff,
	// default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none').
	// Build it with NewHTMLResourcePolicy (validated at construction); the ui/goth
	// authentication adapter maps goth.Bundle.Requirements() into one. Setting
	// HTMLPolicy while Views is nil is ErrHTMLPolicyWithoutViews at construction — a
	// policy for an absent HTML surface is contradictory wiring, never a silent no-op.
	// The pocket core never imports templ or ui/goth: HTMLResourcePolicy is a plain
	// pocket-owned value.
	HTMLPolicy *inbound.HTMLResourcePolicy
}

// WithBrowser replaces the complete BrowserConfig group, including zero values.
func WithBrowser(value BrowserConfig) Option {
	value = cloneBrowserConfig(value)
	return func(c *constructorConfig) {
		value := cloneBrowserConfig(value)
		c.SessionCookie = value.SessionCookie
		c.RefreshCookiePath = value.RefreshCookiePath
		c.AllowedOrigins = value.AllowedOrigins
		c.BrowserLoginPath = value.BrowserLoginPath
		c.BundledRouteAuth = value.BundledRouteAuth
		c.Views = value.Views
		c.HTMLPolicy = value.HTMLPolicy
	}
}

func cloneBrowserConfig(value BrowserConfig) BrowserConfig {
	value.AllowedOrigins = slices.Clone(value.AllowedOrigins)
	if value.HTMLPolicy != nil {
		copy := *value.HTMLPolicy
		value.HTMLPolicy = &copy
	}
	return value
}

// InvitationsConfig supplies invitation grants and host authorization checks.
type InvitationsConfig struct {
	// Granter is the ReBAC-decoupled grant-on-accept seam for invitations (design
	// §6, ratified AV4). Nil → the invitation subsystem is OFF and its routes are
	// NOT registered (deny-by-absence); Repositories.Invitations may then be nil.
	// Non-nil → Repositories.Invitations is required (ErrInvitationRepoRequired),
	// and verified-address resolution completes pending auto-accept invitations.
	Granter invitations.Granter
	// InviteCheck is the relation-aware host authorization seam the pocket's
	// AUTHORIZED invitation operations pose — the ones the shipped create/list routes
	// drive — after live-session validation, principal resolution, request parsing,
	// metadata validation, identifier normalization, and the invitee lookup, and
	// before any row exists or a grant is attempted (design §6/D3). It is REQUIRED
	// whenever Granter enables invitations — nil → ErrInviteCheckRequired at
	// construction, never an allow-by-default. Wiring it without a Granter
	// (invitations off) is the contradictory ErrInviteCheckWithoutGranter. A nil
	// return authorizes; a denial or infrastructure error fails closed through the
	// normal web/sdk mapping.
	InviteCheck invitations.InviteCheck
	// MemberCheck is the optional duplicate-membership predicate for the direct-add
	// path (known invitee + AutoAccept). Nil → no dup check (idempotent grants
	// absorb duplicates). Meaningful only when Granter is wired.
	MemberCheck invitations.MemberCheck
}

// WithInvitations replaces the complete InvitationsConfig group, including zero values.
func WithInvitations(value InvitationsConfig) Option {
	return func(c *constructorConfig) {
		c.Granter = value.Granter
		c.InviteCheck = value.InviteCheck
		c.MemberCheck = value.MemberCheck
	}
}

// AdministrationConfig controls access to bundled administration routes.
type AdministrationConfig struct {
	// MachineRoutesGate is the authorization the bundled machine-identity lifecycle
	// routes (/auth/service-accounts*, /auth/api-keys/{id}/revoke) run behind. Each
	// route runs the BundledRouteAuth.MachineLifecycle authenticator — by default
	// RequireAccessTokenLive(): human credential only (an API key, act-as-user or
	// not, never administers machine identities through the bundled routes) at the
	// immediate-revocation tier (the invitation precedent) — then, on the three
	// MUTATIONS, the browser-safe Origin/CSRF gate, then this gate.
	// The pocket never guesses a policy: nil → the routes are NOT mounted
	// (deny-by-absence, like PasswordFlowsDisabled) and NewService WARNs when the
	// machine repositories are wired without one; key AUTHENTICATION is unaffected.
	// Set with Repositories.ServiceAccounts / APIKeys nil → ErrMachineRoutesGateWithoutRepos.
	// A single middleware, not the []web.Middleware of cms.AdminMiddleware /
	// events.StreamMiddleware: nil is the unambiguous "no policy" — an empty
	// non-nil slice would mean "mounted, ungated", the very bug this field closes.
	// Typical: authorizer.RequirePermissionFixed("platform", "steward", "global").
	MachineRoutesGate web.Middleware
	// UserAdminCheck is the host authorization seam for user administration
	// (CHAU-1.1), and the switch that MOUNTS the bundled admin routes.
	//
	// Nil (default) → the bundled admin routes are NOT registered, even when
	// Repositories.UserAdmin is wired. This is deliberate: a store adapter returns
	// a complete Repositories bundle, and only an explicit host authorization
	// decision turns that capability into an HTTP surface. The trusted service
	// methods (ListUsers/GetUserSummary/DeactivateUser/ReactivateUser) remain
	// available whenever the repository is wired — their caller owns authorization.
	//
	// Non-nil → GET /auth/admin/users, GET /auth/admin/users/{id}, and the
	// deactivate/reactivate mutations mount, each gated on a live session, the
	// browser-safe-mutation Origin/CSRF gate (mutations only), and this check. It
	// then REQUIRES both Repositories.UserAdmin and Repositories.ActiveSessions —
	// missing either is ErrUserAdminReposRequired at construction, because a
	// deactivate button without the fenced session mint would advertise a
	// revocation a concurrent login could defeat.
	//
	// A denial or an infrastructure error both fail closed. The resolved Principal
	// reaches the check verbatim, including a machine principal: whether a service
	// account may administer users is the host's decision, not the pocket's.
	UserAdminCheck authlogic.UserAdminCheck
	// ListStrategy is the DEFAULT pagination strategy the pocket's JSON list
	// endpoints (service accounts, API keys, invitations) apply when a request
	// names neither a cursor nor an offset param (sdk/pkg/list ParseListRequest).
	// "cursor" (the default) or "offset"; empty is treated as "cursor". A host
	// populates it from an env-tagged config field
	// (`env:"AUTH_LIST_STRATEGY" default:"cursor"` via sdk/config ParseEnvTags),
	// never from os.Getenv inside the pocket. Any other value is
	// ErrInvalidListStrategy at construction (the loud-constructorConfig posture).
	ListStrategy string `env:"AUTH_LIST_STRATEGY" default:"cursor"`
}

// WithAdministration replaces the complete AdministrationConfig group, including zero values.
func WithAdministration(value AdministrationConfig) Option {
	return func(c *constructorConfig) {
		c.MachineRoutesGate = value.MachineRoutesGate
		c.UserAdminCheck = value.UserAdminCheck
		c.ListStrategy = value.ListStrategy
	}
}

// WithIDs replaces IDs for this constructor.
func WithIDs(value sdk.IDGenerator) Option {
	return func(c *constructorConfig) {
		c.IDs = value
	}
}

// WithLogger supplies the logger; nil selects slog.Default().
func WithLogger(value *slog.Logger) Option {
	return func(c *constructorConfig) {
		c.Logger = value
	}
}
