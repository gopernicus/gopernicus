package mutations

import "context"

// SemanticValidator is pure current-model tuple-shape validation. Nil adds no model constraints.
type SemanticValidator func(Command) error

// MutationRepository applies one data command under a serialized write boundary.
// Shape validation, integrity planning, delta and audit commit atomically. Refusals
// return nil results; no-op/error paths record nothing. Attribution comes from
// audit.Source in context. Commands refuse an ambient transaction. Only definite
// aborted contention may retry, never ambiguous commit failures.
type MutationRepository interface {
	IntegrityPolicy() IntegrityPolicy
	Apply(context.Context, Command, SemanticValidator) (*Result, error)
}
