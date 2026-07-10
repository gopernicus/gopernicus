package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestWakeChannel_EmitFiresWake(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel() error = %v", err)
	}
	defer sub.Unsubscribe()

	bus.Emit(context.Background(), newTestEvent("test.event", ""), events.WithSync())

	select {
	case <-wake:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected a wake within 100ms")
	}
}

func TestWakeChannel_BurstIsCoalesced(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel() error = %v", err)
	}
	defer sub.Unsubscribe()

	for i := 0; i < 100; i++ {
		bus.Emit(context.Background(), newTestEvent("test.event", ""), events.WithSync())
	}

	received := 0
	timeout := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case <-wake:
			received++
		case <-timeout:
			break drain
		}
	}

	if received == 0 {
		t.Error("expected at least one wake")
	}
	if received > 10 {
		t.Errorf("expected coalescing to limit receives, got %d", received)
	}
}

func TestWakeChannel_NonMatchingTopicIgnored(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "a.foo")
	if err != nil {
		t.Fatalf("WakeChannel() error = %v", err)
	}
	defer sub.Unsubscribe()

	bus.Emit(context.Background(), newTestEvent("b.bar", ""), events.WithSync())

	select {
	case <-wake:
		t.Fatal("unexpected wake for a non-matching topic")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWakeChannel_UnsubscribeStopsWakes(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel() error = %v", err)
	}
	sub.Unsubscribe()

	bus.Emit(context.Background(), newTestEvent("test.event", ""), events.WithSync())

	select {
	case <-wake:
		t.Fatal("unexpected wake after unsubscribe")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWakeChannel_BusCloseIsSafe(t *testing.T) {
	bus := newMemory()

	_, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel() error = %v", err)
	}
	bus.Close(context.Background())

	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("Unsubscribe after Close should be safe, got: %v", err)
	}
}
