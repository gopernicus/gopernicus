package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/production-baseline/integrations/authentication/devissuer"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// recordedEvents keeps every row the security-event rail writes.
type recordedEvents struct {
	securityevent.SecurityEventRepository
	mu   sync.Mutex
	rows []securityevent.SecurityEvent
}

func (r *recordedEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, evt)
	return evt, nil
}

func (r *recordedEvents) ofType(eventType string) []securityevent.SecurityEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []securityevent.SecurityEvent
	for _, row := range r.rows {
		if row.EventType == eventType {
			out = append(out, row)
		}
	}
	return out
}

// TestHostRouteDenialCarriesClientAttribution serves run's router and pockets
// in development (TestRoleRoutesGateOrder's setup) over a recording audit rail
// and proves the role routes — host routes gated by the authorization pocket —
// record their denial with the client: the non-admin's 403 writes one
// authorization_denied row carrying the peer address, the request's
// User-Agent and its request id.
func TestHostRouteDenialCarriesClientAttribution(t *testing.T) {
	for name := range productionKeys {
		t.Setenv(string(name), "")
	}
	t.Setenv("IDP_ISSUER", "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	log := quietLog()
	keys, err := loadKeys(environment.ModeDevelopment, log)
	if err != nil {
		t.Fatalf("loadKeys: %v", err)
	}
	router := newRouter(0, log)
	provider, err := newProvider(context.Background(), router, origin, environment.ModeDevelopment, log)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	events := &recordedEvents{}
	repos := identityRepositories()
	repos.SecurityEvents = events
	if _, err := mount(router, composition{
		mode:                environment.ModeDevelopment,
		log:                 log,
		origin:              origin,
		keys:                keys,
		sessionCookie:       authenticationhttp.CookieConfig{Secure: true},
		limiter:             sharedLimiter{},
		authenticationRepos: repos,
		authorizationRepos:  authorizationRepositories(true),
		provider:            provider,
	}); err != nil {
		t.Fatalf("mount: %v", err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	status, raw := send(t, http.MethodPost, origin+devissuer.TokenPath, `{"sub":"`+memberSubject+`","email":"a@x.test","email_verified":true,"amr":["pwd","mfa"]}`, nil)
	var minted struct{ Token string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &minted) != nil || minted.Token == "" {
		t.Fatalf("mint: %d %s", status, raw)
	}

	const requestID, agent = "baseline-denied-request", "probe/1.0"
	req, err := http.NewRequest(http.MethodPost, origin+"/authorization/roles",
		strings.NewReader(`{"subject_type":"user","subject_id":"user-2","role":"admin","scope":{"kind":"global"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+minted.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", agent)
	req.Header.Set(web.RequestIDHeader, requestID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bearer POST by a non-admin = %d, want 403", resp.StatusCode)
	}

	denials := events.ofType(decisions.EventAuthorizationDenied)
	if len(denials) != 1 {
		t.Fatalf("authorization_denied rows = %d, want 1: %+v", len(denials), denials)
	}
	if row := denials[0]; row.IPAddress != "127.0.0.1" || row.UserAgent != agent || row.RequestID != requestID || row.UserID != memberID {
		t.Fatalf("denial row ip/ua/request/user = %q/%q/%q/%q, want 127.0.0.1/%s/%s/%s",
			row.IPAddress, row.UserAgent, row.RequestID, row.UserID, agent, requestID, memberID)
	}
}

// TestAuditWriteObserverThrottles proves the host's audit-write observer logs
// the first lost row with its fields, only counts the rest of the minute, then
// logs one count line, and starts over with the fields after a quiet minute.
func TestAuditWriteObserverThrottles(t *testing.T) {
	var logs bytes.Buffer
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	observer := &auditWriteLog{log: slog.New(slog.NewTextHandler(&logs, nil)), now: func() time.Time { return now }}
	failure := securityevent.WriteFailure{EventType: decisions.EventAuthorizationDenied, EventStatus: securityevent.StatusFailure, ErrorKind: "unknown"}
	lines := func() []string {
		out := strings.TrimSpace(logs.String())
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}

	for range 5 {
		observer.observe(context.Background(), failure)
		now = now.Add(time.Second)
	}
	got := lines()
	if len(got) != 1 || !strings.Contains(got[0], "level=ERROR") || !strings.Contains(got[0], "event_type=authorization_denied") || !strings.Contains(got[0], "error_kind=unknown") {
		t.Fatalf("five failures in a minute logged %q, want one detailed ERROR line", got)
	}
	now = now.Add(time.Minute)
	observer.observe(context.Background(), failure)
	got = lines()
	if len(got) != 2 || !strings.Contains(got[1], "count=5") || strings.Contains(got[1], "event_type") {
		t.Fatalf("the minute's summary = %q, want one count=5 line", got)
	}
	now = now.Add(2 * time.Minute)
	observer.observe(context.Background(), failure)
	got = lines()
	if len(got) != 3 || !strings.Contains(got[2], "event_type=authorization_denied") {
		t.Fatalf("a failure after a quiet minute = %q, want a new detailed line", got)
	}
}
