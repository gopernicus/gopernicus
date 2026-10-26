package authentication

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// EraseTimeout bounds the erase transaction. EraseUser runs the store call
// detached from the caller's cancellation — a client that disconnects cannot
// roll back an erasure a moment from commit and make every retry repeat it —
// so this is what stops a hung store from holding the request.
const EraseTimeout = 30 * time.Second

// ErrUserAdminUnavailable is returned by the user-administration service methods
// when the administration repository is not wired. It wraps sdk.ErrNotFound so a
// transport maps it to 404 — an unwired capability is absent, not forbidden.
// Checked with errors.Is.
var ErrUserAdminUnavailable = fmt.Errorf("auth: user administration repository is not wired: %w", sdk.ErrNotFound)

// ErrUserErasureUnavailable is returned by EraseUser when the erasure
// repository is not wired. It wraps sdk.ErrNotFound: an unwired capability is
// absent, not forbidden.
var ErrUserErasureUnavailable = fmt.Errorf("auth: user erasure repository is not wired: %w", sdk.ErrNotFound)

// ErrEraseSelf is returned by EraseUser when the actor names their own account.
// It wraps sdk.ErrInvalidInput (400).
var ErrEraseSelf = fmt.Errorf("auth: an administrator cannot erase their own account: %w", sdk.ErrInvalidInput)

// ErrSecurityEventsUnavailable is returned by ListSecurityEvents when the
// security-event repository is not wired. It wraps sdk.ErrNotFound: an unwired
// capability is absent, not forbidden.
var ErrSecurityEventsUnavailable = fmt.Errorf("auth: security-event repository is not wired: %w", sdk.ErrNotFound)

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
	page, err := s.userAdmin.List(ctx, req)
	if err != nil {
		return page, err
	}
	s.recordDirectoryRead(ctx, map[string]any{"page": true})
	return page, nil
}

// GetUserSummary returns one user's directory projection; unknown →
// sdk.ErrNotFound.
//
// TRUSTED: it applies NO authorization — see ListUsers.
func (s *Service) GetUserSummary(ctx context.Context, id string) (user.Summary, error) {
	if s.userAdmin == nil {
		return user.Summary{}, ErrUserAdminUnavailable
	}
	summary, err := s.userAdmin.GetSummary(ctx, id)
	if err != nil {
		return summary, err
	}
	s.recordDirectoryRead(ctx, map[string]any{"page": false, "target_user_id": id})
	return summary, nil
}

// recordDirectoryRead records one successful operator-directory read, attributed
// to the principal the caller's transport resolved onto ctx.
func (s *Service) recordDirectoryRead(ctx context.Context, details map[string]any) {
	var actor securityevent.Principal
	if p, ok := sdk.PrincipalFromContext(ctx); ok {
		actor = securityevent.Principal{Type: p.Type, ID: p.ID}
	}
	s.recordSecurityEvent(ctx, securityEventInput{Type: securityevent.TypeUserDirectoryRead, Status: securityevent.StatusSuccess, Actor: actor, Details: details})
}

// SecurityEventsEnabled reports whether the security-event repository is wired.
// The transport mounts the bundled security-event listing only when this is
// true AND the host opted in (AdministrationConfig.SecurityEventsRoute).
func (s *Service) SecurityEventsEnabled() bool { return s.securityEvents.Repository() != nil }

// ListSecurityEvents returns a page of the security-event rail matching filter,
// newest first, and records one audit_read row for the read, attributed to the
// principal the caller's transport resolved onto ctx. The row names the filter
// keys present, never their values.
//
// TRUSTED: it applies NO authorization — see ListUsers.
func (s *Service) ListSecurityEvents(ctx context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
	repo := s.securityEvents.Repository()
	if repo == nil {
		return list.Page[securityevent.SecurityEvent]{}, ErrSecurityEventsUnavailable
	}
	page, err := repo.List(ctx, filter, req)
	if err != nil {
		return page, err
	}
	var actor securityevent.Principal
	if p, ok := sdk.PrincipalFromContext(ctx); ok {
		actor = securityevent.Principal{Type: p.Type, ID: p.ID}
	}
	s.recordSecurityEvent(ctx, securityEventInput{Type: securityevent.TypeAuditRead, Status: securityevent.StatusSuccess, Actor: actor, Details: map[string]any{
		"rail":     securityevent.AuditReadRailSecurityEvents,
		"filters":  securityEventFilterKeys(filter),
		"limit":    req.Limit,
		"returned": len(page.Items),
		"strategy": string(req.ResolvedStrategy()),
	}})
	return page, nil
}

