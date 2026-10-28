package authentication

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The single-factor causes checkPosture names: login sources that cannot, or
// are not asked to, produce AAL2.
const (
	causeNativePassword = "native_password"
	causePasswordless   = "passwordless"
	causeNativeOAuth    = "native_oauth"
	causeExternalNoMFA  = "external_no_mfa"
)

// Runtime-mode and delivery-transport construction errors. These fire NOW, at
// New, because RuntimeMode and the delivery transports are core
// collaborators the pocket already carries.
var (
	// ErrRuntimeModeRequired is returned when runtimeMode is empty. The
	// mode has no default so a host cannot accidentally ship the dev posture. It
	// WRAPS the canonical environment.ErrModeRequired (CHAU-3.3), so
	// errors.Is matches this sentinel and the sdk one; only the message gained a
	// canonical-error suffix.
	ErrRuntimeModeRequired = fmt.Errorf(`auth: runtimeMode is required ("development" or "production"): %w`, environment.ErrModeRequired)
	// ErrRuntimeModeInvalid is returned when runtimeMode is a value other
	// than "development" or "production". It WRAPS the canonical
	// environment.ErrModeInvalid (CHAU-3.3) on the same terms as
	// ErrRuntimeModeRequired.
	ErrRuntimeModeInvalid = fmt.Errorf(`auth: runtimeMode must be "development" or "production": %w`, environment.ErrModeInvalid)
	// ErrInsecureDeliveryTransport is returned in production RuntimeMode when a
	// wired email Sender or BodySender is development-only or declares no capability
	// metadata (design §6.3): a console transport leaks OTPs and magic links to
	// logs, and an undeclared transport cannot be proven safe. It is also returned
	// for a transport declaring opportunistic STARTTLS unless
	// DeliveryConfig.OpportunisticTLSAcknowledged is set; that error also wraps
	// notify.ErrOpportunisticTLS.
	//
	// The verdict itself is made by the capability that owns the port —
	// notify.CheckTransport — so an app-wide
	// mailer enforces the identical rule without importing this pocket. The
	// returned error wraps BOTH this sentinel and the capability's
	// notify.ErrInsecureTransport, so existing
	// errors.Is checks keep matching and new sdk-only code can match the
	// capability sentinel.
	ErrInsecureDeliveryTransport = errors.New("auth: production RuntimeMode rejects a development-only, metadata-less or opportunistic-TLS delivery transport")
	// ErrDeliveryModeRequired is returned when deliveryMode is empty. The mode
	// has no default (the RuntimeMode precedent) so a host explicitly selects the
	// outbound-delivery execution model and never inherits one from a non-nil
	// collaborator (authv3-delivery-refactor AV3D-0.1).
	ErrDeliveryModeRequired = errors.New(`auth: deliveryMode is required ("off", "in_process", or "jobs")`)
	// ErrDeliveryModeInvalid is returned when deliveryMode is a value other
	// than "off", "in_process", or "jobs".
	ErrDeliveryModeInvalid = errors.New(`auth: deliveryMode must be "off", "in_process", or "jobs"`)
	// ErrDeliveryOffButDeliverable is returned when deliveryMode is "off" yet a
	// delivery capability is wired (DeliveryConfig.DeliveryDispatcher). off declares that no
	// configured capability can send, so a wired delivery dispatcher is a contradiction —
	// the host must select "jobs" or "in_process", or remove the dispatcher.
	ErrDeliveryOffButDeliverable = errors.New(`auth: DeliveryMode "off" selected but a delivery dispatcher is wired (DeliveryConfig.DeliveryDispatcher) — a configured flow could deliver`)
	// ErrDeliveryQueueRequired is returned when deliveryMode is "jobs" but no
	// delivery queue capability is wired (DeliveryConfig.DeliveryDispatcher is nil). Durable
	// jobs delivery cannot run without the generic-jobs dispatcher.
	ErrDeliveryQueueRequired = errors.New(`auth: DeliveryMode "jobs" requires a wired delivery dispatcher (DeliveryConfig.DeliveryDispatcher)`)
	// ErrDeliveryJobsUnacknowledged is returned in production RuntimeMode when
	// deliveryMode is "jobs" but DeliveryConfig.DeliveryJobsAcknowledged is false. The
	// outbox is the only send path, so a production host that enqueues without running
	// the durable jobs delivery runtime would silently never deliver. The pocket
	// cannot observe the host's process lifecycle, so it requires an explicit
	// affirmation that the runtime is run rather than failing open on a stalled queue.
	ErrDeliveryJobsUnacknowledged = errors.New(`auth: production RuntimeMode with DeliveryMode "jobs" requires DeliveryConfig.DeliveryJobsAcknowledged (the host must run the durable jobs delivery runtime)`)
	// ErrDeliveryEphemeralUnacknowledged is returned in production RuntimeMode when
	// deliveryMode is "in_process" but DeliveryConfig.DeliveryEphemeralAcknowledged is
	// false. in_process delivery is process-local and loses accepted, in-flight work on
	// a crash; production must not run an ephemeral send path without the host
	// explicitly accepting that crash-loss (the recommended production posture is
	// "jobs").
	ErrDeliveryEphemeralUnacknowledged = errors.New(`auth: production RuntimeMode with DeliveryMode "in_process" requires DeliveryConfig.DeliveryEphemeralAcknowledged (ephemeral in-process delivery loses in-flight work on crash)`)
	// ErrAuditRequired is returned in production RuntimeMode when
	// Repositories.SecurityEvents is nil: without the rail no sign-in, credential
	// change or denial is recorded. WithAuditDisabledAcknowledged() waives it.
	ErrAuditRequired = errors.New("auth: production RuntimeMode requires Repositories.SecurityEvents (the security-event audit rail); to run without it pass WithAuditDisabledAcknowledged()")
	// ErrSingleFactorUnacknowledged is returned in production RuntimeMode when the
	// host admits single-factor sign-in without IdentityConfig.SingleFactorAcknowledged.
	// The causes are password flows on (native_password), any Passwordless kind
	// (passwordless), any OAuth provider (native_oauth), and an External provider
	// without RequireMFA while MFAConfig.Required is none (external_no_mfa); the
	// refusal names the ones present. A passkey is a second factor only where a
	// gate requires AAL2 — native password, passwordless and OAuth login still
	// sign in at AAL1 — so production asks the host to accept one-factor sign-in
	// explicitly, to require MFA of its external users (WithMFA), or to turn
	// native login off.
	// It is the logic package's sentinel, re-exported here, so one errors.Is
	// target serves New and NewExternalAuthenticator.
	ErrSingleFactorUnacknowledged = authlogic.ErrSingleFactorUnacknowledged
	// ErrMFAUnsatisfiable is returned in every mode when MFAConfig.Required or
	// MFAConfig.Methods cannot be met by the wiring: a Methods source that is not
	// wired, admins without WithPasskeys, WithTOTP or WithExternal (the AAL2
	// sources) or without AdministrationConfig.UserAdminCheck (it would gate
	// nothing); all without WithExternal, or beside native password,
	// passwordless, OAuth or passkey login, or beside WithPasskeys or WithTOTP
	// (native all is the follow-on s4c2-native-all). The error names the field
	// to change.
	ErrMFAUnsatisfiable = errors.New("auth: MFAConfig.Required cannot be satisfied by this wiring")
	// ErrPasskeyReposRequired is returned when WithPasskeys wires a relying party
	// without Repositories.Passkeys, CredentialMutations, AuthenticationGrants,
	// Challenges or a ChallengeProtector. It is the logic package's sentinel,
	// re-exported here.
	ErrPasskeyReposRequired = authlogic.ErrPasskeyReposRequired
	// ErrTOTPReposRequired is returned when WithTOTP wires an encrypter without
	// Repositories.TOTP, CredentialMutations, AuthenticationGrants or a
	// ChallengeProtector.
	ErrTOTPReposRequired = authlogic.ErrTOTPReposRequired
	// ErrTOTPSchemaMissing is returned when WithTOTP is wired and
	// Repositories.TOTP reports its schema missing (apply migration 0026). A
	// host that does not wire TOTP is never probed.
	ErrTOTPSchemaMissing = authlogic.ErrTOTPSchemaMissing
	// ErrPasskeySchemaMissing is returned when WithPasskeys is wired and
	// Repositories.Passkeys reports its schema missing (apply migration
	// 0025_passkey_credentials.sql). A host that does not wire passkeys is never
	// probed.
	ErrPasskeySchemaMissing = authlogic.ErrPasskeySchemaMissing
	// ErrPasskeyDevelopmentRelyingParty is returned in production RuntimeMode for
	// a WebAuthn relying party whose Config().RPID is localhost or an IP address,
	// or whose Config().Origins admits an http namespace: a passkey registered
	// there is bound to a development origin. There is no acknowledgement.
	ErrPasskeyDevelopmentRelyingParty = errors.New("auth: production refuses a passkey relying party with a localhost or IP RP ID, or an http origin namespace")
	// ErrMFARequired denies a credential below AAL2 where multi-factor
	// authentication is required. It is the logic package's sentinel,
	// re-exported here, and matches sdk.ErrUnauthorized.
	ErrMFARequired = authlogic.ErrMFARequired
	// ErrDevelopmentOnlyProvider is returned in production RuntimeMode for an
	// OAuth provider or an external identity provider that declares itself
	// development-only (oauth.Capabilities or the authentication capability's
	// Capabilities with DevelopmentOnly). It is the logic package's sentinel,
	// re-exported here; the error also wraps the capability's own sentinel.
	// Providers that declare nothing are accepted. There is no acknowledgement.
	ErrDevelopmentOnlyProvider = authlogic.ErrDevelopmentOnlyProvider
	// ErrNonDurableRateLimiter is returned in production RuntimeMode when the wired
	// (or defaulted) rate limiter is in-process-only (design §4.4/§8): the bundled
	// ratelimiter.Memory default, or a limiter that declares InProcessOnly through
	// RateLimiterDurabilityReporter. An in-process limiter enforces a per-process
	// budget only, so a multi-instance deployment gets N× the intended login/limit
	// budget — a shared/durable limiter is required. A limiter that does not identify
	// as in-process-only is tolerated ("where metadata can identify it" — a durable
	// store is not asked to prove a negative). Development permits an in-process
	// limiter with a startup WARN.
	ErrNonDurableRateLimiter = errors.New("auth: production RuntimeMode requires a shared/durable rate limiter (the in-process ratelimiter.Memory enforces only a per-process budget)")
)

