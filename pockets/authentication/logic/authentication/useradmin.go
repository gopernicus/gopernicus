package authentication

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// ErrUserAdminUnavailable is returned by the user-administration service methods
// when the administration repository is not wired. It wraps sdk.ErrNotFound so a
// transport maps it to 404 — an unwired capability is absent, not forbidden.
// Checked with errors.Is.
var ErrUserAdminUnavailable = fmt.Errorf("auth: user administration repository is not wired: %w", sdk.ErrNotFound)

// UserAdminEnabled reports whether the administration repository is wired. The
// transport registers the bundled admin routes only when this is true AND a host
// inbound policy is configured (deny-by-absence).
func (s *Service) UserAdminEnabled() bool { return s.userAdmin != nil }

// userDeactivated reports whether userID is a KNOWN subject in the deactivated
// posture.
//
// An unknown user reports false, deliberately. The v1 lifecycle vocabulary has no
// "deleted" value (see user.Status), so a missing row is not a lifecycle state
// and treating it as one would add an owner-must-exist requirement this work
// never asked for — a behavior change unrelated to deactivation. Callers that
// need an existence check do it themselves. An infrastructure error propagates so
// the caller can fail closed on it.
//
// This is a plain read, NOT a fence, and it is used only where no session is
// being minted (the act-as-user API-key path), so there is nothing to serialize
// against. Every session mint goes through the atomic ActiveSessions capability
// instead: a read here followed by a write elsewhere would be exactly the race
// CHAU-1.1 forbids.
func (s *Service) userDeactivated(ctx context.Context, userID string) (bool, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return !u.Active(), nil
}

// ListUsers returns a page of the operator directory.
//
// TRUSTED: it applies NO authorization. The bundled HTTP handler calls
// its host policy first; a host calling this directly from its own transport
// or console owns that decision itself.
func (s *Service) ListUsers(ctx context.Context, req list.Request) (list.Page[user.Summary], error) {
	if s.userAdmin == nil {
		return list.Page[user.Summary]{}, ErrUserAdminUnavailable
	}
	return s.userAdmin.List(ctx, req)
}

// GetUserSummary returns one user's directory projection; unknown →
// sdk.ErrNotFound.
//
// TRUSTED: it applies NO authorization — see ListUsers.
func (s *Service) GetUserSummary(ctx context.Context, id string) (user.Summary, error) {
	if s.userAdmin == nil {
		return user.Summary{}, ErrUserAdminUnavailable
	}
	return s.userAdmin.GetSummary(ctx, id)
}

// SetUserStatus transitions a user's lifecycle status and returns the resulting
// summary alongside the change outcome.
//
// The transition itself — status write, auth_revision increment, session and
// authentication-grant revocation — is ONE store transaction (see
// user.AdminRepository.SetStatus). This method adds only the audit record and the
// post-transition read, neither of which can leave the transition half-applied.
//
// Replaying the current status is a no-op: Changed is false, nothing is revoked,
// and no audit event is recorded, so a retried admin request is safe.
//
// TRUSTED: it applies NO authorization — see ListUsers.
func (s *Service) SetUserStatus(ctx context.Context, actor Principal, id string, status user.Status) (user.Summary, user.StatusChange, error) {
	if s.userAdmin == nil {
		return user.Summary{}, user.StatusChange{}, ErrUserAdminUnavailable
	}
	if !status.Valid() {
		return user.Summary{}, user.StatusChange{}, fmt.Errorf("auth: %q: %w", status, user.ErrInvalidStatus)
	}

	change, err := s.userAdmin.SetStatus(ctx, id, status, s.now().UTC())
	if err != nil {
		return user.Summary{}, user.StatusChange{}, err
	}

	if change.Changed {
		s.recordUserStatusChange(ctx, actor, id, change)
	}

	summary, err := s.userAdmin.GetSummary(ctx, id)
	if err != nil {
		return user.Summary{}, change, err
	}
	return summary, change, nil
}

// recordUserStatusChange writes the best-effort audit row for an applied
// transition. Details carry the resulting status and the revoked-session count
// only — never an address, display name, or any credential material. Like every
// other audit site, a write failure is logged and never fails the operation
// (design §5.1).
// DeactivateUser atomically blocks future session minting and revokes existing
// sessions. The caller owns authorization, as with SetUserStatus.
func (s *Service) DeactivateUser(ctx context.Context, actor Principal, id string) (user.Summary, user.StatusChange, error) {
	return s.SetUserStatus(ctx, actor, id, user.StatusDeactivated)
}

// ReactivateUser restores the user's active status. The caller owns authorization.
func (s *Service) ReactivateUser(ctx context.Context, actor Principal, id string) (user.Summary, user.StatusChange, error) {
	return s.SetUserStatus(ctx, actor, id, user.StatusActive)
}

func (s *Service) recordUserStatusChange(ctx context.Context, actor Principal, userID string, change user.StatusChange) {
	eventType := securityevent.TypeUserDeactivated
	if change.Status == user.StatusActive {
		eventType = securityevent.TypeUserReactivated
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Actor:  securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:   eventType,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"status":           string(change.Status),
			"revoked_sessions": change.RevokedSessions,
		},
	})
}
