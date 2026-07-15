// Package authorization is the public surface of the authorization feature
// module: an IAM domain with two INDEPENDENTLY-WIREABLE KINDS.
//
//   - the RELATIONSHIP kind — the ReBAC engine (schema-driven permission checks,
//     group expansion, through-traversal, platform-admin data-tuple bypass,
//     relationship CRUD) over the `iam_relationships` table.
//   - the ROLES kind — minimal opaque-string role assignments (assign/unassign,
//     scoped-or-global HasRole) over the `iam_roles` table.
//
// ReBAC is ONE kind, not the feature's identity. A host wires either kind, both,
// or neither of a given kind's methods matter to it: a nil Repositories field
// turns that kind OFF structurally (deny-by-absence), and calling an unwired
// kind's methods returns a loud per-kind sentinel — never a silent allow.
//
// # Postures
//
// Authorization is "supported, never required": a host may run with no checks
// (posture 1), enforce at its own call sites by composing this feature's kinds
// in its own closure (posture 2 — there is deliberately NO composed Check facade
// here), or adopt a fuller policy surface later (posture 3, the deferred policy
// seam). Consumer seams are Check-ONLY; everything on Service beyond the boolean
// checks is flagship-specific API, never a cross-feature seam (the AV2 split).
//
// The feature is datastore-free and view-free (FS1): it depends on its
// relationship.Storer / role.Storer ports and sdk facilities only. Register
// mounts NO routes — the /authorization/* namespace is reserved for a future
// admin surface. It does export one HTTP middleware builder, RequirePermission
// (a root re-export of the internal engine implementation in middleware.go),
// so hosts can gate routes on a Check; that builder writes its responses only
// through sdk/foundation/web, never at this root package.
package authorization

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/rolesvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Construction and per-kind sentinel errors. A misconfigured host fails at
// NewService; calling an unwired kind fails closed at the call site.
var (
	// ErrNoKindConfigured is returned by NewService when neither kind is wired
	// (both Repositories fields nil) — an authorization feature that does nothing.
	ErrNoKindConfigured = errors.New("authorization: no kind configured (Repositories.Relationships and Repositories.Roles are both nil)")

	// ErrModelRequired is returned by NewService for a partial relationship-kind
	// wiring: Repositories.Relationships is set without Config.Model, or Config.Model
	// is set without the repository. The relationship kind needs both.
	ErrModelRequired = errors.New("authorization: Repositories.Relationships and Config.Model must be wired together (both or neither)")

	// ErrRelationshipsNotConfigured is returned by every relationship-kind method
	// when that kind is off (Repositories.Relationships was nil).
	ErrRelationshipsNotConfigured = errors.New("authorization: relationship kind is not configured")

	// ErrRolesNotConfigured is returned by every roles-kind method when that kind
	// is off (Repositories.Roles was nil).
	ErrRolesNotConfigured = errors.New("authorization: roles kind is not configured")
)

// Root aliases — the engine's model/check vocabulary, re-exported so hosts write
// authorization.CheckRequest{Principal: authorization.PrincipalRef{…}} without
// importing the internal engine package.
//
// PrincipalRef is the concrete decision caller/actor (Type, ID); SubjectRef is
// the stored relationship subject (Type, ID, Relation) — intentionally different
// types. A decision request carries a PrincipalRef only, never a userset.
type (
	PrincipalRef = authorizersvc.PrincipalRef
	SubjectRef   = relationship.SubjectRef
	Resource     = authorizersvc.Resource
	CheckRequest = authorizersvc.CheckRequest
	CheckResult  = authorizersvc.CheckResult
	LookupResult = authorizersvc.LookupResult

	// Explanation and ExplainStep are the opt-in, bounded explain trace returned
	// by CheckExplain. The trace shares the decision's evaluation budget, records
	// coarse rule/path decisions with a stable Outcome Reason (never a raw
	// infrastructure error), and is never exposed to ordinary callers or logged
	// automatically — a host asks for it explicitly.
	Explanation     = authorizersvc.Explanation
	ExplainStep     = authorizersvc.ExplainStep
	Schema          = authorizersvc.Schema
	SchemaSnapshot  = authorizersvc.SchemaSnapshot
	ResourceSchema  = authorizersvc.ResourceSchema
	ResourceTypeDef = authorizersvc.ResourceTypeDef
	RelationDef     = authorizersvc.RelationDef
	SubjectTypeRef  = authorizersvc.SubjectTypeRef
	PermissionRule  = authorizersvc.PermissionRule
	PermissionCheck = authorizersvc.PermissionCheck

	// EvaluationLimits is the resolved semantic work budget for one decision or
	// enumeration (Through depth, graph states, relation fan-out, batch size,
	// lookup results). Zero fields resolve to safe defaults; negatives fail
	// construction.
	EvaluationLimits = authorizersvc.EvaluationLimits
)