// Stable required-collaborator errors for the v3 security seams. The constructorConfig
// slots below are frozen now; each error is returned by the phase that enables
// its subsystem (challenges → phase 3, delivery outbox → phase 4, PII-free
// limits → phase 5, link flows → phase 7), per the design's "validated only when
// their subsystem becomes enabled" rule. Defining them here keeps the vocabulary
// stable across phases.
var (
	// ErrChallengeProtectorRequired is returned when the challenge subsystem is
	// enabled without a IdentityConfig.ChallengeProtector (design §3.3).
	ErrChallengeProtectorRequired = errors.New("auth: IdentityConfig.ChallengeProtector is required")
	// ErrIdentifierKeyerRequired is returned in production when PII-free rate
	// limiting is enabled without a IdentityConfig.IdentifierKeyer (design §4.4).
	ErrIdentifierKeyerRequired = errors.New("auth: IdentityConfig.IdentifierKeyer is required in production")
	// ErrDeliveryEncrypterRequired is returned when the delivery outbox is
	// enabled without a DeliveryConfig.DeliveryEncrypter (design §6.1.1).
	ErrDeliveryEncrypterRequired = errors.New("auth: DeliveryConfig.DeliveryEncrypter is required")
	// ErrPublicAuthBaseURLRequired is returned when a link flow is enabled
	// without a LinksConfig.PublicAuthBaseURL (design §6.4).
	ErrPublicAuthBaseURLRequired = errors.New("auth: LinksConfig.PublicAuthBaseURL is required when a link flow is enabled")
	// ErrPublicAuthBaseURLInvalid is returned when a link flow is enabled with a
	// LinksConfig.PublicAuthBaseURL that is not a valid absolute http(s) URL (design
	// §6.4): magic links are built from it, never from a request Host, so it must be
	// a well-formed absolute base at construction.
	ErrPublicAuthBaseURLInvalid = errors.New("auth: LinksConfig.PublicAuthBaseURL must be a valid absolute http(s) URL")
	// ErrPublicAuthBaseURLInsecure is returned in production RuntimeMode when
	// LinksConfig.PublicAuthBaseURL is not HTTPS (design §6.4): a magic link over plain
	// HTTP exposes the single-use token in transit.
	ErrPublicAuthBaseURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS LinksConfig.PublicAuthBaseURL")
	// ErrPasswordResetURLRequired is returned in production RuntimeMode when the
	// challenge-backed forgot/reset rail is wired without a LinksConfig.PasswordResetURL
	// (CHAU-5.1). Reset mail that prints only a raw token is not an acceptable
	// production experience, so the omission fails at construction rather than
	// degrading silently. Development permits it with a startup WARN.
	ErrPasswordResetURLRequired = errors.New("auth: production RuntimeMode requires LinksConfig.PasswordResetURL when the password-reset rail is wired")
	// ErrPasswordResetURLInvalid is returned when LinksConfig.PasswordResetURL is not a
	// valid absolute http(s) URL with a host, carries a FRAGMENT (the builder
	// appends a query parameter, which a fragment would swallow), or already carries
	// a `token` query parameter (the builder must never overwrite ambiguous host
	// input).
	ErrPasswordResetURLInvalid = errors.New("auth: LinksConfig.PasswordResetURL must be a valid absolute http(s) URL with no fragment and no token query parameter")
	// ErrPasswordResetURLInsecure is returned in production RuntimeMode when
	// LinksConfig.PasswordResetURL is not HTTPS: the link carries a single-use
	// credential, and plain HTTP exposes it in transit.
	ErrPasswordResetURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS LinksConfig.PasswordResetURL")
	// ErrOAuthLinkURLInvalid is returned when a non-empty LinksConfig.OAuthLinkBaseURL is
	// not a valid absolute http(s) URL with a host, or carries a FRAGMENT (the
	// builder appends "#token=<token>", which an existing fragment would swallow —
	// the token owns the fragment). Empty is allowed (the caller degrades to the
	// bare-token email line), so this covers only the shape of a non-empty value.
	ErrOAuthLinkURLInvalid = errors.New("auth: LinksConfig.OAuthLinkBaseURL must be a valid absolute http(s) URL with no fragment")
	// ErrOAuthLinkURLInsecure is returned in production RuntimeMode when a non-empty
	// LinksConfig.OAuthLinkBaseURL is not HTTPS: the pending-link URL carries a single-use
	// credential in its fragment, and plain HTTP exposes it in transit.
	ErrOAuthLinkURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS LinksConfig.OAuthLinkBaseURL")
	// ErrInvitationURLRequired is returned in production RuntimeMode when
	// invitations are wired (a Granter) without a LinksConfig.InvitationURL: the
	// link would fall back to the relative "/#token=", which a mail client
	// cannot open. Set AUTH_INVITATION_URL to the landing page that reads the
	// fragment. Development permits it with a startup WARN.
	ErrInvitationURLRequired = errors.New("auth: production RuntimeMode requires LinksConfig.InvitationURL (AUTH_INVITATION_URL) when invitations are wired: set it to the absolute landing page that reads the #token fragment")
	// ErrInvitationURLInvalid is returned when a non-empty LinksConfig.InvitationURL
	// is not a valid absolute http(s) URL with a host and no userinfo, or carries a
	// FRAGMENT (the builder appends "#token=<token>").
	ErrInvitationURLInvalid = errors.New("auth: LinksConfig.InvitationURL must be a valid absolute http(s) URL with no fragment")
	// ErrInvitationURLInsecure is returned in production RuntimeMode when
	// LinksConfig.InvitationURL is not HTTPS: the link carries a single-use
	// credential in its fragment.
	ErrInvitationURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS LinksConfig.InvitationURL")
)

