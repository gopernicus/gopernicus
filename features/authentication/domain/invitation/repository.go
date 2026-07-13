package invitation

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// StatusUpdate is the mutable subset UpdateStatus persists for a lifecycle
// transition. The service reads the invitation first and passes the full
// intended state, so a store applies exactly these values (plus bumping
// UpdatedAt): accept sets Status/AcceptedAt/ResolvedSubjectID; decline and
// cancel set Status; resend sets Status/TokenHash/ExpiresAt. AcceptedAt zero
// means "not accepted"; ResolvedSubjectID empty means "unresolved".
type StatusUpdate struct {
	Status            string
	TokenHash         string
	ExpiresAt         time.Time
	AcceptedAt        time.Time
	ResolvedSubjectID string
	UpdatedAt         time.Time
}

// InvitationRepository persists resource invitations. Implemented by feature
// store adapters (features/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// THE PINNED UNIQUENESS CONTRACT (design §6, plan-cut amendment): at most ONE
// PENDING invitation may exist per (resource_type, resource_id, identifier,
// relation) — a colliding Create → sdk.ErrAlreadyExists. This is a PARTIAL
// uniqueness (pending rows only): once UpdateStatus moves a row off pending
// (accepted/declined/cancelled), a NEW pending invite for the same tuple
// SUCCEEDS. Stores express it as a partial/filtered unique index; the reference
// scans for a pending collision.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create colliding on the pending-tuple → sdk.ErrAlreadyExists.
//   - Get / UpdateStatus for an unknown id → sdk.ErrNotFound.
//   - GetByTokenHash for an unknown hash → sdk.ErrNotFound; a present row past
//     ExpiresAt → sdk.ErrExpired (a read-time expiry, mirroring the session /
//     verification / oauthstate precedent); else the record.
//
// ListByResource and ListBySubject are crud-typed (design §9). Ordering is
// CONTRACTUAL: ORDER BY created_at DESC, id DESC — the id tiebreak keeps pages
// stable when several invitations share a created_at (the storetest collision
// case asserts identical order AND NextCursor across implementations).
// ListBySubject keys on the invitee (identifier_kind, Identifier) TUPLE (design
// §7 re-key): it serves "my invitations" and the pending-invite finder resolve-
// on-registration pages over — visibility rides these table columns, never a
// tuple. The kind filter is load-bearing: the same normalized string may address
// two subjects under different kinds (an email and a phone), so a kind-blind
// lookup would leak/cross-grant across kinds.
type InvitationRepository interface {
	// Create persists a new pending invitation; a pending-tuple collision →
	// sdk.ErrAlreadyExists.
	Create(ctx context.Context, inv Invitation) (Invitation, error)
	// Get returns the invitation for id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Invitation, error)
	// GetByTokenHash returns the invitation for tokenHash; unknown → ErrNotFound,
	// present-but-past-ExpiresAt → sdk.ErrExpired, else the record.
	GetByTokenHash(ctx context.Context, tokenHash string) (Invitation, error)
	// ListByResource returns a cursor-paginated page of a resource's invitations,
	// ordered created_at DESC, id DESC.
	ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Invitation], error)
	// ListBySubject returns a cursor-paginated page of invitations addressed to
	// (kind, identifier) — the invitee address and its kind — ordered created_at
	// DESC, id DESC. Both columns filter so a value shared across kinds never
	// cross-resolves.
	ListBySubject(ctx context.Context, kind, identifier string, req crud.ListRequest) (crud.Page[Invitation], error)
	// UpdateStatus applies a lifecycle transition; unknown id → sdk.ErrNotFound.
	UpdateStatus(ctx context.Context, id string, upd StatusUpdate) (Invitation, error)
}
