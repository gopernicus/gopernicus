---
title: Capability packages
description: Behavioral ports with shared policy, their stdlib defaults, and the integrations that replace them.
---

# Capability packages

A capability is a narrow behavioral contract plus the policy every
implementation must share. It may ship a standard-library default, but that is
optional. Capabilities may use kernel and `pkg/` vocabulary and never import one
another.

## Catalog

| Capability | It gives you | Default | Replace with |
|---|---|---|---|
| [`cacher`](#cacher) | byte cache, namespaced JSON cache, public-page middleware | bounded memory LRU, `Noop` | Redis (`integrations/kvstores/goredis`) |
| [`events`](#events) | typed notifications, subscriptions, checked local dispatch, record envelopes | bounded memory, `Noop` | Redis; the events pocket builds on it |
| [`filestorage`](#filestorage) | object keys, streaming upload/replace, range reads; optional signed URLs and resumable uploads | confined disk | GCS, S3-compatible |
| [`notify`](#notify) and `notify/email` | per-call multi-channel delivery, production posture checks, typed email with templates | console and SMTP email | SendGrid; host Slack/SMS clients |
| [`ratelimiter`](#ratelimiter) | allow/retry semantics and HTTP middleware | memory | Redis, pgx-backed limiter |
| [`tracing`](#tracing) | tracer/span vocabulary and HTTP middleware | `Noop` | OpenTelemetry |
| [`transaction`](#transactions) | one transaction callback contract | none | pgx, Turso, Firestore connectors |
| [`oauth`](#oauth) | provider, PKCE and ID-token vocabulary | none | GitHub, Google |
| [`work`](#work) | keyed admission, replace and status protocol | none | jobs pocket |

Defaults are wired values, never global singletons. A host starts with no
infrastructure and swaps each value at its composition root:

```go
cache := cacher.NewMemory()
sender := email.NewConsole(log)
bus := events.NewMemory(events.WithLogger(log))
limiter := ratelimiter.NewMemory()
tracer := tracing.Noop{}
```

Middleware that combines a capability with HTTP lives with the capability
(`cacher.Pages`, `ratelimiter.Middleware`, `tracing.Middleware`), so
capabilities depend on `pkg/web` and never the reverse.

Capabilities with interchangeable backends publish conformance suites:
`cacher/cachertest`, `events/eventstest`, `filestorage/filestoragetest`,
`ratelimiter/ratelimitertest` and `work/worktest`. An integration passes the
suite in addition to its own driver tests. That is how memory and Redis caches,
or disk, GCS and S3 stores, share observable behavior.

## cacher

**What:** a `Storer` of owned bytes, a `Cache` facade with namespaces, error
reporting and JSON helpers, and `Pages` middleware for public HTML.
**Default:** `NewMemory(WithMaxEntries(n))`, a bounded LRU; `Noop{}` disables
storage while keeping the same validation rules.

```go
cache := cacher.New(cacher.NewMemory(cacher.WithMaxEntries(1000)),
    cacher.WithNamespace("catalog:v1"),
    cacher.WithOnError(func(ctx context.Context, operation string, err error) {
        log.WarnContext(ctx, "catalog cache failed", "operation", operation, "error", err)
    }),
)
items, err := cacher.GetOrLoadJSON(ctx, cache, "published:v1", 30*time.Second, source.ListPublished)
```

Keys, TTLs, namespace versions and public/private scope are decided in host or
pocket logic; no repository is cached automatically. `examples/minimal` serves
`/catalog.json` this way and its `cmd/server/catalog_cache_test.go` covers
invalidation, outage, corrupt entries and source errors.

**Storage rules.** Reads and writes copy bytes. Empty values are hits. Raw `Set`
and `SetJSON` need an explicit TTL: zero means no expiration, negative is
`sdk.ErrInvalidInput`; Redis rounds positive TTLs up to milliseconds. Memory
defaults to 10,000 entries for a nonpositive `MaxEntries`, reclaims expired keys
on access and before evicting live keys, and counts entries rather than bytes,
so eviction can precede TTL. Construct it with `NewMemory`; the zero value is
unusable. There is no janitor or `Close`; the host owns any Redis client.
Cancellation returns the context error and does not roll back completed writes.

**Facade rules.** `GetJSON[T]` returns `(T, found, error)` and treats zero,
false and JSON null as hits. `SetJSON` and raw operations are strict.
`GetOrLoadJSON` alone treats cache and codec errors as misses: it reports them
through `OnError`, calls the loader with the caller context and returns source
data even if storing fails. Loader and context errors reach the caller and are
never cached. It does not coalesce concurrent misses or refresh in the
background. `InvalidatePrefix` works when the store implements the optional
`PrefixDeleter` (`DeletePrefix` matches a literal prefix; empty clears the
namespace) and returns `errors.ErrUnsupported` otherwise. `cachertest.RunPrefix`
tests that optional contract.

**Pages.** `cacher.Pages(store, PageConfig{TTL, MaxBodyBytes, Scope})` caches
eligible public GET HTML responses. Zero TTL means 60 seconds, negative
disables; `MaxBodyBytes` defaults to 1 MiB; nil storage bypasses. Keys include
scheme, authority, exact URI and the optional host-owned `Scope` partition such
as a tenant ID, under hashed `page:v2:` keys. Mount it only on public, cacheable
GET HTML routes: requests with credentials, cookies, a principal, conditional or
range headers, or cache directives bypass even a warm entry, and responses with
cookies, `Vary`, content encoding, nonce CSP, explicit `Date`, `Content-Length`,
`Expires` or `Age`, trailers, or `private`/`no-store`/`no-cache` are not
stored. Hits replay committed content and caching headers, carry `Age` and
`X-Cache: HIT`, and get fresh request IDs, tracing fields and `Date`. Put
compression outside `Pages`. `web.NoStore()` is respected. Cache outages become
misses, and `DeletePrefix(ctx, "page:")` invalidates without a guarantee
against concurrent renders.

## events

**What:** `Emitter.Emit` for asynchronous notification, `Subscribe` for fanout,
checked local `Dispatch`, and `Record` envelopes for transport.
**Default:** `NewMemory` (four workers, queue of 1000); `Noop{}` disables
notifications and has no checked delivery.

| Call | Success means |
|---|---|
| `bus.Emit(ctx, event)` | asynchronous admission; later encoding, transport or handler failures are logged |
| `memory.Dispatch(ctx, event)` | every selected local handler completed |
| `redisBus.Publish(ctx, event)` | Redis accepted the stream entry; pub/sub notification was best effort |

Use `Emit` for observation, wake-ups, cache invalidation and live updates where
consumers can re-fetch authoritative state. For side effects that must survive a
crash, append to the [events pocket's transactional outbox](../pockets/events.md#outbox-poller)
or use the keyed [work](#work) protocol; the poller receives the exact checked
delivery function the host requires. `Dispatch` returns the first handler error,
recovers panics as `events.ErrHandlerPanic`, and checks cancellation between
callbacks; an empty selection succeeds, so register required handlers first.

`Subscribe(topic, handler)` takes an exact topic or `"*"`. Redis uses pub/sub,
so every connected instance receives notifications and slow or disconnected
subscribers can miss them; competing work uses the Redis integration's separate
`SubscribeWork`. `Broadcaster` is explicit fanout and `Subscriber` is the smaller
port `WakeChannel` needs. Full admission returns `events.ErrCapacity` and a
closed bus returns `events.ErrClosed`, both matching `sdk.ErrUnavailable`.
Empty or `"*"` event types and empty topics or nil handlers are invalid input.
Accepted events keep context values but detach request cancellation. Keep
events immutable, handlers concurrency-safe and duplicate-tolerant; there is no
total ordering. Stop producers before `Close`, which drains admitted
publications and waits for callbacks; callbacks must not close their own bus.

`Record` carries stable identity, type, time, correlation, payload bytes and
optional aggregate/tenant metadata. `NewRecord(event)` copies payload and
metadata and preserves or generates `EventID`; `record.Event()` returns a
`RemoteEvent`. Use `EventID` for duplicate detection. JSON encodes the payload as
base64; `RemoteEvent.Unmarshal` and `TypedHandler`'s remote fallback decode JSON
payloads only. See the [Redis integration](../integrations/catalog.md#redis) for
recovery and transport migration.

## filestorage

**What:** `Storer` with `Upload`, `Download`, `Delete`, `Exists`, `List`,
`DownloadRange` and `GetObjectSize`; optional `SignedURLer` and
`ResumableUploader`. **Default:** `NewDisk(dir)`, confined with `os.Root`.
There is no service wrapper or automatic logger.

```go
disk, err := filestorage.NewDisk(mediaDir)
if err != nil {
    return err
}
var blobs filestorage.Storer = disk
// Wire blobs into consumers, run the host, stop handlers and workers,
// close returned download readers, then:
return disk.Close()
```

Declare a narrower interface if your domain needs fewer operations, and
discover optional behavior on the actual adapter:

```go
if uploader, ok := blobs.(filestorage.ResumableUploader); ok {
    sessionURI, err := uploader.InitiateResumableUpload(ctx, key,
        filestorage.ResumableUploadOptions{ContentType: "application/octet-stream", Origin: allowedBrowserOrigin})
    // deliver sessionURI only to the authorized client
}
```

**Keys.** Nonempty relative UTF-8 paths separated by `/`. Empty, `.` and `..`
segments, leading or trailing slashes, backslashes and NUL are rejected; spaces,
Unicode and `two..dots` are valid. Adapters never clean, trim or normalize keys,
so inventory existing keys before switching backends. Invalid keys match
`filestorage.ErrInvalidPath` and `sdk.ErrInvalidInput`; missing objects match
`filestorage.ErrObjectNotFound` and `sdk.ErrNotFound`, so the web domain error
responder returns 404. `ValidatePath`, `ValidatePrefix`, `ValidateRange` and
`ValidateExpiry` expose the shared rules to adapters.

**Semantics.** `Upload` streams an `io.Reader` and replaces any existing object
on success; source errors and cancellation abort the uncommitted upload and keep
the previous object, though remote completion races promise no rollback.
`Delete` succeeds on a missing object. `List` matches literal prefixes
(including partial final components and a trailing slash), returns caller-facing
keys without directory markers, materializes in memory and promises no order.
Reads describe stored bytes, including any stored compression. Range offsets are
nonnegative; length `-1` means the remainder; reads truncate at EOF, and
zero-length reads or offsets at or beyond EOF return an empty reader after
confirming existence. Callers close returned readers. Separate size and read
calls are not a snapshot under concurrent writes.

**Disk.** Rejects symlink components and non-regular files, stages complete
replacements before rename, reserves the top-level `.gopernicus-tmp` directory
(any case) and writes every file as `0600`. There is no fsync promise,
cross-filesystem rename fallback or stale-stage sweep; clean abandoned staging
files only when no live uploader owns them. `Close` does not drain operations.
Hard links, mounts and malicious changes inside the tree are outside the
confinement guarantee.

**Optional protocols.** `SignedURLer.SignedURL` returns a bearer GET URL without
checking existence; expiry is whole seconds from one second to seven days, and
credentials may expire sooner. `ResumableUploader` returns a bearer session URI
the client PUTs to; the host authorizes `Origin`, configures bucket CORS and
owns completion. GCS implements it and needs an explicit signing identity; S3
exposes its concrete `InitiateMultipartUpload` instead and supports objects up
to about 48.8 GiB; Disk implements neither. Never log signed URLs or session
URIs. See the [integration catalog](../integrations/catalog.md#file-storage).

## notify

**What:** `notify.Send(ctx, deliveries...)` sends exactly the prepared
deliveries it receives, one per channel, and `notify.CheckTransport` enforces
production posture. `notify/email` adds typed messages, rendering and prepared
email deliveries. **Default:** `email.NewConsole` and `email.NewSMTP`; SendGrid
is the integration; Slack or SMS clients fit `notify.DeliveryFunc`.

```go
err := notify.Send(ctx,
    email.NewDelivery(emailSender, outageEmail),
    notify.DeliveryFunc(func(ctx context.Context) error { return slackSender.Send(ctx, outageSlack) }),
)
```

Deliveries run sequentially in argument order. Ordinary failures do not stop
later deliveries; caller cancellation skips remaining work. On failure,
`errors.As(err, &sendError)` exposes `*notify.SendError` whose `Failures` name
the argument `Index`, whether it was `Attempted`, and the cause; positions
absent from the list succeeded. Empty selections are invalid. There are no
automatic retries, since a remote error cannot prove non-delivery and retrying
the whole call would resend successes.

**Posture fails closed.** Senders report transport security and whether they
are development-only. `notify.CheckTransport(mode, sender)` rejects console or
metadata-less senders in production and lets development inspect and warn. The
check lives here so every consumer observes the same rule.

**Email rendering.** `email.NewRenderer(options...)` renders without a sender:
`Render(email.RenderRequest{Template, Subject, Data, Layout})` returns HTML and
text. Register deliberate `.html` and `.txt` content; text uses
`text/template` and missing text is an error. Empty `Layout` selects
transactional and an unknown layout fails. A prepared delivery snapshots
recipients and the full `email.Message`.

## ratelimiter

**What:** `Allower` admits one unit for a key; `Limiter` adds `Reset`; `Acquire`
waits between denials until admission or cancellation; `Middleware` gates HTTP.
**Default:** `NewMemory(WithMaxEntries(n))`. Redis and the pgx-backed limiter
share the same anchored two-window counter approximation.

```go
limiter := ratelimiter.NewMemory(ratelimiter.WithMaxEntries(1000))
gate := ratelimiter.Middleware(limiter, ratelimiter.MiddlewareConfig{
    Limit: ratelimiter.PerMinute(60).WithBurst(10),
    Key:   func(r *http.Request) string { return "catalog:public" },
    OnError: func(ctx context.Context, err error) {
        log.WarnContext(ctx, "catalog admission failed", "error", err)
    },
})
```

Hosts own limits, key namespaces and any plan lookup; resolve fallible policy
first, then call `Allow` or `Acquire`. Burst adds to the ceiling rather than
refilling separately. Positive windows round up to milliseconds; requests plus
burst must fit `MaxCeiling` and their product with the window in milliseconds
must be at most 2^52−1 (`Normalize` exposes the same checks). A live key's
window cannot change; reset or version the key. `ResetAt` is the current bucket
end and `RetryAfter` a backend-time checkpoint, not an exact earliest admission.

Memory defaults to 10,000 entries, reclaims expired keys on access or at
capacity, never evicts active budgets, and returns `ErrCapacity`
(`sdk.ErrUnavailable`) for a new key at capacity. Middleware fails closed by
default: dependency or capacity errors give 503, invalid configuration 500,
exhaustion a JSON 429 with `Retry-After`; set `FailOpen` explicitly. `OnError`
is synchronous and must be concurrency-safe. Redis and Postgres use v2 key
prefixes and Postgres needs the `window_ms` migration; AUDIT-011 in the root
migration guide covers that rollout. The runnable
`examples/minimal/internal/logic/domains/catalog/ratelimit_example_test.go`
shows policy resolution, HTTP-style `Allow` and worker `Acquire`.

## tracing

**What:** a small string-attribute span port plus `tracing.Middleware`.
**Default:** `Noop{}`. The OpenTelemetry integration adds server spans, typed
HTTP metadata and explicitly enabled W3C propagation.

Optional `HTTPTracer` and `HTTPSpan` let an adapter receive request metadata at
span creation and typed status at completion. The middleware records safe
errors for failed responses and panics, preserves the committed status and
finishes each span once. Put it outside logging so access logs carry the traced
context.

## transactions

**What:** `transaction.Transactor` with one method,
`Transact(ctx, func(ctx) error) error`. **Default:** none; the pgx, Turso and
Firestore connectors implement it.

Pass the callback context to repositories backed by the same datastore
instance; only operations using its handle participate. A nil return means the
transaction committed. An error or panic aborts, and panic values survive
cleanup; preserve `errors.Is`/`errors.As` matching since cleanup may add a
cause. Bundled connectors reject nesting. A connector may retry the callback, so
reset captured results per attempt and perform external effects only after
`Transact` returns nil, using a durable outbox when an effect must survive a
crash. Firestore requires reads before writes and does not observe its pending
writes. SQL commit uses the begin context, rollback gets an independent
five-second deadline, and cancellation racing a commit cannot undo an accepted
commit.

## oauth

**What:** `Provider` owns authorization URL construction, code exchange and
userinfo; `AuthorizationRequest` makes state, verifier, nonce and redirect URI
explicit; optional `IDTokenValidator` and `TokenRefresher` expose real extra
capabilities. **Default:** none; GitHub and Google are the integrations.

Providers report `EmailVerified` and `EmailAuthoritative` evidence and the
authentication host decides email trust. Client flow proof and session delivery
belong to authentication and its transports.

## work

**What:** an interoperability protocol for keyed work with a seven-state
lifecycle. **Default:** none; the jobs pocket implements it, and a custom
process-local implementation is legal.

```text
pending → running → completed
                  ↘ failed → retry
                  ↘ dead_letter
                  ↘ canceled
                  ↘ superseded
```

The ports are segregated: `Enqueuer` for idempotent keyed admission, `Replacer`
for atomic replace/supersede, `StatusReader` for the deterministic latest
status. Keys are nonempty and shared across kinds, so hosts choose a workflow or
tenant namespace. Concurrent admission under one key yields one active
execution with its original kind and payload; replacement atomically supersedes
older active work. Payloads are opaque byte snapshots. Claim, lease, checkpoint,
middleware and fencing are executor concerns outside this protocol; see
[workers](pkg.md#workers) and the [jobs pocket](../pockets/jobs.md).
