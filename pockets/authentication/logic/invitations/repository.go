package invitations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// StatusUpdate conditionally changes an unclaimed  ExpectedTokenHash
// must identify its current token; a resend invalidates every older token. Only
// pending/expired rows may transition to pending, declined or cancelled.
type StatusUpdate struct {
	ExpectedTokenHash string
	Status            string
	TokenHash         string
	ExpiresAt         time.Time
	ResolvedSubjectID string
	UpdatedAt         time.Time
}

// Acceptance binds one current invitation token to a verified accepting subject.
// The invitation ID is the stable host grant operation ID. Now gates initial
// expiry and timestamps; a matching existing claim may resume after expiry.
type Acceptance struct {
	TokenHash   string
	SubjectType string
	SubjectID   string
	Now         time.Time
}

// ActiveListInput selects one resource's active invitations: those whose STORED
// status is pending or accepting. The wall clock never filters, so an expired
// but still pending row (which keeps reserving its tuple) is listed. ResourceType
// and ResourceID are required; a non-empty Relation filters exactly.
type ActiveListInput struct {
	ResourceType string
	ResourceID   string
	Relation     string
}

// InvitationRepository persists invitations and their durable acceptance claims.
// At most one active (pending or accepting) invitation may reserve the tuple
// (resource_type, resource_id, identifier_kind, identifier, relation). Create
// accepts pending rows only; active-tuple, ID or token collisions are
// sdk.ErrAlreadyExists. A completed/declined/cancelled row releases the tuple.
//
// ClaimAcceptance is the atomic proof-to-grant boundary. An accepting row cannot
// be declined or resent, and UpdateStatus never cancels it; failures retain its
// bound subject and token so another process can retry the same idempotent host
// operation. A claim is not a lease and never expires. CompleteAcceptance
// finalizes a matching claim; CancelAcceptance terminates one whose host grant
// the service has fenced.
//
// Reads return detached values. GetByTokenHash expires only unclaimed tokens;
// accepting/accepted records remain readable for bound-subject retries. Unknown
// ID/hash is sdk.ErrNotFound. Conditional state/token/subject mismatches are
// sdk.ErrConflict; expiry of an unclaimed token is sdk.ErrExpired.
//
// Lists filter the resource or exact (identifier_kind, identifier) pair and
// order by created_at DESC, id DESC with the shared list pagination contract.
type InvitationRepository interface {
	// Create persists a new pending invitation; a pending-tuple collision →
	// sdk.ErrAlreadyExists.
	Create(ctx context.Context, inv Invitation) (Invitation, error)
	// Get returns the invitation for id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Invitation, error)
	// GetByTokenHash returns the invitation for tokenHash; unknown → ErrNotFound,
	// unclaimed past-ExpiresAt → sdk.ErrExpired, else the detached record.
	GetByTokenHash(ctx context.Context, tokenHash string) (Invitation, error)
	// ListByResource returns a cursor-paginated page of a resource's invitations,
	// ordered created_at DESC, id DESC.
	ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Invitation], error)
	// ListBySubject returns a cursor-paginated page of invitations addressed to
	// (kind, identifier) — the invitee address and its kind — ordered created_at
	// DESC, id DESC. Both columns filter so a value shared across kinds never
	// cross-resolves.
	ListBySubject(ctx context.Context, kind, identifier string, req list.Request) (list.Page[Invitation], error)
	// UpdateStatus applies a current-token unclaimed transition; stale state/token
	// is ErrConflict, unknown id is ErrNotFound. Accepted transitions use the
	// claim methods below. Invalid StatusUpdate values are ErrInvalidInput.
	UpdateStatus(ctx context.Context, id string, upd StatusUpdate) (Invitation, error)
	// ClaimAcceptance atomically changes a current, unexpired pending token to
	// accepting and binds its subject. A matching accepting/accepted claim returns
	// the current row; another token/subject/state is ErrConflict. Unknown ID is
	// ErrNotFound; an unclaimed expired token is ErrExpired. No grant may precede it.
	ClaimAcceptance(ctx context.Context, id string, claim Acceptance) (Invitation, error)
	// CompleteAcceptance finalizes only the matching durable claim. Repeating the
	// same completion returns the accepted row without changing AcceptedAt.
	CompleteAcceptance(ctx context.Context, id string, claim Acceptance) (Invitation, error)
	// CancelAcceptance terminates only the matching durable claim: accepting →
	// cancelled, keeping the bound subject and token, AcceptedAt stays zero, no
	// expiry predicate (an accepting row past expiry cancels). A matching
	// cancelled row is returned unchanged. accepted, pending, declined,
	// cancelled-via-UpdateStatus, another token or subject are ErrConflict;
	// unknown id is ErrNotFound; an invalid claim is ErrInvalidInput.
	CancelAcceptance(ctx context.Context, id string, claim Acceptance) (Invitation, error)
	// ListActiveByResource returns a cursor-paginated page of the resource's
	// pending and accepting invitations by stored status, ordered created_at DESC,
	// id DESC; counts apply the same filter. It calls ActiveListInput.Validate,
	// and a non-blank req.Search is ErrInvalidInput (no searchable fields).
	ListActiveByResource(ctx context.Context, in ActiveListInput, req list.Request) (list.Page[Invitation], error)
}

// Validate rejects an active listing without a complete resource.
func (in ActiveListInput) Validate() error {
	if strings.TrimSpace(in.ResourceType) == "" || strings.TrimSpace(in.ResourceID) == "" {
		return fmt.Errorf("active invitation listing requires a resource type and id: %w", sdk.ErrInvalidInput)
	}
	return nil
}
