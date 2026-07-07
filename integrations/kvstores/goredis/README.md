# integrations/kvstores/goredis

A multi-port Redis connector wrapping exactly one third-party library —
`github.com/redis/go-redis/v9`. One dependency, one caller-supplied
`*redis.Client`, three `sdk` facility ports:

| type | sdk port | rail |
|---|---|---|
| `goredis.Bus` | `events.Bus` + `events.Broadcaster` | Redis Streams (durable) + pub/sub (fan-out) |
| `goredis.Cacher` | `cacher.Storer` | TTL cache over GET/MGET/SET/DEL/SCAN |
| `goredis.Limiter` | `ratelimiter.Limiter` | sliding-window rate limit via an atomic Lua script |

It imports only `sdk` facility ports and go-redis — **no feature, no other
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
animal — it implements a *feature-owned* port and lives at
`features/auth/stores/redis`; this module carries **facility** ports only.)

The `sdk` ports (`events`, `cacher`, `ratelimiter`) already define the port
vocabulary, so each facility here is one file — no per-port adapter subpackage
like the pre-`sdk` gopernicus model.

## Construction — one client feeds all three

The caller supplies and owns the `*redis.Client`; every facility's `Close` shuts
down its own bookkeeping but **never closes the client**. A single client can
back all three facilities.

```go
rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
defer rdb.Close()

bus := goredis.New(rdb, logger, goredis.Options{
    StreamPrefix:  "events:",
    ConsumerGroup: "myapp",
})
defer bus.Close(ctx)

cache := goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix("cache:"))
limiter := goredis.NewLimiter(rdb, goredis.WithLimiterKeyPrefix("ratelimit:"))
```

`Bus` takes a logger and an `Options` struct (with `env:` struct tags for
`sdk/environment.ParseEnvTags`; a nil logger falls back to `slog.Default()`, and the
zero `Options` takes the defaults `StreamPrefix: "events:"`,
`ConsumerGroup: "default"`, `Workers: 4`, `BlockTimeout: 5s`, `BatchSize: 10`,
`MaxLen: 0` = unbounded). `Cacher` and `Limiter` take functional options; each
defaults its key prefix (`cache:`, `ratelimit:`) for namespacing a shared Redis.

## Connection — `Open` builds a client (bring-your-own stays first-class)

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
    goredis.WithTracing(tracer), // sdk/tracing.Tracer
)
if err != nil {
    return err // ping failed: server unreachable
}
defer rdb.Close()

bus := goredis.New(rdb, logger, goredis.Options{})
cache := goredis.NewCacher(rdb)
limiter := goredis.NewLimiter(rdb)
```

`Config` fills zero fields with the documented defaults and carries `env:` struct
tags for `sdk/environment.ParseEnvTags` (keys are namespaced by component — the host
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
  inside a span from the `sdk/tracing` port (stdlib only — an OpenTelemetry
  exporter is the deferred `integrations/tracing/otel`). A nil tracer falls back
  to `tracing.Noop`. Spans carry the command **name only**, never argument
  values, so no key or value data leaks into traces.

## Bus — delivery guarantees per path

| path | API | mechanism | guarantee |
|---|---|---|---|
| **streams** | `Emit` / `Subscribe` | XADD to a per-type stream; XReadGroup workers; XACK | **durable, at-least-once, competing consumers** — N processes sharing one `ConsumerGroup` split the load, each message to exactly one consumer across the group, unacked messages redelivered |
| **broadcast** | `Emit` / `SubscribeBroadcast` | PUBLISH on every emit; SUBSCRIBE fan-out | **best-effort fan-out, no durability, no replay** — every process with a subscriber gets every event; an event published while a subscriber is offline is gone |

Handlers **must be idempotent**: the streams rail is at-least-once, and `Emit`
also mirrors to pub/sub, so the same event can be observed more than once (and a
`WithSync` emit dispatches locally *and* streams the copy for competing
consumers).

### Poison-pill policy: XACK-always

A stream message is **acknowledged unconditionally** — even when it fails to
parse or a handler errors or panics. One bad message can therefore never block
the group's pending list. There is deliberately **no in-bus retry**: durable
retry is the outbox/jobs rail's responsibility (`features/jobs`), not this
transport's. A handler that must not lose work commits it to that rail.

### Wildcard (`"*"`) semantics

On the **broadcast** path, `"*"` fans out every event across every process — the
natural pattern for SSE and metrics consumers.

On the **streams** path, a `"*"` subscription receives events on topics **this
process also emits** (the worker begins reading a stream once a local wildcard
subscriber makes it relevant). Redis Streams consumer groups are per-stream, so
cross-process wildcard fan-out is the broadcast path's job — not the
competing-consumer streams path's. Exact-topic streams subscriptions have the
full cross-process competing-consumer guarantee.

## Cacher

Opaque `[]byte` values with per-key TTL (`0` = no expiry). `GetMany` is a single
MGET round trip returning only the keys present; `DeletePattern` walks the
keyspace with SCAN (glob syntax, e.g. `"users:*"`) so a large match set never
blocks Redis. `Close` is a no-op (the caller owns the client).

## Limiter

A distributed **sliding window** driven by an atomic Lua script that reads Redis
server time — so instances agree on the window regardless of clock skew — and
lets Redis expire idle keys via PEXPIRE. The script is cached by SHA (EVALSHA
with an EVAL/reload fallback on NOSCRIPT). `Limit.Burst` is added to
`Limit.Requests` to form the effective ceiling. `Close` is a no-op.

## Testing

- **Hermetic** (`bus_test.go`, `cacher_test.go`, `limiter_test.go`,
  `client_test.go`, `hooks_test.go`, `go test ./...`): envelope encode/decode,
  option defaulting, `RemoteEvent` rehydration through `TypedHandler`'s
  Unmarshaler slow path, close/subscribe guards, constructor defaults, Lua-reply
  coercion, `Config` defaulting via the env tags, `Open`'s fail-fast against an
  unreachable address, `ClientOption` wiring, and the logging/tracing hooks
  driven directly with a fake `next` — **no Redis required**.
- **Live** (`conformance_test.go`): the shared `sdk/events/eventstest`,
  `sdk/cacher/cachertest`, and `sdk/ratelimiter/ratelimitertest` suites plus a
  cross-instance broadcast fan-out test and an end-to-end `Open` round trip with
  hooks installed, **env-gated on `REDIS_TEST_ADDR`** with a loud skip so
  `make check` stays hermetic. Every live client is built through `Open`, so the
  leg proves `Open` against real Redis, not just the raw `redis.NewClient` path.

```sh
docker run --rm -d -p 6379:6379 redis:7
REDIS_TEST_ADDR=localhost:6379 go test ./...
```
