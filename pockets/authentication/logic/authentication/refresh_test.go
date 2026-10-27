package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

var _ session.ActivityRepository = (*rotateRaceActivity)(nil)

// loginPair registers + logs in a user and returns the minted pair.
func (h *harness) loginPair(t *testing.T, email, password string) TokenPair {
	t.Helper()
	h.mustRegister(t, email, password)
	pair, _, err := h.svc.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return pair
}

// TestRefreshRotationHappyPath covers §1.3 branch 3: a current-match refresh
// rotates, issuing a new access token AND a new refresh token; the old refresh
// token is now the previous (grace) slot.
func TestRefreshRotationHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "rot@example.com", "password123456789")

	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if next.AccessToken == "" || next.RefreshToken == "" {
		t.Fatalf("rotation did not issue a full pair: %+v", next)
	}
	if next.RefreshToken == pair.RefreshToken {
		t.Error("rotation reused the same refresh token")
	}
	requireEvent(t, h.events, securityevent.TypeRefresh, securityevent.StatusSuccess)
}

// TestRefreshGraceThenReuse covers branches 4 and 5: the first refresh rotates;
// re-presenting the ORIGINAL token once is a single-use grace (new access token
// only, no new refresh token); presenting it a SECOND time is reuse → the session
// is revoked, a refresh_reuse event is recorded, and every token 401s.
func TestRefreshGraceThenReuse(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "grace@example.com", "password123456789")

	rotated, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh (rotate): %v", err)
	}

	// Grace: the original token still yields ONE new access token, no refresh token.
	grace, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh (grace): %v", err)
	}
	if grace.AccessToken == "" {
		t.Error("grace lane issued no access token")
	}
	if grace.RefreshToken != "" {
		t.Error("grace lane must NOT issue a new refresh token")
	}

	// Reuse: a second use of the consumed previous slot burns the session.
	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("reuse: err=%v, want ErrInvalidRefreshToken", err)
	}
	if h.sess.count() != 0 {
		t.Errorf("reuse did not revoke the session: count=%d, want 0", h.sess.count())
	}
	requireEvent(t, h.events, securityevent.TypeRefreshReuse, securityevent.StatusBlocked)

	// The session is gone: even the winning rotated token no longer refreshes.
	if _, err := h.svc.Refresh(context.Background(), rotated.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("post-revoke refresh: err=%v, want ErrInvalidRefreshToken", err)
	}
}

// TestRefreshUnknownToken covers branch 1: an unknown token is a generic 401 with
// no session touched.
func TestRefreshUnknownToken(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.svc.Refresh(context.Background(), "no-such-token"); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("unknown token: err=%v, want ErrInvalidRefreshToken", err)
	}
	if _, err := h.svc.Refresh(context.Background(), ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("blank token: err=%v, want ErrInvalidRefreshToken", err)
	}
}

// snapshotSessions returns the ids currently in the fake store.
func snapshotSessions(h *harness) []string {
	h.sess.mu.Lock()
	defer h.sess.mu.Unlock()
	ids := make([]string, 0, len(h.sess.m))
	for id := range h.sess.m {
		ids = append(ids, id)
	}
	return ids
}

// rotateRaceActivity stages a concurrent rotation just before the service's
// own RotateAt, so the service loses the compare-and-swap.
type rotateRaceActivity struct {
	*fakeActivity
	before func()
}

func (r *rotateRaceActivity) RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error {
	if r.before != nil {
		hook := r.before
		r.before = nil
		hook()
	}
	return r.fakeActivity.RotateAt(ctx, id, expectedCurrentHash, newHash, now)
}

// TestRefreshWiredRotationStampsAndReResolveNeverJudges: with idle expiry
// wired, branch 3 rotates through RotateAt (the stamp lands with the CAS), and
// a lost RotateAt re-resolves to the grace lane without judging idleness or
// stamping — even when the concurrent winner left a stamp an idle judgement
// would refuse.
func TestRefreshWiredRotationStampsAndReResolveNeverJudges(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	race := &rotateRaceActivity{fakeActivity: h.activity}
	h.svc.activity = race
	pair := h.loginPair(t, "race@example.com", "password123456789")
	hash, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	staleStamp := h.clock.Now().Add(-2 * testIdleTimeout)
	race.before = func() {
		id := snapshotSessions(h.harness)[0]
		if err := h.activity.RotateAt(ctx, id, hash, "concurrent-winner-hash", staleStamp); err != nil {
			t.Errorf("staged rotation: %v", err)
		}
	}

	h.clock.advance(time.Minute)
	got, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil || got.AccessToken == "" || got.RefreshToken != "" {
		t.Fatalf("Refresh after a lost RotateAt = %+v, %v; want the grace lane's access token only", got, err)
	}
	ids := snapshotSessions(h.harness)
	if len(ids) != 1 || len(h.activity.stamps) != 1 || !h.activity.stamps[ids[0]].Equal(staleStamp) {
		t.Fatalf("stamps = %v, want only the concurrent winner's %v", h.activity.stamps, staleStamp)
	}
	if len(h.idleEvents()) != 0 || h.eventCount(securityevent.TypeRefreshReuse) != 0 {
		t.Fatalf("idle %d, reuse %d; want neither", len(h.idleEvents()), h.eventCount(securityevent.TypeRefreshReuse))
	}
	sess, _, err := h.sess.GetByRefreshHash(ctx, hash)
	if err != nil || !sess.PreviousUsed {
		t.Fatalf("previous slot after grace = %+v, %v; want consumed", sess, err)
	}
}
