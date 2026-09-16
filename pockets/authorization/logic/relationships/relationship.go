package relationships

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// ErrExpansionBudgetExceeded reports that a bounded check-path group expansion
// discovered more distinct reachable states than the caller's maxExpansionStates
// budget allows. It is the store-layer overflow signal for
// [Storer.CheckRelationWithGroupExpansion] / [Storer.CheckBatchDirect]: an
// INDETERMINATE outcome, never a deny and never a truncated bool. It wraps
// [sdk.ErrUnavailable]; the engine maps it to its own ErrEvaluationLimit (which
// also wraps sdk.ErrUnavailable, so host-facing behavior is identical). The
// domain never imports the engine — the mapping happens at the engine boundary.
var ErrExpansionBudgetExceeded = fmt.Errorf("relationship: group-expansion budget exceeded: %w", sdk.ErrUnavailable)

// SubjectRef is a stored relationship subject — the exact tuple subject. An
// EMPTY Relation names a CONCRETE subject (user:u1); a NON-EMPTY Relation names
// the exact userset Type:ID#Relation (group:eng#member). Relation is one
// canonical string, never a pointer: the empty/non-empty distinction is the
// whole representation, and a store encodes empty as the empty string — there is
// no separate null state.
//
// The three fields are OPAQUE EXACT STRINGS: no case folding, trimming, or
// normalization is applied. Consequently group, group#member, and group#admin
// are three DISTINCT SubjectRefs that never compare equal — the userset relation
// is load-bearing, not decorative.
type SubjectRef = tuples.SubjectRef

// CreateRelationship is one tuple to create, the input to
// [Storer.CreateRelationships].
//
// SubjectRelation is one canonical string (empty = concrete subject; non-empty =
// the exact userset relation) — the [SubjectRef] representation flattened onto
// the tuple. Use [CreateRelationship.Subject] for the canonical view.
type CreateRelationship struct {
	ResourceType    string
	ResourceID      string
	Relation        string
	SubjectType     string
	SubjectID       string
	SubjectRelation string // "" = concrete subject; non-empty = the userset relation
}

// Subject returns the tuple's subject as a canonical [SubjectRef].
func (c CreateRelationship) Subject() SubjectRef {
	return SubjectRef{Type: c.SubjectType, ID: c.SubjectID, Relation: c.SubjectRelation}
}

// Validate reports whether the tuple is structurally well formed: resource
// type/id, relation, and the subject reference are all present and well formed.
// It does not consult a model; model-bound writers validate declared tuple shapes.
func (c CreateRelationship) Validate() error {
	return c.Tuple().Validate()
}

// Tuple returns the canonical resource-scoped fact, preserving the exact subject
// including its userset relation. Conversion does not validate the references;
// missing resource coordinates remain invalid and never imply global scope.
func (c CreateRelationship) Tuple() tuples.Tuple {
	return tuples.Tuple{
		Scope: tuples.On(c.ResourceType, c.ResourceID), Relation: c.Relation,
		Subject: c.Subject(),
	}
}

// RelationTarget is a subject holding a relation on a resource, returned by
// [Storer.GetRelationTargets] for "through" permission traversal. It IS a stored
// relationship subject, so it is exactly a [SubjectRef].
type RelationTarget = SubjectRef

// SubjectRelationship projects a tuple's resource and relation for a requested
// subject type/id. SubjectRelation preserves the exact concrete/userset identity.
type SubjectRelationship struct {
	ResourceType    string
	ResourceID      string
	Relation        string
	SubjectRelation string
}

// SubjectRelationshipFilter narrows [Storer.ListRelationshipsBySubject]. The
// subject (type + id) is a required method parameter, not part of this filter;
// every field here is optional (nil = no constraint on that dimension).
type SubjectRelationshipFilter struct {
	ResourceType *string // filter to a specific resource type (e.g. "tenant")
	Relation     *string // filter to a specific relation (e.g. "member", "admin")
}

// ResourceRelationship projects a tuple's exact subject and relation for a
// requested resource type/id. An empty SubjectRelation names a concrete subject.
type ResourceRelationship struct {
	SubjectType     string
	SubjectID       string
	SubjectRelation string
	Relation        string
}

// ResourceRelationshipFilter narrows [Storer.ListRelationshipsByResource]. The
// resource (type + id) is a required method parameter, not part of this filter;
// every field here is optional (nil = no constraint on that dimension).
type ResourceRelationshipFilter struct {
	SubjectType *string // filter to a specific subject type (e.g. "user")
	Relation    *string // filter to a specific relation (e.g. "owner")
}

