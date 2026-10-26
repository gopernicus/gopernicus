package authentication

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const (
	// globalRevocationBatchSize is the users one RevokeAllSessions batch — one
	// store transaction — bumps.
	globalRevocationBatchSize = 500
)

// ErrGlobalRevocationUnsupported is returned by RevokeAllSessions when
// Repositories.GlobalSessions is not wired. It wraps sdk.ErrNotFound: an
// unwired capability is absent.
var ErrGlobalRevocationUnsupported = fmt.Errorf("auth: global session revocation is not wired: %w", sdk.ErrNotFound)

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
	s.recordSecurityEvent(ctx, securityEventInput{Type: securityevent.TypeSessionRevoked, Status: securityevent.StatusSuccess, UserID: userID, Details: map[string]any{"session_id": sessionID}})
	return nil
}

// RevokeAllUserSessions fences pending approvals as well as deleting sessions.
func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if s.sessionManagement == nil || userID == "" {
		return sdk.ErrForbidden
	}
	if err := s.sessionManagement.RevokeAllForUser(ctx, userID, s.now().UTC()); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{Type: securityevent.TypeAllSessionsRevoked, Status: securityevent.StatusSuccess, UserID: userID})
	return nil
}

// GlobalRevocation reports one RevokeAllSessions run: the users whose revision
// was bumped, the sessions and step-up grants deleted (the final orphan sweep
// included), the batches committed and when the run started.
type GlobalRevocation struct {
	Users     int
	Sessions  int
	Grants    int
	Batches   int
	RevokedAt time.Time
}

// GlobalSessionRevocationEnabled reports whether Repositories.GlobalSessions is
// wired.
func (s *Service) GlobalSessionRevocationEnabled() bool { return s.globalSessions != nil }

// RevokeAllSessions ends every session in the fleet: every active user's
// auth_revision is bumped and every session, step-up grant and password_reset
// challenge they hold is deleted, in batches of 500 users (one store
// transaction each), then the sessions and grants of users who are not active
// are swept. Refresh tokens, Live() routes, step-up grants, pending magic links,
// OTPs and OAuth codes die at once; delegated OAuth2 connections are sessions,
// so connected apps are disconnected too. A first-party access JWT stays valid
// on stateless routes for at most SessionsConfig.AccessTokenTTL; API keys and
// external IdP sessions are untouched.
//
// It records all_sessions_revoked_global twice, attributed to actor: started
// before the first batch, then success with the counts — or failure with the
// partial counts and error_kind, returned beside the error. The run is not
// globally atomic: the batches before a failure stay committed, and a re-run
// is safe (it bumps again). An evidence row that cannot be written is logged
// at ERROR, never swallowed. Nil Repositories.GlobalSessions →
// ErrGlobalRevocationUnsupported.
//
// TRUSTED: it applies NO authorization — the caller owns that decision.
func (s *Service) RevokeAllSessions(ctx context.Context, actor Principal) (GlobalRevocation, error) {
	if s.globalSessions == nil {
		return GlobalRevocation{}, ErrGlobalRevocationUnsupported
	}
	started := s.now().UTC()
	by := securityevent.Principal{Type: actor.Type, ID: actor.ID}
	s.recordGlobalRevocation(ctx, by, securityevent.StatusStarted, map[string]any{"batch_size": globalRevocationBatchSize})
	out := GlobalRevocation{RevokedAt: started}
	err := s.revokeAllBatches(ctx, &out, started)
	details := map[string]any{
		"users":       out.Users,
		"sessions":    out.Sessions,
		"grants":      out.Grants,
		"batches":     out.Batches,
		"duration_ms": s.now().Sub(started).Milliseconds(),
	}
	if err != nil {
		details["error_kind"] = ErrorKind(err)
		s.recordGlobalRevocation(ctx, by, securityevent.StatusFailure, details)
		return out, err
	}
	s.recordGlobalRevocation(ctx, by, securityevent.StatusSuccess, details)
	return out, nil
}

// revokeAllBatches pages every active user through the store, then sweeps the
// leftovers of inactive users, accumulating into out.
func (s *Service) revokeAllBatches(ctx context.Context, out *GlobalRevocation, now time.Time) error {
	after := ""
	for {
		batch, err := s.globalSessions.RevokeAllBatch(ctx, after, globalRevocationBatchSize, now)
		if err != nil {
			return err
		}
		out.Users += batch.Users
		out.Sessions += batch.Sessions
		out.Grants += batch.Grants
		out.Batches++
		if batch.Done || batch.LastUserID == "" || batch.LastUserID == after {
			break
		}
		after = batch.LastUserID
	}
	sessions, grants, err := s.globalSessions.SweepOrphans(ctx, now)
	out.Sessions += sessions
	out.Grants += grants
	return err
}

// recordGlobalRevocation writes one all_sessions_revoked_global row through
// the recorder's failure accounting. The rows are the run's evidence, so a
// write that fails also logs at ERROR with its error_kind.
func (s *Service) recordGlobalRevocation(ctx context.Context, actor securityevent.Principal, status string, details map[string]any) {
	if err := s.securityEvents.RecordReporting(ctx, securityEventInput{Actor: actor, Type: securityevent.TypeAllSessionsRevokedGlobal, Status: status, Details: details}); err != nil {
		s.logger.Error("auth: global session revocation evidence not written", "event_status", status, "error_kind", ErrorKind(err))
	}
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
