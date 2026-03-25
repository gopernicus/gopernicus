package memorybus_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/eventstest"
	"github.com/gopernicus/gopernicus/infrastructure/events/memorybus"
)

func newTestBus() *memorybus.Bus {
	return memorybus.New(slog.Default())
}

func TestCompliance(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())
	eventstest.RunSuite(t, bus)
}

// testEvent is a simple event for testing.
type testEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

// =============================================================================
// Sync Emit
// =============================================================================

func TestEmitSync_CallsHandler(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	var received string
	bus.Subscribe("test.event", func(_ context.Context, e events.Event) error {
		if te, ok := e.(testEvent); ok {
			received = te.Data
		}
		return nil
	})

	evt := testEvent{
		BaseEvent: events.NewBaseEventWithCorrelation("test.event", "corr-1"),
		Data:      "hello",
	}

	err := bus.Emit(context.Background(), evt, events.WithSync())
	if err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if received != "hello" {
		t.Errorf("received = %q, want %q", received, "hello")
	}
}

func TestEmitSync_PropagatesError(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	bus.Subscribe("test.event", func(_ context.Context, _ events.Event) error {
		return errors.New("handler failed")
	})

	evt := testEvent{BaseEvent: events.NewBaseEvent("test.event")}
	err := bus.Emit(context.Background(), evt, events.WithSync())
	if err == nil {
		t.Error("sync Emit should propagate handler error")
	}
}

func TestEmitSync_MultipleHandlers(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	var count int32
	handler := func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	}

	bus.Subscribe("test.event", handler)
	bus.Subscribe("test.event", handler)

	evt := testEvent{BaseEvent: events.NewBaseEvent("test.event")}
	bus.Emit(context.Background(), evt, events.WithSync())

	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("handler called %d times, want 2", count)
	}
}

// =============================================================================
// Async Emit
// =============================================================================

func TestEmitAsync_CallsHandler(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	done := make(chan string, 1)
	bus.Subscribe("test.event", func(_ context.Context, e events.Event) error {
		if te, ok := e.(testEvent); ok {
			done <- te.Data
		}
		return nil
	})

	evt := testEvent{
		BaseEvent: events.NewBaseEvent("test.event"),
		Data:      "async-hello",
	}
	bus.Emit(context.Background(), evt) // Default is async.

	select {
	case received := <-done:
		if received != "async-hello" {
			t.Errorf("received = %q, want %q", received, "async-hello")
		}
	case <-time.After(2 * time.Second):
		t.Error("async handler was not called within timeout")
	}
}

// =============================================================================
// Wildcard Subscription
// =============================================================================

func TestWildcardSubscription(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	var count int32
	bus.Subscribe("*", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("user.created")}, events.WithSync())
	bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("order.completed")}, events.WithSync())

	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("wildcard handler called %d times, want 2", count)
	}
}

// =============================================================================
// Unsubscribe
// =============================================================================

func TestUnsubscribe(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	var count int32
	sub, _ := bus.Subscribe("test.event", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	evt := testEvent{BaseEvent: events.NewBaseEvent("test.event")}
	bus.Emit(context.Background(), evt, events.WithSync())

	sub.Unsubscribe()

	bus.Emit(context.Background(), evt, events.WithSync())

	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times after unsubscribe, want 1", count)
	}
}

func TestUnsubscribe_Idempotent(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	sub, _ := bus.Subscribe("test", func(_ context.Context, _ events.Event) error {
		return nil
	})

	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe() error = %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe() error = %v", err)
	}
}

// =============================================================================
// No Handlers
// =============================================================================

func TestEmit_NoHandlers(t *testing.T) {
	bus := newTestBus()
	defer bus.Close(context.Background())

	// Should not error or panic when no handlers registered.
	evt := testEvent{BaseEvent: events.NewBaseEvent("unknown.event")}
	err := bus.Emit(context.Background(), evt, events.WithSync())
	if err != nil {
		t.Fatalf("Emit() with no handlers should not error, got: %v", err)
	}
}

// =============================================================================
// Close
// =============================================================================

func TestClose_Graceful(t *testing.T) {
	bus := newTestBus()

	err := bus.Close(context.Background())
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	bus := newTestBus()

	bus.Close(context.Background())
	err := bus.Close(context.Background())
	if err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestClose_DropsNewEvents(t *testing.T) {
	bus := newTestBus()
	bus.Close(context.Background())

	// Emit after close should not panic.
	evt := testEvent{BaseEvent: events.NewBaseEvent("test")}
	err := bus.Emit(context.Background(), evt)
	if err != nil {
		t.Fatalf("Emit() after Close should not error, got: %v", err)
	}
}
