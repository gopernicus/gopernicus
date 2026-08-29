---
title: Events
description: Best-effort events, durable outbox delivery, and authenticated SSE streams.
---

# Events

`pockets/events` builds a durable outbox and authenticated SSE gateway on top of the SDK event-bus vocabulary. The event bus and the pocket are different layers: a host may use the bus without the outbox/SSE pocket.

## Routes

| Route | Registration | Behavior |
|---|---|---|
| `GET /events` | always | stream for the authenticated subject; optional exact `types` filter |
| `GET /events/{resource_type}/{resource_id}` | only with `Config.Authorize` | resource-scoped stream |

Routes carry SSE/JSON only and can be safely mounted behind a registrar prefix.

## Construction

```go
bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

eventsSvc, err := events.NewService(events.Repositories{
    Outbox: outboxRepo, // nil selects direct-only mode
}, events.Config{
    Bus:              bus,
    StreamMiddleware: []web.Middleware{authSvc.RequireAccessToken()},
    Authorize:         authorizeStream,
})
if err != nil {
    return err
}

if err := eventsSvc.Register(pocket.Mount{
    Router: router,
    Logger: log,
}); err != nil {
    return err
}
```

`Bus` is required. Stream middleware must store an `identity.Principal`; without one the handler fails closed with 401. `Authorize == nil` removes the resource route rather than allowing it.

## Two delivery guarantees

| Rail | How it enters | Guarantee | Good use |
|---|---|---|---|
| best effort | `Mount.Events.Emit` after a domain write | at-most-once; crash gap and subscriber absence can lose it | re-fetch hints, live UI, cache invalidation |
| durable | outbox record in the same repository transaction as domain rows | at-least-once after polling; duplicates possible | required observation and external reactions with de-duplication |

Do not describe `Mount.Events` as transactional. Durable records travel through pocket repositories, not the mount emitter.

## Outbox poller

The host builds `NewPoller(outboxRepo, bus)` and runs its `Poll` method on a `workers.Pool`. The poller publishes synchronously, then marks the entry published. A publish failure leaves it pending; a mark failure can produce a duplicate next time.

Consumers acting on durable events de-duplicate by event ID.

The current poller assumes one poller per outbox. It does not claim or lease batches. Run one, and stop it before closing the bus so an entry is never marked after publishing has become impossible.

## SSE posture

- default projection exposes metadata only; raw payloads require an explicit `Projector`;
- slow clients drop overflow because SSE is a wake-up channel, not an authoritative queue;
- connection age defaults to 15 minutes and cannot be disabled, bounding revocation latency;
- concurrent streams are capped per subject;
- resource authorization runs at connect time.

Clients should re-fetch authoritative state after a wake-up rather than treating every frame as a complete state transition.

## Events observe work

Never put a best-effort event in front of work that must happen. Authentication submits delivery directly to jobs and may emit lifecycle events afterward for observation. Durable side effects belong in a transactional outbox or durable work queue.

## Stores

The pgx and Turso store modules own the `event_outbox` table and probe it at construction. Export the `events` migration source to the host ledger. A nil outbox repository is valid direct-only mode and requires no poller.
