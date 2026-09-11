package authenticationhttp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	defaultRefreshCookiePath = "/auth"
	defaultBrowserLoginPath  = "/auth/login"
)

type (
	Principal      = authlogic.Principal
	Credential     = authlogic.Credential
	CredentialKind = authlogic.CredentialKind
	Transport      = authlogic.Transport
	TokenPair      = authlogic.TokenPair
)

const (
	CredentialAccessToken = authlogic.CredentialAccessToken
	CredentialAPIKey      = authlogic.CredentialAPIKey
	TransportHeader       = authlogic.TransportHeader
	TransportCookie       = authlogic.TransportCookie
)

// Adapter adapts verified credentials to HTTP cookies, middleware and optional routes.
type Adapter struct {
	routes           *mountDeps
	service          CredentialService
	cookie           CookieConfig
	browserLoginPath string
	limiter          ratelimiter.Limiter
	logger           *slog.Logger
}

// authenticatorConfig holds the HTTP adapter's resolved policy and dependencies.
type authenticatorConfig struct {
	RuntimeMode      environment.Mode
	Cookie           CookieConfig
	BrowserLoginPath string
	Limiter          ratelimiter.Limiter
	Logger           *slog.Logger
}

func newAuthenticator(service CredentialService, cfg authenticatorConfig) *Adapter {
	if cfg.Cookie.Name == "" {
		cfg.Cookie.Name = "session"
	}
	if cfg.Cookie.Path == "" {
		cfg.Cookie.Path = "/"
	}
	if cfg.Cookie.RefreshPath == "" {
		cfg.Cookie.RefreshPath = defaultRefreshCookiePath
	}
	if cfg.BrowserLoginPath == "" {
		cfg.BrowserLoginPath = defaultBrowserLoginPath
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Adapter{service: service, cookie: cfg.Cookie, browserLoginPath: cfg.BrowserLoginPath, limiter: cfg.Limiter, logger: cfg.Logger}
}

func clientIPFromContext(ctx context.Context) string {
	ip, _ := authlogic.ClientInfoFromContext(ctx)
	return ip
}

// CredentialService authenticates supplied proof and reads the resulting context.
// A custom host adapter can implement it without exposing repository internals.
type CredentialService interface {
	Authenticate(context.Context, CredentialKind, Transport, string) (context.Context, bool)
	RequireLive(context.Context) (context.Context, bool)
	MachineEnabled() bool
	SessionLifetime() time.Duration
	CurrentCredential(context.Context) (Credential, bool)
	CurrentPrincipal(context.Context) (Principal, bool)
	CurrentUser(context.Context) (string, bool)
	CurrentSessionID(context.Context) (string, bool)
}

func NewAuthenticator(service CredentialService, runtimeMode environment.Mode, opts ...AuthenticatorOption) (*Adapter, error) {
	cfg := authenticatorConfig{}
	cfg.RuntimeMode = runtimeMode
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication NewAuthenticator: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}

	if err := environment.ValidateMode(cfg.RuntimeMode); err != nil {
		return nil, err
	}
	if cfg.RuntimeMode == environment.ModeProduction && !cfg.Cookie.Secure {
		return nil, fmt.Errorf("authentication HTTP: production cookies require Secure: %w", sdk.ErrInvalidInput)
	}

	if nilDependency(service) {
		return nil, fmt.Errorf("authentication HTTP: credential service is required: %w", sdk.ErrInvalidInput)
	}
	if !validCookieConfig(cfg.Cookie) {
		return nil, fmt.Errorf("authentication HTTP: invalid cookie configuration: %w", sdk.ErrInvalidInput)
	}
	if cfg.BrowserLoginPath != "" && redirect.SafeRelativePath(cfg.BrowserLoginPath) != cfg.BrowserLoginPath {
		return nil, fmt.Errorf("authentication HTTP: invalid browser login path: %w", sdk.ErrInvalidInput)
	}
	if cfg.Limiter != nil && nilDependency(cfg.Limiter) {
		return nil, fmt.Errorf("authentication HTTP: limiter contains a typed nil: %w", sdk.ErrInvalidInput)
	}
	if cfg.Limiter == nil {
		cfg.Limiter = ratelimiter.NewMemory()
	}
	if cfg.RuntimeMode == environment.ModeProduction {
		_, local := cfg.Limiter.(*ratelimiter.Memory)
		if reporter, ok := cfg.Limiter.(authlogic.RateLimiterDurabilityReporter); ok {
			local = reporter.RateLimiterDurability().InProcessOnly
		}
		if local {
			return nil, fmt.Errorf("authentication HTTP: production requires a shared rate limiter: %w", sdk.ErrInvalidInput)
		}
	}

	return newAuthenticator(service, cfg), nil
}
