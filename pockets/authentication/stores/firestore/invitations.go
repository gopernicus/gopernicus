package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var _ invitations.InvitationRepository = (*invitationStore)(nil)

// invitationStore fills invitations.InvitationRepository over the invitations
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
func (s *invitationStore) Create(ctx context.Context, inv invitations.Invitation) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}

// Get returns the invitation by id, or sdk.ErrNotFound.
func (s *invitationStore) Get(ctx context.Context, id string) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}

// GetByTokenHash resolves the mailed secret's hash through its claim document.
func (s *invitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}

// ListByResource pages the resource's invitations on the derived resource key.
func (s *invitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitations.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[invitations.Invitation]{}, err
	}
	return list.Page[invitations.Invitation]{}, errNotImplemented
}

// ListBySubject pages the invitee's invitations on the derived subject key,
// isolated by kind.
func (s *invitationStore) ListBySubject(ctx context.Context, kind, identifier string, req list.Request) (list.Page[invitations.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[invitations.Invitation]{}, err
	}
	return list.Page[invitations.Invitation]{}, errNotImplemented
}

// UpdateStatus will conditionally transition an unclaimed current token.
// ClaimAcceptance and CompleteAcceptance remain part of the separate N4b
// invitation implementation milestone; all three currently fail explicitly.
func (s *invitationStore) UpdateStatus(ctx context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}

func (s *invitationStore) ClaimAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}

func (s *invitationStore) CompleteAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation{}, errNotImplemented
}
