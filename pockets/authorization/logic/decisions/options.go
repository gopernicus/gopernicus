package decisions

import (
	"maps"
	"slices"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// Option configures construction of a decision service. Options apply in order,
// replacing whole values; NewService validates the final settings. Nil is invalid.
type Option func(*config)

// WithRoleModel replaces the role model with a snapshot. A set model requires
// Readers.Roles; permission coordinates must be disjoint from Relationships.
func WithRoleModel(model authmodel.RoleModel) Option {
	model.ResourceTypes = maps.Clone(model.ResourceTypes)
	for name, resource := range model.ResourceTypes {
		resource.Roles = slices.Clone(resource.Roles)
		resource.Permissions = maps.Clone(resource.Permissions)
		for name, roles := range resource.Permissions {
			resource.Permissions[name] = slices.Clone(roles)
		}
		model.ResourceTypes[name] = resource
	}
	return func(cfg *config) { cfg.RoleModel = model }
}

// WithLimits replaces the evaluation budget. An entirely zero budget inherits
// Readers.Relationships.Limits when present; an explicit budget must match it.
// Otherwise each zero dimension uses its safe default. Negative values fail.
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(cfg *config) { cfg.Limits = limits }
}
