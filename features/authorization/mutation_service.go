package authorization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk"
)

// Construction sentinels for the actor-mutation posture (AZ3-0.5). Like
// ErrNoKindConfigured / ErrModelRequired these are BOOT-time failures — a
// misconfigured host fails at NewService rather than at a later call site — so
// they wrap no sdk kind (a construction fault is not a client-actionable request
// error). The runtime read-only-posture refusal is the separate, sdk-kinded
// ErrMutationsNotConfigured (codes.go).
var (
	// ErrGuardWithoutMutations is returned by NewService when a MutationGuard is
	// supplied but Repositories.Mutations is nil. A guard can only be enforced
	// INSIDE the atomic mutation boundary (MutationRepository.ApplyGuarded); with no
	// repository behind it the guarded write path can never run, so a guard alone is
	// a half-enabled system (lesson #4) and fails boot rather than silently doing
	// nothing.
	ErrGuardWithoutMutations = errors.New("authorization: Config.Guard requires Repositories.Mutations (a guard has no atomic write path without it)")

	// ErrAuditWithoutGuard is returned by NewService when an AuditSink is supplied
	// without a MutationGuard. The AuditSink observes actor-facing mutation attempts
	// (accepted/denied/failed); denials are produced only by a guard. With the
	// actor-mutation path off (no guard, the read-only posture) the sink is an
	// ORPHANED actor-mutation setting, and — because a relationship/role kind IS
	// wired — it fails construction rather than being silently ignored (the auth
	// MailFrom silence applies only when the KIND is unwired, AZ3-0.3).
	ErrAuditWithoutGuard = errors.New("authorization: Config.Audit requires Config.Guard (an audit sink with no guard observes no actor-mutation attempts)")

	// ErrTrustedOperationRequired is returned by the actor-facing guarded write seam
	// when a Command names a TRUSTED-ONLY operation — currently OpTeardown, the one
	// operation permitted to zero a protected scope past its guardian minimum. No
	// untrusted Actor may perform it through any public Service method: teardown is
	// reachable ONLY through the separately held SystemMutator.TeardownAuthorizationScope
	// (which also enforces a recorded, non-empty reason). Like ErrMutationsNotConfigured
	// it wraps sdk.ErrInvalidInput — a deterministic precondition refusal of THIS seam —
	// NOT sdk.ErrUnavailable (this is not saturation) and NOT sdk.ErrForbidden, which
	// would falsely imply some OTHER Actor could succeed against the same deployment
	// (none can: teardown is a held capability, not a principal).
	ErrTrustedOperationRequired = fmt.Errorf("authorization: operation is reachable only through the trusted SystemMutator, not an actor-facing mutation: %w", sdk.ErrInvalidInput)

	// ErrTeardownViaTypedMethod is returned by SystemMutator.Apply when a Command names
	// OpTeardown. Teardown carries a trust-boundary guarantee — a recorded, non-empty
	// reason plus the teardown audit — that only the typed TeardownAuthorizationScope
	// enforces; the generic Apply seam carries no reason, so it refuses OpTeardown
	// rather than let a SystemMutator holder zero a protected scope without the mandated
	// justification. It wraps sdk.ErrInvalidInput (a precondition refusal: route
	// teardown through SystemMutator.TeardownAuthorizationScope).
	ErrTeardownViaTypedMethod = fmt.Errorf("authorization: OpTeardown must go through SystemMutator.TeardownAuthorizationScope (which records the required reason), not the generic Apply seam: %w", sdk.ErrInvalidInput)
)

// Actor is the untrusted platform principal on whose behalf an actor-facing
// mutation is attempted. It is exactly the concrete (Type, ID) principal pair
// (PrincipalRef) — nothing more. There is DELIBERATELY no kind/role field and in
// particular no "system" value a caller can construct: trusted, unguarded writes
// are reached ONLY through the separately held SystemMutator capability, never by
// flagging an Actor as privileged. An empty Actor is invalid (Validate rejects
// it), so every actor-facing mutation carries a non-empty, concrete principal.
type Actor struct {
	PrincipalRef
}

// Validate reports whether the actor is a usable, non-empty concrete principal.
// An empty or malformed actor is invalid — an untrusted mutation cannot proceed
// without one.
func (a Actor) Validate() error {
	if err := a.PrincipalRef.Validate(); err != nil {
		return err
	}
	return nil
}

