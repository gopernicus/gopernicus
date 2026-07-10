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
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// CreateRelationship is one tuple to create, the input to
// [Storer.CreateRelationships].
//
// RelationshipID is populated by the engine from the injected generator
// (Q6) — hosts leave it zero. An empty RelationshipID instructs the store to
// omit the id column so the schema DEFAULT generates the key; a non-empty value
// is inserted verbatim. A batch is all-empty or all-populated, never mixed.
type CreateRelationship struct {
	RelationshipID  string
	ResourceType    string
	ResourceID      string
	Relation        string
	SubjectType     string
	SubjectID       string
	SubjectRelation *string // optional userset, e.g. "group:eng#member"
}

// RelationTarget is a subject related to a resource, returned by
// [Storer.GetRelationTargets] for "through" permission traversal.
type RelationTarget struct {
	SubjectType     string
	SubjectID       string
	SubjectRelation *string // optional userset relation
}

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
// store. Group expansion in the check/lookup methods is UNBOUNDED BUT
// CYCLE-SAFE (terminating by visited-set / UNION dedup, matching the original's
// recursive CTE); MaxTraversalDepth is an engine-only bound and is never
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

	// CheckRelationWithGroupExpansion reports whether a subject has a relation
	// to a resource, including indirectly via group membership. Cycle-safe and
	// unbounded (visited-set / UNION dedup).
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)

	// GetRelationTargets returns all subjects holding a specific relation on a
	// resource. Used for "through" permission traversal.
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error)

	// CheckRelationExists reports whether a specific direct relationship tuple
	// exists (no group expansion). Used for the platform-admin data-tuple check.
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)

	// CheckBatchDirect performs a batch permission check across resource IDs for
	// one relation, returning resourceID -> allowed. Optimized for list filtering.
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error)

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
	// given direct relations (with group expansion).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error)

	// LookupResourceIDsByRelationTarget returns resource IDs that have a specific
	// relation pointing to any of the target IDs. Used for through-relation
	// traversal in LookupResources.
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error)

	// LookupDescendantResourceIDs returns all resource IDs reachable by walking a
	// self-referential relation transitively (recursive, cycle-safe). Used when a
	// Through target type equals the current resource type (e.g. space→parent→space):
	// given rootIDs=[S1] with relation="parent", S1←S2←S3 yields [S2, S3].
	LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error)
}
