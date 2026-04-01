---
sidebar_position: 6
title: Events
---

# Infrastructure — Events

`github.com/gopernicus/gopernicus/infrastructure/events`

The events package defines the `Bus` interface (port) and core event types. Multiple implementations satisfy the interface, each with different delivery guarantees.

## Two kinds of events

Gopernicus distinguishes between **data events** and **workflow events**. Understanding which is which determines where the event should be emitted and who should subscribe.

### Data events

Data events are facts about state changes to rows in the database. They are emitted by **repositories** and fire every time the underlying data changes, regardless of why.

```
user.created              — a user row was inserted
user.updated              — a user row was modified
session.ended             — a session row was deleted
relationship.created      — an access relationship was granted
```

**Rule: a data event always fires.** If `SetEmailVerified` emits `user.email_verified`, it fires whether the caller is the Authenticator, an admin API, or a migration script. The event is a fact about data, not about the caller's intent. Subscribers can rely on this contract.

Data events are generated via the `@event` annotation in `queries.sql`:

```sql
-- @func: SetEmailVerified
-- @event: user.email_verified
UPDATE users SET email_verified = true WHERE user_id = @user_id
```

### Workflow events

Workflow events describe business flow context that goes beyond any single data change. They are emitted by **case layers** (like the Authenticator) and carry information about *why* something happened.

```
auth.verification_code_requested   — user registered, send verification email
auth.password_reset_requested      — user asked for a password reset link
auth.user_deletion_requested       — coordinate cascading deletion across stores
```

Workflow events exist because subscribers need context that no single repository has. "Send a welcome email" requires knowing this was a credentials registration, not an OAuth signup — that context lives in the Authenticator, not the users repository.

**Rule: workflow events are the exception, not the default.** Most events should be data events emitted by repositories. A workflow event is justified when:
- The event represents multi-entity orchestration with no single underlying data change (e.g. `user_deletion_requested`)
- The event carries flow-specific context that the repository doesn't have (e.g. "this was a credentials registration, use the welcome email template")

### Both can coexist

The same operation can emit both a data event and a workflow event:

```
Authenticator.Register():
  → repo.Users.Create()     → user.created                    (data, always)
  → repo.Codes.Create()     → verification_code.created       (data, always)
  → bus.Emit(auth.verification_code_requested)                (workflow, authenticator)
```

A search index subscribes to `user.created` to sync users. An email service subscribes to `auth.verification_code_requested` to send the verification email. Different subscribers, different events, no conflict.

## Choosing an approach

Gopernicus offers four levels of event delivery. The right choice depends on what happens if the event is lost.

| Approach | Delivery | Scope | Use when |
|---|---|---|---|
| [`sdk/async`](/docs/gopernicus/sdk/async) | best-effort | in-process | fire-and-forget side effects; no subscribers needed |
| `memorybus` | best-effort | in-process | pub/sub within a single instance; events lost on restart |
| `goredisbus` | at-least-once | cross-instance | multi-instance deployments; events survive restarts via Redis |
| `@event: ... outbox` | transactional | DB-backed | critical work that must survive crashes; writes in the same transaction as domain data |

## Event interface

All domain events must implement:

```go
type Event interface {
    Type() string          // e.g. "user.created", "order.completed"
    OccurredAt() time.Time
    CorrelationID() string // links related events across a request
}
```

The optional `EventWithMetadata` interface adds tenant and aggregate context:

```go
type EventWithMetadata interface {
    TenantID() *string
    AggregateType() *string
    AggregateID() *string
}
```

## BaseEvent

Embed `BaseEvent` to satisfy both interfaces automatically:

```go
type UserCreatedEvent struct {
    events.BaseEvent
    UserID string `json:"user_id"`
    Email  string `json:"email"`
}
```

### Constructors

```go
// New event with auto-generated correlation ID.
base := events.NewBaseEvent("user.created")

// Propagate correlation ID from a parent event.
base := events.NewBaseEventWithCorrelation("user.updated", parentEvent.CorrelationID())
```

### Method chaining

```go
base := events.NewBaseEvent("order.completed").
    WithTenant(tenantID).
    WithAggregate("order", orderID)
```

