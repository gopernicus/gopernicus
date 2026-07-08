package authsvc

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// securityEventInput is the content a sensitive op hands recordSecurityEvent.
// The helper fills the ID, timestamp, and client attribution (IP/UA) from the
// service clock and the request's client-info carrier — the caller never touches
// those, so IP/UA have exactly one source (design §5.1 WI4).
//
// Content hygiene (design §5.1 WI3): Details carries identifiers and key
// PREFIXES only. Never put a raw API key, JWT, session token, password, or OAuth
// token in Details, Actor, UserID, IP, or UA.
type securityEventInput struct {
	UserID  string
	Actor   securityevent.Principal
	Type    string
	Status  string
	Details map[string]any
}

// recordSecurityEvent appends one audit row synchronously (design §5.1). It is
// the SINGLE recording site: every sensitive op routes through it, so the
// never-fail property and the client-info sourcing live in one place.
//
//   - Nil repository → no-op (ratified AV9: the host keeps no audit trail).
//   - A write failure is logged at WARN with COARSE fields only — event_type,
//     status, error kind — never the event body, and NEVER fails the auth flow
//     (the design's non-negotiable).
func (s *Service) recordSecurityEvent(ctx context.Context, in securityEventInput) {
	if s.securityEvents == nil {
		return
	}
	info := clientInfoFromContext(ctx)
	evt := securityevent.New(in.Type, in.Status, s.now())
	evt.UserID = in.UserID
	evt.Actor = in.Actor
	evt.Details = in.Details
	evt.IPAddress = info.ip
	evt.UserAgent = info.ua
	if _, err := s.securityEvents.Create(ctx, evt); err != nil {
		s.logger.Warn("security event write failed",
			"event_type", in.Type,
			"status", in.Status,
			"error_kind", errKind(err),
		)
	}
}

// errKind reduces err to a coarse, secret-free sentinel label for the WARN line
// (design §5.1 WI3 — the log carries event_type, status, and error kind only,
// never the event body). Unrecognized errors report "unknown" rather than their
// message, so no store detail leaks into logs.
func errKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, errs.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, errs.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, errs.ErrInvalidReference):
		return "invalid_reference"
	case errors.Is(err, errs.ErrNotFound):
		return "not_found"
	case errors.Is(err, errs.ErrConflict):
		return "conflict"
	default:
		return "unknown"
	}
}