// Passwordless enablement construction errors (design §4.2/§8). PasswordlessConfig.Passwordless
// is deny-by-absence — empty means the passwordless routes are not registered — so
// these fire only when a host opts in by listing at least one kind. A half-wired
// passwordless configuration would strand the users it is enabled for, so every
// gap degrades LOUDLY at construction (the partial-wiring precedent).
var (
	// ErrPasswordlessKindInvalid is returned when PasswordlessConfig.Passwordless lists a kind
	// other than "email" or "phone" (the v3 kinds, design §4.2).
	ErrPasswordlessKindInvalid = errors.New(`auth: PasswordlessConfig.Passwordless kinds must be "email" or "phone"`)
	// ErrPasswordlessKindUnsupported is returned when a listed kind has no wired
	// delivery channel (design §4.2): email needs the required Mailer; phone
	// needs BodySenders["phone"]. In production the
	// wired transport must also be production-capable (validateDeliveryTransports).
	ErrPasswordlessKindUnsupported = errors.New("auth: PasswordlessConfig.Passwordless lists a kind with no wired delivery channel")
	// ErrPasswordlessChallengeRequired is returned when passwordless is enabled
	// without the atomic challenge rail wired (Repositories.Challenges): a
	// passwordless start issues a login_magic_link / login_otp challenge, so the rail
	// is required (design §4.3).
	ErrPasswordlessChallengeRequired = errors.New("auth: PasswordlessConfig.Passwordless requires Repositories.Challenges (the atomic challenge rail)")
	// ErrPasswordlessDeliveryRequired is returned when passwordless is enabled without
	// a delivery runtime (DeliveryMode "off"): passwordless starts enqueue an opaque
	// delivery command and resolve the account off the request path (design
	// §4.1/§6.1.1, V14), so a delivery runtime ("jobs" or "in_process") is required.
	ErrPasswordlessDeliveryRequired = errors.New(`auth: PasswordlessConfig.Passwordless requires a delivery runtime (DeliveryMode "jobs" or "in_process")`)
)

