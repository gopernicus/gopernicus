package ssebridge_test

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/bridge/events/ssebridge"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/memorybus"
	"github.com/gopernicus/gopernicus/sdk/web"
	"github.com/gopernicus/gopernicus/workshop/testing/testauth"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newBus(t *testing.T) events.Bus {
	t.Helper()
	bus := memorybus.New(quietLog())
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	return bus
}

func emit(t *testing.T, bus events.Bus, eventType, tenant, aggType, aggID string) {
	t.Helper()
	base := events.NewBaseEvent(eventType)
	if tenant != "" {
		base = base.WithTenant(tenant)
	}
	if aggType != "" {
		base = base.WithAggregate(aggType, aggID)
	}
	if err := bus.Emit(context.Background(), base, events.WithSync()); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

// serve mounts the bridge over a real authenticated stack and returns the
// server URL plus a valid bearer token.
func serve(t *testing.T, hub *ssebridge.Hub) (string, string) {
	t.Helper()
	authenticator, signer := testauth.Authenticator("ssetest")
	schema := authorization.NewSchema([]authorization.ResourceSchema{{
		Name: "space",
		Def: authorization.ResourceTypeDef{
			Relations:   map[string]authorization.RelationDef{"owner": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]authorization.PermissionRule{"read": authorization.AnyOf(authorization.Direct("owner"))},
		},
	}})
	authorizer, store := testauth.AuthorizerWithStore(schema)
	store.Seed("space", "s1", "owner", "user", "sse-user")

	bridge := ssebridge.New(quietLog(), hub, authenticator, authorizer, nil)
	handler := web.NewWebHandler()
	bridge.AddHttpRoutes(handler.Group("/events"))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv.URL, testauth.MintAccessToken(signer, "sse-user")
}

// openStream connects and returns a line scanner over the SSE body.
func openStream(t *testing.T, url, token string) (*bufio.Scanner, func()) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != 200 {
		body := make([]byte, 256)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		t.Fatalf("connect status %d: %s", resp.StatusCode, body[:n])
	}
	return bufio.NewScanner(resp.Body), func() { resp.Body.Close() }
}

// expectEvent reads frames until a data line arrives (or times out).
func expectEvent(t *testing.T, sc *bufio.Scanner, wantSub string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	got := make(chan string, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				got <- line
				return
			}
		}
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, wantSub) {
			t.Fatalf("event %q does not contain %q", line, wantSub)
		}
	case <-deadline:
		t.Fatal("no event within deadline")
	}
}

func TestStreamReceivesProjectedEvent(t *testing.T) {
	bus := newBus(t)
	hub, err := ssebridge.NewHub(bus, quietLog(), ssebridge.WithHeartbeat(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	url, token := serve(t, hub)

	sc, closeBody := openStream(t, url+"/events", token)
	defer closeBody()

	waitConns(t, hub, 1)
	emit(t, bus, "note.created", "", "note", "n1")

	// Default projection: metadata only, never the payload.
	expectEvent(t, sc, `"type":"note.created"`)
}

func TestAnonymousStreamRejected(t *testing.T) {
	bus := newBus(t)
	hub, err := ssebridge.NewHub(bus, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	url, _ := serve(t, hub)

	resp, err := http.Get(url + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous connect = %d, want 401", resp.StatusCode)
	}
}

func TestResourceStreamAuthorizesAndFilters(t *testing.T) {
	bus := newBus(t)
	hub, err := ssebridge.NewHub(bus, quietLog(), ssebridge.WithHeartbeat(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	url, token := serve(t, hub)

	// Authorized resource (seeded owner rel on space:s1).
	sc, closeBody := openStream(t, url+"/events/space/s1", token)
	defer closeBody()
	waitConns(t, hub, 1)

	// Event for ANOTHER aggregate must not arrive; the matching one must.
	emit(t, bus, "space.updated", "", "space", "other")
	emit(t, bus, "space.updated", "", "space", "s1")
	expectEvent(t, sc, `"aggregate_id":"s1"`)

	// Unauthorized resource: connect-time 403.
	req, _ := http.NewRequest("GET", url+"/events/space/forbidden", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("unauthorized resource connect = %d, want 403", resp.StatusCode)
	}
}

func TestTypesFilter(t *testing.T) {
	bus := newBus(t)
	hub, err := ssebridge.NewHub(bus, quietLog(), ssebridge.WithHeartbeat(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	url, token := serve(t, hub)

	sc, closeBody := openStream(t, url+"/events?types=note.deleted", token)
	defer closeBody()
	waitConns(t, hub, 1)

	emit(t, bus, "note.created", "", "", "")
	emit(t, bus, "note.deleted", "", "", "")
	expectEvent(t, sc, `"type":"note.deleted"`)
}

func TestDisconnectCleansUp(t *testing.T) {
	bus := newBus(t)
	hub, err := ssebridge.NewHub(bus, quietLog(), ssebridge.WithHeartbeat(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	url, token := serve(t, hub)

	_, closeBody := openStream(t, url+"/events", token)
	waitConns(t, hub, 1)
	closeBody()

	deadline := time.After(5 * time.Second)
	for hub.ConnCount() != 0 {
		select {
		case <-deadline:
			t.Fatalf("conn not cleaned up: %d", hub.ConnCount())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func waitConns(t *testing.T, hub *ssebridge.Hub, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for hub.ConnCount() != want {
		select {
		case <-deadline:
			t.Fatalf("conns = %d, want %d", hub.ConnCount(), want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
