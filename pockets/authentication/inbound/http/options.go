package authenticationhttp

import (
	"maps"
	"slices"

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

// WithRouteAuthentication replaces RouteAuth for this constructor.
func WithRouteAuthentication(value BundledRouteAuthentication) Option {
	return func(c *adapterConfig) {
		c.RouteAuth = value
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
