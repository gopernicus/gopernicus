// Package events is the event-bus facility port: the vocabulary an emitter and
// its consumers share, plus the in-process Memory default and a Noop.
//
// An Event is a typed value that flows through a Bus in-process. Bus backends
// (the shipped Memory, the redis-streams integration) satisfy the port; a
// conformance suite (sdk/capabilities/events/eventstest) pins the common observable
// contract. The bus knows zero CMS/auth concepts — features emit their own
// typed events and consumers subscribe by topic.
//
// Delivery is deliberately weak. The Memory bus is at-most-once, in-process,
// with no persistence and no replay: Emit is a best-effort wake-up signal, NOT
// a transactional write. A caller that must not lose an event rides the durable
// outbox rail (a Record persisted in the same transaction as its domain rows),
// never Emit. Because delivery can drop or duplicate depending on the backend,
// Handler implementations must be idempotent.
//
// This package is stdlib-only apart from sdk/foundation/cryptids (intra-module); a
// distributed backend is a separate integration module.
package events

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

var ids = cryptids.IDGenerator{}

// =============================================================================
// Event vocabulary
// =============================================================================

// Event is what flows through a Bus in-process: a typed value.
type Event interface {
	// Type returns the event type string, by convention lowercase and
	// dot-separated as domain.action (e.g. "content.published").
	Type() string

	// OccurredAt returns when the event occurred.
	OccurredAt() time.Time

	// CorrelationID identifies related events: events emitted while handling
	// one request share a correlation ID.
	CorrelationID() string
}

// Metadata is an optional Event capability carrying the routing metadata the
// SSE gateway filters on. BaseEvent satisfies it. TenantID is vocabulary only —
// nothing filters by tenant until tenancy exists (auth v2+).
type Metadata interface {
	AggregateType() *string
	AggregateID() *string
	TenantID() *string
}

// BaseEvent provides the common Event fields. Embed it in an event type to
// satisfy Event (and Metadata via the optional aggregate/tenant fields). Its
// json tags define the wire shape DecodeRemoteMetadata probes.
type BaseEvent struct {
	EventType   string    `json:"type"`
	Occurred    time.Time `json:"occurred_at"`
	Correlation string    `json:"correlation_id"`

	// Optional routing metadata for durable/remote processing.
	Tenant  *string `json:"tenant_id,omitempty"`
	AggType *string `json:"aggregate_type,omitempty"`
	AggID   *string `json:"aggregate_id,omitempty"`
}

var (
	_ Event    = BaseEvent{}
	_ Metadata = BaseEvent{}
)

// Type implements Event.
func (e BaseEvent) Type() string { return e.EventType }

// OccurredAt implements Event.
func (e BaseEvent) OccurredAt() time.Time { return e.Occurred }

// CorrelationID implements Event.
func (e BaseEvent) CorrelationID() string { return e.Correlation }

// AggregateType implements Metadata (nil when unset).
func (e BaseEvent) AggregateType() *string { return e.AggType }

// AggregateID implements Metadata (nil when unset).
func (e BaseEvent) AggregateID() *string { return e.AggID }

// TenantID implements Metadata (nil when unset).
func (e BaseEvent) TenantID() *string { return e.Tenant }

// NewBaseEvent builds a BaseEvent with the given type, the current UTC time,
// and a fresh correlation ID from sdk/id.
func NewBaseEvent(eventType string) BaseEvent {
	return BaseEvent{
		EventType:   eventType,
		Occurred:    time.Now().UTC(),
		Correlation: ids.MustGenerate(),
	}
}

// NewBaseEventWithCorrelation builds a BaseEvent with an explicit correlation
// ID — use it when an event should be traced together with a parent event.
func NewBaseEventWithCorrelation(eventType, correlationID string) BaseEvent {
	return BaseEvent{
		EventType:   eventType,
		Occurred:    time.Now().UTC(),
		Correlation: correlationID,
	}
}

// WithTenant returns a copy of the event with the tenant ID set.
func (e BaseEvent) WithTenant(tenantID string) BaseEvent {
	e.Tenant = &tenantID
	return e
}

