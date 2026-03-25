# Infrastructure Layer

The infrastructure layer contains adapters for external systems: databases,
caches, message buses, file storage, email providers, OAuth providers, and
cryptographic utilities. Each package defines an interface (port) and one or
more implementations. Core and bridge layers depend on these interfaces; the
app layer selects which implementation to wire in.

---

## Database: `infrastructure/database/`

### `postgres/pgxdb`

PostgreSQL adapter built on `jackc/pgx/v5`. Provides:

- **`Pool`** -- wraps `pgxpool.Pool` with env-tag config (`Options` struct).
- **`fop.go`** -- cursor-based pagination helpers: `ApplyCursorPaginationFromToken`,
  `AddOrderByClause`, `AddLimitClause`. These translate `sdk/fop` types into
  SQL clauses for generated store queries.
- **`cursor.go`** -- keyset pagination with stale-cursor detection.
- **`escape.go`** -- `QuoteIdentifier` for safe dynamic SQL.

### `kvstore/goredisdb`

Redis adapter built on `go-redis/redis/v9`. Returns a `*Client` wrapping
`redis.Client` with env-tag config, optional tracing transport.

### `sqlite`

SQLite adapter (available for lightweight or embedded use cases).

---

## Cache: `infrastructure/cache/`

**Interface: `Cacher`** -- `Get`, `GetMany`, `Set`, `Delete`, `DeletePattern`,
`Close`.

**Service: `Cache`** -- wraps any `Cacher` with optional OTEL tracing and
JSON convenience functions (`GetJSON[T]`, `SetJSON[T]`).

| Implementation | Package | Notes |
|----------------|---------|-------|
| In-memory | `memorycache` | TTL-based, single-process. Good for dev/test. |
| Redis | `rediscache` | Wraps `goredisdb.Client`. Production default. |
| No-op | `noopcache` | All operations succeed silently. For when caching is disabled. |

```go
cacher := memorycache.New(memorycache.Config{})
c := cache.New(cacher, cache.WithTracer(tracer))
```

---

## Events: `infrastructure/events/`

**Interface: `Bus`** -- `Emit(ctx, event, ...EmitOption)`, `Subscribe(topic, handler)`, `Close(ctx)`.

**`Event` interface** -- `Type()`, `OccurredAt()`, `CorrelationID()`. Embed
`BaseEvent` in domain event types.

**`TypedHandler[T]`** -- type-safe event handler that only processes matching
event types.

| Implementation | Package | Durability |
|----------------|---------|------------|
| In-memory | `memorybus` | None. Events lost on restart. Default for dev. |
| Redis Streams | `goredisbus` | At-least-once via XADD/XREADGROUP/XACK. |
| Outbox | `outbox` | Decorates any Bus. Events marked `ToOutbox()` are written to DB atomically. |

```go
bus := memorybus.New(log)
bus.Subscribe("user.created", events.TypedHandler(func(ctx context.Context, e UserCreatedEvent) error {
    return sendWelcomeEmail(e.Email)
}))
bus.Emit(ctx, event, events.WithSync())
```

The **event registry** (`registry.go`) enables dynamic handler registration by
event type string.

---

## Storage: `infrastructure/storage/`

**Interface: `Client`** -- `Upload`, `Download`, `DownloadRange`, `Delete`,
`Exists`, `List`, `GetObjectSize`.

**Service: `FileStorer`** -- wraps any `Client` with structured logging.

| Implementation | Package | Notes |
|----------------|---------|-------|
| Local disk | `diskstorage` | Writes to filesystem. Good for dev. |
| Google Cloud Storage | `gcs` | Uses GCS client library. |
| AWS S3 | `s3` | Uses AWS SDK v2. |

```go
client := diskstorage.New("./data/uploads")
fs := storage.New(log, client)
fs.Upload(ctx, "avatars/abc.png", reader)
```

---

## Email: `infrastructure/communications/emailer/`

**Client interface** -- `Send(ctx, Email)`. Email has `To`, `From`, `Subject`,
`HTML`, `Text` fields.

**Service: `Emailer`** -- wraps a Client with template rendering, default
from address, and `notify.Notifier` implementation.

| Implementation | Package | Notes |
|----------------|---------|-------|
| Console | `stdoutemailer` | Logs emails to stdout. For dev/test. |
| SendGrid | `sendgridemailer` | Production email delivery via SendGrid API. |

Templates are registered via `embed.FS` with namespaces and layout support
(transactional, marketing, minimal).

---

## Cryptography: `infrastructure/cryptids/`

**ID generation** -- `GenerateID()` produces 21-character cryptographically
secure, URL-safe random strings. `GenerateCustomID(alphabet, length)` for
custom formats.

**Interfaces** -- `PasswordHasher`, `JWTSigner` (defined in the package and
structurally matched by `authentication`).

| Adapter | Package | Notes |
|---------|---------|-------|
| bcrypt | `cryptids/bcrypt` | `PasswordHasher` with configurable cost. |
| golang-jwt | `cryptids/golangjwt` | `JWTSigner` using HMAC-SHA256 with env-based secret. |
| AES-GCM | `cryptids/aesgcm` | `TokenEncrypter` for OAuth provider token storage. |

Also provides `SHA256Hasher` for non-password hashing (API keys, tokens).

---

## OAuth: `infrastructure/oauth/`

**Interface: `Provider`** -- `Name`, `SupportsOIDC`, `TrustEmailVerification`,
`GetAuthorizationURL`, `ExchangeCode`, `GetUserInfo`, `ValidateIDToken`,
`RefreshToken`.

| Implementation | Package | Notes |
|----------------|---------|-------|
| Google | `googleoauth` | Full OIDC with cryptographic ID token verification. |
| GitHub | `githuboauth` | OAuth 2.0 (no OIDC, uses GetUserInfo). |

Includes PKCE support (`pkce.go`) for mobile/SPA flows.

---

## Rate Limiter: `infrastructure/ratelimiter/`

**Interface: `Storer`** -- `Allow(ctx, key, Limit)`, `Reset(ctx, key)`,
`Close()`.

**Service: `RateLimiter`** -- combines a `Storer` with a `LimitResolver` for
subject-aware rate limits.

Helpers: `PerSecond(n)`, `PerMinute(n)`, `PerHour(n)`, `Limit.WithBurst(n)`.

| Implementation | Package |
|----------------|---------|
| In-memory | `memorylimiter` |

---

## HTTP Client: `infrastructure/httpc/`

Type-safe JSON HTTP client with generics support.

```go
client := httpc.NewClient(httpc.WithBaseURL("https://api.example.com"), httpc.WithBearerToken(token))
user, err := httpc.GetValue[User](client, ctx, "/users/123")
```

Supports tracing transport wrapper (`NewTracingTransport`).

---

## Tracing: `infrastructure/tracing/`

| Implementation | Package | Notes |
|----------------|---------|-------|
| OTLP | `otlptrace` | OpenTelemetry Protocol exporter. |
| Stdout | `stdouttrace` | Logs spans to stdout. For dev. |

---

## Related

- [SDK Layer](sdk.md) -- types that infrastructure adapters use
- [Core Layer](core.md) -- interfaces that infrastructure satisfies
- [Bridge Layer](bridge.md) -- middleware that uses infrastructure services
- [App Layer](app.md) -- selects and wires infrastructure implementations
