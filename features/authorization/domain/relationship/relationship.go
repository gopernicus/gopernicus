// Package relationship is the public rim of the authorization feature's
// RELATIONSHIP kind — the ReBAC tuple contract. It defines the persisted tuple
// shape, the create input, the listing projections, and the [Storer] port that
// a store adapter (features/authorization/stores/{turso,pgx}), the in-core
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
// rim: authorizersvc holds a cryptids.IDGenerator (from the feature Config.IDs)
// and stamps [CreateRelationship.RelationshipID] on each tuple before calling
// [Storer.CreateRelationships]. There is deliberately no NewRelationship(ids,…)
// constructor — minting is a service concern for this feature (the item-14
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

// Storer is the storage contract for the relationship kind — the full 14-method
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
// The three LOOKUP methods DO carry a result cap: the engine passes its
// resolved MaxLookupResults+1 as limit, and each method returns AT MOST limit
// distinct IDs. A returned count equal to limit is how the engine distinguishes
// a bounded-complete result from an overflow it must report as ErrEvaluationLimit
// — never a silently truncated slice presented as complete (a store MUST NOT
// return more than limit, and MUST NOT drop the overflow signal by capping at
// limit-1). A non-positive limit means unbounded; the engine always passes a
// positive cap. The other engine budget dimensions (Through depth, distinct
// graph states, per-hop fan-out, batch size) are engine-scoped and are not
// threaded here.
//
// The listing methods are crud-typed (design §9): a crud.ListRequest in, a
// crud.Page[T] out. Ordering is contractual — created_at DESC, relationship_id
// DESC — so pages stay stable when several tuples share a created_at (bulk
// create stamps one timestamp for the whole batch, making the id tiebreak
// load-bearing).
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

	// LookupResourceIDs returns resource IDs where the subject has any of the
	// given direct relations (with group expansion), capped at limit distinct IDs
	// (see the Bounding note above).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error)

	// LookupResourceIDsByRelationTarget returns resource IDs that have a specific
	// relation pointing to any of the target IDs, capped at limit distinct IDs.
	// Used for through-relation traversal in LookupResources.
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, limit int) ([]string, error)

	// LookupDescendantResourceIDs returns all resource IDs reachable by walking a
	// self-referential relation transitively (recursive, cycle-safe), capped at
	// limit distinct IDs. Used when a Through target type equals the current
	// resource type (e.g. space→parent→space): given rootIDs=[S1] with
	// relation="parent", S1←S2←S3 yields [S2, S3].
	LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string, limit int) ([]string, error)
}
