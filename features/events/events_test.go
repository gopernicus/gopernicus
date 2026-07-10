package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	events "github.com/gopernicus/gopernicus/features/events"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// recordingRouter records the (method, path) of every registered route so a test
// can assert deny-by-absence of the resource-scoped route.
type recordingRouter struct {
	routes []string
}

func (r *recordingRouter) Handle(method, path string, _ http.HandlerFunc, _ ...web.Middleware) {
	r.routes = append(r.routes, method+" "+path)
}

func (r *recordingRouter) has(route string) bool {
	for _, got := range r.routes {
		if got == route {
			return true
		}
	}
	return false
}

// stashIdentity is a test stand-in for authentication.RequireUser: it stashes a
// fixed Principal on the request context (the middleware side of A-I1).
func stashIdentity(p identity.Principal) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
		})
	}
}

// sseFrame is one parsed Server-Sent Event.
type sseFrame struct {
	id    string
	event string
	data  string
}

// readSSE parses SSE frames off body until it closes, sending each dispatched
// frame on out. Comment (": ping") heartbeat lines are ignored.
func readSSE(body io.Reader, out chan<- sseFrame) {
	sc := bufio.NewScanner(body)
	var cur sseFrame
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur != (sseFrame{}) {
				out <- cur
				cur = sseFrame{}
			}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// newTestServer builds a host router, mounts the events feature over it with the
// given middleware and Authorize, and returns the running server and its bus.
func newTestServer(t *testing.T, mw []web.Middleware, authorize events.AuthorizeStream) (*httptest.Server, sdkevents.Bus) {
	t.Helper()
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	svc, err := events.NewService(events.Repositories{}, events.Config{
		Bus:              bus,
		StreamMiddleware: mw,
		Authorize:        authorize,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	router := web.NewWebHandler()
	if err := svc.Register(feature.Mount{Router: router}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, bus
}

// openStream opens an SSE GET and returns a frames channel plus a cancel that
// closes the connection. It waits for the response headers so the hub connection
// is registered before the caller emits.
func openStream(t *testing.T, url string) (<-chan sseFrame, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	frames := make(chan sseFrame, 8)
	go func() {
		defer resp.Body.Close()
		readSSE(resp.Body, frames)
	}()
	return frames, cancel
}

func awaitFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an SSE frame")
		return sseFrame{}
	}
}

func TestNewService_NilBusErrors(t *testing.T) {
	_, err := events.NewService(events.Repositories{}, events.Config{})
	if err != events.ErrBusRequired {
		t.Fatalf("err = %v, want ErrBusRequired", err)
	}
}

func TestNewService_BuildsWithBus(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	if _, err := events.NewService(events.Repositories{}, events.Config{Bus: bus}); err != nil {
		t.Fatalf("NewService: %v", err)
	}
}

func TestRegister_ResourceRouteDenyByAbsence(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())

	// No Authorize → the resource-scoped route is not registered.
	svc, err := events.NewService(events.Repositories{}, events.Config{Bus: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rr := &recordingRouter{}
	if err := svc.Register(feature.Mount{Router: rr}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !rr.has("GET /events") {
		t.Fatalf("routes = %v, want GET /events", rr.routes)
	}
	if rr.has("GET /events/{resource_type}/{resource_id}") {
		t.Fatalf("resource route registered without Authorize: %v", rr.routes)
	}

	// With Authorize → the resource-scoped route is registered.
	authorize := func(context.Context, identity.Principal, string, string) (bool, error) { return true, nil }
	svc2, err := events.NewService(events.Repositories{}, events.Config{Bus: bus, Authorize: authorize})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rr2 := &recordingRouter{}
	if err := svc2.Register(feature.Mount{Router: rr2}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !rr2.has("GET /events/{resource_type}/{resource_id}") {
		t.Fatalf("resource route not registered with Authorize: %v", rr2.routes)
	}
}

func TestEndToEnd_SubjectStreamReceivesEmit(t *testing.T) {
	mw := []web.Middleware{stashIdentity(identity.Principal{Type: identity.User, ID: "u1"})}
	srv, bus := newTestServer(t, mw, nil)

	frames, cancel := openStream(t, srv.URL+"/events")
	defer cancel()

	evt := sdkevents.NewBaseEvent("content.updated").WithAggregate("entry", "e1")
	if err := bus.Emit(context.Background(), evt); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	f := awaitFrame(t, frames)
	if f.event != "content.updated" {
		t.Fatalf("frame event = %q, want content.updated", f.event)
	}
	if f.id != evt.CorrelationID() {
		t.Fatalf("frame id = %q, want correlation id %q", f.id, evt.CorrelationID())
	}
	// Metadata-only body: type + aggregate, never a raw payload.
	var body map[string]any
	if err := json.Unmarshal([]byte(f.data), &body); err != nil {
		t.Fatalf("data not JSON: %v (%q)", err, f.data)
	}
	if body["type"] != "content.updated" || body["aggregate_type"] != "entry" || body["aggregate_id"] != "e1" {
		t.Fatalf("metadata body = %v", body)
	}
}

func TestEndToEnd_NoMiddlewareFailsClosed(t *testing.T) {
	// No identity-stashing middleware → every stream 401s (A-I1 E1).
	srv, _ := newTestServer(t, nil, nil)

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
