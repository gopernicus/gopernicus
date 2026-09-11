// Package events provides typed notifications, subscriptions and transport
// envelopes. Emit is bounded asynchronous notification; Memory.Dispatch waits
// for local handlers. A remote integration's checked publication confirms only
// its documented handoff. Durable intent belongs in a transactional outbox.
package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

var ids = sdk.IDGenerator{}

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
// consumers can filter on. BaseEvent satisfies it; routing policy stays host-owned.
type Metadata interface {
	AggregateType() *string
	AggregateID() *string
	TenantID() *string
}

// BaseEvent provides the common Event fields. Embed it in an event type to
// satisfy Event (and Metadata via the optional aggregate/tenant fields). Its
// JSON tags also describe the optional fields in its ordinary typed payload.
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
// and a fresh correlation ID from sdk.IDGenerator.
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

// Handler processes an immutable event and must support concurrent calls.
// Implementations MUST be idempotent: an event may
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

// Subscription removes a registration. Calls already selected may finish;
// Unsubscribe is idempotent and does not wait for callbacks.
type Subscription interface{ Unsubscribe() error }

// Emitter admits bounded asynchronous notification. Closed/capacity/validation
// errors mean no admission. Success is not persistence or handler completion.
// Accepted work may outlive the caller; events and referenced data must remain
// immutable. There is no total ordering across events.
type Emitter interface {
	Emit(context.Context, Event) error
}

// Subscriber receives notifications for an exact topic or "*" (all topics).
// Every matching local subscription receives its own invocation. A distributed
// work consumer group has a separate integration-specific API.
type Subscriber interface {
	Subscribe(string, Handler) (Subscription, error)
}

// Bus combines notification and subscription lifecycle. Close refuses new work,
// drains admitted work, and waits with each caller's context. It returns the
// context error if unfinished; repeated calls wait for the same completion.
// Hosts own Close; a callback must not wait for its own bus to finish draining.
type Bus interface {
	Emitter
	Subscriber
	Close(context.Context) error
}

// Broadcaster explicitly selects notification fanout on every connected process.
// Delivery is ephemeral: disconnected/slow consumers may miss notifications.
// Bundled Memory and Redis notification subscriptions already use this behavior.
type Broadcaster interface {
	SubscribeBroadcast(string, Handler) (Subscription, error)
}

// Identified carries stable per-event identity across replay/publication.
// CorrelationID groups related events and is not a deduplication key.
type Identified interface{ EventID() string }

var (
	ErrClosed       = fmt.Errorf("events: bus closed: %w", sdk.ErrUnavailable)
	ErrCapacity     = fmt.Errorf("events: notification queue full: %w", sdk.ErrUnavailable)
	ErrHandlerPanic = errors.New("events: handler panicked")
)

// ValidateEvent checks the generic notification boundary. Typed-nil values or
// panicking custom Event methods are caller misuse, not reflection-detected.
func ValidateEvent(event Event) error {
	if event == nil {
		return fmt.Errorf("events: nil event: %w", sdk.ErrInvalidInput)
	}
	if event.Type() == "" || event.Type() == "*" {
		return fmt.Errorf("events: event type must be nonempty and not '*': %w", sdk.ErrInvalidInput)
	}
	return nil
}

// ValidateSubscription checks notification topics and handler presence.
func ValidateSubscription(topic string, handler Handler) error {
	if topic == "" || handler == nil {
		return fmt.Errorf("events: subscription needs a topic and handler: %w", sdk.ErrInvalidInput)
	}
	return nil
}