// ProposedChange is the actor-independent state an actor-facing mutation would
// apply: the relationship and/or role rows of the underlying Command. It carries
// no MutationID, no actor, and no expected revision — a guard reasons about WHAT
// would change, not who asked or which idempotency key was used.
type ProposedChange struct {
	Relationships []RelationshipRow
	Roles         []RoleRow
}

// MutationAttempt is the full context a MutationGuard authorizes: the untrusted
// Actor, the Operation, the target Scope, and the ProposedChange. It is passed
// alongside the dependency-tracking DecisionView so the guard has both the
// request and a revision-recording read path.
type MutationAttempt struct {
	Actor     Actor
	Operation Operation
	Scope     ScopeKey
	Change    ProposedChange
}

// MutationGuard is the host-supplied authorization policy for actor-facing
// writes. AuthorizeMutation returns nil to ALLOW the attempt or a stable
// denial/error (typically wrapping sdk.ErrForbidden / sdk.ErrUnauthorized) to
// reject it. The feature supplies NO default allow policy: the absence of a
// guard is the read-only posture (ErrMutationsNotConfigured), never an implicit
// allow.
//
// A guard that depends on authorization DATA must read it only through view — the
// dependency-tracking DecisionView the repository supplies inside the atomic
// mutation boundary — and must never call the outer authorization Service, which
// would open a detached check-then-write race. Every scope the guard reads
// through view is recorded with the revision it observed; the repository locks
// those anchors in canonical order and re-validates each revision before commit
// (MutationRepository.ApplyGuarded / DecisionView). AuthorizeMutation must be
// synchronous, promptly return, honor ctx cancellation, and perform no network or
// unrelated-store I/O.
type MutationGuard interface {
	AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error
}

// composeGuard folds the untrusted actor and the host MutationGuard into the
// repository-level Guard closure (mutation.Guard) that MutationRepository.ApplyGuarded
// runs inside the atomic boundary. This is the AZ3-0.4 seam completed: the
// repository port stays free of Actor/MutationGuard and gains them without a
// breaking change. The closure carries only the actor-independent Command state
// into the MutationAttempt (actor supplied separately), and passes the
// repository's DecisionView straight through so every guard read is
// revision-tracked against the mutation scope's dependency set.
func composeGuard(actor Actor, guard MutationGuard, cmd Command) Guard {
	attempt := MutationAttempt{
		Actor:     actor,
		Operation: cmd.Operation,
		Scope:     cmd.Scope,
		Change:    ProposedChange{Relationships: cmd.Relationships, Roles: cmd.Roles},
	}
	return func(ctx context.Context, view DecisionView) error {
		return guard.AuthorizeMutation(ctx, attempt, view)
	}
}

// AuditDecision is the coarse class of an audited mutation attempt.
type AuditDecision string

const (
	// AuditAccepted — the attempt passed the guard and committed a domain outcome.
	AuditAccepted AuditDecision = "accepted"
	// AuditDenied — the guard rejected the attempt (an authorization denial).
	AuditDenied AuditDecision = "denied"
	// AuditFailed — the attempt failed for a non-denial command/infrastructure
	// reason (stale revision, payload mismatch, malformed command, store error).
	AuditFailed AuditDecision = "failed"
)

// AuditEvent is the record handed to an AuditSink for one actor-facing mutation
// attempt. It carries the opaque MutationID (a random idempotency token, safe to
// retain), the coarse Decision, the Operation, the Scope, the Actor, and — when
// applicable — the domain Outcome or the stable Reason. It carries no secrets,
// headers, display strings, or unbounded payload.
//
// A sink implementation must NOT turn Scope.ID or Actor.ID into unbounded metric
// labels; those are opaque high-cardinality identifiers. The feature's own
// best-effort warning on sink failure logs only coarse, bounded fields
// (mutation_id, decision, operation, scope_kind, reason) and never the raw
// resource/subject IDs.
type AuditEvent struct {
	MutationID MutationID
	Actor      Actor
	Operation  Operation
	Scope      ScopeKey
	Decision   AuditDecision
	Outcome    Outcome // set when Decision is AuditAccepted
	Reason     Reason  // set when Decision is AuditDenied or AuditFailed

	// Detail is optional, bounded free-form context. It is set only by
	// SystemMutator.TeardownAuthorizationScope, carrying the required,
	// length-bounded teardown reason (the trust-boundary justification for zeroing a
	// protected scope). It is empty for ordinary actor-facing attempts. Like every
	// other AuditEvent field it must never become an unbounded metric label.
	Detail string
}

