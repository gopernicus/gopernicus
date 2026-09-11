package authentication

import (
	"fmt"
	"reflect"
	"slices"

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
