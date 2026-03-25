// Package authentication provides HTTP handlers for authentication endpoints.
//
// It bridges the [authentication.Authenticator] to HTTP using standard handler
// signatures and the sdk/web helpers for JSON encode/decode.
//
// Routes are registered via [Bridge.HttpRoutes] onto a [*web.RouteGroup].
// Includes both web (browser redirect + cookie) and mobile (JSON + flow secret)
// OAuth flows.
//
//	ab := authentication.New(log, cfg, authSvc, rateLimiter)
//	authGroup := handler.Group("/auth")
//	ab.HttpRoutes(authGroup, authMid)
package authentication

import (
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

const cookiePath = "/"

// Config holds bridge configuration from environment variables.
type Config struct {
	CookieSecure       bool          `env:"AUTH_COOKIE_SECURE" default:"false"`
	FrontendURL        string        `env:"FRONTEND_URL"`
	Environment        string        `env:"ENV" default:"development"`
	AccessTokenExpiry  time.Duration `env:"ACCESS_TOKEN_EXPIRY" default:"30m"`
	RefreshTokenExpiry time.Duration `env:"REFRESH_TOKEN_EXPIRY" default:"720h"`
	CallbackPrefix     string        `env:"AUTH_CALLBACK_PREFIX" default:"/api/v1/auth"`
	MobileRedirectURI  string        `env:"OAUTH_MOBILE_REDIRECT_URI"` // custom scheme URI for mobile (e.g. myapp://oauth-callback)
}

// Bridge is the HTTP handler bridge for authentication endpoints.
type Bridge struct {
	authenticator *authentication.Authenticator
	log           *slog.Logger
	rateLimiter   *ratelimiter.RateLimiter

	cookieSecure       bool
	frontendURL        string
	sessionCookieName  string
	refreshCookieName  string
	accessTokenExpiry  time.Duration
	refreshTokenExpiry time.Duration
	callbackPrefix     string
	mobileRedirectURI  string
}

// Option configures a [Bridge].
type Option func(*Bridge)

// WithCookieSecure overrides the cookie Secure flag.
func WithCookieSecure(secure bool) Option {
	return func(b *Bridge) { b.cookieSecure = secure }
}

// WithFrontendURL overrides the frontend URL for OAuth redirects.
func WithFrontendURL(url string) Option {
	return func(b *Bridge) { b.frontendURL = url }
}

// New creates a new authentication bridge.
func New(log *slog.Logger, cfg Config, authenticator *authentication.Authenticator, rateLimiter *ratelimiter.RateLimiter, opts ...Option) *Bridge {
	b := &Bridge{
		authenticator:      authenticator,
		log:                log,
		rateLimiter:        rateLimiter,
		cookieSecure:       cfg.CookieSecure,
		frontendURL:        cfg.FrontendURL,
		sessionCookieName:  authenticator.SessionTokenName(),
		refreshCookieName:  authenticator.RefreshTokenName(),
		accessTokenExpiry:  cfg.AccessTokenExpiry,
		refreshTokenExpiry: cfg.RefreshTokenExpiry,
		callbackPrefix:     cfg.CallbackPrefix,
		mobileRedirectURI:  cfg.MobileRedirectURI,
	}

	if cfg.Environment == "production" {
		b.cookieSecure = true
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}