// Explain step kinds — the coarse shape an ExplainStep records.
const (
	ExplainKindDirect  = authorizersvc.ExplainKindDirect
	ExplainKindThrough = authorizersvc.ExplainKindThrough
)

// Resolved evaluation-budget defaults (each is the value a zero Config.Limits
// field takes). Re-exported so a host can size relative to the safe defaults.
const (
	DefaultMaxThroughDepth    = authorizersvc.DefaultMaxThroughDepth
	DefaultMaxGraphStates     = authorizersvc.DefaultMaxGraphStates
	DefaultMaxRelationTargets = authorizersvc.DefaultMaxRelationTargets
	DefaultMaxBatchSize       = authorizersvc.DefaultMaxBatchSize
	DefaultMaxLookupResults   = authorizersvc.DefaultMaxLookupResults
)

// Root aliases — the relationship rim types hosts pass to / receive from the
// relationship-kind methods.
type (
	CreateRelationship         = relationship.CreateRelationship
	RelationTarget             = relationship.RelationTarget
	SubjectRelationship        = relationship.SubjectRelationship
	ResourceRelationship       = relationship.ResourceRelationship
	SubjectRelationshipFilter  = relationship.SubjectRelationshipFilter
	ResourceRelationshipFilter = relationship.ResourceRelationshipFilter
)

// Assignment is the roles kind's grant record; hosts construct it via AssignRole
// arguments and receive it from the role listings.
type Assignment = role.Assignment

// EffectiveGrant is one de-duplicated effective role grant on a resource,
// returned by ListEffectiveRoleGrantsByResource with explicit provenance
// (Direct, Global, or both). A global grant is not rewritten as a scoped row.
type EffectiveGrant = role.EffectiveGrant

// Root aliases — the atomic mutation vocabulary (AZ3-0.4). The write path is
// frozen here as a contract; store implementations of MutationRepository land in
// phase 2 (AZ3-2.2/2.3/2.4), and the actor/guard/SystemMutator composition lands
// in AZ3-0.5. Hosts and phase-2 code use authorization.Command / .Receipt / …
// without importing the domain/mutation package directly.
type (
	MutationID         = mutation.MutationID
	Revision           = mutation.Revision
	ScopeKind          = mutation.ScopeKind
	ScopeKey           = mutation.ScopeKey
	Operation          = mutation.Operation
	RelationshipRow    = mutation.RelationshipRow
	RoleRow            = mutation.RoleRow
	Command            = mutation.Command
	Outcome            = mutation.Outcome
	Receipt            = mutation.Receipt
	Dependency         = mutation.Dependency
	DecisionView       = mutation.DecisionView
	Guard              = mutation.Guard
	SemanticValidator  = mutation.SemanticValidator
	MutationRepository = mutation.MutationRepository

	// GuardianPolicy / GuardianRule are the last-owner/guardian invariant vocabulary
	// (default #10 / AZ3-3.2): a protected relation on a resource type keeps at least
	// N DIRECT anchors after every ordinary command, enforced atomically inside the
	// MutationRepository under its scope lock.
	//
	// The sanctioned configuration seam is STORE CONSTRUCTION, not this Config: the
	// invariant is a repository-atomic post-state rule, so it must be known where the
	// atomic lock lives (memstore.WithGuardianPolicy, stores/pgx.WithGuardianPolicy,
	// stores/turso.WithGuardianPolicy). Config cannot carry it — the feature core does
	// not construct the store and could not push a policy into an already-built
	// MutationRepository without a detached, non-atomic seam. These aliases only make
	// the vocabulary reachable (authorization.GuardianPolicy) so a host names it
	// without importing domain/mutation. The default is DefaultGuardianPolicy (owner,
	// min-1, every type); an EXPLICITLY empty GuardianPolicy declares no invariant.
	GuardianPolicy = mutation.GuardianPolicy
	GuardianRule   = mutation.GuardianRule
)

