---
sidebar_position: 1
title: Overview
---

# Infrastructure

Infrastructure provides the concrete implementations for external concerns: caching, databases, file storage, email, events, cryptography, and more. Each package defines a generic interface for its concern, with one or more backend implementations in sub-packages.

## Package Structure

```
infrastructure/
├── cache/              # Caching (memory, Redis, noop)
├── communications/     # Email (SendGrid, stdout)
├── cryptids/           # Cryptography (bcrypt, AES-GCM, JWT, ID generation)
├── database/           # Database connections (PostgreSQL, SQLite, Redis KV)
├── events/             # Event bus (memory, Redis Streams, outbox)
├── httpc/              # HTTP client utilities
├── oauth/              # OAuth providers (GitHub, Google)
├── ratelimiter/        # Rate limiting (memory)
├── storage/            # Object storage (S3, GCS, disk)
├── testing/            # Shared test infrastructure (postgres, redis, HTTP server)
└── tracing/            # Distributed tracing (OTLP, stdout)
```

## The Pattern

Each infrastructure package follows the same structure:

1. **Interface** defined at the package root — the generic contract for this concern
2. **Implementations** in sub-packages — concrete backends
3. **Compliance suite** in a `*test/` sub-package — verifies any implementation satisfies the contract

```
cache/
├── cache.go        # Cacher interface + Cache service
├── cachetest/      # Compliance suite — run against any Cacher implementation
├── memorycache/    # In-process implementation
├── noopcache/      # No-op implementation
└── rediscache/     # Redis implementation
```

## Who Owns the Interfaces

Infrastructure packages own their own interface definitions. `events.Bus`, `cache.Cacher`, `storage.Client` — these live in Infrastructure, not Core. Core imports and programs against them directly.

This is a deliberate choice. For cross-cutting external concerns used across many Core packages, duplicating an identical interface definition in Core adds indirection without adding value. The interfaces are domain-agnostic by design — `events.Bus.Publish(ctx, Event)`, not `PublishUserCreated(ctx, UserID)` — so the coupling is appropriate.

The rule that keeps this safe: **infrastructure interfaces must never contain domain knowledge**. As long as they stay generic, Core depending on them is fine.

## Working with Infrastructure Interfaces

Three patterns cover the space between "the generic interface is exactly right" and "the abstraction doesn't hold here at all."

### Satisfiers

A satisfier adapts between two *existing* interfaces. Use this when an infrastructure interface and a Core interface describe the same capability but with different types or method signatures.

The authentication package is a real example. `authentication.UserRepository` is a Core interface for user operations. The generated `users.Repository` is the data store. They aren't the same shape, so a satisfier bridges them:

```go
// core/auth/authentication/satisfiers/users.go

// UserSatisfier satisfies authentication.UserRepository using the generated users repository.
type UserSatisfier struct {
    repo userRepo
}

func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
    user, err := s.repo.GetByEmail(ctx, email)
    if err != nil {
        return authentication.User{}, err
    }
    return toAuthUser(user), nil
}
```

Satisfiers live close to the interface they're satisfying — in the Core or cases package that defines the need, not in infrastructure.

### Extended Interfaces

Sometimes the generic infrastructure interface isn't enough — your domain needs a capability that requires backend-specific implementation. Define a domain interface that extends the infrastructure one and provide per-backend implementations.

For example, an audio feature might need `MuxAudio` alongside standard storage operations. Not all backends can provide this, and the implementations will differ:

```go
// cases/audio/storer.go

// AudioStorer extends the generic storage interface with audio-specific operations.
type AudioStorer interface {
    storage.Client
    MuxAudio(ctx context.Context, path string) error
}
```

```go
// cases/audio/extenders.go

type gcsAudioStorer struct {
    *gcsstorage.Client
}

func (g *gcsAudioStorer) MuxAudio(ctx context.Context, path string) error {
    // GCS-specific transcoding
}

type s3AudioStorer struct {
    *s3storage.Client
}

func (s *s3AudioStorer) MuxAudio(ctx context.Context, path string) error {
    // S3/MediaConvert equivalent
}
```

