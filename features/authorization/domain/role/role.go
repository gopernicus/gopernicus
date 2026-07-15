// Package role is the public rim of the authorization feature's ROLES kind —
// a deliberately minimal role-assignment contract, independent of the
// relationship (ReBAC) kind. The backing table is `iam_roles`.
//
// A role assignment grants a subject an opaque role, optionally scoped to a
// resource. Roles are OPAQUE STRINGS the host interprets (the invitation
// Relation opacity precedent): v1 has no role registry or vocabulary — a role
// model is policy-seam-adjacent and deferred. This kind does plain lookups
// only; it has NO schema, NO graph walk, and never imports the relationship
// engine.
//
// # Scope
//
// The (ResourceType, ResourceID) pair scopes an assignment. The empty pair
// ("", "") means a GLOBAL assignment — EMPTY STRINGS, never NULL: the DDLs pin
// the scope columns NOT NULL with an empty-string default so a global
// assignment participates in the unique index under both dialects (a nullable
// scope would make two global
// rows DISTINCT under SQL NULL semantics, silently duplicating grants). A
// scoped assignment requires BOTH resource fields; a half-scoped pair is a
// caller error the service rejects.
//
// # Exact vs effective
//
// [Storer.HasExactRole] matches the scope EXACTLY at the store — a global
// assignment does not satisfy a scoped lookup and vice versa. The "a global
// grant also satisfies a scoped check" fallback is a SERVICE rule
// (rolesvc.Service.HasRole, Q5), never the store's.
//
// [Storer.ListByResource] is the RAW direct-scope listing: it returns the
// assignments stored EXACTLY at (resourceType, resourceID) and never surfaces
// globally-granted subjects. [Storer.ListEffectiveByResource] is its EFFECTIVE
// counterpart (AZ3-1.5): it unions the direct scoped assignments with the
// global assignments that the scoped HasRole fallback satisfies, so the grant
// set it enumerates AGREES with HasRole. A global grant is NOT rewritten as
// though it were stored at the requested scope — each [EffectiveGrant] carries
// explicit provenance instead. Use the raw listing to inspect what is stored at
// a scope; use the effective listing to enumerate who HasRole would allow.
package role

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// Assignment is a stored role grant. The empty (ResourceType, ResourceID) pair
// is a GLOBAL assignment; a non-empty pair scopes it to that resource.
//
// CreatedAt provenance: the STORE stamps it via the connector timestamp
// helpers; a duplicate Assign RETAINS the original timestamp (ON CONFLICT DO
// NOTHING semantics), never refreshing it.
type Assignment struct {
	SubjectType  string
	SubjectID    string
	Role         string
	ResourceType string // "" with ResourceID "" ⇒ global
	ResourceID   string
	CreatedAt    time.Time
}

// EffectiveGrant is one de-duplicated EFFECTIVE role grant on a resource,
// produced by [Storer.ListEffectiveByResource]. (SubjectType, SubjectID, Role)
// identify the grant; Direct and Global are its provenance:
//
//   - Direct: a scoped assignment is stored EXACTLY at the requested resource.
//   - Global: a global ("","") assignment exists that a scoped HasRole
//     satisfies as a fallback.
//
// Both may be true — the same subject holds the role directly AND globally. At
// least one is always true. A global grant is never rewritten as a scoped row:
// this type deliberately carries only the subject+role identity plus provenance,
// not a fabricated resource scope. Enumeration by Effective listing therefore
// describes the SAME grant set HasRole decides, without claiming the global
// assignment lives at the resource.
type EffectiveGrant struct {
	SubjectType string
	SubjectID   string
	Role        string
	Direct      bool
	Global      bool
}

// Provenance returns the grant's provenance label — "direct", "global", or
// "both". A grant always has at least one source, so the zero label never
// occurs on a value returned by the store.
func (g EffectiveGrant) Provenance() string {
	switch {
	case g.Direct && g.Global:
		return "both"
	case g.Global:
		return "global"
	default:
		return "direct"
	}
}

// Storer is the storage contract for the roles kind — plain lookups, no graph
// walk. The listing methods are crud-typed with the same cursor/tiebreak
// conventions as the relationship listings.
type Storer interface {
	// Assign inserts a role assignment. It is idempotent: a duplicate (same
	// subject, role, and scope) is a no-op returning nil, and the existing row
	// keeps its original CreatedAt (ON CONFLICT DO NOTHING).
	Assign(ctx context.Context, a Assignment) error

	// Unassign removes an exact assignment. It is idempotent: removing an absent
	// assignment (zero rows) returns nil.
	Unassign(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) error

	// HasExactRole reports whether an assignment exists at the EXACT scope. A
	// global assignment does not satisfy a scoped lookup and vice versa — the
	// global-fallback rule lives in the service (rolesvc.Service.HasRole, Q5),
	// not here.
	HasExactRole(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) (bool, error)

	// ListBySubject pages a subject's assignments (keyset; contractual order
	// created_at DESC with a stable tiebreak).
	ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[Assignment], error)

	// ListByResource is the RAW direct-scope listing: it pages the assignments
	// stored EXACTLY at (resourceType, resourceID) and never surfaces
	// globally-granted subjects. It is not effective — use it to inspect what is
	// stored at a scope, never to enumerate effective access.
	ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Assignment], error)

	// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the
	// union of the direct scoped assignments at (resourceType, resourceID) with
	// the global assignments a scoped HasRole satisfies, de-duplicated by
	// (subject, role) and each tagged with its provenance. Rows are ordered
	// deterministically by the (subject_type, subject_id, role) grant key
	// ascending BEFORE pagination, so the keyset cursor is stable. A global grant
	// is not rewritten as a scoped row (see [EffectiveGrant]). When the requested
	// scope is itself global ("",""), there is no fallback and every grant is
	// Direct — matching HasRole's no-fallback path for an unscoped query.
	ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[EffectiveGrant], error)
}
