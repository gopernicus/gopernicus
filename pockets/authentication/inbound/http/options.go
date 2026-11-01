package authenticationhttp

import (
	"maps"
	"slices"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Option configures New before construction. Nil options are invalid.
type Option func(*adapterConfig)

// BrowserConfig configures browser routes, cookies, origins, and views.
type BrowserConfig struct {
	RefreshCookiePath string
	AllowedOrigins    []string
	OriginPolicy      web.OriginPolicy
	Views             Views
	HTMLPolicy        *HTMLResourcePolicy
}

// WithBrowser replaces the complete BrowserConfig group, including zero values.
func WithBrowser(value BrowserConfig) Option {
	value = cloneBrowserConfig(value)
	return func(c *adapterConfig) {
		value := cloneBrowserConfig(value)
		c.RefreshCookiePath = value.RefreshCookiePath
		c.AllowedOrigins = value.AllowedOrigins
		c.OriginPolicy = value.OriginPolicy
		c.Views = value.Views
		c.HTMLPolicy = value.HTMLPolicy
	}
}

func cloneBrowserConfig(value BrowserConfig) BrowserConfig {
	value.AllowedOrigins = slices.Clone(value.AllowedOrigins)
	if value.HTMLPolicy != nil {
		copy := *value.HTMLPolicy
		value.HTMLPolicy = &copy
	}
	return value
}

// WithInvitations replaces Invitations for this constructor.
func WithInvitations(value InvitationService) Option {
	return func(c *adapterConfig) {
		c.Invitations = value
	}
}

// WithAuthenticatorPolicy replaces Authenticator for this constructor.
func WithAuthenticatorPolicy(value AuthenticatorPolicy) Option {
	return func(c *adapterConfig) {
		c.Authenticator = value
	}
}

// WithListStrategy replaces ListStrategy for this constructor.
func WithListStrategy(value list.Strategy) Option {
	return func(c *adapterConfig) {
		c.ListStrategy = value
	}
}

// WithMachineGate replaces MachineGate for this constructor.
func WithMachineGate(value web.Middleware) Option {
	return func(c *adapterConfig) {
		c.MachineGate = value
	}
}

// WithUserErasureRoute opts in to POST /auth/admin/users/{id}/erase. It mounts
// only beside WithUserAdminCheck and a service whose erasure is wired; enabling
// it without either is sdk.ErrInvalidInput at New. False (the default) leaves
// the destructive route unmounted even where both exist.
func WithUserErasureRoute(enabled bool) Option {
	return func(c *adapterConfig) {
		c.UserErasureRoute = enabled
	}
}

// WithSecurityEventsRoute opts in to GET /auth/admin/security-events. It mounts
// only beside WithUserAdminCheck and a service whose security-event repository
// is wired; enabling it without either is sdk.ErrInvalidInput at New. False
// (the default) leaves the listing unmounted.
func WithSecurityEventsRoute(enabled bool) Option {
	return func(c *adapterConfig) {
		c.SecurityEventsRoute = enabled
	}
}

// WithPublicRouteLimits replaces the per-client-IP budgets of the bundled public
// routes; zero fields select their defaults and New validates the rest.
func WithPublicRouteLimits(value PublicRouteLimits) Option {
	return func(c *adapterConfig) {
		c.RouteLimits = value
	}
}

// WithRouteAuthentication replaces RouteAuth for this constructor.
func WithRouteAuthentication(value BundledRouteAuthentication) Option {
	return func(c *adapterConfig) {
		c.RouteAuth = value
	}
}

// WithLivenessFloorRelaxed lowers the liveness floor under the sensitive
// bundled groups (SessionHydration, SessionSecurityReads, CredentialManagement,
// MachineLifecycle, UserAdministration) from Live() to Stateless(), so a
// WithRouteAuthentication override's own tier governs. False (the default)
// keeps the Live() floor.
func WithLivenessFloorRelaxed(relaxed bool) Option {
	return func(c *adapterConfig) {
		c.LivenessFloorRelaxed = relaxed
	}
}

// WithInviteCheck sets the invitation create/list policy. With a resource rule it
// is the optional create-only refinement; without one it is required.
func WithInviteCheck(check InviteCheck) Option {
	return func(c *adapterConfig) { c.InviteCheck = check }
}

// WithInvitationResourceRule sets the resource rule for all bundled invitation
// management routes. Permissions and Can are wired together; the map is cloned.
func WithInvitationResourceRule(rule InvitationResourceRule) Option {
	rule.Permissions = maps.Clone(rule.Permissions)
	return func(c *adapterConfig) {
		c.ResourceRule = InvitationResourceRule{Permissions: maps.Clone(rule.Permissions), Can: rule.Can}
	}
}

// WithUserAdminCheck enables user administration routes with the supplied policy.
// Nil leaves the routes unmounted, even when the repository is available.
func WithUserAdminCheck(check UserAdminCheck) Option {
	return func(c *adapterConfig) { c.UserAdminCheck = check }
}

// WithAdministrationMFA requires AAL2 on /auth/admin/users* of every caller the
// UserAdminCheck admits: an admitted caller below AAL2 gets the RFC 9470 401
// and an mfa_required_denied row, a non-admin keeps the check's 403, and an API
// key gets the 403 mfa_credential_not_eligible unless WithMFAAPIKeysExempt.
// When the service has passkeys wired it also gates the machine
// lifecycle routes after the MachineGate (gate machine), since a passkey can
// now satisfy it there. It gates no other route. Without WithUserAdminCheck it
// is sdk.ErrInvalidInput at New.
func WithAdministrationMFA() Option {
	return func(c *adapterConfig) { c.AdministrationMFA = true }
}

// EnrollmentFloor is the first-party enrollment floor of MFAConfig.Required
// all beside passkeys or TOTP (WithMFAEnrollmentFloor).
type EnrollmentFloor struct {
	// Enabled runs the floor in every RequirePrincipal without
	// AllowMFAEnrollment(): a first-party access token below AAL2 passes only
	// while its mfa_enroll_by lies ahead. Past it the JSON answer is a 403
	// mfa_enrollment_required carrying enroll_by and a Browser() cookie is
	// redirected to the enrollment page (the TOTP enrollment page, else the
	// passkeys page, when their views are wired); a token whose holder holds a
	// second factor (mfa_enrolled) gets the RequireMFA step-up answer; a token
	// carrying neither claim gets the plain JSON 401 (a Browser() cookie the
	// login redirect with recover=1), so a refresh mints the claim. It also
	// gates the OAuth2 authorize page and the machine lifecycle routes at
	// AAL2. It needs a service with passkeys or TOTP wired.
	Enabled bool
}

// WithMFAEnrollmentFloor replaces the EnrollmentFloor.
func WithMFAEnrollmentFloor(value EnrollmentFloor) Option {
	return func(c *adapterConfig) { c.EnrollmentFloor = value }
}

// WithGlobalSessionRevocationRoute opts in to POST
// /auth/admin/sessions/revoke-all: the service's RevokeAllSessions behind its
// own Live authenticator (a first-party access token or an external token; no
// API key, no delegated token), the browser-safe
// gate, the UserAdminCheck with UserAdminRevokeAllSessions (and the admins MFA
// gate) and one call a minute per actor. It mounts only beside
// WithUserAdminCheck and a service whose global revocation is wired; enabling
// it without either is sdk.ErrInvalidInput at New.
func WithGlobalSessionRevocationRoute(enabled bool) Option {
	return func(c *adapterConfig) { c.GlobalSessionRevocationRoute = enabled }
}

// WithMFAAPIKeysExempt lets an API key pass every MFA gate (MFAConfig.
// APIKeysExempt): RequireMFA(), the admin and the machine gates. A key cannot
// hold AAL2, so RequireMFA() and the machine gate admit it unverified, and the
// admin gate admits a self-acting key by default and an act-as-user key only
// under an override that admits one. False (the default) refuses a key there
// with a 403 mfa_credential_not_eligible.
func WithMFAAPIKeysExempt(exempt bool) Option {
	return func(c *adapterConfig) { c.MFAAPIKeysExempt = exempt }
}

// WithPasskeyViews mounts the passkey pages — GET /auth/account/passkeys, its
// form lane POST /auth/passkeys/{id}, and GET /auth/step-up/passkey — and turns
// a Browser() RequireMFA() denial of an AAL1 cookie into a redirect to the
// step-up page. It requires a service with passkeys wired and BrowserConfig
// Views (sdk.ErrInvalidInput at New). Nil leaves the passkey routes JSON-only.
func WithPasskeyViews(value PasskeyViews) Option {
	return func(c *adapterConfig) { c.PasskeyViews = value }
}

// WithTOTPViews mounts the TOTP pages — GET /auth/account/totp, GET
// /auth/account/totp/enroll, the chooser GET /auth/step-up/mfa and the remove
// form lane POST /auth/totp — adds the form arms of the TOTP POST routes, and
// turns a Browser() RequireMFA() denial of an AAL1 cookie into a redirect to the
// chooser. It requires a service with TOTP wired and BrowserConfig Views
// (sdk.ErrInvalidInput at New). Nil leaves the TOTP routes JSON-only.
func WithTOTPViews(value TOTPViews) Option {
	return func(c *adapterConfig) { c.TOTPViews = value }
}

// WithMFAMethods restricts the AAL2 sources every MFA gate admits (MFAConfig.
// Methods): RequireMFA(), the admin and machine gates, and the chooser's
// offers. Empty admits any source.
func WithMFAMethods(methods ...authlogic.MFAMethod) Option {
	methods = slices.Clone(methods)
	return func(c *adapterConfig) { c.MFAMethods = slices.Clone(methods) }
}
