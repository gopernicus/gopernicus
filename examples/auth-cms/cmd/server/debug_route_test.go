package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/oauth2fixture"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestDebugSecurityEventsRequiresPlatformAdmin drives GET /debug/security-events
// over real HTTP sessions with AUTH_DEBUG=1: no session is 401, a signed-in
// non-admin is 403, and a platform admin reads the rail — including the
// non-admin's denial, which the decision bridge recorded with the request id,
// IP and User-Agent that host-route request carried (run's ClientInfoMiddleware
// Use), and the OAuth2 demo's confidential-client mismatches: client_id only
// for the configured client, IP and User-Agent, no secret.
func TestDebugSecurityEventsRequiresPlatformAdmin(t *testing.T) {
	t.Setenv("AUTH_DEBUG", "1")
	securityEvents := &deferredRecorder{}
	comps, err := newAuthorization(nil, securityEvents, environment.ModeDevelopment, quietLog())
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
	oauthFixture, err := oauth2fixture.New("http://127.0.0.1:8082")
	if err != nil {
		t.Fatal(err)
	}
	options := append(cfg.options(), auth.WithOAuth2(auth.OAuth2Config{Server: oauthFixture.Config(), Clients: oauthFixture, Views: cfg.Views.(authenticationhttp.OAuthViews)}))
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, options...)
	if err != nil {
		t.Fatal(err)
	}
	securityEvents.set(svc.SecurityEvents)
	router := web.NewWebHandler()
	router.Use(web.TrustProxies(0), web.RequestID())
	router.Use(svc.HTTP.ClientInfoMiddleware())
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
	const deniedRequestID, deniedUA = "debug-denied-request", "debug-probe/1.0"
	if resp, body := plain.do("GET", "/debug/security-events", "", http.Header{web.RequestIDHeader: {deniedRequestID}, "User-Agent": {deniedUA}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("signed-in non-admin = %d %s, want 403", resp.StatusCode, body)
	}
	const mismatchUA = "mcp-probe/1.0"
	mismatches := []struct{ requestID, user, clientID string }{
		{"oauth2-mismatch-configured", "oauth-demo-mcp", "oauth-demo-mcp"},
		{"oauth2-mismatch-foreign", "someone-else", ""},
		{"oauth2-mismatch-nul", "oauth-demo-mcp%00", ""},
	}
	for _, m := range mismatches {
		req, err := http.NewRequest("POST", srv.URL+"/auth/oauth2/token", strings.NewReader("grant_type=urn:ietf:params:oauth:grant-type:token-exchange"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(web.RequestIDHeader, m.requestID)
		req.Header.Set("User-Agent", mismatchUA)
		req.SetBasicAuth(m.user, "wrong-secret-0451")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token with %q = %d, want 401 invalid_client", m.user, resp.StatusCode)
		}
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
		Count  int `json:"count"`
		Events []struct {
			EventType   string         `json:"event_type"`
			EventStatus string         `json:"event_status"`
			UserID      string         `json:"user_id"`
			RequestID   string         `json:"request_id"`
			Details     map[string]any `json:"details"`
			IPAddress   string         `json:"ip_address"`
			UserAgent   string         `json:"user_agent"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &dump); err != nil || dump.Count == 0 {
		t.Fatalf("admin read %s (err %v), want the rail's rows", body, err)
	}
	var denials int
	for _, e := range dump.Events {
		if e.RequestID == deniedRequestID {
			denials++
			if e.EventType != "authorization_denied" || e.UserID != plain.userIDFor() {
				t.Errorf("row for %s = %+v, want the non-admin's authorization_denied", deniedRequestID, e)
			}
			if e.IPAddress == "" || e.UserAgent != deniedUA {
				t.Errorf("row for %s ip/ua = %q/%q, want the host-route request's", deniedRequestID, e.IPAddress, e.UserAgent)
			}
		}
	}
	if denials != 1 {
		t.Errorf("rows carrying request_id %s = %d, want 1; dump=%s", deniedRequestID, denials, body)
	}
	for _, m := range mismatches {
		var rows int
		for _, e := range dump.Events {
			if e.RequestID != m.requestID {
				continue
			}
			rows++
			clientID, attributed := e.Details["client_id"]
			if e.EventType != "oauth2_grant_failed" || e.EventStatus != "failure" || e.Details["reason"] != "invalid_client" ||
				attributed != (m.clientID != "") || (attributed && clientID != m.clientID) {
				t.Errorf("row for %s = %+v, want oauth2_grant_failed invalid_client with client_id %q", m.requestID, e, m.clientID)
			}
			if e.IPAddress == "" || e.UserAgent != mismatchUA {
				t.Errorf("row for %s ip/ua = %q/%q, want the request's", m.requestID, e.IPAddress, e.UserAgent)
			}
			if _, ok := e.Details["client_secret"]; ok || strings.Contains(fmt.Sprint(e.Details), "wrong-secret-0451") {
				t.Errorf("row for %s carries the presented secret: %v", m.requestID, e.Details)
			}
		}
		if rows != 1 {
			t.Errorf("rows carrying request_id %s = %d, want 1", m.requestID, rows)
		}
	}
}

// TestDebugSecurityEventsAbsentWithoutFlag proves the default-off posture.
func TestDebugSecurityEventsAbsentWithoutFlag(t *testing.T) {
	t.Setenv("AUTH_DEBUG", "")
	comps, err := newAuthorization(nil, nil, environment.ModeDevelopment, quietLog())
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
