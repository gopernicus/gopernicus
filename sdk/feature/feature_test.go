package feature

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Compile-time interface satisfaction assertion: web.WebHandler is the
// concrete router every host wires into a Mount, and it must keep satisfying
// RouteRegistrar structurally (constitution rule 3's seam) without either
// package importing the other's concrete type. If this line stops compiling,
// the seam has drifted.
var _ RouteRegistrar = (*web.WebHandler)(nil)

// recordingRegistrar is a fake RouteRegistrar that records Handle calls.
type recordingRegistrar struct {
	calls []string
}

func (r *recordingRegistrar) Handle(method, path string, _ http.HandlerFunc, _ ...web.Middleware) {
	r.calls = append(r.calls, method+" "+path)
}

// registerThroughMount is the shape a feature's Register function takes: it
// reaches only the Mount's narrow ports, never a service locator.
func registerThroughMount(m Mount) error {
	m.Router.Handle(http.MethodGet, "/widgets", func(w http.ResponseWriter, r *http.Request) {})
	return nil
}

func TestMount_RegisterHitsRouter(t *testing.T) {
	router := &recordingRegistrar{}
	m := Mount{Router: router, Logger: slog.Default()}

	if err := registerThroughMount(m); err != nil {
		t.Fatalf("registerThroughMount() error = %v", err)
	}

	if len(router.calls) != 1 || router.calls[0] != "GET /widgets" {
		t.Errorf("router.calls = %v, want [\"GET /widgets\"]", router.calls)
	}
}

func TestMount_ZeroValueFieldsAreNilable(t *testing.T) {
	// A Mount with only a router must be constructible; Logger and Events are
	// optional (a nil Events means the feature emits nothing).
	m := Mount{Router: &recordingRegistrar{}, Logger: slog.Default()}
	if m.Router == nil {
		t.Fatal("Router should be set")
	}
	if m.Events != nil {
		t.Fatal("Events should be nil on a zero-value construction")
	}
}

func TestMount_EventsDeliversToSubscriber(t *testing.T) {
	bus := events.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	got := make(chan events.Event, 1)
	if _, err := bus.Subscribe("*", func(_ context.Context, e events.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	m := Mount{Router: &recordingRegistrar{}, Logger: slog.Default(), Events: bus}

	want := events.NewBaseEvent("widget.created")
	if err := m.Events.Emit(context.Background(), want, events.WithSync()); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	select {
	case e := <-got:
		if e.Type() != "widget.created" {
			t.Errorf("delivered event Type() = %q, want %q", e.Type(), "widget.created")
		}
	default:
		t.Fatal("WithSync emit did not deliver to the subscriber")
	}
}
