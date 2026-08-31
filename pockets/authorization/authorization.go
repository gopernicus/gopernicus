// Package authorization is the public surface of the authorization pocket
// module: an IAM domain with two INDEPENDENTLY-WIREABLE KINDS.
//
//   - the RELATIONSHIP kind — the ReBAC engine (schema-driven permission checks,
//     group expansion, through-traversal, platform-admin data-tuple bypass,
//     relationship CRUD) over the `iam_relationships` table.
//   - the ROLES kind — opaque-string role assignments (assign/unassign,
//     scoped-or-global HasRole) over the `iam_roles` table, plus an OPTIONAL
//     Config.RoleModel declaring which roles grant which permissions. With a
//     model the kind decides; without one it is lookup-only.
//
// ReBAC is ONE kind, not the pocket's identity. A host wires either kind, both,
// or neither of a given kind's methods matter to it: a nil Repositories field
// turns that kind OFF structurally (deny-by-absence), and calling an unwired
// kind's methods returns a loud per-kind sentinel — never a silent allow.
//
// # Postures
//
// Authorization is "supported, never required": a host may run with no checks
// (posture 1), enforce at its own call sites with a plain closure over its own
// data (posture 2 — no IAM module in the graph), or adopt a fuller policy
// surface later (posture 3, the deferred policy seam). Within posture 3 the
// wired kinds share ONE decision surface, dispatched by pair ownership; what is
// deliberately NOT built is a cross-kind UNION or a universal-role bypass, which
// a host that needs one still composes in its own closure. Consumer seams are
// Check-ONLY; everything on Service beyond the boolean checks is
// flagship-specific API, never a cross-pocket seam (the AV2 split).
//
// The pocket is datastore-free and view-free (FS1): it depends on its
// relationship.Storer / role.Storer ports and sdk facilities only. Register
// mounts the bundled ROLE-ADMINISTRATION routes under /authorization/* when the
// host names a Config.RoleRoutesGate (assign, unassign, and the three role
// listings, JSON only) and mounts NOTHING otherwise; the rest of the
// /authorization/* namespace stays reserved. It does export the
// RequirePermission/RequirePermissionOn/
// RequirePermissionFixed/RequireAnyPermission middleware builders (root delegations to the internal
// implementation in middleware.go), so hosts can gate routes on a Check; those
// builders write their responses only through sdk/foundation/web, never at this
// root package.
package authorization

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	inbound "github.com/gopernicus/gopernicus/pockets/authorization/internal/inbound/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/decisionsvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/rolesvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// Construction and per-kind sentinel errors. A misconfigured host fails at
// NewService; calling an unwired kind fails closed at the call site.
var (
	// ErrNoKindConfigured is returned by NewService when neither kind is wired
	// (both Repositories fields nil) — an authorization pocket that does nothing.
	ErrNoKindConfigured = errors.New("authorization: no kind configured (Repositories.Relationships and Repositories.Roles are both nil)")

	// ErrModelRequired is returned by NewService for a partial relationship-kind
	// wiring: Repositories.Relationships is set without Config.RelationshipModel, or
	// Config.RelationshipModel is set without the repository. The relationship kind
	// needs both.
	ErrModelRequired = errors.New("authorization: Repositories.Relationships and Config.RelationshipModel must be wired together (both or neither)")

	// ErrRoleModelWithoutRoles is returned by NewService when Config.RoleModel is
	// set without Repositories.Roles. The asymmetry with ErrModelRequired is
	// deliberate: a roles repository with NO model is the valid opaque posture,
	// but a model with no repository could never decide anything.
	ErrRoleModelWithoutRoles = errors.New("authorization: Config.RoleModel requires Repositories.Roles (a role model with no roles kind decides nothing)")

	// ErrNoDecisionKind is returned by every decision method (Check, CheckBatch,
	// CheckExplain, FilterAuthorized, LookupResources, LookupResourcesIn) on a host where NO kind
	// bears a model — a roles-only wiring with no Config.RoleModel. It is a
	// SERVER-SIDE WIRING FAULT on the decision surface, not anything the caller
	// said: it wraps no sdk taxonomy kind, so ReasonFor reports no decision reason
	// and web.ErrFromDomain's default lands it at HTTP 500 — consistent with the
	// RequirePermission gates, which panic at mount for exactly this wiring. It is
	// deliberately UNLIKE ErrMutationsNotConfigured (400): that one is a
	// precondition an actor can observe and act on, this one is a deployment the
	// operator must fix. It never wraps sdk.ErrForbidden, which would falsely
	// present a wiring gap as a deny.
	ErrNoDecisionKind = errors.New("authorization: no decision-capable kind is configured (set Config.RelationshipModel for the relationship kind or Config.RoleModel for the roles kind)")

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

	// LookupRequest is the struct-input enumeration query LookupResourcesIn takes
	// — the positional LookupResources plus a caller Limit. See its own
	// documentation for the exact Limit semantics (0 is the MaxLookupResults
	// budget ceiling, not a page default) and the deferred After field.
	LookupRequest = authorizersvc.LookupRequest

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

	// RoleModel is the ROLES kind's permission model — which roles exist on each
	// resource type and which permissions they grant. Its Schema counterpart for
	// the relationship kind is Schema/ResourceTypeDef above; the two models may
	// share a resource TYPE but never a (type, permission) PAIR.
	RoleModel   = decisionsvc.RoleModel
	RoleTypeDef = decisionsvc.RoleTypeDef

	// RelationshipModel is the RELATIONSHIP kind's permission model — the ReBAC
	// schema Config.RelationshipModel carries. It is an alias of Schema, named for
	// symmetry with RoleModel so the two kinds read alike at the wiring site.
	RelationshipModel = Schema
)

