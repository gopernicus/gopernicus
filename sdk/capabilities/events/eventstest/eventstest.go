// Package eventstest is a conformance suite for events.Bus implementations:
// every backend that satisfies the port should pass Run against a fresh
// instance. Modeled on the cachertest / go/analysis/analysistest pattern — a
// Run(t, newBus) runner so adapters are verified against one shared behavioral
// contract. Imports stdlib + sdk/capabilities/events only (sdk stays dependency-free per the
// constitution).
//
// Scope is deliberate. Unlike cacher, event buses do NOT share one
// delivery-count contract: the Memory bus is at-most-once, the durable outbox
// rail is at-least-once, and Redis consumer groups differ again. The suite
// therefore asserts only the common observable contract — subscribe-then-emit
// delivers; the "*" wildcard matches; unsubscribe stops delivery; no delivery
// after Close; Close is idempotent; WithSync completes handlers before
// returning; TypedHandler serves both the direct type-assertion path and the
// Unmarshaler slow path. Delivery-count guarantees are documented per backend,
// never asserted here, so an at-least-once backend passes the same suite.
package eventstest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// deliverTimeout bounds how long a delivery assertion waits for an async
// handler to fire. It is generous to avoid flakiness on a loaded CI box.
const deliverTimeout = 2 * time.Second

// settleWindow is how long a non-delivery assertion waits after a
// synchronization point before concluding a handler was correctly skipped.
const settleWindow = 100 * time.Millisecond

// suiteEvent is the concrete event type the suite emits and decodes. Its
// BaseEvent json tags let it round-trip through the Unmarshaler slow path.
type suiteEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

func newSuiteEvent(topic, data string) suiteEvent {
	return suiteEvent{BaseEvent: events.NewBaseEvent(topic), Data: data}
}

// Run exercises the events.Bus contract against a fresh instance obtained from
// newBus for each subtest.
func Run(t *testing.T, newBus func(t *testing.T) events.Bus) {
	t.Helper()

	t.Run("SubscribeThenEmitDelivers", func(t *testing.T) { testSubscribeThenEmitDelivers(t, newBus(t)) })
	t.Run("WildcardMatches", func(t *testing.T) { testWildcardMatches(t, newBus(t)) })
	t.Run("UnsubscribeStopsDelivery", func(t *testing.T) { testUnsubscribeStopsDelivery(t, newBus(t)) })
	t.Run("NoDeliveryAfterClose", func(t *testing.T) { testNoDeliveryAfterClose(t, newBus(t)) })
	t.Run("CloseIdempotent", func(t *testing.T) { testCloseIdempotent(t, newBus(t)) })
	t.Run("WithSyncCompletesBeforeReturning", func(t *testing.T) { testWithSyncCompletesBeforeReturning(t, newBus(t)) })
	t.Run("TypedHandlerAssertionPath", func(t *testing.T) { testTypedHandlerAssertionPath(t, newBus(t)) })
	t.Run("TypedHandlerUnmarshalerPath", func(t *testing.T) { testTypedHandlerUnmarshalerPath(t, newBus(t)) })
}

