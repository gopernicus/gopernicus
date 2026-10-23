package authentication

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
)

// securityEventInput is the content a sensitive op hands recordSecurityEvent:
// securityevent.Input, whose recorder fills the ID, timestamp, correlation ids
// and client attribution (IP/UA) — the caller never touches those, so each has
// exactly one source (design §5.1 WI4).
//
// Content hygiene (design §5.1 WI3): Details carries identifiers and key
// PREFIXES only. Never put a raw API key, JWT, session token, password, or OAuth
// token in Details, Actor, UserID, IP, or UA.
type securityEventInput = securityevent.Input

// recordSecurityEvent appends one audit row synchronously through the service's
// recorder (design §5.1), the one recording site every sensitive op routes
// through. A recorder without a repository is a no-op (ratified AV9: the host
// keeps no audit trail); a write failure is logged, counted and observed by the
// recorder and NEVER fails the auth flow (the design's non-negotiable).
func (s *Service) recordSecurityEvent(ctx context.Context, in securityEventInput) {
	s.securityEvents.Record(ctx, in)
}

// ErrorKind reduces err to a coarse, secret-free sentinel label for a WARN line.
// It forwards to securityevent.ErrorKind.
func ErrorKind(err error) string {
	return securityevent.ErrorKind(err)
}