// Explain step kinds — the coarse shape an ExplainStep records.
const (
	ExplainKindDirect  = authorizersvc.ExplainKindDirect
	ExplainKindThrough = authorizersvc.ExplainKindThrough
	ExplainKindRole    = authorizersvc.ExplainKindRole
)

// Explain step scopes — where an ExplainKindRole step's grant was found.
const (
	ExplainScopeDirect = authorizersvc.ExplainScopeDirect
	ExplainScopeGlobal = authorizersvc.ExplainScopeGlobal
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

// Root aliases — the optional high-integrity mutation vocabulary. Hosts use
// authorization.Command / .Receipt / …
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
	// stores/turso.WithGuardianPolicy). Config cannot carry it — the pocket core does
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
// idempotency: a SystemMutator holder (for a use case that opts into durable replay)
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

// Repositories is the set of outbound ports the pocket needs. Each kind is
// nil-safe: a nil field turns that kind OFF structurally.
type Repositories struct {
	// Relationships backs the ReBAC kind; nil = the relationship kind is off.
	Relationships relationship.Storer
	// Roles backs the roles kind; nil = the roles kind is off.
	Roles role.Storer

	// Mutations backs the optional high-integrity guarded/receipted write path. A
	// nil field leaves baseline RelationshipWriter operations fully available.
	// It is independent of the read/check ports above.
	Mutations mutation.MutationRepository
}

// Config carries each kind's model plus the settings shared across them.
// RelationshipModel and IDs are relationship-kind-scoped; RoleModel is
// roles-kind-scoped; Limits is the decision-surface budget charged by whichever
// kind bears a model; Guard and Audit configure the actor-facing mutation posture. A setting orphaned by an
// unwired kind is ignored with no error (the auth MailFrom precedent) — so
// negative Limits are only rejected once some model-bearing kind is wired.
//
// # The orphan rule
//
// Stated once, here, because the bundled role-administration routes add a third
// optional subsystem: a SECURITY-AFFECTING orphaned setting fails construction
// (Audit without Guard, AssignmentPolicy without RoleRoutesGate — a rule that
// can never run is a silent no-op giving false confidence); a COSMETIC orphaned
// setting is silently ignored (ListStrategy without a gate — the MailFrom
// precedent). An INVALID value is invalid either way: ListStrategy is validated
// whenever it is non-zero, orphaned or not.
type Config struct {
	// RelationshipModel is the relationship kind's model — the ReBAC schema.
	// Required when Repositories.Relationships is wired, forbidden otherwise
	// (ErrModelRequired).
	RelationshipModel Schema
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

	// RoleModel is the roles kind's permission model. Unset (no ResourceTypes)
	// = no model: the roles kind answers HasRole and the listings only
	// (today's behaviour) and takes no part in the decision
	// surface. Set requires Repositories.Roles (ErrRoleModelWithoutRoles); it is
	// validated at NewService (ErrInvalidRoleModel for a structurally invalid
	// model, ErrModelConflict for a pair declared by both models). While it is
	// set it ALSO governs role assignment: a (resource type, role) pair the model
	// does not declare is rejected with ErrInvalidRoleModel at assign time (D8),
	// on every high-integrity path. Unassignment and every read path stay opaque.
	RoleModel RoleModel

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

	// RoleRoutesGate mounts the bundled role-administration routes under
	// /authorization/* (assign, unassign, and the three role listings). Nil is the
	// default: NOTHING mounts and a host serves its own wire over the Service
	// methods. Non-nil requires Repositories.Roles (ErrRoleRoutesGateWithoutRoles)
	// and Config.Guard (ErrRoleRoutesGateWithoutGuard).
	//
	// THE GATE IS THE ENTIRE MIDDLEWARE STACK, not just an authorization check.
	// Unlike the authentication pocket, this pocket owns no credential and adds NO
	// middleware of its own beneath the gate — no authenticator, no CSRF layer — so
	// whatever the gate does not do, nothing does. It must therefore compose, in
	// this order:
	//
	//  1. AUTHENTICATION that stashes the principal with
	//     identity.WithPrincipal — the bundled writes read it back with
	//     identity.FromContext and answer 401 when it is absent (they never
	//     fabricate a zero Actor);
	//  2. for a COOKIE-credential host, a browser-origin/CSRF defense — these are
	//     state-changing POSTs and the pocket supplies no browserSafe layer;
	//  3. the AUTHORIZATION decision, e.g.
	//     authorizer.RequirePermissionFixed("platform", "admin", "platform").
	//
	// There is no web.Chain in sdk; compose them in the host:
	//
	//	gate := func(next http.Handler) http.Handler {
	//		return authenticate(browserSafe(authorize(next)))
	//	}
	//
	// The field stays a SINGLE middleware (the MachineRoutesGate precedent). The
	// five bundled paths are distinct, so a gate that wants read-wide/write-narrow
	// granularity switches on the request method or path itself.
	RoleRoutesGate web.Middleware

	// AssignmentPolicy is the optional legality pre-check the bundled assign route
	// consults before Service.AssignRole. It requires RoleRoutesGate
	// (ErrAssignmentPolicyWithoutRoutes). See [AssignmentPolicy] for the full
	// contract: legality only, assign only, bundled routes only, not audited, and
	// mapped through web.RespondJSONDomainError.
	AssignmentPolicy AssignmentPolicy

	// ListStrategy is the DEFAULT pagination strategy the bundled role listings
	// apply when a request names neither a cursor nor an offset param. The zero
	// value is crud.StrategyCursor; anything other than the zero value,
	// crud.StrategyCursor, or crud.StrategyOffset fails NewService with
	// ErrInvalidListStrategy. With no RoleRoutesGate it is a cosmetic orphan,
	// silently ignored (see the orphan rule above).
	ListStrategy crud.Strategy
}

// Service is the authorization pocket's host-facing surface. Each kind's method
// family is present unconditionally; an unwired kind's methods fail closed with
// that kind's sentinel. The decision surface (Check, CheckBatch, CheckExplain,
// FilterAuthorized, LookupResources and the RequirePermission gates) is ONE
// facade over the composite decider, which dispatches each (resource type,
// permission) pair to the model that declares it — the relationship Schema or the
// RoleModel, never both. A host still composes any bypass or cross-kind policy of
// its own in its own closure; the pocket merges no kinds.
type Service struct {
	relationships *authorizersvc.Service         // nil = relationship kind off
	roles         *rolesvc.Service               // nil = roles kind off
	roleModel     *decisionsvc.CompiledRoleModel // nil = the roles kind carries no model
	decider       *decisionsvc.Composite         // nil = no model-bearing kind (ErrNoDecisionKind)
	guard         MutationGuard                  // nil = read-only actor-mutation posture
	mutations     mutation.MutationRepository    // nil = no atomic write path
	audit         AuditSink                      // nil = no actor-mutation auditing
	maxBatchSize  int                            // resolved EvaluationLimits.MaxBatchSize (0 = no model-bearing kind)
	log           *slog.Logger                   // set at Register; falls back to slog.Default()

	roleRoutesGate   web.Middleware   // nil = the bundled role-administration routes do not mount
	assignmentPolicy AssignmentPolicy // nil = no bundled-assign legality pre-check
	listStrategy     crud.Strategy    // "" = crud.StrategyCursor at the bundled listings
}

// NewService validates the (repos, cfg) pair, builds the wired kinds, and returns
// the Components bundle: the host-facing Service plus separately held baseline
// and high-integrity write capabilities. Zero kinds is ErrNoKindConfigured; a
// relationship kind wired without its RelationshipModel (or vice versa) is
// ErrModelRequired; an invalid model is the schema validator's loud error. A
// roles-only wiring succeeds with no relationship model.
//
// Model construction matrix: Config.RoleModel without Repositories.Roles is
// ErrRoleModelWithoutRoles; a structurally invalid RoleModel is
// ErrInvalidRoleModel; a (resource type, permission) pair declared by BOTH models
// is ErrModelConflict. Config.Limits is resolved — and a negative field rejected
// with ErrInvalidLimits — whenever ANY model-bearing kind is wired; on a
// roles-only wiring with no RoleModel it stays an orphaned, ignored setting.
//
// Actor-mutation construction matrix (AZ3-0.5): a nil Config.Guard is the
// read-only posture (actor-facing mutations fail closed with
// ErrMutationsNotConfigured); a Guard without Repositories.Mutations fails with
// ErrGuardWithoutMutations; an Audit sink without a Guard is an orphaned
// actor-mutation setting and fails with ErrAuditWithoutGuard.
//
// Bundled role-administration construction matrix (issue #20): a
// Config.RoleRoutesGate without Repositories.Roles is
// ErrRoleRoutesGateWithoutRoles; a gate without Config.Guard is
// ErrRoleRoutesGateWithoutGuard (every bundled write would fail closed); a
// Config.AssignmentPolicy without a gate is ErrAssignmentPolicyWithoutRoutes; a
// Config.ListStrategy that names neither strategy is ErrInvalidListStrategy. The
// remaining route check — a gate with no Mount.Router — belongs to Register
// (ErrRoleRoutesWithoutRouter).
func NewService(repos Repositories, cfg Config) (Components, error) {
	hasRel := repos.Relationships != nil
	hasRoles := repos.Roles != nil
	if !hasRel && !hasRoles {
		return Components{}, ErrNoKindConfigured
	}

	model := cfg.RelationshipModel
	modelSet := len(model.ResourceTypes) > 0
	if hasRel != modelSet {
		return Components{}, ErrModelRequired
	}

	roleModelSet := cfg.RoleModel.IsSet()
	if roleModelSet && !hasRoles {
		return Components{}, ErrRoleModelWithoutRoles
	}

	if cfg.Guard != nil && repos.Mutations == nil {
		return Components{}, ErrGuardWithoutMutations
	}
	if cfg.Audit != nil && cfg.Guard == nil {
		return Components{}, ErrAuditWithoutGuard
	}

	if cfg.RoleRoutesGate != nil && !hasRoles {
		return Components{}, ErrRoleRoutesGateWithoutRoles
	}
	if cfg.RoleRoutesGate != nil && cfg.Guard == nil {
		return Components{}, ErrRoleRoutesGateWithoutGuard
	}
	if cfg.AssignmentPolicy != nil && cfg.RoleRoutesGate == nil {
		return Components{}, ErrAssignmentPolicyWithoutRoutes
	}
	if err := validateListStrategy(cfg.ListStrategy); err != nil {
		return Components{}, err
	}

	svc := &Service{
		guard:            cfg.Guard,
		mutations:        repos.Mutations,
		audit:            cfg.Audit,
		roleRoutesGate:   cfg.RoleRoutesGate,
		assignmentPolicy: cfg.AssignmentPolicy,
		listStrategy:     cfg.ListStrategy,
	}
	if hasRel {
		eng, err := authorizersvc.NewService(repos.Relationships, model, authorizersvc.Config{
			Limits: cfg.Limits,
			IDs:    cfg.IDs,
		})
		if err != nil {
			return Components{}, err
		}
		svc.relationships = eng
	}
	// The evaluation budget is the DECISION SURFACE's, not the relationship
	// kind's: it is resolved (and a negative field rejected) as soon as any
	// model-bearing kind is wired. Under a roles-only wiring with no role model
	// nothing consumes it and it stays silently orphaned. It is resolved AFTER
	// the relationship engine is built so an invalid model still reports
	// ErrInvalidSchema first, exactly as it did before the decision surface
	// gained a second model-bearing kind.
	var limits EvaluationLimits
	if hasRel || roleModelSet {
		resolved, err := cfg.Limits.Resolve()
		if err != nil {
			return Components{}, err
		}
		limits = resolved
	}
	if hasRoles {
		svc.roles = rolesvc.NewService(repos.Roles)
	}
	if roleModelSet {
		// Pair ownership (D1 rule 4) is checked against the relationship schema
		// when that kind is wired; a roles-only host has none to conflict with.
		var declared decisionsvc.Declarer
		if svc.relationships != nil {
			declared = svc.relationships
		}
		compiled, err := decisionsvc.CompileRoleModel(cfg.RoleModel, declared)
		if err != nil {
			return Components{}, err
		}
		svc.roleModel = compiled
	}
	if hasRel || roleModelSet {
		// The composite and the relationship engine share ONE resolved budget, so
		// the MaxBatchSize gate is not doubled in effect. maxBatchSize is captured
		// for the actor-facing mutation blast-radius bound.
		svc.decider = decisionsvc.NewComposite(svc.relationships, svc.roles, svc.roleModel, limits)
		svc.maxBatchSize = limits.MaxBatchSize
	}
	var writer *RelationshipWriter
	if svc.relationships != nil {
		writer = &RelationshipWriter{relationships: svc.relationships}
	}
	return Components{
		Service:            svc,
		RelationshipWriter: writer,
		// The trusted SystemMutator shares the same audit sink (when wired) so a
		// resource teardown is observed on the same seam as actor-facing attempts, and
		// the same relationship engine so its trusted calls stamp the governing schema
		// digest and run the current-schema semantic validator exactly as the guarded
		// seam does — it bypasses only the host MutationGuard, never the atomic contract.
		SystemMutator: &SystemMutator{mutations: repos.Mutations, audit: cfg.Audit, relationships: svc.relationships, roleModel: svc.roleModel},
	}, nil
}

// Register mounts the pocket: it logs one line, captures the Mount logger for
// best-effort audit warnings, and — when the host named a Config.RoleRoutesGate
// — mounts the bundled role-administration routes under /authorization/*. With
// no gate it registers NO routes and tolerates a zero-value Mount, exactly as
// before; the rest of the /authorization/* namespace stays reserved either way.
//
// With a gate set, a nil Mount.Router is ErrRoleRoutesWithoutRouter: routes were
// promised, so nowhere to mount them must be loud rather than silently
// route-free. With the roles kind and a Guard wired but NO gate, Register warns
// once — the bundled routes are not mounted and will answer 404, which an
// upgrading host should learn at boot rather than from production.
func (s *Service) Register(m pocket.Mount) error {
	if m.Logger != nil {
		s.log = m.Logger
		m.Logger.Info("registered authorization pocket",
			"relationships", s.relationships != nil,
			"roles", s.roles != nil,
			"role_model", s.roleModel != nil,
			"baseline_relationship_writes", s.relationships != nil,
			"actor_mutations", s.guard != nil,
			"role_routes", s.roleRoutesGate != nil,
		)
	}
	if s.roleRoutesGate == nil {
		if s.roles != nil && s.guard != nil {
			s.logger().Warn("authorization: the roles kind and Config.Guard are wired but Config.RoleRoutesGate is unset; the bundled role-administration routes are NOT mounted (404) — set a gate or serve your own routes over the Service methods")
		}
		return nil
	}
	if m.Router == nil {
		return ErrRoleRoutesWithoutRouter
	}
	inbound.Mount(m.Router, inbound.Deps{
		Service:      roleRouteAdapter{svc: s, policy: s.assignmentPolicy},
		Gate:         s.roleRoutesGate,
		ListStrategy: s.listStrategy,
	})
	return nil
}

// =============================================================================
// Decision surface (fails closed with ErrNoDecisionKind when NO kind bears a
// model)
//
// One facade, dispatched by pair ownership: each (resource type, permission) is
// answered by the model that declares it — the relationship Schema or the
// RoleModel, never both (ErrModelConflict forbids the overlap at construction).
// A pair neither model declares denies with "no rules defined". These six
// methods are the ONLY ones that report ErrNoDecisionKind; every other
// relationship-kind method below keeps ErrRelationshipsNotConfigured.
// =============================================================================

// Check evaluates a permission check on the model that declares the pair.
func (s *Service) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	if s.decider == nil {
		return CheckResult{}, ErrNoDecisionKind
	}
	return s.decider.Check(ctx, req)
}