Your service takes `AudioStorer` — fully testable and backend-agnostic at the call site. The backend-specific coupling is contained in the extender implementations.

### Direct Injection

Sometimes the abstraction genuinely doesn't hold and there's no value in pretending otherwise. If a specific capability only exists on one backend and cannot be meaningfully generalized, inject the concrete client directly alongside the generic service.

PostgreSQL advisory locks are a good example. There's no SQLite equivalent, no generic `database.Client` method that maps to it, and writing one would be misleading. The honest approach is to take what you need:

```go
// cases/jobs/claimer.go

type Claimer struct {
    pool *pgxpool.Pool  // explicit: this feature requires PostgreSQL
}

func (c *Claimer) Claim(ctx context.Context, jobID string) error {
    _, err := c.pool.Exec(ctx, "SELECT pg_advisory_lock($1)", jobID)
    return err
}
```

The wiring layer already constructs both the concrete backend client and any generic services, so it has both available to inject:

```go
// app wiring
pgxPool   := database.NewPgx(cfg)
dbClient  := pgxprovider.New(pgxPool)   // satisfies database.Client
claimer   := jobs.NewClaimer(pgxPool)   // takes pgx directly
```

**Document the coupling explicitly** — the constructor signature should make it obvious that a specific backend is required.

---

Use the pattern that matches your situation. Most cases work fine with the generic interface directly — if your use case needs domain-specific methods on top (like `UploadAvatar` wrapping generic `Upload`), that's just standard composition inside your use case package. Use extended interfaces when multiple backends need to satisfy a new capability. Use direct injection when the abstraction would be dishonest.

## Logging

Infrastructure packages follow a consistent logging policy based on whether they operate synchronously or asynchronously.

**Asynchronous packages** — those with background goroutines or fire-and-forget paths — require a `*slog.Logger` in their constructor. Errors in async code paths have no caller to return to, so logging is the only way to surface them. This applies to `memorybus`, `goredisbus`, `outbox`, `throttler`, and `tokenbucket`.

**Synchronous packages** offer logging as an opt-in via `WithLogger(*slog.Logger)`. All errors are returned to the caller regardless — logging is purely observational. When a caller passes `WithLogger`, they're saying "log your defaults." When they don't, the package is silent.

```go
// Async — logger required (no caller to return errors to)
bus := memorybus.New(log)

// Sync — logger opt-in (caller gets the error either way)
storer := storage.New(client, storage.WithLogger(log))
e, err := emailer.New(client, defaultFrom, emailer.WithLogger(log))
limiter := ratelimiter.New(store, resolver, ratelimiter.WithLogger(log))
```

This keeps infrastructure from making assumptions about how your app handles observability. If you want storage to log not-found warnings, opt in. If you'd rather handle that in your domain layer, don't pass a logger.

## Test Infrastructure

Each package ships a compliance suite as a sub-package (`cachetest/`, `eventstest/`, `storagetest/`, etc.). Import the suite and call `RunSuite` in your adapter's test file to verify it satisfies the full contract. These live in infrastructure rather than the workshop testing layer to avoid circular imports — infrastructure packages can import each other's compliance suites freely.

`infrastructure/testing/` contains shared bootstrapping for integration tests: real database connections, Redis pools, HTTP test servers, and environment helpers. Use these when you need a real backend rather than a fake.

## Packages

| Package | Purpose |
|---|---|
| [Cache](./cache.md) | Key-value caching with TTL, bulk ops, and JSON helpers |
| [Communications](./communications.md) | Email rendering and delivery |
| [Cryptids](./cryptography.md) | Hashing, encryption, JWT, and ID generation |
| [Database](./database.md) | Database connection management |
| [Events](./events.md) | Domain event bus with outbox support |
| [HTTP Client](./httpc.md) | Outbound HTTP client utilities |
| [OAuth](./oauth.md) | OAuth 2.0 provider integrations |
| [Rate Limiting](./rate-limiting.md) | Token bucket rate limiting |
| [Storage](./storage.md) | Object storage for files and blobs |
| [Tracing](./tracing.md) | Distributed tracing exporters |
