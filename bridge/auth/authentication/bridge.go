// Package authentication provides HTTP handlers for authentication endpoints.
//
// It bridges the [authentication.Authenticator] to HTTP using standard handler
// signatures and the sdk/web helpers for JSON encode/decode.
//
// Routes are registered via [Bridge.AddHttpRoutes] onto a [*web.RouteGroup].
// Includes both web (browser redirect + cookie) and mobile (JSON + flow secret)
// OAuth flows.
//
//	ab := authentication.New(log, cfg, authSvc, rateLimiter)
//	authGroup := handler.Group("/auth")
//	ab.AddHttpRoutes(authGroup, authMid)
package authentication

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/bridge/transit/allowlist"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

const (
	cookiePath = "/"

	// defaultResetPath is appended to the first allowed frontend when the
	// caller does not supply a valid reset_url. Frontends conventionally
	// mount the password-reset page at this path.
	defaultResetPath = "/reset-password"
)

// Config holds bridge configuration from environment variables.
type Config struct {
	CookieSecure       bool          `env:"AUTH_COOKIE_SECURE" default:"false"`
	CookieDomain       string        `env:"AUTH_COOKIE_DOMAIN"`     // e.g. ".example.com" for cross-subdomain cookies
	CallbackBaseURL    string        `env:"AUTH_CALLBACK_BASE_URL"` // e.g. "https://api.example.com" for OAuth callbacks
	Environment        string        `env:"ENV" default:"development"`
	AccessTokenExpiry  time.Duration `env:"ACCESS_TOKEN_EXPIRY" default:"30m"`
	RefreshTokenExpiry time.Duration `env:"REFRESH_TOKEN_EXPIRY" default:"720h"`
	CallbackPrefix     string        `env:"AUTH_CALLBACK_PREFIX" default:"/api/v1/auth"`
	MobileRedirectURI  string        `env:"OAUTH_MOBILE_REDIRECT_URI"` // custom scheme URI for mobile (e.g. myapp://oauth-callback)
	AllowedFrontends   []string      `env:"ALLOWED_FRONTENDS" envSeparator:","`
}

// Bridge is the HTTP handler bridge for authentication endpoints.
type Bridge struct {
	authenticator *authentication.Authenticator
	log           *slog.Logger
	rateLimiter   *ratelimiter.RateLimiter

	cookieSecure       bool
	cookieDomain       string
	callbackBaseURL    string
	sessionCookieName  string
	refreshCookieName  string
	accessTokenExpiry  time.Duration
	refreshTokenExpiry time.Duration
	callbackPrefix     string
	mobileRedirectURI  string
	originMatcher      *allowlist.Matcher
}

// Option configures a [Bridge].
type Option func(*Bridge)

// WithCookieSecure overrides the cookie Secure flag.
func WithCookieSecure(secure bool) Option {
	return func(b *Bridge) { b.cookieSecure = secure }
}

// WithCookieDomain sets the cookie Domain attribute for cross-subdomain sharing.
func WithCookieDomain(domain string) Option {
	return func(b *Bridge) { b.cookieDomain = domain }
}

// WithCallbackBaseURL sets the base URL for OAuth callbacks (prevents header injection).
func WithCallbackBaseURL(url string) Option {
	return func(b *Bridge) { b.callbackBaseURL = url }
}

// WithAllowedFrontends sets the origin allow-list for client-supplied URLs.
func WithAllowedFrontends(origins []string) Option {
	return func(b *Bridge) {
		m, err := allowlist.New(origins)
		if err != nil {
			panic("authentication: invalid allowed frontends: " + err.Error())
		}
		b.originMatcher = m
	}
}

