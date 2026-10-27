package authentication

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// ErrInvalidRefreshToken is the single generic error every refresh denial returns
// (no row, expired row, or detected reuse) so the response cannot distinguish
// garbage from a stale/stolen token. It wraps sdk.ErrUnauthorized (→ 401).
// Checked with errors.Is.
var ErrInvalidRefreshToken = fmt.Errorf("invalid refresh token: %w", sdk.ErrUnauthorized)

// Refresh implements POST /auth/refresh's pinned rotation contract (§1.3). It
// resolves H = hash(presented) via ONE GetByRefreshHash scan, then branches:
//
//  1. No row → generic 401 (no family detection — the one-generation bound, D5).
//  2. Row expired → 401, re-login (fixed horizon, D2).
//  3. Matched current → rotate (compare-and-swap): respond with a new access JWT
//     AND a new refresh token. A CAS conflict re-resolves once (below).
//  4. Matched previous, not yet consumed → single-use grace: consume the slot and
//     respond with a new access JWT ONLY (no new refresh token — the cookie client
//     keeps the winning token from the concurrent rotation). Recorded refresh
//     success with a "grace" detail.
//  5. Matched previous, already consumed → reuse: revoke the session, record
//     refresh_reuse (blocked, + unconditional WARN), 401.
//
// With SessionsConfig.IdleTimeout wired, branches 3 and 4 first judge the
// session's last activity (idleSince's grandfather rule): an idle session is
// deleted with a compare-and-swap DeleteIfIdle, recorded once as
// session_idle_expired, and answered with the same generic 401. Branch 5 is
// reached before any idle judgement, so a replayed consumed slot is always
// reuse; the re-resolution after a CAS conflict and the grace lane never judge
// or stamp.
//
// The write path is compare-and-swap, never blind: a racing refresh cannot
// corrupt the chain or false-revoke an honest client.
func (s *Service) Refresh(ctx context.Context, presented string) (TokenPair, error) {
	if session.IsDelegatedRefreshToken(presented) {
		return TokenPair{}, ErrDelegatedCredential
	}
	if presented == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	hash, err := s.hashSessionToken(presented)
	if err != nil {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	return s.refreshByHash(ctx, hash)
}

func (s *Service) refreshByHash(ctx context.Context, hash string) (TokenPair, error) {
	var (
		sess     session.Session
		match    session.RefreshMatch
		activity session.Activity
		err      error
	)
	if s.activity != nil {
		sess, match, activity, err = s.activity.GetByRefreshHashWithActivity(ctx, hash)
	} else {
		sess, match, err = s.sessions.GetByRefreshHash(ctx, hash)
	}
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return TokenPair{}, ErrInvalidRefreshToken // branch 1
		}
		return TokenPair{}, err
	}
	if !sess.FirstParty() {
		return TokenPair{}, ErrDelegatedCredential
	}
	if sess.Expired(s.now()) {
		return TokenPair{}, ErrInvalidRefreshToken // branch 2
	}
	// A limiter error fails closed as sdk.ErrUnavailable (503) unless the host
	// chose AbuseProtectionConfig.LimiterFailOpen. Report failures without
	// logging the key.
	res, limitErr := s.limiter.Allow(ctx, refreshSessionKey(sess.ID), ratelimiter.PerMinute(refreshAttemptsPerMinute))
	if err := ctx.Err(); err != nil {
		return TokenPair{}, err
	}
	switch {
	case limitErr != nil:
		s.logger.WarnContext(ctx, "refresh rate limiter failed", "error_kind", ErrorKind(limitErr), "fail_open", s.limiterFailOpen)
		if !s.limiterFailOpen {
			return TokenPair{}, fmt.Errorf("authentication: refresh rate limiter: %w: %w", sdk.ErrUnavailable, limitErr)
		}
	case !res.Allowed:
		return TokenPair{}, ErrRateLimited
	}
	switch match {
	case session.RefreshMatchCurrent:
		if last, idle := s.idleExpired(sess, activity); idle {
			return s.expireIdle(ctx, sess, hash, activity, last)
		}
		return s.rotate(ctx, sess, hash)
	case session.RefreshMatchPrevious:
		if !sess.PreviousUsed {
			if last, idle := s.idleExpired(sess, activity); idle {
				return s.expireIdle(ctx, sess, hash, activity, last)
			}
		}
		return s.grace(ctx, sess, hash)
	default:
		return TokenPair{}, ErrInvalidRefreshToken
	}
}

// rotate performs branch 3: compare-and-swap the live refresh token, minting a
// new one. A CAS conflict re-resolves the presented hash once.
func (s *Service) rotate(ctx context.Context, sess session.Session, currentHash string) (TokenPair, error) {
	rawNew := session.NewRefreshToken()
	newHash, err := s.hashSessionToken(rawNew)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.rotateHash(ctx, sess.ID, currentHash, newHash); err != nil {
		if errors.Is(err, session.ErrRotationConflict) {
			return s.reResolve(ctx, sess, currentHash)
		}
		return TokenPair{}, err
	}
	access, expiresAt, err := s.signAccessToken(sess.UserID, sess.ID, sess.Authentication)
	if err != nil {
		return TokenPair{}, err
	}
	// RotationCount++ landed in the store; reflect it in the audit row.
	sess.RotationCount++
	s.recordRefresh(ctx, sess, "")
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt, RefreshToken: rawNew}, nil
}

