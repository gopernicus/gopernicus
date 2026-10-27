package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The liveness floor under the sensitive bundled groups (SOC 2 S4d part 2,
// R-2): a host override owns a group's credential set and transports, but
// SessionHydration, SessionSecurityReads, CredentialManagement,
// MachineLifecycle and UserAdministration still prove a live session unless
// the floor is relaxed.

// sensitiveGroups names the floored groups and selects each one's middleware.
var sensitiveGroups = map[string]func(RouteAuthentication) web.Middleware{
	"SessionHydration":     func(a RouteAuthentication) web.Middleware { return a.SessionHydration },
	"SessionSecurityReads": func(a RouteAuthentication) web.Middleware { return a.SessionSecurityReads },
	"CredentialManagement": func(a RouteAuthentication) web.Middleware { return a.CredentialManagement },
	"MachineLifecycle":     func(a RouteAuthentication) web.Middleware { return a.MachineLifecycle },
	"UserAdministration":   func(a RouteAuthentication) web.Middleware { return a.UserAdministration },
}

// statelessOverrides lowers every group to Stateless(), the override shape
// the floor exists for.
func statelessOverrides(svc *testService) RouteAuthentication {
	s := svc.RequirePrincipal(Stateless())
	return RouteAuthentication{
		OAuthLinkStart: s, SessionSecurityReads: s, CSRFBootstrap: s, SessionHydration: s, CredentialManagement: s,
		MachineLifecycle: s, UserAdministration: s, Invitations: s, BrowserAccount: s,
	}
}

// TestLivenessFloorDeniesARevokedSessionUnderAStatelessOverride: an override
// lowered to Stateless() still answers 401 on a sensitive group once the
// session is gone, while the non-sensitive groups keep the override's tier;
// relaxed, every group passes until the token expires.
func TestLivenessFloorDeniesARevokedSessionUnderAStatelessOverride(t *testing.T) {
	h := newCredentialHarness(t)
	enforced := statelessOverrides(h.svc).withDefaults(h.svc, false)
	relaxed := statelessOverrides(h.svc).withDefaults(h.svc, true)

	for name, group := range sensitiveGroups {
		if rec, _ := serveGuarded(h, group(enforced), h.credentialRequest(h.accessJWT, "")); rec.Code != http.StatusOK {
			t.Fatalf("%s with a live session = %d, want 200", name, rec.Code)
		}
	}
	if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
		t.Fatal(err)
	}
	for name, group := range sensitiveGroups {
		if rec, got := serveGuarded(h, group(enforced), h.credentialRequest(h.accessJWT, "")); rec.Code != http.StatusUnauthorized || got.reached {
			t.Errorf("%s floored, revoked session = %d reached=%v, want 401", name, rec.Code, got.reached)
		}
		if rec, _ := serveGuarded(h, group(relaxed), h.credentialRequest(h.accessJWT, "")); rec.Code != http.StatusOK {
			t.Errorf("%s relaxed, revoked session = %d, want 200 until the token expires", name, rec.Code)
		}
	}
	for name, mw := range map[string]web.Middleware{
		"OAuthLinkStart": enforced.OAuthLinkStart, "CSRFBootstrap": enforced.CSRFBootstrap, "Invitations": enforced.Invitations,
	} {
		if rec, _ := serveGuarded(h, mw, h.credentialRequest(h.accessJWT, "")); rec.Code != http.StatusOK {
			t.Errorf("%s keeps the Stateless() floor; revoked session = %d, want 200", name, rec.Code)
		}
	}
	// BrowserAccount is cookie-only by default; under the override it keeps the
	// Stateless() floor on the cookie transport too.
	if rec, _ := serveGuarded(h, enforced.BrowserAccount, h.credentialRequest("", h.accessJWT)); rec.Code != http.StatusOK {
		t.Errorf("BrowserAccount keeps the Stateless() floor; revoked session by cookie = %d, want 200", rec.Code)
	}
}

