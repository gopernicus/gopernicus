package eventshttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestDirectAdapterConstructionAndCustomRoutes(t *testing.T) {
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	unconfigured, err := streams.New(bus)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unconfigured.Close() })
	if _, err := New(unconfigured); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing visibility: %v", err)
	}
	if _, err := New(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing service: %v", err)
	}
	service, err := streams.New(bus, streams.WithVisibility(func(context.Context, sdk.Principal, sdkevents.Event) (bool, error) { return true, nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	var middlewareCalls int
	middleware := []web.Middleware{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { middlewareCalls++; next.ServeHTTP(w, r) })
	}}
	adapter, err := New(service, WithMiddleware(middleware...))
	if err != nil {
		t.Fatal(err)
	}
	middleware[0] = nil
	if adapter.heartbeat != defaultHeartbeat || adapter.maxConnAge != defaultMaxConnAge {
		t.Fatal("direct construction omitted stream defaults")
	}
	if _, err := adapter.ResourceStream(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing resource gate: %v", err)
	}
	response := httptest.NewRecorder()
	adapter.SubjectStream().ServeHTTP(response, httptest.NewRequest("GET", "/custom/feed", nil))
	if response.Code != http.StatusUnauthorized || middlewareCalls != 1 {
		t.Fatalf("status=%d middleware=%d", response.Code, middlewareCalls)
	}
	if _, err := New(service, WithMiddleware([]web.Middleware{nil}...)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil middleware: %v", err)
	}
	_ = service.Close()
	if _, err := New(service); !errors.Is(err, sdkevents.ErrClosed) {
		t.Fatalf("closed service: %v", err)
	}
}

func TestCustomResourceHandlerRequiresPathIdentity(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	service, err := streams.New(bus, streams.WithVisibility(func(context.Context, sdk.Principal, sdkevents.Event) (bool, error) { return true, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authorizations := 0
	adapter, err := New(service, WithAuthorization(func(context.Context, sdk.Principal, string, string) (bool, error) { authorizations++; return true, nil }), WithMiddleware([]web.Middleware{stashIdentity(sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u"})}...))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := adapter.ResourceStream()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/custom/resource", nil))
	if response.Code != http.StatusBadRequest || authorizations != 0 {
		t.Fatalf("status=%d authorizations=%d", response.Code, authorizations)
	}
}