// Storer is a resource-scoped facade over canonical tuples. The same facts may
// also be written or read through exact role APIs. Different relation labels
// coexist; the full scope, relation and exact subject identify a fact. Trusted
// raw writes retain the configured store IntegrityPolicy. Atomic commands enforce
// those rules inside the serialized repository boundary.
//
// # Bounding (AZ3-1.3, AZ3-5.7 F4)
//
// The CHECK-path group expansion (CheckRelationWithGroupExpansion,
// CheckBatchDirect) is BOUNDED by the caller-supplied maxExpansionStates: the
// number of DISTINCT reachable subject-reference states the expansion may visit
// before the call is declared indeterminate. A maxExpansionStates <= 0 means
// UNBOUNDED (cycle-safe by visited-set / UNION dedup only — the posture for a
// non-engine caller that opts out of a budget); the engine ALWAYS passes its
// resolved positive MaxGraphStates. When the distinct reachable states EXCEED
// maxExpansionStates the method returns [ErrExpansionBudgetExceeded] — never a
// deny, never a truncated bool. The engine maps that sentinel to its own
// ErrEvaluationLimit so the middleware/reasons treat it as the indeterminate
// budget class. A graph that fits WITHIN maxExpansionStates returns exactly the
// same bool it returned before the bound existed. Expansion stays cycle-safe by
// construction (visited-set / relation-aware UNION dedup).
//
// The three LOOKUP methods are KEYSET reads (authorization-lookup-paging, A2):
// each returns DISTINCT resource IDs sorted ascending in BYTE order, STRICTLY
// GREATER than after (after == "" means from the start), and AT MOST limit of
// them. The engine passes either its resolved MaxLookupResults+1 (a complete,
// budget-bounded enumeration: a returned count equal to limit is how it
// distinguishes a bounded-complete result from an overflow it must report as
// ErrEvaluationLimit — never a silently truncated slice presented as complete)
// or page+1 (a paged enumeration: the extra row is the HasMore lookahead). A
// store MUST NOT return more than limit, MUST NOT cap at limit-1, MUST NOT
// return an ID <= after, and MUST order by the raw bytes of resource_id — the
// engine merges several of these streams by Go string comparison, so a
// locale-collated order would skip or repeat IDs across pages (the pgx store
// pins COLLATE "C"; SQLite's BINARY is byte order). A non-positive limit means
// unbounded; the engine always passes a positive cap. The other engine budget
// dimensions (Through depth, distinct graph states, per-hop fan-out, batch
// size) are engine-scoped and are not threaded here.
//
// The listing methods are list-typed (design §9): a list.Request in, a
// list.Page[T] out. Default ordering is the full canonical tuple identity in
// byte order, exposed through opaque tuple_key cursors. Resource scope is fixed
// for these listings. Listings expose fact fields, not surrogate identifiers.
//
// Ambient transactions. When ctx carries the connector's Transact-owned
// transaction (sdk/capabilities/transaction.Transactor — the connector stashes the
// transaction in the context and its own QuerierFrom/TxFromContext retrieve
// it), EVERY method of the store — reads and writes alike — runs ON that
// transaction and never opens, commits, or rolls back one of its own; the
// enclosing Transact decides the outcome from its callback's return value. A
// host must return a write error from that callback to roll the whole workflow
// back; returning nil requests
// commit of everything before it. Outside an ambient transaction behavior is
// unchanged. A SetRelationTargets that must serialize concurrent callers does
// so with a lock scoped to the ambient transaction, so the serialization lasts
// until the host's commit. The contract names the connector's ambient
// transaction, not a dialect: a store over any connector honors it the same
// way, and the storetest RunTransactional family is its executable form.
// Standalone listings and counts use a coherent tuple snapshot. In an ambient
// transaction, these raw methods retain the host's isolation, including READ
// COMMITTED. Decision operations and the tuple-backed Service require suitable
// snapshot isolation instead. Invalid selectors and nonblank search are rejected.
type Storer interface {
	// ForModel binds every permission read to the current immutable host model.
	// The returned reader must use this store's same transaction context.
	ForModel(ReadModel) Reader

	// -------------------------------------------------------------------
	// Permission checks
	// -------------------------------------------------------------------

	// CheckRelationWithGroupExpansion reports whether a CONCRETE subject has a
	// relation to a resource, including indirectly via exact userset membership.
	// Expansion is relation-aware: a grant referencing group#admin is satisfied
	// only by admin membership, never by group#member, and a concrete-group grant
	// is satisfied only by the group entity itself. Cycle-safe (visited-set /
	// UNION dedup on the full subject-relation-carrying key). maxExpansionStates
	// bounds the distinct reachable states (see the Bounding note above);
	// exceeding it returns ErrExpansionBudgetExceeded, never a deny.
	// maxExpansionStates <= 0 means unbounded.
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)

	// GetRelationTargets returns all subjects holding a specific relation on a
	// resource. Used for "through" permission traversal.
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error)

	// CheckRelationExists reports whether a specific direct relationship tuple
	// exists for a CONCRETE subject (no expansion; a stored userset tuple with the
	// same type/id does not satisfy it). This is raw fact inspection, not graph
	// expansion or an integrity-policy count.
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)

	// CheckBatchDirect performs a batch permission check across resource IDs for
	// one relation, returning resourceID -> allowed. Optimized for list filtering.
	// maxExpansionStates bounds the distinct reachable states of the shared subject
	// expansion (see the Bounding note above); exceeding it returns
	// ErrExpansionBudgetExceeded, never a partial map. maxExpansionStates <= 0
	// means unbounded.
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error)

	// -------------------------------------------------------------------
	// Relationship CRUD
	// -------------------------------------------------------------------

	// CreateRelationships inserts a batch of full-identity facts atomically.
	// Exact duplicates are idempotent; different relations for the same resource
	// and subject coexist. An empty batch adds no facts.
	CreateRelationships(ctx context.Context, relationships []CreateRelationship) error

	// SetRelationTargets atomically makes the stored targets for exactly one
	// (resource_type, resource_id, relation) equal the supplied set. Every input
	// row must name that same resource and relation. Existing matching rows are
	// retained, absent desired rows are inserted, and rows not in the desired set
	// are removed. An empty set clears the relation. Repeating a desired state is
	// a no-op, and concurrent calls for the same key must serialize rather than
	// merge into an accidental union. Store adapters must provide real transaction
	// or lock atomicity; a delete-then-create implementation is non-conforming.
	//
	// The writer validates and de-duplicates rows. Other relation labels are
	// independent facts and must remain unchanged, even for the same subject.
	SetRelationTargets(ctx context.Context, resourceType, resourceID, relation string, targets []CreateRelationship) error

	// DeleteRelationshipTarget removes one exact tuple, including the userset
	// relation carried by target. Deleting an absent tuple is nil (idempotent).
	DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relation string, target SubjectRef) error

	// DeleteResourceRelationships removes every relationship for a resource.
	DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error

	// DeleteRelationship removes one specific relationship tuple. Deleting an
	// absent tuple is nil (idempotent).
	DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error

	// DeleteByResourceAndSubject removes every relation a subject holds on a
	// specific resource. Idempotent.
	DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error

	// -------------------------------------------------------------------
	// Counts
	// -------------------------------------------------------------------

	// CountByResourceAndRelation counts stored tuples for a resource+relation,
	// including userset references, without expanding them. Integrity minima
	// count only concrete subjects through IntegrityPolicy, not this method.
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)

	// -------------------------------------------------------------------
	// Listing (list-typed, contractual order: tuple_key ASC)
	// -------------------------------------------------------------------

	// ListRelationshipsBySubject pages the resources a subject relates to.
	ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req list.Request) (list.Page[SubjectRelationship], error)

	// ListRelationshipsByResource pages the subjects related to a resource.
	ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req list.Request) (list.Page[ResourceRelationship], error)

	// -------------------------------------------------------------------
	// LookupAllResourceIDs
	// -------------------------------------------------------------------

	// LookupResourceIDs returns the resource IDs where the subject has any of the
	// given direct relations (with group expansion): distinct, byte-order sorted,
	// strictly greater than after, at most limit (see the Bounding note above).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error)

	// LookupResourceIDsByRelationTarget returns the resource IDs whose relation
	// points at any of the target IDs (concrete subjects only, no expansion):
	// distinct, byte-order sorted, strictly greater than after, at most limit.
	// Used for through-relation traversal in LookupAllResourceIDs.
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error)

	// LookupDescendantResourceIDs returns the resource IDs reachable from the
	// root IDs by walking the UNION of the self-referential relations
	// transitively (recursive, cycle-safe; a path may alternate relations), then
	// applies after/limit to the sorted, distinct closure. Used when a Through
	// target type equals the current resource type (e.g. space→parent→space):
	// given rootIDs=[S1] with relations=["parent"], S1←S2←S3 yields [S2, S3]. A
	// root is returned only when a cycle makes it a genuine descendant. The
	// database computes the whole closure on every call; after/limit page its
	// result, not its work (plan A3).
	LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error)
}
