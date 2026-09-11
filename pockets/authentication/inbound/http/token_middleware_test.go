package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
)

func TestRequireAccessTokenBearerJWT(t *testing.T) {
	h := newMiddlewareTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "bru@example.com", "password123456789")
	pair, err := h.svc.IssueToken(context.Background(), "bru@example.com", "password123456789")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	tok := pair.AccessToken

	var gotID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser not set inside RequireAccessToken() via bearer JWT")
		}
		gotID = id
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequireAccessToken()(next).ServeHTTP(rec, middlewareBearerRequest(tok))
	if rec.Code != http.StatusNoContent || gotID != u.ID {
		t.Errorf("bearer RequireAccessToken(): status=%d id=%q, want 204 %q", rec.Code, gotID, u.ID)
	}
}

func TestRequirePrincipalBearerJWT(t *testing.T) {
	h := newMiddlewareTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "brp@example.com", "password123456789")
	pair, _ := h.svc.IssueToken(context.Background(), "brp@example.com", "password123456789")
	tok := pair.AccessToken

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal()(next).ServeHTTP(rec, middlewareBearerRequest(tok))
	if rec.Code != http.StatusNoContent || got.Type != authlogic.PrincipalUser || got.ID != u.ID {
		t.Errorf("bearer RequirePrincipal(): status=%d principal=%+v, want 204 {user, %s}", rec.Code, got, u.ID)
	}
}

func TestBearerExpiredDenied(t *testing.T) {
	signer := newFakeSigner()
	h := newMiddlewareTokenHarness(t, signer, false, nil)
	// A genuinely-expired token (exp in the past) — the honest fake rejects it.
	expired, err := signer.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	assertBearerDenied(t, h, expired, "expired")
}

func TestBearerTamperedSignatureDenied(t *testing.T) {
	signer := newFakeSigner()
	h := newMiddlewareTokenHarness(t, signer, false, nil)
	valid, _ := signer.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(time.Hour))
	tampered := flipLastRune(valid)
	if tampered == valid {
		t.Fatal("failed to tamper the signature")
	}
	assertBearerDenied(t, h, tampered, "tampered")
}

func TestBearerGarbageDenied(t *testing.T) {
	h := newMiddlewareTokenHarness(t, newFakeSigner(), false, nil)
	// Two-dot garbage classes as a JWT, then Verify fails — no panic, 401.
	assertBearerDenied(t, h, "aaa.bbb.ccc", "garbage")
}

func TestBearerWrongSecretDenied(t *testing.T) {
	// A token signed by a DIFFERENT secret must not verify against this service.
	other := &fakeSigner{secret: []byte("a-totally-different-secret-value!"), now: time.Now}
	forged, _ := other.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(time.Hour))
	h := newMiddlewareTokenHarness(t, newFakeSigner(), false, nil)
	assertBearerDenied(t, h, forged, "wrong-secret")
}

// assertBearerDenied asserts that both RequireAccessToken() and RequirePrincipal()
// reject a bearer token with 401 and never call next.
func assertBearerDenied(t *testing.T, h *middlewareHarness, token, label string) {
	t.Helper()
	for name, mw := range map[string]func(http.Handler) http.Handler{
		"RequireAccessToken": h.svc.RequireAccessToken(),
		"RequirePrincipal":   h.svc.RequirePrincipal(),
	} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, middlewareBearerRequest(token))
		if rec.Code != http.StatusUnauthorized || called {
			t.Errorf("%s(%s): status=%d called=%v, want 401 not-called", name, label, rec.Code, called)
		}
	}
}

// flipLastRune returns s with its final byte flipped to a different base64url
// character, corrupting the signature segment.
func flipLastRune(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}
