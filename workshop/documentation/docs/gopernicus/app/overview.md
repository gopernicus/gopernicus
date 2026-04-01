---
sidebar_position: 1
title: Overview
---

# App

The App layer is the composition root of a Gopernicus application. It sits at the outermost edge of the dependency graph, imports from every other layer, and wires everything together. No other layer imports from App.

The App layer contains no business logic — only configuration parsing, dependency construction, and startup/shutdown orchestration.

## Generated Structure

`gopernicus init` scaffolds the App layer:

```
app/
└── server/
    ├── main.go                    # Entry point, infrastructure bootstrap
    ├── config/
    │   └── server.go              # Composition root — DI, middleware, routes
    └── emails/
        ├── emails.go              # Email branding, template registration
        ├── layouts/
        │   ├── transactional.html # Branded HTML email layout
        │   └── transactional.txt  # Plaintext email layout
        └── templates/
            └── authentication/    # App-level overrides for auth emails
                ├── verification.html
                ├── verification.txt
                ├── password_reset.html
                ├── password_reset.txt
                ├── oauth_link_verification.html
                └── oauth_link_verification.txt
```

All files are scaffolded once and fully customizable.

## Boot Sequence

### main.go

`main.go` initializes infrastructure in order, then delegates to `server.New()`:

1. **Telemetry** — create provider, register globally
2. **Database** — parse config, open connection pool
3. **Redis** — connect (if configured)
4. **Event Bus** — memory or Redis Streams backend
5. **Cache** — memory or Redis backend
6. **Storage** — disk, GCS, or S3 (if configured)
7. **Email** — stdout or SendGrid backend
8. **Server** — pass all infrastructure to `server.New()`

Each step follows the same pattern — parse env config, construct the client:

```go
var pgCfg pgxdb.Options
if err := environment.ParseEnvTags(AppName, &pgCfg); err != nil {
    return fmt.Errorf("parsing postgres config: %w", err)
}
pool, err := pgxdb.New(pgCfg)
```

### server.go

`server.New()` receives infrastructure and composes the domain:

1. **Async Pool** — bounded goroutine pool for fire-and-forget tasks
2. **Rate Limiter** — memory-backed by default
3. **Repositories** — one `NewRepositories()` call per domain
4. **Authorization** — schema composition, cache store, authorizer
5. **Authentication** — satisfiers, hasher, JWT signer, authenticator
6. **Middleware** — global stack (Panics, Telemetry, TrustProxies, ClientInfo, Logger)
7. **Routes** — bridge composites registered on route groups
8. **OpenAPI** — aggregated specs served at `/openapi.json`
9. **HTTP Server** — configured and returned

## Configuration

All configuration uses `environment.ParseEnvTags()` with struct tags:

```go
type Options struct {
    DatabaseURL string        `env:"DB_DATABASE_URL" required:"true"`
    MaxConns    int           `env:"DB_MAX_CONNS" default:"25"`
    Timeout     time.Duration `env:"SHUTDOWN_TIMEOUT" default:"5s"`
}
```

The `AppName` (e.g., `MYAPP`) is prepended as a namespace: `MYAPP_DB_DATABASE_URL`, `MYAPP_DB_MAX_CONNS`, etc.

`.env.example` is scaffolded with all available configuration and sensible defaults.

## Infrastructure Injection

`server.New()` accepts an `Infrastructure` struct for external dependencies that tests need to swap:

```go
type Infrastructure struct {
    Pool     *pgxdb.Pool         // required: database connection
    Provider *telemetry.Provider // optional: nil disables tracing
    EventBus events.Bus          // optional: nil disables async events
    Cache    *cache.Cache        // optional: nil disables caching
    Storage  *storage.FileStorer // optional: nil disables file storage
    Emailer  *emailer.Emailer    // optional: nil disables email
}
```

Optional fields are nil-safe — passing `nil` disables the capability without changing any wiring code.

## Config Helpers

`main.go` includes helper functions that encapsulate backend selection logic:

