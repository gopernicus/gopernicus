---
title: Capability packages
description: Behavioral ports, shared policy, defaults, and implementations in the Gopernicus SDK.
---

# Capability packages

A capability combines a narrow behavioral contract with shared observable policy. It may ship a stdlib default, but that is optional. Capability packages can use kernel and pkg vocabulary; they never import one another.

## Catalog

| Capability | Contract / policy | SDK default | Other implementations or consumers |
|---|---|---|---|
| `cacher` | byte storage, namespaced data cache, JSON loading and public-page middleware | bounded memory, noop | Redis |
| `notify/email` | typed email, render-only templates/layouts, prepared deliveries | console, SMTP | SendGrid |
| `events` | typed notifications, subscriptions, checked local dispatch and record envelopes | bounded memory, noop | Redis; events pocket builds on it |
| `filestorage` | portable object keys, streaming/replacement and range semantics; optional signed reads and direct PUT sessions | confined disk | GCS, S3-compatible |
| `notify` | explicit per-call deliveries and shared production posture | dispatch, body console | host-selected channel implementations |
| `oauth` | OAuth/OIDC provider and PKCE vocabulary | none | GitHub, Google |
| `ratelimiter` | allow/retry semantics and HTTP middleware | memory | Redis, pgx-backed limiter |
| `transaction` | transaction callback and participation contract | none | pgx, Turso, Firestore |
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

## File storage and object ownership

Use `filestorage.Storer` directly, or declare a narrower interface with only the
operations your domain needs. Its seven methods are Upload, Download, Delete,
Exists, List, DownloadRange and GetObjectSize. There is no FileStore service
wrapper or automatic storage logger. Hosts own logging and concrete resources.

```go
disk, err := filestorage.NewDisk(mediaDir)
if err != nil {
    return err
}
var blobs filestorage.Storer = disk
// Wire blobs into consumers, run the host, then stop handlers and workers.
// Close returned download readers before releasing the store.
return disk.Close()
```

`Upload` accepts a streaming `io.Reader` without requiring seeking or closing the
input. Success replaces an existing object. Source errors and cancellation abort
uncommitted uploads and retain the previous object; returned errors preserve
`errors.Is` and `errors.As`. Cancellation is cooperative, so a blocked source
reader cannot be forcibly interrupted. Remote completion races do not promise
rollback. Download callers own and close their returned readers.

Keys are nonempty relative UTF-8 paths separated by `/`. Empty, `.` and `..`
segments, leading/trailing slashes, backslashes and NUL are rejected. Spaces,
Unicode, `.segovia/boot-probe` and `two..dots` remain valid. Adapters never clean,
slug, trim or normalize keys. Inventory existing incompatible keys before an
upgrade or backend switch; filesystem case and file/directory conflicts remain
limits of Disk.

`List` matches literal prefixes, including partial final components, `.` matching
`.segovia`, empty prefix for all objects and a trailing slash. It returns
caller-facing keys, omits directory markers, materializes the result in memory
and promises no order. `Download`, `DownloadRange` and `GetObjectSize` describe
stored bytes, including any stored compression. Range offsets are nonnegative;
length `-1` means the remainder, otherwise length must be nonnegative and its
inclusive end must not overflow. Reads truncate at EOF. Zero-length reads and
offsets at/beyond EOF return an empty reader after confirming object existence.
Separate size/read requests do not promise a snapshot during concurrent writes.

`ValidatePath`, `ValidatePrefix`, `ValidateRange` and `ValidateExpiry` expose these
small shared rules for adapters. Missing objects match both
`filestorage.ErrObjectNotFound` and `sdk.ErrNotFound`, so the standard web domain
error responder maps them to HTTP 404. Invalid paths/prefixes match
`filestorage.ErrInvalidPath` and `sdk.ErrInvalidInput`; invalid ranges and expiry
also match `sdk.ErrInvalidInput`. Provider configuration and permission failures
remain errors. `Delete` succeeds when the object is already missing.

Disk confines operations with `os.Root`, rejects symlink components and
non-regular objects, and stages complete replacements before rename. It reserves
the top-level `.gopernicus-tmp` directory (including case variants); that restriction
belongs to Disk, not to the generic cloud-key contract. Staged objects are private and invisible
to List. Every newly uploaded or replaced file uses mode `0600`, including
replacements of formerly group-readable files. There is no fsync durability
promise, cross-filesystem rename fallback or automatic stale-stage sweep.
Stop users before Close; it does not drain operations or close returned readers.
After a crash, the host may clean abandoned staging files only after establishing
that no live uploader owns them. The host must control the filesystem tree;
hard links, mounts and malicious changes within it are outside confinement's
application-level guarantee.

