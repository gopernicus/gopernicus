package invitations

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

var (
	ErrInvitationNotFound      = fmt.Errorf("invitation not found: %w", errs.ErrNotFound)
	ErrInvitationExpired       = fmt.Errorf("invitation expired: %w", errs.ErrConflict)
	ErrInvitationAlreadyUsed   = fmt.Errorf("invitation already used: %w", errs.ErrConflict)
	ErrInvitationCancelled     = fmt.Errorf("invitation cancelled: %w", errs.ErrConflict)
	ErrInvitationInvalidStatus = fmt.Errorf("invitation invalid status: %w", errs.ErrConflict)
	ErrIdentifierMismatch      = fmt.Errorf("identifier does not match invitation: %w", errs.ErrForbidden)
	ErrAlreadyMember           = fmt.Errorf("already a member: %w", errs.ErrAlreadyExists)
	ErrPendingInvitationExists = fmt.Errorf("pending invitation already exists: %w", errs.ErrAlreadyExists)
)