// AuditSink is the optional, best-effort observer of actor-facing mutation
// attempts. RecordMutation is called once per attempt after the guarded apply
// resolves. It is BEST-EFFORT: a non-nil return is warned (with coarse fields
// only) and NEVER changes a committed mutation or the result the caller sees. A
// sink must not block the write path — it should return promptly and do its own
// buffering/retry if it needs durability.
type AuditSink interface {
	RecordMutation(ctx context.Context, event AuditEvent) error
}

// SystemMutator is the trusted, actor-free HIGH-INTEGRITY mutation capability for
// operations that opt into durable idempotency, revisions, invariants, receipts,
// audit, or explicit resource teardown. Ordinary relationship state instead uses
// the separately held RelationshipWriter.
// It runs the repository's unguarded Apply path (no MutationGuard), so it is
// deliberately held SEPARATELY from Service and passed by the composition root
// only to trusted callers — it is NOT reachable from Service and HTTP handlers
// receive only Components.Service. It still obeys every non-guard rule of the
// atomic mutation contract (idempotency by MutationID, revision, invariants,
// receipts).
//
// Its command surface is the trusted Apply seam plus the typed
// TeardownAuthorizationScope operation (AZ3-3.2); further typed trusted commands
// (bootstrap/invitation/…) mature in AZ3-3.4.
type SystemMutator struct {
	mutations     MutationRepository
	audit         AuditSink              // nil = no structured teardown audit sink (logger still records)
	log           *slog.Logger           // nil = slog.Default()
	relationships *authorizersvc.Service // nil = relationship kind off (no digest stamping / semantic validation)
}

// Apply runs a trusted (unguarded) atomic mutation. It bypasses only the
// MutationGuard — the dedup/revision/invariant/receipt rules of
// MutationRepository.Apply still hold, AND it runs the current-schema semantic
// validator plus governing-digest stamping exactly as the actor-facing guarded seam
// does (AZ3-3.1 parity): a trusted grant/replace of a relation the current schema
// rejects is refused, and its first-application receipt records the schema that
// governed it. With no atomic MutationRepository wired it fails closed with
// ErrMutationsNotConfigured.
//
// It refuses OpTeardown (ErrTeardownViaTypedMethod): teardown must go through the
// typed TeardownAuthorizationScope so the mandated non-empty reason and teardown
// audit are never bypassable, even by a SystemMutator holder using the generic seam.
func (m *SystemMutator) Apply(ctx context.Context, cmd Command) (*Receipt, error) {
	if m == nil || m.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if cmd.Operation == OpTeardown {
		return nil, ErrTeardownViaTypedMethod
	}
	cmd = stampSchemaDigest(m.relationships, cmd)
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	return m.mutations.Apply(ctx, cmd, schemaValidatorFor(m.relationships))
}

// GrantRelationship runs a TRUSTED single-relation grant — the invitation-acceptance
// and bootstrap seam (a SystemMutator holder). It builds the same actor-independent
// command the guarded Service.GrantRelationship builds and drives it through the
// unguarded trusted Apply, so it stamps the governing schema digest and runs the
// current-schema validator but bypasses the host MutationGuard. Idempotency is by
// MutationID (derive a stable one with DeriveMutationID so a retried grant dedups).
func (m *SystemMutator) GrantRelationship(ctx context.Context, cmd GrantRelationshipCommand) (*Receipt, error) {
	return m.Apply(ctx, grantRelationshipCommand(cmd))
}

// AssignRole runs a TRUSTED role assignment (a SystemMutator holder's bootstrap /
// migration seam). It mirrors the guarded Service.AssignRole payload without the
// actor/guard and drives it through trusted Apply. A half-scoped resource pair is
// rejected before any write.
func (m *SystemMutator) AssignRole(ctx context.Context, cmd AssignRoleCommand) (*Receipt, error) {
	command, err := assignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return m.Apply(ctx, command)
}