// DefaultGuardianPolicy is the ratified default protected set (owner, minimum one
// direct anchor, on every resource type — default #10). A host passes it, a
// narrowed policy, or an explicitly empty GuardianPolicy to a store's
// WithGuardianPolicy option.
var DefaultGuardianPolicy = mutation.DefaultGuardianPolicy

// NewMutationID re-exports the canonical cryptographically strong idempotency-key
// generator (256-bit, base32).
var NewMutationID = mutation.NewMutationID

// DeriveMutationID re-exports the deterministic MutationID derivation for TRUSTED
// idempotency: a SystemMutator holder (bootstrap, migration, invitation acceptance)
// derives a stable MutationID from a fixed operation identity so a retry of the same
// operation dedups against its stored receipt — no duplicate mutation or revision
// bump — while still satisfying MutationID.Validate. Actor-facing callers use the
// unguessable NewMutationID instead.
var DeriveMutationID = mutation.DeriveMutationID

// Mutation scope kinds, operations, and stable domain outcomes.
const (
	ScopeResource = mutation.ScopeResource
	ScopeSubject  = mutation.ScopeSubject

	OpGrant        = mutation.OpGrant
	OpRevoke       = mutation.OpRevoke
	OpReplace      = mutation.OpReplace
	OpPurge        = mutation.OpPurge
	OpTeardown     = mutation.OpTeardown
	OpRoleAssign   = mutation.OpRoleAssign
	OpRoleUnassign = mutation.OpRoleUnassign

	OutcomeApplied          = mutation.OutcomeApplied
	OutcomeNoChange         = mutation.OutcomeNoChange
	OutcomeSemanticConflict = mutation.OutcomeSemanticConflict
	OutcomeInvariantBlocked = mutation.OutcomeInvariantBlocked
	OutcomeNotFound         = mutation.OutcomeNotFound

	MutationEncodingVersion = mutation.MutationEncodingVersion
)

// PrincipalFrom converts a platform identity.Principal into the concrete
// decision-caller PrincipalRef. The two types share the same (Type, ID) shape,
// so a host maps its resolved principal onto a decision request with one call.
func PrincipalFrom(p identity.Principal) PrincipalRef {
	return PrincipalRef{Type: p.Type, ID: p.ID}
}

// Schema DSL, re-exported for host schema construction.
var (
	NewSchema         = authorizersvc.NewSchema
	MergeResourceType = authorizersvc.MergeResourceType
	Direct            = authorizersvc.Direct
	Through           = authorizersvc.Through
	AnyOf             = authorizersvc.AnyOf
	Remove            = authorizersvc.Remove
)

// Repositories is the set of outbound ports the feature needs. Each kind is
// nil-safe: a nil field turns that kind OFF structurally.
type Repositories struct {
	// Relationships backs the ReBAC kind; nil = the relationship kind is off.
	Relationships relationship.Storer
	// Roles backs the roles kind; nil = the roles kind is off.
	Roles role.Storer

	// Mutations backs the atomic write path (mutation.MutationRepository). It is
	// the frozen AZ3-0.4 contract; a nil field means the atomic mutation surface is
	// not yet wired (store implementations land in phase 2). It is independent of
	// the read/check ports above — a store may implement all three.
	Mutations mutation.MutationRepository
}

