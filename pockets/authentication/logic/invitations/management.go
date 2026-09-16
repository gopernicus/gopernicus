package invitations

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// PreparedManagement pins one invitation read for cancellation or resend. It is
// not permission to act: the caller admits access before executing the command.
// Only the originating Service can execute it, against the observed token hash.
type PreparedManagement struct {
	owner      *Service
	invitation Invitation
}

func (p PreparedManagement) ID() string        { return p.invitation.ID }
func (p PreparedManagement) InvitedBy() string { return p.invitation.InvitedBy }

// PrepareManagement reads one target without changing state or queuing delivery.
// Inspection exposes only the target and issuer, never its token or metadata.
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
