// Package authorizationhttp supplies optional HTTP gates and role handlers.
package authorizationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	pathAssignRole      = "/authorization/roles"
	pathUnassignRole    = "/authorization/roles/unassign"
	pathRolesBySubject  = "/authorization/roles/by-subject"
	pathRolesByResource = "/authorization/roles/by-resource"
	pathRolesEffective  = "/authorization/roles/effective"
)

var (
	ErrRoleRoutesGateWithoutRoles             = errors.New("authorization: RoleRoutes.Gate set but Repositories.Roles is nil (no roles kind to administer)")
	ErrRoleRoutesGateWithoutGuard             = errors.New("authorization: RoleRoutes.Gate requires WithGuard (the bundled role writes are guarded; without a guard every one of them fails closed)")
	ErrRoleRouteAssignmentPolicyWithoutRoutes = errors.New("authorization: RoleRoutes.AssignmentPolicy requires RoleRoutes.Gate (a policy consulted only by the bundled assign route would never run)")
	ErrInvalidListStrategy                    = errors.New(`authorization: RoleRoutes.ListStrategy must be "cursor" or "offset"`)
	ErrRoleRoutesWithoutRouter                = errors.New("authorization: RoleRoutes.Gate is set but Mount.Router is nil (the bundled role-administration routes have nowhere to mount)")
)

// DecisionService supplies permission checks, model coordinates and resolved budgets.
type DecisionService interface {
	Checker
	DeclaresPermission(string, string) bool
	Limits() authmodel.EvaluationLimits
}

// RoleReader is the role listing surface used by the bundled handlers.
type RoleReader interface {
	ListRoleAssignmentsBySubject(context.Context, authmodel.PrincipalRef, list.Request) (list.Page[roles.Assignment], error)
	ListRoleAssignmentsByResource(context.Context, string, string, list.Request) (list.Page[roles.Assignment], error)
	ListEffectiveRoleGrantsByResource(context.Context, string, string, list.Request) (list.Page[roles.EffectiveGrant], error)
}

// RoleWriter is actor-facing only. Its implementation must enforce its guard atomically.
type RoleWriter interface {
	AssignRole(context.Context, mutations.Actor, mutations.AssignRoleCommand) (*mutations.Result, error)
	UnassignRole(context.Context, mutations.Actor, mutations.UnassignRoleCommand) (mutations.UnassignRoleResult, error)
	Guarded() bool
}

// RoleRouteAssignmentPolicy optionally checks only bundled assignment commands.
type RoleRouteAssignmentPolicy func(context.Context, mutations.AssignRoleCommand) error

// Services supplies the decision, role-read and guarded-write surfaces. Each
// kind may be absent; enabled role routes require Roles and guarded Mutations.
// The adapter borrows these services without changing their policy.
type Services struct {
	Decisions DecisionService
	Roles     RoleReader
	Mutations RoleWriter
}

// RoleRoutes selects the optional bundled role-administration surface. A nil
// Gate disables all role handlers. The host gate supplies authentication,
// browser-origin defense when needed, and authorization, in that order.
type RoleRoutes struct {
	Gate web.Middleware
	// AssignmentPolicy checks legality before a bundled assignment. It requires
	// Gate; it does not replace the mutation service's atomic actor guard.
	AssignmentPolicy RoleRouteAssignmentPolicy
	// ListStrategy is the default pagination mode. Empty uses cursor. Invalid
	// values fail construction even when Gate is absent.
	ListStrategy list.Strategy
}

type config struct {
	Services
	RoleRoutes
}

// Adapter owns validated HTTP dependencies. Its state is immutable after New.
type Adapter struct {
	decisions        DecisionService
	roles            RoleReader
	mutations        RoleWriter
	gate             web.Middleware
	assignmentPolicy RoleRouteAssignmentPolicy
	listStrategy     list.Strategy
}

// New validates the final dependency and route policy before building an adapter.
func New(services Services, opts ...Option) (*Adapter, error) {
	cfg := config{Services: services}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorization HTTP: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	for _, dep := range []any{cfg.Decisions, cfg.Roles, cfg.Mutations} {
		if typedNil(dep) {
			return nil, fmt.Errorf("authorization HTTP: typed nil dependency: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.Gate != nil && cfg.Roles == nil {
		return nil, ErrRoleRoutesGateWithoutRoles
	}
	if cfg.Gate != nil && (cfg.Mutations == nil || !cfg.Mutations.Guarded()) {
		return nil, ErrRoleRoutesGateWithoutGuard
	}
	if cfg.AssignmentPolicy != nil && cfg.Gate == nil {
		return nil, ErrRoleRouteAssignmentPolicyWithoutRoutes
	}
	if err := ValidateListStrategy(cfg.ListStrategy); err != nil {
		return nil, err
	}
	if cfg.Decisions != nil {
		limits := cfg.Decisions.Limits()
		resolved, err := limits.Resolve()
		if err != nil {
			return nil, err
		}
		if limits != resolved {
			return nil, fmt.Errorf("authorization HTTP: decision service must supply resolved limits: %w", sdk.ErrInvalidInput)
		}
	}
	return &Adapter{decisions: cfg.Decisions, roles: cfg.Roles, mutations: cfg.Mutations, gate: cfg.Gate, assignmentPolicy: cfg.AssignmentPolicy, listStrategy: cfg.ListStrategy}, nil
}
func ValidateListStrategy(strategy list.Strategy) error {
	switch strategy {
	case "", list.StrategyCursor, list.StrategyOffset:
		return nil
	}
	return ErrInvalidListStrategy
}
func (a *Adapter) RoutesEnabled() bool { return a != nil && a.gate != nil }
func (a *Adapter) Register(r pockets.RouteRegistrar) error {
	if !a.RoutesEnabled() {
		return nil
	}
	if r == nil || typedNil(r) {
		return ErrRoleRoutesWithoutRouter
	}
	r.Handle("POST", pathAssignRole, a.assignRole, a.gate)
	r.Handle("POST", pathUnassignRole, a.unassignRole, a.gate)
	r.Handle("GET", pathRolesBySubject, a.listBySubject, a.gate)
	r.Handle("GET", pathRolesByResource, a.listByResource, a.gate)
	r.Handle("GET", pathRolesEffective, a.listEffectiveByResource, a.gate)
	return nil
}

// handler returns a callable handler with the configured host gate attached.
// The disabled route posture also applies when hosts mount handlers themselves.
func (a *Adapter) handler(fn http.HandlerFunc) http.Handler {
	if !a.RoutesEnabled() {
		return http.NotFoundHandler()
	}
	return a.gate(fn)
}
func (a *Adapter) AssignRole() http.Handler      { return a.handler(a.assignRole) }
func (a *Adapter) UnassignRole() http.Handler    { return a.handler(a.unassignRole) }
func (a *Adapter) RolesBySubject() http.Handler  { return a.handler(a.listBySubject) }
func (a *Adapter) RolesByResource() http.Handler { return a.handler(a.listByResource) }
func (a *Adapter) EffectiveRolesByResource() http.Handler {
	return a.handler(a.listEffectiveByResource)
}
func typedNil(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
