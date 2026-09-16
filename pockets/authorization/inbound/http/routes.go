// Package authorizationhttp supplies optional HTTP gates and role handlers.
package authorizationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	pathAssignRole      = "/authorization/roles"
	pathUnassignRole    = "/authorization/roles/unassign"
	pathRolesBySubject  = "/authorization/roles/by-subject"
	pathRolesByResource = "/authorization/roles/by-resource"
)

var (
	ErrRoleRoutesGateWithoutRoles   = errors.New("authorization: RoleRoutes.Gate set but Roles service is nil")
	ErrRoleRoutesWithoutMutations   = errors.New("authorization: RoleRoutes.Gate requires Mutations")
	ErrRoleRoutesWithoutWritePolicy = errors.New("authorization: RoleRoutes.Gate requires RoleRoutes.WritePolicy")
	ErrRoleWritePolicyWithoutRoutes = errors.New("authorization: RoleRoutes.WritePolicy requires RoleRoutes.Gate")
	ErrInvalidListStrategy          = errors.New(`authorization: RoleRoutes.ListStrategy must be "cursor" or "offset"`)
	ErrRoleRoutesWithoutRouter      = errors.New("authorization: RoleRoutes.Gate is set but Mount.Router is nil (the bundled role-administration routes have nowhere to mount)")
)

// DecisionService validates a policy and evaluates it as one coherent operation.
// ValidateExpression checks the entire tree without I/O. EvaluateResolved must
// be safe for concurrent requests, use one coherent snapshot and shared budgets,
// and short-circuit in order. It resolves inputs lazily and memoizes each input
// across whole-operation cache fallback. Errors discard provisional decisions.
type DecisionService interface {
	ValidateExpression(decisions.Expression) error
	EvaluateResolved(context.Context, authmodel.PrincipalRef, decisions.Expression, decisions.ResourceResolver) (authmodel.CheckResult, error)
}

// RoleReader is the role listing surface used by the bundled handlers.
type RoleReader interface {
	ListRoleAssignmentsBySubject(context.Context, authmodel.PrincipalRef, list.Request) (list.Page[roles.Assignment], error)
	ListRoleAssignmentsByScope(context.Context, tuples.Scope, list.Request) (list.Page[roles.Assignment], error)
}

// RoleWriter applies admitted data commands and enforces data integrity atomically.
type RoleWriter interface {
	AssignRole(context.Context, mutations.AssignRoleCommand) (*mutations.Result, error)
	UnassignRole(context.Context, mutations.UnassignRoleCommand) (mutations.UnassignRoleResult, error)
}

// RoleWriteRequest is the exact validated write admitted by the host. Its fields
// are values, so the policy cannot alter the command that will be applied.
type RoleWriteRequest struct {
	Principal sdk.Principal
	Operation mutations.Operation
	Subject   authmodel.PrincipalRef
	Role      string
	Scope     tuples.Scope
}

// RoleWritePolicy authorizes one assign or unassign at inbound, before the write.
// Return nil to admit, or an error to refuse. Later revocation does not retract admission.
type RoleWritePolicy func(context.Context, RoleWriteRequest) error

// Services supplies decision, role-read and data-write surfaces. Each kind may
// be absent; enabled role routes require Roles and Mutations.
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
	// WritePolicy authorizes each exact assign/unassign command after validation.
	// It is required when Gate enables routes.
	WritePolicy RoleWritePolicy
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
	decisions    DecisionService
	roles        RoleReader
	mutations    RoleWriter
	gate         web.Middleware
	writePolicy  RoleWritePolicy
	listStrategy list.Strategy
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
	if cfg.Gate != nil && cfg.Mutations == nil {
		return nil, ErrRoleRoutesWithoutMutations
	}
	if cfg.Gate != nil && cfg.WritePolicy == nil {
		return nil, ErrRoleRoutesWithoutWritePolicy
	}
	if cfg.WritePolicy != nil && cfg.Gate == nil {
		return nil, ErrRoleWritePolicyWithoutRoutes
	}
	if err := ValidateListStrategy(cfg.ListStrategy); err != nil {
		return nil, err
	}

	return &Adapter{decisions: cfg.Decisions, roles: cfg.Roles, mutations: cfg.Mutations, gate: cfg.Gate, writePolicy: cfg.WritePolicy, listStrategy: cfg.ListStrategy}, nil
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
