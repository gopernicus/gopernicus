package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestDebugSecurityEventsRequiresPlatformAdmin drives GET /debug/security-events
// over real HTTP sessions with AUTH_DEBUG=1: no session is 401, a signed-in
// non-admin is 403, and a platform admin reads the rail.
func TestDebugSecurityEventsRequiresPlatformAdmin(t *testing.T) {
	t.Setenv("AUTH_DEBUG", "1")
	comps, err := newAuthorization(nil, environment.ModeDevelopment, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	repos := authmem.New().Repositories()
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	if err := svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))}); err != nil {
		t.Fatal(err)
	}
	registerDebugRoutes(router, svc.HTTP, comps.HTTP, repos, quietLog())
	t.Cleanup(runDelivery(t, svc))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}

	if resp, body := host.newClient().do("GET", "/debug/security-events", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no session = %d %s, want 401", resp.StatusCode, body)
	}
	plain := host.signUp("debug-plain@example.com")
	if resp, body := plain.do("GET", "/debug/security-events", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("signed-in non-admin = %d %s, want 403", resp.StatusCode, body)
	}
	admin := host.signUp("debug-admin@example.com")
	if _, err := comps.Mutations.GrantRelationship(trustedContext(), mutations.GrantRelationshipCommand{
		ResourceType: platformResourceType,
		ResourceID:   platformResourceID,
		Relation:     "admin",
		Subject:      relationships.SubjectRef{Type: "user", ID: admin.userIDFor()},
	}); err != nil {
		t.Fatal(err)
	}
	resp, body := admin.do("GET", "/debug/security-events", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("platform admin = %d %s, want 200", resp.StatusCode, body)
	}
	var dump struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &dump); err != nil || dump.Count == 0 {
		t.Errorf("admin read %s (err %v), want the rail's rows", body, err)
	}
}

// TestDebugSecurityEventsAbsentWithoutFlag proves the default-off posture.
func TestDebugSecurityEventsAbsentWithoutFlag(t *testing.T) {
	t.Setenv("AUTH_DEBUG", "")
	comps, err := newAuthorization(nil, environment.ModeDevelopment, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	svc := bootInProcess(t, &recordingSender{}, nil)
	router := web.NewWebHandler()
	registerDebugRoutes(router, svc.HTTP, comps.HTTP, authmem.New().Repositories(), quietLog())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/security-events", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("AUTH_DEBUG unset = %d, want 404", rec.Code)
	}
}
