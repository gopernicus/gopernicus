# Architecture Overview

Gopernicus (`github.com/gopernicus/gopernicus`) is a Go framework for building
production APIs. It generates the repetitive parts of a web application --
CRUD repositories, HTTP handlers, request validation, cursor-based pagination,
authorization wiring -- while leaving business logic to hand-written code.

The framework follows a hexagonal (ports and adapters) architecture with five
distinct layers, a strict dependency rule, and a clear split between generated
and hand-written code.

---

## Layer Diagram

```
+-------------------------------------------------------------------+
|                            App (main)                             |
|  Wires everything together. Reads config, starts the HTTP server. |
+-------------------------------------------------------------------+
        |               |                |               |
        v               v                v               v
+-------------------------------------------------------------------+
|                            Bridge                                 |
|  HTTP handlers, middleware, request/response models, route         |
|  registration. Two kinds: generated CRUD bridges and hand-written |
|  case bridges.                                                    |
+-------------------------------------------------------------------+
        |               |                |
        v               v                v
+-------------------------------------------------------------------+
|                             Core                                  |
|  Domain logic: repositories (generated), cases (hand-written),    |
|  authentication, authorization. Defines interfaces (ports) that   |
|  infrastructure must satisfy.                                     |
+-------------------------------------------------------------------+
        |               |                |
        v               v                v
+-------------------------------------------------------------------+
|                        Infrastructure                             |
|  Concrete adapters: pgx stores, Redis cache, bcrypt hashing,     |
|  JWT signing, email sending, OAuth providers, event buses.        |
+-------------------------------------------------------------------+
        |
        v
+-------------------------------------------------------------------+
|                             SDK                                   |
|  Zero-dependency utilities shared by every layer: errors,         |
|  validation, logging, web helpers, pagination, conversions.       |
+-------------------------------------------------------------------+
```

Dependencies point **inward only**. Bridge imports Core. Core imports
Infrastructure (for concrete adapters it wraps) and SDK. Infrastructure
imports SDK. SDK imports nothing from the framework. The App layer sits at
the outer edge and imports everything to wire the dependency graph.

---

## The Five Layers

### SDK (`sdk/`)

Foundational packages with no framework coupling:

| Package        | Purpose                                           |
|----------------|---------------------------------------------------|
| `sdk/errs`     | Sentinel errors: `ErrNotFound`, `ErrAlreadyExists`|
| `sdk/validation` | Field validation, custom rules, error formatting|
| `sdk/web`      | HTTP server, routing, middleware, JSON encode/decode, OpenAPI spec generation |
| `sdk/fop`      | Cursor-based pagination, ordering, filter types   |
| `sdk/logger`   | Structured logging with tracing integration       |
| `sdk/environment` | Env var parsing via struct tags               |
| `sdk/conversion` | Type conversions, pointer helpers, date parsing |
| `sdk/async`    | Worker pool for concurrent operations             |
| `sdk/notify`   | Notification abstractions                         |

`sdk/web` provides the HTTP server built on the standard library `http.ServeMux`.
Routes are registered through `WebHandler` and `RouteGroup`, which support
global and per-route middleware:

```go
handler := web.NewWebHandler(web.WithLogging(log), web.WithCORS(origins))
group := handler.Group("/v1")
group.GET("/users/:user_id", bridge.httpGet,
    httpmid.Authenticate(authenticator, log, jsonErrors),
    httpmid.RateLimit(rateLimiter, log),
)
```

### Core (`core/`)

Domain logic and data contracts. Core contains two categories of code:

**Repositories** (`core/repositories/`) -- generated CRUD data access. Each
entity (e.g., `apikeys`, `users`, `sessions`) gets:

- `generated.go` -- entity struct (`Entity`), input/update/filter structs,
  `Storer` interface (with `// gopernicus:start` / `// gopernicus:end` markers
  in repository.go), `Repository` struct with pagination logic. Regenerated on
  every `gopernicus generate` run. Types use final names directly (e.g.,
  `APIKey`, `CreateAPIKey`, `Storer`, `Repository`) -- no `Generated` prefix.
- `repository.go` -- bootstrapped file created once. Contains the `Storer`
  interface with generated methods between `// gopernicus:start` /
  `// gopernicus:end` markers (custom methods go above the markers), the
  `Repository` struct with fields (store, generateID, bus), and
  `NewRepository` constructor.
- `fop.go` -- hand-written ordering defaults and field maps.
- `queries.sql` -- SQL source for code generation (data annotations only:
  `@func`, `@database`, `@filter`, `@search`, `@order`, `@max`, `@fields`,
  `@cache`, `@event`, `@scan`, `@returns`, `@type`).
- `<entity>pgx/` -- generated pgx store implementation.
- `cache.go` / `generated_cache.go` -- optional caching layer.

The `Storer` interface is the port that database stores satisfy:

