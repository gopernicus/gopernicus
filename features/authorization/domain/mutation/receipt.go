package mutation

import "time"

// Outcome is the stable domain result of an [MutationRepository.Apply]. It is a
// separate fact from replay ([Receipt.Replayed]) and from a command error: an
// Apply that returns a nil error carries exactly one of these values. A conflict
// is one of these values, NEVER (nil, nil) (default #8).
type Outcome string

const (
	// OutcomeApplied — the command changed rows and advanced the scope revision by
	// exactly one. Committed and replayable.
	OutcomeApplied Outcome = "applied"
	// OutcomeNoChange — the command was well formed and authorized but changed
	// nothing (e.g. an exact-duplicate grant). No revision bump. Committed and
	// replayable, so a retry is a stable no-op.
	OutcomeNoChange Outcome = "no_change"
	// OutcomeSemanticConflict — a well-formed command could not be applied without
	// violating a state rule that a replacement would resolve (the one-relation
	// rule: a second, different relation for a subject already related to the
	// resource, without [OpReplace]). It changes nothing and, because nothing
	// committed, does NOT persist a receipt; a later retry re-evaluates against
	// current state.
	OutcomeSemanticConflict Outcome = "semantic_conflict"
	// OutcomeInvariantBlocked — a protected invariant (last-owner/guardian minimum)
	// blocked the write. It changes nothing and does NOT persist a receipt.
	OutcomeInvariantBlocked Outcome = "invariant_blocked"
	// OutcomeNotFound — the command targeted rows that do not exist (revoking or
	// unassigning an absent tuple/role). It is a committed, replayable no-op — not
	// an error — so a retry returns the same receipt.
	OutcomeNotFound Outcome = "not_found"
)

// Valid reports whether o is one of the five stable outcomes.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeApplied, OutcomeNoChange, OutcomeSemanticConflict, OutcomeInvariantBlocked, OutcomeNotFound:
		return true
	}
	return false
}

// Persisted reports whether an outcome commits a durable, replayable receipt.
// Only applied, no_change, and not_found do; semantic_conflict and
// invariant_blocked change nothing and persist no receipt, so their MutationID
// is NOT consumed and a later retry re-evaluates. This is the persistence
// contract [MutationRepository.Apply] and every store must honor.
func (o Outcome) Persisted() bool {
	switch o {
	case OutcomeApplied, OutcomeNoChange, OutcomeNotFound:
		return true
	}
	return false
}

// Receipt is the durable, per-[MutationID] record of an applied command and the
// value [MutationRepository.Apply] returns for a domain outcome. It is minted
// once, on first application, and returned verbatim (with Replayed=true) on every
// later exact replay. It carries no display data, secrets, headers, or unbounded
// payload — only the digest that identifies the payload (AZ3-2.1).
type Receipt struct {
	// MutationID is the idempotency key this receipt records.
	MutationID MutationID
	// Scope is the single scope the command mutated.
	Scope ScopeKey
	// Operation is the applied operation.
	Operation Operation
	// PayloadEncoding is the canonical encoding version of PayloadDigest
	// (MutationEncodingVersion).
	PayloadEncoding string
	// PayloadDigest is the actor-independent payload digest ([Command.PayloadDigest]).
	// A replay with the same MutationID but a different digest is the stable
	// payload-mismatch command error, never a silent overwrite.
	PayloadDigest string
	// Outcome is the stable domain outcome that produced this receipt. Persisted
	// receipts carry an outcome for which [Outcome.Persisted] is true.
	Outcome Outcome
	// Revision is the scope revision AFTER the command: bumped once for applied,
	// unchanged for a no-op outcome.
	Revision Revision
	// SchemaDigest records the compiled-schema digest that governed the ORIGINAL
	// application (AZ3-0.2). A replay returns this original digest even if the
	// current schema no longer accepts the command's relation.
	SchemaDigest string
	// Replayed is independent metadata, NEVER stored and NEVER an outcome: it is
	// false on first application and true only when Apply returns a pre-existing
	// receipt. Do not switch domain logic on it as if it were an outcome.
	Replayed bool
	// CreatedAt is the original application time; a replay preserves it.
	CreatedAt time.Time

	// SameRoleGrantRemains is a NON-PERSISTED, computed, operation-specific
	// annotation — the same non-column, non-migration posture as [Replayed]. It is
	// set only by a SCOPED [OpRoleUnassign] evaluator, INSIDE Apply's atomic
	// critical section (AZ3-3.3): true iff, after removing the exact scoped role
	// row, a GLOBAL ("","") assignment for one of the command's exact (subject,
	// role) rows still satisfies the scoped HasRole fallback. It is false for every
	// other operation, for a subject-scoped (global) unassign, and on replay (the
	// first application's answer is not re-derived).
	//
	// It reports ONLY that this exact role grant is still effective via the global
	// fallback — it does NOT claim generic access remains, which a host may compose
	// from other role/ReBAC rules. The service repackages it into the op-specific
	// [github.com/gopernicus/gopernicus/features/authorization.UnassignRoleResult]
	// so callers read a legible, role-scoped result rather than a general receipt
	// field. Being computed inside Apply's transaction, it reflects a global-grant
	// state consistent with the same transaction that removed the scoped row — a
	// value a detached post-commit read could not honestly promise.
	SameRoleGrantRemains bool
}
