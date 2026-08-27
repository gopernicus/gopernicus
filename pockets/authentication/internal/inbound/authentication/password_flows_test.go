package authentication

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// newPasswordFlowsHandler mounts the JSON route table over the in-memory rail
// with the password posture on or off. The machine repositories are wired
// throughout, behind an allow-all host gate.
func newPasswordFlowsHandler(t *testing.T, disabled bool) http.Handler {
	t.Helper()
	return newPostureHandler(t, disabled, allowMachineGate)
}

// newPostureHandler mounts the JSON route table over the in-memory rail with
// the password posture and the machine-route gate set independently. A nil gate
// is the machine-lifecycle deny-by-absence posture.
func newPostureHandler(t *testing.T, passwordDisabled bool, machineGate web.Middleware) http.Handler {
	t.Helper()
	users := newMemUsers()
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:                 users,
		Identifiers:           newMemIdentifiers(users),
		Passwords:             &memPasswords{m: map[string]string{}},
		Sessions:              &memSessions{m: map[string]session.Session{}},
		Challenges:            &memChallenges{byID: map[string]challenge.Challenge{}},
		Protector:             memProtector{},
		Hasher:                fakeHasher{},
		Deliver:               router,
		Queue:                 stubQueue{},
		Limiter:               ratelimiter.NewMemory(),
		Cookie:                authsvc.CookieConfig{},
		TokenSigner:           newFakeSigner(),
		Clock:                 time.Now,
		PasswordFlowsDisabled: passwordDisabled,
		ServiceAccounts:       &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:               &memAPIKeys{m: map[string]apikey.APIKey{}},
	})
	h := web.NewWebHandler()
	Mount(h, Deps{Auth: svc, MachineGate: machineGate})
	return h
}

// TestPasswordFlowsDisabled_RoutesAbsent proves deny-by-absence: with the
// posture off every password route answers 404 (not 400/401/403 — it is not
// there), while the non-password surface (refresh, logout, me) is unchanged.
// With the posture on (the default) the same routes exist.
func TestPasswordFlowsDisabled_RoutesAbsent(t *testing.T) {
	passwordRoutes := []string{
		"/auth/register", "/auth/login", "/auth/verify", "/auth/verification/resend",
		"/auth/password/forgot", "/auth/password/reset", "/auth/password/change", "/auth/password/set",
		"/auth/password/remove/start", "/auth/password/remove", "/auth/step-up/password",
	}
	keptRoutes := []string{"/auth/refresh", "/auth/logout", "/auth/step-up/begin"}
	post := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	off := newPasswordFlowsHandler(t, true)
	for _, p := range passwordRoutes {
		if code := post(off, p); code != http.StatusNotFound {
			t.Errorf("disabled: POST %s = %d, want 404", p, code)
		}
	}
	for _, p := range keptRoutes {
		if code := post(off, p); code == http.StatusNotFound {
			t.Errorf("disabled: POST %s must stay mounted, got 404", p)
		}
	}

	on := newPasswordFlowsHandler(t, false)
	for _, p := range passwordRoutes {
		if code := post(on, p); code == http.StatusNotFound {
			t.Errorf("enabled (default): POST %s must be mounted, got 404", p)
		}
	}
}

// TestMachineRoutesGateAbsent_RoutesAbsent proves the lifecycle routes are
// deny-by-absence without a Config.MachineRoutesGate, while the machine
// subsystem itself stays on (MachineEnabled true — a bearer key still
// authenticates).
func TestMachineRoutesGateAbsent_RoutesAbsent(t *testing.T) {
	lifecycle := []string{"/auth/service-accounts", "/auth/service-accounts/sa-1/keys", "/auth/api-keys/k-1/revoke"}
	post := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	off := newPostureHandler(t, false, nil)
	for _, p := range lifecycle {
		if code := post(off, p); code != http.StatusNotFound {
			t.Errorf("no gate: POST %s = %d, want 404", p, code)
		}
	}
	on := newPostureHandler(t, false, allowMachineGate)
	for _, p := range lifecycle {
		if code := post(on, p); code == http.StatusNotFound {
			t.Errorf("gated: POST %s must be mounted, got 404", p)
		}
	}
}