// Config carries the relationship kind's settings. All three fields are
// relationship-kind-scoped; under a roles-only wiring they are ignored with no
// error (an orphaned tuning field is silent, the auth MailFrom precedent) — so
// negative Limits are only rejected when the relationship kind is actually wired.
type Config struct {
	// Model is the ReBAC schema. Required when Relationships is wired, forbidden
	// otherwise (ErrModelRequired).
	Model Schema
	// Limits is the resolved semantic evaluation budget (Through depth, graph
	// states, relation fan-out, batch size, lookup results). Each zero field
	// resolves to a safe nonzero default; a negative field fails NewService with
	// ErrInvalidLimits. Zero never means unlimited. Every dimension is charged per
	// decision (AZ3-1.3): exhaustion returns ErrEvaluationLimit (indeterminate),
	// never a deny or a truncated list. The lookup result cap is the one dimension
	// carried into a store (a MaxLookupResults+1 fetch, so overflow is
	// distinguishable); the rest are engine-scoped.
	Limits EvaluationLimits
	// IDs mints each relationship_id at CreateRelationships. The zero value is the
	// nanoid default; a cryptids.Database generator defers to the DDL DEFAULT.
	IDs cryptids.IDGenerator

	// Guard is the host authorization policy for actor-facing writes (AZ3-0.5). A
	// nil Guard is the READ-ONLY posture: decision/list APIs and the separately held
	// SystemMutator remain available, but every actor-facing mutation (the typed
	// guarded methods) fails closed with ErrMutationsNotConfigured. There is
	// no default allow guard. A non-nil Guard requires Repositories.Mutations
	// (ErrGuardWithoutMutations) so it can only be enforced inside the atomic
	// boundary.
	Guard MutationGuard

	// Audit is the optional best-effort sink for actor-facing mutation attempts
	// (accepted/denied/failed). It requires a Guard (ErrAuditWithoutGuard): with the
	// actor-mutation path off there is nothing for it to observe. Its failures are
	// warned and never change a committed mutation.
	Audit AuditSink
}

// Service is the authorization feature's host-facing surface. Each kind's method
// family is present unconditionally; an unwired kind's methods fail closed with
// that kind's sentinel. There is no composed Check facade — a host composes the
// kinds in its own closure.
type Service struct {
	relationships *authorizersvc.Service      // nil = relationship kind off
	roles         *rolesvc.Service            // nil = roles kind off
	guard         MutationGuard               // nil = read-only actor-mutation posture
	mutations     mutation.MutationRepository // nil = no atomic write path
	audit         AuditSink                   // nil = no actor-mutation auditing
	maxBatchSize  int                         // resolved EvaluationLimits.MaxBatchSize (0 = relationship kind off)
	log           *slog.Logger                // set at Register; falls back to slog.Default()
}

// NewService validates the (repos, cfg) pair, builds the wired kinds, and returns
// the Components bundle: the host-facing Service and the separately held, trusted
// SystemMutator (ratified default #4). Zero kinds is ErrNoKindConfigured; a
// relationship kind wired without its Model (or vice versa) is ErrModelRequired;
// an invalid Model is the schema validator's loud error. A roles-only wiring
// succeeds with no Model.
//
// Actor-mutation construction matrix (AZ3-0.5): a nil Config.Guard is the
// read-only posture (actor-facing mutations fail closed with
// ErrMutationsNotConfigured); a Guard without Repositories.Mutations fails with
// ErrGuardWithoutMutations; an Audit sink without a Guard is an orphaned
// actor-mutation setting and fails with ErrAuditWithoutGuard.
func NewService(repos Repositories, cfg Config) (Components, error) {
	hasRel := repos.Relationships != nil
	hasRoles := repos.Roles != nil
	if !hasRel && !hasRoles {
		return Components{}, ErrNoKindConfigured
	}

	modelSet := len(cfg.Model.ResourceTypes) > 0
	if hasRel != modelSet {
		return Components{}, ErrModelRequired
	}

	if cfg.Guard != nil && repos.Mutations == nil {
		return Components{}, ErrGuardWithoutMutations
	}
	if cfg.Audit != nil && cfg.Guard == nil {
		return Components{}, ErrAuditWithoutGuard
	}

	svc := &Service{
		guard:     cfg.Guard,
		mutations: repos.Mutations,
		audit:     cfg.Audit,
	}
	if hasRel {
		eng, err := authorizersvc.NewService(repos.Relationships, cfg.Model, authorizersvc.Config{
			Limits: cfg.Limits,
			IDs:    cfg.IDs,
		})
		if err != nil {
			return Components{}, err
		}
		svc.relationships = eng
		// The engine build above already validated the limits, so Resolve cannot
		// fail here; capture the resolved batch ceiling for the actor-facing
		// mutation blast-radius bounds.
		resolved, _ := cfg.Limits.Resolve()
		svc.maxBatchSize = resolved.MaxBatchSize
	}
	if hasRoles {
		svc.roles = rolesvc.NewService(repos.Roles)
	}
	return Components{
		Service: svc,
		// The trusted SystemMutator shares the same audit sink (when wired) so a
		// resource teardown is observed on the same seam as actor-facing attempts, and
		// the same relationship engine so its trusted calls stamp the governing schema
		// digest and run the current-schema semantic validator exactly as the guarded
		// seam does — it bypasses only the host MutationGuard, never the atomic contract.
		SystemMutator: &SystemMutator{mutations: repos.Mutations, audit: cfg.Audit, relationships: svc.relationships},
	}, nil
}

