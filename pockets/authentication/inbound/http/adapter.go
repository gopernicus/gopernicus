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
// authorization of machine routes; this adapter owns invitation and user policies.
type adapterConfig struct {
	OAuth2            *OAuth2Config
	InviteCheck       InviteCheck
	ResourceRule      InvitationResourceRule
	UserAdminCheck    UserAdminCheck
	Authentication    AuthenticationService
	Invitations       InvitationService
	Authenticator     AuthenticatorPolicy
	RefreshCookiePath string
	ListStrategy      list.Strategy
	AllowedOrigins    []string
	OriginPolicy      web.OriginPolicy
	Views             Views
	HTMLPolicy        *HTMLResourcePolicy
	MachineGate       web.Middleware
	RouteAuth         BundledRouteAuthentication
	UserErasureRoute  bool
	AdministrationMFA bool
	RouteLimits       PublicRouteLimits
	PasskeyViews      PasskeyViews
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
	if cfg.OAuth2 != nil {
		for _, dep := range []any{cfg.OAuth2.Service, cfg.OAuth2.Sessions, cfg.OAuth2.Views, cfg.OAuth2.Limiter, cfg.Views} {
			if nilDependency(dep) {
				return nil, fmt.Errorf("authentication HTTP: OAuth2 requires service, sessions, limiter and views: %w", sdk.ErrInvalidInput)
			}
		}
	}
	if cfg.HTMLPolicy != nil && nilDependency(cfg.Views) {
		return nil, fmt.Errorf("authentication HTTP: HTMLPolicy requires Views: %w", sdk.ErrInvalidInput)
	}
	var passkeys passkeyService
	if p, ok := cfg.Authentication.(passkeyService); ok && p.PasskeysEnabled() {
		passkeys = p
	}
	var passkeyViews PasskeyViews
	if !nilDependency(cfg.PasskeyViews) {
		if passkeys == nil || nilDependency(cfg.Views) {
			return nil, fmt.Errorf("authentication HTTP: PasskeyViews requires a service with passkeys wired and Views: %w", sdk.ErrInvalidInput)
		}
		passkeyViews = cfg.PasskeyViews
	}
	if runtimeMode == environment.ModeProduction && cfg.OriginPolicy.HasHTTPNamespace() {
		return nil, fmt.Errorf("authentication HTTP: production OriginPolicy must not admit an http namespace: %w", sdk.ErrInvalidInput)
	}
	if cfg.MachineGate != nil && !cfg.Authentication.MachineEnabled() {
		return nil, fmt.Errorf("authentication HTTP: machine gate requires machine service: %w", sdk.ErrInvalidInput)
	}
	if err := validateInvitationPolicy(cfg); err != nil {
		return nil, err
	}
	if cfg.UserAdminCheck != nil && !cfg.Authentication.UserAdminEnabled() {
		return nil, fmt.Errorf("authentication HTTP: UserAdminCheck requires user administration: %w", sdk.ErrInvalidInput)
	}
	if cfg.AdministrationMFA && cfg.UserAdminCheck == nil {
		return nil, fmt.Errorf("authentication HTTP: administration MFA requires UserAdminCheck: %w", sdk.ErrInvalidInput)
	}
	var eraser userEraser
	if cfg.UserErasureRoute {
		e, ok := cfg.Authentication.(userEraser)
		if !ok || !e.UserErasureEnabled() || cfg.UserAdminCheck == nil {
			return nil, fmt.Errorf("authentication HTTP: the user erasure route requires user erasure and UserAdminCheck: %w", sdk.ErrInvalidInput)
		}
		eraser = e
	}
	if err := cfg.RouteLimits.Validate(); err != nil {
		return nil, err
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
		WithCookies(cfg.Authenticator.Cookie), WithExternalCookies(cfg.Authenticator.ExternalCookies...), WithBrowserLoginPath(cfg.Authenticator.BrowserLoginPath),
		WithLimiter(cfg.Authenticator.Limiter), WithLimiterFailOpen(cfg.Authenticator.LimiterFailOpen), WithAuthenticatorLogger(cfg.Authenticator.Logger))
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
		InviteCheck: cfg.InviteCheck, ResourceRule: cfg.ResourceRule, UserAdminCheck: cfg.UserAdminCheck, OAuth2: cfg.OAuth2,
		Auth:        &routedService{AuthenticationService: cfg.Authentication, Adapter: auth},
		Invitations: inv, ListStrategy: cfg.ListStrategy,
		Mutation: MutationSecurity{AllowedOrigins: append([]string(nil), cfg.AllowedOrigins...), OriginPolicy: cfg.OriginPolicy, SessionCookieName: auth.SessionCookieName()},
		Views:    views, HTMLPolicy: snapshotHTMLPolicy(cfg.HTMLPolicy), MachineGate: cfg.MachineGate,
		RouteAuth: resolveBundledRouteAuth(cfg.RouteAuth, auth),
		Eraser:    eraser,
		Proxies:   auth.proxies, RouteLimits: cfg.RouteLimits, AdministrationMFA: cfg.AdministrationMFA,
		Passkeys: passkeys, PasskeyViews: passkeyViews,
	}
	if passkeyViews != nil {
		auth.passkeyStepUpPath = passkeyStepUpPathFor(auth.browserLoginPath)
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

// validateInvitationPolicy mirrors the root constructor's invitation matrix
// against service presence: an incomplete or invalid resource rule first, then a
// service needs a complete rule or InviteCheck, and no service admits neither.
func validateInvitationPolicy(cfg adapterConfig) error {
	rule := cfg.ResourceRule
	ruleSet := rule.Can != nil || len(rule.Permissions) > 0
	if ruleSet {
		if rule.Can == nil || len(rule.Permissions) == 0 {
			return fmt.Errorf("authentication HTTP: invitation resource rule requires both Permissions and Can: %w", sdk.ErrInvalidInput)
		}
		for resourceType, permission := range rule.Permissions {
			if resourceType == "" || permission == "" {
				return fmt.Errorf("authentication HTTP: invitation resource rule has an empty resource type or permission: %w", sdk.ErrInvalidInput)
			}
		}
	}
	hasService := !nilDependency(cfg.Invitations)
	switch {
	case hasService && !ruleSet && cfg.InviteCheck == nil:
		return fmt.Errorf("authentication HTTP: invitations require a resource rule or InviteCheck: %w", sdk.ErrInvalidInput)
	case !hasService && ruleSet:
		return fmt.Errorf("authentication HTTP: invitation resource rule requires invitations: %w", sdk.ErrInvalidInput)
	case !hasService && cfg.InviteCheck != nil:
		return fmt.Errorf("authentication HTTP: InviteCheck requires invitations: %w", sdk.ErrInvalidInput)
	}
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