// UnassignRole runs a TRUSTED role unassignment. The returned receipt carries the
// in-lock SameRoleGrantRemains annotation (AZ3-3.3) like the guarded path.
func (m *SystemMutator) UnassignRole(ctx context.Context, cmd UnassignRoleCommand) (*Receipt, error) {
	command, err := unassignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return m.Apply(ctx, command)
}

// MaxTeardownReasonLen bounds the teardown reason so the audit record stays
// bounded (the receipt/audit vocabulary carries no unbounded payload).
const MaxTeardownReasonLen = 1024

// ErrTeardownReasonRequired is returned by TeardownAuthorizationScope when the
// teardown reason is empty (after trimming) or exceeds MaxTeardownReasonLen. The
// non-empty reason is part of the trust boundary — a teardown must articulate WHY
// a protected scope is being zeroed — so an empty reason is refused before any
// write. It wraps sdk.ErrInvalidInput (a precondition refusal).
var ErrTeardownReasonRequired = fmt.Errorf("authorization: TeardownAuthorizationScope requires a non-empty teardown reason (<= %d bytes): %w", MaxTeardownReasonLen, sdk.ErrInvalidInput)

// TeardownAuthorizationScopeCommand asks the trusted SystemMutator to remove ALL
// relationships (and scoped roles) for a resource being destroyed. It is the ONE
// operation allowed to reduce a protected scope to zero, distinct from the
// actor-facing PurgeResourceAuthorization (which still honors guardian invariants).
// Reason is REQUIRED and non-empty: possession of the separately wired
// SystemMutator plus an explicit teardown reason is the trust boundary.
type TeardownAuthorizationScopeCommand struct {
	MutationID       MutationID
	ResourceType     string
	ResourceID       string
	Reason           string
	ExpectedRevision *Revision
}

// TeardownAuthorizationScope runs the trusted resource-teardown operation. It is the
// single explicit exception to the ordinary guardian minimum (OpTeardown may zero a
// protected scope) and otherwise obeys the full atomic contract: it is idempotent by
// MutationID, revisioned, and receipted. It bypasses only the host MutationGuard.
//
// The teardown reason is recorded observably in two places: ALWAYS through the
// SystemMutator's structured logger (an info line naming the mutation, scope,
// outcome, and reason — durable in the host's log pipeline), and — when a host wired
// an AuditSink — through a best-effort AuditEvent carrying the reason in Detail. The
// receipt durably records THAT the teardown occurred (operation, scope, resulting
// revision, permanent retention); the free-form reason rides the observability seam
// rather than the idempotency ledger, so the frozen dual-dialect receipt schema is
// unchanged. A teardown is actor-free, so its AuditEvent has a zero Actor.
//
// Host ordering and ID-reuse hazards (documented honestly, NOT misrepresented as
// cross-feature atomicity): authorization does NOT call the foreign resource
// repository from inside its transaction, so tearing down the authorization scope and
// deleting the resource itself are TWO operations in the HOST's chosen order. Tear
// down authorization AFTER the resource is gone (or logically deleted) so no window
// exists where a live resource has lost its guardians. The scope's revision anchor
// PERSISTS after teardown (revision is monotonic, never reset); a later resource that
// reuses the same (type, id) continues from that revision — safe for idempotency and
// stale-revision detection, but the host must not assume a reused ID starts at
// revision 0.
func (m *SystemMutator) TeardownAuthorizationScope(ctx context.Context, cmd TeardownAuthorizationScopeCommand) (*Receipt, error) {
	if m == nil || m.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	reason := strings.TrimSpace(cmd.Reason)
	if reason == "" || len(reason) > MaxTeardownReasonLen {
		return nil, ErrTeardownReasonRequired
	}
	command := stampSchemaDigest(m.relationships, Command{
		MutationID:       cmd.MutationID,
		Scope:            ScopeKey{Kind: ScopeResource, Type: cmd.ResourceType, ID: cmd.ResourceID},
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpTeardown,
	})
	if err := command.Validate(); err != nil {
		return nil, err
	}
	receipt, err := m.mutations.Apply(ctx, command, schemaValidatorFor(m.relationships))
	m.recordTeardown(ctx, command, reason, receipt, err)
	return receipt, err
}

