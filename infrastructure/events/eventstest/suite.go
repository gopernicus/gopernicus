// Package eventstest provides compliance tests for events.Bus implementations.
//
// Example:
//
//	func TestCompliance(t *testing.T) {
//	    bus := memorybus.New(slog.Default())
//	    defer bus.Close(context.Background())
//	    eventstest.RunSuite(t, bus)
//	}
package eventstest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// testEvent is a simple event for testing.
type testEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

// RunSuite runs the standard compliance tests against any Bus implementation.
func RunSuite(t *testing.T, bus events.Bus) {
	t.Helper()

	t.Run("EmitAndSubscribe", func(t *testing.T) {
		var received atomic.Int32
		_, err := bus.Subscribe("test.event", func(ctx context.Context, e events.Event) error {
			received.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}

		evt := testEvent{
			BaseEvent: events.NewBaseEvent("test.event"),
			Data:      "hello",
		}
		if err := bus.Emit(context.Background(), evt, events.WithSync()); err != nil {
			t.Fatalf("Emit: %v", err)
		}

		// Allow async delivery time.
		time.Sleep(100 * time.Millisecond)

		if received.Load() != 1 {
			t.Fatalf("expected 1 event received, got %d", received.Load())
		}
	})

	t.Run("SubscribeWildcard", func(t *testing.T) {
		var received atomic.Int32
		_, err := bus.Subscribe("*", func(ctx context.Context, e events.Event) error {
			received.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe wildcard: %v", err)
		}

		evt := testEvent{
			BaseEvent: events.NewBaseEvent("wildcard.test"),
			Data:      "wild",
		}
		if err := bus.Emit(context.Background(), evt, events.WithSync()); err != nil {
			t.Fatalf("Emit: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		if received.Load() < 1 {
			t.Fatal("wildcard subscriber should have received the event")
		}
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		var received atomic.Int32
		sub, err := bus.Subscribe("unsub.test", func(ctx context.Context, e events.Event) error {
			received.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}

		if err := sub.Unsubscribe(); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}

		evt := testEvent{
			BaseEvent: events.NewBaseEvent("unsub.test"),
			Data:      "after-unsub",
		}
		if err := bus.Emit(context.Background(), evt, events.WithSync()); err != nil {
			t.Fatalf("Emit: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		if received.Load() != 0 {
			t.Fatal("should not receive events after unsubscribe")
		}
	})
}
