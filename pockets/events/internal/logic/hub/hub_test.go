package hub

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// discardLogger keeps the single-instance and drop warnings out of test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() Config {
	return Config{Logger: discardLogger()}
}

// idEvent exposes EventID, so the hub sources the SSE id: from it (the durable
// rail's rehydrated events).
type idEvent struct {
	sdkevents.BaseEvent
	id string
}

func (e idEvent) EventID() string { return e.id }

// noopSub is a Subscription that records Unsubscribe.
type noopSub struct{ unsubscribed *bool }

func (s noopSub) Unsubscribe() error {
	if s.unsubscribed != nil {
		*s.unsubscribed = true
	}
	return nil
}

// plainBus satisfies sdkevents.Bus but NOT Broadcaster — it drives the
// single-instance path.
type plainBus struct {
	subscribeCalled bool
	handler         sdkevents.Handler
	unsubscribed    bool
}

func (b *plainBus) Emit(ctx context.Context, e sdkevents.Event, _ ...sdkevents.EmitOption) error {
	if b.handler != nil {
		return b.handler(ctx, e)
	}
	return nil
}

func (b *plainBus) Subscribe(_ string, h sdkevents.Handler) (sdkevents.Subscription, error) {
	b.subscribeCalled = true
	b.handler = h
	return noopSub{unsubscribed: &b.unsubscribed}, nil
}

func (b *plainBus) Close(context.Context) error { return nil }

// broadcastBus additionally satisfies Broadcaster — the multi-instance path.
type broadcastBus struct {
	plainBus
	broadcastCalled bool
}

func (b *broadcastBus) SubscribeBroadcast(_ string, h sdkevents.Handler) (sdkevents.Subscription, error) {
	b.broadcastCalled = true
	b.handler = h
	return noopSub{unsubscribed: &b.unsubscribed}, nil
}

func recv(t *testing.T, ch <-chan Frame) Frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a frame")
		return Frame{}
	}
}

func expectNoFrame(t *testing.T, ch <-chan Frame) {
	t.Helper()
	select {
	case f := <-ch:
		t.Fatalf("expected no frame, got %+v", f)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNew_UsesBroadcastWhenAvailable(t *testing.T) {
	bus := &broadcastBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !bus.broadcastCalled {
		t.Fatal("expected SubscribeBroadcast to be used for a Broadcaster bus")
	}
	if bus.subscribeCalled {
		t.Fatal("expected plain Subscribe not to be used for a Broadcaster bus")
	}
	_ = h.Close()
}

func TestNew_FallsBackToSubscribe(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !bus.subscribeCalled {
		t.Fatal("expected plain Subscribe for a non-Broadcaster bus")
	}
	_ = h.Close()
}

func TestClose_Unsubscribes(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bus.unsubscribed {
		t.Fatal("expected Close to unsubscribe from the bus")
	}
}

func TestConnect_MetadataOnlyProjection(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	evt := sdkevents.NewBaseEvent("content.updated").WithAggregate("entry", "e1")
	if err := bus.Emit(context.Background(), evt); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	f := recv(t, frames)
	if f.Type != "content.updated" {
		t.Fatalf("frame type = %q, want content.updated", f.Type)
	}
	if f.ID != evt.CorrelationID() {
		t.Fatalf("frame id = %q, want correlation id %q", f.ID, evt.CorrelationID())
	}
	mv, ok := f.Data.(metaView)
	if !ok {
		t.Fatalf("frame data = %T, want metaView", f.Data)
	}
	if mv.Type != "content.updated" || mv.AggregateType == nil || *mv.AggregateType != "entry" || mv.AggregateID == nil || *mv.AggregateID != "e1" {
		t.Fatalf("metadata projection = %+v, want type/entry/e1", mv)
	}
}

func TestConnect_EventIDSourcesID(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	evt := idEvent{BaseEvent: sdkevents.NewBaseEvent("content.published"), id: "evt-123"}
	if err := bus.Emit(context.Background(), evt); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	f := recv(t, frames)
	if f.ID != "evt-123" {
		t.Fatalf("frame id = %q, want the EventID evt-123", f.ID)
	}
}

func TestConnect_ProjectorOverride(t *testing.T) {
	bus := &plainBus{}
	cfg := testConfig()
	cfg.Projector = func(e sdkevents.Event) any {
		return map[string]string{"custom": e.Type()}
	}
	h, err := New(bus, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	f := recv(t, frames)
	m, ok := f.Data.(map[string]string)
	if !ok || m["custom"] != "content.updated" {
		t.Fatalf("projector body = %+v, want custom map", f.Data)
	}
}

func TestConnect_TypesFilter(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{Types: []string{"content.updated"}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.deleted")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	expectNoFrame(t, frames)

	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	f := recv(t, frames)
	if f.Type != "content.updated" {
		t.Fatalf("frame type = %q, want content.updated", f.Type)
	}
}

func TestConnect_ResourceScopedFilter(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{ResourceType: "entry", ResourceID: "e1"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	// Non-matching aggregate id → suppressed.
	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated").WithAggregate("entry", "e2")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	expectNoFrame(t, frames)

	// No aggregate metadata → suppressed (deny-by-default, P4).
	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	expectNoFrame(t, frames)

	// Matching aggregate → delivered.
	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated").WithAggregate("entry", "e1")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	f := recv(t, frames)
	if f.Type != "content.updated" {
		t.Fatalf("frame type = %q, want content.updated", f.Type)
	}
}

func TestConnect_PerSubjectCap(t *testing.T) {
	bus := &plainBus{}
	cfg := testConfig()
	cfg.MaxConnsPerSubject = 2
	h, err := New(bus, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, u1, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect 1: %v", err)
	}
	defer u1()
	_, u2, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect 2: %v", err)
	}
	defer u2()

	if _, _, err := h.Connect("user:u1", ConnectOptions{}); err != ErrTooManyConnections {
		t.Fatalf("Connect 3 err = %v, want ErrTooManyConnections", err)
	}

	// A different subject is unaffected by another subject's cap.
	if _, u3, err := h.Connect("user:u2", ConnectOptions{}); err != nil {
		t.Fatalf("Connect other subject: %v", err)
	} else {
		u3()
	}

	// Releasing a slot lets a new connection in.
	u1()
	if _, u4, err := h.Connect("user:u1", ConnectOptions{}); err != nil {
		t.Fatalf("Connect after release: %v", err)
	} else {
		u4()
	}
}

func TestConnect_DropsWhenBufferFull(t *testing.T) {
	bus := &plainBus{}
	cfg := testConfig()
	cfg.BufferSize = 1
	h, err := New(bus, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Register a connection but never drain it.
	_, unregister, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer unregister()

	// One event fills the buffer; the next two are dropped.
	for i := 0; i < 3; i++ {
		if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	if got := h.dropped.Load(); got < 2 {
		t.Fatalf("dropped = %d, want >= 2", got)
	}
}

func TestUnregister_StopsDelivery(t *testing.T) {
	bus := &plainBus{}
	h, err := New(bus, testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frames, unregister, err := h.Connect("user:u1", ConnectOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	unregister()

	if err := bus.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	expectNoFrame(t, frames)
}
