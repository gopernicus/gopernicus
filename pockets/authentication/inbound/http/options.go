package authenticationhttp

import (
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
