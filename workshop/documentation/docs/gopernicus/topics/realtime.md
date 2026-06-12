---
title: Realtime (SSE)
---

# Realtime push over Server-Sent Events

The `ssebridge` streams domain events to browsers: one `Hub` per process
subscribes to the event bus and fans events into connected clients. On
multi-instance deployments with the redis bus, the hub uses **broadcast**
delivery (redis pub/sub alongside the durable streams) so every instance
sees every event; the in-memory bus already fans out.

## Wiring

```go
hub, err := ssebridge.NewHub(bus, log)            // options below
if err != nil { return err }
sse := ssebridge.New(log, hub, authenticator, authorizer, rateLimiter)
sse.AddHttpRoutes(api.Group("/events"))
```

Routes:

| Route | Auth | Stream |
|---|---|---|
| `GET /events` | authenticate | every event whose `tenant_id` matches the subject's tenant |
| `GET /events/{resource_type}/{resource_id}` | authenticate + `authorize read` (connect-time) | events for that aggregate |

Both accept `?types=a,b` to allow-list event types.

## What clients receive

By default events are **projected to metadata only** — `{type,
occurred_at, tenant_id?, aggregate_type?, aggregate_id?}` — never the
payload (auth events carry verification codes; the projection removes the
"which events are safe to stream" audit entirely). Clients treat a frame
as a wake-up and re-fetch state through the authorized API:

```ts
const { events } = client.events.subscribe(`/events/space/${spaceID}`);
for await (const e of events) {
  if (e.type === "dashboard.updated") await refetchDashboard();
}
```

Opt into richer payloads per deployment with
`ssebridge.WithPayloadProjector(func(events.Event) any { ... })` — audit
your event contents first.

## Options

| Option | Default | Notes |
|---|---|---|
| `WithTopics(...)` | `"*"` | bus subscription scope |
| `WithBufferSize(n)` | 64 | per-connection buffer; full buffer **drops** (SSE is a wake-up channel, not a durable feed) |
| `WithHeartbeat(d)` | 25s | `: ping` comment frames |
| `WithMaxConnAge(d)` | 0 (unlimited) | force reconnect + re-auth; recommended ~15m when revocation latency matters |
| `WithMaxConnsPerSubject(n)` | 10 | concurrent streams per subject |

## Security model

Authorization runs at **connect time only** — the middleware chain runs
once per connection. A revoked session keeps an open stream alive until
it disconnects; bound that window with `WithMaxConnAge`. Tenant scoping
comes from the authenticated subject's context, never from the query
string.

## Delivery semantics

Fire-and-forget fan-out: no replay, no durability, drops under
backpressure. Anything that must not be missed belongs on the durable
bus/outbox path — SSE only tells connected clients to look again.
