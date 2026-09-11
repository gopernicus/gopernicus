package events_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newMemory(opts ...events.MemoryOption) *events.Memory {
	return events.NewMemory(append([]events.MemoryOption{events.WithLogger(silentLogger())}, opts...)...)
}

type testEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

func newTestEvent(topic, data string) testEvent {
	return testEvent{BaseEvent: events.NewBaseEvent(topic), Data: data}
}

// =============================================================================
// Sync delivery
// =============================================================================

func TestMemory_Dispatch_CallsHandler(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	var received string
	bus.Subscribe("test.event", func(_ context.Context, e events.Event) error {
		if te, ok := e.(testEvent); ok {
			received = te.Data
		}
		return nil
	})

	if err := bus.Dispatch(context.Background(), newTestEvent("test.event", "hello")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if received != "hello" {
		t.Errorf("received = %q, want %q", received, "hello")
	}
}

func TestMemory_Dispatch_PropagatesError(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	bus.Subscribe("test.event", func(_ context.Context, _ events.Event) error {
		return errors.New("handler failed")
	})

	err := bus.Dispatch(context.Background(), newTestEvent("test.event", ""))
	if err == nil {
		t.Error("sync Emit should propagate the handler error")
	}
}

func TestMemory_Dispatch_MultipleHandlers(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	var count int32
	h := func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	}
	bus.Subscribe("test.event", h)
	bus.Subscribe("test.event", h)

	bus.Dispatch(context.Background(), newTestEvent("test.event", ""))
	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("handler called %d times, want 2", count)
	}
}

// =============================================================================
// Async delivery
// =============================================================================

func TestMemory_EmitAsync_CallsHandler(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	done := make(chan string, 1)
	bus.Subscribe("test.event", func(_ context.Context, e events.Event) error {
		if te, ok := e.(testEvent); ok {
			done <- te.Data
		}
		return nil
	})

	bus.Emit(context.Background(), newTestEvent("test.event", "async-hello")) // default async

	select {
	case received := <-done:
		if received != "async-hello" {
			t.Errorf("received = %q, want %q", received, "async-hello")
		}
	case <-time.After(2 * time.Second):
		t.Error("async handler was not called within timeout")
	}
}

func TestMemory_EmitAsync_PreservesContextValues(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	type ctxKey struct{}
	got := make(chan any, 1)
	bus.Subscribe("test.event", func(ctx context.Context, _ events.Event) error {
		got <- ctx.Value(ctxKey{})
		return nil
	})

	// A cancelled parent must not abort async work, but its values survive.
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "v"))
	bus.Emit(parent, newTestEvent("test.event", ""))
	cancel()

	select {
	case v := <-got:
		if v != "v" {
			t.Errorf("context value = %v, want %q", v, "v")
		}
	case <-time.After(2 * time.Second):
		t.Error("async handler was not called")
	}
}

// =============================================================================
// Wildcard
// =============================================================================

func TestMemory_Wildcard(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	var count int32
	bus.Subscribe("*", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	bus.Dispatch(context.Background(), newTestEvent("user.created", ""))
	bus.Dispatch(context.Background(), newTestEvent("order.completed", ""))

	if atomic.LoadInt32(&count) != 2 {
		t.Errorf("wildcard handler called %d times, want 2", count)
	}
}

// =============================================================================
// Unsubscribe
// =============================================================================

func TestMemory_Unsubscribe(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	var count int32
	sub, _ := bus.Subscribe("test.event", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	bus.Dispatch(context.Background(), newTestEvent("test.event", ""))
	sub.Unsubscribe()
	bus.Dispatch(context.Background(), newTestEvent("test.event", ""))

	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times after unsubscribe, want 1", count)
	}
}

func TestMemory_Unsubscribe_Idempotent(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	sub, _ := bus.Subscribe("test", func(_ context.Context, _ events.Event) error { return nil })
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe() error = %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe() error = %v", err)
	}
}

// =============================================================================
// No handlers
// =============================================================================

func TestMemory_NoHandlers(t *testing.T) {
	bus := newMemory()
	defer bus.Close(context.Background())

	if err := bus.Dispatch(context.Background(), newTestEvent("unknown.event", "")); err != nil {
		t.Fatalf("Emit() with no handlers should not error, got: %v", err)
	}
}

