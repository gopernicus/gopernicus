package authorization

import (
	"log/slog"
	"time"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

var (
	// ErrOperationRetentionTooShort is returned by New when WithOperationRetention
	// is below the 7-day minimum.
	ErrOperationRetentionTooShort = mutations.ErrOperationRetentionTooShort
	// ErrOperationRetentionWithoutExpirer is returned by New when
	// WithOperationRetention is set without Repositories.OperationExpiry.
	ErrOperationRetentionWithoutExpirer = mutations.ErrOperationRetentionWithoutExpirer
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

// WithRuntimeMode sets the host's deployment posture. In production, New
// returns ErrAuditRequired unless Repositories.Tuples, and Repositories.Mutations
// when wired, report AuditRecording() true (audit.RecordingReporter) and
// Repositories.Audit is wired; development logs one warning instead. The zero
// mode, the default, applies no posture check. A non-zero mode must pass
// environment.ValidateMode.
func WithRuntimeMode(mode environment.Mode) Option {
	return func(cfg *config) { cfg.RuntimeMode = mode }
}

// WithAuditDisabledAcknowledged waives the production audit requirement for an
// authority that does not record changes; New logs one posture_waiver warning.
// Beside a recording authority it is ErrAuditAcknowledgedWithRecording in every
// mode. Pass it as a literal in the composition root, never from configuration.
func WithAuditDisabledAcknowledged() Option {
	return func(cfg *config) { cfg.AuditDisabledAcknowledged = true }
}

// WithDiagnosticObserver enables bounded transition observations for scoped
// denials with a global grant. The default is disabled and adds no reads.
// Events contain only a low-cardinality code, never principal/resource IDs.
func WithDiagnosticObserver(observer decisions.DiagnosticObserver) Option {
	return func(cfg *config) { cfg.DiagnosticObserver = observer }
}

// WithDecisionObserver forwards observer to the decision service and the HTTP
// adapter: one decisions.DecisionEvent per completed unbound decision operation
// and per role write the WritePolicy refuses. A host bridges denials and
// refusals to durable storage from a host pocket. Nil, the default, records
// nothing durable; production then logs one WARN at construction.
func WithDecisionObserver(observer decisions.DecisionObserver) Option {
	return func(cfg *config) { cfg.DecisionObserver = observer }
}

// WithOperationRetention sets how long Mutations.ExpireOperations keeps an
// operation record; the default is 180 days. New refuses a retention below 7
// days (ErrOperationRetentionTooShort) and one set without
// Repositories.OperationExpiry (ErrOperationRetentionWithoutExpirer). The
// retention must exceed every producer's retry window and the age of the oldest
// open claim that names an operation ID: a command retried after its record
// expired applies again.
func WithOperationRetention(retention time.Duration) Option {
	return func(cfg *config) { cfg.OperationRetention = retention; cfg.OperationRetentionSet = true }
}
