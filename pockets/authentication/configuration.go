package authentication

import (
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"slices"
	"strings"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
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
// admins when a native AAL2 source (a passkey relying party or a TOTP
// encrypter) and a UserAdminCheck are both wired, and none otherwise (source
// default).
func resolveMFARequired(cfg constructorConfig) (MFARequirement, string) {
	if cfg.MFARequired != MFARequiredDefault {
		return cfg.MFARequired, "config"
	}
	if nativeMFA(cfg) && cfg.UserAdminCheck != nil {
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
	wired := mfaSources(cfg)
	for _, m := range cfg.MFAMethods {
		switch m {
		case MFAMethodPasskey, MFAMethodTOTP, MFAMethodIdP:
		default:
			return fmt.Errorf("authentication: MFAConfig.Methods %q is not passkey, totp or idp: %w", m, sdk.ErrInvalidInput)
		}
		if !slices.Contains(wired, m) {
			return fmt.Errorf("%w: MFAConfig.Methods names %s, which is not wired (passkey needs WithPasskeys, totp WithTOTP, idp WithExternal)", ErrMFAUnsatisfiable, m)
		}
	}
	switch cfg.MFARequired {
	case MFARequiredNone:
		return nil
	case MFARequiredAdmins, MFARequiredAll:
	default:
		return fmt.Errorf("authentication: MFAConfig.Required %q is not none, admins or all: %w", cfg.MFARequired, sdk.ErrInvalidInput)
	}
	native := nativeMFA(cfg)
	if nativeAll(cfg) {
		return validateMFAEnrollment(cfg)
	}
	if cfg.External == nil && (cfg.MFARequired == MFARequiredAll || !native) {
		return fmt.Errorf("%w: MFAConfig.Required %q needs WithExternal, WithPasskeys or WithTOTP, the AAL2 sources (all needs WithExternal)", ErrMFAUnsatisfiable, cfg.MFARequired)
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

// validateMFAEnrollment checks native all: a first-party AAL2 source
// MFAConfig.Methods admits, an EnrollmentStart, and an EnrollmentGrace within
// bounds. Native single-factor login stays on: the grace is its bound.
func validateMFAEnrollment(cfg constructorConfig) error {
	if len(cfg.MFAMethods) > 0 && !slices.Contains(cfg.MFAMethods, MFAMethodPasskey) && !slices.Contains(cfg.MFAMethods, MFAMethodTOTP) {
		return fmt.Errorf("%w: MFAConfig.Required all beside WithPasskeys or WithTOTP needs MFAConfig.Methods to admit passkey or totp, or no first-party session can reach AAL2", ErrMFAUnsatisfiable)
	}
	if cfg.MFAEnrollmentStart.IsZero() {
		return ErrMFAEnrollmentStartRequired
	}
	if cfg.MFAEnrollmentGrace < 0 || cfg.MFAEnrollmentGrace > MaxMFAEnrollmentGrace {
		return fmt.Errorf("%w (got %s)", ErrMFAEnrollmentGraceInvalid, cfg.MFAEnrollmentGrace)
	}
	return nil
}

// resolveMFAEnrollmentGrace resolves a zero MFAConfig.EnrollmentGrace to
// DefaultMFAEnrollmentGrace under native all; anywhere else it is unread.
func resolveMFAEnrollmentGrace(cfg constructorConfig) time.Duration {
	if nativeAll(cfg) && cfg.MFAEnrollmentGrace == 0 {
		return DefaultMFAEnrollmentGrace
	}
	return cfg.MFAEnrollmentGrace
}

// resolveRegistrationSource names where IdentityConfig.Registration came
// from: config when set, default for the zero value. The value itself is
// validated by the service constructor.
func resolveRegistrationSource(cfg constructorConfig) string {
	if cfg.Registration != RegistrationOpen {
		return "config"
	}
	return "default"
}

// resolveAPIKeyMaxTTL resolves AdministrationConfig.APIKeyMaxTTL once: a
// positive value stands (source config); zero is DefaultAPIKeyMaxTTL in
// production unless APIKeyUnlimitedTTLAcknowledged keeps it unlimited (source
// acknowledged), and unlimited in development (source default). A negative
// value is sdk.ErrInvalidInput.
func resolveAPIKeyMaxTTL(cfg constructorConfig) (time.Duration, string, error) {
	switch {
	case cfg.APIKeyMaxTTL < 0:
		return 0, "", fmt.Errorf("authentication: AdministrationConfig.APIKeyMaxTTL (AUTH_API_KEY_MAX_TTL) is negative: %w", sdk.ErrInvalidInput)
	case cfg.APIKeyMaxTTL > 0:
		return cfg.APIKeyMaxTTL, "config", nil
	case !cfg.RuntimeMode.IsProduction():
		return 0, "default", nil
	case cfg.APIKeyUnlimitedTTLAcknowledged:
		return 0, "acknowledged", nil
	}
	return DefaultAPIKeyMaxTTL, "default", nil
}

// logSessionLifetime logs the one boot line naming the resolved session
// lifetimes: the access and refresh lifetimes, the idle timeout (off when
// zero) with its source, what counts as activity, which sessions it governs,
// and whether the liveness floor under the sensitive bundled groups is
// enforced. An environment value and a literal are both source config: the
// pocket never reads the process environment.
func logSessionLifetime(cfg constructorConfig, policy authlogic.SessionsConfig, log *slog.Logger) {
	idle, source := "off", "default"
	if policy.IdleTimeout > 0 {
		idle, source = policy.IdleTimeout.String(), "config"
	}
	floor := "enforced"
	if cfg.LivenessFloorRelaxed {
		floor = "relaxed"
	}
	log.Info("auth: session lifetime", "access_token_ttl", policy.AccessTokenTTL.String(), "refresh_ttl", policy.RefreshTTL.String(), "idle_timeout", idle, "idle_source", source, "activity", "refresh_rotation", "idle_scope", "first_party", "live_floor", floor)
}

// keyRole describes one key-bearing collaborator for the auth: keys boot line:
// its kind (ring, single, opaque or none) and, for a ring, the ids it reports.
// Key ids are configuration, never key material.
type keyRole struct {
	role   string
	prefix string
	kind   string
	active string
	ids    []string
	legacy string
}

// describeKeyRole classifies v: a KeyRingReporter (sdk v0.23.0's AESGCMRing,
// the ring signer, the bundled challenge protector) is a ring with its ids and
// legacy id; any other value with an active key id (an older AESGCMRing, a
// third-party cryptids.KeyedEncrypter or protection.ChallengeProtector) is a
// ring with its active id only and no legacy read to report; the sdk's
// single-key *cryptids.AESGCM is single; nil is none; anything else is opaque,
// never a guessed single.
func describeKeyRole(role, prefix string, v any) keyRole {
	d := keyRole{role: role, prefix: prefix, kind: "opaque"}
	if nilDependency(v) {
		d.kind = "none"
		return d
	}
	switch r := v.(type) {
	case authlogic.KeyRingReporter:
		d.kind, d.active, d.ids, d.legacy = "ring", r.ActiveKeyID(), r.KeyIDs(), r.LegacyKeyID()
	case interface{ ActiveKeyID() string }:
		d.kind, d.active = "ring", r.ActiveKeyID()
	case *cryptids.AESGCM:
		d.kind = "single"
	}
	return d
}

// keyRoles describes the pocket's key-bearing collaborators in boot-line
// order.
func keyRoles(cfg constructorConfig) []keyRole {
	return []keyRole{
		describeKeyRole("jwt_signer", "jwt", cfg.TokenSigner),
		describeKeyRole("delivery_encrypter", "delivery", cfg.DeliveryEncrypter),
		describeKeyRole("token_encrypter", "token", cfg.TokenEncrypter),
		describeKeyRole("challenge_protector", "challenge", cfg.ChallengeProtector),
		describeKeyRole("totp_encrypter", "totp", cfg.TOTP.Encrypter),
	}
}

// logOAuth2 logs the one auth: oauth2 boot line when WithOAuth2 is wired:
// whether delegated access tokens carry the approving session's aal and
// methods (OAuth2Config.DelegatedAssurance).
func logOAuth2(cfg constructorConfig, log *slog.Logger) {
	if cfg.OAuth2 == nil {
		return
	}
	delegated := "off"
	if cfg.OAuth2.Server.DelegatedAssurance {
		delegated = "on"
	}
	log.Info("auth: oauth2", "delegated_assurance", delegated)
}

// logKeys logs the one auth: keys boot line: per role its kind and, for a
// ring, the active id, the reported ids and whether a legacy id reads
// pre-ring values; the identifier keyer's kind; and the first-party token
// issuer and audience (none when unset). It never logs key material.
func logKeys(cfg constructorConfig, roles []keyRole, log *slog.Logger) {
	attrs := make([]any, 0, 4*len(roles)+6)
	for _, r := range roles {
		attrs = append(attrs, r.role, r.kind)
		if r.kind != "ring" {
			continue
		}
		attrs = append(attrs, r.prefix+"_active_key_id", r.active)
		if r.ids != nil {
			attrs = append(attrs, r.prefix+"_key_ids", strings.Join(r.ids, ","))
		}
		if r.role != "challenge_protector" {
			legacy := "no"
			if r.legacy != "" {
				legacy = "yes"
			}
			attrs = append(attrs, r.prefix+"_legacy", legacy)
		}
	}
	keyer := "opaque"
	if _, ok := cfg.IdentifierKeyer.(*protection.HMACIdentifierKeyer); ok {
		keyer = "single"
	}
	if nilDependency(cfg.IdentifierKeyer) {
		keyer = "none"
	}
	issuer, audience := cfg.TokenIssuer, cfg.TokenAudience
	if issuer == "" {
		issuer = "none"
	}
	if audience == "" {
		audience = "none"
	}
	attrs = append(attrs, "identifier_keyer", keyer, "token_issuer", issuer, "token_audience", audience)
	log.Info("auth: keys", attrs...)
}

// nativeMFA reports whether a native AAL2 source is wired: a passkey relying
// party or a TOTP encrypter.
func nativeMFA(cfg constructorConfig) bool {
	return !nilDependency(cfg.Passkey.RelyingParty) || !nilDependency(cfg.TOTP.Encrypter)
}

// nativeAll reports native all: MFAConfig.Required all beside a native AAL2
// source, where first-party sessions are gated too.
func nativeAll(cfg constructorConfig) bool {
	return cfg.MFARequired == MFARequiredAll && nativeMFA(cfg)
}

// mfaSources lists the wired AAL2 sources in the boot line's order.
func mfaSources(cfg constructorConfig) []MFAMethod {
	var out []MFAMethod
	if !nilDependency(cfg.Passkey.RelyingParty) {
		out = append(out, MFAMethodPasskey)
	}
	if !nilDependency(cfg.TOTP.Encrypter) {
		out = append(out, MFAMethodTOTP)
	}
	if cfg.External != nil {
		out = append(out, MFAMethodIdP)
	}
	return out
}

// validateTOTPConfig checks WithTOTP against the wiring. An encrypter needs
// the TOTP store, the credential-mutation rail, the grants and the challenge
// protector (recovery codes are digested under its pepper). Views needs an
// encrypter and BrowserConfig.Views. Nothing here runs without WithTOTP.
func validateTOTPConfig(repos Repositories, cfg constructorConfig) error {
	t := cfg.TOTP
	if t.Encrypter == nil {
		if t.Views != nil {
			return fmt.Errorf("authentication: TOTPConfig.Views requires TOTPConfig.Encrypter: %w", sdk.ErrInvalidInput)
		}
		return nil
	}
	if nilDependency(t.Encrypter) {
		return fmt.Errorf("authentication: TOTPConfig.Encrypter contains a typed nil: %w", sdk.ErrInvalidInput)
	}
	if nilDependency(repos.TOTP) || nilDependency(repos.CredentialMutations) || nilDependency(repos.AuthenticationGrants) || nilDependency(cfg.ChallengeProtector) {
		return ErrTOTPReposRequired
	}
	if t.Views != nil && (nilDependency(t.Views) || nilDependency(cfg.Views)) {
		return fmt.Errorf("authentication: TOTPConfig.Views requires BrowserConfig.Views: %w", sdk.ErrInvalidInput)
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
