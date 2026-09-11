# integrations/kvstores/goredis

A multi-port Redis connector wrapping exactly one third-party library —
`github.com/redis/go-redis/v9`. One dependency, one caller-supplied
`*redis.Client`, three `sdk` facility ports:

| type | sdk port | rail |
|---|---|---|
| `goredis.Bus` | `events.Bus` + `events.Broadcaster` | Notifications via pub/sub; explicit reliable `SubscribeWork` via Streams |
| `goredis.Cacher` | `cacher.Storer` | TTL cache over GET/MGET/SET/DEL/SCAN |
| `goredis.Limiter` | `ratelimiter.Limiter` | sliding-window rate limit via an atomic Lua script |

It imports only `sdk` facility ports and go-redis — **no pocket, no other
integration**.

## Why one module, three ports (R-KV1)

An integration module implements several `sdk` facility ports when ONE client
library serves them: **the module unit is the library, not the port.** go-redis
is a single dependency that a host uses for its event bus, its cache, and its
rate limiter at once, so splitting it into three modules would triplicate the
same `require`, `go.sum`, and version-bump surface for no boundary benefit. The
category is named for the tech family (`kvstores/`) precisely because the wrapped
library is genuinely multi-port; capability-named categories (`oauth/`,
`scheduling/`) stay one-port. (A redis session store for auth is a different
animal — it implements a *pocket-owned* port and lives at
`pockets/auth/stores/redis`; this module carries **facility** ports only.)

The `sdk` ports (`events`, `cacher`, `ratelimiter`) already define the port
vocabulary, so each facility here is one file — no per-port adapter subpackage
like the pre-`sdk` gopernicus model.

## Construction — one client feeds all three

The caller supplies and owns the `*redis.Client`. The bus's `Close(ctx)` stops
its own work; the cache and limiter have no Close method. None closes the shared
client. A single client can back all three facilities.

```go
rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", ContextTimeoutEnabled: true})
defer rdb.Close()

bus := goredis.New(rdb,
    goredis.WithLogger(logger),
    goredis.WithStreamPrefix("events:"),
    goredis.WithConsumerGroup("myapp"),
)
defer bus.Close(ctx)

cache := goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix("cache:"))
limiter := goredis.NewLimiter(rdb, goredis.WithLimiterKeyPrefix("ratelimit:"))
```

`New(rdb, opts ...BusOption)` keeps the required client explicit. Bus settings
use `WithLogger`, `WithStreamPrefix`, `WithConsumerGroup`, `WithWorkers`,
`WithQueueSize`, `WithBlockTimeout`, `WithRetryAfter`, and `WithHandlerTimeout`.
No options uses `slog.Default()`, prefix `"events:"`, group `"default"`, 4 workers,
a queue of 1000, block timeout 5s, retry after 1m, and handler timeout 30s.
A nil logger, empty prefix/group, nonpositive worker/queue count, and zero
individual durations select those defaults. Negative/fractional work durations
remain errors at `SubscribeWork`; retry after must exceed handler timeout.

Options configure private construction settings in order; the last value for a
setting wins. All settings resolve before the publisher goroutines start. The
stream namespace always receives an internal `v2:` suffix. Work reads one entry
per available worker; there is no `BatchSize` or automatic `MaxLen` trimming.
Hosts map their own environment-loaded bus policy to these options; the former
exported `Options` record and its `EVENT_BUS_*` tags are removed.

`Cacher` and `Limiter` retain `WithCacheKeyPrefix` and `WithLimiterKeyPrefix`.
Their defaults are `cache:` and `ratelimit:`; explicit empty prefixes are allowed.
Limiter construction appends its internal `v2:` suffix after resolving options.
Options can be reused for new objects; they cannot mutate a running facility.
`New`, `NewCacher`, `NewLimiter`, and `LoggingHook` panic with a specific diagnostic
for a nil option. `Open` returns an error wrapping `sdk.ErrInvalidInput` for nil
`ClientOption` or nested `LoggingOption` values before allocating a client.

Connection `Config` and `ClientOption` remain separate from bus options.
`WithLogging` and `WithTracing` append hooks in order, including repeated calls.
`WithLogging` snapshots its `LoggingOption` slice; logger and tracer dependencies
remain borrowed. Nil logger/tracer arguments keep their default/no-op meaning.

