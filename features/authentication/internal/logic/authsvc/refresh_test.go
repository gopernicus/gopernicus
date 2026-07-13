package authsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
)

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

// TestRequireLiveSessionUserJWT covers the §1.4 matrix for a user JWT: a live
// session passes; after the session is deleted, the SAME (still-unexpired) access
// JWT is denied — immediate revocation, unlike stateless RequireUser.
func TestRequireLiveSessionUserJWT(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "live@example.com", "password123456789")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireLiveSession(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("live session status = %d, want 204", rec.Code)
	}

	// Revoke every session, then re-issue the same request: the JWT is still valid
	// statelessly, but the live-session lookup now denies.
	for _, id := range snapshotSessions(h) {
		if err := h.sess.Delete(context.Background(), id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec2 := httptest.NewRecorder()
	h.svc.RequireLiveSession(next).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("revoked live session status = %d, want 401", rec2.Code)
	}

	// But stateless RequireUser still admits the unexpired JWT (the bounded window).
	req3 := httptest.NewRequest("GET", "/x", nil)
	req3.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec3 := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("stateless RequireUser after revoke status = %d, want 204 (bounded window)", rec3.Code)
	}
}

// TestRequireLiveSessionFailsClosed covers the fail-CLOSED posture (D1): a
// repository error on the session lookup denies.
func TestRequireLiveSessionFailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "fc@example.com", "password123456789")
	h.sess.getErr = errors.New("store unavailable")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireLiveSession(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("repo error status = %d, want 401 (fail closed)", rec.Code)
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
