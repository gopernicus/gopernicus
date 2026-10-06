package mutations

import "context"

// SemanticValidator is pure fresh-operation admission and current-model tuple-shape
// validation. Replays skip it. Nil adds no model constraints.
type SemanticValidator func(Command) error

// MutationRepository applies one data command under a serialized write boundary.
// Shape validation, integrity planning, delta and audit commit atomically. Refusals
// return nil results and record nothing. Nonempty OperationID atomically retains
// every definite outcome; a matching replay writes nothing and skips validation
// and planning. Mismatched payloads return ErrOperationMismatch. Empty IDs retain
// state-based behavior. Attribution comes from
// audit.Source in context. Commands refuse an ambient transaction. Only definite
// aborted contention may retry, never ambiguous commit failures.
type MutationRepository interface {
	IntegrityPolicy() IntegrityPolicy
	Apply(context.Context, Command, SemanticValidator) (*Result, error)
}