// WithAggregate returns a copy of the event with the aggregate type and ID set.
func (e BaseEvent) WithAggregate(aggregateType, aggregateID string) BaseEvent {
	e.AggType = &aggregateType
	e.AggID = &aggregateID
	return e
}

// =============================================================================
// Handlers
// =============================================================================

// Handler processes an event. Implementations MUST be idempotent: an event may
// be delivered more than once (the durable rail is at-least-once; a remote
// backend may redeliver), and a best-effort event may be dropped entirely.
type Handler func(ctx context.Context, event Event) error

// Unmarshaler is an optional interface an event may implement to be
// deserialized from an encoded form (a RemoteEvent read off Redis Streams or
// the outbox). TypedHandler uses it as the fallback when the direct type
// assertion fails.
type Unmarshaler interface {
	Unmarshal(target any) error
}

// TypedHandler adapts a type-safe handler to the untyped Handler the bus
// dispatches. It has two paths:
//
//   - Fast path — the event is already a T (the in-process Memory bus, which
//     dispatches the original typed value); the handler is called directly.
//   - Slow path — the event was rehydrated from a wire/durable form and only
//     implements Unmarshaler (a RemoteEvent); the payload is decoded into a
//     zero-value T and the handler is called with the result.
//
// An event that is neither a T nor an Unmarshaler is ignored.
func TypedHandler[T Event](fn func(ctx context.Context, event T) error) Handler {
	return func(ctx context.Context, event Event) error {
		if typed, ok := event.(T); ok {
			return fn(ctx, typed)
		}
		if u, ok := event.(Unmarshaler); ok {
			var typed T
			if err := u.Unmarshal(&typed); err != nil {
				return err
			}
			return fn(ctx, typed)
		}
		return nil
	}
}

// =============================================================================
// Ports
// =============================================================================

// Subscription is an active subscription that can be cancelled.
type Subscription interface {
	// Unsubscribe removes this subscription. It is safe to call more than once
	// (subsequent calls are no-ops).
	Unsubscribe() error
}

// Emitter is the narrow emit-only port — what Mount.Events carries.
//
// Emit is best-effort: on the Memory bus it is at-most-once, in-process, and
// returns without waiting for handlers by default. It is NOT transactional and
// NOT durable; an event lost between a domain commit and Emit is simply gone.
// Work that must not be lost belongs on the outbox rail, not here.
type Emitter interface {
	Emit(ctx context.Context, event Event, opts ...EmitOption) error
}

// Bus is the full port a bus backend satisfies and the events feature consumes.
type Bus interface {
	Emitter

	// Subscribe registers a handler for an exact topic, or "*" for every event.
	Subscribe(topic string, handler Handler) (Subscription, error)

	// Close shuts the bus down, draining in-flight async handlers up to the
	// context deadline. Close is idempotent.
	Close(ctx context.Context) error
}

// Broadcaster is an optional Bus capability: SubscribeBroadcast delivers every
// matching event to this handler on EVERY process — fan-out with no durability
// or replay, for ephemeral consumers (SSE streams, metrics) that reconnect and
// re-fetch. Memory satisfies it trivially (one process); a distributed backend
// distinguishes consumer-group Subscribe (one process) from broadcast.
type Broadcaster interface {
	SubscribeBroadcast(topic string, handler Handler) (Subscription, error)
}

// =============================================================================
// Emit options
// =============================================================================

// EmitConfig is the resolved configuration for a single Emit call.
type EmitConfig struct {
	// Sync dispatches synchronously and waits for handlers to complete before
	// Emit returns, propagating the first handler error.
	Sync bool
}

// EmitOption configures a single Emit call.
type EmitOption func(*EmitConfig)

// WithSync dispatches the event synchronously and waits for handlers — used for
// deterministic tests and same-request flows that need the handler to have run
// before Emit returns.
func WithSync() EmitOption {
	return func(c *EmitConfig) { c.Sync = true }
}

// ApplyOptions resolves options into an EmitConfig. Bus backends call it to
// honor WithSync consistently.
func ApplyOptions(opts ...EmitOption) EmitConfig {
	var cfg EmitConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
