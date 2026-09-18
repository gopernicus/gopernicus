package authentication

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// ListUserSessions is an owner-constrained inventory. The trusted caller must
// authorize userID; bundled HTTP derives it from a live first-party session.
func (s *Service) ListUserSessions(ctx context.Context, userID string, req list.Request) (list.Page[session.Session], error) {
	if s.sessionManagement == nil || userID == "" {
		return list.Page[session.Session]{}, sdk.ErrForbidden
	}
	return s.sessionManagement.ListByUser(ctx, userID, req)
}

func (s *Service) RevokeUserSession(ctx context.Context, userID, sessionID string) error {
	if s.sessionManagement == nil || userID == "" || sessionID == "" {
		return sdk.ErrForbidden
	}
	if err := s.sessionManagement.DeleteForUser(ctx, sessionID, userID); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{Type: "session_revoked", Status: securityevent.StatusSuccess, UserID: userID, Details: map[string]any{"session_id": sessionID}})
	return nil
}

// RevokeAllUserSessions fences pending approvals as well as deleting sessions.
func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if s.sessionManagement == nil || userID == "" {
		return sdk.ErrForbidden
	}
	if err := s.sessionManagement.RevokeAllForUser(ctx, userID, time.Now().UTC()); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{Type: "all_sessions_revoked", Status: securityevent.StatusSuccess, UserID: userID})
	return nil
}

// VerifyOAuth2AccessToken verifies cryptographic proof only. OAuth2 operations
// separately read the live session and account before using these bindings.
func (s *Service) VerifyOAuth2AccessToken(ctx context.Context, raw string) (oauth2.AccessToken, error) {
	if err := ctx.Err(); err != nil {
		return oauth2.AccessToken{}, err
	}
	principal, cred, ok := s.verifyAccessTokenCredential(raw, TransportHeader)
	if !ok || cred.Profile != session.ProfileDelegated || len(cred.Audiences) != 1 || !time.Now().Before(cred.accessExpiresAt) {
		return oauth2.AccessToken{}, sdk.ErrUnauthorized
	}
	return oauth2.AccessToken{UserID: principal.ID, SessionID: cred.SessionID,
		Issuer: cred.Issuer, Audience: cred.Audiences[0], ClientID: cred.ClientID,
		OriginClientID: cred.OriginClientID, ActorID: cred.ActorID, ExpiresAt: cred.accessExpiresAt}, nil
}
