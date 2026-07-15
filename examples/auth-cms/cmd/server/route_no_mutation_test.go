package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// TestNoSessionOnlyAuthorizationMutationRoute is the permanent regression guard for the
// AZ3-4.1 removal (checklist item 6/12): the host's demo HTTP surface ships NO session-only
// authorization-mutation route. It drives the real registerDemoRoutes registration over
// httptest and asserts the retired mutation paths are unregistered (404), while a retained
// read route answers 405 to a POST — proving the router is live, so the 404s are a real
// absence rather than a dead router. Trusted authorization writes reach the SystemMutator
// only at boot (seedAuthorization) and through the invitation Granter (membership.go), never
// an ordinary HTTP route. This complements TestHostSystemMutatorHeldApartFromService (which
// pins that the Service handed to handlers cannot yield the SystemMutator) by pinning the
// ROUTE surface itself — a future re-addition of a browser-driven mutation route regresses
// here.
func TestNoSessionOnlyAuthorizationMutationRoute(t *testing.T) {
	authCfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	// in_process delivery owns its bounded pool and needs no dispatcher — enough to
	// construct a real Service for the route-registration surface under test.
	authCfg.DeliveryMode = auth.DeliveryModeInProcess
	authCfg.DeliveryEphemeralAcknowledged = true
	authSvc, err := auth.NewService(authmem.New().Repositories(), authCfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	authorizer := hostAuthz(t).Service

	router := web.NewWebHandler(web.WithLogging(quietLog()))
	registerDemoRoutes(router, authSvc, authorizer)

	// The retired session-only authorization-mutation routes must be absent (404). A
	// shipped HTTP route must never mutate authorization with session presence alone.
	for _, path := range []string{
		"/demo/roles/assign",
		"/demo/roles/unassign",
		"/demo/admin/bootstrap",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s = %d, want 404 (no session-only authorization mutation route may ship)", path, rec.Code)
		}
	}

	// Sanity: a retained READ route is registered GET-only, so the 404s above are a real
	// absence, not a dead router. A POST to it is method-not-allowed (405), returned by the
	// mux before any handler or middleware runs.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/demo/whoami", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /demo/whoami = %d, want 405 (read route registered GET-only)", rec.Code)
	}
}