// validateRuntimeMode enforces the required-enum rule (design §8): empty →
// ErrRuntimeModeRequired, unknown → ErrRuntimeModeInvalid, else nil. The rule
// itself is the canonical environment.ValidateMode (CHAU-3.3); this only
// re-labels the verdict in auth's stable constructorConfig-oriented vocabulary, and both
// sentinels remain errors.Is-matchable because auth's wrap the sdk's.
func validateRuntimeMode(m environment.Mode) error {
	err := environment.ValidateMode(m)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, environment.ErrModeRequired):
		return ErrRuntimeModeRequired
	default:
		return fmt.Errorf("%w: %q", ErrRuntimeModeInvalid, m)
	}
}

// validateDeliveryMode enforces the required-enum rule for DeliveryMode
// (authv3-delivery-refactor AV3D-0.1): empty → ErrDeliveryModeRequired, unknown →
// ErrDeliveryModeInvalid, else nil. The mode-specific capability/acknowledgment
// matrix is enforced in NewService's delivery block; this is only the loud
// empty/unknown gate, mirroring validateRuntimeMode.
func validateDeliveryMode(m delivery.Mode) error {
	switch m {
	case "":
		return ErrDeliveryModeRequired
	case delivery.ModeOff, delivery.ModeInProcess, delivery.ModeJobs:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrDeliveryModeInvalid, m)
	}
}