// New creates a new authentication bridge.
func New(log *slog.Logger, cfg Config, authenticator *authentication.Authenticator, rateLimiter *ratelimiter.RateLimiter, opts ...Option) *Bridge {
	originMatcher, err := allowlist.New(cfg.AllowedFrontends)
	if err != nil {
		panic("authentication: invalid ALLOWED_FRONTENDS: " + err.Error())
	}

	b := &Bridge{
		authenticator:      authenticator,
		log:                log,
		rateLimiter:        rateLimiter,
		cookieSecure:       cfg.CookieSecure,
		cookieDomain:       cfg.CookieDomain,
		callbackBaseURL:    cfg.CallbackBaseURL,
		sessionCookieName:  authenticator.SessionTokenName(),
		refreshCookieName:  authenticator.RefreshTokenName(),
		accessTokenExpiry:  cfg.AccessTokenExpiry,
		refreshTokenExpiry: cfg.RefreshTokenExpiry,
		callbackPrefix:     cfg.CallbackPrefix,
		mobileRedirectURI:  cfg.MobileRedirectURI,
		originMatcher:      originMatcher,
	}

	if cfg.Environment == "production" {
		b.cookieSecure = true
	}

	for _, opt := range opts {
		opt(b)
	}

	if b.originMatcher.Empty() && log != nil {
		log.Warn("ALLOWED_FRONTENDS is unset — client-supplied URLs are not validated; set ALLOWED_FRONTENDS in production")
	}
	if b.callbackBaseURL == "" && log != nil {
		log.Warn("AUTH_CALLBACK_BASE_URL is unset — OAuth callback URLs derived from request headers; set AUTH_CALLBACK_BASE_URL in production")
	}

	return b
}

// resolveResetURL chooses the password-reset frontend URL.
//
// When an allow-list is configured, a client-supplied URL is honored only if
// its origin is on the list; otherwise the first allowed frontend is used
// with [defaultResetPath] appended. In legacy mode (no allow-list) the
// client value is returned unchanged — empty included — preserving the
// pre-allowlist behavior where the email may fall back to a raw token.
func (b *Bridge) resolveResetURL(provided string) string {
	if provided != "" && b.originMatcher.Matches(provided) {
		return provided
	}
	if base := b.originMatcher.Default(); base != "" {
		return stripDefaultPort(strings.TrimRight(base, "/")) + defaultResetPath
	}
	return provided
}

// stripDefaultPort removes :443 from https and :80 from http origins so the
// rendered URL is clean for users (the allowlist canonicalizes ports for
// matching, but emails should show the host without the implied port).
func stripDefaultPort(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return origin
	}
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		u.Host = u.Hostname()
	}
	return u.String()
}

// resolveOrigin chooses the post-OAuth redirect origin. A client-supplied
// origin (carried in OAuth state from /oauth/initiate) is honored only when it
// is on the allow-list; an unlisted origin falls back to a known frontend.
// Without this check an attacker who seeded AppOrigin at /oauth/initiate could
// turn the authenticated callback into an open redirect that leaks the session
// cookies just set (oauth.go setSessionCookies → resolveOrigin → 307). This
// mirrors resolveResetURL's discipline. In legacy mode (no allow-list
// configured — the startup warning covers it) the client origin is returned
// unchanged, preserving pre-allowlist behavior.
func (b *Bridge) resolveOrigin(appOrigin string) string {
	if appOrigin != "" && (b.originMatcher.Empty() || b.originMatcher.Matches(appOrigin)) {
		return appOrigin
	}
	// Unlisted/empty origin: fall back to a known-safe allowed frontend.
	origins := b.originMatcher.Origins()
	if len(origins) > 0 {
		return origins[0]
	}
	return ""
}

// BuildAllowedRedirectURIs constructs the OAuth redirect URI allowlist from
// bridge configuration. Call this when constructing the core authenticator
// and pass the result to [authentication.WithAllowedRedirectURIs].
//
// Returns nil if callbackBaseURL is empty (no explicit base URL configured).
// Each provider generates two URIs: the web callback and the mobile redirect proxy.
func BuildAllowedRedirectURIs(callbackBaseURL, callbackPrefix string, providers []string) []string {
	if callbackBaseURL == "" {
		return nil
	}

	uris := make([]string, 0, len(providers)*2)
	for _, p := range providers {
		uris = append(uris, callbackBaseURL+callbackPrefix+"/oauth/callback/"+p)
		uris = append(uris, callbackBaseURL+callbackPrefix+"/oauth/mobile-redirect/"+p)
	}
	return uris
}

// buildCallbackURI constructs the OAuth callback URL. Uses callbackBaseURL if
// configured, otherwise derives from request headers (legacy, vulnerable to injection).
func (b *Bridge) buildCallbackURI(r *http.Request, provider string) string {
	if b.callbackBaseURL != "" {
		return b.callbackBaseURL + b.callbackPrefix + "/oauth/callback/" + provider
	}
	// Legacy fallback: derive from request headers.
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s/oauth/callback/%s", scheme, r.Host, b.callbackPrefix, provider)
}
