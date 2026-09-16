package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestNoSessionOnlyAuthorizationMutationRoute(t *testing.T) {
	authCfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	// in_process delivery owns its bounded pool and needs no dispatcher — enough to
	// construct a real Service for the route-registration surface under test.
	authCfg.DeliveryMode = delivery.ModeInProcess
	authCfg.DeliveryEphemeralAcknowledged = true
	authSvc, err := auth.New(authmem.New().Repositories(), authCfg.TokenSigner, authCfg.RuntimeMode, authCfg.DeliveryMode, authCfg.options()...)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	authorizer := hostAuthz(t)

	router := web.NewWebHandler()
	registerDemoRoutes(router, authSvc.HTTP, authorizer.Decisions, authorizer.Roles, authorizer.HTTP)

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