## Bus interface

```go
type Bus interface {
    Emit(ctx context.Context, event Event, opts ...EmitOption) error
    Subscribe(topic string, handler Handler) (Subscription, error)
    Close(ctx context.Context) error
}
```

- `Emit` — publishes to all registered handlers for that topic. Default is **async** (returns immediately; errors are logged, not returned).
- `Subscribe` — registers a handler. Use `"*"` to subscribe to all event types.
- `Close` — graceful shutdown; waits for in-flight events up to the context deadline.

### Subscription

```go
sub, err := bus.Subscribe("user.created", handler)
// ...
sub.Unsubscribe() // safe to call multiple times
```

## Handler

```go
type Handler func(ctx context.Context, event Event) error
```

Handlers should be idempotent — at-least-once buses (Redis, outbox) may deliver the same event more than once.

### TypedHandler

Wraps a handler so it only fires for a specific event type. Works across all bus implementations.

```go
handler := events.TypedHandler(func(ctx context.Context, e UserCreatedEvent) error {
    return sendWelcomeEmail(e.Email)
})
bus.Subscribe("user.created", handler)
```

When events arrive as raw bytes (from Redis Streams or the outbox), `TypedHandler` uses the `Unmarshaler` interface to deserialize before calling your function. You don't need to handle this yourself.

## Emit options

| Option | Description |
|---|---|
| `events.WithSync()` | Block until all handlers complete |
| `events.WithPriority(n int)` | Processing priority for async events (higher = first) |

```go
// Wait for handlers — useful in tests or when you need the side effect before returning.
bus.Emit(ctx, event, events.WithSync())
```

## Implementations

### memorybus

In-process pub/sub with a background worker pool. No external dependencies.

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/memorybus"

bus := memorybus.New(log)
defer bus.Close(ctx)
```

**Options:**

```go
bus := memorybus.New(log,
    memorybus.WithWorkerCount(8),   // default: 4
    memorybus.WithQueueSize(5000),  // default: 1000
)
```

**Trade-offs:** Events are delivered to in-process subscribers only. If the queue fills up or the process restarts, events are dropped. Appropriate for side effects that are acceptable to lose (e.g. cache invalidation, metrics, non-critical notifications).

### goredisbus

Redis Streams-backed bus. Events survive process restarts and are shared across all instances in the same consumer group.

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/goredisbus"

var opts goredisbus.Options
environment.ParseEnvTags("MYAPP", &opts)

bus := goredisbus.New(rdb, log, opts)
defer bus.Close(ctx)
```

**Environment variables** (prefix configurable via `ParseEnvTags`):

| Variable | Default | Description |
|---|---|---|
| `EVENT_BUS_STREAM_PREFIX` | `events:` | Redis key prefix for streams |
| `EVENT_BUS_CONSUMER_GROUP` | `default` | Consumer group name; instances with the same group share load |
| `EVENT_BUS_WORKERS` | `4` | Concurrent message processors |
| `EVENT_BUS_BLOCK_TIMEOUT` | `5s` | How long XREADGROUP blocks waiting for messages |
| `EVENT_BUS_BATCH_SIZE` | `10` | Max messages per XREADGROUP call |
| `EVENT_BUS_MAX_LEN` | `0` | Approximate max entries per stream; 0 means no trimming |

**Stream retention:** By default, Redis streams grow unbounded — consumed messages remain in the stream after acknowledgment. Set `EVENT_BUS_MAX_LEN` to cap each stream at an approximate number of entries. Redis trims old entries inline with each XADD using approximate trimming (`MAXLEN ~`), which is O(1). For example, `EVENT_BUS_MAX_LEN=10000` keeps roughly the last 10,000 entries per stream.

**Trade-offs:** Requires Redis. Events are durable and distributed across instances, but delivery depends on Redis availability. Use `TypedHandler` to deserialize events received from the stream.

### NoopBus

A bus that does nothing. Use as a default when events are disabled or not yet configured.

```go
bus := events.NewNoopBus()
```

## EventRegistry

The registry is a pattern-based router for **raw event bytes**. It solves a different problem than `bus.Subscribe`.

