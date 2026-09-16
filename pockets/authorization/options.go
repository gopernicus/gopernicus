package authorization

import (
	"log/slog"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
)

// Option configures construction. Options apply in order; each replaces its
// complete setting or group. New validates the final settings. Nil is invalid.
type Option func(*config)

// WithModel snapshots the one named permission and explicit shape model.
// Exact role reads and ad-hoc expressions require no named model.
func WithModel(model decisions.Model) Option {
	option := decisions.WithModel(model)
	return func(cfg *config) { cfg.ModelOption = option }
}

// WithLimits replaces the common decision/mutation evaluation budget. Zero
// dimensions use safe defaults; negative dimensions are invalid.
// Zero never means unlimited.
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(cfg *config) { cfg.Limits = limits }
}

// WithLogger sets the borrowed operational logger for mutations and DEBUG
// decision records. Nil captures slog.Default at construction; later registration
// never substitutes Mount.Logger. The host's handler controls levels and output.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) { cfg.Logger = logger }
}

// WithRoleRoutes replaces the complete bundled role route policy. Nil Gate
// disables routes; enabled routes require Roles, Mutations and WritePolicy.
// The gate is the complete middleware stack: authenticate and set the SDK
// principal, apply browser-origin defense for cookie credentials, then authorize
// route access. WritePolicy additionally admits each validated exact command.
// The pocket does not install an authenticator or CSRF layer beneath that gate.
// WritePolicy without Gate is invalid. ListStrategy is always validated,
// including when routes are disabled. The host owns the gate's middleware stack.
func WithRoleRoutes(routes authorizationhttp.RoleRoutes) Option {
	return func(cfg *config) {
		cfg.RoleRoutesGate = routes.Gate
		cfg.RoleWritePolicy = routes.WritePolicy
		cfg.ListStrategy = routes.ListStrategy
	}
}

// WithTupleCache enables a maintained raw canonical tuple mirror. The backend is
// borrowed; the host drives Components.TupleCache.Poll and owns its lifecycle.
func WithTupleCache(backend tuplecache.Backend, policy tuplecache.Policy) Option {
	return func(cfg *config) { cfg.TupleBackend = backend; cfg.TuplePolicy = policy }
}

// WithDiagnosticObserver enables bounded transition observations for scoped
// denials with a global grant. The default is disabled and adds no reads.
// Events contain only a low-cardinality code, never principal/resource IDs.
func WithDiagnosticObserver(observer decisions.DiagnosticObserver) Option {
	return func(cfg *config) { cfg.DiagnosticObserver = observer }
}
