package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireAccessTokenOrAPIKeyLiveUserJWT covers the §1.4 matrix for a user
// JWT: a live session passes; after the session is deleted, the SAME
// (still-unexpired) access JWT is denied — immediate revocation, unlike the
// stateless tier.
func TestRequireAccessTokenOrAPIKeyLiveUserJWT(t *testing.T) {
	h := newMiddlewareHarness(t, nil)
	pair := h.loginPair(t, "live@example.com", "password123456789")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireAccessTokenOrAPIKeyLive()(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("live session status = %d, want 204", rec.Code)
	}

	// Revoke every session, then re-issue the same request: the JWT is still valid
	// statelessly, but the live-session lookup now denies.
	for _, id := range middlewareSessions(h) {
		if err := h.sess.Delete(context.Background(), id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec2 := httptest.NewRecorder()
	h.svc.RequireAccessTokenOrAPIKeyLive()(next).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("revoked live session status = %d, want 401", rec2.Code)
	}

	// But the stateless RequireAccessToken() still admits the unexpired JWT (the
	// bounded window).
	req3 := httptest.NewRequest("GET", "/x", nil)
	req3.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec3 := httptest.NewRecorder()
	h.svc.RequireAccessToken()(next).ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("stateless RequireAccessToken() after revoke status = %d, want 204 (bounded window)", rec3.Code)
	}
}

// TestRequireAccessTokenOrAPIKeyLiveFailsClosed covers the fail-CLOSED posture
// (D1): a repository error on the session lookup denies.
func TestRequireAccessTokenOrAPIKeyLiveFailsClosed(t *testing.T) {
	h := newMiddlewareHarness(t, nil)
	pair := h.loginPair(t, "fc@example.com", "password123456789")
	h.sess.getErr = errors.New("store unavailable")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireAccessTokenOrAPIKeyLive()(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("repo error status = %d, want 401 (fail closed)", rec.Code)
	}
}
