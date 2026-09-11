package authorization

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

// Components contains independently usable services and separately held trusted writers.
// Give request code only the services it needs; keep trusted writers at host composition.
type Components struct {
	Decisions          *decisions.Service
	Relationships      *relationships.Service
	Roles              *roles.Service
	Mutations          *mutations.Service
	HTTP               *authorizationhttp.Adapter
	RelationshipWriter *relationships.RelationshipWriter
	SystemMutator      *mutations.SystemMutator
	log                *slog.Logger
}

// New validates host wiring and assembles the public services.
func New(repos Repositories, opts ...Option) (Components, error) {
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"Repositories.Relationships", repos.Relationships}, {"Repositories.Roles", repos.Roles}, {"Repositories.Mutations", repos.Mutations}, {"Repositories.Audit", repos.Audit}, {"WithGuard", cfg.Guard}} {
		if isTypedNil(dep.value) {
			return Components{}, fmt.Errorf("authorization: %s is typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	hasRel, hasRoles := repos.Relationships != nil, repos.Roles != nil
	if !hasRel && !hasRoles {
		return Components{}, ErrNoKindConfigured
	}
	if hasRel != (len(cfg.RelationshipModel.ResourceTypes) > 0) {
		return Components{}, ErrModelRequired
	}
	if cfg.RoleModel.IsSet() && !hasRoles {
		return Components{}, ErrRoleModelWithoutRoles
	}
	if cfg.Guard != nil && repos.Mutations == nil {
		return Components{}, mutations.ErrGuardWithoutMutations
	}
	// Validate the optional route posture before any model work, preserving boot errors.
	if cfg.RoleRoutesGate != nil && !hasRoles {
		return Components{}, authorizationhttp.ErrRoleRoutesGateWithoutRoles
	}
	if cfg.RoleRoutesGate != nil && cfg.Guard == nil {
		return Components{}, authorizationhttp.ErrRoleRoutesGateWithoutGuard
	}
	if cfg.RoleRouteAssignmentPolicy != nil && cfg.RoleRoutesGate == nil {
		return Components{}, authorizationhttp.ErrRoleRouteAssignmentPolicyWithoutRoutes
	}
	if err := authorizationhttp.ValidateListStrategy(cfg.ListStrategy); err != nil {
		return Components{}, err
	}
	comps := Components{log: cfg.Logger}
	if comps.log == nil {
		comps.log = slog.Default()
	}
	if hasRel {
		parts, err := relationships.NewService(repos.Relationships, cfg.RelationshipModel, relationships.WithLimits(cfg.Limits))
		if err != nil {
			return Components{}, err
		}
		comps.Relationships = parts.Service
		comps.RelationshipWriter = parts.RelationshipWriter
	}
	if hasRoles {
		svc, err := roles.NewService(repos.Roles)
		if err != nil {
			return Components{}, err
		}
		comps.Roles = svc
	}
	if hasRel || cfg.RoleModel.IsSet() {
		var err error
		var roleReader decisions.RoleReader
		if comps.Roles != nil {
			roleReader = comps.Roles
		}
		comps.Decisions, err = decisions.NewService(
			decisions.Readers{Relationships: comps.Relationships, Roles: roleReader},
			decisions.WithRoleModel(cfg.RoleModel),
			decisions.WithLimits(cfg.Limits),
		)
		if err != nil {
			return Components{}, err
		}
	}
	mut, err := mutations.NewService(
		repos.Mutations,
		mutations.Services{Relationships: comps.Relationships, Roles: comps.Roles},
		mutations.WithRoleModel(comps.Decisions.CompiledRoleModel()),
		mutations.WithGuard(cfg.Guard),
		mutations.WithLimits(cfg.Limits),
		mutations.WithLogger(comps.log),
	)
	if err != nil {
		return Components{}, err
	}
	comps.Mutations, comps.SystemMutator = mut.Service, mut.SystemMutator
	var decision authorizationhttp.DecisionService
	if comps.Decisions != nil {
		decision = comps.Decisions
	}
	var roleReader authorizationhttp.RoleReader
	if comps.Roles != nil {
		roleReader = comps.Roles
	}
	comps.HTTP, err = authorizationhttp.New(
		authorizationhttp.Services{Decisions: decision, Roles: roleReader, Mutations: comps.Mutations},
		authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
			Gate:             cfg.RoleRoutesGate,
			AssignmentPolicy: cfg.RoleRouteAssignmentPolicy,
			ListStrategy:     cfg.ListStrategy,
		}),
	)
	if err != nil {
		return Components{}, err
	}
	return comps, nil
}

// Register mounts only the configured bundled HTTP routes.
func (c Components) Register(m pockets.Mount) error {
	if c.HTTP == nil {
		return fmt.Errorf("authorization: components are not initialized: %w", sdk.ErrInvalidInput)
	}
	c.log.Info("registered authorization pocket", "relationships", c.Relationships != nil, "roles", c.Roles != nil, "role_model", c.Decisions.CompiledRoleModel() != nil, "baseline_relationship_writes", c.RelationshipWriter != nil, "actor_mutations", c.Mutations.Guarded(), "role_routes", c.HTTP.RoutesEnabled())
	return c.HTTP.Register(m.Router)
}
func isTypedNil(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
