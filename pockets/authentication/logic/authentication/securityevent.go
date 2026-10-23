package authentication

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
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

// recordDenial appends a refused-operation row of eventType: status is
// StatusFailure or StatusBlocked, and reason is one of the bounded
// securityevent Reason* values, carried under the Details key "reason". userID
// is the resolved account, empty when the attempt resolved none.
func (s *Service) recordDenial(ctx context.Context, eventType, userID, status, reason string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    eventType,
		Status:  status,
		Details: map[string]any{"reason": reason},
	})
}

// challengeDenial classifies a code-challenge refusal: a spent attempt budget is
// a blocked lockout (consumeChallenge records challenge_lockout beside it), a
// wrong or expired code a failure. ok is false for any other error.
func challengeDenial(err error) (status, reason string, ok bool) {
	switch {
	case errors.Is(err, ErrTooManyAttempts):
		return securityevent.StatusBlocked, securityevent.ReasonLockout, true
	case errors.Is(err, ErrChallengeInvalid), errors.Is(err, ErrChallengeExpired):
		return securityevent.StatusFailure, securityevent.ReasonInvalidCode, true
	}
	return "", "", false
}

// recordChallengeDenial records a code-challenge refusal of eventType for
// userID; an error challengeDenial does not classify records nothing.
func (s *Service) recordChallengeDenial(ctx context.Context, eventType, userID string, err error) {
	if status, reason, ok := challengeDenial(err); ok {
		s.recordDenial(ctx, eventType, userID, status, reason)
	}
}

// recordCredentialMutationDenial records a refused credential mutation of
// eventType for userID: a credential-policy refusal or a verification-required
// use change is policy, a lost revision race revision_conflict. Any other error
// records nothing.
func (s *Service) recordCredentialMutationDenial(ctx context.Context, eventType, userID string, err error) {
	var refused credentialPolicyRefusal
	switch {
	case errors.As(err, &refused), errors.Is(err, identifier.ErrVerificationRequired):
		s.recordDenial(ctx, eventType, userID, securityevent.StatusFailure, securityevent.ReasonPolicy)
	case errors.Is(err, sdk.ErrConflict):
		s.recordDenial(ctx, eventType, userID, securityevent.StatusFailure, securityevent.ReasonRevisionConflict)
	}
}

// ErrorKind reduces err to a coarse, secret-free sentinel label for a WARN line.
// It forwards to securityevent.ErrorKind.
func ErrorKind(err error) string {
	return securityevent.ErrorKind(err)
}
