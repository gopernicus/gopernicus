package invitations

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// PreparedManagement pins one invitation read for cancellation or resend. It is
// not permission to act: the caller admits access before executing the command.
// Only the originating Service can execute it, against the observed token hash.
type PreparedManagement struct {
	owner      *Service
	invitation Invitation
	actor      sdk.Principal
}

func (p PreparedManagement) ID() string           { return p.invitation.ID }
func (p PreparedManagement) InvitedBy() string    { return p.invitation.InvitedBy }
func (p PreparedManagement) ResourceType() string { return p.invitation.ResourceType }
func (p PreparedManagement) ResourceID() string   { return p.invitation.ResourceID }

// WithActor returns a copy attributed to the principal executing the command.
// The actor is audit attribution only; it never authorizes the command. A zero
// principal keeps the headless InvitedBy attribution.
func (p PreparedManagement) WithActor(actor sdk.Principal) PreparedManagement {
	p.actor = actor
	return p
}

// PrepareManagement reads one target without changing state or queuing delivery.
// Inspection exposes only the target resource and issuer, never its token or
// metadata.
func (s *Service) PrepareManagement(ctx context.Context, id string) (PreparedManagement, error) {
	if err := ctx.Err(); err != nil {
		return PreparedManagement{}, err
	}
	if id == "" {
		return PreparedManagement{}, fmt.Errorf("invitation id is required: %w", sdk.ErrInvalidInput)
	}
	inv, err := s.invitations.Get(ctx, id)
	if err != nil {
		return PreparedManagement{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreparedManagement{}, err
	}
	if inv.ID != id {
		return PreparedManagement{}, fmt.Errorf("invitation target mismatch: %w", sdk.ErrInvalidReference)
	}
	inv.Metadata = CloneMetadata(inv.Metadata)
	return PreparedManagement{owner: s, invitation: inv}, nil
}

func (p PreparedManagement) checked(ctx context.Context, s *Service) (Invitation, error) {
	if p.owner == nil || p.owner != s {
		return Invitation{}, fmt.Errorf("invitation command belongs to another service: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Invitation{}, err
	}
	return p.invitation, nil
}

// attribution resolves the audit actor and user for a management command. A user
// actor is the attributed user; a non-user actor never lands in UserID; no actor
// keeps the headless InvitedBy fallback.
func (p PreparedManagement) attribution() (securityevent.Principal, string) {
	if p.actor == (sdk.Principal{}) {
		return securityevent.Principal{}, p.invitation.InvitedBy
	}
	actor := securityevent.Principal{Type: p.actor.Type, ID: p.actor.ID}
	if p.actor.Type == sdk.PrincipalTypeUser {
		return actor, p.actor.ID
	}
	return actor, ""
}