| Helper | Env Var | Backends |
|---|---|---|
| `configEventBus` | `EVENT_BUS_BACKEND` | `redis-streams`, `memory` |
| `configCache` | `CACHE_BACKEND` | `redis`, `memory` |
| `configStorage` | `STORAGE_BACKEND` | `disk`, `gcs`, `s3` |
| `configEmail` | `EMAIL_BACKEND` | `sendgrid`, `stdout` |

Each reads a backend selection env var and constructs the appropriate client. Switch backends by changing an env var — no code changes.

## Email

The App layer owns email branding and template overrides via `app/server/emails/`.

### Template Layering

Email templates use a two-layer system:

```go
func Options() []emailer.Option {
    return []emailer.Option{
        // Framework defaults (LayerCore) — shipped with Gopernicus
        emailer.WithContentTemplates("authentication", authbridge.AuthTemplates(), emailer.LayerCore),

        // App overrides (LayerApp) — your copy and design
        emailer.WithContentTemplates("authentication", Templates, emailer.LayerApp),

        // App layouts override infrastructure defaults
        emailer.WithLayouts(Layouts, "layouts", emailer.LayerApp),

        // Branding data available in templates as .Brand
        emailer.WithBranding(NewBranding()),
    }
}
```

`LayerApp` templates override `LayerCore` templates with the same name. This means framework auth emails work out of the box, and you customize them by editing the files under `app/server/emails/templates/`.

### Branding

`NewBranding()` returns project-specific branding used in email layouts:

```go
func NewBranding() *emailer.Branding {
    return &emailer.Branding{
        Name:    "MyApp",
        Tagline: "Your Platform",
        LogoURL: "https://example.com/logo.png",
        Address: "123 Main St, City, State 12345",
        SocialLinks: []emailer.SocialLink{
            {Name: "Twitter", URL: "https://twitter.com/myapp"},
        },
    }
}
```

Templates access branding via `{{.Brand.Name}}`, `{{.Brand.LogoURL}}`, etc. The scaffolded transactional layout includes a branded header, content area, social links, and footer.

### Layouts

The transactional layout (`layouts/transactional.html`) wraps all email content with consistent branding. Both HTML and plaintext variants are scaffolded. The HTML layout includes a gradient header, content area, and dark footer with social links.

## Event Subscribers

Event subscribers are registered in `server.New()` after the event bus and domain objects are constructed:

```go
// Authentication email subscriber
authSubs := authbridge.NewSubscribers(mailer, log, frontendURL)
authSubs.Register(bus)

// Invitation auto-resolve subscriber
invitationSubs := invbridge.NewSubscribers(inviter, authRepos.Users, log)
invitationSubs.Register(bus)
```

Subscribers live in bridge packages but are wired here because the app layer owns the event bus and emailer.

## Async Patterns

The generated app documents four durability levels for background work:

| Pattern | Durability | Use When |
|---|---|---|
| `AsyncPool` | None — lost on crash | Cache invalidation, non-critical fanout |
| Memory Event Bus | None — lost on restart | Domain events where the request is the durability guarantee |
| Redis Streams Bus | Durable — survives restarts | Cross-process events with at-least-once delivery |
| Event Outbox | Durable — DB-backed | Critical events that must not be lost (atomic with business transaction) |

## Shutdown

Graceful shutdown follows a specific order:

1. **Stop HTTP server** — stops accepting new requests, waits for in-flight handlers
2. **Drain async pool** — no new tasks can be submitted
3. **Close event bus** — allows pending subscriptions to finish
4. **Flush telemetry** — sends remaining spans to the collector

Infrastructure cleanup (database pool, Redis) happens via `defer` in `main.go` after `Shutdown()` returns.

## Scaffolded Files

Beyond the Go code, `gopernicus init` also generates:

| File | Purpose |
|---|---|
| `.env.example` | Configuration reference with all env vars and defaults |
| `Makefile` | Development targets: `dev`, `run`, `build`, `test`, `dev-up`, `dev-down`, `dev-psql` |
| `docker-compose.yml` | Local dev infrastructure (PostgreSQL, Redis if configured) |
| `Dockerfile` | Multi-stage production build (Go build → Alpine runtime) |
| `.air.toml` | Hot-reload configuration for development |
