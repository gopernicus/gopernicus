package events_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/memorybus"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type testEvent struct {
	events.BaseEvent
}

func TestWakeChannel_EmitFiresWake(t *testing.T) {
	bus := memorybus.New(silentLogger())
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel: %v", err)
	}
	defer sub.Unsubscribe()

	bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("test.event")}, events.WithSync())

	select {
	case <-wake:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected wake within 100ms")
	}
}

func TestWakeChannel_BurstIsCoalesced(t *testing.T) {
	bus := memorybus.New(silentLogger())
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel: %v", err)
	}
	defer sub.Unsubscribe()

	for i := 0; i < 100; i++ {
		bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("test.event")}, events.WithSync())
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
	bus := memorybus.New(silentLogger())
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "a.foo")
	if err != nil {
		t.Fatalf("WakeChannel: %v", err)
	}
	defer sub.Unsubscribe()

	bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("b.bar")}, events.WithSync())

	select {
	case <-wake:
		t.Fatal("unexpected wake for non-matching topic")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWakeChannel_UnsubscribeStopsWakes(t *testing.T) {
	bus := memorybus.New(silentLogger())
	defer bus.Close(context.Background())

	wake, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel: %v", err)
	}

	sub.Unsubscribe()

	bus.Emit(context.Background(), testEvent{BaseEvent: events.NewBaseEvent("test.event")}, events.WithSync())

	select {
	case <-wake:
		t.Fatal("unexpected wake after unsubscribe")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWakeChannel_BusCloseIsSafe(t *testing.T) {
	bus := memorybus.New(silentLogger())

	_, sub, err := events.WakeChannel(bus, "test.event")
	if err != nil {
		t.Fatalf("WakeChannel: %v", err)
	}

	bus.Close(context.Background())

	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("Unsubscribe after close should be safe, got: %v", err)
	}
}
