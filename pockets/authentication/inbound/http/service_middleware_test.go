package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireAccessTokenValidSession(t *testing.T) {
	h := newMiddlewareHarness(t, nil)
	h.mustRegister(t, "ru@example.com", "password123456789")
	pair, u, _ := h.svc.Login(context.Background(), "ru@example.com", "password123456789")

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser not set inside RequireAccessToken()")
		}
		gotUserID = id
		w.WriteHeader(http.StatusNoContent)
	})

	// The browser session cookie carries the access JWT (verified statelessly).
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireAccessToken()(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if gotUserID != u.ID {
		t.Errorf("CurrentUser = %q, want %q", gotUserID, u.ID)
	}
}

func TestRequireAccessTokenNoCookie(t *testing.T) {
	h := newMiddlewareHarness(t, nil)
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.svc.RequireAccessToken()(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler called without a session")
	}
}

func TestRequireAccessTokenExpiredSession(t *testing.T) {
	h := newMiddlewareHarness(t, nil)
	// An expired access JWT in the session cookie is rejected statelessly (§1.2).
	expired, err := h.signer.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign(expired): %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: expired})
	rec := httptest.NewRecorder()
	h.svc.RequireAccessToken()(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
