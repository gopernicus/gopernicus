package events_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/events"
	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type subscriptionCounter struct {
	sdkevents.Bus
	subscriptions int
}

func (b *subscriptionCounter) Subscribe(topic string, handler sdkevents.Handler) (sdkevents.Subscription, error) {
	b.subscriptions++
	return b.Bus.Subscribe(topic, handler)
}

func TestNilOptionsDoNotAcquireSubscriptions(t *testing.T) {
	bus := &subscriptionCounter{Bus: sdkevents.Noop{}}
	if _, err := events.New(bus, events.WithVisibility(allowEvents), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("root nil option: %v", err)
	}
	if _, err := streams.New(bus, streams.WithVisibility(allowEvents), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("stream nil option: %v", err)
	}
	if bus.subscriptions != 0 {
		t.Fatalf("failed constructors subscribed %d times", bus.subscriptions)
	}
	service, err := events.New(bus, events.WithVisibility(allowEvents))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for range 2 {
		if _, err := service.HTTP(); err != nil {
			t.Fatal(err)
		}
	}
	if bus.subscriptions != 1 {
		t.Fatalf("assembly and repeated HTTP subscribed %d times", bus.subscriptions)
	}
}

func TestOptionsSnapshotMiddlewareBeforeConstructionAndCanClearIt(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	hits := 0
	middleware := []web.Middleware{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; next.ServeHTTP(w, r) })
	}}
	captured := events.WithStreamMiddleware(middleware...)
	middleware[0] = nil
	for range 2 {
		service, err := events.New(bus, events.WithVisibility(allowEvents), captured)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		for range 2 {
			adapter, err := service.HTTP()
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			adapter.SubjectStream().ServeHTTP(response, httptest.NewRequest("GET", "/events", nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("missing identity status=%d", response.Code)
			}
		}
	}
	if hits != 4 {
		t.Fatalf("reused captured middleware ran %d times", hits)
	}
	service, err := events.New(bus, events.WithVisibility(allowEvents), captured, events.WithStreamMiddleware(),
		events.WithAuthorization(func(context.Context, sdk.Principal, string, string) (bool, error) { return true, nil }), events.WithAuthorization(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	adapter, err := service.HTTP()
	if err != nil {
		t.Fatal(err)
	}
	adapter.SubjectStream().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/events", nil))
	if hits != 4 {
		t.Fatal("empty final middleware option did not clear the group")
	}
	if _, err := adapter.ResourceStream(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("cleared resource gate: %v", err)
	}
}
