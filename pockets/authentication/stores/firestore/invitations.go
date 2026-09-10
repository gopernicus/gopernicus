package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ invitation.InvitationRepository = (*invitationStore)(nil)

// invitationStore fills invitation.InvitationRepository over the invitations
// collection and its two claims — the token hash and the PARTIAL pending tuple
// (SCHEMA.md §5.5, §5.6). Bodies land in N4b.
type invitationStore struct {
	db *firestoredb.DB
}

func newInvitationStore(db *firestoredb.DB) *invitationStore {
	return &invitationStore{db: db}
}

// Create writes the invitation with its token claim and — while the stored
// status is pending — its pending-tuple claim.
func (s *invitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	return invitation.Invitation{}, errNotImplemented
}

// Get returns the invitation by id, or sdk.ErrNotFound.
func (s *invitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	return invitation.Invitation{}, errNotImplemented
}

// GetByTokenHash resolves the mailed secret's hash through its claim document.
func (s *invitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	return invitation.Invitation{}, errNotImplemented
}

// ListByResource pages the resource's invitations on the derived resource key.
func (s *invitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.Page[invitation.Invitation]{}, errNotImplemented
}

// ListBySubject pages the invitee's invitations on the derived subject key,
// isolated by kind.
func (s *invitationStore) ListBySubject(ctx context.Context, kind, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.Page[invitation.Invitation]{}, errNotImplemented
}

// UpdateStatus transitions the stored status and releases the pending claim in
// the same transaction when the row leaves pending.
func (s *invitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	return invitation.Invitation{}, errNotImplemented
}