```go
type Storer interface {
    // custom methods above markers

    // gopernicus:start
    List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]APIKey, error)
    Get(ctx context.Context, apiKeyID string) (APIKey, error)
    Create(ctx context.Context, input CreateAPIKey) (APIKey, error)
    Update(ctx context.Context, apiKeyID string, input UpdateAPIKey) (APIKey, error)
    SoftDelete(ctx context.Context, apiKeyID string) error
    Delete(ctx context.Context, apiKeyID string) error
    // ... custom queries from queries.sql
    // gopernicus:end
}
```

**Cases** (`core/auth/`) -- hand-written business logic. Cases orchestrate
multiple repositories, emit domain events, and enforce business rules. For
example, `core/auth/invitations/case.go` defines an invitation workflow that
coordinates the invitations repository, the authorizer, a hasher, and the
event bus:

```go
type Case struct {
    invitations *invitationsrepo.Repository
    authorizer  *authorization.Authorizer
    hasher      *cryptids.SHA256Hasher
    bus         events.Bus
    lookupUser  UserLookup
    checkMember MemberCheck
}
```

Cases accept their dependencies as interfaces or concrete structs through
constructor injection with the functional options pattern.

Core also houses the authentication and authorization systems:

- `core/auth/authentication/` -- login, registration, session management,
  password flows, OAuth, API key authentication, security event logging.
  `Authenticator` accepts interfaces like `PasswordHasher`, `JWTSigner`, and
  repository ports (`UserRepository`, `SessionRepository`).
- `core/auth/authorization/` -- ReBAC (relationship-based access control)
  with `Authorizer`, permission checking, and resource lookup.

### Infrastructure (`infrastructure/`)

Concrete adapters that satisfy Core's interfaces:

| Package                    | What it provides                              |
|----------------------------|-----------------------------------------------|
| `database/postgres/pgxdb`  | `Querier` interface, migrations, pgx helpers  |
| `database/kvstore/goredisdb` | Redis connection via go-redis               |
| `cache/`                   | `Cacher` interface + memory, Redis, noop stores |
| `cryptids/`                | ID generation, `Hasher`, `Encrypter`, `JWTSigner` with bcrypt, AES-GCM, golang-jwt adapters |
| `communications/emailer/`  | `Emailer` interface + SendGrid, stdout adapters, HTML templates |
| `events/`                  | `Bus` interface + memory bus, Redis bus, outbox pattern |
| `oauth/`                   | OAuth provider abstraction (OIDC)             |
| `ratelimiter/`             | Rate limiting                                 |
| `storage/`                 | Object storage (S3, GCS)                      |
| `tracing/`                 | OpenTelemetry tracing setup                   |

The `Querier` interface in `pgxdb` is a key design point. It is satisfied by
both `*pgxpool.Pool` and `pgx.Tx`, so generated stores can be used with or
without transactions:

```go
type Querier interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
```

### Bridge (`bridge/`)

Translates between the outside world (HTTP) and the domain (Core). There are
two kinds of bridges:

**Generated CRUD bridges** (`bridge/repositories/`) -- one per entity. Each
entity bridge (e.g., `apikeysbridge`) contains:

- `generated.go` -- HTTP handler methods (`httpList`, `httpGet`, `httpCreate`,
  etc.) on `*Bridge`, request models with `Validate()` and `ToRepo()` methods,
  OpenAPI specs, `addGeneratedRoutes()` (private), and authorization
  relationship wiring. Regenerated on every `gopernicus generate` run.
- `bridge.go` -- bootstrapped file created once. Defines the `Bridge` struct
  with ALL fields directly (repository, log, rateLimiter, authenticator,
  authorizer, jsonErrors), `NewBridge` constructor, and optional dependencies.
  No embedding of a generated type.
- `bridge.yml` -- YAML configuration that declares routes, ordered middleware
  arrays, and auth schema (`auth_relations`, `auth_permissions`). Protocol
  concerns (routes, authentication, authorization, permissions) are configured
  here, not in queries.sql.
- `routes.go` -- bootstrapped file with `AddHttpRoutes()` (public) that calls
  `addGeneratedRoutes()` from generated.go plus any custom routes.
- `http.go` -- hand-written custom HTTP handlers.
- `fop.go` -- filter/order parsing customization.

**Hand-written case bridges** (`bridge/auth/`) -- for complex flows that go
beyond CRUD. For example, `bridge/auth/invitations/` bridges the
`invitations.Inviter` to HTTP with custom routes for create, accept, decline,
cancel, resend, and list-mine. These bridges are entirely hand-written.

### App (user project)

The `main` package in the user's project. It reads configuration, creates
infrastructure (database pools, cache, email clients), constructs Core
objects (repositories, cases, authenticator, authorizer), builds Bridge
structs, registers routes, and starts the HTTP server.

---

## Key Principles

### Accept Interfaces, Return Structs

Every constructor in gopernicus accepts interfaces for its dependencies and
returns a concrete struct. This makes dependencies explicit, testable, and
swappable without type assertion gymnastics.

