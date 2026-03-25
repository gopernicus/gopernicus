// Package events provides domain event bus infrastructure.
// It defines the Bus interface (port) and Event types that bus implementations satisfy.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Event Interface & Base Types
// =============================================================================

// Event is the interface all domain events must implement.
type Event interface {
	// Type returns the event type string (e.g., "user.created", "order.completed").
	// Convention: lowercase, dot-separated domain.action format.
	Type() string

	// OccurredAt returns when the event occurred.
	OccurredAt() time.Time

	// CorrelationID returns an identifier for tracing related events.
	// Multiple events from the same request share a correlation ID.
	CorrelationID() string
}

// EventWithMetadata is an optional interface for events that carry
// tenant and aggregate metadata. Events embedding BaseEvent automatically
// satisfy this interface via BaseEvent's methods.
type EventWithMetadata interface {
	TenantID() *string
	AggregateType() *string
	AggregateID() *string
}

// BaseEvent provides common fields for all events.
// Embed this in your event types to satisfy the Event interface.
type BaseEvent struct {
	EventType   string    `json:"type"`
	Occurred    time.Time `json:"occurred_at"`
	Correlation string    `json:"correlation_id"`

	// Optional metadata for durable event processing.
	Tenant  *string `json:"tenant_id,omitempty"`
	AggType *string `json:"aggregate_type,omitempty"`
	AggID   *string `json:"aggregate_id,omitempty"`

	// outbox marks this event for persistence to an event outbox table.
	outbox bool
}

// Type implements Event.
func (e BaseEvent) Type() string {
	return e.EventType
}

// OccurredAt implements Event.
func (e BaseEvent) OccurredAt() time.Time {
	return e.Occurred
}

// CorrelationID implements Event.
func (e BaseEvent) CorrelationID() string {
	return e.Correlation
}

// TenantID returns the tenant ID for routing (nil if not set).
func (e BaseEvent) TenantID() *string {
	return e.Tenant
}

// AggregateType returns the aggregate type (nil if not set).
func (e BaseEvent) AggregateType() *string {
	return e.AggType
}

// AggregateID returns the aggregate ID (nil if not set).
func (e BaseEvent) AggregateID() *string {
	return e.AggID
}

// GenerateID creates unique identifiers for event correlation.
// Override this to use a custom ID generator (e.g., cryptids.GenerateID).
// Defaults to a 16-byte crypto/rand hex-encoded string.
var GenerateID = defaultGenerateID

func defaultGenerateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// NewBaseEvent creates a BaseEvent with the given type and current timestamp.
func NewBaseEvent(eventType string) BaseEvent {
	id, err := GenerateID()
	if err != nil {
		id = fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return BaseEvent{
		EventType:   eventType,
		Occurred:    time.Now().UTC(),
		Correlation: id,
	}
}

// NewBaseEventWithCorrelation creates a BaseEvent with an explicit correlation ID.
// Use this when emitting events that should be traced together with a parent event.
func NewBaseEventWithCorrelation(eventType, correlationID string) BaseEvent {
	return BaseEvent{
		EventType:   eventType,
		Occurred:    time.Now().UTC(),
		Correlation: correlationID,
	}
}

// WithTenant sets the tenant ID on a BaseEvent (for method chaining).
func (e BaseEvent) WithTenant(tenantID string) BaseEvent {
	e.Tenant = &tenantID
	return e
}

// WithAggregate sets the aggregate type and ID on a BaseEvent (for method chaining).
func (e BaseEvent) WithAggregate(aggregateType, aggregateID string) BaseEvent {
	e.AggType = &aggregateType
	e.AggID = &aggregateID
	return e
}

// ToOutbox marks this event for persistence to the event outbox table.
func (e BaseEvent) ToOutbox() BaseEvent {
	e.outbox = true
	return e
}

// IsOutbox returns true if this event should be persisted to the outbox.
func (e BaseEvent) IsOutbox() bool {
	return e.outbox
}

// =============================================================================
// Event Encoding
// =============================================================================

// EventEncoder is an optional interface for custom event serialization.
// Events can implement this for protobuf, msgpack, or other encodings.
// If not implemented, EncodeEvent falls back to json.Marshal.
type EventEncoder interface {
	EncodeEvent() ([]byte, error)
}

// EncodeEvent serializes an event for transport (logging, external brokers).
// If the event implements EventEncoder, uses that. Otherwise falls back to JSON.
func EncodeEvent(event Event) ([]byte, error) {
	if enc, ok := event.(EventEncoder); ok {
		return enc.EncodeEvent()
	}
	return json.Marshal(event)
}

// =============================================================================
// Bus Interface (Port)
// =============================================================================

// Bus is the interface for publishing and subscribing to events.
// Bus implementations (memory, Kafka, NATS) satisfy this interface.
type Bus interface {
	// Emit publishes an event to all registered handlers for that event type.
	// Options control sync/async behavior and other settings.
	// Default is async — returns immediately, errors logged not returned.
	Emit(ctx context.Context, event Event, opts ...EmitOption) error

	// Subscribe registers a handler for events of the given topic.
	// Use "*" to subscribe to all event types (wildcard).
	// Returns a Subscription handle for clean unsubscription.
	Subscribe(topic string, handler Handler) (Subscription, error)

	// Close gracefully shuts down the bus.
	// Waits for pending async events to complete up to context deadline.
	Close(ctx context.Context) error
}

// Subscription represents an active subscription that can be cancelled.
type Subscription interface {
	// Unsubscribe removes this subscription from the bus.
	// Safe to call multiple times (subsequent calls are no-ops).
	Unsubscribe() error
}

// =============================================================================
// Handler
// =============================================================================

// Handler processes events. Implementations should be idempotent
// since events may be delivered more than once in some scenarios.
type Handler func(ctx context.Context, event Event) error

// Unmarshaler is an optional interface that events may implement to allow
// deserialization from a serialized form (e.g., Redis Streams, outbox).
// TypedHandler uses this as a fallback when the direct type assertion fails.
type Unmarshaler interface {
	Unmarshal(target any) error
}

// TypedHandler creates a type-safe handler for a specific event type.
// If the event matches T directly, the handler is called immediately.
// Otherwise, if the event implements [Unmarshaler] (e.g., events read from
// Redis Streams), the payload is deserialized into a zero-value T and the
// handler is called with the result.
//
// Example:
//
//	handler := events.TypedHandler(func(ctx context.Context, e UserCreatedEvent) error {
//	    return sendWelcomeEmail(e.Email)
//	})
//	bus.Subscribe("user.created", handler)
func TypedHandler[T Event](fn func(ctx context.Context, event T) error) Handler {
	return func(ctx context.Context, event Event) error {
		// Fast path: direct type assertion (in-memory bus).
		if typed, ok := event.(T); ok {
			return fn(ctx, typed)
		}

		// Slow path: deserialize from payload (Redis Streams, outbox).
		if u, ok := event.(Unmarshaler); ok {
			var typed T
			if err := u.Unmarshal(&typed); err != nil {
				return fmt.Errorf("typed handler unmarshal: %w", err)
			}
			return fn(ctx, typed)
		}

		return nil
	}
}

// =============================================================================
// Emit Options
// =============================================================================

// EmitConfig holds configuration for a single Emit call.
type EmitConfig struct {
	// Sync dispatches the event synchronously and waits for handlers.
	Sync bool

	// Priority affects processing order for async events.
	// Higher values = higher priority. Default is 0.
	Priority int
}

// EmitOption configures an Emit call.
type EmitOption func(*EmitConfig)

// WithSync dispatches the event synchronously and waits for handlers.
func WithSync() EmitOption {
	return func(c *EmitConfig) {
		c.Sync = true
	}
}

// WithPriority sets the processing priority for async events.
func WithPriority(p int) EmitOption {
	return func(c *EmitConfig) {
		c.Priority = p
	}
}

// ApplyOptions applies all options to a config and returns it.
func ApplyOptions(opts ...EmitOption) EmitConfig {
	cfg := EmitConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// =============================================================================
// Noop Bus (convenience)
// =============================================================================

// NewNoopBus returns a Bus that does nothing.
// Use this as a default when events are disabled or not configured.
func NewNoopBus() Bus {
	return &noopBus{}
}

type noopBus struct{}

func (n *noopBus) Emit(_ context.Context, _ Event, _ ...EmitOption) error {
	return nil
}

func (n *noopBus) Subscribe(_ string, _ Handler) (Subscription, error) {
	return &noopSubscription{}, nil
}

func (n *noopBus) Close(_ context.Context) error {
	return nil
}

type noopSubscription struct{}

func (n *noopSubscription) Unsubscribe() error {
	return nil
}
