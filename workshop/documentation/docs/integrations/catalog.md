---
title: Integration catalog
description: Reusable Gopernicus connectors for third-party libraries and vendor APIs.
---

# Integration catalog

Integrations isolate concrete technology at the edge of the dependency graph. A module wraps one third-party library/family or one external vendor API contract and implements SDK or consumer-declared ports without importing a pocket.

## Datastores

| Module | Technology | Surface |
|---|---|---|
| `integrations/datastores/pgxdb` | pgx v5 / PostgreSQL | connection pool, transactions, error mapping, status, migrations, CRUD list toolkit, durable rate limiter |
| `integrations/datastores/turso` | libSQL / Turso | symmetric database wrapper, transactions, error mapping, status, migrations, CRUD list toolkit |
| `integrations/datastores/firestore` | Firestore Native mode | document I/O, transactional snapshots, error mapping, listing and index-manifest tooling |

Datastore integrations own how to talk to a database. They do not own pocket tables or SQL; those live in pocket `stores/<dialect>` modules.

SQL connectors support host-driven migration runners over flat directories.
PostgreSQL configuration has environment tags for host parsers; pgx's DSN parser
also honors standard `PG*` defaults. Turso configuration is supplied explicitly.
Firestore has its own document/index lifecycle rather than SQL migrations.

Omitted PostgreSQL pool lifetimes retain DSN/driver defaults; positive fields
override them. Authentication, authorization and events SQL store constructors
take the host's startup context so schema probing follows its cancellation and
deadline. The host owns clients, migrations and connection policy.

Query logging is opt-in, logs arguments verbatim, and is development-only.

## Cryptography and identifiers

| Module | Implements | Notes |
|---|---|---|
| `integrations/cryptids/bcrypt` | authentication's password-hasher shape | rejects passwords over bcrypt's 72-byte boundary instead of truncating |
| `integrations/cryptids/golang-jwt` | `cryptids.JWTSigner` | HMAC JWTs with method pinning and minimum secret length |
| `integrations/cryptids/google-uuid` | `sdk.IDGenerateFunc` | UUID v4 or time-ordered v7 entity IDs |

Password policy stays in authentication; bcrypt owns hashing mechanics. Entity ID choice is made once in pocket config and never controls secret/token generation.

## Email and notification

| Module | Implements | Notes |
|---|---|---|
| `integrations/email/sendgrid` | `email.Sender` | Twilio SendGrid v3 mail API |

Hosts select deliveries per call through `sdk/capabilities/notify`; email
delivery composition lives in `sdk/capabilities/notify/email` and accepts an
`email.Sender` such as SendGrid. No separate mailer bridge is needed.

SDK also ships SMTP and console email implementations. Production hosts should run the capability posture check instead of inferring safety from a type name.

## File storage

| Module | Technology | Capabilities |
|---|---|---|
| `integrations/filestorage/gcs` | Google Cloud Storage | `Storer`, `SignedURLer`, client PUT sessions through `ResumableUploader` |
| `integrations/filestorage/s3` | AWS SDK v2 / S3-compatible | `Storer`, `SignedURLer`; concrete `InitiateMultipartUpload` returns an S3 upload ID |

