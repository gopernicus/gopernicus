package oauth2

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The OAuth2 event types live in securityevent's registry; these are aliases.
const (
	TypeConsentApproved     = securityevent.TypeOAuth2ConsentApproved
	TypeConnectionCreated   = securityevent.TypeOAuth2ConnectionCreated
	TypeRefresh             = securityevent.TypeOAuth2Refresh
	TypeRefreshReuse        = securityevent.TypeOAuth2RefreshReuse
	TypeTokenExchanged      = securityevent.TypeOAuth2TokenExchanged
	TypeRevocationRequested = securityevent.TypeOAuth2RevocationRequested
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
	in := securityevent.Input{UserID: userID, Type: kind, Status: status, Details: map[string]any{"client_id": clientID}}
	if sessionID != "" {
		in.Details["session_id"] = sessionID
	}
	if p, ok := sdk.PrincipalFromContext(ctx); ok {
		in.Actor = securityevent.Principal{Type: p.Type, ID: p.ID}
	}
	s.events.Record(ctx, in)
}
