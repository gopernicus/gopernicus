package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/events"
)

// =============================================================================
// Test event types
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
// BaseEvent
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

func TestNewBaseEvent_UniqueCorrelation(t *testing.T) {
	a := events.NewBaseEvent("t")
	b := events.NewBaseEvent("t")
	if a.CorrelationID() == b.CorrelationID() {
		t.Error("distinct events should get distinct correlation IDs")
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
	if tid == nil || *tid != "tenant-abc" {
		t.Errorf("TenantID() = %v, want %q", tid, "tenant-abc")
	}
}

func TestBaseEvent_WithAggregate(t *testing.T) {
	e := events.NewBaseEvent("user.created").WithAggregate("user", "user-123")
	if at := e.AggregateType(); at == nil || *at != "user" {
		t.Errorf("AggregateType() = %v, want %q", at, "user")
	}
	if aid := e.AggregateID(); aid == nil || *aid != "user-123" {
		t.Errorf("AggregateID() = %v, want %q", aid, "user-123")
	}
}

func TestBaseEvent_MethodChaining(t *testing.T) {
	e := events.NewBaseEvent("user.created").
		WithTenant("tenant-1").
		WithAggregate("user", "user-1")

	if e.TenantID() == nil || *e.TenantID() != "tenant-1" {
		t.Error("chaining should preserve TenantID")
	}
	if e.AggregateType() == nil || *e.AggregateType() != "user" {
		t.Error("chaining should preserve AggregateType")
	}
}

func TestBaseEvent_DefaultMetadataNil(t *testing.T) {
	e := events.NewBaseEvent("test")
	if e.TenantID() != nil || e.AggregateType() != nil || e.AggregateID() != nil {
		t.Error("default metadata should be nil")
	}
}

func TestBaseEvent_SatisfiesMetadata(t *testing.T) {
	var _ events.Metadata = events.NewBaseEvent("t")
}

// =============================================================================
// TypedHandler
// =============================================================================

func TestTypedHandler_FastPath(t *testing.T) {
	var received string
	handler := events.TypedHandler(func(_ context.Context, e userCreated) error {
		received = e.Email
		return nil
	})

	evt := userCreated{
		BaseEvent: events.NewBaseEvent("user.created"),
		Email:     "test@example.com",
	}
	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if received != "test@example.com" {
		t.Errorf("received = %q, want %q", received, "test@example.com")
	}
}

func TestTypedHandler_MismatchedTypeIgnored(t *testing.T) {
	called := false
	handler := events.TypedHandler(func(_ context.Context, _ userCreated) error {
		called = true
		return nil
	})

	evt := orderCompleted{
		BaseEvent: events.NewBaseEvent("order.completed"),
		OrderID:   "order-1",
	}
	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if called {
		t.Error("handler should not be called for a mismatched, non-Unmarshaler event")
	}
}

func TestTypedHandler_SlowPath_RemoteEvent(t *testing.T) {
	original := userCreated{
		BaseEvent: events.NewBaseEventWithCorrelation("user.created", "corr-9"),
		Email:     "slow@example.com",
	}
	payload, err := events.EncodeEvent(original)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	remote := events.RemoteEvent{
		EventType:   original.Type(),
		Occurred:    original.OccurredAt(),
		Correlation: original.CorrelationID(),
		Payload:     payload,
	}

	var received string
	handler := events.TypedHandler(func(_ context.Context, e userCreated) error {
		received = e.Email
		return nil
	})
	if err := handler(context.Background(), remote); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if received != "slow@example.com" {
		t.Errorf("received = %q, want %q (Unmarshaler slow path)", received, "slow@example.com")
	}
}

// =============================================================================
// Encoding
// =============================================================================

func TestEncodeEvent_JSONFallback(t *testing.T) {
	evt := userCreated{
		BaseEvent: events.NewBaseEventWithCorrelation("user.created", "corr-1"),
		Email:     "test@example.com",
	}
	data, err := events.EncodeEvent(evt)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	var decoded map[string]any
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

type customEncoded struct {
	events.BaseEvent
}

func (customEncoded) EncodeEvent() ([]byte, error) { return []byte("custom"), nil }

func TestEncodeEvent_CustomEncoder(t *testing.T) {
	data, err := events.EncodeEvent(customEncoded{BaseEvent: events.NewBaseEvent("c")})
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}
	if string(data) != "custom" {
		t.Errorf("EncodeEvent() = %q, want %q", data, "custom")
	}
}

// =============================================================================
// Record
// =============================================================================

func TestNewRecord(t *testing.T) {
	evt := userCreated{
		BaseEvent: events.NewBaseEventWithCorrelation("user.created", "corr-2").
			WithAggregate("user", "u-1").
			WithTenant("t-1"),
		Email: "rec@example.com",
	}

	rec, err := events.NewRecord(evt)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	if rec.EventID == "" {
		t.Error("EventID should be assigned")
	}
	if rec.Type != "user.created" {
		t.Errorf("Type = %q, want %q", rec.Type, "user.created")
	}
	if rec.CorrelationID != "corr-2" {
		t.Errorf("CorrelationID = %q, want %q", rec.CorrelationID, "corr-2")
	}
	if rec.AggregateType == nil || *rec.AggregateType != "user" {
		t.Errorf("AggregateType = %v, want %q", rec.AggregateType, "user")
	}
	if rec.AggregateID == nil || *rec.AggregateID != "u-1" {
		t.Errorf("AggregateID = %v, want %q", rec.AggregateID, "u-1")
	}
	if rec.TenantID == nil || *rec.TenantID != "t-1" {
		t.Errorf("TenantID = %v, want %q", rec.TenantID, "t-1")
	}

	var decoded map[string]any
	if err := json.Unmarshal(rec.Payload, &decoded); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if decoded["email"] != "rec@example.com" {
		t.Errorf("payload email = %v, want %q", decoded["email"], "rec@example.com")
	}
}

func TestNewRecord_UniqueEventID(t *testing.T) {
	evt := userCreated{BaseEvent: events.NewBaseEvent("user.created")}
	a, _ := events.NewRecord(evt)
	b, _ := events.NewRecord(evt)
	if a.EventID == b.EventID {
		t.Error("each Record should get a unique EventID")
	}
}

func TestNewRecord_NoMetadata(t *testing.T) {
	rec, err := events.NewRecord(events.NewBaseEvent("plain"))
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	// BaseEvent satisfies Metadata but its pointers are nil when unset.
	if rec.AggregateType != nil || rec.AggregateID != nil || rec.TenantID != nil {
		t.Error("unset metadata should stay nil on the record")
	}
}

// =============================================================================
// RemoteEvent / DecodeRemoteMetadata
// =============================================================================

func TestRemoteEvent_Accessors(t *testing.T) {
	now := time.Now().UTC()
	tenant, agg, aggID := "t", "user", "u-1"
	re := events.RemoteEvent{
		EventType:   "user.created",
		Occurred:    now,
		Correlation: "corr-3",
		Payload:     []byte(`{"x":1}`),
		Tenant:      &tenant,
		AggType:     &agg,
		AggID:       &aggID,
	}

	if re.Type() != "user.created" || re.CorrelationID() != "corr-3" || !re.OccurredAt().Equal(now) {
		t.Error("RemoteEvent Event accessors mismatch")
	}
	if *re.TenantID() != "t" || *re.AggregateType() != "user" || *re.AggregateID() != "u-1" {
		t.Error("RemoteEvent Metadata accessors mismatch")
	}
	enc, err := re.EncodeEvent()
	if err != nil || string(enc) != `{"x":1}` {
		t.Errorf("EncodeEvent() = %q, %v; want original payload", enc, err)
	}
}

func TestDecodeRemoteMetadata(t *testing.T) {
	evt := userCreated{
		BaseEvent: events.NewBaseEvent("user.created").
			WithAggregate("user", "u-2").
			WithTenant("t-2"),
		Email: "d@example.com",
	}
	payload, err := events.EncodeEvent(evt)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	tenant, aggType, aggID := events.DecodeRemoteMetadata(payload)
	if tenant == nil || *tenant != "t-2" {
		t.Errorf("tenant = %v, want %q", tenant, "t-2")
	}
	if aggType == nil || *aggType != "user" {
		t.Errorf("aggType = %v, want %q", aggType, "user")
	}
	if aggID == nil || *aggID != "u-2" {
		t.Errorf("aggID = %v, want %q", aggID, "u-2")
	}
}

func TestDecodeRemoteMetadata_Absent(t *testing.T) {
	tenant, aggType, aggID := events.DecodeRemoteMetadata([]byte(`{"type":"x"}`))
	if tenant != nil || aggType != nil || aggID != nil {
		t.Error("absent metadata should decode to nil pointers")
	}
}

func TestDecodeRemoteMetadata_Garbage(t *testing.T) {
	tenant, aggType, aggID := events.DecodeRemoteMetadata([]byte("not json"))
	if tenant != nil || aggType != nil || aggID != nil {
		t.Error("unparseable payload should decode to nil pointers")
	}
}

// =============================================================================
// Emit options
// =============================================================================

func TestApplyOptions_Default(t *testing.T) {
	if events.ApplyOptions().Sync {
		t.Error("default Sync should be false")
	}
}

func TestApplyOptions_WithSync(t *testing.T) {
	if !events.ApplyOptions(events.WithSync()).Sync {
		t.Error("WithSync() should set Sync true")
	}
}

// =============================================================================
// Noop bus
// =============================================================================

func TestNoop(t *testing.T) {
	var bus events.Bus = events.Noop{}

	if err := bus.Emit(context.Background(), events.NewBaseEvent("x")); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	sub, err := bus.Subscribe("x", func(context.Context, events.Event) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNoop_SatisfiesBroadcaster(t *testing.T) {
	var _ events.Broadcaster = events.Noop{}
}
