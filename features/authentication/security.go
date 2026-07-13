package authentication

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
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
	// ErrNonDurableDeliveryRepository is returned in production RuntimeMode when the
	// wired DeliveryJobs repository identifies itself as in-process-only via
	// deliveryjob.DurabilityReporter (design §8). Unlike the transport check, a
	// repository that declares NO metadata is tolerated ("where metadata can
	// identify it"): a durable store need not prove a negative. Only a positively
	// non-durable outbox is rejected — it silently drops delivery on restart.
	ErrNonDurableDeliveryRepository = errors.New("auth: production RuntimeMode rejects an in-process-only (non-durable) delivery repository")
	// ErrDeliveryWorkerUnacknowledged is returned in production RuntimeMode when the
	// DeliveryJobs outbox is wired but Config.DeliveryWorkerAcknowledged is false
	// (design §8). The outbox is the only send path (AV3-4.3), so a production host
	// that enqueues without running RunDeliveryWorker would silently never deliver.
	// The feature cannot observe the host's process lifecycle, so it requires an
	// explicit affirmation that the worker is run rather than failing open on a
	// stalled outbox.
	ErrDeliveryWorkerUnacknowledged = errors.New("auth: production RuntimeMode requires Config.DeliveryWorkerAcknowledged when the delivery outbox is wired (the host must run RunDeliveryWorker)")
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
// durable host limiter may implement it to positively declare safety. It mirrors
// deliveryjob.DurabilityReporter, defined feature-side because the Limiter port
// lives in sdk.
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
	// ErrPasswordlessDeliveryRequired is returned when passwordless is enabled
	// without the durable delivery outbox wired (Repositories.DeliveryJobs):
	// passwordless starts enqueue an opaque delivery command and resolve the account
	// off the request path (design §4.1/§6.1.1, V14), so the outbox is required.
	ErrPasswordlessDeliveryRequired = errors.New("auth: Config.Passwordless requires Repositories.DeliveryJobs (the durable delivery outbox)")
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

// validateDeliveryDurability enforces the durable-outbox posture (design §8): in
// production a DeliveryJobs repository that declares itself in-process-only via
// deliveryjob.DurabilityReporter is ErrNonDurableDeliveryRepository. A repository
// that declares no durability metadata is tolerated (the "where metadata can
// identify it" rule — a durable store is not asked to prove a negative), and
// development permits either. repo is non-nil at the call site.
func validateDeliveryDurability(mode RuntimeMode, repo deliveryjob.Repository) error {
	if mode != RuntimeModeProduction {
		return nil
	}
	r, ok := repo.(deliveryjob.DurabilityReporter)
	if !ok {
		return nil
	}
	if r.Durability().InProcessOnly {
		return ErrNonDurableDeliveryRepository
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