// recordTeardown observably records a teardown attempt and its reason: always via
// the structured logger, and — best-effort — via a wired AuditSink. A sink error
// never changes the committed teardown; it is warned with coarse, bounded fields.
func (m *SystemMutator) recordTeardown(ctx context.Context, cmd Command, reason string, receipt *Receipt, applyErr error) {
	logger := m.log
	if logger == nil {
		logger = slog.Default()
	}
	if applyErr == nil && receipt != nil {
		logger.InfoContext(ctx, "authorization scope teardown",
			"mutation_id", string(cmd.MutationID),
			"operation", string(cmd.Operation),
			"scope_kind", string(cmd.Scope.Kind),
			"scope_type", cmd.Scope.Type,
			"scope_id", cmd.Scope.ID,
			"outcome", string(receipt.Outcome),
			"reason", reason,
		)
	} else {
		logger.WarnContext(ctx, "authorization scope teardown failed",
			"mutation_id", string(cmd.MutationID),
			"operation", string(cmd.Operation),
			"scope_kind", string(cmd.Scope.Kind),
			"reason", reason,
			"error", applyErr,
		)
	}

	if m.audit == nil {
		return
	}
	event := AuditEvent{
		MutationID: cmd.MutationID,
		Operation:  cmd.Operation,
		Scope:      cmd.Scope,
		Detail:     reason,
	}
	if applyErr == nil && receipt != nil {
		event.Decision = AuditAccepted
		event.Outcome = receipt.Outcome
	} else {
		event.Decision = AuditFailed
		if r, ok := ReasonFor(applyErr); ok {
			event.Reason = r
		}
	}
	if sinkErr := m.audit.RecordMutation(ctx, event); sinkErr != nil {
		logger.WarnContext(ctx, "authorization audit sink failed",
			"mutation_id", string(event.MutationID),
			"decision", string(event.Decision),
			"operation", string(event.Operation),
			"scope_kind", string(event.Scope.Kind),
			"reason", string(event.Reason),
			"error", sinkErr,
		)
	}
}

// Components is the construction bundle NewService returns: the host-facing
// Service, the separately held baseline RelationshipWriter, and the separately
// held high-integrity SystemMutator. The composition root deliberately places
// capabilities; none is recoverable from Service and Register mounts no routes.
type Components struct {
	// Service is the host-facing decision/list/actor-mutation surface.
	Service *Service
	// RelationshipWriter is the normal trusted application-side state writer. It
	// is non-nil whenever the relationship kind is configured, independently of
	// Repositories.Mutations.
	RelationshipWriter *RelationshipWriter
	// SystemMutator is the trusted, actor-free mutation capability, held apart from
	// Service. It is the advanced occurrence-oriented path and requires
	// Repositories.Mutations when called.
	SystemMutator *SystemMutator
}

// applyMutation is the generic actor-facing guarded write seam: it composes the
// untrusted actor and the configured MutationGuard into the repository's
// dependency-tracking ApplyGuarded boundary. With no guard (the read-only
// posture) or no atomic MutationRepository it fails closed with
// ErrMutationsNotConfigured — there is no default-allow path. Possession of a
// MutationID is not authority: the guard runs on first application and on every
// actor-facing replay before a stored receipt is returned.
//
// It is UNEXPORTED on purpose (F1 remediation): the only public actor-facing write
// surface is the typed set of methods (GrantRelationship, RevokeRelationship,
// ReplaceRelationship, PurgeResourceAuthorization, AssignRole, UnassignRole) that
// wrap it — there is no generic Command seam a caller can hand an arbitrary
// Operation or blast-radius bound. Two guardrails run here so even an internal
// caller cannot misuse it: a TRUSTED-ONLY operation (OpTeardown) is rejected
// (teardown is reachable only through SystemMutator.TeardownAuthorizationScope, which
// records the required reason), and the OpPurge blast radius is FORCED to the
// resolved EvaluationLimits.MaxBatchSize (every other operation carries no bound),
// never trusting a caller-supplied MaxAffectedRows. MaxAffectedRows is excluded from
// the payload digest and ignored on replay (see domain/mutation Command), so
// overwriting it cannot alter idempotency.
//
// Two phase-3 wirings happen here: the current-schema SemanticValidator (schema-shape
// validation of additive relationship rows, run receipt-absent INSIDE Apply so an
// exact replay survives a later schema change per the AZ3-0.4 order), and the
// governing schema digest stamped onto a relationship command so the receipt records
// the digest that governed the original application.
func (s *Service) applyMutation(ctx context.Context, actor Actor, cmd Command) (*Receipt, error) {
	if s.guard == nil || s.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if cmd.Operation == OpTeardown {
		return nil, ErrTrustedOperationRequired
	}
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	if cmd.Operation == OpPurge {
		cmd.MaxAffectedRows = s.maxBatchSize
	} else {
		cmd.MaxAffectedRows = 0
	}
	cmd = stampSchemaDigest(s.relationships, cmd)
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	guard := composeGuard(actor, s.guard, cmd)
	receipt, err := s.mutations.ApplyGuarded(ctx, cmd, guard, schemaValidatorFor(s.relationships))
	s.recordMutationAudit(ctx, actor, cmd, receipt, err)
	return receipt, err
}