// Register mounts the feature: it logs one line, captures the Mount logger for
// best-effort audit warnings, and registers NO routes (the /authorization/*
// namespace is reserved). It tolerates a zero-value Mount.
func (s *Service) Register(m feature.Mount) error {
	if m.Logger != nil {
		s.log = m.Logger
		m.Logger.Info("registered authorization feature",
			"relationships", s.relationships != nil,
			"roles", s.roles != nil,
			"actor_mutations", s.guard != nil,
		)
	}
	return nil
}

// =============================================================================
// Relationship kind (fails closed with ErrRelationshipsNotConfigured when off)
// =============================================================================

// Check evaluates a permission check.
func (s *Service) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	if s.relationships == nil {
		return CheckResult{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.Check(ctx, req)
}

// CheckExplain evaluates a permission check and returns a bounded Explanation of
// the rule/path decisions taken. It rides the SAME evaluation path and work
// budget as Check — an explain request cannot create a separate, more permissive
// evaluator, cannot change the decision, and fails with the same limit class. The
// trace excludes raw infrastructure errors and is not logged automatically.
func (s *Service) CheckExplain(ctx context.Context, req CheckRequest) (CheckResult, Explanation, error) {
	if s.relationships == nil {
		return CheckResult{}, Explanation{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.CheckExplain(ctx, req)
}

// CheckBatch evaluates multiple permission checks.
func (s *Service) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.CheckBatch(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs the principal can access.
func (s *Service) FilterAuthorized(ctx context.Context, principal PrincipalRef, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.FilterAuthorized(ctx, principal, permission, resourceType, resourceIDs)
}

// LookupResources returns the resource IDs of a type the subject can access.
//
// Check/Lookup parity (D1(c) closed, AZ3-1.4): every resource a Check allows for
// a supported finite query is enumerated here. A self-referential Through
// hierarchy seeds its descendant walk from EVERY root the permission grants —
// direct grants AND roots derived through a non-self Through — so a grandchild
// Check honors is no longer omitted. IDs are returned sorted, each exactly once;
// limit exhaustion is ErrEvaluationLimit, never a partial list.
func (s *Service) LookupResources(ctx context.Context, principal PrincipalRef, permission, resourceType string) (LookupResult, error) {
	if s.relationships == nil {
		return LookupResult{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.LookupResources(ctx, principal, permission, resourceType)
}

// ValidateRelation reports whether a relationship is allowed by the schema,
// matching the full (subject type, subject relation) pair. subjectRelation is ""
// for a concrete subject and the userset relation otherwise.
func (s *Service) ValidateRelation(resourceType, relation, subjectType, subjectRelation string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.ValidateRelation(resourceType, relation, subjectType, subjectRelation)
}

// ValidateRelationships validates every relationship against the schema.
func (s *Service) ValidateRelationships(relationships []CreateRelationship) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.ValidateRelationships(relationships)
}

// GetSchema returns a deep, read-only snapshot of the relationship kind's
// compiled schema. The snapshot shares no memory with the runtime policy, so a
// caller can neither mutate the live schema nor race the engine.
func (s *Service) GetSchema() (SchemaSnapshot, error) {
	if s.relationships == nil {
		return SchemaSnapshot{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetSchema(), nil
}

// SchemaDigest returns the relationship kind's stable compiled-schema digest.
// Equivalent schemas yield an identical digest; any policy change yields a
// different one.
func (s *Service) SchemaDigest() (string, error) {
	if s.relationships == nil {
		return "", ErrRelationshipsNotConfigured
	}
	return s.relationships.SchemaDigest(), nil
}

// GetPermissionsForRelation returns the permissions a relation grants on a type.
func (s *Service) GetPermissionsForRelation(resourceType, relation string) ([]string, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetPermissionsForRelation(resourceType, relation), nil
}

// GetRelationTargets returns all subjects with a specific relation to a resource.
func (s *Service) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetRelationTargets(ctx, resourceType, resourceID, relation)
}

// ListRelationshipsBySubject pages the resources a subject relates to.
func (s *Service) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[SubjectRelationship], error) {
	if s.relationships == nil {
		return crud.Page[SubjectRelationship]{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, req)
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (s *Service) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[ResourceRelationship], error) {
	if s.relationships == nil {
		return crud.Page[ResourceRelationship]{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.ListRelationshipsByResource(ctx, resourceType, resourceID, filter, req)
}

// =============================================================================
// Roles kind (fails closed with ErrRolesNotConfigured when off)
//
// Role methods take a concrete PrincipalRef: userset subjects are structurally
// impossible here, so there is no runtime userset-rejection path — the type
// prevents it.
// =============================================================================

// HasRole reports whether a principal holds a role at a scope (with the global
// fallback: a global grant satisfies a scoped check).
func (s *Service) HasRole(ctx context.Context, principal PrincipalRef, roleName, resourceType, resourceID string) (bool, error) {
	if s.roles == nil {
		return false, ErrRolesNotConfigured
	}
	return s.roles.HasRole(ctx, principal.Type, principal.ID, roleName, resourceType, resourceID)
}

// ListRoleAssignmentsBySubject pages a principal's role assignments.
func (s *Service) ListRoleAssignmentsBySubject(ctx context.Context, principal PrincipalRef, req crud.ListRequest) (crud.Page[Assignment], error) {
	if s.roles == nil {
		return crud.Page[Assignment]{}, ErrRolesNotConfigured
	}
	return s.roles.ListRoleAssignmentsBySubject(ctx, principal.Type, principal.ID, req)
}

// ListRoleAssignmentsByResource pages the RAW direct-scope assignments stored at
// a resource. It never surfaces globally-granted subjects — use
// ListEffectiveRoleGrantsByResource for the enumeration that agrees with HasRole.
func (s *Service) ListRoleAssignmentsByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Assignment], error) {
	if s.roles == nil {
		return crud.Page[Assignment]{}, ErrRolesNotConfigured
	}
	return s.roles.ListRoleAssignmentsByResource(ctx, resourceType, resourceID, req)
}

// ListEffectiveRoleGrantsByResource pages the EFFECTIVE role grants on a
// resource: the union of the direct scoped assignments with the global
// assignments a scoped HasRole satisfies, de-duplicated by (subject, role) with
// explicit provenance. Its grant set agrees with HasRole (the Q5 fallback), so a
// subject allowed only via a global grant appears here with Global provenance —
// closing the enumeration-vs-decision divergence — without rewriting the global
// assignment as a scoped row. A generic access decision may still compose other
// role/ReBAC rules the host owns; this enumerates the roles kind only.
func (s *Service) ListEffectiveRoleGrantsByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[EffectiveGrant], error) {
	if s.roles == nil {
		return crud.Page[EffectiveGrant]{}, ErrRolesNotConfigured
	}
	return s.roles.ListEffectiveRoleGrantsByResource(ctx, resourceType, resourceID, req)
}
