package mutation

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

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

// StoreDecisionView is the dependency-tracking authorization reader a
// [MutationRepository] supplies to a [Guard] INSIDE the atomic mutation
// boundary: the transaction-bound primitives a store implements. Every read
// records the scope key and the revision it observed (see [Dependency]) so the
// repository can validate those dependencies at commit. A guard may make
// authorization reads ONLY through this view: it must never call the outer
// authorization Service (which would open a detached check-then-write race) and
// must perform no network or unrelated-store I/O. Reads observe the
// transaction's own held snapshot; they do not recursively lock.
//
// Store authors implement THIS interface. The host-facing [DecisionView] the
// authorization core hands a MutationGuard embeds it and adds the schema-driven
// permission walk, which the core composes over these primitives.
//
// Dependency ordering is part of the contract: a primitive records a scope's
// revision BEFORE it reads that scope's rows (or obtains both from one statement
// snapshot), and a scope already recorded keeps its FIRST observed revision. Under
// a read-committed transaction the two-statement alternative — read rows, then
// record the revision — can pair a pre-revoke row with a post-revoke revision,
// which commit validation would wrongly accept.
type StoreDecisionView interface {
	// CheckRelation reports whether subjectType:subjectID holds relation on the
	// resource named by scope (a [ScopeResource] key), with UNBOUNDED exact-userset
	// expansion, recording that scope, its revision, and every expansion scope as
	// mutation dependencies. It is CheckRelationBounded with a non-positive bound.
	CheckRelation(ctx context.Context, scope ScopeKey, relation, subjectType, subjectID string) (bool, error)

	// CheckRelationBounded is CheckRelation with the read-side expansion-state
	// accounting: maxExpansionStates bounds the distinct reachable states of the
	// subject's userset expansion (the seed counts), and exceeding it returns
	// relationship.ErrExpansionBudgetExceeded — never an allow, never a truncated
	// deny. A non-positive bound selects the unbounded mode. The resource scope and
	// every expansion scope are recorded as dependencies; dependency collection is
	// itself bounded by the same budget.
	CheckRelationBounded(ctx context.Context, scope ScopeKey, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)

	// RelationTargets returns the subjects holding relation on the resource named
	// by scope (a [ScopeResource] key), recording that scope and its revision as a
	// dependency BEFORE the rows are read. Userset targets are returned as stored;
	// the permission walk skips them. It is the transaction-bound twin of the
	// read-side GetRelationTargets and the read a Through hop navigates.
	RelationTargets(ctx context.Context, scope ScopeKey, relation string) ([]relationship.RelationTarget, error)

	// HasRole reports whether subjectType:subjectID holds role at scope (resource
	// or subject), recording that scope and its revision as a mutation dependency.
	HasRole(ctx context.Context, scope ScopeKey, role, subjectType, subjectID string) (bool, error)

	// Dependencies returns the scopes and revisions observed so far, sorted by
	// [ScopeKey.Canonical]. The repository consults it to build its lock and
	// revision-validation set; a test asserts what a guard depended on.
	Dependencies() []Dependency
}

// DecisionView is what a host MutationGuard receives: the store's
// [StoreDecisionView] plus the schema-driven permission walk the authorization
// core composes over it. A host guard that needs "does this principal hold this
// PERMISSION here" — inherited through the model's Through hops, not just a
// direct tuple — asks CheckPermission and gets the read-side Check's answer,
// evaluated inside the mutation boundary.
type DecisionView interface {
	StoreDecisionView

	// CheckPermission reports whether principalType:principalID holds permission
	// on the resource named by scope (a [ScopeResource] key) by the SAME
	// evaluation the read-side Check performs — direct relations, exact usersets,
	// every Through hop, the same budgets — with every scope the walk navigated
	// recorded as a dependency. Budget exhaustion is the evaluation-limit error
	// (a command error: the mutation writes nothing), never a deny. A pair the
	// roles model owns is refused with a stable error; the guard uses HasRole for
	// those.
	CheckPermission(ctx context.Context, scope ScopeKey, permission, principalType, principalID string) (bool, error)
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
type Guard func(ctx context.Context, view StoreDecisionView) error

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
// ApplyGuarded runs guard against a [StoreDecisionView] INSIDE the same boundary,
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
