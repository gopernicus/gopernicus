// Package mutation is the public rim of the authorization feature's WRITE
// contract — the atomic, idempotent, revisioned command vocabulary every store
// adapter (features/authorization/stores/{turso,pgx}, the in-core memstore) and
// the mutation service compose to. It is the security kernel of authorization
// v3's write path: the doc comments here are NORMATIVE, and a store that
// implements [MutationRepository] as detached read/check/write calls violates
// them.
//
// A write is ONE [Command]: a required cryptographically strong [MutationID], a
// single mutation [ScopeKey], an optional expected [Revision], one [Operation],
// and the requested relationship/role rows — all rows within the one scope. A
// command has no field capable of naming a second scope, so v3 exposes no
// cross-resource/cross-subject atomic batch (ratified default #3 / AZ3-0.4).
//
// # Scopes and revisions
//
//   - A RESOURCE scope (Kind [ScopeResource]) serializes relationship mutations
//     and scoped-role mutations on that resource.
//   - A SUBJECT scope (Kind [ScopeSubject]) serializes global-role mutations for
//     that subject.
//
// Each scope carries a monotonically increasing [Revision] anchor (backed by
// `iam_scopes`, AZ3-2.1). A change that alters rows increments the scope revision
// exactly once; a no-op does not. An absent anchor reads as revision 0, so a
// concurrent first writer is a detectable 0→1 change, never a phantom.
//
// # Idempotency, outcome, and replay
//
// [MutationRepository.Apply] de-duplicates by [MutationID] against persisted
// receipts (`iam_mutations`, AZ3-2.1). The domain [Outcome] (applied, no_change,
// semantic_conflict, invariant_blocked, not_found) is a separate fact from
// replay: [Receipt.Replayed] is independent metadata, never an outcome, and a
// conflict is NEVER encoded as (nil, nil) (default #8). Stale expected/dependency
// revision and [MutationID] payload mismatch are COMMAND ERRORS, not outcomes.
//
// The core imports sdk and the sibling relationship rim only — no engine, no
// store, no integration (FS1). Actor, MutationGuard, SystemMutator, and AuditSink
// are the composition types that land in AZ3-0.5; this rim defines the
// repository-level [Guard] and [DecisionView] seam they complete without a
// breaking change.
package mutation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// MutationIDBytes is the entropy [NewMutationID] draws from crypto/rand: 256
// bits, comfortably beyond the globally-unguessable bar ratified in default #2.
const MutationIDBytes = 32

// MinMutationIDLen is the smallest [MutationID] length [MutationID.Validate]
// accepts, in bytes. It rejects trivially guessable keys (a short counter) while
// staying encoding-agnostic: a host may supply its own cryptographically strong
// ID under any encoding, but must meet this floor (~128 bits under base32). The
// canonical [NewMutationID] emits well above it.
const MinMutationIDLen = 26

// mutationIDEncoding renders [NewMutationID]'s random bytes as lowercase,
// unpadded base32 — a URL-safe, case-insensitive, delimiter-free token.
var mutationIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrInvalidCommand indicates a command is structurally malformed: a missing or
// too-weak MutationID, a bad scope, an operation/scope/rows mismatch, or a row
// outside the command's single scope. It wraps [sdk.ErrInvalidInput].
var ErrInvalidCommand = fmt.Errorf("authorization mutation: invalid command: %w", sdk.ErrInvalidInput)

// ErrPayloadMismatch is the stable COMMAND ERROR [MutationRepository.Apply]
// returns when a [MutationID] is reused with a DIFFERENT [Command.PayloadDigest]
// than its persisted receipt: a MutationID identifies one write, and reusing it
// under a different payload is never a silent overwrite. It changes nothing and
// persists no receipt. It wraps [sdk.ErrConflict] so every store maps it to the
// same transport class; the root package re-exports it as ErrMutationMismatch.
var ErrPayloadMismatch = fmt.Errorf("authorization mutation: mutation id payload mismatch: %w", sdk.ErrConflict)

// ErrStaleRevision is the stable COMMAND ERROR [MutationRepository.Apply] and
// [MutationRepository.ApplyGuarded] return when an expected scope revision — or a
// guard-observed dependency revision — no longer matches at commit. It changes
// nothing and persists no receipt. It wraps [sdk.ErrConflict]; the root package
// re-exports it as ErrStaleRevision. It is a command error, never a domain
// [Outcome] (default #8).
var ErrStaleRevision = fmt.Errorf("authorization mutation: stale revision: %w", sdk.ErrConflict)

// MutationID is the required, cryptographically strong, globally unguessable
// idempotency key for one write. Possession or guessing of a MutationID is NOT
// mutation authority (an actor-facing replay still runs its guard). It is
// actor-independent: two commands with the same MutationID and the same payload
// digest are the same write regardless of who issued them.
type MutationID string

