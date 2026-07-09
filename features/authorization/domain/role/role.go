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
// (rolesvc.Service.HasRole, Q5), never the store's. [Storer.ListByResource]
// likewise returns DIRECT-scope assignments only and never surfaces
// globally-granted subjects that the service would allow — an accepted,
// documented v1 limitation ("effective grants for a resource" enumeration is
// deferred).
package role

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
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

// Storer is the storage contract for the roles kind — 5 methods, plain lookups,
// no graph walk. The listing methods are crud-typed with the same
// cursor/tiebreak conventions as the relationship listings.
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

	// ListByResource pages the assignments scoped to a resource. It returns
	// DIRECT-scope assignments ONLY — it never surfaces globally-granted
	// subjects that Service.HasRole would allow (documented v1 limitation).
	ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Assignment], error)
}
