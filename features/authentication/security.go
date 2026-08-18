package authentication

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// RuntimeMode selects the feature's fail-closed posture (design §8). It is a
// REQUIRED enum: an empty value is ErrRuntimeModeRequired and an unknown value
// is ErrRuntimeModeInvalid, so a host can never accidentally inherit the
// development posture. "production" rejects development-only delivery transports
// (and, as later phases wire them, insecure public URLs/cookies, non-durable
// limiters, and missing security collaborators); "development" warns instead.
//
// Deployment posture is APPLICATION vocabulary, not authentication vocabulary:
// a host's general mailer, notifier composition, or any other capability needs
// the same development/production switch without importing this feature. The
// canonical type therefore lives in sdk/foundation/environment, and RuntimeMode
// is a TYPE ALIAS of environment.Mode
// (coordination-hub-auth-upstream CHAU-3.3). The alias keeps every existing
// host source-compatible — struct literals, env-tag parsing, and variables
// typed RuntimeMode all still compile — while new app-wide code names
// environment.Mode directly and imports no feature. The two are the same type,
// so values pass between them with no conversion.
//
// RuntimeMode is distinct from DeliveryMode: the former is the security
// posture, the latter selects the outbound-delivery execution model.
type RuntimeMode = environment.Mode

const (
	// RuntimeModeDevelopment is the local/dev posture: unsafe transports are
	// permitted with a startup WARN. It is an alias of environment.ModeDevelopment.
	RuntimeModeDevelopment = environment.ModeDevelopment
	// RuntimeModeProduction is the fail-closed posture: development-only or
	// metadata-less delivery transports are rejected at construction. It is an
	// alias of environment.ModeProduction.
	RuntimeModeProduction = environment.ModeProduction
)

// DeliveryMode is the host's EXPLICIT selection of the outbound-delivery execution
// model (authv3-delivery-refactor §"No automatic production fallback"). It is a
// REQUIRED enum with no default — construction never infers a mode from a non-nil
// collaborator, so a host cannot accidentally ship an ephemeral posture or a jobs
// posture whose runtime is never started. An empty value is ErrDeliveryModeRequired
// and an unknown value is ErrDeliveryModeInvalid.
//
//   - DeliveryModeJobs: durable delivery. The generic jobs runtime executes the
//     delivery command, with retry/status/fencing surviving restart. Requires the
//     narrow queue capability (Config.DeliveryDispatcher) and Config.DeliveryEncrypter;
//     production additionally requires Config.DeliveryJobsAcknowledged (the host runs
//     the jobs delivery runtime).
//   - DeliveryModeInProcess: bounded ephemeral delivery. The same delivery processor
//     runs behind a process-local bounded queue and fixed worker pool; accepted work
//     does NOT survive a restart. Production requires the explicit crash-loss
//     acknowledgment Config.DeliveryEphemeralAcknowledged.
//   - DeliveryModeOff: no delivery runtime. Allowed only when no configured auth
//     capability can send — a wired delivery dispatcher makes off contradictory
//     (ErrDeliveryOffButDeliverable), and enabling passwordless under off is
//     ErrPasswordlessDeliveryRequired.
//
// The host owns the runtime lifecycle in every mode: Register starts no worker.
type DeliveryMode string

const (
	// DeliveryModeOff selects no outbound-delivery runtime. It is valid only when no
	// configured capability can send (no wired delivery queue, no passwordless).
	DeliveryModeOff DeliveryMode = "off"
	// DeliveryModeInProcess selects the bounded, process-local, EPHEMERAL delivery
	// pool: accepted work is lost on a crash. Production requires the explicit
	// crash-loss acknowledgment. It is PER PROCESS with NO cross-instance coordination —
	// each instance keeps its own queue, submit-once de-duplication, and status, so two
	// instances can each render and each send the same logical delivery (a user may get
	// two messages). Use DeliveryModeJobs for a multi-instance deployment.
	DeliveryModeInProcess DeliveryMode = "in_process"
	// DeliveryModeJobs selects durable delivery on the generic jobs runtime: accepted
	// work survives a restart and retry/status are durable. The recommended production
	// posture.
	DeliveryModeJobs DeliveryMode = "jobs"
)