// =============================================================================
// Broadcaster
// =============================================================================

func TestMemory_SubscribeBroadcast(t *testing.T) {
	memory := newMemory()
	defer memory.Close(context.Background())
	var bus events.Broadcaster = memory

	var count int32
	sub, err := bus.SubscribeBroadcast("test.event", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeBroadcast() error = %v", err)
	}
	defer sub.Unsubscribe()

	bus.(*events.Memory).Dispatch(context.Background(), newTestEvent("test.event", ""))
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("broadcast handler called %d times, want 1", count)
	}
}

// =============================================================================
// Close
// =============================================================================

func TestMemory_Close_Idempotent(t *testing.T) {
	bus := newMemory()
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestMemory_NoDeliveryAfterClose(t *testing.T) {
	bus := newMemory()

	var count int32
	bus.Subscribe("test.event", func(_ context.Context, _ events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})

	bus.Close(context.Background())

	// Both paths reject post-close admissions without delivering.
	if err := bus.Emit(context.Background(), newTestEvent("test.event", "")); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("Emit after Close = %v, want ErrClosed", err)
	}
	if err := bus.Dispatch(context.Background(), newTestEvent("test.event", "")); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("Dispatch after Close = %v, want ErrClosed", err)
	}
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("handler called %d times after Close, want 0", got)
	}
}

// =============================================================================
// Panic recovery
// =============================================================================

func TestMemory_AsyncPanicRecovered(t *testing.T) {
	// A single worker so survival of the panic is what lets the next event run.
	bus := newMemory(events.WithWorkerCount(1))
	defer bus.Close(context.Background())

	done := make(chan struct{}, 1)
	bus.Subscribe("panic.event", func(_ context.Context, _ events.Event) error {
		panic("boom")
	})
	bus.Subscribe("next.event", func(_ context.Context, _ events.Event) error {
		done <- struct{}{}
		return nil
	})

	bus.Emit(context.Background(), newTestEvent("panic.event", "")) // panics, recovered
	bus.Emit(context.Background(), newTestEvent("next.event", ""))  // must still be delivered

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("a panicking handler took down the only worker; a later event was never delivered")
	}
}

// =============================================================================
// Bounded queue / admission failure
// =============================================================================

func TestMemory_RejectsWhenQueueFull(t *testing.T) {
	const queueSize = 2
	bus := newMemory(events.WithWorkerCount(1), events.WithQueueSize(queueSize))

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var delivered int32

	bus.Subscribe("flood.event", func(_ context.Context, _ events.Event) error {
		once.Do(func() {
			close(started)
			<-release // hold the single worker so the queue fills
		})
		atomic.AddInt32(&delivered, 1)
		return nil
	})

	// First emit occupies the worker.
	bus.Emit(context.Background(), newTestEvent("flood.event", ""))
	<-started

	// Flood while the worker is blocked: at most queueSize get queued, the rest
	// are dropped. Emit must never block.
	const flood = 50
	for i := 0; i < flood; i++ {
		emitReturned := make(chan error, 1)
		go func() {
			emitReturned <- bus.Emit(context.Background(), newTestEvent("flood.event", ""))
		}()
		select {
		case err := <-emitReturned:
			if i < queueSize && err != nil {
				t.Fatalf("queue admission %d: %v", i, err)
			}
			if i >= queueSize && !errors.Is(err, events.ErrCapacity) {
				t.Fatalf("full queue admission %d: %v, want ErrCapacity", i, err)
			}
		case <-time.After(time.Second):
			t.Fatal("Emit blocked while the queue was full (drop-on-full violated)")
		}
	}

	close(release)
	bus.Close(context.Background()) // drains queued events

	got := atomic.LoadInt32(&delivered)
	if got != queueSize+1 { // 1 in-flight + queueSize queued
		t.Errorf("delivered %d events, want %d accepted events", got, queueSize+1)
	}
	if got >= flood {
		t.Errorf("delivered %d events, expected drops below %d", got, flood)
	}
}

func TestNewMemoryRejectsNilOption(t *testing.T) {
	defer func() {
		if got := recover(); got != "events.NewMemory: nil option" {
			t.Fatalf("panic = %v", got)
		}
	}()
	events.NewMemory(nil)
}
