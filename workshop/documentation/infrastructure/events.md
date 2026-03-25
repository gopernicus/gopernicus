# infrastructure/events -- Event Bus Reference

Package `events` provides domain event bus infrastructure with pluggable backends, typed handlers, and support for both synchronous and asynchronous dispatch.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/events`

## Event Interface

All domain events must implement:

```go
type Event interface {
    Type() string            // e.g. "user.created", "order.completed"
    OccurredAt() time.Time
    CorrelationID() string   // traces related events across a request
}
```

Optional `EventWithMetadata` adds tenant and aggregate metadata:

```go
type EventWithMetadata interface {
    TenantID() *string
    AggregateType() *string
    AggregateID() *string
}
```

## BaseEvent

Embed `BaseEvent` to satisfy both `Event` and `EventWithMetadata` automatically.

```go
type UserCreatedEvent struct {
    events.BaseEvent
    UserID string `json:"user_id"`
    Email  string `json:"email"`
}
```

### Constructors

```go
base := events.NewBaseEvent("user.created")
base := events.NewBaseEventWithCorrelation("user.updated", parentCorrelationID)
```

### Method Chaining

```go
base := events.NewBaseEvent("user.created").
    WithTenant(tenantID).
    WithAggregate("user", userID).
    ToOutbox() // marks for outbox persistence
```

## Bus Interface

The port for publishing and subscribing to events.

```go
type Bus interface {
    Emit(ctx context.Context, event Event, opts ...EmitOption) error
    Subscribe(topic string, handler Handler) (Subscription, error)
    Close(ctx context.Context) error
}
```

- `Emit` -- publishes to all registered handlers. Default is async (returns immediately, errors logged).
- `Subscribe` -- registers a handler for events of the given topic. Use `"*"` for wildcard.
- `Close` -- graceful shutdown, waits for pending async events.

### Subscription

```go
sub, err := bus.Subscribe("user.created", handler)
sub.Unsubscribe() // safe to call multiple times
```

## Handler

```go
type Handler func(ctx context.Context, event Event) error
```

Handlers should be idempotent since events may be delivered more than once.

### TypedHandler

Creates a type-safe handler that only processes events matching type `T`:

```go
handler := events.TypedHandler(func(ctx context.Context, e UserCreatedEvent) error {
    return sendWelcomeEmail(e.Email)
})
bus.Subscribe("user.created", handler)
```

Events of other types are silently ignored.

## Emit Options

| Option | Description |
|---|---|
| `WithSync()` | Dispatch synchronously; waits for all handlers to complete |
| `WithPriority(p int)` | Processing priority for async events (higher = higher priority) |

```go
bus.Emit(ctx, event, events.WithSync())
bus.Emit(ctx, event, events.WithPriority(10))
```

## Bus Implementations

### memorybus

In-memory bus with async dispatch via goroutines. Suitable for single-instance deployments.

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/memorybus"

bus := memorybus.New()
```

### goredisbus (Redis Streams)

Durable event bus using Redis Streams. Supports multi-instance deployments with consumer groups.

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/goredisbus"

bus := goredisbus.New(redisClient, opts...)
```

### NewNoopBus

A bus that does nothing. Use as a default when events are disabled.

```go
bus := events.NewNoopBus()
```

## EventRegistry

Routes events to handlers based on type patterns. Used by outbox processors and queue consumers.

```go
registry := events.NewEventRegistry(log)
registry.Register("email.verification_code", handleVerification)
registry.Register("email.*", handleAllEmailEvents) // prefix match
registry.Register("*", handleAny)                  // wildcard

err := registry.Handle(ctx, "email.verification_code", payload)
```

Pattern priority: exact match > longest prefix match > wildcard.

## Outbox Pattern

Events marked with `.ToOutbox()` are persisted to a database outbox table within the same transaction as the domain operation. A background processor reads the outbox and dispatches events.

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/outbox"
```

This ensures at-least-once delivery even if the event bus is temporarily unavailable.

## Event Encoding

Events can implement `EventEncoder` for custom serialization, or fall back to JSON:

```go
data, err := events.EncodeEvent(event)
```

## Related

- [infrastructure/database](../infrastructure/database.md) -- outbox table lives alongside domain data
- [sdk/errs](../sdk/errs.md) -- domain errors that handlers may return
