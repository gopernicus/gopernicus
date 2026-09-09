// Package relationship is the public rim of the authorization pocket's
// RELATIONSHIP kind — the ReBAC tuple contract. It defines the persisted tuple
// shape, the create input, the listing projections, and the [Storer] port that
// a store adapter (pockets/authorization/stores/{turso,pgx}), the in-core
// memstore, or any host implementation fills. The backing table is
// `iam_relationships` (owner direction, 2026-07-08 — the original `rebac_`
// name does not survive).
//
// A relationship is a tuple (resource_type, resource_id, relation,
// subject_type, subject_id, subject_relation): "subject has relation on
// resource", where an optional subject_relation names a userset
// ("group:eng#member") rather than a concrete principal. The engine
// (internal/logic/authorizersvc) evaluates permission checks over these tuples
// against a schema; this rim is storage-only and holds no evaluation logic.
//
// # Identity (Q6, 2026-07-09)
//
// relationship_id is a surrogate key MINTED AT THE ENGINE SEAM, not in this
// rim: authorizersvc holds a cryptids.IDGenerator (from the pocket Config.IDs)
// and stamps [CreateRelationship.RelationshipID] on each tuple before calling
// [Storer.CreateRelationships]. There is deliberately no NewRelationship(ids,…)
// constructor — minting is a service concern for this pocket (the item-14
// entity-ID obligation carries this recorded exception). Hosts leave
// RelationshipID zero; under a cryptids.Database generator every id is "" and
// the store omits the id column so the DDL DEFAULT fills it. The mint is
// all-or-none per batch (one generator), so a batch is either all-empty or
// all-populated — never mixed.
//
// # Duplicate semantics (2026-07-08 owner ruling; Q7, 2026-07-09)
//
// A subject holds AT MOST ONE relation on a resource (owner OR member, never
// both — the schema's AnyOf handles implication). The store enforces this with
// a unique index on (subject_type, subject_id, resource_type, resource_id)
// under a bare ON CONFLICT DO NOTHING: a second, DIFFERENT relation for the
// same subject on the same resource is a SILENT NO-OP (nil error, the existing
// relation unchanged — NOT ErrAlreadyExists), and a role change stays a
// delete+create. An exact-duplicate tuple is likewise an idempotent no-op.
package relationship

import (
	"context"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// MaxRefFieldLen bounds an opaque reference component (a type, id, relation, or
// permission name). It is a byte bound, applied after the UTF-8 validity check.
const MaxRefFieldLen = 256

// ErrInvalidRef indicates a reference component is empty, over-long, not valid
// UTF-8, or carries a control character. It wraps [sdk.ErrInvalidInput].
var ErrInvalidRef = fmt.Errorf("authorization reference: %w", sdk.ErrInvalidInput)

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
type SubjectRef struct {
	Type     string
	ID       string
	Relation string // "" = concrete subject; non-empty = the exact userset relation
}

// IsUserset reports whether the ref names a userset (a non-empty Relation).
func (s SubjectRef) IsUserset() bool { return s.Relation != "" }

// String renders the canonical Type:ID(#Relation) form for logs and debug
// output. It is not a parse target.
func (s SubjectRef) String() string {
	if s.Relation == "" {
		return s.Type + ":" + s.ID
	}
	return s.Type + ":" + s.ID + "#" + s.Relation
}

// Validate reports whether the ref is structurally usable: Type and ID must be
// present and well formed; Relation is optional but, when present, must be well
// formed too (see [ValidateRefField]). It applies no schema knowledge.
func (s SubjectRef) Validate() error {
	if err := ValidateRefField("subject type", s.Type); err != nil {
		return err
	}
	if err := ValidateRefField("subject id", s.ID); err != nil {
		return err
	}
	if s.Relation != "" {
		if err := ValidateRefField("subject relation", s.Relation); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRefField reports whether an opaque reference component is well formed:
// non-empty, at most [MaxRefFieldLen] bytes, valid UTF-8, and free of control
// characters. The value is treated as an opaque exact string — no case folding
// or trimming is applied. field names the component for the error message.
func ValidateRefField(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, ErrInvalidRef)
	}
	if len(value) > MaxRefFieldLen {
		return fmt.Errorf("%s exceeds %d bytes: %w", field, MaxRefFieldLen, ErrInvalidRef)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8: %w", field, ErrInvalidRef)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character: %w", field, ErrInvalidRef)
		}
	}
	return nil
}