// NewMutationID returns a fresh 256-bit MutationID from crypto/rand, encoded as
// lowercase unpadded base32. It is the canonical strong generator; a host may
// substitute its own provided it clears [MinMutationIDLen] and is drawn from a
// cryptographically strong source.
func NewMutationID() (MutationID, error) {
	buf := make([]byte, MutationIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("authorization mutation: generate id: %w", err)
	}
	return MutationID(mutationIDEncoding.EncodeToString(buf)), nil
}

// DeriveMutationID deterministically derives a stable, valid MutationID from the
// given parts by hashing a length-prefixed encoding of them with SHA-256 and
// rendering the digest as lowercase unpadded base32. Unlike [NewMutationID] it is
// DETERMINISTIC: identical parts always yield the same id, so a TRUSTED caller
// (bootstrap, migration, invitation acceptance) that retries the SAME operation
// reuses the SAME MutationID and [MutationRepository.Apply] dedups the write — no
// duplicate stored mutation and no duplicate revision bump. It is for trusted
// idempotency only; actor-facing callers use the unguessable [NewMutationID] (a
// derivable id is not a secret and possession of a MutationID is never authority).
//
// The 256-bit digest encodes to 52 base32 characters, comfortably clearing
// [MinMutationIDLen], with no control characters, so the result always satisfies
// [MutationID.Validate]. Parts are length-prefixed before hashing so distinct part
// boundaries cannot alias (["a","bc"] and ["ab","c"] derive different ids). Two
// different logical events deriving from the same operation identity is intentional:
// they are the same write, and idempotency by design collapses them.
func DeriveMutationID(parts ...string) MutationID {
	h := sha256.New()
	var lp [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(lp[:], uint64(len(p)))
		h.Write(lp[:])
		h.Write([]byte(p))
	}
	return MutationID(mutationIDEncoding.EncodeToString(h.Sum(nil)))
}

// Validate reports whether the id is structurally usable: present, valid UTF-8,
// free of control characters, and at least [MinMutationIDLen] bytes. It does not
// and cannot verify the caller's entropy source; the length floor is the
// structural guard against a trivially guessable key.
func (m MutationID) Validate() error {
	if m == "" {
		return fmt.Errorf("mutation id must not be empty: %w", ErrInvalidCommand)
	}
	if len(m) < MinMutationIDLen {
		return fmt.Errorf("mutation id must be at least %d bytes (cryptographically strong): %w", MinMutationIDLen, ErrInvalidCommand)
	}
	if !utf8.ValidString(string(m)) {
		return fmt.Errorf("mutation id is not valid UTF-8: %w", ErrInvalidCommand)
	}
	for _, r := range string(m) {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("mutation id contains a control character: %w", ErrInvalidCommand)
		}
	}
	return nil
}

// Revision is a scope's monotonically increasing anchor value. 0 is the initial
// (or absent-anchor) revision; every applied change increments it by exactly one.
type Revision uint64

// ScopeKind classifies what a mutation scope serializes.
type ScopeKind string

const (
	// ScopeResource is a resource scope: it serializes relationship mutations and
	// scoped-role mutations on one resource (Type, ID).
	ScopeResource ScopeKind = "resource"
	// ScopeSubject is a subject scope: it serializes global-role mutations for one
	// subject (Type, ID). Global roles have no resource, so their revision anchor
	// is the subject.
	ScopeSubject ScopeKind = "subject"
)

// ScopeKey identifies the single scope a [Command] mutates and whose [Revision]
// it advances. Type and ID are opaque exact strings (the relationship rim's
// rules): no case folding or trimming.
type ScopeKey struct {
	Kind ScopeKind
	Type string
	ID   string
}

// Validate reports whether the scope key is structurally usable: a known Kind and
// well-formed Type/ID.
func (s ScopeKey) Validate() error {
	switch s.Kind {
	case ScopeResource, ScopeSubject:
	default:
		return fmt.Errorf("scope kind %q is not resource or subject: %w", s.Kind, ErrInvalidCommand)
	}
	if err := relationship.ValidateRefField("scope type", s.Type); err != nil {
		return err
	}
	return relationship.ValidateRefField("scope id", s.ID)
}

// Canonical returns the stable, collision-free ordering key a repository sorts by
// when it locks the mutation scope plus dependency anchors "in canonical order"
// before validating revisions. It is length-prefixed so opaque Type/ID values
// cannot alias across field boundaries; it is not a parse target.
func (s ScopeKey) Canonical() string {
	return fmt.Sprintf("%d:%s\x1f%d:%s\x1f%d:%s",
		len(s.Kind), s.Kind, len(s.Type), s.Type, len(s.ID), s.ID)
}