// validateDeliveryTransports enforces the transport-security posture (design
// §6.3): in production a development-only or metadata-less email Sender or
// BodySender is ErrInsecureDeliveryTransport; in development a development-only
// transport emits a startup WARN. The Mailer is always the required email
// transport; each wired BodySender is checked too.
//
// Unless opportunisticTLSAcknowledged, production also refuses a transport
// declaring STARTTLS (notify.RequireMandatoryTLS). That refusal is the only
// waivable one, so it is returned separately as opportunistic, joined across
// transports, for New to report beside the other posture violations; any other
// refusal is err and returns at once. startTLS labels every transport declaring
// STARTTLS, whatever the verdict.
//
// The production VERDICT is delegated to the capability that owns each port —
// notify.CheckTransport — so this pocket and a
// host's app-wide mailer enforce one rule from one place. What stays here is
// auth's own vocabulary (ErrInsecureDeliveryTransport, the per-transport label)
// and auth's development WARN wording, because message text and log routing are
// composition concerns the sdk validators deliberately do not own.
func validateDeliveryTransports(mode environment.Mode, mailer email.Sender, bodySenders map[string]delivery.BodySender, opportunisticTLSAcknowledged bool, log *slog.Logger) (startTLS []string, opportunistic error, err error) {
	var opts []notify.CheckOption
	if !opportunisticTLSAcknowledged {
		opts = append(opts, notify.RequireMandatoryTLS())
	}
	type labeled struct {
		label     string
		transport any
	}
	transports := []labeled{{"email sender", mailer}}
	for _, kind := range slices.Sorted(maps.Keys(bodySenders)) {
		transports = append(transports, labeled{"body sender " + kind, bodySenders[kind]})
	}

	var refusals []error
	for _, t := range transports {
		posture, capErr := notify.CheckTransport(mode, t.transport, opts...)
		if posture.Capabilities.TransportSecurity == notify.TransportSecurityStartTLS {
			startTLS = append(startTLS, t.label)
		}
		if errors.Is(capErr, notify.ErrOpportunisticTLS) {
			refusals = append(refusals, fmt.Errorf("%w: %s declares opportunistic STARTTLS; set DeliveryConfig.OpportunisticTLSAcknowledged to accept it: %w", ErrInsecureDeliveryTransport, t.label, capErr))
			continue
		}
		if err := asInsecureTransport(capErr, posture.Declared, t.label); err != nil {
			return nil, nil, err
		}
		warnDevelopmentOnlyTransport(mode, posture.Declared, posture.Capabilities.DevelopmentOnly, t.label, log)
	}
	return startTLS, errors.Join(refusals...), nil
}

// asInsecureTransport re-labels a capability-owned production rejection in auth's
// stable vocabulary. The result wraps BOTH ErrInsecureDeliveryTransport and the
// capability sentinel, so existing host errors.Is checks and new sdk-only ones
// both match. A non-verdict error (an invalid RuntimeMode reaching the capability
// check) passes through unchanged rather than being mislabelled as a transport
// problem.
func asInsecureTransport(capErr error, declared bool, label string) error {
	if capErr == nil {
		return nil
	}
	if !errors.Is(capErr, notify.ErrInsecureTransport) {
		return capErr
	}
	if !declared {
		return fmt.Errorf("%w: %s declares no capability metadata: %w", ErrInsecureDeliveryTransport, label, capErr)
	}
	return fmt.Errorf("%w: %s is development-only: %w", ErrInsecureDeliveryTransport, label, capErr)
}

// warnDevelopmentOnlyTransport emits auth's startup WARN for a transport that
// DECLARED itself development-only while running in development. A metadata-less
// transport is deliberately not warned about here — it is rejected in production
// and silent in development, which is the behavior this pocket already shipped.
func warnDevelopmentOnlyTransport(mode environment.Mode, declared, developmentOnly bool, label string, log *slog.Logger) {
	if mode == environment.ModeDevelopment && declared && developmentOnly {
		log.Warn("auth: development-only delivery transport wired; never use in production (leaks message bodies to logs)", "transport", label)
	}
}

// validateRateLimiter enforces the shared-limiter posture (design §4.4/§8): PII-free
// login rate limiting is always active, so a multi-instance production deployment
// needs a shared/durable limiter — an in-process one enforces only a per-process
// budget (N× the intended limit). In production an in-process-only limiter is
// ErrNonDurableRateLimiter; in development it is permitted with a startup WARN. A
// limiter that does not identify as in-process-only is tolerated in both modes.
// cfgLimiter is the HOST-supplied limiter: nil means the pocket defaulted a nil
// RateLimiter to the in-process ratelimiter.Memory.
func validateRateLimiter(mode environment.Mode, cfgLimiter ratelimiter.Limiter, log *slog.Logger) error {
	if !limiterInProcessOnly(cfgLimiter) {
		return nil
	}
	switch mode {
	case environment.ModeProduction:
		return ErrNonDurableRateLimiter
	case environment.ModeDevelopment:
		log.Warn("auth: in-process rate limiter wired; its budget is per-process, so a multi-instance deployment gets N× the intended limit — wire a shared/durable limiter for production", "limiter", "in-process")
	}
	return nil
}

