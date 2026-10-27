package authentication

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	defaultRevocationCacheTTL     = 30 * time.Second
	defaultRevocationOutageGrace  = 5 * time.Minute
	defaultRevocationTimeout      = 2 * time.Second
	maxRevocationTimeout          = 10 * time.Second
	defaultRevocationCacheEntries = 10_000
	revocationCacheNamespace      = "authext"

	defaultLoginRecordCacheEntries = 10_000
	loginRecordCacheNamespace      = "authext_login"
	ageDenialCacheNamespace        = "authext_age_denied"
	maxLoginRecordTTL              = 24 * time.Hour
	minLoginRecordTTL              = time.Minute
)

// ErrDevelopmentOnlyProvider is returned in production for an external identity
// provider, or at the pocket root an OAuth provider, that declares itself
// development-only through its capability hook (sdkauth.CapabilityReporter,
// oauth.CapabilityReporter). The error also wraps the capability's own
// sentinel. A provider that declares nothing is accepted.
var ErrDevelopmentOnlyProvider = errors.New("authentication: production refuses a development-only identity provider")

// ExternalProvider is one wired identity provider.
type ExternalProvider struct {
	// Name is the host-chosen mirror namespace ("idp"). Entries for one
	// provider that issues under several issuers share a Name.
	Name string
	// Provider is required. Its Issuer is unique across entries and differs from
	// the delegated-token issuer.
	Provider sdkauth.Provider
	// Revocation is optional; nil limits re-checks to local user status.
	Revocation sdkauth.RevocationChecker
	// CookieName is the provider's own session cookie, read by the HTTP adapter.
	CookieName string
	// ProvisionOnFirstVerification creates a local user for an unknown subject.
	// The zero value denies unknown subjects. Under IdentityConfig.Registration
	// invitation_only it provisions only a provider-verified address with a
	// pending invitation; closed beside it is a construction error.
	ProvisionOnFirstVerification bool
	// RequireVerifiedEmail denies a token whose email is present but unverified.
	RequireVerifiedEmail bool
	// RequireMFA denies a token below AAL2 — one whose provider did not assert
	// multi-factor authentication (sdkauth.Identity.MFA) — on every route, with
	// ErrMFARequired and an mfa_required_denied row. The check precedes subject
	// resolution, so a denied token never provisions a user.
	RequireMFA bool
	// AllowedTenants, when non-empty, must contain Identity.Tenant exactly.
	AllowedTenants []string
	// RevocationFailOpen lets LivenessOnMutation pass on validity and local status
	// while the provider cannot answer. LivenessLive always fails closed.
	// Production logs posture_waiver=RevocationFailOpen for it.
	RevocationFailOpen bool
	// MaxAuthenticationAge bounds how long ago the token's holder authenticated
	// (OIDC max_age semantics), checked on every request before the subject
	// resolves with a 60-second skew leeway: an older token, or one carrying no
	// authentication time, is denied (external_subject_denied
	// authentication_age_exceeded or authenticated_at_missing). The instant is
	// sdkauth.Identity.AuthenticatedAt — the auth_time claim when the provider
	// asserts it (an absolute cap on the provider session), else iat (then it
	// bounds token age only). Zero is unbounded; negative is sdk.ErrInvalidInput.
	MaxAuthenticationAge time.Duration
}

// ExternalConfig wires external identity providers. Zero durations and counts
// select their defaults.
type ExternalConfig struct {
	Providers []ExternalProvider
	// RevocationCache stores provider revocation facts under the "authext"
	// namespace. Nil selects an in-process cacher.Memory.
	RevocationCache cacher.Storer
	// RevocationCacheTTL is how long fetched facts count as fresh (30s).
	RevocationCacheTTL time.Duration
	// RevocationOutageGrace is how long stale facts are served while the provider
	// cannot answer (5m). It must be at least RevocationCacheTTL.
	RevocationOutageGrace time.Duration
	// RevocationTimeout bounds one provider call (2s); it must be under 10s. A
	// shorter request deadline still applies.
	RevocationTimeout time.Duration
	// RevocationCacheEntries bounds the default in-process cache (10,000).
	RevocationCacheEntries int
	// LoginRecordCache remembers which tokens already recorded external_login,
	// under its own "authext_login" namespace, so one token records one row per
	// cache. Nil selects an in-process cacher.Memory: dedupe per instance. A
	// store shared across instances dedupes fleet-wide. It is separate from
	// RevocationCache so login records never evict revocation facts. The same
	// store, under "authext_age_denied", records external_subject_denied once
	// per token for the MaxAuthenticationAge reasons.
	LoginRecordCache cacher.Storer
	// LoginRecordCacheEntries bounds the default in-process login-record cache
	// (10,000).
	LoginRecordCacheEntries int
	// SingleFactorAcknowledged affirms that an external-only host accepts
	// single-factor sign-in: a provider without RequireMFA admits AAL1 tokens.
	// Production NewExternalAuthenticator refuses that posture without it
	// (ErrSingleFactorUnacknowledged, cause external_no_mfa) and logs one
	// posture_waiver WARN with it; development WARNs either way. It is inert
	// when every provider sets RequireMFA. The pocket root's New refuses it: a
	// hybrid host sets IdentityConfig.SingleFactorAcknowledged instead. It is a
	// wiring assertion set as a literal in the composition root, not an env
	// knob.
	SingleFactorAcknowledged bool
}

