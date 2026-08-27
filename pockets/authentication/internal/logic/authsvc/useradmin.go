package authsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// UserAdminAction is the user-administration operation a host UserAdminCheck
// policy is asked about (coordination-hub-auth-upstream CHAU-1.1). The set is
// closed: a new administrative capability adds a value here, so a host policy's
// default arm always sees an action it can recognize or refuse.
type UserAdminAction string

const (
	// UserAdminList is the paginated directory read. It carries no target user.
	UserAdminList UserAdminAction = "list"
	// UserAdminRead is a single-user directory read; the target is the user read.
	UserAdminRead UserAdminAction = "read"
	// UserAdminDeactivate denies the target subject every new session.
	UserAdminDeactivate UserAdminAction = "deactivate"
	// UserAdminReactivate returns the target subject to the active posture.
	UserAdminReactivate UserAdminAction = "reactivate"
	// UserAdminResendVerification re-issues the target's registration
	// verification challenge (the authorized counterpart to the enumeration-safe
	// public resend).
	UserAdminResendVerification UserAdminAction = "resend-verification"
)

// UserAdminCheckRequest is the parsed, principal-resolved authorization question
// the feature poses to a host UserAdminCheck. TargetUserID is empty for
// UserAdminList and set for every other action.
//
// The Principal reaches the policy VERBATIM — including a machine principal from
// an API key. The feature does not pre-decide whether a service account may
// administer users; that is exactly the decision the host owns.
type UserAdminCheckRequest struct {
	Principal    identity.Principal
	Action       UserAdminAction
	TargetUserID string
}

// UserAdminCheck is the host authorization seam for user administration. It is
// the InviteCheck precedent applied to the user directory: the feature owns
// session validation, principal resolution, and request parsing, then asks the
// host one question it can answer with its own roles, tenancy, or policy engine.
//
// Authentication NEVER invents a role named "admin" and never interprets a role
// string. It does not import features/authorization. A host that has an
// authorization feature wires a closure over it; a host with a hard-coded
// operator list wires that instead.
//
// A nil return authorizes. A denial (wrap sdk.ErrForbidden) or an infrastructure
// error BOTH fail closed — the feature never distinguishes "policy said no" from
// "policy could not answer" by proceeding.
//
// Wiring a nil UserAdminCheck leaves the bundled admin routes UNMOUNTED even when
// the repositories are present, so a store adapter may return a complete bundle
// without an authorization surface appearing anywhere.
type UserAdminCheck func(ctx context.Context, req UserAdminCheckRequest) error

// ErrUserAdminUnavailable is returned by the user-administration service methods
// when the administration repository is not wired. It wraps sdk.ErrNotFound so a
// transport maps it to 404 — an unwired capability is absent, not forbidden.
// Checked with errors.Is.
var ErrUserAdminUnavailable = fmt.Errorf("auth: user administration repository is not wired: %w", sdk.ErrNotFound)

// UserAdminEnabled reports whether the administration repository is wired. The
// transport registers the bundled admin routes only when this is true AND a host
// UserAdminCheck is configured (deny-by-absence, the Providers precedent).
func (s *Service) UserAdminEnabled() bool { return s.userAdmin != nil }

// UserAdminAuthorized reports whether a host authorization policy is wired.
func (s *Service) UserAdminAuthorized() bool { return s.userAdminCheck != nil }

// AuthorizeUserAdmin runs the host UserAdminCheck for one action/target and
// returns its verdict. It FAILS CLOSED on a nil check: a caller that reached this
// method without a configured policy gets sdk.ErrForbidden rather than an
// allow-by-default. The bundled handlers call it after live-session validation
// and principal resolution, before any target resolution or mutation.
func (s *Service) AuthorizeUserAdmin(ctx context.Context, principal Principal, action UserAdminAction, targetUserID string) error {
	if s.userAdminCheck == nil {
		return fmt.Errorf("auth: user administration is not authorized by this host: %w", sdk.ErrForbidden)
	}
	return s.userAdminCheck(ctx, UserAdminCheckRequest{
		Principal:    principal,
		Action:       action,
		TargetUserID: targetUserID,
	})
}

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
// AuthorizeUserAdmin first; a host calling this directly from its own transport
// or console owns that decision itself.
func (s *Service) ListUsers(ctx context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	if s.userAdmin == nil {
		return crud.Page[user.Summary]{}, ErrUserAdminUnavailable
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