// limiterInProcessOnly reports whether limiter is in-process-only: the bundled
// ratelimiter.Memory (nil default or the concrete type — it is sdk-only and cannot
// declare pocket metadata), or a limiter positively declaring InProcessOnly
// through RateLimiterDurabilityReporter. Any other limiter is presumed
// shared/durable (a negative it need not prove).
func limiterInProcessOnly(limiter ratelimiter.Limiter) bool {
	if limiter == nil {
		return true // the pocket defaults a nil RateLimiter to the in-process ratelimiter.Memory
	}
	if r, ok := limiter.(authlogic.RateLimiterDurabilityReporter); ok {
		return r.RateLimiterDurability().InProcessOnly
	}
	if _, ok := limiter.(*ratelimiter.Memory); ok {
		return true
	}
	return false
}

// validatePasswordResetURL validates the reset landing URL (CHAU-5.1).
//
// Empty is handled by the CALLER, because its meaning depends on the mode:
// production requires it (ErrPasswordResetURLRequired) while development falls
// back to the legacy raw-token template with a warning. What this function owns
// is the shape of a non-empty value.
//
// The fragment and pre-existing `token` rejections are not fussiness. The builder
// appends `?token=...`, which a fragment would swallow (a token placed after a
// fragment lands INSIDE the fragment, where the SPA's query parser will not find
// it), and a host-supplied `token` parameter would be silently overwritten — both
// are failures a host would discover only from a broken reset flow in production.
func validatePasswordResetURL(mode environment.Mode, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrPasswordResetURLInvalid, raw)
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: fragment present in %q", ErrPasswordResetURLInvalid, raw)
	}
	if u.Query().Has(authlogic.PasswordResetTokenParam) {
		return fmt.Errorf("%w: %q already carries a %q parameter", ErrPasswordResetURLInvalid, raw, authlogic.PasswordResetTokenParam)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if mode == environment.ModeProduction {
			return fmt.Errorf("%w: %q", ErrPasswordResetURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrPasswordResetURLInvalid, raw)
	}
}

// validatePasswordless enforces the passwordless enablement matrix (design
// §4.1/§4.2/§6.4/§8). It is a no-op while PasswordlessConfig.Passwordless is empty (the routes
// are then absent — deny-by-absence). When a host opts in, every listed kind must be
// a valid v3 kind (email/phone) with a wired delivery channel (router.Supports —
// email always, phone iff a phone-kind notifier is wired); the atomic challenge rail
// and the durable delivery outbox must be wired (a start issues a challenge and
// enqueues asynchronously, V14); and, a magic link being an always-selectable method,
// LinksConfig.PublicAuthBaseURL must be a valid absolute base (HTTPS in production, §6.4).
// The always-on production gates — a shared/durable limiter, the identifier keyer,
// and the delivery-worker acknowledgment — are validated by NewService before this
// runs, so a passwordless-enabled production host inherits them. Production-capability
// of a wired transport is enforced by validateDeliveryTransports; this check only
// requires the channel to exist. Any gap is a loud construction error rather than a
// half-wired config that would strand the users passwordless is enabled for.
func validatePasswordless(mode environment.Mode, kinds []string, router *delivery.Router, challengesWired, outboxWired bool, publicBaseURL string) error {
	if len(kinds) == 0 {
		return nil
	}
	for _, k := range kinds {
		switch k {
		case sdk.AddressKindEmail, sdk.AddressKindPhone:
		default:
			return fmt.Errorf("%w: %q", ErrPasswordlessKindInvalid, k)
		}
		if router == nil || !router.Supports(k) {
			return fmt.Errorf("%w: %q", ErrPasswordlessKindUnsupported, k)
		}
	}
	if !challengesWired {
		return ErrPasswordlessChallengeRequired
	}
	if !outboxWired {
		return ErrPasswordlessDeliveryRequired
	}
	return validatePublicAuthBaseURL(mode, publicBaseURL)
}

// validatePublicAuthBaseURL validates the magic-link base URL (design §6.4): empty →
// ErrPublicAuthBaseURLRequired, a value that is not an absolute http(s) URL with a
// host → ErrPublicAuthBaseURLInvalid, and a non-HTTPS URL in production →
// ErrPublicAuthBaseURLInsecure. Links are built from this base only, never from a
// request Host/forwarded header, so it must be well-formed at construction.
func validatePublicAuthBaseURL(mode environment.Mode, raw string) error {
	if raw == "" {
		return ErrPublicAuthBaseURLRequired
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrPublicAuthBaseURLInvalid, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if mode == environment.ModeProduction {
			return fmt.Errorf("%w: %q", ErrPublicAuthBaseURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrPublicAuthBaseURLInvalid, raw)
	}
}

// validateOAuthLinkBaseURL validates the OAuth pending-link landing URL
// (oauth-pending-link plan D2/D5).
//
// Empty is handled by the CALLER: an unset value degrades to the historical
// bare-token email line, so what this function owns is the shape of a non-empty
// value. The fragment rejection is not fussiness — the builder appends
// "#token=<token>", and an existing fragment would swallow the token (a value
// placed after a fragment lands INSIDE the fragment, where the SPA's fragment
// parser will not find the token it expects). Existing non-secret QUERY parameters
// are preserved by the builder and are not rejected here.
func validateOAuthLinkBaseURL(mode environment.Mode, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrOAuthLinkURLInvalid, raw)
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: fragment present in %q", ErrOAuthLinkURLInvalid, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if mode == environment.ModeProduction {
			return fmt.Errorf("%w: %q", ErrOAuthLinkURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrOAuthLinkURLInvalid, raw)
	}
}

