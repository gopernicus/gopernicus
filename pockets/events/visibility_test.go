package events_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/events"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestRegisterRequiresRouterAndVisibility(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	svc, err := events.New(bus)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	router := &recordingRouter{}
	for _, mount := range []pockets.Mount{{}, {Router: router}} {
		if err := svc.Register(mount); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Register = %v", err)
		}
	}
	if len(router.routes) != 0 {
		t.Fatalf("registered without visibility: %v", router.routes)
	}
}

func TestServiceCloseAndMiddlewareSnapshot(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	var applied atomic.Int32
	middleware := []web.Middleware{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { applied.Add(1); next.ServeHTTP(w, r) })
	}}
	svc, err := events.New(bus, events.WithVisibility(allowEvents), events.WithStreamMiddleware(middleware...))
	if err != nil {
		t.Fatal(err)
	}
	middleware[0] = func(next http.Handler) http.Handler { return next }
	router := web.NewWebHandler()
	if err := svc.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatal(err)
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/events", nil))
	if applied.Load() != 1 {
		t.Fatal("caller mutation replaced gateway middleware")
	}
	for range 2 {
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Register(pockets.Mount{Router: &recordingRouter{}}); !errors.Is(err, sdkevents.ErrClosed) {
		t.Fatalf("Register after Close = %v", err)
	}
	if err := bus.Dispatch(context.Background(), sdkevents.NewBaseEvent("test.open")); err != nil {
		t.Fatalf("gateway closed host bus: %v", err)
	}
}

// Each tenant opens both stream shapes for the same aggregate ID. Only the host
// policy knows tenant membership; neither a matching resource nor a type filter
// grants visibility to another tenant's event.
func TestVisibilityFiltersBothStreamShapesBeforeProjection(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	var projected atomic.Int32
	cfg := []events.Option{events.WithVisibility(func(_ context.Context, p sdk.Principal, e sdkevents.Event) (bool, error) {
		md, ok := e.(sdkevents.Metadata)
		return ok && md.TenantID() != nil && *md.TenantID() == p.ID, nil
	}), events.WithProjector(func(e sdkevents.Event) any { projected.Add(1); return e.Type() }), events.WithAuthorization(func(context.Context, sdk.Principal, string, string) (bool, error) { return true, nil }), events.WithStreamMiddleware([]web.Middleware{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := sdk.Principal{Type: "tenant", ID: r.URL.Query().Get("tenant")}
			next.ServeHTTP(w, r.WithContext(sdk.WithPrincipal(r.Context(), principal)))
		})
	}}...)}
	svc, err := events.New(bus, cfg...)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	router := web.NewWebHandler()
	if err := svc.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	streams := make(map[string][]<-chan sseFrame)
	for _, tenant := range []string{"A", "B"} {
		for _, path := range []string{"/events", "/events/entry/shared"} {
			frames, cancel := openStream(t, server.URL+path+"?tenant="+tenant)
			defer cancel()
			streams[tenant] = append(streams[tenant], frames)
		}
	}
	for _, tenant := range []string{"B", "A"} {
		event := sdkevents.NewBaseEvent("tenant."+tenant).WithAggregate("entry", "shared").WithTenant(tenant)
		if err := bus.Dispatch(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	for tenant, connections := range streams {
		for _, frames := range connections {
			if got := awaitFrame(t, frames).event; got != "tenant."+tenant {
				t.Fatalf("tenant %s received %s", tenant, got)
			}
		}
	}
	if got := projected.Load(); got != 4 {
		t.Fatalf("projected %d events, want one allowed event per connection", got)
	}
}

type lockedLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.data.String() }

func TestVisibilityFailureIsLoggedAndSlowPolicyDoesNotBlockBus(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	logOutput := &lockedLog{}
	cfg := []events.Option{events.WithVisibility(func(ctx context.Context, _ sdk.Principal, e sdkevents.Event) (bool, error) {
		switch e.Type() {
		case "policy.slow":
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return false, nil
		case "policy.error":
			return false, errors.New("policy unavailable")
		case "policy.panic":
			panic("private panic detail")
		default:
			return true, nil
		}
	}), events.WithLogger(slog.New(slog.NewTextHandler(logOutput, nil))), events.WithStreamMiddleware([]web.Middleware{stashIdentity(sdk.Principal{Type: "user", ID: "u"})}...)}
	svc, err := events.New(bus, cfg...)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	router := web.NewWebHandler()
	if err := svc.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	slow, cancelSlow := openStream(t, server.URL+"/events?types=policy.slow")
	_ = slow
	defer cancelSlow()
	fast, cancelFast := openStream(t, server.URL+"/events?types=policy.error,policy.panic,policy.allowed")
	defer cancelFast()
	dispatched := make(chan error, 1)
	go func() { dispatched <- bus.Dispatch(context.Background(), sdkevents.NewBaseEvent("policy.slow")) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow policy did not start")
	}
	select {
	case err := <-dispatched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("policy blocked bus dispatch")
	}
	for _, kind := range []string{"policy.error", "policy.panic", "policy.allowed"} {
		if err := bus.Dispatch(context.Background(), sdkevents.NewBaseEvent(kind)); err != nil {
			t.Fatal(err)
		}
	}
	if got := awaitFrame(t, fast).event; got != "policy.allowed" {
		t.Fatalf("failure leaked event %s", got)
	}
	logs := logOutput.String()
	if !strings.Contains(logs, "policy unavailable") || !strings.Contains(logs, "panicked") || strings.Contains(logs, "private panic detail") {
		t.Fatalf("policy diagnostics: %s", logs)
	}
}