// CreateRelationship is one tuple to create, the input to
// [Storer.CreateRelationships].
//
// RelationshipID is populated by the engine from the injected generator
// (Q6) — hosts leave it zero. An empty RelationshipID instructs the store to
// omit the id column so the schema DEFAULT generates the key; a non-empty value
// is inserted verbatim. A batch is all-empty or all-populated, never mixed.
//
// SubjectRelation is one canonical string (empty = concrete subject; non-empty =
// the exact userset relation) — the [SubjectRef] representation flattened onto
// the tuple. Use [CreateRelationship.Subject] for the canonical view.
type CreateRelationship struct {
	RelationshipID  string
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
// It does NOT consult the schema — schema conformance is the engine's
// ValidateRelationships.
func (c CreateRelationship) Validate() error {
	if err := ValidateRefField("resource type", c.ResourceType); err != nil {
		return err
	}
	if err := ValidateRefField("resource id", c.ResourceID); err != nil {
		return err
	}
	if err := ValidateRefField("relation", c.Relation); err != nil {
		return err
	}
	return c.Subject().Validate()
}

// RelationTarget is a subject holding a relation on a resource, returned by
// [Storer.GetRelationTargets] for "through" permission traversal. It IS a stored
// relationship subject, so it is exactly a [SubjectRef].
type RelationTarget = SubjectRef

// SubjectRelationship is a resource a subject has a relationship with —
// a projection row of [Storer.ListRelationshipsBySubject] ("what resources does
// this subject have access to?"). ID is the surrogate relationship_id: it is
// the keyset tiebreak (created_at DESC, relationship_id DESC) and the vehicle
// the DB-generated-id conformance case reads to prove a minted key is non-empty.
type SubjectRelationship struct {
	ID           string
	ResourceType string
	ResourceID   string
	Relation     string
	CreatedAt    time.Time
}

// SubjectRelationshipFilter narrows [Storer.ListRelationshipsBySubject]. The
// subject (type + id) is a required method parameter, not part of this filter;
// every field here is optional (nil = no constraint on that dimension).
type SubjectRelationshipFilter struct {
	ResourceType *string // filter to a specific resource type (e.g. "tenant")
	Relation     *string // filter to a specific relation (e.g. "member", "admin")
}

// ResourceRelationship is a subject's relationship to a resource — a projection
// row of [Storer.ListRelationshipsByResource] ("what subjects have access to
// resource X?"). ID is the surrogate relationship_id (keyset tiebreak +
// DB-generated-id assertion vehicle, as on [SubjectRelationship]).
type ResourceRelationship struct {
	ID          string
	SubjectType string
	SubjectID   string
	Relation    string
	CreatedAt   time.Time
}

// ResourceRelationshipFilter narrows [Storer.ListRelationshipsByResource]. The
// resource (type + id) is a required method parameter, not part of this filter;
// every field here is optional (nil = no constraint on that dimension).
type ResourceRelationshipFilter struct {
	SubjectType *string // filter to a specific subject type (e.g. "user")
	Relation    *string // filter to a specific relation (e.g. "owner")
}

// Storer is the storage contract for the relationship kind — the full 18-method
// surface the engine needs for permission checks, tuple CRUD, direct counts,
// listing, and resource lookup. It is intentionally lean: business logic
// (last-owner guards, role-change validation) lives on the engine, not the
// store.
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
// # Set reads (authorization-batch-decision, S1)
//
// FilterRelation and RelationTargetsFor are the SET forms of the two
// permission-walk reads: one read per (branch, hop) over a whole candidate set
// instead of one per (candidate, branch, hop). They answer EXACTLY what a loop
// over their per-resource siblings answers — same expansion, same userset
// exactness, same bounding sentinel — so a store proves them by parity against
// CheckRelationWithGroupExpansion / GetRelationTargets in storetest.
//
// The candidate set is bounded by the CALLER (the engine passes at most
// MaxBatchSize ids), so the port declares no id-count ceiling of its own. A
// store whose dialect bounds the parameters of one statement CHUNKS internally
// and merges the chunks — it never rejects an id list and never returns a
// partial answer. Chunking is invisible: the output contract (distinct, byte
// order, subset) holds over the whole input.
//
// The listing methods are crud-typed (design §9): a crud.ListRequest in, a
// crud.Page[T] out. Ordering is contractual — created_at DESC, relationship_id
// DESC — so pages stay stable when several tuples share a created_at (bulk
// create stamps one timestamp for the whole batch, making the id tiebreak
// load-bearing).
//
// Ambient transactions. When ctx carries the connector's Transact-owned
// transaction (sdk/foundation/crud.Transactor — the connector stashes the
// transaction in the context and its own QuerierFrom/TxFromContext retrieve
// it), EVERY method of the store — reads and writes alike — runs ON that
// transaction and never opens, commits, or rolls back one of its own; the
// enclosing Transact decides the outcome from its callback's return value. A
// host must return a write error from that callback (SetRelationTargets'
// conflict included) to roll the whole workflow back; returning nil requests
// commit of everything before it. Outside an ambient transaction behavior is
// unchanged. A SetRelationTargets that must serialize concurrent callers does
// so with a lock scoped to the ambient transaction, so the serialization lasts
// until the host's commit. The contract names the connector's ambient
// transaction, not a dialect: a store over any connector honors it the same
// way, and the storetest RunTransactional family is its executable form.
type Storer interface {
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

	// FilterRelation reports WHICH of resourceIDs carry relation for the subject,
	// directly or through exact userset (group) expansion — the SET form of
	// CheckRelationWithGroupExpansion, answered in ONE read (see the Set reads
	// note above). Output is DISTINCT, sorted ascending in BYTE order, and a
	// SUBSET of resourceIDs; a duplicated input id appears at most once. An empty
	// resourceIDs is an empty result and NO store call. maxExpansionStates bounds
	// the shared subject expansion exactly as it bounds the per-resource method:
	// exceeding it returns ErrExpansionBudgetExceeded — never a short list.
	// maxExpansionStates <= 0 means unbounded.
	FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error)

	// RelationTargetsFor returns, for each of resourceIDs, the subjects holding
	// relation on it — the SET form of GetRelationTargets, answered in ONE read
	// (see the Set reads note above). An id with NO targets is ABSENT from the
	// map (a missing key reads as the nil slice callers range over), and a
	// duplicated input id carries one entry. An empty resourceIDs is an empty map
	// and NO store call. Userset targets are returned as stored; the permission
	// walk skips them.
	RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]RelationTarget, error)

	// CheckRelationExists reports whether a specific direct relationship tuple
	// exists for a CONCRETE subject (no expansion; a stored userset tuple with the
	// same type/id does not satisfy it). Used for the platform-admin data-tuple
	// check and last-owner counting.
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

	// CreateRelationships inserts a batch of tuples. It is error-only (no
	// RETURNING): the id is minted by the engine (Q6). An empty-id batch omits
	// the id column so the DDL DEFAULT fills each key; a populated batch inserts
	// the ids verbatim. A second, different relation for the same subject on the
	// same resource — and an exact-duplicate tuple — is a SILENT NO-OP under the
	// bare ON CONFLICT DO NOTHING (nil error, existing row unchanged, never
	// ErrAlreadyExists). An empty batch is nil.
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
	// The engine validates and de-duplicates the rows before calling this method.
	// Store implementations should nevertheless reject a desired target that is
	// already related to the resource under a different relation, because the
	// one-relation-per-subject invariant would otherwise make the requested state
	// impossible. The whole operation must then roll back unchanged.
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

	// CountByResourceAndRelation counts DIRECT tuples for a resource+relation.
	// It counts direct tuples ONLY, never expanded membership — a count
	// divergence is a security divergence (design §2.5): last-owner protection
	// depends on this being direct-only.
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)

	// -------------------------------------------------------------------
	// Listing (crud-typed, contractual order: created_at DESC, relationship_id DESC)
	// -------------------------------------------------------------------

	// ListRelationshipsBySubject pages the resources a subject relates to.
	ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[SubjectRelationship], error)

	// ListRelationshipsByResource pages the subjects related to a resource.
	ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[ResourceRelationship], error)

	// -------------------------------------------------------------------
	// LookupResources
	// -------------------------------------------------------------------

	// LookupResourceIDs returns the resource IDs where the subject has any of the
	// given direct relations (with group expansion): distinct, byte-order sorted,
	// strictly greater than after, at most limit (see the Bounding note above).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error)

	// LookupResourceIDsByRelationTarget returns the resource IDs whose relation
	// points at any of the target IDs (concrete subjects only, no expansion):
	// distinct, byte-order sorted, strictly greater than after, at most limit.
	// Used for through-relation traversal in LookupResources.
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
