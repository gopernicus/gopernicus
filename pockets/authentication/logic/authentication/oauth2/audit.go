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

// Audit input is limited to identifiers, each passed through
// securityevent.BoundValue. Request bodies, redirect query strings, state, token
// material and storage errors never enter it.
func (s *Service) audit(ctx context.Context, kind, userID, sessionID, clientID string) {
	s.auditReason(ctx, kind, userID, sessionID, clientID, "")
}

// auditGrantFailure records a token-endpoint request refused for its grant,
// its client or a parameter value as oauth2_grant_failed with the bounded
// reason; the reasons mirror the wire error codes. Every such row is Budgeted:
// the caller may hold no credential at all. A refresh reuse records
// oauth2_refresh_reuse instead, and an infrastructure fault records nothing.
//
// A row is written when the server evaluated a credential, grant, token or
// parameter value and refused it. A request refused for its shape (auth-header
// form, credentials in the body, encoding, a parameter the grant does not
// accept) writes none; it appears only in access logs and rate-limit counters.
func (s *Service) auditGrantFailure(ctx context.Context, clientID string, err error) {
	var reason string
	switch {
	case errors.Is(err, ErrInvalidClient):
		reason = securityevent.ReasonInvalidClient
	case errors.Is(err, ErrInvalidGrant):
		reason = securityevent.ReasonInvalidGrant
	case errors.Is(err, ErrInvalidRequest):
		reason = securityevent.ReasonInvalidRequest
	case errors.Is(err, ErrInvalidScope):
		reason = securityevent.ReasonInvalidScope
	case errors.Is(err, ErrInvalidTarget):
		reason = securityevent.ReasonInvalidTarget
	default:
		return
	}
	s.auditReason(ctx, TypeGrantFailed, "", "", clientID, reason)
}

// auditReason records kind. An empty clientID omits the client_id key: the row
// has no client to attribute. A presented id the bound refuses records blank.
func (s *Service) auditReason(ctx context.Context, kind, userID, sessionID, clientID, reason string) {
	bounded := func(value string) string { return securityevent.BoundValue(securityevent.FieldDetail, value) }
	presented := clientID != ""
	userID, sessionID, clientID = bounded(userID), bounded(sessionID), bounded(clientID)
	status := securityevent.StatusSuccess
	switch kind {
	case TypeRefreshReuse:
		status = securityevent.StatusBlocked
		s.logger.WarnContext(ctx, "OAuth refresh reuse revoked a connection", "client_id", clientID, "session_id", sessionID, "user_id", userID)
	case TypeGrantFailed:
		status = securityevent.StatusFailure
	}
	in := securityevent.Input{UserID: userID, Type: kind, Status: status, Details: map[string]any{}, Budgeted: kind == TypeGrantFailed}
	if presented {
		in.Details["client_id"] = clientID
	}
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
