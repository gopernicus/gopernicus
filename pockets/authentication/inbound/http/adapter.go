package authenticationhttp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// adapterConfig holds independently usable authentication/invitation services and
// the HTTP policies for their bundled routes. Host middleware still owns
// authorization of machine routes, and service policy owns user administration.
type adapterConfig struct {
	Authentication    AuthenticationService
	Invitations       InvitationService
	Authenticator     AuthenticatorPolicy
	RefreshCookiePath string
	ListStrategy      list.Strategy
	AllowedOrigins    []string
	Views             Views
	HTMLPolicy        *HTMLResourcePolicy
	MachineGate       web.Middleware
	RouteAuth         BundledRouteAuthentication
}

type routedService struct {
	AuthenticationService
	*Adapter
}

// The private route contract calls account registration; Adapter.Register mounts HTTP.
func (s *routedService) Register(ctx context.Context, email, password, name string) (user.User, error) {
	return s.AuthenticationService.Register(ctx, email, password, name)
}

func New(service AuthenticationService, runtimeMode environment.Mode, opts ...Option) (*Adapter, error) {
	cfg := adapterConfig{}
	cfg.Authentication = service
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication New: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}

	if nilDependency(cfg.Authentication) {
		return nil, fmt.Errorf("authentication HTTP: Authentication is required: %w", sdk.ErrInvalidInput)
	}
	if cfg.HTMLPolicy != nil && nilDependency(cfg.Views) {
		return nil, fmt.Errorf("authentication HTTP: HTMLPolicy requires Views: %w", sdk.ErrInvalidInput)
	}
	if cfg.MachineGate != nil && !cfg.Authentication.MachineEnabled() {
		return nil, fmt.Errorf("authentication HTTP: machine gate requires machine service: %w", sdk.ErrInvalidInput)
	}
	if cfg.ListStrategy == "" {
		cfg.ListStrategy = list.StrategyCursor
	}
	if cfg.ListStrategy != list.StrategyCursor && cfg.ListStrategy != list.StrategyOffset {
		return nil, fmt.Errorf("authentication HTTP: invalid list strategy: %w", sdk.ErrInvalidInput)
	}
	if cfg.RefreshCookiePath != "" {
		cfg.Authenticator.Cookie.RefreshPath = cfg.RefreshCookiePath
	}
	auth, err := NewAuthenticator(cfg.Authentication, runtimeMode,
		WithCookies(cfg.Authenticator.Cookie), WithBrowserLoginPath(cfg.Authenticator.BrowserLoginPath),
		WithLimiter(cfg.Authenticator.Limiter), WithAuthenticatorLogger(cfg.Authenticator.Logger))
	if err != nil {
		return nil, err
	}
	var inv InvitationService
	if !nilDependency(cfg.Invitations) {
		inv = cfg.Invitations
	}
	var views Views
	if !nilDependency(cfg.Views) {
		views = cfg.Views
	}
	auth.routes = &mountDeps{
		Auth:        &routedService{AuthenticationService: cfg.Authentication, Adapter: auth},
		Invitations: inv, ListStrategy: cfg.ListStrategy,
		Mutation: MutationSecurity{AllowedOrigins: append([]string(nil), cfg.AllowedOrigins...), SessionCookieName: auth.SessionCookieName()},
		Views:    views, HTMLPolicy: snapshotHTMLPolicy(cfg.HTMLPolicy), MachineGate: cfg.MachineGate,
		RouteAuth: resolveBundledRouteAuth(cfg.RouteAuth, auth),
	}
	return auth, nil
}

// Register mounts the bundled HTTP routes. It starts no worker and applies no
// migration; hosts may instead use this adapter's middleware on their own routes.
func (a *Adapter) Register(m pockets.Mount) error {
	if a == nil || a.routes == nil || nilDependency(m.Router) {
		return fmt.Errorf("authentication HTTP: Register requires an adapter and router: %w", sdk.ErrInvalidInput)
	}
	mount(m.Router, *a.routes)
	return nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func validCookieConfig(c CookieConfig) bool {
	if c.Name == "" {
		c.Name = "session"
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if err := (&http.Cookie{Name: c.Name, Path: c.Path, Domain: c.Domain}).Valid(); err != nil {
		return false
	}
	p := c.RefreshPath
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") || (len(p) > 1 && strings.HasSuffix(p, "/")) || strings.ContainsAny(p, "?#;, \"") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
