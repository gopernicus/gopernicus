package authorizationhttp

// Option configures construction of an Adapter. Options apply in order;
// New validates final settings. A nil option is invalid.
type Option func(*config)

// WithRoleRoutes replaces the complete bundled role route policy. A nil Gate
// disables all handlers. AssignmentPolicy requires Gate; invalid ListStrategy
// fails even when routes are disabled. Middleware and callback values are borrowed.
func WithRoleRoutes(routes RoleRoutes) Option {
	return func(cfg *config) { cfg.RoleRoutes = routes }
}
