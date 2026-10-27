package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// DefaultAccessTokenTTLCeiling is the access-token lifetime above which a
// production New logs posture_warning=access_token_ttl_long. It is a WARN, not
// a refusal: every refresh and every Live() read still bounds the window.
const DefaultAccessTokenTTLCeiling = time.Hour

const (
	// idleTimeoutProbeTimeout bounds the boot-time probe of the activity
	// column.
	idleTimeoutProbeTimeout = 5 * time.Second
	// idleSurfaceRefresh is session_idle_expired's surface: idle expiry is
	// judged at refresh only.
	idleSurfaceRefresh = "refresh"
)

var (
	// ErrIdleTimeoutRequires is returned by New when SessionsConfig.IdleTimeout
	// is set and Repositories.Sessions does not implement
	// session.ActivityRepository: upgrade to a store that does
	// (pockets/authentication/stores/pgx or turso, the minor after core
	// v0.32.0) or leave the idle timeout off.
	ErrIdleTimeoutRequires = fmt.Errorf("authentication: SessionsConfig.IdleTimeout requires Repositories.Sessions to implement session.ActivityRepository (stores pgx and turso at the minor after core v0.32.0): %w", sdk.ErrInvalidInput)
	// ErrIdleTimeoutSchemaMissing is returned by New when
	// SessionsConfig.IdleTimeout is set and the session store reports its
	// activity column absent: apply migration 0029_session_activity.sql. A
	// host that does not set it is never probed.
	ErrIdleTimeoutSchemaMissing = errors.New("authentication: SessionsConfig.IdleTimeout is set but the sessions schema is missing last_rotated_at (apply migration 0029_session_activity.sql)")
	// ErrIdleTimeoutBelowAccessTokenTTL is returned by New in every mode for a
	// negative SessionsConfig.IdleTimeout, or one at or below the resolved
	// AccessTokenTTL, under which every refresh would find its session idle.
	ErrIdleTimeoutBelowAccessTokenTTL = fmt.Errorf("authentication: SessionsConfig.IdleTimeout must be zero (off) or greater than SessionsConfig.AccessTokenTTL: %w", sdk.ErrInvalidInput)
	// ErrAccessTokenTTLExceedsRefreshTTL is returned by New in every mode when
	// the resolved SessionsConfig.AccessTokenTTL exceeds the resolved
	// SessionsConfig.RefreshTTL: an access token would outlive its session.
	ErrAccessTokenTTLExceedsRefreshTTL = fmt.Errorf("authentication: SessionsConfig.AccessTokenTTL exceeds SessionsConfig.RefreshTTL: %w", sdk.ErrInvalidInput)

	// errSessionActivityCASMismatch is the internal error of a DeleteIfIdle
	// that missed a row which had not moved: a store defect, never reuse. It
	// wraps no sdk kind, so it answers 500.
	errSessionActivityCASMismatch = errors.New("session activity compare-and-swap missed an unchanged row")
)

// SessionPolicy reports the resolved session lifetimes: AccessTokenTTL and
// RefreshTTL after their defaults, and IdleTimeout (zero when off).
func (s *Service) SessionPolicy() SessionsConfig {
	return SessionsConfig{AccessTokenTTL: s.accessTTL, RefreshTTL: s.refreshTTL, IdleTimeout: s.idleTimeout}
}

// validateSessionLifetime checks SessionsConfig on its post-default values in
// every mode — an incoherent pair refuses, a coarse or inert idle timeout
// WARNs — then, with an idle timeout set, the store's activity capability.
func validateSessionLifetime(d constructorConfig, log *slog.Logger) error {
	access, refresh, idle := d.AccessTokenTTL, d.RefreshTTL, d.IdleTimeout
	if access <= 0 {
		access = defaultAccessTokenTTL
	}
	if refresh <= 0 {
		refresh = defaultRefreshTTL
	}
	if access > refresh {
		return fmt.Errorf("%w (AccessTokenTTL %s, RefreshTTL %s)", ErrAccessTokenTTLExceedsRefreshTTL, access, refresh)
	}
	if idle < 0 || (idle > 0 && idle <= access) {
		return fmt.Errorf("%w (IdleTimeout %s, AccessTokenTTL %s)", ErrIdleTimeoutBelowAccessTokenTTL, idle, access)
	}
	if d.RuntimeMode == environment.ModeProduction && access > DefaultAccessTokenTTLCeiling {
		log.Warn("auth: access tokens outlive the recommended ceiling", "posture_warning", "access_token_ttl_long", "access_token_ttl", access.String(), "ceiling", DefaultAccessTokenTTLCeiling.String())
	}
	if idle == 0 {
		return nil
	}
	if idle < 2*access {
		log.Warn("auth: the idle timeout is under twice the access-token lifetime, so idle expiry lands up to one access-token lifetime late", "posture_warning", "idle_granularity", "idle_timeout", idle.String(), "access_token_ttl", access.String())
	}
	if idle >= refresh {
		log.Warn("auth: the idle timeout is not below the refresh horizon, so a session expires before it can idle", "posture_warning", "idle_never_fires", "idle_timeout", idle.String(), "refresh_ttl", refresh.String())
	}
	return validateIdleTimeout(d)
}