// TestLivenessFloorSessionReads: the default postures reuse their own Live()
// read (one per call); a Stateless() override now pays the floor's one read;
// relaxed, it pays none.
func TestLivenessFloorSessionReads(t *testing.T) {
	h := newCredentialHarness(t)
	for _, tc := range []struct {
		name    string
		routes  RouteAuthentication
		relaxed bool
		reads   int
	}{
		{"default posture", RouteAuthentication{}, false, 1},
		{"stateless override", RouteAuthentication{SessionHydration: h.svc.RequirePrincipal(Stateless())}, false, 1},
		{"stateless override, relaxed", RouteAuthentication{SessionHydration: h.svc.RequirePrincipal(Stateless())}, true, 0},
	} {
		before := h.sessionGets()
		rec, _ := serveGuarded(h, tc.routes.withDefaults(h.svc, tc.relaxed).SessionHydration, h.credentialRequest(h.accessJWT, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d, want 200", tc.name, rec.Code)
		}
		if got := h.sessionGets() - before; got != tc.reads {
			t.Errorf("%s session reads = %d, want %d", tc.name, got, tc.reads)
		}
	}
}

// TestLivenessFloorExternalFailOpenOutage: an external token under
// RevocationFailOpen passes a LiveOnMutation override's mutation during a
// provider outage, but the Live() floor re-checks and fails closed (503);
// relaxed, the fail-open pass stands.
func TestLivenessFloorExternalFailOpenOutage(t *testing.T) {
	h := newExternalHarness(t, externalSetup{failOpen: true})
	h.revocation.set(sdkauth.RevocationFacts{}, sdkauth.ErrProviderUnavailable)
	override := RouteAuthentication{SessionHydration: h.svc.RequirePrincipal(LiveOnMutation())}

	if rec, _ := h.serve(h.svc.RequirePrincipal(LiveOnMutation()), extReq{method: http.MethodPost, bearer: h.token}); rec.Code != http.StatusOK {
		t.Fatalf("fail-open override alone, POST during the outage = %d, want 200", rec.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec, got := h.serve(override.withDefaults(h.svc, false).SessionHydration, extReq{method: method, bearer: h.token})
		if rec.Code != http.StatusServiceUnavailable || got.reached {
			t.Errorf("floored %s during the outage = %d reached=%v, want 503", method, rec.Code, got.reached)
		}
		if rec, _ := h.serve(override.withDefaults(h.svc, true).SessionHydration, extReq{method: method, bearer: h.token}); rec.Code != http.StatusOK {
			t.Errorf("relaxed %s during the outage = %d, want 200", method, rec.Code)
		}
	}
}

// TestLivenessFloorMountedMe drives GET /auth/me through mount: an override
// lowered to PrincipalStrategy()'s LiveOnMutation answers 401 at once after the
// session is revoked, and 200 with the floor relaxed.
func TestLivenessFloorMountedMe(t *testing.T) {
	for _, tc := range []struct {
		relaxed bool
		want    int
	}{{false, http.StatusUnauthorized}, {true, http.StatusOK}} {
		h := newCredentialHarness(t)
		mux := web.NewWebHandler()
		mount(mux, mountDeps{
			Auth: h.svc, ListStrategy: list.StrategyCursor, MachineGate: allowMachineGate,
			RouteAuth:            RouteAuthentication{SessionHydration: h.svc.RequirePrincipal()},
			LivenessFloorRelaxed: tc.relaxed,
		})
		if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, bearerRequest("/auth/me", h.accessJWT))
		if rec.Code != tc.want {
			t.Errorf("relaxed=%v GET /auth/me after revocation = %d, want %d; body=%s", tc.relaxed, rec.Code, tc.want, rec.Body)
		}
	}
}

// TestWithLivenessFloorRelaxedReachesTheRoutes: the option lands on the
// mounted routes; the default keeps the floor.
func TestWithLivenessFloorRelaxedReachesTheRoutes(t *testing.T) {
	h := newCredentialHarness(t)
	for _, relaxed := range []bool{false, true} {
		var opts []Option
		if relaxed {
			opts = append(opts, WithLivenessFloorRelaxed(true))
		}
		adapter, err := New(h.svc.Service, environment.ModeDevelopment, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if adapter.routes.LivenessFloorRelaxed != relaxed {
			t.Errorf("LivenessFloorRelaxed = %v, want %v", adapter.routes.LivenessFloorRelaxed, relaxed)
		}
	}
}
