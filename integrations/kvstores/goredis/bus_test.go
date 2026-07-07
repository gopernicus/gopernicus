// These tests are hermetic: they exercise encoding/decoding, defaulting, and
// the RemoteEvent rehydration path without any Redis connection. The live
// contract is verified by conformance_test.go under REDIS_TEST_ADDR.
package goredis

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/events"
)

// testEvent is a concrete event whose BaseEvent json tags let it round-trip
// through EncodeEvent and the Unmarshaler slow path.
type testEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

// dummyClient builds a client that is never used for I/O in these tests (New
// does no network work; construction is enough to inspect defaults).
func dummyClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:6390"})
}

func TestNewAppliesDefaults(t *testing.T) {
	b := New(dummyClient(), slog.New(slog.DiscardHandler), Options{})

	if b.cfg.StreamPrefix != defaultStreamPrefix {
		t.Errorf("StreamPrefix = %q, want %q", b.cfg.StreamPrefix, defaultStreamPrefix)
	}
	if b.cfg.ConsumerGroup != defaultConsumerGroup {
		t.Errorf("ConsumerGroup = %q, want %q", b.cfg.ConsumerGroup, defaultConsumerGroup)
	}
	if b.cfg.Workers != defaultWorkers {
		t.Errorf("Workers = %d, want %d", b.cfg.Workers, defaultWorkers)
	}
	if b.cfg.BlockTimeout != defaultBlockTimeout {
		t.Errorf("BlockTimeout = %s, want %s", b.cfg.BlockTimeout, defaultBlockTimeout)
	}
	if b.cfg.BatchSize != defaultBatchSize {
		t.Errorf("BatchSize = %d, want %d", b.cfg.BatchSize, defaultBatchSize)
	}
	if b.consumerName == "" {
		t.Error("consumerName was not generated")
	}
}

func TestNewKeepsExplicitOptions(t *testing.T) {
	opts := Options{
		StreamPrefix:  "myapp:",
		ConsumerGroup: "workers",
		Workers:       8,
		BlockTimeout:  250 * time.Millisecond,
		BatchSize:     32,
		MaxLen:        1000,
	}
	b := New(dummyClient(), slog.New(slog.DiscardHandler), opts)
	if b.cfg != opts {
		t.Errorf("cfg = %+v, want %+v", b.cfg, opts)
	}
}

func TestNewNilLoggerFallsBack(t *testing.T) {
	b := New(dummyClient(), nil, Options{})
	if b.log == nil {
		t.Fatal("nil logger was not replaced with a default")
	}
}

func TestParseMessageRoundTrip(t *testing.T) {
	base := events.NewBaseEvent("widget.created").WithTenant("t1").WithAggregate("widget", "w1")
	src := testEvent{BaseEvent: base, Data: "hello"}

	data, err := events.EncodeEvent(src)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	values := map[string]any{
		"type":           src.Type(),
		"correlation_id": src.CorrelationID(),
		"occurred_at":    src.OccurredAt().Format(time.RFC3339Nano),
		"payload":        string(data),
	}

	got, err := parseMessage(values)
	if err != nil {
		t.Fatalf("parseMessage() error = %v", err)
	}
	remote, ok := got.(events.RemoteEvent)
	if !ok {
		t.Fatalf("parseMessage() returned %T, want events.RemoteEvent", got)
	}

	if remote.Type() != "widget.created" {
		t.Errorf("Type() = %q, want widget.created", remote.Type())
	}
	if remote.CorrelationID() != src.CorrelationID() {
		t.Errorf("CorrelationID() = %q, want %q", remote.CorrelationID(), src.CorrelationID())
	}
	if !remote.OccurredAt().Equal(src.OccurredAt()) {
		t.Errorf("OccurredAt() = %s, want %s", remote.OccurredAt(), src.OccurredAt())
	}
	if remote.TenantID() == nil || *remote.TenantID() != "t1" {
		t.Errorf("TenantID() = %v, want t1", remote.TenantID())
	}
	if remote.AggregateType() == nil || *remote.AggregateType() != "widget" {
		t.Errorf("AggregateType() = %v, want widget", remote.AggregateType())
	}
	if remote.AggregateID() == nil || *remote.AggregateID() != "w1" {
		t.Errorf("AggregateID() = %v, want w1", remote.AggregateID())
	}
	if string(remote.Payload) != string(data) {
		t.Errorf("Payload not preserved: got %q, want %q", remote.Payload, data)
	}
}

