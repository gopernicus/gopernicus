package decisions

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Durable event types a host bridge records from DecisionEvents: a denied
// decision and a refused role write. Authorization owns their meaning; the host
// chooses where they are stored.
const (
	EventAuthorizationDenied = "authorization_denied"
	EventRoleWriteRefused    = "role_write_refused"
)

// DecisionOutcome is the final result a DecisionEvent reports.
type DecisionOutcome string

const (
	OutcomeAllowed DecisionOutcome = "allowed"
	OutcomeDenied  DecisionOutcome = "denied"
	OutcomeError   DecisionOutcome = "error"
	// OutcomeComplete ends a batch, filter or lookup; its counts carry the result.
	OutcomeComplete DecisionOutcome = "complete"
	// OutcomeRefused is a role write the host's WritePolicy refused.
	OutcomeRefused DecisionOutcome = "refused"
)

// DecisionEvent describes one completed unbound public decision operation, or
// one refused role write. Identifiers are bounded as the decision log bounds
// them. Requests, Allowed and Denied count batch and filter checks; lookups and
// filtered pages report their returned IDs as Allowed. Subject is set only for
// a refused role write. ErrorKind is a low-cardinality class, never the error text.
type DecisionEvent struct {
	Operation    string
	Principal    sdk.Principal
	Subject      sdk.Principal
	Permission   string
	ResourceType string
	ResourceID   string
	Outcome      DecisionOutcome
	Requests     int
	Allowed      int
	Denied       int
	ErrorKind    string
	Duration     time.Duration
}

// DecisionObserver receives one event per completed unbound public operation,
// after the operation returns and never inside a snapshot or retry callback.
// Observers must return promptly and support calls from concurrent operations.
type DecisionObserver func(context.Context, DecisionEvent)

// WithDecisionObserver reports every completed unbound public operation,
// regardless of the logger's level. Caller-owned bound views (the *With
// family) do not emit events: they run inside the caller's transaction. Nil
// disables observation.
func WithDecisionObserver(observer DecisionObserver) Option {
	return func(c *config) { c.observer = observer }
}