// rotateHash runs the rotation compare-and-swap: RotateAt, which also stamps
// the activity column, when idle expiry is wired; Rotate, which never names
// the column, otherwise. Both report a lost CAS as ErrRotationConflict.
func (s *Service) rotateHash(ctx context.Context, id, currentHash, newHash string) error {
	if s.activity != nil {
		return s.activity.RotateAt(ctx, id, currentHash, newHash, s.now())
	}
	return s.sessions.Rotate(ctx, id, currentHash, newHash)
}

// grace performs branch 4: single-use grace on the previous slot. A CAS conflict
// (someone consumed it concurrently) means reuse.
func (s *Service) grace(ctx context.Context, sess session.Session, previousHash string) (TokenPair, error) {
	if sess.PreviousUsed {
		return s.reuse(ctx, sess) // branch 5
	}
	if err := s.sessions.ConsumeGrace(ctx, sess.ID, previousHash); err != nil {
		if errors.Is(err, session.ErrRotationConflict) {
			return s.reuse(ctx, sess)
		}
		return TokenPair{}, err
	}
	access, expiresAt, err := s.signAccessToken(sess.UserID, sess.ID, sess.Authentication)
	if err != nil {
		return TokenPair{}, err
	}
	s.recordRefresh(ctx, sess, "grace")
	// No new refresh token: the cookie client keeps the winning rotation's token.
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt}, nil
}

// reResolve is the once-only re-resolution after a Rotate CAS conflict (§1.3
// branch 3): if the presented hash now sits in the previous slot, it is a benign
// race with a concurrent rotation → fall through to the grace path; if it now
// matches nothing, treat it as reuse (revoke + refresh_reuse). Any other outcome
// (still current — impossible under the single-writer-per-hash invariant) fails
// closed as reuse.
func (s *Service) reResolve(ctx context.Context, sess session.Session, hash string) (TokenPair, error) {
	fresh, match, err := s.sessions.GetByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return s.reuse(ctx, sess)
		}
		return TokenPair{}, err
	}
	return s.reResolveFrom(ctx, sess, hash, fresh, match)
}

// reResolveFrom is reResolve's branch over an already-read row, so a caller
// that has just read it does not read again.
func (s *Service) reResolveFrom(ctx context.Context, sess session.Session, hash string, fresh session.Session, match session.RefreshMatch) (TokenPair, error) {
	if !fresh.FirstParty() {
		return TokenPair{}, ErrDelegatedCredential
	}
	if match == session.RefreshMatchPrevious {
		return s.grace(ctx, fresh, hash)
	}
	return s.reuse(ctx, fresh)
}

// reuse performs branch 5: a reuse of the consumed previous slot burns the
// session (Delete) and records refresh_reuse. Theft collapses here — the second
// arrival on the consumed slot, thief or victim, forces a re-login.
func (s *Service) reuse(ctx context.Context, sess session.Session) (TokenPair, error) {
	if err := s.sessions.Delete(ctx, sess.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return TokenPair{}, err
	}
	s.recordRefreshReuse(ctx, sess)
	return TokenPair{}, ErrInvalidRefreshToken
}

// recordRefresh appends a refresh success audit row. Details carry session_id and
// rotation_count (never the raw token); the grace lane adds a "grace" detail. IP
// and UA ride the event via the client-info carrier (recordSecurityEvent).
func (s *Service) recordRefresh(ctx context.Context, sess session.Session, detail string) {
	details := map[string]any{
		"session_id":     sess.ID,
		"rotation_count": sess.RotationCount,
	}
	if detail != "" {
		details["detail"] = detail
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  sess.UserID,
		Type:    securityevent.TypeRefresh,
		Status:  securityevent.StatusSuccess,
		Details: details,
	})
}

// recordRefreshReuse records a blocked refresh_reuse audit row AND emits an
// unconditional WARN via the service logger (§7) — even when the audit rail is
// unwired, a nil-audit host must not be blind to token theft. Neither the log nor
// the Details ever carries the raw token.
func (s *Service) recordRefreshReuse(ctx context.Context, sess session.Session) {
	info := clientInfoFromContext(ctx)
	s.logger.Warn("refresh token reuse detected",
		"session_id", sess.ID,
		"user_id", sess.UserID,
		"rotation_count", sess.RotationCount,
		"ip", info.ip,
		"ua", info.ua,
	)
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: sess.UserID,
		Type:   securityevent.TypeRefreshReuse,
		Status: securityevent.StatusBlocked,
		Details: map[string]any{
			"session_id":     sess.ID,
			"rotation_count": sess.RotationCount,
		},
	})
}

// refreshSessionKey derives the per-session rate-limit key for refresh.
func refreshSessionKey(sessionID string) string { return "refresh:" + sessionID }
