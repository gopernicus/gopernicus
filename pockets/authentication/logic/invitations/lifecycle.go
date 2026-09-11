package invitations

import (
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// Validate rejects incomplete acceptance bindings before a store changes state.
func (a Acceptance) Validate() error {
	if strings.TrimSpace(a.TokenHash) == "" || strings.TrimSpace(a.SubjectType) == "" || strings.TrimSpace(a.SubjectID) == "" || a.Now.IsZero() {
		return sdk.ErrInvalidInput
	}
	return nil
}

// Matches reports whether a durable claim belongs to this exact token/subject.
func (a Acceptance) Matches(i Invitation) bool {
	return i.TokenHash == a.TokenHash && i.ResolvedSubjectType == a.SubjectType && i.ResolvedSubjectID == a.SubjectID
}

// ClaimAcceptance returns the claimed state. Stores apply it atomically with
// the pending/token/expiry preconditions, never as detached read then write.
func (i Invitation) ClaimAcceptance(a Acceptance) (Invitation, error) {
	if err := a.Validate(); err != nil {
		return Invitation{}, err
	}
	if (i.Status == StatusAccepting || i.Status == StatusAccepted) && a.Matches(i) {
		return i, nil
	}
	if i.Status != StatusPending || i.TokenHash != a.TokenHash {
		return Invitation{}, sdk.ErrConflict
	}
	if i.Expired(a.Now) {
		return Invitation{}, sdk.ErrExpired
	}
	i.Status = StatusAccepting
	i.ResolvedSubjectType = a.SubjectType
	i.ResolvedSubjectID = a.SubjectID
	i.UpdatedAt = a.Now.UTC()
	return i, nil
}

// CompleteAcceptance returns the final state of a matching claim.
func (i Invitation) CompleteAcceptance(a Acceptance) (Invitation, error) {
	if err := a.Validate(); err != nil {
		return Invitation{}, err
	}
	if !a.Matches(i) || (i.Status != StatusAccepting && i.Status != StatusAccepted) {
		return Invitation{}, sdk.ErrConflict
	}
	if i.Status == StatusAccepted {
		return i, nil
	}
	i.Status = StatusAccepted
	i.AcceptedAt = a.Now.UTC()
	i.UpdatedAt = a.Now.UTC()
	return i, nil
}

// Validate limits status updates to unclaimed lifecycle operations.
func (u StatusUpdate) Validate() error {
	if u.ExpectedTokenHash == "" || u.TokenHash == "" || u.UpdatedAt.IsZero() || u.ExpiresAt.IsZero() {
		return sdk.ErrInvalidInput
	}
	switch u.Status {
	case StatusPending, StatusDeclined, StatusCancelled:
		return nil
	default:
		return sdk.ErrInvalidInput
	}
}

// UpdateStatus returns an unclaimed transition. Stores must compare the current
// status and token in the same atomic operation that persists the result.
func (i Invitation) UpdateStatus(u StatusUpdate) (Invitation, error) {
	if err := u.Validate(); err != nil {
		return Invitation{}, err
	}
	if (i.Status != StatusPending && i.Status != StatusExpired) || i.TokenHash != u.ExpectedTokenHash {
		return Invitation{}, sdk.ErrConflict
	}
	i.Status = u.Status
	i.TokenHash = u.TokenHash
	i.ExpiresAt = u.ExpiresAt.UTC()
	i.ResolvedSubjectID = u.ResolvedSubjectID
	i.UpdatedAt = u.UpdatedAt.UTC()
	return i, nil
}

// Active reports whether the invitation still reserves its resource/subject tuple.
func (i Invitation) Active() bool {
	return i.Status == StatusPending || i.Status == StatusAccepting
}

// Clone detaches invitation metadata when values cross an ownership boundary.
func (i Invitation) Clone() Invitation { i.Metadata = CloneMetadata(i.Metadata); return i }