// testSubscribeThenEmitDelivers covers the async default path: an event emitted
// after a subscription is delivered at least once (no count assertion, so an
// at-least-once backend passes).
func testSubscribeThenEmitDelivers(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	var count int32
	sub, err := bus.Subscribe("suite.deliver", func(context.Context, events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Emit(ctx, newSuiteEvent("suite.deliver", "hello")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	waitForAtLeast(t, &count, 1, "subscribe-then-emit should deliver the event")
}

// testWildcardMatches proves a "*" subscription receives an event whose exact
// topic has no subscriber.
func testWildcardMatches(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	var count int32
	sub, err := bus.Subscribe("*", func(context.Context, events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(*) error = %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Emit(ctx, newSuiteEvent("suite.wildcard", "")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	waitForAtLeast(t, &count, 1, `"*" wildcard should match an event with no exact subscriber`)
}

// testUnsubscribeStopsDelivery proves an unsubscribed handler receives no event
// emitted after Unsubscribe. An active control handler on the same topic gives
// a synchronization point (its delivery proves the emit dispatched) so the
// removed-handler assertion is meaningful rather than racing the emit.
func testUnsubscribeStopsDelivery(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	var control, removed int32
	controlSub, err := bus.Subscribe("suite.unsub", func(context.Context, events.Event) error {
		atomic.AddInt32(&control, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(control) error = %v", err)
	}
	defer controlSub.Unsubscribe()

	removedSub, err := bus.Subscribe("suite.unsub", func(context.Context, events.Event) error {
		atomic.AddInt32(&removed, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(removed) error = %v", err)
	}
	if err := removedSub.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}

	if err := bus.Emit(ctx, newSuiteEvent("suite.unsub", "")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	waitForAtLeast(t, &control, 1, "control handler should receive the emitted event")

	// Give any stray delivery to the removed handler a window to (wrongly) land.
	time.Sleep(settleWindow)
	if got := atomic.LoadInt32(&removed); got != 0 {
		t.Errorf("unsubscribed handler was called %d times, want 0", got)
	}
}

// testNoDeliveryAfterClose proves a Close'd bus delivers nothing and does not
// error or panic on a subsequent Emit.
func testNoDeliveryAfterClose(t *testing.T, bus events.Bus) {
	ctx := context.Background()

	var count int32
	if _, err := bus.Subscribe("suite.closed", func(context.Context, events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	if err := bus.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := bus.Emit(ctx, newSuiteEvent("suite.closed", "")); err != nil {
		t.Fatalf("Emit() after Close should not error, got: %v", err)
	}

	time.Sleep(settleWindow)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("handler called %d times after Close, want 0", got)
	}
}

// testCloseIdempotent proves Close can be called more than once without error.
func testCloseIdempotent(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	if err := bus.Close(ctx); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := bus.Close(ctx); err != nil {
		t.Errorf("second Close() error = %v, want nil (Close must be idempotent)", err)
	}
}

// testWithSyncCompletesBeforeReturning proves a WithSync emit runs handlers
// before it returns: the delivery is asserted immediately with no wait.
func testWithSyncCompletesBeforeReturning(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	var count int32
	sub, err := bus.Subscribe("suite.sync", func(context.Context, events.Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Emit(ctx, newSuiteEvent("suite.sync", ""), events.WithSync()); err != nil {
		t.Fatalf("Emit(WithSync) error = %v", err)
	}
	if got := atomic.LoadInt32(&count); got < 1 {
		t.Errorf("WithSync handler ran %d times before Emit returned, want >= 1", got)
	}
}

// testTypedHandlerAssertionPath proves TypedHandler's fast path: an emitted
// concrete suiteEvent is delivered to the typed handler with its payload intact.
func testTypedHandlerAssertionPath(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	got := make(chan string, 1)
	handler := events.TypedHandler(func(_ context.Context, e suiteEvent) error {
		select {
		case got <- e.Data:
		default:
		}
		return nil
	})
	sub, err := bus.Subscribe("suite.typed", handler)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Emit(ctx, newSuiteEvent("suite.typed", "assert-path")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	waitForValue(t, got, "assert-path", "TypedHandler assertion path should deliver the typed payload")
}

// testTypedHandlerUnmarshalerPath proves TypedHandler's slow path: a RemoteEvent
// (never a suiteEvent) is decoded through Unmarshaler into the handler's
// concrete type. Emitting a RemoteEvent forces the slow branch regardless of
// backend.
func testTypedHandlerUnmarshalerPath(t *testing.T, bus events.Bus) {
	ctx := context.Background()
	defer bus.Close(ctx)

	payload, err := events.EncodeEvent(newSuiteEvent("suite.remote", "unmarshal-path"))
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}
	remote := events.RemoteEvent{
		EventType:   "suite.remote",
		Occurred:    time.Now().UTC(),
		Correlation: "corr-remote",
		Payload:     payload,
	}

	got := make(chan string, 1)
	handler := events.TypedHandler(func(_ context.Context, e suiteEvent) error {
		select {
		case got <- e.Data:
		default:
		}
		return nil
	})
	sub, err := bus.Subscribe("suite.remote", handler)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Emit(ctx, remote); err != nil {
		t.Fatalf("Emit(RemoteEvent) error = %v", err)
	}
	waitForValue(t, got, "unmarshal-path", "TypedHandler Unmarshaler path should decode the RemoteEvent payload")
}

// waitForAtLeast polls counter until it reaches want or deliverTimeout elapses.
// It asserts >= want, never an exact count, so at-least-once backends pass.
func waitForAtLeast(t *testing.T, counter *int32, want int32, msg string) {
	t.Helper()
	deadline := time.Now().Add(deliverTimeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(counter) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := atomic.LoadInt32(counter); got < want {
		t.Fatalf("%s: handler ran %d times, want >= %d within %s", msg, got, want, deliverTimeout)
	}
}

// waitForValue waits up to deliverTimeout for want on ch.
func waitForValue(t *testing.T, ch <-chan string, want, msg string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Errorf("%s: got %q, want %q", msg, got, want)
		}
	case <-time.After(deliverTimeout):
		t.Fatalf("%s: no delivery within %s", msg, deliverTimeout)
	}
}
