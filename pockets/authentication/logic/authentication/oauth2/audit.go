package oauth2

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The OAuth2 event types live in securityevent's registry; these are aliases.
const (
	TypeConsentApproved     = securityevent.TypeOAuth2ConsentApproved
	TypeConsentDenied       = securityevent.TypeOAuth2ConsentDenied
	TypeConnectionCreated   = securityevent.TypeOAuth2ConnectionCreated
	TypeRefresh             = securityevent.TypeOAuth2Refresh
	TypeRefreshReuse        = securityevent.TypeOAuth2RefreshReuse
	TypeTokenExchanged      = securityevent.TypeOAuth2TokenExchanged
	TypeRevocationRequested = securityevent.TypeOAuth2RevocationRequested
	TypeGrantFailed         = securityevent.TypeOAuth2GrantFailed
)

// Audit input is deliberately limited to validated identifiers. Request bodies,
// redirect query strings, state, token material and storage errors never enter it.
func (s *Service) audit(ctx context.Context, kind, userID, sessionID, clientID string) {
	s.auditReason(ctx, kind, userID, sessionID, clientID, "")
}

// auditGrantFailure records a token-endpoint request refused for its grant or
// its client as oauth2_grant_failed with the bounded reason. Any other error —
// a malformed request, an unsupported target, an infrastructure fault, a
// refresh reuse (recorded as oauth2_refresh_reuse) — records nothing.
func (s *Service) auditGrantFailure(ctx context.Context, clientID string, err error) {
	switch {
	case errors.Is(err, ErrInvalidClient):
		s.auditReason(ctx, TypeGrantFailed, "", "", clientID, securityevent.ReasonInvalidClient)
	case errors.Is(err, ErrInvalidGrant):
		s.auditReason(ctx, TypeGrantFailed, "", "", clientID, securityevent.ReasonInvalidGrant)
	}
}

func (s *Service) auditReason(ctx context.Context, kind, userID, sessionID, clientID, reason string) {
	bounded := func(value string) string {
		if len(value) > 2048 {
			return value[:2048]
		}
		return value
	}
	userID, sessionID, clientID = bounded(userID), bounded(sessionID), bounded(clientID)
	status := securityevent.StatusSuccess
	switch kind {
	case TypeRefreshReuse:
		status = securityevent.StatusBlocked
		s.logger.WarnContext(ctx, "OAuth refresh reuse revoked a connection", "client_id", clientID, "session_id", sessionID, "user_id", userID)
	case TypeGrantFailed:
		status = securityevent.StatusFailure
	}
	in := securityevent.Input{UserID: userID, Type: kind, Status: status, Details: map[string]any{"client_id": clientID}}
	if sessionID != "" {
		in.Details["session_id"] = sessionID
	}
	if reason != "" {
		in.Details["reason"] = reason
	}
	if p, ok := sdk.PrincipalFromContext(ctx); ok {
		in.Actor = securityevent.Principal{Type: p.Type, ID: p.ID}
	}
	s.events.Record(ctx, in)
}
