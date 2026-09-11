package authentication

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

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
	d.UserAdmin = repos.UserAdmin
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
	if err := d.AuthenticationLimits.Validate(); err != nil {
		return nil, err
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
	if d.UserAdminCheck != nil && nilDependency(d.UserAdmin) {
		return nil, fmt.Errorf("authentication: UserAdminCheck requires UserAdmin: %w", sdk.ErrInvalidInput)
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
		{"IdentifierKeyer", d.IdentifierKeyer}, {"UserAdmin", d.UserAdmin}, {"PasswordlessRedeem", d.PasswordlessRedeem},
		{"OAuthAccounts", d.OAuthAccounts}, {"OAuthStates", d.OAuthStates}, {"TokenEncrypter", d.TokenEncrypter},
		{"SecurityEvents", d.SecurityEvents}, {"Invitations", d.Invitations}, {"Compromised", d.Compromised},
	} {
		if dep.value != nil && nilDependency(dep.value) {
			return nil, fmt.Errorf("authentication: %s contains a typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
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