// ExternalRepositories are the ports the external engine uses.
type ExternalRepositories struct {
	Users            user.UserRepository                   // required
	Identifiers      identifier.IdentifierRepository       // required
	ExternalSubjects externalsubject.Repository            // required
	SecurityEvents   securityevent.SecurityEventRepository // optional
	Invitations      invitationResolver                    // optional: pending auto-accept after provisioning
}

// ExternalOption configures NewExternalAuthenticator. Nil options are invalid.
type ExternalOption func(*externalOptions)

type externalOptions struct {
	clock      func() time.Time
	logger     *slog.Logger
	ids        sdk.IDGenerator
	normalizer identifier.Normalizer
	keyer      identifierKeyer
	recorder   *securityevent.Recorder
	// registration is the service's IdentityConfig.Registration, handed in by
	// New: an external-only engine stays open.
	registration RegistrationMode
}

// WithExternal enables external identity providers on New. The Service builds
// the engine from its own users, identifiers, audit and invitation ports plus
// Repositories.ExternalSubjects.
func WithExternal(value ExternalConfig) Option {
	value = cloneExternalConfig(value)
	return func(c *constructorConfig) {
		value := cloneExternalConfig(value)
		c.External = &value
	}
}

// WithExternalClock supplies the clock; nil selects time.Now.
func WithExternalClock(value func() time.Time) ExternalOption {
	return func(o *externalOptions) { o.clock = value }
}

// WithExternalLogger supplies the logger; nil selects slog.Default().
func WithExternalLogger(value *slog.Logger) ExternalOption {
	return func(o *externalOptions) { o.logger = value }
}

// WithExternalIDs supplies the entity-ID strategy for provisioned rows.
func WithExternalIDs(value sdk.IDGenerator) ExternalOption {
	return func(o *externalOptions) { o.ids = value }
}

// WithExternalNormalizer supplies the identifier normalizer; nil selects
// identifier.DefaultNormalizer.
func WithExternalNormalizer(value identifier.Normalizer) ExternalOption {
	return func(o *externalOptions) { o.normalizer = value }
}

// WithExternalIdentifierKeyer supplies the PII-free identifier keyer used in
// collision audit rows. Production requires it when any provider provisions.
func WithExternalIdentifierKeyer(value identifierKeyer) ExternalOption {
	return func(o *externalOptions) { o.keyer = value }
}

// WithExternalSecurityEventRecorder supplies the recorder the engine records
// through, so its rows share one recorder — and one failure count — with the
// host's other recording sites. It wins over ExternalRepositories.SecurityEvents.
// Without it the engine records through its own recorder over
// ExternalRepositories.SecurityEvents. New passes its service's recorder to the
// engine WithExternal builds.
func WithExternalSecurityEventRecorder(value *securityevent.Recorder) ExternalOption {
	return func(o *externalOptions) { o.recorder = value }
}

func cloneExternalConfig(value ExternalConfig) ExternalConfig {
	value.Providers = slices.Clone(value.Providers)
	for i := range value.Providers {
		value.Providers[i].AllowedTenants = slices.Clone(value.Providers[i].AllowedTenants)
	}
	return value
}

