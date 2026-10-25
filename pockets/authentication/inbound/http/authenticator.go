package authenticationhttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
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
	// defaultPasskeyStepUpPath is the passkey step-up page a Browser() gate with
	// RequireMFA() redirects to, re-rooted under the browser login path's prefix
	// when that path ends in defaultBrowserLoginPath.
	defaultPasskeyStepUpPath = passkeyStepUpPagePath
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
	_ MFADenialRecorder = (*authlogic.Service)(nil)
	_ MFADenialRecorder = (*authlogic.ExternalAuthenticator)(nil)
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
	// mfaDenials records MFA denials when the credential service implements
	// MFADenialRecorder; nil records nothing.
	mfaDenials MFADenialRecorder
	// passkeyStepUpPath is where a Browser() RequireMFA() denial of a cookie
	// below AAL2 redirects; empty (no passkey pages mounted) answers JSON.
	passkeyStepUpPath string
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
	mfaDenials, _ := service.(MFADenialRecorder)
	return &Adapter{
		service: service, cookie: cfg.Cookie, externalCookies: slices.Clone(cfg.ExternalCookies),
		browserLoginPath: cfg.BrowserLoginPath, browserLoginSet: browserLoginSet, limiter: cfg.Limiter, limiterFailOpen: cfg.LimiterFailOpen, logger: cfg.Logger,
		proxies: newProxyPosture(cfg.RuntimeMode, cfg.Logger), mfaDenials: mfaDenials,
	}
}

// recordMFADenial hands one MFA denial of gate to the credential service's
// recorder, attributed to the request's principal.
func (s *Adapter) recordMFADenial(ctx context.Context, gate authlogic.MFAGate, cred Credential) {
	if s.mfaDenials == nil {
		return
	}
	principal, _ := s.service.CurrentPrincipal(ctx)
	s.mfaDenials.RecordMFADenied(ctx, authlogic.MFADenial{Gate: gate, Principal: principal, Credential: cred})
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
// CurrentCredential's Assurance is what RequireMFA reads: an implementation
// that admits external tokens stamps session.AssuranceAAL2 when the provider
// asserted MFA (sdkauth.Identity.MFA) and AssuranceAAL1 otherwise, or every
// RequireMFA gate denies its tokens.
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

// MFADenialRecorder is the optional recording seam beside CredentialService. A
// credential service that implements it records each RequireMFA and admin-gate
// denial as one mfa_required_denied row; NewAuthenticator asserts it once.
// *authlogic.Service and *authlogic.ExternalAuthenticator implement it. A
// service that does not still denies; it records nothing for those gates.
type MFADenialRecorder interface {
	RecordMFADenied(context.Context, authlogic.MFADenial)
}

// NewAuthenticator builds the credential middleware over service. When service
// implements MFADenialRecorder, MFA denials are recorded through it.
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

// passkeyStepUpPathFor derives the passkey step-up page from the browser login
// path, as the login redirect is configured: a login path ending in /auth/login
// keeps its mount prefix; any other keeps the default.
func passkeyStepUpPathFor(loginPath string) string {
	if prefix, ok := strings.CutSuffix(loginPath, defaultBrowserLoginPath); ok {
		return prefix + defaultPasskeyStepUpPath
	}
	return defaultPasskeyStepUpPath
}