func TestParseMessageMissingPayload(t *testing.T) {
	_, err := parseMessage(map[string]any{"type": "widget.created"})
	if err == nil {
		t.Fatal("parseMessage() with no payload field: want error, got nil")
	}
}

// TestRemoteEventRehydratesThroughTypedHandler proves the slow path a stream
// consumer relies on: a RemoteEvent parsed off the wire decodes into the
// handler's concrete type via TypedHandler's Unmarshaler branch.
func TestRemoteEventRehydratesThroughTypedHandler(t *testing.T) {
	src := testEvent{BaseEvent: events.NewBaseEvent("widget.updated"), Data: "payload-body"}
	data, err := events.EncodeEvent(src)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}
	values := map[string]any{
		"type":           src.Type(),
		"correlation_id": src.CorrelationID(),
		"occurred_at":    src.OccurredAt().Format(time.RFC3339Nano),
		"payload":        string(data),
	}
	got, err := parseMessage(values)
	if err != nil {
		t.Fatalf("parseMessage() error = %v", err)
	}

	var received testEvent
	handler := events.TypedHandler(func(_ context.Context, e testEvent) error {
		received = e
		return nil
	})
	if err := handler(context.Background(), got); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if received.Data != "payload-body" {
		t.Errorf("decoded Data = %q, want payload-body", received.Data)
	}
	if received.Type() != "widget.updated" {
		t.Errorf("decoded Type() = %q, want widget.updated", received.Type())
	}
}

// TestBroadcastEnvelopeRoundTrip mirrors broadcastLoop's decode: envelope →
// RemoteEvent with metadata recovered from the payload.
func TestBroadcastEnvelopeRoundTrip(t *testing.T) {
	base := events.NewBaseEvent("note.created").WithTenant("t9").WithAggregate("note", "n9")
	src := testEvent{BaseEvent: base, Data: "note-body"}
	payload, err := events.EncodeEvent(src)
	if err != nil {
		t.Fatalf("EncodeEvent() error = %v", err)
	}

	env := broadcastEnvelope{
		Type:          src.Type(),
		CorrelationID: src.CorrelationID(),
		OccurredAt:    src.OccurredAt(),
		Payload:       payload,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var decoded broadcastEnvelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	tenant, aggType, aggID := events.DecodeRemoteMetadata(decoded.Payload)
	remote := events.RemoteEvent{
		EventType:   decoded.Type,
		Occurred:    decoded.OccurredAt,
		Correlation: decoded.CorrelationID,
		Payload:     decoded.Payload,
		Tenant:      tenant,
		AggType:     aggType,
		AggID:       aggID,
	}

	if remote.Type() != "note.created" {
		t.Errorf("Type() = %q, want note.created", remote.Type())
	}
	if remote.TenantID() == nil || *remote.TenantID() != "t9" {
		t.Errorf("TenantID() = %v, want t9", remote.TenantID())
	}
	if remote.AggregateID() == nil || *remote.AggregateID() != "n9" {
		t.Errorf("AggregateID() = %v, want n9", remote.AggregateID())
	}
}

func TestSubscribeAfterCloseErrors(t *testing.T) {
	b := New(dummyClient(), slog.New(slog.DiscardHandler), Options{})
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := b.Subscribe("widget.created", func(context.Context, events.Event) error { return nil }); err == nil {
		t.Error("Subscribe() after Close: want error, got nil")
	}
	if _, err := b.SubscribeBroadcast("widget.created", func(context.Context, events.Event) error { return nil }); err == nil {
		t.Error("SubscribeBroadcast() after Close: want error, got nil")
	}
}

func TestCloseIsIdempotentWithoutRedis(t *testing.T) {
	b := New(dummyClient(), slog.New(slog.DiscardHandler), Options{})
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestPortSatisfaction(t *testing.T) {
	var _ events.Bus = (*Bus)(nil)
	var _ events.Broadcaster = (*Bus)(nil)
}