// String renders a human-readable scope for logs and debug output.
func (s ScopeKey) String() string { return string(s.Kind) + ":" + s.Type + ":" + s.ID }

// Operation names the state transition a [Command] requests. The set is closed:
// grant/revoke/replace/purge/teardown mutate relationships (resource scope);
// role_assign/role_unassign mutate roles (resource or subject scope).
type Operation string

const (
	// OpGrant adds the command's relationship rows. Under the one-relation rule a
	// second, DIFFERENT relation for a subject already related to the resource is
	// NOT a silent overwrite: it is [OutcomeSemanticConflict] (default #1). Use
	// [OpReplace] to change a subject's relation atomically.
	OpGrant Operation = "grant"
	// OpRevoke removes the command's exact relationship rows. Revoking an absent
	// row is [OutcomeNotFound] (a committed, replayable no-op), never an error.
	OpRevoke Operation = "revoke"
	// OpReplace atomically replaces whatever relation each row's subject currently
	// holds on the resource with the row's relation — the sanctioned answer to the
	// one-relation conflict, with no delete/create visibility gap (default #1).
	OpReplace Operation = "replace"
	// OpPurge removes every relationship on the resource (carries no rows). It
	// still honors guardian invariants: a purge that would orphan a protected
	// resource is [OutcomeInvariantBlocked]. It is not resource teardown.
	OpPurge Operation = "resource_purge"
	// OpTeardown removes all relationships (and scoped roles) for a resource being
	// destroyed. It is a DISTINCT command from [OpPurge] because it may reduce
	// protected guardian counts to zero; it is a trusted operation (SystemMutator,
	// AZ3-0.5) and carries no rows. There is no subject-purge command in v3.
	OpTeardown Operation = "resource_teardown"
	// OpRoleAssign assigns the command's role rows. Scope [ScopeResource] is a
	// scoped assignment; scope [ScopeSubject] is a global assignment whose rows'
	// subject must equal the scope subject.
	OpRoleAssign Operation = "role_assign"
	// OpRoleUnassign removes the command's exact role rows. Unassigning an absent
	// role is [OutcomeNotFound].
	OpRoleUnassign Operation = "role_unassign"
)

// isRelationshipOp reports whether op mutates relationships (resource scope).
func (o Operation) isRelationshipOp() bool {
	switch o {
	case OpGrant, OpRevoke, OpReplace, OpPurge, OpTeardown:
		return true
	}
	return false
}

// isRoleOp reports whether op mutates roles.
func (o Operation) isRoleOp() bool {
	return o == OpRoleAssign || o == OpRoleUnassign
}

// carriesRows reports whether op takes explicit rows; purge and teardown operate
// on the whole resource and carry none.
func (o Operation) carriesRows() bool {
	return o != OpPurge && o != OpTeardown
}

// RelationshipRow is one relationship change within a resource scope. The
// resource is the command's scope, so a row names only the relation and subject
// — a row cannot escape the command's single scope by construction.
type RelationshipRow struct {
	Relation string
	Subject  relationship.SubjectRef
}

// Validate reports whether the row is structurally well formed.
func (r RelationshipRow) Validate() error {
	if err := relationship.ValidateRefField("relation", r.Relation); err != nil {
		return err
	}
	return r.Subject.Validate()
}

// RoleRow is one role change. Role and subject are always explicit so the payload
// digest is self-contained; for a subject-scoped (global) command the subject
// must equal the scope subject (enforced by [Command.Validate]).
type RoleRow struct {
	SubjectType string
	SubjectID   string
	Role        string
}

// Validate reports whether the row is structurally well formed.
func (r RoleRow) Validate() error {
	if err := relationship.ValidateRefField("role subject type", r.SubjectType); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("role subject id", r.SubjectID); err != nil {
		return err
	}
	return relationship.ValidateRefField("role", r.Role)
}