```go
// Core defines the port as an interface
type Storer interface {
    Get(ctx context.Context, id string) (APIKey, error)
    // ...
}

// Infrastructure returns a concrete struct
func NewStore(log *slog.Logger, db pgxdb.Querier) *Store { ... }

// Core's repository accepts the interface, returns a struct
func NewRepository(store Storer, opts ...Option) *Repository { ... }
```

### Satisfiers Bridge Interface Gaps

When a Core case defines its own repository interface (e.g.,
`authentication.UserRepository`), a "satisfier" adapter in
`core/auth/authentication/satisfiers/` translates between the generated
repository and the case's interface:

```go
type UserSatisfier struct {
    repo userRepo  // accepts the generated repo's interface
}

// Satisfies authentication.UserRepository
func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
    u, err := s.repo.GetByEmail(ctx, email)
    return toAuthUser(u), err
}
```

### Generated vs Hand-Written Split

Files prefixed with `generated_` or marked with `// Code generated by
gopernicus. DO NOT EDIT.` are regenerated on every `gopernicus generate` run.
Files with the comment `// This file is created once by gopernicus and will
NOT be overwritten.` are scaffolded once and then owned by the developer.

| Layer          | Generated                                   | Hand-written                         |
|----------------|---------------------------------------------|--------------------------------------|
| Core repos     | `generated.go`, `generated_cache.go`, pgx `generated.go` | `repository.go`, `fop.go`, pgx `store.go` |
| Core cases     | --                                          | Everything                           |
| Bridge repos   | `generated.go`, `generated_composite.go`    | `bridge.go`, `bridge.yml`, `routes.go`, `http.go`, `fop.go` |
| Bridge cases   | --                                          | Everything                           |

---

## Request Lifecycle

A request flows through the layers in this order:

```
HTTP Request
    |
    v
[sdk/web] WebHandler.ServeHTTP
    |
    v
[bridge/protocol/httpmid] Global middleware (logging, panic recovery, telemetry, trust proxies)
    |
    v
[bridge/protocol/httpmid] Route middleware (ordered array from bridge.yml: authenticate, rate_limit, authorize, etc.)
    |
    v
[bridge] HTTP handler (generated or hand-written)
    |-- Parses request (query params, JSON body)
    |-- Validates input (generated Validate())
    |-- Converts to domain types (ToRepo())
    |
    v
[core] Repository or Case method
    |-- Business logic, ID generation, event emission
    |
    v
[core/repositories] Repository
    |-- Pagination logic (over-fetch, cursor encoding)
    |
    v
[infrastructure] Store (e.g., apikeyspgx.Store)
    |-- SQL execution via pgxdb.Querier
    |
    v
[infrastructure] PostgreSQL / Redis / etc.
```

### Concrete Example: GET /api-keys/:api_key_id

1. `WebHandler` matches the route and applies middleware.
2. `authenticate` middleware (from bridge.yml) validates the session/token and
   sets subject info on the request context.
3. `rate_limit` middleware checks rate limits.
4. `authorize` middleware extracts `api_key_id` from the URL and checks
   "read" permission via the authorizer.
5. `Bridge.httpGet` extracts the path parameter, calls
   `apiKeyRepository.Get(ctx, apiKeyID)`.
6. `Repository.Get` delegates to `store.Get`.
7. `apikeyspgx.Store.Get` executes a SELECT query via `pgxdb.Querier`.
8. The bridge checks per-record authorization, resolves the caller's
   relationship and permissions, and responds with JSON.

### Concrete Example: POST /invitations/{resource_type}/{resource_id}

1. `authenticate` middleware validates the session.
2. The hand-written `httpCreate` handler extracts `resource_type` and
   `resource_id` from the URL, performs dynamic authorization against the
   target resource.
3. `web.DecodeJSON` parses and validates the request body.
4. The handler calls `invitations.Inviter.Create(ctx, input)`.
5. The Inviter looks up the user, checks membership, generates a token, hashes
   it, creates the invitation record via the generated repository, creates
   ReBAC relationships via the authorizer, and emits an `InvitationSentEvent`
   via the event bus.
6. The bridge converts the result to an HTTP response.

---

## Configuration

The project manifest `gopernicus.yml` declares features and database schemas:

```yaml
version: "1"
features:
  authentication: gopernicus
  authorization: gopernicus
  tenancy: gopernicus

databases:
  primary:
    driver: postgres/pgx
    domains:
      auth: [users, sessions, api_keys, ...]
      rebac: [rebac_relationships, groups, invitations, ...]
      tenancy: [tenants]
      events: [event_outbox]
```

Running `gopernicus generate` reads this manifest and produces all generated
files across Core and Bridge layers.

---

## Related

- [Design Philosophy](design-philosophy.md)
- [Repositories](repositories.md)
- [Cases](cases.md)
- [Extending Generated Code](extending-generated-code.md)
- [Error Handling](error-handling.md)
- [Events](events.md)
- [Caching](caching.md)
- [Testing](testing.md)
