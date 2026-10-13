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

// OperationLedger reads and fences the operation records MutationRepository
// writes, over the same authority and write boundary.
type OperationLedger interface {
	// Fence records operationID as fenced under the write lock, so no command can
	// ever commit under it; an existing record is returned as is. It writes no
	// tuple or audit row and needs no audit.Source. It refuses an ambient
	// transaction (ErrMutationInsideTransaction). Idempotent: safe to retry after
	// an unknown-outcome commit.
	Fence(ctx context.Context, operationID string) (*FenceResult, error)
	// Recorded reads the outcome recorded under operationID without the write
	// lock. false is never proof that the operation will not commit later; only
	// Fence is. Read from the primary: a lagging replica can report false.
	Recorded(ctx context.Context, operationID string) (Outcome, bool, error)
}

// FenceResult reports the operation record a Fence call left in place.
type FenceResult struct {
	// Outcome is OutcomeFenced when this call or an earlier fence holds; otherwise
	// the outcome a command already committed under the operation ID.
	Outcome Outcome
	// Recorded is true when a record already existed and this call wrote nothing.
	Recorded bool
}
