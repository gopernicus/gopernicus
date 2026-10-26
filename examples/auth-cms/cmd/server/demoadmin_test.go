package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestDemoAdminEmailGrantsPlatformAdmin boots the run() wiring with
// AUTH_DEMO_ADMIN_EMAIL set (in a non-canonical case, so the normalization is
// exercised): the account that registers and verifies that address reads both
// audit routes (the security-event listing after a TOTP step-up), the grant is an audited "demo-admin" fact, and another account
// is refused.
func TestDemoAdminEmailGrantsPlatformAdmin(t *testing.T) {
	t.Setenv("AUTH_DEMO_ADMIN_EMAIL", " Owner@Example.COM ")
	email, err := demoAdminEmail(environment.ModeDevelopment, quietLog())
	if err != nil || email != "owner@example.com" {
		t.Fatalf("demoAdminEmail = %q, %v; want owner@example.com", email, err)
	}

	gate := &deferredMiddleware{}
	securityEvents := &deferredRecorder{}
	comps, err := newAuthorization(gate.middleware, securityEvents, environment.ModeDevelopment, quietLog())
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
	cfg.UserAdminCheck = access.New(comps.Decisions).UserAdmin
	cfg.SecurityEventsRoute = true
	repos := withDemoAdmin(authmem.New().Repositories(), comps.Mutations, email, quietLog())
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatal(err)
	}
	securityEvents.set(svc.SecurityEvents)
	router := web.NewWebHandler()
	mount := pockets.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))}
	if err := comps.Register(mount); err != nil {
		t.Fatal(err)
	}
	if err := svc.HTTP.Register(mount); err != nil {
		t.Fatal(err)
	}
	gate.set(roleAdministrationGate(
		svc.HTTP.RequireAccessTokenLive(),
		svc.HTTP.RequireBrowserSafeMutation(),
		comps.HTTP.Require(authorizationhttp.Can("admin", authorizationhttp.Fixed(platformResourceType, platformResourceID))),
	))
	if err := seedAuthorization(context.Background(), comps.Mutations); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runDelivery(t, svc))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}

	owner := host.signUp("owner@example.com")
	other := host.signUp("other@example.com")
	ownerID := owner.userIDFor()

	resp, body := owner.do("GET", "/authorization/audit?subject_type=user&subject_id="+ownerID, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner GET /authorization/audit = %d %s, want 200", resp.StatusCode, body)
	}
	var page struct {
		Items []struct {
			Source struct {
				System string `json:"system"`
			} `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 || page.Items[0].Source.System != "demo-admin" {
		t.Fatalf("owner's audit history = %s (err %v), want exactly one demo-admin grant", body, err)
	}
	// The user-administration routes default to AAL2 for admins; a TOTP step-up reaches it.
	secret, _ := owner.enrollTOTP()
	if resp, body := owner.mutate("/auth/step-up/totp", `{"code":"`+totpCode(t, secret, 1)+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("owner TOTP step-up = %d %s", resp.StatusCode, body)
	}
	if resp, body := owner.do("GET", "/auth/admin/security-events?event_type=audit_read", "", nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "audit_read") {
		t.Fatalf("owner GET /auth/admin/security-events = %d %s, want 200 listing the audit read", resp.StatusCode, body)
	}
	if resp, body := other.do("GET", "/authorization/audit", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other GET /authorization/audit = %d %s, want 403", resp.StatusCode, body)
	}
	if resp, body := other.do("GET", "/auth/admin/security-events", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other GET /auth/admin/security-events = %d %s, want 403", resp.StatusCode, body)
	}
}

// TestDemoAdminEmailRefusedInProduction proves run() refuses the variable in
// production before anything is built, and that unset is off in every mode.
func TestDemoAdminEmailRefusedInProduction(t *testing.T) {
	t.Setenv("AUTH_DEMO_ADMIN_EMAIL", "owner@example.com")
	err := run(context.Background(), quietLog(), environment.ModeProduction)
	if !errors.Is(err, errDemoAdminOutsideDevelopment) || !strings.Contains(err.Error(), "AUTH_DEMO_ADMIN_EMAIL") {
		t.Fatalf("run(production, AUTH_DEMO_ADMIN_EMAIL) = %v, want errDemoAdminOutsideDevelopment", err)
	}
	t.Setenv("AUTH_DEMO_ADMIN_EMAIL", "")
	if email, err := demoAdminEmail(environment.ModeProduction, quietLog()); email != "" || err != nil {
		t.Errorf("production without the variable = %q, %v; want off", email, err)
	}
}
