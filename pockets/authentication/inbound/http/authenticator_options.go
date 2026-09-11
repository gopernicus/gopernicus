package authenticationhttp

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// AuthenticatorOption configures NewAuthenticator before construction. Nil options are invalid.
type AuthenticatorOption func(*authenticatorConfig)

// WithCookies replaces cookie policy; empty name/path fields receive their documented defaults.
func WithCookies(value CookieConfig) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.Cookie = value
	}
}

// WithBrowserLoginPath sets the browser denial destination; empty selects /auth/login.
func WithBrowserLoginPath(value string) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.BrowserLoginPath = value
	}
}

// WithLimiter supplies the HTTP rate limiter. Nil selects a memory limiter, which production rejects.
func WithLimiter(value ratelimiter.Limiter) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.Limiter = value
	}
}

// WithAuthenticatorLogger supplies the HTTP logger; nil selects slog.Default().
func WithAuthenticatorLogger(value *slog.Logger) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.Logger = value
	}
}

// AuthenticatorPolicy groups optional cookie, browser denial and limiter policy.
// Runtime mode remains an explicit constructor argument.
type AuthenticatorPolicy struct {
	Cookie           CookieConfig
	BrowserLoginPath string
	Limiter          ratelimiter.Limiter
	Logger           *slog.Logger
}
