package mutations

import (
	"log/slog"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// Option configures construction of mutation services. Options apply in order,
// replacing whole values; NewService validates final settings. Nil is invalid.
type Option func(*config)

// WithRoleModel selects the immutable compiled model shared with decisions.
// A nonnil model requires Services.Roles and must be disjoint from Relationships.
// Nil leaves role assignment facts opaque. The immutable model is borrowed.
func WithRoleModel(model *authmodel.CompiledRoleModel) Option {
	return func(cfg *config) { cfg.RoleModel = model }
}

// WithGuard selects the actor-facing mutation policy. Nil disables actor writes.
// A nonnil guard requires the atomic mutation repository passed to NewService.
func WithGuard(guard MutationGuard) Option {
	return func(cfg *config) { cfg.Guard = guard }
}

// WithLimits replaces the evaluation budget. An entirely zero budget inherits
// Services.Relationships.Limits when present; explicit limits must match it.
// Other zero dimensions default when a model or guard uses the budget.
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(cfg *config) { cfg.Limits = limits }
}

// WithLogger supplies the borrowed operational logger. Nil uses slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) { cfg.Logger = logger }
}
