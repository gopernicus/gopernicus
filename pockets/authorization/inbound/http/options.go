package authorizationhttp

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
)

// Option configures construction of an Adapter. Options apply in order;
// New validates final settings. A nil option is invalid.
type Option func(*config)

// WithRoleRoutes replaces the complete bundled role route policy. A nil Gate
// disables all handlers. WritePolicy requires Gate; invalid ListStrategy
// fails even when routes are disabled. Middleware and callback values are borrowed.
func WithRoleRoutes(routes RoleRoutes) Option {
	return func(cfg *config) { cfg.RoleRoutes = routes }
}

// WithDecisionObserver reports each role write the WritePolicy refuses as a
// decisions.DecisionEvent with OutcomeRefused, after the WARN line. A
// cancelled request is not a refusal and emits nothing. Nil disables it.
func WithDecisionObserver(observer decisions.DecisionObserver) Option {
	return func(cfg *config) { cfg.observer = observer }
}

// WithAuditReadObserver reports each completed GET /authorization/audit read as
// one AuditReadEvent, after the page is built and before it is written. A host
// records it durably (an audit_read row) from a host pocket; reads are never
// deduplicated. Nil, the default, records nothing; beside an AuditGate New logs
// one WARN.
func WithAuditReadObserver(observer AuditReadObserver) Option {
	return func(cfg *config) { cfg.auditObserver = observer }
}

// WithLogger sets the borrowed logger for refused role writes. Nil captures
// slog.Default at construction.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) { cfg.logger = logger }
}