## Rate limiting

The limiter uses the SDK's anchored two-window counter approximation, including
Burst. Limits are normalized to whole milliseconds; invalid keys/numeric ranges
match `sdk.ErrInvalidInput`. A live key cannot change Window without Reset or
expiration. Ceiling changes retain consumed quota. RetryAfter comes from Redis
server time and is a retry checkpoint, not a reservation or earliest-admission
promise. Cancellation reports the caller error; a completed remote write is not
rolled back.

The physical prefix is always the host prefix plus `v2:`. Default logical key
`login:alice` therefore becomes `ratelimit:v2:login:alice`. This normally creates
fresh budgets on upgrade; a colliding old record without a valid `window_ms` is
rejected with `sdk.ErrConflict` and left intact by both Allow and Reset. The host
owns rollout and old-key cleanup. There is no automatic data migration. See
[AUDIT-011](../../../AUDIT.md#audit-011-rate-limiter-contract-and-adapter-corrections)
for rolling-upgrade and namespace considerations. Old and new writers must have
disjoint physical keys; choose a fresh host prefix if old logical v2:* keys could
overlap. The format guard cannot detect an old writer overwriting new state.

## Connection — `Open` builds a client (bring-your-own stays first-class)

`Open` enables go-redis's `ContextTimeoutEnabled`, so command deadlines also
bound I/O on established connections. Set this option on borrowed clients when
you need that behavior; adapters never mutate or close the shared client.

Bring-your-own `redis.NewClient` is fully supported and shown above. When a host
wants the module to build the client, `Open` constructs one from a `Config`,
installs any instrumentation hooks, and performs a **fail-fast PING** — a
construction-time network round trip bounded by the passed `ctx`, mirroring the
`datastores/pgxdb` connector's ping-on-open. It returns the **raw `*redis.Client`**
(no wrapper type), so the client `Open` returns and a bring-your-own client are
interchangeable and one client can feed every facility. `StatusCheck(ctx, rdb)`
is the matching runtime health probe — a PING with a 1s default deadline (a
caller-supplied `ctx` deadline wins), mirroring `datastores/pgxdb`'s `StatusCheck`.

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

rdb, err := goredis.Open(ctx, goredis.Config{Addr: "localhost:6379"},
    goredis.WithLogging(logger, goredis.WithSlowThreshold(50*time.Millisecond)),
    goredis.WithTracing(tracer), // sdk/capabilities/tracing.Tracer
)
if err != nil {
    return err // ping failed: server unreachable
}
defer rdb.Close()

bus := goredis.New(rdb, goredis.WithLogger(logger))
cache := goredis.NewCacher(rdb)
limiter := goredis.NewLimiter(rdb)
```

`Config` fills zero fields with the documented defaults and carries `env:` struct
tags for `sdk/pkg/environment.ParseEnvTags` (keys are namespaced by component — the host
passes its own app namespace):

| field | env key | default |
|---|---|---|
| `Addr` | `REDIS_ADDR` | `localhost:6379` |
| `Password` | `REDIS_PASSWORD` | (empty) |
| `DB` | `REDIS_DB` | `0` |
| `TLSEnabled` | `REDIS_TLS_ENABLED` | `false` |
| `MaxRetries` | `REDIS_MAX_RETRIES` | `3` |
| `DialTimeout` | `REDIS_DIAL_TIMEOUT` | `5s` |
| `ReadTimeout` | `REDIS_READ_TIMEOUT` | `3s` |
| `WriteTimeout` | `REDIS_WRITE_TIMEOUT` | `3s` |
| `PoolSize` | `REDIS_POOL_SIZE` | `10` |
| `MinIdleConns` | `REDIS_MIN_IDLE_CONNS` | `2` |

### Instrumentation hooks

Both hooks use go-redis's own `redis.Hook` API and can be installed via the
`Open` options above or handed to `rdb.AddHook` directly on a bring-your-own
client:

- `WithLogging(log, ...LoggingOption)` / `goredis.LoggingHook(log, ...)` — logs
  command **errors always** (the `redis.Nil` cache-miss sentinel is not an
  error) and, with `WithSlowThreshold(d)`, commands slower than `d` at Warn.
- `WithTracing(tracer)` / `goredis.TracingHook(tracer)` — runs each command
  inside a span from the `sdk/capabilities/tracing` port (stdlib only — an OpenTelemetry
  exporter is the deferred `integrations/tracing/otel`). A nil tracer falls back
  to `tracing.Noop`. Spans carry the command **name only**, never argument
  values, so no key or value data leaks into traces.

## Bus — notifications and reliable work

| API | Completion and delivery |
|---|---|
| `Emit(ctx, event)` | Bounded asynchronous admission; encoding/publication failures are logged. Returns `events.ErrCapacity` when the queue is full. |
| `Publish(ctx, event)` | Waits for XADD acceptance, then attempts best-effort pub/sub fanout. Returns acceptance errors; it does not wait for or force local handlers. |
| `Subscribe(topic, handler)` / `SubscribeBroadcast(topic, handler)` | Notification fanout for an exact topic or `"*"`; waits for Redis subscription acknowledgment before success. No replay for disconnected subscribers. |
| `SubscribeWork(ctx, topic, handler)` | Reliable competing-consumer work for one exact topic; validates configuration and creates its group before success. |

A nil result from `Emit` confirms only queue admission. Use `Publish` as a checked
outbox delivery callback. Both publication paths write one canonical
`events.Record` envelope to Streams and mirror the same bytes to pub/sub. Envelope
metadata is independent of payload encoding, so opaque binary payloads retain
their ID, tenant and aggregate fields. JSON represents payload bytes as base64.
For retries, create the record once and publish `record.Event()` to preserve its
ID. Publication cancellation cannot undo a remote write that already succeeded.

Operations check their contexts before and after remote calls. Subscription setup
uses a five-second context; prompt network interruption also depends on the
caller-owned go-redis client's timeout and `ContextTimeoutEnabled` configuration.
The bus does not change shared client settings.

Handlers must support concurrent calls and duplicate delivery. The caller keeps
emitted event graphs immutable; asynchronous admission detaches cancellation but
preserves context values. `Close(ctx)` refuses new work, drains admitted
publications and waits for active callbacks. Every concurrent or repeated Close
uses its own context to wait on the same completion. It never closes the caller's
Redis client. The host owns shutdown; callbacks must not call Close themselves.

### Reliable work and the pending-entry policy

Instances sharing a `ConsumerGroup` compete for work and must deploy the same
handler responsibilities for each topic. Put required steps in one composed
handler: the first successful registration starts readers immediately, so several
independent registration calls do not form an atomic startup boundary. Work
subscriptions accept exact topics; wildcard fanout belongs to notifications.

Each worker claims one entry from one stream at a time. It alternates new reads
with `XAUTOCLAIM` recovery of idle pending entries (Redis 6.2 or newer), keeping
scan cursors so older pending work remains reachable. Unsubscribe removes the
active topic when its final local registration leaves. Already claimed work with
no current handler stays pending for a peer. If a group disappears, readers
recreate it at `0`; retained entries can therefore be delivered again.

`HandlerTimeout` covers the **entire selected-handler attempt**, and
`RetryAfter` must exceed it. Both durations and `BlockTimeout` must be positive
whole milliseconds. A timeout cancels the callback context; it cannot forcibly
stop a callback that ignores cancellation. Such a callback may overlap a reclaim,
so idempotency is required. There is no distributed exactly-once guarantee.

Only successful completion of every selected handler permits XACK. Handler
errors, panics, timeouts, cancellation, malformed envelopes, stream/type mismatch
and missing handlers leave work pending. One callback panic does not skip other
selected handlers. Failures are logged; reclaim retries them without preventing
fresh work from being read.

Permanent poison remains pending and observable until the host repairs it or
makes an explicit terminal disposition using its Redis client. There is no
automatic discard, maximum-attempt policy or DLQ framework. There is also no
automatic stream trimming: hosts own retention, archival and deletion after
acknowledgments, and should inspect pending state before removing entries.

### Upgrade from the previous bus

`EmitOption`/`WithSync`, `Options.BatchSize` and `Options.MaxLen` are removed.
Replace checked publication with `Publish`. `Subscribe` now means notification
fanout; migrate reliable work handlers to `SubscribeWork(ctx, exactTopic, handler)`.
The previous unconditional acknowledgment of failed work is gone.

All stream and broadcast prefixes append `v2:`, including custom prefixes. The
new serialized Record format has no automatic old-message conversion or replay.
Drain old streams with old code or stop old writers and coordinate the upgrade.
Use disjoint physical namespaces during overlap; do not assume arbitrary old
logical topics cannot collide with the new suffix. Retain visibility into old
pending entries and use duplicate-safe replay with stable IDs. The adapter never
automatically deletes or migrates old state. Existing outbox Record fields and
payloads need no schema migration.

## Cacher

Opaque `[]byte` values with per-key TTL: zero means no expiry, negative is
`sdk.ErrInvalidInput`, and positive TTL rounds **up** to milliseconds. Each Set
replaces the previous TTL; driver-specific keep-TTL sentinels are not accepted.
`GetMany` uses one MGET, returning present logical keys and independent bytes.
Cancellation is checked before and after commands; prompt network interruption
still depends on the host-owned client's timeout configuration. Cancellation can
race a completed write and does not roll it back.

`Cacher` also implements optional `cacher.PrefixDeleter`. `DeletePrefix(ctx,
"users:")` deletes a **literal** prefix. Redis glob metacharacters in both the
adapter namespace and the supplied prefix are escaped before a SCAN/DEL walk.
An empty prefix clears this adapter's namespace; it does not mean the entire
Redis database unless the host configured an empty adapter namespace. Deletion
is best effort with concurrent writers, and SCAN COUNT is a hint, not a batch
size or atomicity guarantee.

There is no cache `Close`: the caller closes the Redis client it owns. Wrap this
adapter with `cacher.New(store, cacher.WithNamespace("catalog:v1"))` for
namespaced application-data caching and typed JSON load helpers. Adapter and
service namespaces compose; they do not replace one another. For custom store
verification, run `cachertest.Run` and the optional `cachertest.RunPrefix` suite.

## Limiter

A distributed **sliding window** driven by an atomic Lua script that reads Redis
server time — so instances agree on the window regardless of clock skew — and
lets Redis expire idle keys via PEXPIRE. The script is cached by SHA (EVALSHA
with an EVAL fallback on NOSCRIPT). `Limit.Burst` is added to
`Limit.Requests` to form the effective ceiling. The limiter has no Close method;
the host owns the shared Redis client.

## Testing

- **Hermetic** (`bus_test.go`, `cacher_test.go`, `limiter_test.go`,
  `client_test.go`, `hooks_test.go`, `go test ./...`): envelope encode/decode,
  option defaulting, `RemoteEvent` rehydration through `TypedHandler`'s
  Unmarshaler slow path, bounded bus admission/shared shutdown, work-attempt
  timeout and reclaim-cursor behavior, close/subscribe guards, constructor defaults, Lua-reply
  coercion, `Config` defaulting via the env tags, `Open`'s fail-fast against an
  unreachable address, `ClientOption` wiring, and the logging/tracing hooks
  driven directly with a fake `next` — **no Redis required**.
- **Live** (`conformance_test.go`, `bus_live_test.go`, `bus_lifecycle_live_test.go`): the shared `sdk/capabilities/events/eventstest`,
  `sdk/capabilities/cacher/cachertest`, and `sdk/capabilities/ratelimiter/ratelimitertest` suites plus a
  cross-instance broadcast fan-out test and an end-to-end `Open` round trip with
  hooks installed, **env-gated on `REDIS_TEST_ADDR`** with a loud skip so
  `make check` stays hermetic. Shared conformance clients use `Open`; bus setup
  tests also use controlled raw clients to reproduce connection failure and
  shutdown races. Bus regressions cover opaque envelope preservation, callback
  failure/panic/timeout recovery, malformed pending entries, another consumer's
  abandoned work, NOGROUP recreation, unsubscribe/peer recovery and subscription
  readiness without setup sleeps.

```sh
docker run --rm -d -p 6379:6379 redis:7
REDIS_TEST_ADDR=localhost:6379 go test ./...
```