// validateExternal checks a wired configuration and returns it with defaults
// applied. delegatedIssuer is the hybrid host's delegated-token issuer, if any.
// Production refuses a provider declaring itself development-only
// (sdkauth.CheckProvider); development logs one WARN per such provider on log.
func validateExternal(cfg ExternalConfig, repos ExternalRepositories, mode environment.Mode, keyer identifierKeyer, delegatedIssuer string, log *slog.Logger) (ExternalConfig, error) {
	cfg = cloneExternalConfig(cfg)
	if len(cfg.Providers) == 0 {
		return cfg, fmt.Errorf("authentication: external identity requires at least one provider: %w", sdk.ErrInvalidInput)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"Users", repos.Users}, {"Identifiers", repos.Identifiers}, {"ExternalSubjects", repos.ExternalSubjects}} {
		if nilDependency(dep.value) {
			return cfg, fmt.Errorf("authentication: external identity requires %s: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"SecurityEvents", repos.SecurityEvents}, {"Invitations", repos.Invitations}, {"RevocationCache", cfg.RevocationCache}, {"LoginRecordCache", cfg.LoginRecordCache}} {
		if dep.value != nil && nilDependency(dep.value) {
			return cfg, fmt.Errorf("authentication: external %s contains a typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	issuers := map[string]bool{}
	provisions := false
	for i, p := range cfg.Providers {
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Name) != p.Name {
			return cfg, fmt.Errorf("authentication: external provider %d needs a name: %w", i, sdk.ErrInvalidInput)
		}
		if nilDependency(p.Provider) {
			return cfg, fmt.Errorf("authentication: external provider %q has no Provider: %w", p.Name, sdk.ErrInvalidInput)
		}
		posture, err := sdkauth.CheckProvider(mode, p.Provider)
		if errors.Is(err, sdkauth.ErrDevelopmentOnlyProvider) {
			return cfg, fmt.Errorf("%w: external provider %q: %w", ErrDevelopmentOnlyProvider, p.Name, err)
		}
		if err != nil {
			return cfg, err
		}
		if posture.Capabilities.DevelopmentOnly {
			log.Warn("authentication: development-only external identity provider wired; production refuses it", "provider", p.Name)
		}
		if p.Revocation != nil && nilDependency(p.Revocation) {
			return cfg, fmt.Errorf("authentication: external provider %q Revocation contains a typed nil: %w", p.Name, sdk.ErrInvalidInput)
		}
		issuer := p.Provider.Issuer()
		u, err := url.Parse(issuer)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(issuer) != issuer || (u.Scheme != "https" && u.Scheme != "http") || (mode == environment.ModeProduction && u.Scheme != "https") {
			return cfg, fmt.Errorf("authentication: external provider %q has an invalid or insecure issuer: %w", p.Name, sdk.ErrInvalidInput)
		}
		if issuers[issuer] || issuer == delegatedIssuer {
			return cfg, fmt.Errorf("authentication: external provider %q reuses an issuer: %w", p.Name, sdk.ErrInvalidInput)
		}
		issuers[issuer] = true
		if p.CookieName != "" && !validCookieName(p.CookieName) {
			return cfg, fmt.Errorf("authentication: external provider %q has an invalid cookie name: %w", p.Name, sdk.ErrInvalidInput)
		}
		if slices.Contains(p.AllowedTenants, "") {
			return cfg, fmt.Errorf("authentication: external provider %q allows a blank tenant: %w", p.Name, sdk.ErrInvalidInput)
		}
		if p.MaxAuthenticationAge < 0 {
			return cfg, fmt.Errorf("authentication: external provider %q MaxAuthenticationAge is negative: %w", p.Name, sdk.ErrInvalidInput)
		}
		provisions = provisions || p.ProvisionOnFirstVerification
	}
	if mode == environment.ModeProduction && provisions && nilDependency(keyer) {
		return cfg, fmt.Errorf("authentication: production external provisioning requires an IdentifierKeyer: %w", sdk.ErrInvalidInput)
	}
	if cfg.RevocationCacheTTL == 0 {
		cfg.RevocationCacheTTL = defaultRevocationCacheTTL
	}
	if cfg.RevocationOutageGrace == 0 {
		cfg.RevocationOutageGrace = defaultRevocationOutageGrace
	}
	if cfg.RevocationTimeout == 0 {
		cfg.RevocationTimeout = defaultRevocationTimeout
	}
	if cfg.RevocationCacheEntries == 0 {
		cfg.RevocationCacheEntries = defaultRevocationCacheEntries
	}
	if cfg.RevocationCacheTTL < 0 || cfg.RevocationOutageGrace < cfg.RevocationCacheTTL || cfg.RevocationTimeout < 0 || cfg.RevocationTimeout >= maxRevocationTimeout || cfg.RevocationCacheEntries < 0 {
		return cfg, fmt.Errorf("authentication: incoherent external revocation timing: %w", sdk.ErrInvalidInput)
	}
	if cfg.LoginRecordCacheEntries == 0 {
		cfg.LoginRecordCacheEntries = defaultLoginRecordCacheEntries
	}
	if cfg.LoginRecordCacheEntries < 0 {
		return cfg, fmt.Errorf("authentication: external LoginRecordCacheEntries is negative: %w", sdk.ErrInvalidInput)
	}
	for _, p := range cfg.Providers {
		logExternalLifetime(p, mode, log)
	}
	return cfg, nil
}

// logExternalLifetime logs one provider's lifetime boot line and, in
// production, the WARN for a provider whose tokens nothing bounds after
// sign-in: no revocation checker and no MaxAuthenticationAge.
func logExternalLifetime(p ExternalProvider, mode environment.Mode, log *slog.Logger) {
	age, checker := "off", "none"
	if p.MaxAuthenticationAge > 0 {
		age = p.MaxAuthenticationAge.String()
	}
	if p.Revocation != nil {
		checker = "wired"
	}
	log.Info("auth: external provider lifetime", "provider", p.Name, "max_authentication_age", age, "revocation_checker", checker, "fail_open", p.RevocationFailOpen)
	if mode == environment.ModeProduction && p.Revocation == nil && p.MaxAuthenticationAge == 0 {
		log.Warn("auth: an external provider's sign-in is bounded by token expiry alone; wire a RevocationChecker or set MaxAuthenticationAge", "external_authentication_age", "unbounded", "provider", p.Name, "revocation_checker", "none")
	}
}

// validCookieName reports whether name is an RFC 6265 cookie-name token.
func validCookieName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(`()<>@,;:\"/[]?={}`, c) >= 0 {
			return false
		}
	}
	return name != ""
}
