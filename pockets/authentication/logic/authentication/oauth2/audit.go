package oauth2

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

const (
	TypeConsentApproved     = "oauth2_consent_approved"
	TypeConnectionCreated   = "oauth2_connection_created"
	TypeRefresh             = "oauth2_refresh"
	TypeRefreshReuse        = "oauth2_refresh_reuse"
	TypeTokenExchanged      = "oauth2_token_exchanged"
	TypeRevocationRequested = "oauth2_revocation_requested"
)

// Audit input is deliberately limited to validated identifiers. Request bodies,
// redirect query strings, state, token material and storage errors never enter it.
func (s *Service) audit(ctx context.Context, kind, userID, sessionID, clientID string) {
	bounded := func(value string) string {
		if len(value) > 2048 {
			return value[:2048]
		}
		return value
	}
	userID, sessionID, clientID = bounded(userID), bounded(sessionID), bounded(clientID)
	status := securityevent.StatusSuccess
	if kind == TypeRefreshReuse {
		status = securityevent.StatusBlocked
		s.logger.WarnContext(ctx, "OAuth refresh reuse revoked a connection", "client_id", clientID, "session_id", sessionID, "user_id", userID)
	}
	if s.repos.SecurityEvents == nil {
		return
	}
	event := securityevent.New(sdk.IDGenerator{}, kind, status, s.now())
	event.UserID = userID
	event.Details = map[string]any{"client_id": clientID}
	if sessionID != "" {
		event.Details["session_id"] = sessionID
	}
	if _, err := s.repos.SecurityEvents.Create(ctx, event); err != nil {
		s.logger.WarnContext(ctx, "OAuth security event could not be recorded", "event_type", kind)
	}
}
