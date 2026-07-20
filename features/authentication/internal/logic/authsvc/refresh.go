package authsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
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
// The write path is compare-and-swap, never blind: a racing refresh cannot
// corrupt the chain or false-revoke an honest client.
func (s *Service) Refresh(ctx context.Context, presented string) (TokenPair, error) {
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
	sess, match, err := s.sessions.GetByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return TokenPair{}, ErrInvalidRefreshToken // branch 1
		}
		return TokenPair{}, err
	}
	if sess.Expired(s.now()) {
		return TokenPair{}, ErrInvalidRefreshToken // branch 2
	}
	// Rate-limit per session (the by-session arm of the §6 refresh limit; the
	// by-IP arm is a route middleware). A limiter error fails OPEN — the in-memory
	// default never errors and a refresh must not break on a limiter outage.
	if res, lerr := s.limiter.Allow(ctx, refreshSessionKey(sess.ID), ratelimiter.PerMinute(refreshAttemptsPerMinute)); lerr == nil && !res.Allowed {
		return TokenPair{}, ErrRateLimited
	}
	switch match {
	case session.RefreshMatchCurrent:
		return s.rotate(ctx, sess, hash)
	case session.RefreshMatchPrevious:
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
	if err := s.sessions.Rotate(ctx, sess.ID, currentHash, newHash); err != nil {
		if errors.Is(err, session.ErrRotationConflict) {
			return s.reResolve(ctx, sess, currentHash)
		}
		return TokenPair{}, err
	}
	access, expiresAt, err := s.signAccessToken(sess.UserID, sess.ID)
	if err != nil {
		return TokenPair{}, err
	}
	// RotationCount++ landed in the store; reflect it in the audit row.
	sess.RotationCount++
	s.recordRefresh(ctx, sess, "")
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt, RefreshToken: rawNew}, nil
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
	access, expiresAt, err := s.signAccessToken(sess.UserID, sess.ID)
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

// RequireLiveSession gates next on a live session, one PK lookup per request
// (§1.2/§1.4). It is the immediate-revocation tier for sensitive routes: unlike
// RequireUser (stateless JWT), a deleted or expired session denies here within
// one round-trip. Per-principal matrix (§1.4):
//
//   - user JWT (bearer or access cookie): verify JWT, then sessions.Get(session_id);
//     a missing/expired row denies.
//   - API key: already DB-checked at resolution; pass (no session row exists, so a
//     naive lookup would wrongly reject every machine caller).
//   - no/invalid credential: deny.
//   - repository error: deny — fails CLOSED (never harmonized toward the limiter's
//     fail-open, D1).
//
// On success it stashes the resolved Principal (read via CurrentPrincipal /
// CurrentUser) and calls next; otherwise it writes a 401.
func (s *Service) RequireLiveSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, sessionID, ok := s.resolveLiveSession(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		ctx := identity.WithPrincipal(r.Context(), p)
		// A user session stamps its id so a sensitive-mutation handler binds its
		// step-up grant to the exact live session (design §5.0). A machine (API-key)
		// caller has no session row, so sessionID stays empty and CurrentSessionID
		// reports absent.
		if sessionID != "" {
			ctx = withSessionID(ctx, sessionID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireLiveSessionBrowser is the browser-facing sibling of RequireLiveSession
// (design §9.2): it enforces the SAME §1.4 live-session matrix through
// resolveLiveSession and stashes the SAME Principal (and, on the user path, the live
// session id), but on denial it 303s to the configured browser login path instead of
// writing a JSON 401. A denied GET/HEAD carries a validated return_to
// (redirectToBrowserLogin). It is mounted deliberately on HTML routes and NEVER sniffs
// Accept or Fetch Metadata; a statelessly-valid but revoked user session that passes
// RequirePrincipalBrowser is denied here.
func (s *Service) RequireLiveSessionBrowser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, sessionID, ok := s.resolveLiveSession(r)
		if !ok {
			s.redirectToBrowserLogin(w, r)
			return
		}
		ctx := identity.WithPrincipal(r.Context(), p)
		if sessionID != "" {
			ctx = withSessionID(ctx, sessionID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveLiveSession classes the credential and enforces the §1.4 matrix. It
// returns the resolved principal and, for a user session, the live session id (empty
// for a session-less machine caller).
func (s *Service) resolveLiveSession(r *http.Request) (Principal, string, bool) {
	if raw, ok := bearerToken(r); ok {
		if isJWTToken(raw) {
			userID, sessionID, ok := s.verifyBearerClaims(raw)
			if !ok || !s.sessionLive(r.Context(), sessionID) {
				return Principal{}, "", false
			}
			return Principal{Type: PrincipalUser, ID: userID}, sessionID, true
		}
		// API key: already DB-checked by resolution; no session row exists, pass.
		if !s.MachineEnabled() {
			return Principal{}, "", false
		}
		p, err := s.AuthenticateAPIKey(r.Context(), raw)
		if err != nil {
			return Principal{}, "", false
		}
		return p, "", true
	}
	// No bearer: the access-JWT session cookie.
	c, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return Principal{}, "", false
	}
	userID, sessionID, ok := s.verifyBearerClaims(c.Value)
	if !ok || !s.sessionLive(r.Context(), sessionID) {
		return Principal{}, "", false
	}
	return Principal{Type: PrincipalUser, ID: userID}, sessionID, true
}

// sessionLive reports whether sessionID resolves to a live (present, unexpired)
// session. A blank id, a missing/expired row, OR any repository error all return
// false — the fail-CLOSED posture (D1): a store outage denies, never admits.
func (s *Service) sessionLive(ctx context.Context, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, err := s.sessions.Get(ctx, sessionID)
	return err == nil
}