// CheckExplain evaluates a permission check and returns a bounded Explanation of
// the rule/path decisions the OWNING model took. It rides the SAME evaluation
// path and work budget as Check — an explain request cannot create a separate,
// more permissive evaluator, cannot change the decision, and fails with the same
// limit class. The trace excludes raw infrastructure errors and is not logged
// automatically.
func (s *Service) CheckExplain(ctx context.Context, req CheckRequest) (CheckResult, Explanation, error) {
	if s.decider == nil {
		return CheckResult{}, Explanation{}, ErrNoDecisionKind
	}
	return s.decider.CheckExplain(ctx, req)
}

// CheckBatch evaluates multiple permission checks, each on its owning model. The
// MaxBatchSize ceiling is charged once for the whole batch.
func (s *Service) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if s.decider == nil {
		return nil, ErrNoDecisionKind
	}
	return s.decider.CheckBatch(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs the principal can access.
func (s *Service) FilterAuthorized(ctx context.Context, principal PrincipalRef, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if s.decider == nil {
		return nil, ErrNoDecisionKind
	}
	return s.decider.FilterAuthorized(ctx, principal, permission, resourceType, resourceIDs)
}

// LookupResources returns the resource IDs of a type the subject can access,
// enumerated by the model that declares the pair.
//
// Check/Lookup parity (D1(c) closed, AZ3-1.4): every resource a Check allows for
// a supported finite query is enumerated here. A self-referential Through
// hierarchy seeds its descendant walk from EVERY root the permission grants —
// direct grants AND roots derived through a non-self Through — so a grandchild
// Check honors is no longer omitted. IDs are returned sorted, each exactly once;
// limit exhaustion is ErrEvaluationLimit, never a partial list. A role-owned pair
// whose granting role is held GLOBALLY reports LookupResult.Unrestricted with an
// empty IDs — the host must then skip ID filtering entirely.
func (s *Service) LookupResources(ctx context.Context, principal PrincipalRef, permission, resourceType string) (LookupResult, error) {
	if s.decider == nil {
		return LookupResult{}, ErrNoDecisionKind
	}
	return s.decider.LookupResources(ctx, principal, permission, resourceType)
}

// LookupResourcesIn is LookupResources with a caller Limit — the struct-input
// sibling, not a replacement: LookupResources keeps its signature, so hosts and
// their ports are untouched.
//
// The owning kind enumerates exactly as LookupResources does — same parity, same
// deterministic sorted ordering, same budget — and the result is then capped to
// the first LookupRequest.Limit IDs, with LookupResult.Truncated set when that
// drops any. Limit 0 is the MaxLookupResults budget ceiling (today's behavior),
// deliberately NOT crud.ListRequest's DefaultLimit; a negative Limit is a
// validation error wrapping sdk.ErrInvalidInput (HTTP 400).
//
// The Limit NEVER weakens the budget: an enumeration that overflows
// MaxLookupResults is still ErrEvaluationLimit even for a tiny Limit, and v1
// Limit does not reduce enumeration cost — it moves the host's re-cap into the
// engine. An Unrestricted answer ignores the Limit and passes through untouched:
// the host must still skip ID filtering entirely.
func (s *Service) LookupResourcesIn(ctx context.Context, req LookupRequest) (LookupResult, error) {
	if s.decider == nil {
		return LookupResult{}, ErrNoDecisionKind
	}
	return s.decider.LookupResourcesIn(ctx, req)
}

// =============================================================================
// Relationship kind (fails closed with ErrRelationshipsNotConfigured when off)
// =============================================================================

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