Both use the [SDK storage contract](../sdk/capabilities.md#file-storage-and-object-ownership):
canonical keys, literal prefixes, streaming replacement, stored-byte reads and
consistent range/error rules. Hosts inject the adapter directly and own resource
closure and error reporting. SDK `filestorage.Disk` remains the local default.

S3 supports custom endpoints and path-style addressing for MinIO and other
compatible services. `Open` rejects incomplete static credential pairs; supplying
neither selects the AWS default chain. `Open` also requires a resolved signing
region, honoring explicit configuration and AWS environment/shared-config fallback.
AWS transfermanager v0.1.6 buffers 5 MiB
parts with two concurrent requests, supporting uploads up to about 48.8 GiB.
Failed uploads attempt abort with a separate 30-second cleanup deadline and
preserve source/provider/cleanup errors. Explicit missing-bucket errors remain
errors; a bare HEAD 404 cannot distinguish a missing bucket from a missing key.
The host owns the S3 client's resources and any explicit multipart lifecycle.

GCS `Open(ctx, Config, opts...)` accepts credentials, ADC and emulator endpoints.
Its optional Prefix is a canonical directory prefix with one trailing slash;
invalid prefixes are rejected and keys are never rewritten. `WithClientOption`
retains vendor configuration, including authenticated HTTP clients, endpoints
and credentials. The configured HTTP client is shared by object operations,
resumable initiation and IAM signing. Credential discovery may perform I/O;
supply a startup deadline. Stop store users before `Store.Close()`.

For GCS signed reads, set `Config.SigningServiceAccount` to explicitly use IAM
SignBlob, or supply a service-account private key in `Config.CredentialsJSON`
while leaving SigningServiceAccount empty for local signing. ADC or credentials
supplied only through vendor options do not infer signing identity. SignedURL
requires whole-second expiry from one second through seven days; credential
expiry may shorten it. IAM requests use the caller context and configured auth.

GCS resumable initiation now uses the authenticated JSON upload API, independent
of signed-URL configuration. Pass ContentType and Origin through `filestorage.ResumableUploadOptions`; the
host authorizes the browser origin and configures bucket CORS.
The result is a bearer URI the client PUTs to. S3 multipart IDs cannot be used
through that protocol. Keep both signed URLs and session URIs out of logs.

The shared suite and provider tests cover these contracts. Live cloud tests
remain explicitly environment-gated; green hermetic tests alone do not verify
cloud permissions, persistence or browser CORS. See AUDIT-013 in the root
migration guide for consumer changes and the implementation plan for current
verification results.

## Redis

`integrations/kvstores/goredis` wraps one caller-owned go-redis client and exposes three facilities:

- an `events.Bus`/`Broadcaster` for notification fanout, plus explicit stream publication and competing work subscriptions;
- a `cacher.Storer`;
- a sliding-window `ratelimiter.Limiter`.

One library genuinely serves all three ports, so one module is the meaningful dependency boundary.

```go
rdb := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
    ContextTimeoutEnabled: true,
})
defer rdb.Close()

bus := goredis.New(rdb,
    goredis.WithLogger(log),
    goredis.WithStreamPrefix("events:"),
    goredis.WithConsumerGroup("myapp"),
)
cache := goredis.NewCacher(rdb)
limiter := goredis.NewLimiter(rdb)
```

Each facility owns its bookkeeping but does not close the shared client.
`goredis.Open` enables context deadlines for network I/O. Borrowed clients should
set `ContextTimeoutEnabled` when that behavior is required; wrappers leave their
transport configuration under host control.

For events, `Emit(ctx, event)` queues bounded asynchronous publication;
`Publish(ctx, event)` checks XADD acceptance and then attempts best-effort pub/sub
notification. Publish does not invoke local handlers. `Subscribe` and
`SubscribeBroadcast` receive ephemeral fanout for an exact topic or `"*"`.
Use `SubscribeWork(ctx, exactTopic, handler)` for reliable competing consumers.
Instances in one ConsumerGroup must deploy the same handler responsibilities for
each topic. See [SDK events](../sdk/capabilities.md#event-notification-and-checked-delivery)
for admission errors, event ownership and checked local dispatch.

Work recovery requires **Redis 6.2 or newer**. Each worker claims one record at a
time and alternates fresh reads with XAUTOCLAIM recovery. RetryAfter defaults to
one minute; HandlerTimeout defaults to 30 seconds and covers the entire selected
handler attempt. Both must be positive whole-millisecond durations, with
RetryAfter greater than HandlerTimeout; work registration checks these settings.
BlockTimeout also requires a positive whole-millisecond duration. Workers defaults
to four and bounds each publisher/consumer pool; QueueSize defaults to 1000 waiting
asynchronous publications.

Only successful complete attempts are acknowledged. Handler errors, panics,
cancellation, malformed records and missing handlers leave entries pending.
Unsubscribe stops future topic selection; a read already in progress can still
claim an entry, which stays pending without a current handler. Lost groups are
recreated from the start, so retained entries can be delivered again. Timeouts
cancel callback contexts but cannot stop callbacks that ignore them; redelivery
can overlap. Handlers must be idempotent. There is no automatic poison discard,
maximum attempt count or dead-letter mechanism: hosts monitor pending entries and
own repair or explicit terminal disposition through their Redis client.

The former event-bus MaxLen and BatchSize options are removed. Streams are never
automatically trimmed; hosts own retention and archival, considering every
consumer group before deleting acknowledged records. Stop producers/pollers,
close the bus and await its callbacks/publications, then close the shared client.
A Close deadline reports incomplete draining and can be followed by another Close.

### Existing Redis event deployments

The adapter appends internal `v2:` to every effective StreamPrefix and uses one
serialized Record envelope per entry. The default `content.published` stream is
now `events:v2:content.published`, and pub/sub uses `events:v2:broadcast`. IDs,
metadata and opaque payload bytes survive both transports. Outbox schema is
unchanged; legacy Redis entries are not converted, replayed or deleted.

Coordinate the cutover: stop old writers and drain old streams with old consumers,
or retain unresolved old pending work for explicit repair/replay. Choose disjoint
physical prefixes; an old logical topic starting with `v2:` can overlap the new
namespace, so the internal marker alone does not isolate arbitrary old data.
Use a fresh host prefix when needed. Do not assume mixed versions communicate or
share delivery state. Replay with preserved EventIDs and duplicate-safe handlers;
keep old pending records until the host has accounted for them. Neither Close nor
the upgrade transfers old pending work or supplies a retention policy.

## OAuth

| Module | Protocol | Construction posture |
|---|---|---|
| `integrations/oauth/github` | GitHub OAuth 2.0 + user/email APIs | no construction-time network request |
| `integrations/oauth/google` | OAuth 2.0 + OpenID Connect | fetches discovery at construction; bound it with a timeout |

Both implement `sdk/capabilities/oauth.Provider`, take concrete `Config` values and use PKCE. GitHub construction returns local configuration errors; Google adds optional ID token validation. Both expose optional refresh, copy scopes/client configuration, refuse redirects and reject malformed or oversized responses. Email evidence is separate from host trust. GitHub is stdlib-only but remains an integration because it isolates GitHub's live vendor API contract. Google uses OIDC discovery/JWKS verification through `go-oidc`.

## Scheduling and tracing

| Module | Implements | Notes |
|---|---|---|
| `integrations/scheduling/robfig-cron` | jobs' cron-parser shape | five-field cron + descriptors, evaluated in UTC |
| `integrations/tracing/otel` | `tracing.Tracer` | stdout, OTLP/gRPC, or caller-supplied provider |

The OpenTelemetry connector owns exporter construction and returns explicit `Shutdown`/`ForceFlush`. `otel.Middleware` starts HTTP server spans with typed metadata and optional W3C parent trust; passing a nil tracer yields Noop middleware. OTLP literal sample rate zero disables root sampling, while parent decisions take precedence. Environment loading defaults the ratio to one.

## How to choose or add an integration

Use an existing integration when its generic seam fits. Keep mapping specific to your domain in a pocket store or host outbound adapter.

Add a reusable integration when:

- a third-party library/family or vendor API is the real boundary;
- it implements an existing stable port or consumer-declared structural seam;
- configuration and lifecycle ownership are explicit;
- backend errors map to capability/domain error vocabulary where appropriate;
- construction-time network behavior is documented;
- hermetic tests and the relevant conformance suite cover observable behavior;
- it imports no pocket, example, or Workshop package.
