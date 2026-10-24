package authenticationhttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
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
	LivenessTier   = authlogic.LivenessTier
)

const (
	CredentialAccessToken   = authlogic.CredentialAccessToken
	CredentialAPIKey        = authlogic.CredentialAPIKey
	CredentialExternalToken = authlogic.CredentialExternalToken
	TransportHeader         = authlogic.TransportHeader
	TransportCookie         = authlogic.TransportCookie
	LivenessOnMutation      = authlogic.LivenessOnMutation
	LivenessLive            = authlogic.LivenessLive
	LivenessStateless       = authlogic.LivenessStateless
)

var (
	_ CredentialService = (*authlogic.Service)(nil)
	_ CredentialService = (*authlogic.ExternalAuthenticator)(nil)
)

// Adapter adapts verified credentials to HTTP cookies, middleware and optional routes.
type Adapter struct {
	routes           *mountDeps
	service          CredentialService
	cookie           CookieConfig
	externalCookies  []string
	browserLoginPath string
	// browserLoginSet records an explicit WithBrowserLoginPath: the default
	// /auth/login is not mounted on an external-only host.
	browserLoginSet bool
	limiter         ratelimiter.Limiter
	// limiterFailOpen admits RateLimitByIP and the bundled route budgets on a
	// limiter error; false (the default) answers 503.
	limiterFailOpen bool
	logger          *slog.Logger
	// proxies logs the one production trusted-proxy warning, shared by
	// ClientInfoMiddleware and the bundled routes; nil outside production.
	proxies *proxyPosture
}

// authenticatorConfig holds the HTTP adapter's resolved policy and dependencies.
type authenticatorConfig struct {
	RuntimeMode      environment.Mode
	Cookie           CookieConfig
	ExternalCookies  []string
	BrowserLoginPath string
	Limiter          ratelimiter.Limiter
	LimiterFailOpen  bool
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
	browserLoginSet := cfg.BrowserLoginPath != ""
	if !browserLoginSet {
		cfg.BrowserLoginPath = defaultBrowserLoginPath
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Adapter{
		service: service, cookie: cfg.Cookie, externalCookies: slices.Clone(cfg.ExternalCookies),
		browserLoginPath: cfg.BrowserLoginPath, browserLoginSet: browserLoginSet, limiter: cfg.Limiter, limiterFailOpen: cfg.LimiterFailOpen, logger: cfg.Logger,
		proxies: newProxyPosture(cfg.RuntimeMode, cfg.Logger),
	}
}

// clientIPFor returns the client-info carrier's IP, or clientIP(r) when no
// ClientInfoMiddleware stamped the request: a host route without the stamp
// keys on the TrustProxies-resolved or peer address, never one shared bucket.
func clientIPFor(r *http.Request) string {
	if ip, _ := authlogic.ClientInfoFromContext(r.Context()); ip != "" {
		return ip
	}
	return clientIP(r)
}

// CredentialService authenticates supplied proof and reads the resulting context.
// A custom host adapter can implement it without exposing repository internals.
// Authenticate and RequireLive errors matching sdk.ErrUnavailable answer 503
// with Retry-After; every other error denies (401 or the Browser() 303).
// RequireLive receives the route's tier and whether the request method is unsafe.
// ClassifyBearer names the one kind a JWT-shaped bearer or provider cookie is
// verified as, from its unverified issuer; false denies without verifying.
type CredentialService interface {
	Authenticate(context.Context, CredentialKind, Transport, string) (context.Context, error)
	RequireLive(context.Context, LivenessTier, bool) (context.Context, error)
	ClassifyBearer(string) (CredentialKind, bool)
	ExternalEnabled() bool
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
	if err := validExternalCookies(cfg.ExternalCookies, cfg.Cookie.Name, service.ExternalEnabled()); err != nil {
		return nil, err
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

// validExternalCookies rejects a provider cookie name that is malformed,
// repeated, or shared with the native session, refresh or CSRF cookie: a shared
// name would let one credential be read as another.
func validExternalCookies(names []string, sessionName string, externalEnabled bool) error {
	if len(names) == 0 {
		return nil
	}
	if !externalEnabled {
		return fmt.Errorf("authentication HTTP: external cookies require external identity providers: %w", sdk.ErrInvalidInput)
	}
	if sessionName == "" {
		sessionName = "session"
	}
	taken := map[string]bool{sessionName: true, sessionName + "_refresh": true, csrfCookieName: true}
	for _, name := range names {
		if (&http.Cookie{Name: name}).Valid() != nil || name == "" {
			return fmt.Errorf("authentication HTTP: invalid external cookie name %q: %w", name, sdk.ErrInvalidInput)
		}
		if taken[name] {
			return fmt.Errorf("authentication HTTP: external cookie name %q collides with another cookie: %w", name, sdk.ErrInvalidInput)
		}
		taken[name] = true
	}
	return nil
}