Discover optional capabilities on the actual adapter:

```go
if uploader, ok := blobs.(filestorage.ResumableUploader); ok {
    sessionURI, err := uploader.InitiateResumableUpload(ctx, key,
        filestorage.ResumableUploadOptions{
            ContentType: "application/octet-stream",
            Origin: allowedBrowserOrigin,
        })
    // Handle err, then deliver sessionURI only to the authorized client.
}
```

`ResumableUploader` returns a bearer URI to which the client PUTs bytes. Hosts
authorize Origin, configure bucket CORS and own session completion/cancellation.
GCS implements this protocol. S3 instead exposes the concrete
`InitiateMultipartUpload` method, returning an upload ID for the S3 part and
completion/abort APIs. Disk implements neither optional protocol.

`SignedURLer.SignedURL` returns a bearer GET URL without checking object existence.
Expiry must be whole seconds from one second through seven days; credentials can
expire sooner. Do not log signed URLs or session URIs. GCS requires explicit IAM
signing identity or a configured service-account private key; see the
[integration catalog](../integrations/catalog.md#file-storage) for provider setup.
S3 Upload uses bounded multipart buffering and supports objects up to about
48.8 GiB. Complete migration details are in AUDIT-013 in the root migration guide.

## Rate limiting HTTP and worker work

`Allower` admits one unit for a logical key. `Limiter` adds Reset; `Acquire` needs
only Allower and waits between denied attempts until admission or cancellation.
Hosts own limits, key namespaces and any account/plan lookup. Resolve fallible
policy first, then call Allow or Acquire. The SDK has no default tier resolver.

```go
limiter := ratelimiter.NewMemory(ratelimiter.WithMaxEntries(1000))
gate := ratelimiter.Middleware(limiter, ratelimiter.MiddlewareConfig{
    Limit: ratelimiter.PerMinute(60).WithBurst(10),
    Key: func(r *http.Request) string { return "catalog:public" },
    OnError: func(ctx context.Context, err error) {
        log.WarnContext(ctx, "catalog admission failed", "error", err)
    },
})
// Mount gate on the host's selected routes. This example shares one public budget.
```

Memory, Redis and Postgres share an anchored two-window counter approximation.
Burst adds to the ceiling; it is not a separate refill mechanism. Positive windows
round up to milliseconds. Invalid inputs (including empty keys) match
`sdk.ErrInvalidInput`. Requests+Burst must fit MaxCeiling and its product with
window milliseconds must be at most 2^52-1; Normalize exposes the same validation.
A live key's Window cannot change; reset, expire or version the key deliberately.
Changing the ceiling preserves consumed quota. ResetAt is the current bucket end;
RetryAfter is a backend-time retry checkpoint, not an exact earliest admission.

Memory defaults to 10,000 entries for nonpositive MaxEntries. It reclaims expired
keys on access/at capacity and never evicts active budgets. A new key at capacity
returns ErrCapacity, matching `sdk.ErrUnavailable`; existing budgets still work.
The limit is a count, not a byte budget. No janitor or Close is needed; hosts own
underlying clients and pools.

Middleware defaults to fail closed: dependency/capacity errors produce 503, invalid
configuration produces 500, exhaustion produces JSON 429 with Retry-After. Set
FailOpen explicitly for routes that should proceed during dependency failure.
OnError reports errors synchronously and must support concurrent requests. Caller
cancellation never invokes downstream work or writes a new response. Cancellation
cannot undo admission already committed by a remote backend.

Redis/Postgres now use internal v2 key prefixes; Postgres also requires a host
migration adding `window_ms`. Coordinate that rollout using AUDIT-011 in the root
consumer migration guide. Authentication explicitly preserves its open public-IP
and refresh-session outage policies and logs their failures.

The runnable `examples/minimal/internal/logic/domains/catalog/ratelimit_example_test.go`
shows host-owned policy resolution, HTTP-style Allow, and worker Acquire, including
failure before quota consumption.

## Caching application data and public pages

`cacher` has three responsibilities in one capability package:

| Surface | Responsibility |
|---|---|
| `Storer` | Concurrency-safe Get/GetMany/Set/Delete of owned bytes; empty values are hits |
| `Cache` | Logical namespaces, error reporting, strict raw operations and JSON helpers |
| `Pages` | Opt-in caching of eligible public GET HTML responses |

### Storage and ownership

`NewMemory(WithMaxEntries(1000))` supplies a bounded stdlib LRU. A
nonpositive MaxEntries defaults to 10,000. Reads and writes copy bytes; changing a
returned slice cannot alter another request's result. Expired keys are reclaimed
on access and before capacity evicts live keys. There is no janitor or Close.
Capacity limits entry count, not total payload bytes, and eviction can happen
before TTL. Construct Memory with NewMemory; its zero value is not initialized.

Raw Set and SetJSON require an explicit TTL: zero means no expiration, positive
means expiration, negative returns an error matching `sdk.ErrInvalidInput`.
Redis rounds positive TTL up to milliseconds. A canceled operation returns the
caller-context error; cancellation is not a rollback of completed writes.
The host owns any underlying Redis client and closes that client directly.

`PrefixDeleter` is optional. `DeletePrefix(ctx, "catalog:")` matches a literal
prefix; `*`, `?`, brackets and backslashes are ordinary characters. Empty clears
the adapter's namespace. Only stores implementing this capability should expose
it. `cachertest.Run` tests core storage; `cachertest.RunPrefix` tests this optional
contract. Use `Noop{}` to disable storage while preserving the same valid-input
and cancellation rules.

### Host-owned data caching

```go
cache := cacher.New(cacher.NewMemory(cacher.WithMaxEntries(1000)),
    cacher.WithNamespace("catalog:v1"),
    cacher.WithOnError(func(ctx context.Context, operation string, err error) {
        log.WarnContext(ctx, "catalog cache failed", "operation", operation, "error", err)
    }),
)
items, err := cacher.GetOrLoadJSON(ctx, cache, "published:v1", 30*time.Second, source.ListPublished)
```

New accepts optional namespace and error-reporting options; the namespace is
length-framed even when empty. Nil
storage selects Noop. Keep namespace/schema versions, keys, TTLs and public/private
scope in host or pocket logic. No repository is automatically cached.

`GetJSON[T]` returns `(T, found, error)` and preserves zero, false and JSON null as
hits. `SetJSON` reports and returns serialization/storage errors. Raw Cache
operations are also strict. `GetOrLoadJSON` alone treats cache/codec errors as
misses: it reports them through OnError, calls the supplied loader with the caller
context, and returns successful source data even if storing it fails. Loader and
caller-context errors reach the caller and are never cached. OnError is a
synchronous, concurrency-safe host hook; the cache adds no keys or payloads to it.

The load helper does not coalesce concurrent misses, refresh in the background,
or suppress authoritative errors. Hosts choose these policies when needed.
`Cache.InvalidatePrefix(ctx, "published:")` invalidates a logical prefix in its
namespace or returns `errors.ErrUnsupported` if the underlying store lacks
PrefixDeleter. The facade itself does not claim that optional storage interface.
Invalidation can race an in-flight loader or writer; TTL remains part of the
freshness contract.

For a runnable host example, start `examples/minimal` with `go run ./cmd/server`
and request `http://localhost:8081/catalog.json`. It serves the first 100 published
products through a host service and a CMS adapter, caches only their public
projection for 30 seconds, and logs cache errors. The JSON route uses HTTP
`no-store` independently of server-side data caching. Replace its cache storage
with Noop to load the source on every request. See the host's
`cmd/server/catalog_cache_test.go` for real HTTP, invalidation, disabled-cache,
outage, corrupt-entry and source-error behavior.

### Public HTML response caching

```go
pageCache := cacher.Pages(store, cacher.PageConfig{
    TTL:          30 * time.Second,
    MaxBodyBytes: 512 << 10,
})
```

Zero page TTL selects 60 seconds; negative disables Pages. MaxBodyBytes defaults
to 1 MiB when nonpositive. Nil storage bypasses the middleware. This default is
specific to Pages; raw Set's zero still means no expiration.

Pages keys include request scheme, authority, exact URI and optional
`Scope func(*http.Request) string`. Scope adds a trusted host-owned partition,
such as a tenant ID. Proxy-header trust remains host configuration. Versioned
response records sit under hashed `page:v2:` keys and carry absolute expiration.

Mount Pages only on public, cacheable GET HTML routes. Credential/cookie/principal,
conditional/range and request cache-directive traffic bypasses even a warm entry.
Responses must be successful HTML with an explicit valid Content-Type. Cookies,
Vary, content encoding, nonce CSP, explicit Date/Content-Length/Expires/Age,
trailers and unsupported metadata bypass storage. Cache-Control supports public,
positive max-age/s-maxage bounds, must-revalidate/proxy-revalidate, no-transform
and immutable. Other directives, including private/no-store/no-cache, bypass.

Hits preserve committed Content-Type/charset, Content-Language, Cache-Control,
ETag/Last-Modified, Link and supported CSP/security headers. Outer metadata
conflicts cause a render. Put compression and per-request headers outside Pages;
request IDs, tracing/timing fields and Date are freshly supplied rather than
replayed. Nonce-dependent pages and responses varying by unkeyed request state
must not use Pages. Oversized or failed responses keep streaming without being
cached. Cache outages become misses; the data Cache's OnError hook does not
implicitly instrument Pages.

`web.NoStore()` is respected inside or outside Pages. A stricter current TTL or
body limit also applies to existing entries. Hits carry Age and X-Cache: HIT;
eligible lookup misses carry X-Cache: MISS. A host can invalidate raw page entries
with `PrefixDeleter.DeletePrefix(ctx, "page:")`; this is not an immediate
invalidation guarantee against concurrent renders.

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
posture, err := notify.CheckTransport(mode, sender)
if err != nil {
    return err
}
```

Production rejects console or metadata-less implementations instead of assuming they are safe. Development can inspect the returned posture and warn while remaining usable.

This policy lives with the capability because every consumer should observe the same rule. The SDK does not log composition-specific prose or choose a sender.

## Select deliveries for each send

A call sends exactly the prepared deliveries it receives. Each channel retains
its own typed content. For an outage, the host can select both email and Slack:

```go
err := notify.Send(ctx,
    email.NewDelivery(emailSender, outageEmail),
    notify.DeliveryFunc(func(ctx context.Context) error {
        return slackSender.Send(ctx, outageSlack)
    }),
)
```

For a password reset, select only the email:

```go
err := notify.Send(ctx, email.NewDelivery(emailSender, resetEmail))
```

`email` is imported from `sdk/capabilities/notify/email`. Its prepared delivery
snapshots recipients and preserves the full `email.Message`, including HTML and
plain text. Host-specific Slack/SMS clients fit `DeliveryFunc`; no universal
message shape or global registration is required.

Calls run sequentially in argument order. Ordinary failures do not prevent later
selected deliveries; caller cancellation skips remaining work. On failure,
`errors.As(err, &sendError)` exposes `*notify.SendError`. Its `Failures` identify
the original argument `Index`, whether it was `Attempted`, and the underlying
`Err`. Positions absent from that list succeeded. Empty selections are invalid.
There are no automatic retries; a remote error cannot prove non-delivery, and
retrying the entire call would resend successes.

`notify/email` also provides `NewRenderer(options...)` for rendering without a
sender. `Render(email.RenderRequest{Template, Subject, Data, Layout})` returns
HTML and text. Register deliberate `.html` and `.txt` content; text uses
`text/template`, and missing or broken text is an error. Subject reaches layouts
explicitly, and Data works as a struct or map. Empty Layout selects transactional;
an unknown explicit layout fails. App > Core > Infra overrides remain available.

A capability may have cohesive subpackages, so notify/email can import notify.
Different capability subsystems remain independent; external providers belong in
integrations. A separate bridge module is unnecessary for this composition.

## Event notification and checked delivery

`Emitter.Emit(ctx, event)` admits asynchronous notification without waiting for
handlers or persistence. Use it for observation, wake-ups, cache invalidation and
live updates where consumers can re-fetch authoritative state. Emit has no delivery
options; the former WithSync option is removed.

| Call | What success means |
|---|---|
| `bus.Emit(ctx, event)` | asynchronous admission; later encoding, transport or handler failures are logged |
| `memory.Dispatch(ctx, event)` | all selected local handlers completed successfully |
| `redisBus.Publish(ctx, event)` | Redis accepted the stream entry; pub/sub notification was attempted on a best-effort basis |

Dispatch returns the first handler error, recovers callback panics as
`events.ErrHandlerPanic`, and checks cancellation between callbacks and before
returning success. Register required handlers before using Dispatch as a durable
outbox handoff: an empty selection is successful. Redis Publish does not force
local handler execution. A committed remote write cannot be undone by cancellation.

`Subscribe(topic, handler)` means notification fanout, with an exact topic or
`"*"`. Redis uses pub/sub for this operation, so connected instances each receive
notifications and disconnected or slow subscribers can miss them. `Broadcaster`
remains available for explicit fanout; `Subscriber` is the smaller port needed by
`WakeChannel`. Competing Redis work uses the separate integration method
`SubscribeWork(ctx, topic, handler)`, with exact topics only.

Memory defaults to four workers and a queue of 1000. Memory and Redis return
`events.ErrCapacity` when asynchronous admission is full and `events.ErrClosed`
after closure; both match `sdk.ErrUnavailable`. Invalid events/subscriptions match
`sdk.ErrInvalidInput`: event types cannot be empty or `"*"`, and subscriptions need
a nonempty topic and non-nil handler. Accepted events retain context values but
detach request cancellation. Keep events and referenced data immutable, make
handlers safe for concurrent calls, and tolerate duplicates; there is no total
ordering guarantee.

The host stops producers before closing a bus. Close drains admitted publications
and waits for active callbacks. Each caller waits for the same completion with its
own context; a timeout returns the context error without terminating a callback.
Callbacks must not call their own bus's Close. `events.Noop{}` explicitly disables
notifications and owns no lifecycle state; it supplies no checked delivery method.

`Record` holds stable event identity, type, occurrence time, correlation, payload
bytes and optional aggregate/tenant metadata. `NewRecord(event)` copies payload
and metadata, preserving a nonempty `Identified.EventID()` or generating one.
`record.Event()` returns a copied `RemoteEvent` embedding that Record. Use EventID
for duplicate detection; CorrelationID can group several distinct events. Metadata
comes from the envelope, so DecodeRemoteMetadata is removed. JSON represents
Record's opaque payload as base64; RemoteEvent.Unmarshal and TypedHandler's remote
fallback decode JSON payloads only. Binary consumers decode the bytes explicitly.

For side effects that must survive a crash, append to the
[events pocket's transactional outbox](../pockets/events.md#outbox-poller) or use
the keyed work protocol implemented by jobs. The poller receives the exact checked
delivery function the host requires; asynchronous Emit is insufficient for this
handoff. See the [Redis integration](../integrations/catalog.md#redis) for recovery
and transport migration requirements.

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

Logical keys must be nonempty; empty admission, replacement and status reads
return `sdk.ErrInvalidInput`. Keys are shared across kinds, so hosts choose an
appropriate workflow/tenant namespace. Concurrent admission under a key produces
one active execution, preserving its original kind and payload. Replacement
atomically supersedes older active work. Payloads are opaque byte snapshots;
caller mutation after admission must not change queued data. `worktest` exercises
concurrent admission/replacement, payload ownership and status vocabulary.

Executor-side claim, lease, checkpoint, middleware and fencing are outside this
consumer protocol. Jobs implements the protocol and supplies its richer executor
domain. The capability ships no default; a custom implementation can be
process-local, with durability determined by its chosen store.

## Capability middleware stays with its owner

Pure HTTP mechanics live in `pkg/web`. Middleware that combines a capability with HTTP lives in the capability:

- `cacher.Pages`;
- `ratelimiter.Middleware`;
- `tracing.Middleware`.

That direction lets capabilities depend on web mechanism without web importing every service facility.

## Transactions

`capabilities/transaction.Transactor` exposes one method:
`Transact(context.Context, func(context.Context) error) error`. Pass the callback
context to participating repositories backed by the same datastore instance.
Only operations that use its transaction handle participate. Independently
opened connections, other stores and external effects are outside that scope.

A successful return means the transaction committed. An error or panic aborts
an uncommitted attempt; panic values survive cleanup. Preserve domain error
matching with `errors.Is`/`errors.As`, since cleanup can add another error cause.
Nesting through `Transact` is rejected by the bundled connectors.

A connector may retry the callback. Reset captured results at the start of each
attempt and perform external effects only after `Transact` returns nil. Use a
durable outbox when an effect must survive a crash after commit. Firestore
requires reads before writes and does not observe its pending writes. SQL
isolation and lock behavior remain connector-specific.

SQL commit uses the Begin context. Rollback receives an independent five-second
deadline; actual completion depends on the driver. Cancellation racing a commit
cannot guarantee undoing a commit already accepted by the server.

## OAuth and HTTP tracing boundaries

OAuth's core `Provider` owns authorization URL construction, code exchange and
userinfo. `AuthorizationRequest` makes state, verifier, nonce and redirect URI
explicit; optional `IDTokenValidator` and `TokenRefresher` expose real additional
capabilities. Providers report `EmailVerified` and `EmailAuthoritative` evidence;
authentication hosts choose email trust. Client flow proof and session delivery
belong to authentication and its transports, not this capability.

Tracing keeps its small string-attribute span port. Optional `HTTPTracer` and
`HTTPSpan` let an adapter receive request metadata at span creation and typed
status at completion. SDK middleware records safe errors for failed responses
and panics, preserves committed status and finishes once. The OTel adapter adds
server spans, typed HTTP metadata and explicitly enabled W3C propagation.
