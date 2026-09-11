package authorization

import (
	"log/slog"
	"maps"
	"slices"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// Option configures construction. Options apply in order; each replaces its
// complete setting or group. New validates the final settings. Nil is invalid.
type Option func(*config)

// WithRelationshipModel replaces the relationship schema with a snapshot.
// A nonempty schema and Repositories.Relationships must be supplied together.
func WithRelationshipModel(model relationships.Schema) Option {
	snapshot := cloneRelationshipModel(model)
	return func(cfg *config) { cfg.RelationshipModel = snapshot }
}

// WithRoleModel replaces the role permission model with a snapshot. A set model
// requires Repositories.Roles. An unset model leaves role facts opaque; it does
// not contribute decisions. Models must declare disjoint permission coordinates.
func WithRoleModel(model authmodel.RoleModel) Option {
	snapshot := cloneRoleModel(model)
	return func(cfg *config) { cfg.RoleModel = snapshot }
}

// WithLimits replaces the common decision/mutation evaluation budget. Zero
// dimensions use safe defaults; negative dimensions fail when a model or guard
// uses the budget. Zero never means unlimited.
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(cfg *config) { cfg.Limits = limits }
}

// WithGuard sets the actor-facing mutation policy. Nil leaves actor writes
// disabled while separately held trusted writers remain available. A nonnil
// guard requires Repositories.Mutations and runs inside its atomic boundary.
func WithGuard(guard mutations.MutationGuard) Option {
	return func(cfg *config) { cfg.Guard = guard }
}

// WithLogger sets the borrowed operational logger. Nil captures slog.Default
// at construction; later registration never substitutes Mount.Logger.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) { cfg.Logger = logger }
}

// WithRoleRoutes replaces the complete bundled role route policy. Nil Gate
// disables routes; a gate requires a roles repository and an actor guard.
// The gate is the complete middleware stack: authenticate and set the SDK
// principal, apply browser-origin defense for cookie credentials, then authorize.
// The pocket does not install an authenticator or CSRF layer beneath that gate.
// AssignmentPolicy without Gate is invalid. ListStrategy is always validated,
// including when routes are disabled. The host owns the gate's middleware stack.
func WithRoleRoutes(routes authorizationhttp.RoleRoutes) Option {
	return func(cfg *config) {
		cfg.RoleRoutesGate = routes.Gate
		cfg.RoleRouteAssignmentPolicy = routes.AssignmentPolicy
		cfg.ListStrategy = routes.ListStrategy
	}
}

func cloneRelationshipModel(model relationships.Schema) relationships.Schema {
	model.ResourceTypes = maps.Clone(model.ResourceTypes)
	for name, resource := range model.ResourceTypes {
		resource.Relations = maps.Clone(resource.Relations)
		for name, relation := range resource.Relations {
			relation.AllowedSubjects = slices.Clone(relation.AllowedSubjects)
			resource.Relations[name] = relation
		}
		resource.Permissions = maps.Clone(resource.Permissions)
		for name, permission := range resource.Permissions {
			permission.AnyOf = slices.Clone(permission.AnyOf)
			resource.Permissions[name] = permission
		}
		model.ResourceTypes[name] = resource
	}
	return model
}

func cloneRoleModel(model authmodel.RoleModel) authmodel.RoleModel {
	model.ResourceTypes = maps.Clone(model.ResourceTypes)
	for name, resource := range model.ResourceTypes {
		resource.Roles = slices.Clone(resource.Roles)
		resource.Permissions = maps.Clone(resource.Permissions)
		for name, roles := range resource.Permissions {
			resource.Permissions[name] = slices.Clone(roles)
		}
		model.ResourceTypes[name] = resource
	}
	return model
}
