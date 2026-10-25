package authentication

import (
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"

	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// validateRepositories checks the collaborators used by enabled operations.
// Optional capabilities remain absent rather than silently accepting a partial
// implementation that can fail after persisting an account.
func validateRepositories(repos Repositories, cfg constructorConfig) error {
	if err := cfg.AuthenticationLimits.Validate(); err != nil {
		return err
	}
	if err := cfg.PublicRouteLimits.Validate(); err != nil {
		return err
	}
	for _, required := range []struct {
		name  string
		value any
	}{
		{"Users", repos.Users},
		{"Identifiers", repos.Identifiers},
		{"Sessions", repos.Sessions},
		{"ActiveSessions", repos.ActiveSessions},
	} {
		if nilDependency(required.value) {
			return fmt.Errorf("authentication: Repositories.%s is required: %w", required.name, sdk.ErrInvalidInput)
		}
	}
	if cfg.OAuth2 != nil {
		for _, dependency := range []any{repos.OAuth2, repos.SessionManagement, cfg.OAuth2.Clients, cfg.OAuth2.Views, cfg.Views} {
			if nilDependency(dependency) {
				return fmt.Errorf("authentication: OAuth2 requires storage, client trust, browser and consent views: %w", sdk.ErrInvalidInput)
			}
		}
	}
	if !cfg.PasswordFlowsDisabled && nilDependency(repos.Passwords) {
		return fmt.Errorf("authentication: password flows require Repositories.Passwords: %w", sdk.ErrInvalidInput)
	}
	if !cfg.PasswordFlowsDisabled && cfg.DeliveryMode != delivery.ModeOff {
		if nilDependency(repos.Challenges) || nilDependency(repos.PasswordResets) || nilDependency(cfg.ChallengeProtector) {
			return fmt.Errorf("authentication: password delivery requires Challenges, PasswordResets and ChallengeProtector: %w", sdk.ErrInvalidInput)
		}
	}
	if repos.ContactChanges != nil && (nilDependency(repos.ContactChanges) || nilDependency(repos.CredentialMutations) || nilDependency(repos.AuthenticationGrants) || nilDependency(repos.Challenges) || nilDependency(cfg.ChallengeProtector)) {
		return fmt.Errorf("authentication: ContactChanges requires CredentialMutations, AuthenticationGrants, Challenges and ChallengeProtector: %w", sdk.ErrInvalidInput)
	}
	if cfg.RuntimeMode == environment.ModeProduction && !cfg.SessionCookie.Secure {
		return fmt.Errorf("authentication: production session cookies require Secure: %w", sdk.ErrInvalidInput)
	}
	if cfg.SessionCookie.Domain != "" {
		if cfg.OriginPolicy.HasNamespace() {
			return ErrCookieDomainWithNamespaceRule
		}
		if cfg.RuntimeMode == environment.ModeProduction && !cfg.CookieDomainAcknowledged {
			return ErrCookieDomainRefused
		}
	}
	return nil
}

// resolveMFARequired resolves MFAConfig.Required once, before any consumer
// reads it: an explicit value stands (source config); the zero value becomes
// admins when a passkey relying party and a UserAdminCheck are both wired, and
// none otherwise (source default).
func resolveMFARequired(cfg constructorConfig) (MFARequirement, string) {
	if cfg.MFARequired != MFARequiredDefault {
		return cfg.MFARequired, "config"
	}
	if !nilDependency(cfg.Passkey.RelyingParty) && cfg.UserAdminCheck != nil {
		return MFARequiredAdmins, "default"
	}
	return MFARequiredNone, "default"
}

// validateMFA checks the RESOLVED MFAConfig against the wiring in every mode,
// naming the field to change. ExternalConfig.SingleFactorAcknowledged belongs to
// NewExternalAuthenticator; New refuses it so it is never inert.
func validateMFA(cfg constructorConfig) error {
	if cfg.External != nil && cfg.External.SingleFactorAcknowledged {
		return fmt.Errorf("authentication: ExternalConfig.SingleFactorAcknowledged is for NewExternalAuthenticator; New reads IdentityConfig.SingleFactorAcknowledged: %w", sdk.ErrInvalidInput)
	}
	switch cfg.MFARequired {
	case MFARequiredNone:
		return nil
	case MFARequiredAdmins, MFARequiredAll:
	default:
		return fmt.Errorf("authentication: MFAConfig.Required %q is not none, admins or all: %w", cfg.MFARequired, sdk.ErrInvalidInput)
	}
	passkeys := !nilDependency(cfg.Passkey.RelyingParty)
	if cfg.MFARequired == MFARequiredAll && passkeys {
		return fmt.Errorf("%w: MFAConfig.Required all is not available beside WithPasskeys: native all waits for recovery codes (S4c); use admins meanwhile", ErrMFAUnsatisfiable)
	}
	if cfg.External == nil && (cfg.MFARequired == MFARequiredAll || !passkeys) {
		return fmt.Errorf("%w: MFAConfig.Required %q needs WithExternal or WithPasskeys, the AAL2 sources (all needs WithExternal)", ErrMFAUnsatisfiable, cfg.MFARequired)
	}
	if cfg.MFARequired == MFARequiredAdmins && cfg.UserAdminCheck == nil {
		return fmt.Errorf("%w: MFAConfig.Required admins needs AdministrationConfig.UserAdminCheck, or it gates nothing", ErrMFAUnsatisfiable)
	}
	if cfg.MFARequired == MFARequiredAll {
		var native []string
		if !cfg.PasswordFlowsDisabled {
			native = append(native, "PasswordConfig.PasswordFlowsDisabled is false")
		}
		if len(cfg.Passwordless) > 0 {
			native = append(native, "PasswordlessConfig.Passwordless is set")
		}
		if len(cfg.Providers) > 0 {
			native = append(native, "OAuthConfig.Providers is set")
		}
		if len(native) > 0 {
			return fmt.Errorf("%w: MFAConfig.Required all refuses native single-factor login (%s)", ErrMFAUnsatisfiable, strings.Join(native, "; "))
		}
	}
	return nil
}

// validatePasskeyConfig checks WithPasskeys against the wiring. A relying party
// needs the passkey store, the credential-mutation rail, the grants and the
// challenge rail with its protector. Views needs a relying party and
// BrowserConfig.Views. Production refuses a relying party bound to a
// development origin: a localhost or IP RP ID, or an Origins policy with an
// http namespace.
func validatePasskeyConfig(repos Repositories, cfg constructorConfig) error {
	pk := cfg.Passkey
	if pk.RelyingParty == nil {
		if pk.Views != nil {
			return fmt.Errorf("authentication: PasskeyConfig.Views requires PasskeyConfig.RelyingParty: %w", sdk.ErrInvalidInput)
		}
		return nil
	}
	if nilDependency(pk.RelyingParty) {
		return fmt.Errorf("authentication: PasskeyConfig.RelyingParty contains a typed nil: %w", sdk.ErrInvalidInput)
	}
	if nilDependency(repos.Passkeys) || nilDependency(repos.CredentialMutations) || nilDependency(repos.AuthenticationGrants) || nilDependency(repos.Challenges) || nilDependency(cfg.ChallengeProtector) {
		return ErrPasskeyReposRequired
	}
	if pk.Views != nil && (nilDependency(pk.Views) || nilDependency(cfg.Views)) {
		return fmt.Errorf("authentication: PasskeyConfig.Views requires BrowserConfig.Views: %w", sdk.ErrInvalidInput)
	}
	if cfg.RuntimeMode.IsProduction() {
		rp := pk.RelyingParty.Config()
		if rp.RPID == "localhost" || strings.HasSuffix(rp.RPID, ".localhost") || net.ParseIP(rp.RPID) != nil || rp.Origins.HasHTTPNamespace() {
			return fmt.Errorf("%w: RP ID %q", ErrPasskeyDevelopmentRelyingParty, rp.RPID)
		}
	}
	return nil
}

// An interface containing a nil pointer is still an absent dependency. Check it
// at the construction boundary rather than letting the first request panic.
func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func snapshotConfig(cfg constructorConfig) constructorConfig {
	cfg.AllowedOrigins = slices.Clone(cfg.AllowedOrigins)
	cfg.RedirectAllowlist = slices.Clone(cfg.RedirectAllowlist)
	cfg.Passwordless = slices.Clone(cfg.Passwordless)
	cfg.Providers = slices.Clone(cfg.Providers)
	return cfg
}
