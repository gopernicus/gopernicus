package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// =============================================================================
// Test Event Types
// =============================================================================

type userCreated struct {
	events.BaseEvent
	Email string `json:"email"`
}

type orderCompleted struct {
	events.BaseEvent
	OrderID string `json:"order_id"`
}

// =============================================================================
// BaseEvent Tests
// =============================================================================

func TestNewBaseEvent(t *testing.T) {
	e := events.NewBaseEvent("user.created")

	if e.Type() != "user.created" {
		t.Errorf("Type() = %q, want %q", e.Type(), "user.created")
	}
	if e.CorrelationID() == "" {
		t.Error("CorrelationID() should not be empty")
	}
	if e.OccurredAt().IsZero() {
		t.Error("OccurredAt() should not be zero")
	}
	if time.Since(e.OccurredAt()) > time.Second {
		t.Error("OccurredAt() should be recent")
	}
}

func TestNewBaseEventWithCorrelation(t *testing.T) {
	e := events.NewBaseEventWithCorrelation("order.created", "corr-123")

	if e.CorrelationID() != "corr-123" {
		t.Errorf("CorrelationID() = %q, want %q", e.CorrelationID(), "corr-123")
	}
}

func TestBaseEvent_WithTenant(t *testing.T) {
	e := events.NewBaseEvent("user.created").WithTenant("tenant-abc")

	tid := e.TenantID()
	if tid == nil {
		t.Fatal("TenantID() should not be nil")
	}
	if *tid != "tenant-abc" {
		t.Errorf("TenantID() = %q, want %q", *tid, "tenant-abc")
	}
}

func TestBaseEvent_WithAggregate(t *testing.T) {
	e := events.NewBaseEvent("user.created").WithAggregate("user", "user-123")

	at := e.AggregateType()
	if at == nil || *at != "user" {
		t.Errorf("AggregateType() = %v, want %q", at, "user")
	}
	aid := e.AggregateID()
	if aid == nil || *aid != "user-123" {
		t.Errorf("AggregateID() = %v, want %q", aid, "user-123")
	}
}

func TestBaseEvent_MethodChaining(t *testing.T) {
	e := events.NewBaseEvent("user.created").
		WithTenant("tenant-1").
		WithAggregate("user", "user-1")

	if e.TenantID() == nil || *e.TenantID() != "tenant-1" {
		t.Error("method chaining should preserve TenantID")
	}
	if e.AggregateType() == nil || *e.AggregateType() != "user" {
		t.Error("method chaining should preserve AggregateType")
	}
}

func TestBaseEvent_ToOutbox(t *testing.T) {
	e := events.NewBaseEvent("user.created")
	if e.IsOutbox() {
		t.Error("new event should not be outbox")
	}

	e = e.ToOutbox()
	if !e.IsOutbox() {
		t.Error("after ToOutbox() should be outbox")
	}
}

func TestBaseEvent_DefaultMetadataNil(t *testing.T) {
	e := events.NewBaseEvent("test")

	if e.TenantID() != nil {
		t.Error("default TenantID should be nil")
	}
	if e.AggregateType() != nil {
		t.Error("default AggregateType should be nil")
	}
	if e.AggregateID() != nil {
		t.Error("default AggregateID should be nil")
	}
}

// =============================================================================
// TypedHandler Tests
// =============================================================================

func TestTypedHandler_MatchingType(t *testing.T) {
	var received string
	handler := events.TypedHandler(func(_ context.Context, e userCreated) error {
		received = e.Email
		return nil
	})

	evt := userCreated{
		BaseEvent: events.NewBaseEvent("user.created"),
		Email:     "test@example.com",
	}

	err := handler(context.Background(), evt)
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if received != "test@example.com" {
		t.Errorf("received = %q, want %q", received, "test@example.com")
	}
}

func TestTypedHandler_MismatchedType(t *testing.T) {
	called := false
	handler := events.TypedHandler(func(_ context.Context, _ userCreated) error {
		called = true
		return nil
	})

	// Pass a different event type.
	evt := orderCompleted{
		BaseEvent: events.NewBaseEvent("order.completed"),
		OrderID:   "order-1",
	}

	err := handler(context.Background(), evt)
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if called {
		t.Error("handler should not be called for mismatched type")
	}
}

// =============================================================================
// EncodeEvent Tests
// =============================================================================

func TestEncodeEvent_JSON(t *testing.T) {
	evt := userCreated{
		BaseEvent: events.NewBaseEventWithCorrelation("user.created", "corr-1"),
		Email:     "test@example.com",
	}

	data, err := events.EncodeEvent(evt)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if decoded["email"] != "test@example.com" {
		t.Errorf("email = %v, want %q", decoded["email"], "test@example.com")
	}
	if decoded["type"] != "user.created" {
		t.Errorf("type = %v, want %q", decoded["type"], "user.created")
	}
}

// =============================================================================
// EmitOptions Tests
// =============================================================================

func TestApplyOptions_Defaults(t *testing.T) {
	cfg := events.ApplyOptions()
	if cfg.Sync {
		t.Error("default Sync should be false")
	}
	if cfg.Priority != 0 {
		t.Errorf("default Priority = %d, want 0", cfg.Priority)
	}
}

func TestApplyOptions_WithSync(t *testing.T) {
	cfg := events.ApplyOptions(events.WithSync())
	if !cfg.Sync {
		t.Error("WithSync() should set Sync to true")
	}
}

func TestApplyOptions_WithPriority(t *testing.T) {
	cfg := events.ApplyOptions(events.WithPriority(5))
	if cfg.Priority != 5 {
		t.Errorf("Priority = %d, want 5", cfg.Priority)
	}
}

// =============================================================================
// NoopBus Tests
// =============================================================================

func TestNoopBus_Emit(t *testing.T) {
	bus := events.NewNoopBus()
	err := bus.Emit(context.Background(), events.NewBaseEvent("test"))
	if err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
}

func TestNoopBus_Subscribe(t *testing.T) {
	bus := events.NewNoopBus()
	sub, err := bus.Subscribe("test", func(_ context.Context, _ events.Event) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
}

func TestNoopBus_Close(t *testing.T) {
	bus := events.NewNoopBus()
	err := bus.Close(context.Background())
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