// securityEventFilterKeys names the filter fields set, in a fixed order.
func securityEventFilterKeys(f securityevent.ListFilter) []string {
	keys := []string{}
	for _, field := range []struct {
		key string
		set bool
	}{
		{"user_id", f.UserID != ""}, {"event_type", f.EventType != ""}, {"event_status", f.EventStatus != ""},
		{"since", !f.Since.IsZero()}, {"until", !f.Until.IsZero()},
	} {
		if field.set {
			keys = append(keys, field.key)
		}
	}
	return keys
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
	if status == user.StatusErased {
		return user.Summary{}, user.StatusChange{}, fmt.Errorf("auth: erasure is EraseUser, not a status transition: %w", user.ErrErased)
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

// UserErasureEnabled reports whether the erasure repository is wired. The
// transport mounts the bundled erase route only when this is true AND the host
// opted in (AdministrationConfig.UserErasureRoute).
func (s *Service) UserErasureEnabled() bool { return s.userErasure != nil }

// EraseUser erases the subject id and returns its summary afterwards with the
// erasure outcome.
//
// The erasure is ONE store transaction (user.EraseRepository.Erase): the user is
// marked erased and deactivated, its identifiers retired and blanked, its
// credentials, links, mirrors, grants, sessions, codes and challenges deleted,
// the invitations addressed to it deleted or blanked and the ones it issued
// cancelled, its act-as-user service accounts and their keys deleted — and the
// user_erased row, prepared here with the actor's attribution and client info,
// is appended inside that transaction with the counts. Earlier security events
// keep the subject's pseudonymous id; they age out under retention.
//
// A repeat is a no-op: Changed is false and nothing is recorded. An accepting
// invitation claim held for the user is user.ErrEraseBlockedByClaim (409): the
// operator settles the claim first. Erasing one's own account is ErrEraseSelf.
// The store call runs detached from ctx's cancellation, bounded by
// EraseTimeout.
//
// Erasure is not a ban: the external-subject mirror goes with it, so the same
// provider subject signing in again provisions a fresh user. Deactivate to bar
// a subject.
//
// TRUSTED: it applies NO authorization — see ListUsers.
func (s *Service) EraseUser(ctx context.Context, actor Principal, id string) (user.Summary, user.Erasure, error) {
	if s.userErasure == nil {
		return user.Summary{}, user.Erasure{}, ErrUserErasureUnavailable
	}
	if s.userAdmin == nil {
		return user.Summary{}, user.Erasure{}, ErrUserAdminUnavailable
	}
	if strings.TrimSpace(id) == "" {
		return user.Summary{}, user.Erasure{}, fmt.Errorf("auth: erase: empty user id: %w", sdk.ErrInvalidInput)
	}
	if actor.ID == id {
		return user.Summary{}, user.Erasure{}, ErrEraseSelf
	}
	keys, err := s.magicLinkSubjectKeys(ctx, id)
	if err != nil {
		return user.Summary{}, user.Erasure{}, err
	}
	evt, _ := s.securityEvents.Prepare(ctx, securityevent.Input{
		UserID: id,
		Actor:  securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:   securityevent.TypeUserErased,
		Status: securityevent.StatusSuccess,
	})
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), EraseTimeout)
	defer cancel()
	erasure, err := s.userErasure.Erase(storeCtx, user.EraseInput{ID: id, Now: s.now().UTC(), SubjectKeys: keys, Event: evt, TOTP: s.TOTPEnabled(), Passkeys: s.PasskeysEnabled()})
	if err != nil {
		return user.Summary{}, user.Erasure{}, err
	}
	if s.TOTPEnabled() {
		if _, err := s.totp.Get(storeCtx, id); err == nil {
			s.logger.Error("auth: erasure left a TOTP credential; the store's EraseRepository must honour EraseInput.TOTP", "user_id", id)
		}
	}
	summary, err := s.userAdmin.GetSummary(storeCtx, id)
	if err != nil {
		return user.Summary{}, erasure, err
	}
	return summary, erasure, nil
}

// magicLinkSubjectKeys returns the challenge subject keys of id's active
// identifiers — the keys an address-keyed magic link carries, computed by the
// same function that writes them.
func (s *Service) magicLinkSubjectKeys(ctx context.Context, id string) ([]string, error) {
	if s.identifiers == nil {
		return nil, nil
	}
	idents, err := s.identifiers.ListByUser(ctx, id)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(idents))
	for _, ident := range idents {
		key := s.magicLinkSubjectKey(string(ident.Kind), ident.NormalizedValue)
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}
