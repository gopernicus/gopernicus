---
title: Events
description: Best-effort events, durable outbox delivery, and authenticated SSE streams.
---

# Events

`pockets/events` builds a durable outbox and authenticated SSE gateway on top of the SDK event-bus vocabulary. The event bus and the pocket are different layers: a host may use the bus without the outbox/SSE pocket.

Public services live in `logic/streams` and `logic/outbox`; HTTP lives in
`inbound/http`. `events.New` assembles named components, while hosts may construct
services and adapters directly. The streams service enforces event visibility
before projection, including when consumed without HTTP. The raw fan-out hub is
private. `components.HTTP()` returns the validated adapter; `SubjectStream()`
and `ResourceStream()` provide handlers for host routes with configured middleware.

## Routes

| Route | Registration | Behavior |
|---|---|---|
| `GET /events` | explicit `WithVisibility` required | stream for the authenticated subject; optional exact `types` filter |
| `GET /events/{resource_type}/{resource_id}` | only with `WithAuthorization` | resource-scoped stream |

Routes carry SSE/JSON only and can be safely mounted behind a registrar prefix.

## Construction

```go
bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

eventsSvc, err := events.New(bus,
    events.WithOutbox(outboxRepo), // optional; the host runs the poller
    events.WithLogger(log),
    events.WithVisibility(eventVisibleToPrincipal),
    events.WithStreamMiddleware(authSvc.HTTP.RequireAccessToken()),
    events.WithAuthorization(authorizeStream))
if err != nil {
    return err
}

defer eventsSvc.Close()

if err := eventsSvc.Register(pockets.Mount{
    Router: router,
    Logger: log,
}); err != nil {
    return err
}
```

`Bus` is required. `Register` requires a nonnil router and an explicit `Visible`
policy, returning `sdk.ErrInvalidInput` before mounting any routes if either is
absent. `Visible(context.Context, sdk.Principal, sdkevents.Event) (bool, error)`
runs after type/resource filters and before projection. Denial or error suppresses
the event; errors are logged through `WithLogger`. Hosts may explicitly allow
all events for a shared authenticated feed. The framework does not infer tenant
ownership from matching aggregate IDs. Middleware is copied when the option is created and remains fixed across constructors.

 Stream middleware must store an `sdk.Principal`; without one the handler fails closed with 401. `Authorize == nil` removes the resource route rather than allowing it.

## Two delivery guarantees

| Rail | How it enters | Guarantee | Good use |
|---|---|---|---|
| best effort | `Mount.Events.Emit` after a domain write | asynchronous admission; crashes, disconnection or delivery failure can lose it | re-fetch hints, live UI, cache invalidation |
| durable | outbox record in the same repository transaction as domain rows | retry until the host's checked handoff succeeds; duplicates possible | required observation and external reactions with de-duplication |

Do not describe `Mount.Events` as transactional. Durable records travel through pocket repositories, not the mount emitter.

## Outbox poller

The host supplies a `DeliverFunc(context.Context, sdkevents.Event) error` to
`NewPoller` and runs its `Poll` method on a `workers.Pool`. Choose the handoff that
must succeed before marking the outbox entry published:

```go
// Required local subscribers, such as a handler that enqueues a durable job.
// Register all required handlers before starting this poller.
poller, err := outbox.NewPoller(outboxRepo, bus.Dispatch)
if err != nil {
    return err
}
```

For a Redis handoff, use `outbox.NewPoller(outboxRepo, redisBus.Publish)`.
Memory.Dispatch checks completion of the selected local handlers. Redis.Publish
checks Redis stream acceptance and attempts best-effort notification; it does not
wait for local or remote handlers. Redis work handlers must register through
`SubscribeWork(ctx, exactTopic, handler)`. Ordinary Subscribe uses ephemeral
notification fanout, suitable for SSE.

Do not pass asynchronous Emit or wrap a no-op emitter as checked delivery.
The poller cannot infer what an arbitrary callback guarantees. A nil callback or repository
fails with `sdk.ErrInvalidInput`. Delivery failures or cancellation leave entries
unpublished; a crash or mark failure after successful delivery can cause replay.
`WithBatchSize` remains available for the poller's batch size (default 100).

Replay uses `entry.Record.Event()`, preserving the stable EventID, metadata and
payload. Consumers acting on durable events de-duplicate by EventID, not by
CorrelationID, which several events may share. PostgreSQL payload storage now requires migration
`0002_event_outbox_payload_bytes.sql`; apply it before new writers. Both stores
preserve empty/binary payloads and validate identity/type before writing the batch.
Existing malformed records can block the oldest-first poller; monitor failures and
repair or quarantine those records explicitly, preserving stable IDs for replay.

The current poller assumes one poller per outbox. It does not claim or lease
batches. Stop HTTP streams and the poller, close the gateway with
`eventsSvc.Close()`, then close the bus and host-owned clients. Gateway Close is
idempotent, only releases its subscription, and rejects later registration; it
does not shut down HTTP or the bus. Closed checked-delivery methods return `events.ErrClosed`,
and the poller does not mark those entries. Review the
[Redis integration's cutover and retention rules](../integrations/catalog.md#redis)
when moving an existing outbox onto the new transport.

## SSE posture

- default projection exposes metadata only; raw payloads require an explicit `Projector`;
- slow clients drop overflow because SSE is a wake-up channel, not an authoritative queue;
- connection age defaults to 15 minutes and cannot be disabled, bounding revocation latency;
- concurrent streams are capped per subject;
- identity and resource authorization run at connect time; `Visible` evaluates every event;
- visibility and projection run per connection, may run concurrently, and must not mutate events;
- visibility must honor cancellation; policy/projection panics deny the event and are logged.

Clients should re-fetch authoritative state after a wake-up rather than treating every frame as a complete state transition.

## Events observe work

Never put a best-effort event in front of work that must happen. Authentication submits delivery directly to jobs and may emit lifecycle events afterward for observation. Durable side effects belong in a transactional outbox or durable work queue.

## Stores

The pgx and Turso store modules own the `event_outbox` table and probe it at construction. Export the `events` migration source to the host ledger. A nil outbox repository is valid direct-only mode and requires no poller.

`Append` uses its own transaction. Use `AppendTx` in the store adapter when domain
rows and outbox rows must commit together; context alone does not join an existing
transaction. PostgreSQL migration 0002 converts historical JSON text to bytea and
requires old writers to stop before applying it. Earlier `{}` normalization of
empty input cannot be reversed. Turso uses BLOB bindings in its existing column
and still reads legacy TEXT rows. Apply its matching no-op 0002 migration to
retain the shared version set; it requires no schema rebuild.
