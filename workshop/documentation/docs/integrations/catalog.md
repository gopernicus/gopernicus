---
title: Integration catalog
description: Reusable Gopernicus connectors for third-party libraries and vendor APIs.
---

# Integration catalog

Integrations isolate concrete technology at the edge of the dependency graph. A module wraps one third-party library/family or one external vendor API contract and implements SDK or consumer-declared ports without importing a feature.

## Datastores

| Module | Technology | Surface |
|---|---|---|
| `integrations/datastores/pgxdb` | pgx v5 / PostgreSQL | connection pool, transactions, error mapping, status, migrations, CRUD list toolkit, durable rate limiter |
| `integrations/datastores/turso` | libSQL / Turso | symmetric database wrapper, transactions, error mapping, status, migrations, CRUD list toolkit |

Datastore integrations own how to talk to a database. They do not own feature tables or SQL; those live in feature `stores/<dialect>` modules.

Both connectors support host-driven migration runners and explicit configuration. They never read environment variables inside `Open`; config structs carry tags so the host may use `environment.ParseEnvTags` or construct them directly.

Query logging is opt-in, logs arguments verbatim, and is development-only.

## Cryptography and identifiers

| Module | Implements | Notes |
|---|---|---|
| `integrations/cryptids/bcrypt` | authentication's password-hasher shape | rejects passwords over bcrypt's 72-byte boundary instead of truncating |
| `integrations/cryptids/golang-jwt` | `cryptids.JWTSigner` | HMAC JWTs with method pinning and minimum secret length |
| `integrations/cryptids/google-uuid` | `cryptids.GenerateFunc` | UUID v4 or time-ordered v7 entity IDs |

Password policy stays in authentication; bcrypt owns hashing mechanics. Entity ID choice is made once in feature config and never controls secret/token generation.

## Email and notification

| Module | Implements | Notes |
|---|---|---|
| `integrations/email/sendgrid` | `email.Sender` | Twilio SendGrid v3 mail API |
| `integrations/notify/mailer` | `notify.Notifier` | composing integration: email-kind notification over an `email.Sender` |

The mailer bridge demonstrates cross-capability composition outside SDK. It has no third-party dependency of its own but still belongs in integrations because it joins two behavioral packages.

SDK also ships SMTP and console email implementations. Production hosts should run the capability posture check instead of inferring safety from a type name.

## File storage

| Module | Technology | Capabilities |
|---|---|---|
| `integrations/filestorage/gcs` | Google Cloud Storage | core storage, signed URLs, resumable upload |
| `integrations/filestorage/s3` | AWS SDK v2 / S3-compatible | core storage, presigned GET, multipart upload |

S3 supports custom endpoints and path-style addressing for MinIO and DigitalOcean Spaces. GCS supports credentials or ADC and emulator endpoints. Both pass the SDK file-storage conformance suite; SDK `filestorage.Disk` remains the zero-infrastructure default.

## Redis

`integrations/kvstores/goredis` wraps one caller-owned go-redis client and exposes three facilities:

- an `events.Bus`/`Broadcaster` over streams and pub/sub;
- a `cacher.Storer`;
- a sliding-window `ratelimiter.Limiter`.

One library genuinely serves all three ports, so one module is the meaningful dependency boundary.

```go
rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
defer rdb.Close()

bus := goredis.New(rdb, log, goredis.Options{
    StreamPrefix:  "events:",
    ConsumerGroup: "myapp",
})
cache := goredis.NewCacher(rdb)
limiter := goredis.NewLimiter(rdb)
```

Each facility owns its bookkeeping but does not close the shared client.

## OAuth

| Module | Protocol | Construction posture |
|---|---|---|
| `integrations/oauth/github` | GitHub OAuth 2.0 + user/email APIs | no construction-time network request |
| `integrations/oauth/google` | OAuth 2.0 + OpenID Connect | fetches discovery at construction; bound it with a timeout |

Both implement `sdk/capabilities/oauth.Provider` and use PKCE. GitHub is stdlib-only but remains an integration because it isolates GitHub's live vendor API contract. Google uses OIDC discovery/JWKS verification through `go-oidc`.

## Scheduling and tracing

| Module | Implements | Notes |
|---|---|---|
| `integrations/scheduling/robfig-cron` | jobs' cron-parser shape | five-field cron + descriptors, evaluated in UTC |
| `integrations/tracing/otel` | `tracing.Tracer` | stdout, OTLP/gRPC, or caller-supplied provider |

The OpenTelemetry connector owns exporter construction and returns an explicit `Shutdown`. A host can choose `tracing.Noop` and keep tracing middleware wired, then switch only the tracer at deployment.

## How to choose or add an integration

Use an existing integration when its generic seam fits. Keep mapping specific to your domain in a feature store or host outbound adapter.

Add a reusable integration when:

- a third-party library/family or vendor API is the real boundary;
- it implements an existing stable port or consumer-declared structural seam;
- configuration and lifecycle ownership are explicit;
- backend errors map to capability/domain error vocabulary where appropriate;
- construction-time network behavior is documented;
- hermetic tests and the relevant conformance suite cover observable behavior;
- it imports no feature, example, or Workshop package.
