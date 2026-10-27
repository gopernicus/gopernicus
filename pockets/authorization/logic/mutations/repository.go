package mutations

import (
	"context"
	"time"
)

// SemanticValidator is pure fresh-operation admission and current-model tuple-shape
// validation. Replays skip it. Nil adds no model constraints.
type SemanticValidator func(Command) error

// MutationRepository applies one data command under a serialized write boundary.
// Shape validation, integrity planning, delta and audit commit atomically. Refusals
// return nil results and record nothing. Nonempty OperationID atomically retains
// every definite outcome until an OperationExpirer expires it; a matching replay
// writes nothing and skips validation and planning. Mismatched payloads return
// ErrOperationMismatch. Empty IDs retain state-based behavior. Attribution comes from
// audit.Source in context. Commands refuse an ambient transaction of the store's
// own *DB; another instance's transaction is neither joined nor refused. Only
// definite aborted contention may retry, never ambiguous commit failures.
type MutationRepository interface {
	IntegrityPolicy() IntegrityPolicy
	Apply(context.Context, Command, SemanticValidator) (*Result, error)
}

// OperationLedger reads and fences the operation records MutationRepository
// writes, over the same authority and write boundary. An operation ID whose
// record an OperationExpirer expired reads as never recorded.
type OperationLedger interface {
	// Fence records operationID as fenced under the write lock, so no command can
	// ever commit under it; an existing record is returned as is. An expired
	// record is absent, so Fence records the ID as fenced. It writes no tuple or
	// audit row and needs no audit.Source. It refuses an ambient transaction of
	// the store's own *DB (ErrMutationInsideTransaction). Idempotent: safe to
	// retry after an unknown-outcome commit.
	Fence(ctx context.Context, operationID string) (*FenceResult, error)
	// Recorded reads the outcome recorded under operationID without the write
	// lock. Unlike Fence it may run inside an ambient transaction, whose view it
	// reads. false is never proof that the operation will not commit later; only
	// Fence is. An expired record reads as never recorded. Read from the primary:
	// a lagging replica can report false.
	Recorded(ctx context.Context, operationID string) (Outcome, bool, error)
}

// OperationExpirer deletes expired operation records over the same authority as
// the OperationLedger. It is an optional capability beside the ledger.
type OperationExpirer interface {
	// Expire deletes at most limit records whose outcome is applied, no_change or
	// not_found and whose commit time is at or before before, oldest first
	// (commit time, then operation ID), and returns how many it deleted. It never
	// deletes a fenced record. It takes no write lock, refuses an ambient
	// transaction of the store's own *DB (ErrMutationInsideTransaction) and
	// refuses limit <= 0 (sdk.ErrInvalidInput). An expired operation ID reads as
	// never recorded: a retried command under it applies again, and Fence
	// records it as fenced.
	Expire(ctx context.Context, before time.Time, limit int) (int, error)
}

// FenceResult reports the operation record a Fence call left in place.
type FenceResult struct {
	// Outcome is OutcomeFenced when this call or an earlier fence holds; otherwise
	// the outcome a command already committed under the operation ID and whose
	// record has not expired.
	Outcome Outcome
	// Recorded is true when a record already existed and this call wrote nothing.
	Recorded bool
}
