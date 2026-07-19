package mutation

import "context"

// Dependency is one authorization scope a guard read while deciding, plus the
// revision it observed. A [DecisionView] records these as the guard runs; the
// repository locks the mutation scope plus these anchors in canonical order and
// re-validates every observed revision before commit. A scope observed with no
// anchor records revision 0, so a concurrent first writer becomes a detectable
// 0→1 change rather than a phantom.
type Dependency struct {
	Scope    ScopeKey
	Revision Revision
}

// DecisionView is the dependency-tracking authorization reader the repository
// supplies to a [Guard] INSIDE the atomic mutation boundary. Every read records
// the scope key and the revision it observed (see [Dependency]) so the repository
// can validate those dependencies at commit. A guard may make authorization reads
// ONLY through this view: it must never call the outer authorization Service
// (which would open a detached check-then-write race) and must perform no
// network or unrelated-store I/O. Reads observe the transaction's own held
// snapshot; they do not recursively lock.
type DecisionView interface {
	// CheckRelation reports whether subjectType:subjectID holds relation on the
	// resource named by scope (a [ScopeResource] key), recording that scope and its
	// revision as a mutation dependency.
	CheckRelation(ctx context.Context, scope ScopeKey, relation, subjectType, subjectID string) (bool, error)

	// HasRole reports whether subjectType:subjectID holds role at scope (resource
	// or subject), recording that scope and its revision as a mutation dependency.
	HasRole(ctx context.Context, scope ScopeKey, role, subjectType, subjectID string) (bool, error)

	// Dependencies returns the scopes and revisions observed so far, sorted by
	// [ScopeKey.Canonical]. The repository consults it to build its lock and
	// revision-validation set; a test asserts what a guard depended on.
	Dependencies() []Dependency
}

// Guard is the actor-facing authorization callback [MutationRepository.ApplyGuarded]
// runs inside the mutation boundary. It is the seam AZ3-0.5 completes: the
// composition builds this closure over the untrusted Actor and the host
// MutationGuard, so the repository contract stays free of those types and gains
// them without a breaking change. The closure returns nil to ALLOW or a stable
// denial/error to reject.
//
// The callback is SYNCHRONOUS and cancellation-bound: it must return promptly, it
// may make authorization reads only through view, and it must not call the outer
// Service or perform network/unrelated-store I/O. Possession of a MutationID is
// not authority — the guard runs on first application AND on every actor-facing
// replay before a stored receipt is returned.
type Guard func(ctx context.Context, view DecisionView) error

// SemanticValidator validates a receipt-ABSENT command against the CURRENT
// compiled schema. It is PURE (no I/O) and is invoked by Apply ONLY when no
// receipt exists for the MutationID. This pins the validation order across schema
// upgrades: an exact stored replay returns its original receipt even if the
// current schema no longer accepts that old relation, because the validator is
// skipped on replay; only a first application runs current-schema semantics. A
// nil validator means the caller supplies no additional semantic check (the
// trusted-migration path may pass nil deliberately).
type SemanticValidator func(cmd Command) error

// MutationRepository is the atomic authorization write contract. Its two methods
// are the ONLY sanctioned write path: raw create/delete calls are not part of the
// v3 mutation surface, so a store cannot advance rows without advancing the scope
// revision and minting a receipt.
//
// # Apply — the atomic core (trusted / SystemMutator path)
//
// Apply performs the entire write as ONE atomic, isolated operation. There is no
// sanctioned implementation as separate read/check/write repository calls; a
// store that splits it violates this contract and the reference specification in
// storetest. In one boundary it MUST:
//
//  1. Look up the receipt for cmd.MutationID. If one exists and its PayloadDigest
//     equals cmd.PayloadDigest(), return that receipt verbatim with Replayed=true
//     and change NOTHING — an exact replay, even if the current schema would now
//     reject the relation. If one exists with a DIFFERENT digest, return the
//     stable MutationID payload-mismatch command error (wrapping sdk.ErrConflict)
//     and change nothing.
//  2. On a receipt-absent command, run validate (when non-nil) against the current
//     schema; a failure is a command error and changes nothing.
//  3. When cmd.ExpectedRevision is non-nil, compare it to the scope's current
//     revision; a mismatch is the stale-revision command error and changes
//     nothing.
//  4. Evaluate invariants (last-owner/guardian minimums) and apply ALL requested
//     row changes or NONE. A protected block is [OutcomeInvariantBlocked]; a
//     one-relation conflict is [OutcomeSemanticConflict]; both change nothing and
//     persist no receipt.
//  5. On a change, increment the scope revision by exactly one and persist a
//     receipt recording scope, operation, payload digest, resulting revision,
//     outcome, and the governing schema digest. A no-op that is a committed
//     outcome (no_change, not_found) persists a receipt without a revision bump.
//
// Apply returns (nil, err) for a COMMAND ERROR — malformed command, stale
// revision, payload mismatch, denial, cancellation, or infrastructure failure —
// and persists no receipt in any of those cases. It returns (receipt, nil) for a
// domain [Outcome]; the receipt is durable and replayable only when
// [Outcome.Persisted] is true. It never returns (nil, nil).
//
// # ApplyGuarded — the actor-facing path
//
// ApplyGuarded runs guard against a [DecisionView] INSIDE the same boundary,
// before the receipt/revision/apply steps above, so the guard's authorization
// dependencies are revision-tracked and validated at commit — never a detached
// Check followed by a separate write. The guard runs on first application AND on
// an actor-facing replay before a stored receipt is returned (possession of a
// MutationID is not authority). Before committing, the repository locks the
// mutation scope plus every recorded dependency anchor in [ScopeKey.Canonical]
// order and re-validates each observed revision; any mismatch returns the
// stale-revision command error and writes nothing. A guard denial is a command
// error and writes nothing. The trusted Apply bypasses only the guard, not the
// atomicity, dedup, revision, or invariant rules.
type MutationRepository interface {
	// Apply atomically applies cmd with no guard (the trusted / SystemMutator
	// path). See the type doc for the full ordered contract.
	Apply(ctx context.Context, cmd Command, validate SemanticValidator) (*Receipt, error)

	// ApplyGuarded atomically applies cmd, running guard against a
	// dependency-tracking view inside the boundary and validating every observed
	// revision before commit. See the type doc for the full ordered contract.
	ApplyGuarded(ctx context.Context, cmd Command, guard Guard, validate SemanticValidator) (*Receipt, error)
}
