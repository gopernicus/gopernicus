package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func TestAPIKeyCredentialSubsystemOff(t *testing.T) {
	// No machine repos wired → the subsystem is off; the default set drops the
	// api_key kind entirely, so a key bearer denies without a lookup.
	svc := newServiceWithFakes(authenticationFixture{
		Users:     newMemUsers(),
		Passwords: &memPasswords{m: map[string]string{}},
		Sessions:  &memSessions{m: map[string]session.Session{}},
		Hasher:    &fakeHasher{},
		Limiter:   ratelimiter.NewMemory(),
	})
	if svc.MachineEnabled() {
		t.Fatal("MachineEnabled reported true with no machine repos")
	}
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	svc.RequirePrincipal()(next).ServeHTTP(rec, middlewareBearerRequest("prefix_x"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("api key with subsystem off: status = %d, want 401", rec.Code)
	}
}

func middlewareBearerRequest(token string) *http.Request {
	r := httptest.NewRequest("GET", "/x", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestRequireAPIKeyValidKey(t *testing.T) {
	h := newMiddlewareHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.svc.CurrentPrincipal(r.Context())
		if !ok {
			t.Error("CurrentPrincipal not set inside RequireAPIKey")
		}
		got = p
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequireAPIKey()(next).ServeHTTP(rec, middlewareBearerRequest(raw))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got.Type != authlogic.PrincipalServiceAccount || got.ID != sa.ID {
		t.Errorf("principal = %+v, want {service_account, %s}", got, sa.ID)
	}
}

func TestRequireAPIKeyNoHeader(t *testing.T) {
	h := newMiddlewareHarness(t)
	rec := httptest.NewRecorder()
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	h.svc.RequireAPIKey()(next).ServeHTTP(rec, middlewareBearerRequest(""))
	if rec.Code != http.StatusUnauthorized || called {
		t.Errorf("no header: status=%d called=%v, want 401 not-called", rec.Code, called)
	}
}

func TestRequireAPIKeyJWTShapedDenied(t *testing.T) {
	// A two-dot bearer is classed as a JWT; RequireAPIKey admits only api_key, so
	// the access-token kind falls outside its set and denies rather than being
	// hashed as a key.
	h := newMiddlewareHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequireAPIKey()(next).ServeHTTP(rec, middlewareBearerRequest("aaa.bbb.ccc"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("jwt-shaped bearer: status = %d, want 401", rec.Code)
	}
}

func TestRequireAPIKeySubsystemOff(t *testing.T) {
	svc := newServiceWithFakes(authenticationFixture{
		Users: newMemUsers(), Passwords: &memPasswords{m: map[string]string{}}, Sessions: &memSessions{m: map[string]session.Session{}},
		Hasher:  &fakeHasher{},
		Limiter: ratelimiter.NewMemory(),
	})
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	svc.RequireAPIKey()(next).ServeHTTP(rec, middlewareBearerRequest("prefix_x"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("api-key path with subsystem off: status = %d, want 401", rec.Code)
	}
}

func TestRequirePrincipalAPIKey(t *testing.T) {
	h := newMiddlewareHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal()(next).ServeHTTP(rec, middlewareBearerRequest(raw))
	if rec.Code != http.StatusNoContent || got.Type != authlogic.PrincipalServiceAccount || got.ID != sa.ID {
		t.Errorf("api-key principal: status=%d principal=%+v", rec.Code, got)
	}
}

func TestRequirePrincipalSession(t *testing.T) {
	h := newMiddlewareHarness(t)
	ctx := context.Background()
	u, err := h.svc.Register(ctx, "sess@example.com", "password123456789", "Sess")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, _, err := h.svc.Login(ctx, "sess@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal()(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || got.Type != authlogic.PrincipalUser || got.ID != u.ID {
		t.Errorf("session principal: status=%d principal=%+v, want {user, %s}", rec.Code, got, u.ID)
	}
}

func TestRequirePrincipalJWTShapedGarbageDenied(t *testing.T) {
	// A two-dot bearer is the JWT path; garbage fails verification → 401, and it
	// must NOT fall through to the session path.
	h := newMiddlewareHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequirePrincipal()(next).ServeHTTP(rec, middlewareBearerRequest("aaa.bbb.ccc"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("jwt-shaped bearer: status = %d, want 401", rec.Code)
	}
}

func TestRequirePrincipalNoCredential(t *testing.T) {
	h := newMiddlewareHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequirePrincipal()(next).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no credential: status = %d, want 401", rec.Code)
	}
}