// Command is one atomic authorization write. It is the actor-INDEPENDENT payload:
// the actor is supplied to the guard, never encoded here, so the payload digest
// (see [Command.PayloadDigest]) identifies the same write across actors.
//
// Invariants (enforced by [Command.Validate]):
//   - MutationID is required and cryptographically strong.
//   - Exactly one Scope; every row belongs to it (structurally, since rows carry
//     no scope of their own).
//   - Operation matches the populated row set: relationship ops populate
//     Relationships (except purge/teardown, which carry none); role ops populate
//     Roles.
//   - ExpectedRevision is a first-application precondition only; it is NOT part of
//     the payload digest and is ignored on replay.
//   - SchemaDigest and MaxAffectedRows are governing metadata / a precondition,
//     not payload: like ExpectedRevision they are excluded from [Command.PayloadDigest]
//     and ignored on replay (an exact replay returns the stored receipt verbatim).
type Command struct {
	MutationID       MutationID
	Scope            ScopeKey
	ExpectedRevision *Revision
	Operation        Operation
	Relationships    []RelationshipRow
	Roles            []RoleRow

	// SchemaDigest is the compiled-schema digest that GOVERNS this application. The
	// mutation service stamps it from the relationship engine's current schema so a
	// first-application receipt records the digest that governed it (AZ3-0.2/AZ3-3.1);
	// [MutationRepository.Apply] copies it into [Receipt.SchemaDigest]. It is empty on
	// trusted/migration paths that supply no schema, and a store may substitute a
	// stable placeholder to satisfy a non-empty column constraint. It is metadata, not
	// payload: excluded from the payload digest and ignored on replay.
	SchemaDigest string

	// MaxAffectedRows bounds the store rows a whole-resource operation (OpPurge) may
	// remove — the blast-radius ceiling the service sources from
	// EvaluationLimits.MaxBatchSize. Zero means unbounded (the trusted teardown path
	// and row-carrying operations, whose command row set is its own bound). A purge
	// whose affected relationship rows exceed a nonzero bound is
	// [OutcomeInvariantBlocked]. It is a precondition, not payload: excluded from the
	// payload digest and ignored on replay.
	MaxAffectedRows int
}

// Validate structurally validates the command — the FIRST step of the pinned
// order (structural validate → authorize actor → Apply checks id/digest →
// receipt-absent semantic validate). It consults no schema and performs no I/O.
func (c Command) Validate() error {
	if err := c.MutationID.Validate(); err != nil {
		return err
	}
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	switch {
	case c.Operation.isRelationshipOp():
		if c.Scope.Kind != ScopeResource {
			return fmt.Errorf("operation %q requires a resource scope, got %q: %w", c.Operation, c.Scope.Kind, ErrInvalidCommand)
		}
		if len(c.Roles) != 0 {
			return fmt.Errorf("relationship operation %q must not carry role rows: %w", c.Operation, ErrInvalidCommand)
		}
		if c.Operation.carriesRows() {
			if len(c.Relationships) == 0 {
				return fmt.Errorf("operation %q requires at least one relationship row: %w", c.Operation, ErrInvalidCommand)
			}
		} else if len(c.Relationships) != 0 {
			return fmt.Errorf("operation %q operates on the whole resource and must carry no rows: %w", c.Operation, ErrInvalidCommand)
		}
		seen := make(map[relationship.SubjectRef]struct{}, len(c.Relationships))
		for _, row := range c.Relationships {
			if err := row.Validate(); err != nil {
				return err
			}
			// The one-relation invariant makes two rows for the SAME subject
			// reference in one command intrinsically contradictory (differing
			// relations) or non-canonical (an exact-duplicate row), so all three
			// backends must reject it before any evaluator runs — a dialect-agnostic
			// domain rule, not a store-specific unique-index side effect.
			if _, dup := seen[row.Subject]; dup {
				return fmt.Errorf("subject %s:%s#%s appears in more than one relationship row of one command: %w",
					row.Subject.Type, row.Subject.ID, row.Subject.Relation, ErrInvalidCommand)
			}
			seen[row.Subject] = struct{}{}
		}
	case c.Operation.isRoleOp():
		if len(c.Relationships) != 0 {
			return fmt.Errorf("role operation %q must not carry relationship rows: %w", c.Operation, ErrInvalidCommand)
		}
		if len(c.Roles) == 0 {
			return fmt.Errorf("operation %q requires at least one role row: %w", c.Operation, ErrInvalidCommand)
		}
		seen := make(map[RoleRow]struct{}, len(c.Roles))
		for _, row := range c.Roles {
			if err := row.Validate(); err != nil {
				return err
			}
			if c.Scope.Kind == ScopeSubject && (row.SubjectType != c.Scope.Type || row.SubjectID != c.Scope.ID) {
				return fmt.Errorf("global (subject-scoped) role row subject %s:%s must equal the scope subject %s:%s: %w",
					row.SubjectType, row.SubjectID, c.Scope.Type, c.Scope.ID, ErrInvalidCommand)
			}
			// A subject may hold multiple DISTINCT roles in one command, but an
			// exact-duplicate (subject, role) row is non-canonical; reject it in the
			// dialect-agnostic domain so every backend agrees before evaluation.
			if _, dup := seen[row]; dup {
				return fmt.Errorf("role row %s:%s/%s is duplicated in one command: %w",
					row.SubjectType, row.SubjectID, row.Role, ErrInvalidCommand)
			}
			seen[row] = struct{}{}
		}
	default:
		return fmt.Errorf("unknown operation %q: %w", c.Operation, ErrInvalidCommand)
	}
	return nil
}
