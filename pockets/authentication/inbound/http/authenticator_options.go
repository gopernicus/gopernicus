package authenticationhttp

import (
	"log/slog"
	"slices"

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

// WithExternalCookies names the external identity providers' own session
// cookies the cookie transport reads, in order, after the native session cookie.
// A name shared with the native session, refresh or CSRF cookie is rejected.
func WithExternalCookies(names ...string) AuthenticatorOption {
	names = slices.Clone(names)
	return func(c *authenticatorConfig) {
		c.ExternalCookies = slices.Clone(names)
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

// WithLimiterFailOpen selects the outage policy of RateLimitByIP and the
// bundled route budgets. False (the default) fails CLOSED in every mode: a
// limiter error answers JSON 503 + Retry-After. True admits the request and
// logs a WARN per failure.
func WithLimiterFailOpen(enabled bool) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.LimiterFailOpen = enabled
	}
}

// WithAuthenticatorLogger supplies the HTTP logger; nil selects slog.Default().
func WithAuthenticatorLogger(value *slog.Logger) AuthenticatorOption {
	return func(c *authenticatorConfig) {
		c.Logger = value
	}
}

// AuthenticatorPolicy groups optional cookie (native and provider), browser denial and limiter policy.
// Runtime mode remains an explicit constructor argument.
type AuthenticatorPolicy struct {
	Cookie           CookieConfig
	ExternalCookies  []string
	BrowserLoginPath string
	Limiter          ratelimiter.Limiter
	LimiterFailOpen  bool
	Logger           *slog.Logger
}