// Runtime-mode and delivery-transport construction errors. These fire NOW, at
// NewService/Register, because RuntimeMode and the delivery transports are core
// collaborators the feature already carries.
var (
	// ErrRuntimeModeRequired is returned when Config.RuntimeMode is empty. The
	// mode has no default so a host cannot accidentally ship the dev posture. It
	// WRAPS the canonical environment.ErrModeRequired (CHAU-3.3), so
	// errors.Is matches this sentinel and the sdk one; only the message gained a
	// canonical-error suffix.
	ErrRuntimeModeRequired = fmt.Errorf(`auth: Config.RuntimeMode is required ("development" or "production"): %w`, environment.ErrModeRequired)
	// ErrRuntimeModeInvalid is returned when Config.RuntimeMode is a value other
	// than "development" or "production". It WRAPS the canonical
	// environment.ErrModeInvalid (CHAU-3.3) on the same terms as
	// ErrRuntimeModeRequired.
	ErrRuntimeModeInvalid = fmt.Errorf(`auth: Config.RuntimeMode must be "development" or "production": %w`, environment.ErrModeInvalid)
	// ErrInsecureDeliveryTransport is returned in production RuntimeMode when a
	// wired email Sender or Notifier is development-only or declares no capability
	// metadata (design §6.3): a console transport leaks OTPs and magic links to
	// logs, and an undeclared transport cannot be proven safe.
	//
	// The verdict itself is made by the capability that owns the port —
	// email.CheckSender and notify.CheckNotifier (CHAU-3.2) — so an app-wide
	// mailer enforces the identical rule without importing this feature. The
	// returned error wraps BOTH this sentinel and the capability's
	// email.ErrInsecureTransport / notify.ErrInsecureTransport, so existing
	// errors.Is checks keep matching and new sdk-only code can match the
	// capability sentinel.
	ErrInsecureDeliveryTransport = errors.New("auth: production RuntimeMode rejects a development-only or metadata-less delivery transport")
	// ErrDeliveryModeRequired is returned when Config.DeliveryMode is empty. The mode
	// has no default (the RuntimeMode precedent) so a host explicitly selects the
	// outbound-delivery execution model and never inherits one from a non-nil
	// collaborator (authv3-delivery-refactor AV3D-0.1).
	ErrDeliveryModeRequired = errors.New(`auth: Config.DeliveryMode is required ("off", "in_process", or "jobs")`)
	// ErrDeliveryModeInvalid is returned when Config.DeliveryMode is a value other
	// than "off", "in_process", or "jobs".
	ErrDeliveryModeInvalid = errors.New(`auth: Config.DeliveryMode must be "off", "in_process", or "jobs"`)
	// ErrDeliveryOffButDeliverable is returned when Config.DeliveryMode is "off" yet a
	// delivery capability is wired (Config.DeliveryDispatcher). off declares that no
	// configured capability can send, so a wired delivery dispatcher is a contradiction —
	// the host must select "jobs" or "in_process", or remove the dispatcher.
	ErrDeliveryOffButDeliverable = errors.New(`auth: DeliveryMode "off" selected but a delivery dispatcher is wired (Config.DeliveryDispatcher) — a configured flow could deliver`)
	// ErrDeliveryQueueRequired is returned when Config.DeliveryMode is "jobs" but no
	// delivery queue capability is wired (Config.DeliveryDispatcher is nil). Durable
	// jobs delivery cannot run without the generic-jobs dispatcher.
	ErrDeliveryQueueRequired = errors.New(`auth: DeliveryMode "jobs" requires a wired delivery dispatcher (Config.DeliveryDispatcher)`)
	// ErrDeliveryJobsUnacknowledged is returned in production RuntimeMode when
	// Config.DeliveryMode is "jobs" but Config.DeliveryJobsAcknowledged is false. The
	// outbox is the only send path, so a production host that enqueues without running
	// the durable jobs delivery runtime would silently never deliver. The feature
	// cannot observe the host's process lifecycle, so it requires an explicit
	// affirmation that the runtime is run rather than failing open on a stalled queue.
	ErrDeliveryJobsUnacknowledged = errors.New(`auth: production RuntimeMode with DeliveryMode "jobs" requires Config.DeliveryJobsAcknowledged (the host must run the durable jobs delivery runtime)`)
	// ErrDeliveryEphemeralUnacknowledged is returned in production RuntimeMode when
	// Config.DeliveryMode is "in_process" but Config.DeliveryEphemeralAcknowledged is
	// false. in_process delivery is process-local and loses accepted, in-flight work on
	// a crash; production must not run an ephemeral send path without the host
	// explicitly accepting that crash-loss (the recommended production posture is
	// "jobs").
	ErrDeliveryEphemeralUnacknowledged = errors.New(`auth: production RuntimeMode with DeliveryMode "in_process" requires Config.DeliveryEphemeralAcknowledged (ephemeral in-process delivery loses in-flight work on crash)`)
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

// LimiterDurability is the optional metadata a rate-limiter backend may declare
// through RateLimiterDurabilityReporter (design §4.4/§8). InProcessOnly marks a
// limiter whose window state lives in a single process, so its budget is enforced
// N× across N instances; production rejects it and development warns. The zero
// value (InProcessOnly false) declares a shared/durable limiter safe for
// multi-instance use.
type LimiterDurability struct {
	InProcessOnly bool
}

// RateLimiterDurabilityReporter is the optional interface a ratelimiter.Limiter
// may implement to declare whether it is shared/durable across instances (design
// §8). The bundled in-process ratelimiter.Memory is detected structurally — it is
// sdk-only and cannot import this feature to declare metadata — while a host's
// custom in-process limiter implements this to be rejected in production, and a
// durable host limiter may implement it to positively declare safety. It is defined
// feature-side because the Limiter port lives in sdk.
type RateLimiterDurabilityReporter interface {
	RateLimiterDurability() LimiterDurability
}

// Stable required-collaborator errors for the v3 security seams. The Config
// slots below are frozen now; each error is returned by the phase that enables
// its subsystem (challenges → phase 3, delivery outbox → phase 4, PII-free
// limits → phase 5, link flows → phase 7), per the design's "validated only when
// their subsystem becomes enabled" rule. Defining them here keeps the vocabulary
// stable across phases.
var (
	// ErrChallengeProtectorRequired is returned when the challenge subsystem is
	// enabled without a Config.ChallengeProtector (design §3.3).
	ErrChallengeProtectorRequired = errors.New("auth: Config.ChallengeProtector is required")
	// ErrIdentifierKeyerRequired is returned in production when PII-free rate
	// limiting is enabled without a Config.IdentifierKeyer (design §4.4).
	ErrIdentifierKeyerRequired = errors.New("auth: Config.IdentifierKeyer is required in production")
	// ErrDeliveryEncrypterRequired is returned when the delivery outbox is
	// enabled without a Config.DeliveryEncrypter (design §6.1.1).
	ErrDeliveryEncrypterRequired = errors.New("auth: Config.DeliveryEncrypter is required")
	// ErrPublicAuthBaseURLRequired is returned when a link flow is enabled
	// without a Config.PublicAuthBaseURL (design §6.4).
	ErrPublicAuthBaseURLRequired = errors.New("auth: Config.PublicAuthBaseURL is required when a link flow is enabled")
	// ErrPublicAuthBaseURLInvalid is returned when a link flow is enabled with a
	// Config.PublicAuthBaseURL that is not a valid absolute http(s) URL (design
	// §6.4): magic links are built from it, never from a request Host, so it must be
	// a well-formed absolute base at construction.
	ErrPublicAuthBaseURLInvalid = errors.New("auth: Config.PublicAuthBaseURL must be a valid absolute http(s) URL")
	// ErrPublicAuthBaseURLInsecure is returned in production RuntimeMode when
	// Config.PublicAuthBaseURL is not HTTPS (design §6.4): a magic link over plain
	// HTTP exposes the single-use token in transit.
	ErrPublicAuthBaseURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS Config.PublicAuthBaseURL")
	// ErrPasswordResetURLRequired is returned in production RuntimeMode when the
	// challenge-backed forgot/reset rail is wired without a Config.PasswordResetURL
	// (CHAU-5.1). Reset mail that prints only a raw token is not an acceptable
	// production experience, so the omission fails at construction rather than
	// degrading silently. Development permits it with a startup WARN.
	ErrPasswordResetURLRequired = errors.New("auth: production RuntimeMode requires Config.PasswordResetURL when the password-reset rail is wired")
	// ErrPasswordResetURLInvalid is returned when Config.PasswordResetURL is not a
	// valid absolute http(s) URL with a host, carries a FRAGMENT (the builder
	// appends a query parameter, which a fragment would swallow), or already carries
	// a `token` query parameter (the builder must never overwrite ambiguous host
	// input).
	ErrPasswordResetURLInvalid = errors.New("auth: Config.PasswordResetURL must be a valid absolute http(s) URL with no fragment and no token query parameter")
	// ErrPasswordResetURLInsecure is returned in production RuntimeMode when
	// Config.PasswordResetURL is not HTTPS: the link carries a single-use
	// credential, and plain HTTP exposes it in transit.
	ErrPasswordResetURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS Config.PasswordResetURL")
	// ErrOAuthLinkURLInvalid is returned when a non-empty Config.OAuthLinkBaseURL is
	// not a valid absolute http(s) URL with a host, or carries a FRAGMENT (the
	// builder appends "#token=<token>", which an existing fragment would swallow —
	// the token owns the fragment). Empty is allowed (the caller degrades to the
	// bare-token email line), so this covers only the shape of a non-empty value.
	ErrOAuthLinkURLInvalid = errors.New("auth: Config.OAuthLinkBaseURL must be a valid absolute http(s) URL with no fragment")
	// ErrOAuthLinkURLInsecure is returned in production RuntimeMode when a non-empty
	// Config.OAuthLinkBaseURL is not HTTPS: the pending-link URL carries a single-use
	// credential in its fragment, and plain HTTP exposes it in transit.
	ErrOAuthLinkURLInsecure = errors.New("auth: production RuntimeMode requires an HTTPS Config.OAuthLinkBaseURL")
	// ErrCredentialPolicyRequired is reserved for the credential suite (phase 6):
	// strict production validation rejects a configuration that disables the
	// bundled default without supplying a replacement policy (design §5.6/§8). A
	// nil Config.CredentialPolicy otherwise selects the bundled
	// credential.NewDefaultPolicy default.
	ErrCredentialPolicyRequired = errors.New("auth: Config.CredentialPolicy is required when the bundled default is disabled")
)

// Passwordless enablement construction errors (design §4.2/§8). Config.Passwordless
// is deny-by-absence — empty means the passwordless routes are not registered — so
// these fire only when a host opts in by listing at least one kind. A half-wired
// passwordless configuration would strand the users it is enabled for, so every
// gap degrades LOUDLY at construction (the partial-wiring precedent).
var (
	// ErrPasswordlessKindInvalid is returned when Config.Passwordless lists a kind
	// other than "email" or "phone" (the v3 kinds, design §4.2).
	ErrPasswordlessKindInvalid = errors.New(`auth: Config.Passwordless kinds must be "email" or "phone"`)
	// ErrPasswordlessKindUnsupported is returned when a listed kind has no wired
	// delivery channel (design §4.2): email needs the required Mailer or an
	// email-kind Notifier, phone needs a wired phone-kind Notifier. In production the
	// wired transport must also be production-capable (validateDeliveryTransports).
	ErrPasswordlessKindUnsupported = errors.New("auth: Config.Passwordless lists a kind with no wired delivery channel")
	// ErrPasswordlessChallengeRequired is returned when passwordless is enabled
	// without the atomic challenge rail wired (Repositories.Challenges): a
	// passwordless start issues a login_magic_link / login_otp challenge, so the rail
	// is required (design §4.3).
	ErrPasswordlessChallengeRequired = errors.New("auth: Config.Passwordless requires Repositories.Challenges (the atomic challenge rail)")
	// ErrPasswordlessDeliveryRequired is returned when passwordless is enabled without
	// a delivery runtime (DeliveryMode "off"): passwordless starts enqueue an opaque
	// delivery command and resolve the account off the request path (design
	// §4.1/§6.1.1, V14), so a delivery runtime ("jobs" or "in_process") is required.
	ErrPasswordlessDeliveryRequired = errors.New(`auth: Config.Passwordless requires a delivery runtime (DeliveryMode "jobs" or "in_process")`)
)

// DigestCandidate pairs a challenge-protector key ID with the code digest
// computed under that key. During key rotation CandidateCodeDigests returns one
// candidate per accepted key ID, and an atomic store selects the candidate whose
// KeyID matches the challenge row's protector_key_id (design §3.3). It is an
// alias of challenge.DigestCandidate so the protector and challenge.Repository
// speak one type without an import cycle (the challenge domain, which the
// Challenges repository references, cannot import this package back).
type DigestCandidate = challenge.DigestCandidate

// CredentialPolicy evaluates a proposed credential/identifier mutation against
// the current and proposed MethodSet (design §5.6). It is an alias of
// credential.Policy so the Config slot names one type across the public and
// domain packages (the Principal/Granter alias precedent); the bundled safe
// default is credential.NewDefaultPolicy. A host wiring a stronger policy
// implements credential.Policy directly.
type CredentialPolicy = credential.Policy

// ChallengeProtector protects short authentication codes with a keyed HMAC and
// digests high-entropy tokens (design §3.3). The bundled HMACChallengeProtector
// (AV3-0.2) implements it over crypto/hmac + sha256; the host supplies the key
// ring. The pepper key is distinct from the JWT signing key and the encryption
// key.
type ChallengeProtector interface {
	// ActiveKeyID reports the key ID new issues are digested under.
	ActiveKeyID() string
	// DigestCode returns the HMAC digest of a code under keyID, domain-separated
	// and bound to userID + purpose + code.
	DigestCode(keyID, userID, purpose, code string) (string, error)
	// CandidateCodeDigests returns one DigestCandidate per accepted key ID so an
	// unexpired challenge issued under an old key stays verifiable during
	// rotation.
	CandidateCodeDigests(userID, purpose, code string) ([]DigestCandidate, error)
	// DigestToken returns the SHA-256 digest of a high-entropy URL token; entropy
	// protects tokens, so no pepper is applied.
	DigestToken(token string) string
}

// IdentifierNormalizer produces the single canonical form of an identifier value
// used for persistence, lookup, invitations, rate-limit keys, and audit details
// (design §2.2). One injected policy is shared across the feature. Nil selects
// the bundled strict default (AV3-1.1).
type IdentifierNormalizer interface {
	Normalize(kind, value string) (string, error)
}

// IdentifierKeyer derives a stable, non-reversible key from an identifier for
// rate-limiter and idempotency keys, so raw PII never enters limiter keys
// (design §4.4). Its key is distinct from the challenge pepper, the JWT signing
// key, and the delivery-encryption key. The bundled HMAC keyer (AV3-0.2)
// implements it.
type IdentifierKeyer interface {
	IdentifierKey(kind, normalizedValue string) string
}

// validateRuntimeMode enforces the required-enum rule (design §8): empty →
// ErrRuntimeModeRequired, unknown → ErrRuntimeModeInvalid, else nil. The rule
// itself is the canonical environment.ValidateMode (CHAU-3.3); this only
// re-labels the verdict in auth's stable Config-oriented vocabulary, and both
// sentinels remain errors.Is-matchable because auth's wrap the sdk's.
func validateRuntimeMode(m RuntimeMode) error {
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
func validateDeliveryMode(m DeliveryMode) error {
	switch m {
	case "":
		return ErrDeliveryModeRequired
	case DeliveryModeOff, DeliveryModeInProcess, DeliveryModeJobs:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrDeliveryModeInvalid, m)
	}
}

// validateDeliveryTransports enforces the transport-security posture (design
// §6.3): in production a development-only or metadata-less email Sender or
// Notifier is ErrInsecureDeliveryTransport; in development a development-only
// transport emits a startup WARN. The Mailer is always the required email
// transport; each wired Notifier is checked too.
//
// The production VERDICT is delegated to the capability that owns each port —
// email.CheckSender and notify.CheckNotifier (CHAU-3.2) — so this feature and a
// host's app-wide mailer enforce one rule from one place. What stays here is
// auth's own vocabulary (ErrInsecureDeliveryTransport, the per-transport label)
// and auth's development WARN wording, because message text and log routing are
// composition concerns the sdk validators deliberately do not own.
func validateDeliveryTransports(mode RuntimeMode, mailer email.Sender, notifiers []notify.Notifier, log *slog.Logger) error {
	posture, err := email.CheckSender(mode, mailer)
	if err := asInsecureTransport(err, posture.Declared, "email sender"); err != nil {
		return err
	}
	warnDevelopmentOnlyTransport(mode, posture.Declared, posture.Capabilities.DevelopmentOnly, "email sender", log)

	for _, n := range notifiers {
		label := "notifier " + n.Kind()
		nposture, err := notify.CheckNotifier(mode, n)
		if err := asInsecureTransport(err, nposture.Declared, label); err != nil {
			return err
		}
		warnDevelopmentOnlyTransport(mode, nposture.Declared, nposture.Capabilities.DevelopmentOnly, label, log)
	}
	return nil
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
	if !errors.Is(capErr, email.ErrInsecureTransport) && !errors.Is(capErr, notify.ErrInsecureTransport) {
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
// and silent in development, which is the behavior this feature already shipped.
func warnDevelopmentOnlyTransport(mode RuntimeMode, declared, developmentOnly bool, label string, log *slog.Logger) {
	if mode == RuntimeModeDevelopment && declared && developmentOnly {
		log.Warn("auth: development-only delivery transport wired; never use in production (leaks message bodies to logs)", "transport", label)
	}
}

// validateRateLimiter enforces the shared-limiter posture (design §4.4/§8): PII-free
// login rate limiting is always active, so a multi-instance production deployment
// needs a shared/durable limiter — an in-process one enforces only a per-process
// budget (N× the intended limit). In production an in-process-only limiter is
// ErrNonDurableRateLimiter; in development it is permitted with a startup WARN. A
// limiter that does not identify as in-process-only is tolerated in both modes.
// cfgLimiter is the HOST-supplied limiter: nil means the feature defaulted a nil
// RateLimiter to the in-process ratelimiter.Memory.
func validateRateLimiter(mode RuntimeMode, cfgLimiter ratelimiter.Limiter, log *slog.Logger) error {
	if !limiterInProcessOnly(cfgLimiter) {
		return nil
	}
	switch mode {
	case RuntimeModeProduction:
		return ErrNonDurableRateLimiter
	case RuntimeModeDevelopment:
		log.Warn("auth: in-process rate limiter wired; its budget is per-process, so a multi-instance deployment gets N× the intended limit — wire a shared/durable limiter for production", "limiter", "in-process")
	}
	return nil
}

// limiterInProcessOnly reports whether limiter is in-process-only: the bundled
// ratelimiter.Memory (nil default or the concrete type — it is sdk-only and cannot
// declare feature metadata), or a limiter positively declaring InProcessOnly
// through RateLimiterDurabilityReporter. Any other limiter is presumed
// shared/durable (a negative it need not prove).
func limiterInProcessOnly(limiter ratelimiter.Limiter) bool {
	if limiter == nil {
		return true // the feature defaults a nil RateLimiter to the in-process ratelimiter.Memory
	}
	if r, ok := limiter.(RateLimiterDurabilityReporter); ok {
		return r.RateLimiterDurability().InProcessOnly
	}
	if _, ok := limiter.(*ratelimiter.Memory); ok {
		return true
	}
	return false
}

// PasswordResetTokenParam is the query parameter the password-reset landing URL
// receives (CHAU-5.2). It is exported so a host's SPA route and this feature's
// validator agree on one name instead of two string literals. It is an alias of
// the internal builder's constant, so the validator and the builder can never
// disagree about what they reject and what they append.
const PasswordResetTokenParam = authsvc.PasswordResetTokenParam

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
func validatePasswordResetURL(mode RuntimeMode, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrPasswordResetURLInvalid, raw)
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: fragment present in %q", ErrPasswordResetURLInvalid, raw)
	}
	if u.Query().Has(PasswordResetTokenParam) {
		return fmt.Errorf("%w: %q already carries a %q parameter", ErrPasswordResetURLInvalid, raw, PasswordResetTokenParam)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if mode == RuntimeModeProduction {
			return fmt.Errorf("%w: %q", ErrPasswordResetURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrPasswordResetURLInvalid, raw)
	}
}

// validatePasswordless enforces the passwordless enablement matrix (design
// §4.1/§4.2/§6.4/§8). It is a no-op while Config.Passwordless is empty (the routes
// are then absent — deny-by-absence). When a host opts in, every listed kind must be
// a valid v3 kind (email/phone) with a wired delivery channel (router.Supports —
// email always, phone iff a phone-kind notifier is wired); the atomic challenge rail
// and the durable delivery outbox must be wired (a start issues a challenge and
// enqueues asynchronously, V14); and, a magic link being an always-selectable method,
// Config.PublicAuthBaseURL must be a valid absolute base (HTTPS in production, §6.4).
// The always-on production gates — a shared/durable limiter, the identifier keyer,
// and the delivery-worker acknowledgment — are validated by NewService before this
// runs, so a passwordless-enabled production host inherits them. Production-capability
// of a wired transport is enforced by validateDeliveryTransports; this check only
// requires the channel to exist. Any gap is a loud construction error rather than a
// half-wired config that would strand the users passwordless is enabled for.
func validatePasswordless(mode RuntimeMode, kinds []string, router *delivery.Router, challengesWired, outboxWired bool, publicBaseURL string) error {
	if len(kinds) == 0 {
		return nil
	}
	for _, k := range kinds {
		switch k {
		case identity.KindEmail, identity.KindPhone:
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
func validatePublicAuthBaseURL(mode RuntimeMode, raw string) error {
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
		if mode == RuntimeModeProduction {
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
func validateOAuthLinkBaseURL(mode RuntimeMode, raw string) error {
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
		if mode == RuntimeModeProduction {
			return fmt.Errorf("%w: %q", ErrOAuthLinkURLInsecure, raw)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrOAuthLinkURLInvalid, raw)
	}
}