// stampSchemaDigest stamps the governing compiled-schema digest onto a relationship
// command (unless the caller already set one) so the receipt records the schema that
// governed this application. It is metadata, excluded from the payload digest and
// ignored on replay. eng nil (relationship kind off) or a non-relationship operation
// leaves the digest empty. Shared by the actor-facing ApplyMutation and the trusted
// SystemMutator paths so both stamp identically.
func stampSchemaDigest(eng *authorizersvc.Service, cmd Command) Command {
	if eng != nil && cmd.SchemaDigest == "" && isRelationshipOperation(cmd.Operation) {
		cmd.SchemaDigest = eng.SchemaDigest()
	}
	return cmd
}

// schemaValidatorFor returns the receipt-absent current-schema validator Apply runs
// inside its boundary (nil when the relationship kind is off). It validates only
// ADDITIVE relationship operations (grant, replace): the rows they introduce must be
// allowed by the current schema (the full subject-type/relation pair). Revoke, purge,
// and role operations remove or carry no schema-shaped rows and are not validated —
// a relation the schema no longer accepts must still be revocable. Because Apply
// invokes it only when no receipt exists, an exact replay returns its stored receipt
// even after a schema change that would now reject the original relation (AZ3-0.4).
// Shared by the actor-facing and trusted SystemMutator paths so both validate
// identically.
func schemaValidatorFor(eng *authorizersvc.Service) SemanticValidator {
	if eng == nil {
		return nil
	}
	return func(cmd Command) error {
		switch cmd.Operation {
		case OpGrant, OpReplace:
			for _, row := range cmd.Relationships {
				if err := eng.ValidateRelation(cmd.Scope.Type, row.Relation, row.Subject.Type, row.Subject.Relation); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// isRelationshipOperation reports whether op mutates relationships (resource scope).
// It mirrors the mutation package's own classification without reaching into an
// unexported method.
func isRelationshipOperation(op Operation) bool {
	switch op {
	case OpGrant, OpRevoke, OpReplace, OpPurge, OpTeardown:
		return true
	}
	return false
}

// recordMutationAudit best-effort reports an actor-facing attempt to the
// AuditSink. It never changes the committed mutation or the returned result; a
// sink error is warned with coarse, bounded fields only (never raw resource or
// subject IDs).
func (s *Service) recordMutationAudit(ctx context.Context, actor Actor, cmd Command, receipt *Receipt, applyErr error) {
	if s.audit == nil {
		return
	}
	event := AuditEvent{
		MutationID: cmd.MutationID,
		Actor:      actor,
		Operation:  cmd.Operation,
		Scope:      cmd.Scope,
	}
	switch {
	case applyErr == nil && receipt != nil:
		event.Decision = AuditAccepted
		event.Outcome = receipt.Outcome
	case errors.Is(applyErr, sdk.ErrForbidden), errors.Is(applyErr, sdk.ErrUnauthorized):
		event.Decision = AuditDenied
		if reason, ok := ReasonFor(applyErr); ok {
			event.Reason = reason
		}
	default:
		event.Decision = AuditFailed
		if reason, ok := ReasonFor(applyErr); ok {
			event.Reason = reason
		}
	}
	if sinkErr := s.audit.RecordMutation(ctx, event); sinkErr != nil {
		s.logger().WarnContext(ctx, "authorization audit sink failed",
			"mutation_id", string(event.MutationID),
			"decision", string(event.Decision),
			"operation", string(event.Operation),
			"scope_kind", string(event.Scope.Kind),
			"reason", string(event.Reason),
			"error", sinkErr,
		)
	}
}

// logger returns the Service's logger, defaulting to slog.Default() when Register
// has not supplied one.
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