When an event leaves your process — into a database, Redis, or an external queue — it becomes bytes. When it comes back, something has to look at the type string and decide which handler to call. That's the registry: `"user.created" + []byte → right handler`.

```go
registry := events.NewEventRegistry(events.WithLogger(log))

// Exact match
registry.Register("user.created", handleUserCreated)

// Prefix match — handles "email.verification", "email.reset", etc.
registry.Register("email.*", handleAllEmailEvents)

// Wildcard catch-all
registry.Register("*", handleAny)

err := registry.Handle(ctx, "user.created", payload)
```

Logging is opt-in via `WithLogger`. When configured, the registry logs handler registrations. Without it, the registry operates silently.

**Pattern priority:** exact match → longest prefix match → wildcard.

The `EventHandler` signature used by the registry is distinct from the bus `Handler`:

```go
// Bus handler — receives a Go Event object
type Handler func(ctx context.Context, event Event) error

// Registry handler — receives raw bytes
type EventHandler func(ctx context.Context, eventType string, payload json.RawMessage) error
```

## Transactional outbox

The transactional outbox pattern guarantees that a domain event is recorded atomically with the business write that caused it. If the business write commits, the event is guaranteed to exist. If it rolls back, the event is never visible.

### How it works

1. The `@event` annotation in `queries.sql` with the `outbox` modifier tells the generator to route the event through the outbox instead of the bus:

```sql
-- @func: SetEmailVerified
-- @event: user.email_verified outbox
UPDATE users SET email_verified = true WHERE user_id = @user_id
```

2. The generator produces code across two layers:
   - **Repository** (domain layer) — creates a typed event struct (`UserEmailVerifiedEvent`), serializes it with `events.EncodeEvent`, and passes an `events.OutboxEvent` to the store.
   - **Store** (infrastructure layer) — wraps the business query in `pgxdb.InTx`, executes the UPDATE, and atomically inserts the serialized event into the `event_outbox` table via `pgxdb.InsertOutboxEvent`.

3. A **poller** (`infrastructure/events/poller`) reads committed outbox rows and publishes them to the normal event bus. Subscribers receive the event via `TypedHandler` as usual — they don't know the event went through an outbox.

### Comparison with `@event` (bus)

```sql
-- Bus delivery — emitted after the store call returns, not transactional.
-- @event: user.created

-- Outbox delivery — written atomically with the business query.
-- @event: user.email_verified outbox
```

Both annotations generate typed event structs. The only difference is the delivery path: bus events are emitted immediately at the repository level; outbox events are persisted in the same transaction and published later by the poller.

### Manual outbox writes

For custom store methods or writable CTEs, use `pgxdb.InsertOutboxEvent` directly inside a transaction:

```go
pgxdb.InTx(ctx, s.db, func(tx pgx.Tx) error {
    // business write
    tx.Exec(ctx, `UPDATE users SET ...`, args)
    // atomic outbox write
    return pgxdb.InsertOutboxEvent(ctx, tx, pgxdb.OutboxEvent{
        Type:    "user.email_verified",
        Payload: payload,
    })
})
```

## Test utilities

`eventstest.RunSuite` runs a compliance test suite against any `Bus` implementation:

```go
import "github.com/gopernicus/gopernicus/infrastructure/events/eventstest"

func TestBusCompliance(t *testing.T) {
    bus := memorybus.New(slog.Default())
    defer bus.Close(context.Background())
    eventstest.RunSuite(t, bus)
}
```

The suite covers: emit and subscribe, wildcard subscriptions, and unsubscribe behaviour.

## Event encoding

Events can implement `EventEncoder` for custom serialization (protobuf, msgpack, etc.). Without it, `EncodeEvent` falls back to JSON.

```go
data, err := events.EncodeEvent(event)
```

## See also

- [SDK / Async](/docs/gopernicus/sdk/async) — simpler fire-and-forget without pub/sub
- [SDK / Workers](/docs/gopernicus/sdk/workers) — worker pool that powers the outbox poller and job queue
- [Infrastructure / Database](/docs/gopernicus/infrastructure/database) — outbox and job queue tables live alongside domain data
