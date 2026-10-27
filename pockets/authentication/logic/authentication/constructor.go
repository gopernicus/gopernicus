package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// serviceAccountStatusProbeTimeout bounds the boot-time probe of the
// service-account status schema.
const serviceAccountStatusProbeTimeout = 5 * time.Second

// Components separates ordinary use cases from delivery initialization authority.
// Hosts pass DeliveryInitializer only to their delivery processor; request handlers
// receive Service. Constructing either requires the host's repositories and policy.
type Components struct {
	Service             *Service
	DeliveryInitializer delivery.Initializer
}

// New constructs independently usable authentication components. Required
// repositories and the selected credential rails are checked before any call can
// persist state. New at the pocket root additionally assembles delivery and HTTP.
func New(repos Repositories, signer cryptids.JWTSigner, runtimeMode environment.Mode, limiter ratelimiter.Limiter, opts ...Option) (*Components, error) {
	d := constructorConfig{}
	d.Users = repos.Users
	d.Identifiers = repos.Identifiers
	d.Passwords = repos.Passwords
	d.Sessions = repos.Sessions
	d.SessionManagement = repos.SessionManagement
	d.UserAdmin = repos.UserAdmin
	d.UserErasure = repos.UserErasure
	d.PasswordlessRedeem = repos.PasswordlessRedeem
	d.ActiveSessions = repos.ActiveSessions
	d.Challenges = repos.Challenges
	d.PasswordResets = repos.PasswordResets
	d.ContactChanges = repos.ContactChanges
	d.CredentialMutations = repos.CredentialMutations
	d.AuthenticationGrants = repos.AuthenticationGrants
	d.SecurityEvents = repos.SecurityEvents
	d.OAuthAccounts = repos.OAuthAccounts
	d.OAuthStates = repos.OAuthStates
	d.ServiceAccounts = repos.ServiceAccounts
	d.APIKeys = repos.APIKeys
	d.ExternalSubjects = repos.ExternalSubjects
	d.Passkeys = repos.Passkeys
	d.TOTP = repos.TOTP
	d.GlobalSessions = repos.GlobalSessions
	d.ServiceAccountStatuses = repos.ServiceAccountStatus
	d.TokenSigner = signer
	d.RuntimeMode = runtimeMode
	d.Limiter = limiter
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication New: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&d)
	}

	if err := environment.ValidateMode(d.RuntimeMode); err != nil {
		return nil, err
	}
	if d.DelegatedTokens.Issuer != "" {
		u, err := url.Parse(d.DelegatedTokens.Issuer)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(d.DelegatedTokens.Issuer) != d.DelegatedTokens.Issuer || (u.Scheme != "https" && u.Scheme != "http") || (d.RuntimeMode == environment.ModeProduction && u.Scheme != "https") {
			return nil, fmt.Errorf("authentication: invalid or insecure delegated issuer: %w", sdk.ErrInvalidInput)
		}
	}
	if err := d.AuthenticationLimits.Validate(); err != nil {
		return nil, err
	}
	if d.SecurityEventRecorder != nil && d.AuditWriteObserver != nil {
		return nil, fmt.Errorf("authentication: WithAuditWriteObserver applies only to the recorder New builds; set the observer on the supplied recorder: %w", sdk.ErrInvalidInput)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{
		{"Users", d.Users}, {"Identifiers", d.Identifiers}, {"Sessions", d.Sessions},
		{"ActiveSessions", d.ActiveSessions}, {"TokenSigner", d.TokenSigner}, {"Limiter", d.Limiter},
	} {
		if nilDependency(dep.value) {
			return nil, fmt.Errorf("authentication: %s is required: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	if !d.PasswordFlowsDisabled && (nilDependency(d.Hasher) || nilDependency(d.Passwords)) {
		return nil, fmt.Errorf("authentication: password flows require Hasher and Passwords: %w", sdk.ErrInvalidInput)
	}
	if nilDependency(d.ServiceAccounts) != nilDependency(d.APIKeys) {
		return nil, fmt.Errorf("authentication: machine repositories must be wired together: %w", sdk.ErrInvalidInput)
	}
	if nilDependency(d.Challenges) != nilDependency(d.Protector) {
		return nil, fmt.Errorf("authentication: challenges require their protector: %w", sdk.ErrInvalidInput)
	}
	if !nilDependency(d.Queue) && d.Deliver == nil {
		return nil, fmt.Errorf("authentication: delivery queue and renderer must be wired together: %w", sdk.ErrInvalidInput)
	}
	if !nilDependency(d.Queue) && !d.PasswordFlowsDisabled && (nilDependency(d.Challenges) || nilDependency(d.PasswordResets)) {
		return nil, fmt.Errorf("authentication: delivered password flows require challenge and reset stores: %w", sdk.ErrInvalidInput)
	}
	if d.RuntimeMode == environment.ModeProduction && nilDependency(d.IdentifierKeyer) {
		return nil, fmt.Errorf("authentication: production requires an IdentifierKeyer: %w", sdk.ErrInvalidInput)
	}

	if len(d.Passwordless) > 0 && (nilDependency(d.Challenges) || nilDependency(d.Protector) || d.Deliver == nil || nilDependency(d.Queue) || strings.TrimSpace(d.PublicAuthBaseURL) == "") {
		return nil, fmt.Errorf("authentication: passwordless requires challenges, delivery and a public URL: %w", sdk.ErrInvalidInput)
	}
	if d.ProvisionOnRedeem && (nilDependency(d.PasswordlessRedeem) || len(d.Passwordless) == 0) {
		return nil, fmt.Errorf("authentication: provisioning requires passwordless redemption: %w", sdk.ErrInvalidInput)
	}
	if len(d.Providers) > 0 && (nilDependency(d.OAuthAccounts) || nilDependency(d.OAuthStates) || strings.TrimSpace(d.OAuthCallbackBase) == "") {
		return nil, fmt.Errorf("authentication: OAuth requires account/state repositories and callback URL: %w", sdk.ErrInvalidInput)
	}
	if err := redirect.ValidateOAuthConfig(d.RuntimeMode, d.Providers, d.OAuthCallbackBase, d.OAuthNativeRedirectURIs); err != nil {
		return nil, err
	}
	if d.ContactChanges != nil && (nilDependency(d.ContactChanges) || nilDependency(d.CredentialMutations) || nilDependency(d.AuthenticationGrants) || nilDependency(d.Challenges) || nilDependency(d.Protector)) {
		return nil, fmt.Errorf("authentication: ContactChanges requires credential mutations, grants and challenges: %w", sdk.ErrInvalidInput)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{
		{"ServiceAccounts", d.ServiceAccounts}, {"APIKeys", d.APIKeys}, {"Challenges", d.Challenges}, {"Hasher", d.Hasher}, {"Passwords", d.Passwords},
		{"Queue", d.Queue}, {"Protector", d.Protector}, {"Normalizer", d.Normalizer}, {"CredentialPolicy", d.CredentialPolicy},
		{"PasswordResets", d.PasswordResets}, {"CredentialMutations", d.CredentialMutations}, {"AuthenticationGrants", d.AuthenticationGrants},
		{"IdentifierKeyer", d.IdentifierKeyer}, {"UserAdmin", d.UserAdmin}, {"UserErasure", d.UserErasure}, {"PasswordlessRedeem", d.PasswordlessRedeem},
		{"OAuthAccounts", d.OAuthAccounts}, {"OAuthStates", d.OAuthStates}, {"TokenEncrypter", d.TokenEncrypter},
		{"SessionManagement", d.SessionManagement}, {"SecurityEvents", d.SecurityEvents}, {"Invitations", d.Invitations}, {"Compromised", d.Compromised},
		{"ExternalSubjects", d.ExternalSubjects}, {"Passkeys", d.Passkeys}, {"RelyingParty", d.Passkey.RelyingParty},
		{"TOTP", d.TOTP}, {"TOTPConfig.Encrypter", d.TOTPConfig.Encrypter},
		{"GlobalSessions", d.GlobalSessions}, {"ServiceAccountStatus", d.ServiceAccountStatuses},
	} {
		if dep.value != nil && nilDependency(dep.value) {
			return nil, fmt.Errorf("authentication: %s contains a typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	if err := validatePasskeys(d); err != nil {
		return nil, err
	}
	if err := validateTOTP(d); err != nil {
		return nil, err
	}
	if err := validateRegistration(d); err != nil {
		return nil, err
	}
	if err := validateServiceAccountStatus(d); err != nil {
		return nil, err
	}
	lifetimeLog := d.Logger
	if lifetimeLog == nil {
		lifetimeLog = slog.Default()
	}
	if err := validateSessionLifetime(d, lifetimeLog); err != nil {
		return nil, err
	}
	if d.APIKeyMaxTTL < 0 {
		return nil, fmt.Errorf("authentication: MachineConfig.APIKeyMaxTTL is negative: %w", sdk.ErrInvalidInput)
	}
	if d.ProvisionOnRedeem && (!slices.Contains(d.Passwordless, sdk.AddressKindEmail) || nilDependency(d.IdentifierKeyer)) {
		return nil, fmt.Errorf("authentication: provisioning requires email passwordless and IdentifierKeyer: %w", sdk.ErrInvalidInput)
	}
	for _, kind := range d.Passwordless {
		if (kind != sdk.AddressKindEmail && kind != sdk.AddressKindPhone) || !d.Deliver.Supports(kind) {
			return nil, fmt.Errorf("authentication: passwordless kind %q has no supported delivery channel: %w", kind, sdk.ErrInvalidInput)
		}
	}
	if len(d.Passwordless) > 0 {
		if err := validatePublicURL(d.RuntimeMode, d.PublicAuthBaseURL, false, false); err != nil {
			return nil, err
		}
	}
	if !nilDependency(d.PasswordResets) && !nilDependency(d.Protector) {
		if d.PasswordResetURL != "" {
			if err := validatePublicURL(d.RuntimeMode, d.PasswordResetURL, true, true); err != nil {
				return nil, err
			}
		} else if d.RuntimeMode == environment.ModeProduction {
			return nil, fmt.Errorf("authentication: password reset URL required in production: %w", sdk.ErrInvalidInput)
		}
	}
	if len(d.Providers) > 0 && d.OAuthLinkBaseURL != "" {
		if err := validatePublicURL(d.RuntimeMode, d.OAuthLinkBaseURL, true, false); err != nil {
			return nil, err
		}
	}
	if d.RuntimeMode == environment.ModeProduction {
		_, memory := d.Limiter.(*ratelimiter.Memory)
		if reporter, ok := d.Limiter.(RateLimiterDurabilityReporter); ok {
			memory = reporter.RateLimiterDurability().InProcessOnly
		}
		if memory {
			return nil, fmt.Errorf("authentication: production requires a shared rate limiter: %w", sdk.ErrInvalidInput)
		}
		if d.Deliver != nil {
			if err := d.Deliver.ValidateTransports(d.RuntimeMode); err != nil {
				return nil, err
			}
		}
	}
	d.Providers = append(d.Providers[:0:0], d.Providers...)
	d.RedirectAllowlist = append(d.RedirectAllowlist[:0:0], d.RedirectAllowlist...)
	d.OAuthNativeRedirectURIs = append(d.OAuthNativeRedirectURIs[:0:0], d.OAuthNativeRedirectURIs...)
	d.Passwordless = append(d.Passwordless[:0:0], d.Passwordless...)
	service := newService(d)
	var external *ExternalAuthenticator
	if d.External != nil {
		repos := ExternalRepositories{Users: d.Users, Identifiers: d.Identifiers, ExternalSubjects: d.ExternalSubjects, SecurityEvents: d.SecurityEvents, Invitations: d.Invitations}
		log := d.Logger
		if log == nil {
			log = slog.Default()
		}
		cfg, err := validateExternal(*d.External, repos, d.RuntimeMode, d.IdentifierKeyer, d.DelegatedTokens.Issuer, log)
		if err != nil {
			return nil, err
		}
		external = newExternalAuthenticator(repos, cfg, d.RuntimeMode, externalOptions{clock: d.Clock, logger: d.Logger, ids: d.IDs, normalizer: d.Normalizer, keyer: d.IdentifierKeyer, recorder: service.securityEvents, registration: service.registration})
	}
	service.external = external
	service.warnOrphanedRecoveryCodes(context.Background())
	return &Components{Service: service, DeliveryInitializer: &deliveryInitializer{service: service}}, nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func validatePublicURL(mode environment.Mode, raw string, noFragment, noToken bool) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || (mode == environment.ModeProduction && u.Scheme != "https") || (noFragment && strings.Contains(raw, "#")) || (noToken && u.Query().Has(PasswordResetTokenParam)) {
		return fmt.Errorf("authentication: invalid or insecure public credential URL: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// validatePasskeys checks WithPasskeys against the wiring: a relying party needs
// the passkey store, the credential-mutation rail its removals ride, the
// recent-authentication grants its enrollment gate consumes, and the challenge
// rail that holds each ceremony's state; a store that probes its schema must
// report it present. Without WithPasskeys nothing here runs, so a store without
// migration 0025 boots.
func validatePasskeys(d constructorConfig) error {
	cfg := d.Passkey
	if cfg.RelyingParty == nil {
		return nil
	}
	if nilDependency(d.Passkeys) || nilDependency(d.CredentialMutations) || nilDependency(d.AuthenticationGrants) || nilDependency(d.Challenges) || nilDependency(d.Protector) {
		return ErrPasskeyReposRequired
	}
	switch cfg.UserVerification {
	case "", webauthn.Required, webauthn.Preferred:
	case webauthn.Discouraged:
		return fmt.Errorf("authentication: PasskeyConfig.UserVerification discouraged would leave no native AAL2 path (only a user-verified passkey reaches AAL2); use preferred or required: %w", sdk.ErrInvalidInput)
	default:
		return fmt.Errorf("authentication: PasskeyConfig.UserVerification %q is not preferred or required: %w", cfg.UserVerification, sdk.ErrInvalidInput)
	}
	if cfg.MaxPerUser < 0 || cfg.MaxPerUser > maxPasskeysPerUser {
		return fmt.Errorf("authentication: PasskeyConfig.MaxPerUser %d is outside 0 to %d: %w", cfg.MaxPerUser, maxPasskeysPerUser, sdk.ErrInvalidInput)
	}
	if prober, ok := d.Passkeys.(passkey.SchemaProber); ok {
		if err := prober.ProbeSchema(context.Background()); err != nil {
			return fmt.Errorf("%w: %w", ErrPasskeySchemaMissing, err)
		}
	}
	return nil
}

// validateTOTP checks WithTOTP against the wiring: an encrypter needs the TOTP
// store, the recent-authentication grants its gates consume, the challenge
// protector that digests recovery codes, and an issuer an authenticator app
// can list; a store that probes its schema must report it present. Without
// WithTOTP nothing here runs, so an unmigrated store boots.
func validateTOTP(d constructorConfig) error {
	cfg := d.TOTPConfig
	if cfg.Encrypter == nil {
		return nil
	}
	if nilDependency(d.TOTP) || nilDependency(d.AuthenticationGrants) || nilDependency(d.Protector) || nilDependency(d.CredentialMutations) {
		return ErrTOTPReposRequired
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.Contains(cfg.Issuer, ":") {
		return fmt.Errorf("authentication: TOTPConfig.Issuer is required and must not contain a colon: %w", sdk.ErrInvalidInput)
	}
	if prober, ok := d.TOTP.(totp.SchemaProber); ok {
		if err := prober.ProbeSchema(context.Background()); err != nil {
			return fmt.Errorf("%w: %w", ErrTOTPSchemaMissing, err)
		}
	}
	return nil
}

// validateRegistration checks IdentityConfig.Registration in every mode: an
// unknown value is refused, invitation_only needs an invitation service that
// can look up a pending invitation, and closed contradicts an external
// provider that provisions on first verification.
func validateRegistration(d constructorConfig) error {
	mode, err := normalizeRegistrationMode(d.Registration)
	if err != nil {
		return err
	}
	switch mode {
	case RegistrationInvitationOnly:
		if _, ok := d.Invitations.(invitationLookup); !ok || nilDependency(d.Invitations) {
			return ErrRegistrationModeRequiresInvitations
		}
	case RegistrationClosed:
		if d.External != nil && slices.ContainsFunc(d.External.Providers, func(p ExternalProvider) bool { return p.ProvisionOnFirstVerification }) {
			return ErrRegistrationClosedWithProvisioning
		}
	}
	return nil
}

// validateServiceAccountStatus checks MachineConfig.ServiceAccountStatus
// against the wiring: the flag needs the machine repositories and the status
// repository, whose schema probe runs under a bounded deadline. Only a probe
// reporting sdk.ErrNotFound means migration 0027 is absent; any other probe
// failure is sdk.ErrUnavailable. Without the flag nothing here runs, so an
// unmigrated store boots.
func validateServiceAccountStatus(d constructorConfig) error {
	if !d.ServiceAccountStatus {
		return nil
	}
	if nilDependency(d.ServiceAccountStatuses) || nilDependency(d.ServiceAccounts) || nilDependency(d.APIKeys) {
		return ErrServiceAccountStatusRequires
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceAccountStatusProbeTimeout)
	defer cancel()
	if err := d.ServiceAccountStatuses.ProbeSchema(ctx); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("%w: %w", ErrServiceAccountStatusSchemaMissing, err)
		}
		return fmt.Errorf("authentication: probing the service-account status schema: %w: %w", sdk.ErrUnavailable, err)
	}
	return nil
}

// validateIdleTimeout checks a set SessionsConfig.IdleTimeout against the
// wiring: Repositories.Sessions must implement session.ActivityRepository,
// whose schema probe runs under a bounded deadline. Only a probe reporting
// sdk.ErrNotFound means migration 0029 is absent; any other probe failure is
// sdk.ErrUnavailable. Without an idle timeout nothing here runs, so an
// unmigrated store boots.
func validateIdleTimeout(d constructorConfig) error {
	activity, ok := d.Sessions.(session.ActivityRepository)
	if !ok {
		return ErrIdleTimeoutRequires
	}
	ctx, cancel := context.WithTimeout(context.Background(), idleTimeoutProbeTimeout)
	defer cancel()
	if err := activity.ProbeSchema(ctx); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("%w: %w", ErrIdleTimeoutSchemaMissing, err)
		}
		return fmt.Errorf("authentication: probing the session activity schema: %w: %w", sdk.ErrUnavailable, err)
	}
	return nil
}