// validateInvitationURL validates the invitation landing URL (R-7). Empty is
// handled by the caller; a non-empty value is an absolute http(s) URL with a
// host and no fragment, HTTPS in production.
func validateInvitationURL(mode environment.Mode, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: %q", ErrInvitationURLInvalid, raw)
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: fragment present in %q", ErrInvitationURLInvalid, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if mode == environment.ModeProduction {
			return fmt.Errorf("%w: %q", ErrInvitationURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvitationURLInvalid, raw)
	}
}

// validateOAuthConfig rejects ambiguous provider names and callback construction.
// Native URIs are exact provider-registered destinations, not redirect templates.
// Production refuses a provider declaring itself development-only
// (oauth.CheckProvider) with ErrDevelopmentOnlyProvider; development WARNs once
// per such provider on cfg.Logger.
func validateOAuthConfig(cfg constructorConfig) error {
	if err := redirect.ValidateOAuthConfig(cfg.RuntimeMode, cfg.Providers, cfg.OAuthCallbackBase, cfg.OAuthNativeRedirectURIs); err != nil {
		return err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	for _, p := range cfg.Providers {
		posture, err := oauth.CheckProvider(cfg.RuntimeMode, p)
		if errors.Is(err, oauth.ErrDevelopmentOnlyProvider) {
			return fmt.Errorf("%w: OAuth provider %q: %w", ErrDevelopmentOnlyProvider, p.Name(), err)
		}
		if err != nil {
			return err
		}
		if posture.Capabilities.DevelopmentOnly {
			log.Warn("auth: development-only OAuth provider wired; production refuses it", "provider", p.Name())
		}
	}
	return nil
}

// validateTokenAudience refuses a SessionsConfig.Audience equal to the OAuth2
// MCP or API resource: the same signer mints delegated tokens with that aud.
// The issuer collision and the whitespace rule are the logic constructor's.
func validateTokenAudience(cfg constructorConfig) error {
	if cfg.OAuth2 == nil || cfg.TokenAudience == "" {
		return nil
	}
	if cfg.TokenAudience == cfg.OAuth2.Server.MCPResource || cfg.TokenAudience == cfg.OAuth2.Server.APIResource {
		return fmt.Errorf("%w: SessionsConfig.Audience equals an OAuth2Config resource", ErrTokenAudienceCollides)
	}
	return nil
}

// logKeyPosture logs, in production only, one
// posture_warning=legacy_key_read_enabled WARN per role whose ring still
// reads pre-ring values under a legacy id, so the migration window gets
// closed. Single-key and opaque roles log nothing: rotation is a procedure,
// not a misconfiguration.
func logKeyPosture(cfg constructorConfig, roles []keyRole, log *slog.Logger) {
	if !cfg.RuntimeMode.IsProduction() {
		return
	}
	for _, r := range roles {
		if r.legacy != "" {
			log.Warn("auth: a key ring still reads values written before the ring; unset the single-key variable once the rotation window has passed", "posture_warning", "legacy_key_read_enabled", "key", r.role, "legacy_key_id", r.legacy)
		}
	}
}

// logPasswordPosture logs, in production only and while password flows are
// on, posture_warning=password_breach_check_none when no
// CompromisedPasswordChecker is wired, and posture_warning=password_hash_cost_low
// when the hasher reports (authlogic.CostReporter) a cost below
// authlogic.RecommendedHashCost. They WARN, never refuse.
func logPasswordPosture(cfg constructorConfig, log *slog.Logger) {
	if !cfg.RuntimeMode.IsProduction() || cfg.PasswordFlowsDisabled || nilDependency(cfg.Hasher) {
		return
	}
	if nilDependency(cfg.CompromisedPasswordChecker) {
		log.Warn("auth: password flows are on with no breach check; wire PasswordConfig.CompromisedPasswordChecker (the hibp integration or protection.NewBlocklistChecker)", "posture_warning", "password_breach_check_none")
	}
	if c, ok := cfg.Hasher.(authlogic.CostReporter); ok && c.Cost() < authlogic.RecommendedHashCost {
		log.Warn("auth: the password hasher's cost is below the recommendation", "posture_warning", "password_hash_cost_low", "cost", c.Cost(), "recommended", authlogic.RecommendedHashCost)
	}
}

// postureCondition is one acknowledgeable production-posture requirement.
type postureCondition struct {
	present      bool
	acknowledged bool
	flag         string
	refusal      error
	warning      string
	attrs        []any
}

// checkPosture enforces the acknowledgeable production posture: the
// security-event audit rail, single-factor sign-in, and opportunistic
// STARTTLS on a delivery transport (startTLS and opportunistic come from
// validateDeliveryTransports). Production returns every unacknowledged
// violation at once with errors.Join, each naming its acknowledgement, and logs
// one posture_waiver WARN per honored acknowledgement; development logs one WARN
// per present condition. The single-factor condition carries its causes on the
// refusal, the waiver and the WARN. An acknowledgement is inert while its
// condition is absent, except WithAuditDisabledAcknowledged() beside a wired
// SecurityEvents, which is contradictory in every mode.
func checkPosture(repos Repositories, cfg constructorConfig, startTLS []string, opportunistic error, log *slog.Logger) error {
	auditWired := !nilDependency(repos.SecurityEvents)
	if cfg.AuditDisabledAcknowledged && auditWired {
		return ErrAuditAcknowledgedWithSecurityEvents
	}
	causes := singleFactorCauses(cfg)
	conditions := []postureCondition{
		{
			present: !auditWired, acknowledged: cfg.AuditDisabledAcknowledged,
			flag: "AuditDisabledAcknowledged", refusal: ErrAuditRequired,
			warning: "auth: Repositories.SecurityEvents is nil, so the security-event audit rail records nothing; production refuses this without WithAuditDisabledAcknowledged()",
		},
		{
			present: len(causes) > 0, acknowledged: cfg.SingleFactorAcknowledged,
			flag:    "SingleFactorAcknowledged",
			refusal: singleFactorRefusal(causes),
			warning: "auth: this host admits single-factor sign-in; production refuses this without IdentityConfig.SingleFactorAcknowledged",
			attrs:   []any{"causes", causes},
		},
		{
			present: len(startTLS) > 0, acknowledged: cfg.OpportunisticTLSAcknowledged,
			flag: "OpportunisticTLSAcknowledged", refusal: opportunistic,
			warning: "auth: a delivery transport declares opportunistic STARTTLS; production refuses it without DeliveryConfig.OpportunisticTLSAcknowledged",
			attrs:   []any{"transports", startTLS},
		},
	}
	var refusals []error
	for _, c := range conditions {
		switch {
		case !c.present:
		case c.acknowledged:
			log.Warn("auth: production posture requirement waived", append([]any{"posture_waiver", c.flag, "runtime_mode", cfg.RuntimeMode.String()}, c.attrs...)...)
		case cfg.RuntimeMode.IsProduction():
			refusals = append(refusals, c.refusal)
		default:
			log.Warn(c.warning, c.attrs...)
		}
	}
	return errors.Join(refusals...)
}

// logLivenessFloor logs the one WARN for a relaxed liveness floor under the
// sensitive bundled groups: a posture_waiver line in production, a plain WARN
// in development. An enforced floor logs nothing here.
func logLivenessFloor(cfg constructorConfig, log *slog.Logger) {
	switch {
	case !cfg.LivenessFloorRelaxed:
	case cfg.RuntimeMode.IsProduction():
		log.Warn("auth: production posture requirement waived", "posture_waiver", "BundledRouteAuthLivenessFloorRelaxed", "runtime_mode", cfg.RuntimeMode.String())
	default:
		log.Warn("auth: BrowserConfig.BundledRouteAuthLivenessFloorRelaxed lowers the sensitive bundled groups' liveness floor to Stateless(): a BundledRouteAuth override's own tier governs, so a revoked session's access token passes a stateless override until it expires")
	}
}

// singleFactorCauses lists the login sources that admit single-factor (AAL1)
// sign-in: native sources cannot produce AAL2, so they count under any
// MFAConfig.Required; an external provider counts only while it is not asked
// to (no RequireMFA and Required none). Admins clears external_no_mfa because
// its UserAdminCheck always gates something.
func singleFactorCauses(cfg constructorConfig) []string {
	var causes []string
	if !cfg.PasswordFlowsDisabled {
		causes = append(causes, causeNativePassword)
	}
	if len(cfg.Passwordless) > 0 {
		causes = append(causes, causePasswordless)
	}
	if len(cfg.Providers) > 0 {
		causes = append(causes, causeNativeOAuth)
	}
	if cfg.External != nil && cfg.MFARequired == MFARequiredNone && slices.ContainsFunc(cfg.External.Providers, func(p authlogic.ExternalProvider) bool { return !p.RequireMFA }) {
		causes = append(causes, causeExternalNoMFA)
	}
	return causes
}

// singleFactorRefusal wraps ErrSingleFactorUnacknowledged with the causes it
// tripped and the field that acknowledges them.
func singleFactorRefusal(causes []string) error {
	if slices.Contains(causes, causeExternalNoMFA) {
		return fmt.Errorf("%w: set IdentityConfig.SingleFactorAcknowledged, or WithMFA for external_no_mfa (causes %v)", ErrSingleFactorUnacknowledged, causes)
	}
	return fmt.Errorf("%w: set IdentityConfig.SingleFactorAcknowledged (causes %v)", ErrSingleFactorUnacknowledged, causes)
}
