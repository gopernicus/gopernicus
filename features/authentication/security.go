package authentication

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// RuntimeMode selects the feature's fail-closed posture (design §8). It is a
// REQUIRED enum: an empty value is ErrRuntimeModeRequired and an unknown value
// is ErrRuntimeModeInvalid, so a host can never accidentally inherit the
// development posture. "production" rejects development-only delivery transports
// (and, as later phases wire them, insecure public URLs/cookies, non-durable
// limiters, and missing security collaborators); "development" warns instead.
type RuntimeMode string

const (
	// RuntimeModeDevelopment is the local/dev posture: unsafe transports are
	// permitted with a startup WARN.
	RuntimeModeDevelopment RuntimeMode = "development"
	// RuntimeModeProduction is the fail-closed posture: development-only or
	// metadata-less delivery transports are rejected at construction.
	RuntimeModeProduction RuntimeMode = "production"
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
	// mode has no default so a host cannot accidentally ship the dev posture.
	ErrRuntimeModeRequired = errors.New(`auth: Config.RuntimeMode is required ("development" or "production")`)
	// ErrRuntimeModeInvalid is returned when Config.RuntimeMode is a value other
	// than "development" or "production".
	ErrRuntimeModeInvalid = errors.New(`auth: Config.RuntimeMode must be "development" or "production"`)
	// ErrInsecureDeliveryTransport is returned in production RuntimeMode when a
	// wired email Sender or Notifier is development-only or declares no capability
	// metadata (design §6.3): a console transport leaks OTPs and magic links to
	// logs, and an undeclared transport cannot be proven safe.
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
// ErrRuntimeModeRequired, unknown → ErrRuntimeModeInvalid, else nil.
func validateRuntimeMode(m RuntimeMode) error {
	switch m {
	case "":
		return ErrRuntimeModeRequired
	case RuntimeModeDevelopment, RuntimeModeProduction:
		return nil
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
func validateDeliveryTransports(mode RuntimeMode, mailer email.Sender, notifiers []notify.Notifier, log *slog.Logger) error {
	caps, declared := emailCapabilities(mailer)
	if err := enforceTransport(mode, declared, caps.DevelopmentOnly, "email sender", log); err != nil {
		return err
	}
	for _, n := range notifiers {
		ncaps, ndeclared := notifyCapabilities(n)
		if err := enforceTransport(mode, ndeclared, ncaps.DevelopmentOnly, "notifier "+n.Kind(), log); err != nil {
			return err
		}
	}
	return nil
}

// enforceTransport applies the mode-specific rule to one transport given whether
// it declared metadata and whether it is development-only.
func enforceTransport(mode RuntimeMode, declared, developmentOnly bool, label string, log *slog.Logger) error {
	switch mode {
	case RuntimeModeProduction:
		if !declared {
			return fmt.Errorf("%w: %s declares no capability metadata", ErrInsecureDeliveryTransport, label)
		}
		if developmentOnly {
			return fmt.Errorf("%w: %s is development-only", ErrInsecureDeliveryTransport, label)
		}
	case RuntimeModeDevelopment:
		if declared && developmentOnly {
			log.Warn("auth: development-only delivery transport wired; never use in production (leaks message bodies to logs)", "transport", label)
		}
	}
	return nil
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

// emailCapabilities reads a Sender's optional capability metadata. declared is
// false when the Sender does not implement email.CapabilityReporter.
func emailCapabilities(s email.Sender) (email.Capabilities, bool) {
	r, ok := s.(email.CapabilityReporter)
	if !ok {
		return email.Capabilities{}, false
	}
	return r.Capabilities(), true
}

// notifyCapabilities reads a Notifier's optional capability metadata. declared is
// false when the Notifier does not implement notify.CapabilityReporter.
func notifyCapabilities(n notify.Notifier) (notify.Capabilities, bool) {
	r, ok := n.(notify.CapabilityReporter)
	if !ok {
		return notify.Capabilities{}, false
	}
	return r.Capabilities(), true
}