// idleSince reports the activity instant a first-party refresh is judged
// against and whether it is judged at all (the grandfather rule): the stamp
// when present; created_at for a row never rotated; otherwise — a row rotated
// while idle expiry was not wired — not judged until RotateAt stamps it.
func idleSince(sess session.Session, a session.Activity) (time.Time, bool) {
	switch {
	case a.Stamped:
		return a.LastRotatedAt, true
	case sess.RotationCount == 0:
		return sess.CreatedAt, true
	}
	return time.Time{}, false
}

// idleExpired reports whether the wired idle timeout ends sess at s.now(),
// and the activity instant it was judged on.
func (s *Service) idleExpired(sess session.Session, a session.Activity) (time.Time, bool) {
	if s.activity == nil || !sess.FirstParty() {
		return time.Time{}, false
	}
	last, judged := idleSince(sess, a)
	if !judged {
		return time.Time{}, false
	}
	return last, s.now().Sub(last) >= s.idleTimeout
}

// expireIdle deletes an idle session with the compare-and-swap DeleteIfIdle.
// Only the call that deleted the row records session_idle_expired; a lost CAS
// re-resolves the presented hash, since the row moved under a concurrent
// refresh. Either way the caller gets the generic refresh denial unless the
// re-resolution admits a grace refresh.
func (s *Service) expireIdle(ctx context.Context, sess session.Session, hash string, a session.Activity, last time.Time) (TokenPair, error) {
	now := s.now()
	deleted, err := s.activity.DeleteIfIdle(ctx, sess.ID, a, sess.RotationCount)
	if err != nil {
		return TokenPair{}, err
	}
	if !deleted {
		return s.reResolveIdle(ctx, sess, hash)
	}
	s.recordIdleExpired(ctx, sess, last, now)
	return TokenPair{}, ErrInvalidRefreshToken
}

// reResolveIdle is reResolve after a lost DeleteIfIdle, over ONE read: a row
// that is already gone was ended by someone else (a concurrent idle expiry, a
// logout or a revocation, each recorded by its own path), so it answers the
// generic denial without a refresh_reuse row; a row that moved goes through
// reResolveFrom. A row that did NOT move — the hash still current at the same
// rotation count — means the store's CAS missed a row it should have matched
// (an activity-encoding defect), so it is an internal error, never reuse.
func (s *Service) reResolveIdle(ctx context.Context, sess session.Session, hash string) (TokenPair, error) {
	fresh, match, err := s.sessions.GetByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return TokenPair{}, ErrInvalidRefreshToken
		}
		return TokenPair{}, err
	}
	if match == session.RefreshMatchCurrent && fresh.ID == sess.ID && fresh.RotationCount == sess.RotationCount {
		s.logger.WarnContext(ctx, "auth: DeleteIfIdle missed an unchanged session row; check the store's last_rotated_at encoding",
			"warning", "session_activity_cas_mismatch", "session_id", sess.ID, "user_id", sess.UserID, "rotation_count", sess.RotationCount)
		return TokenPair{}, fmt.Errorf("authentication: idle expiry of session %s: %w", sess.ID, errSessionActivityCASMismatch)
	}
	return s.reResolveFrom(ctx, sess, hash, fresh, match)
}

// recordIdleExpired appends the blocked session_idle_expired row. Details
// never carry a token or a hash.
func (s *Service) recordIdleExpired(ctx context.Context, sess session.Session, last, now time.Time) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: sess.UserID,
		Type:   securityevent.TypeSessionIdleExpired,
		Status: securityevent.StatusBlocked,
		Details: map[string]any{
			"session_id":           sess.ID,
			"last_activity":        last.UTC().Format(time.RFC3339),
			"idle_seconds":         int(now.Sub(last) / time.Second),
			"idle_timeout_seconds": int(s.idleTimeout / time.Second),
			"surface":              idleSurfaceRefresh,
		},
	})
}
