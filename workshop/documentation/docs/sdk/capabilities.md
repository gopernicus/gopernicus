---
title: Capability packages
description: Behavioral ports, shared policy, defaults, and implementations in the Gopernicus SDK.
---

# Capability packages

A capability combines a narrow behavioral contract with shared observable policy. It may ship a stdlib default, but that is optional. Capability packages can use kernel and foundation vocabulary; they never import one another.

## Catalog

| Capability | Contract / policy | SDK default | Other implementations or consumers |
|---|---|---|---|
| `cacher` | TTL storage and page-cache middleware | memory | Redis |
| `email` | message sender, templates/layouts, production transport posture | console, SMTP | SendGrid |
| `events` | event bus, broadcast/emit contracts, delivery options | memory, noop | Redis; events pocket builds on it |
| `filestorage` | object storage plus optional signed/resumable capabilities | disk | GCS, S3-compatible |
| `notify` | address-kind delivery and production posture | console | mailer bridge integration |
| `oauth` | OAuth/OIDC provider and PKCE vocabulary | none | GitHub, Google |
| `ratelimiter` | allow/retry semantics and HTTP middleware | memory | Redis, pgx-backed limiter |
| `tracing` | tracer/span vocabulary and HTTP middleware | noop | OpenTelemetry |
| `work` | keyed admission, replace, status, and lifecycle vocabulary | none | jobs pocket |

## Defaults keep simple hosts simple

Defaults are wired values, not global singletons:

```go
cache := cacher.NewMemory()
sender := email.NewConsole(log)
bus := events.NewMemory(events.WithLogger(log))
limiter := ratelimiter.NewMemory()
tracer := tracing.Noop{}
```

A host can start without external infrastructure and replace each value at the composition root when deployment needs change.

## Conformance over implementation details

Capabilities with interchangeable backends expose test packages:

- `cacher/cachertest`;
- `events/eventstest`;
- `filestorage/filestoragetest`;
- `ratelimiter/ratelimitertest`;
- `work/worktest`.

An integration should pass the capability suite in addition to its own driver-specific tests. This is how memory and Redis caches—or disk, GCS, and S3 stores—share observable behavior without sharing implementation.

## Production posture fails closed

Email senders and notifiers may report transport security and whether they are development-only. The capability owns inspection and enforcement:

```go
posture, err := email.CheckSender(mode, sender)
if err != nil {
    return err
}
```

Production rejects console or metadata-less implementations instead of assuming they are safe. Development can inspect the returned posture and warn while remaining usable.

This policy lives with the capability because every consumer should observe the same rule. The SDK does not log composition-specific prose or choose a sender.

## No capability-to-capability imports

If notification wants to deliver through email, neither package should import the other. The composition becomes an integration:

```text
notify capability ← integrations/notify/mailer → email capability
```

The rule prevents the SDK from turning into a web of facilities whose optional dependencies become inseparable.

## Events are not durable work

The SDK event bus supports in-process and distributed event behavior, but the emit rail is not a transaction or queue. Use it for observation, wake-ups, cache invalidation, and live updates where consumers can re-fetch authoritative state.

For side effects that must survive a crash, use a durable pocket-owned outbox or the keyed work protocol implemented by jobs. Never emit an event merely to trigger required work and mistake asynchronous delivery for durability.

## Work is an interoperability protocol

`work` owns a stable seven-state lifecycle:

```text
pending → running → completed
                  ↘ failed → retry
                  ↘ dead_letter
                  ↘ canceled
                  ↘ superseded
```

`failed` is non-terminal. The ports are segregated:

- `Enqueuer` for idempotent keyed admission;
- `Replacer` for atomic replace/supersede;
- `StatusReader` for deterministic latest status.

The payload is opaque bytes. Executor-side claim, lease, checkpoint, and fencing are deliberately outside this consumer protocol. The jobs pocket implements both the SDK-facing protocol and its richer executor domain.

## Capability middleware stays with its owner

Pure HTTP mechanics live in `foundation/web`. Middleware that combines a capability with HTTP lives in the capability:

- `cacher.Pages`;
- `ratelimiter.Middleware`;
- `tracing.Middleware`.

That direction lets capabilities depend on web mechanism without web importing every service facility.
