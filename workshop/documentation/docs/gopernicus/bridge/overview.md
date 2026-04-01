---
sidebar_position: 1
title: Overview
---

# Bridge

The Bridge layer translates between the outside world and the domain. It parses incoming requests, calls Core (repositories and cases), and formats the response.

Today, "outside world" means HTTP. The layer is structured so that additional protocols (gRPC, GraphQL) can be added alongside the existing HTTP handlers without changing the core wiring.

Bridge imports from Core, Infrastructure, and SDK. It never imports App.

## Package Structure

```
bridge/
├── auth/                    # Case bridges (hand-written, ships with Gopernicus)
│   ├── authentication/      # Login, registration, session, OAuth endpoints
│   └── invitations/         # Invitation workflow endpoints
├── cases/                   # Case bridges (hand-written, user-defined)
├── repositories/            # Generated CRUD bridges
│   ├── authreposbridge/     # Auth domain composite + entity bridges
│   │   ├── usersbridge/
│   │   ├── apikeysbridge/
│   │   └── ...
│   ├── rebacreposbridge/    # ReBAC domain
│   ├── eventsreposbridge/   # Events domain
│   └── tenancyreposbridge/  # Tenancy domain
└── transit/                 # Shared HTTP utilities
    ├── httpmid/             # Middleware (auth, rate limiting, logging, etc.)
    └── fop/                 # Response envelopes, post-filter authorization
```

Core packages often map directly to their bridge counterparts — `core/repositories/auth/users` maps to `bridge/repositories/authreposbridge/usersbridge`, and `core/auth/authentication` maps to `bridge/auth/authentication`. This is a common pattern but not a strict rule; bridge packages are organized however makes sense for the API surface.

## Two Kinds of Bridges

### Generated Repository Bridges

Located in `bridge/repositories/`. The code generator creates one bridge package per database entity. Each package contains a mix of always-regenerated and one-time-scaffolded files:

| File | Regenerated? | Purpose |
|---|---|---|
| `generated.go` | Yes | HTTP handlers, request/response models, `addGeneratedRoutes()`, OpenAPI specs |
| `bridge.go` | No (scaffolded once) | `Bridge` struct, `NewBridge()` constructor |
| `bridge.yml` | No (scaffolded once) | Route definitions, middleware ordering, auth schema |
| `routes.go` | No (scaffolded once) | `AddHttpRoutes()` — calls `addGeneratedRoutes()` plus any custom routes |
| `http.go` | No (scaffolded once) | Custom HTTP handlers (empty by default) |
| `fop.go` | No (scaffolded once) | Filter/order query parameter parsing |

The "scaffolded once" files are created when you first generate an entity and are never overwritten. This is where you customize behavior — add routes in `routes.go`, write custom handlers in `http.go`, tune filters in `fop.go`, or adjust middleware ordering in `bridge.yml`.

See [Repositories](./repositories.md) for the full generated bridge walkthrough.

### Hand-Written Case Bridges

Located in `bridge/auth/` and `bridge/cases/`. These handle complex flows that go beyond CRUD — multi-step authentication, invitation workflows, and anything that orchestrates a Core case rather than a single repository. They follow the same structural patterns (a `Bridge` struct, `NewBridge()`, `AddHttpRoutes()`) but are entirely hand-written.

Gopernicus ships with case bridges for authentication and invitations under `bridge/auth/`. Your application's case bridges live under `bridge/cases/`.

See [Cases](./cases.md), [Authentication](./auth/authentication.md), and [Invitations](./auth/invitations.md) for details.

## Composites

Each domain has a generated **composite** that groups all of its entity bridges. For example, `authreposbridge.Bridges` aggregates every auth entity bridge and exposes three methods:

| Method | Purpose |
|---|---|
| `AddHttpRoutes(group)` | Registers all entity routes under a route group |
| `OpenAPISpec()` | Returns aggregated OpenAPI specs for all entities |
| `AuthSchema()` | Returns the authorization resource schemas for the domain |

The composite constructor takes the domain's `Repositories` struct, shared infrastructure (logger, rate limiter), and auth (authenticator, authorizer), then constructs each entity bridge internally:

```go
bridges := authreposbridge.NewBridges(log, repos, rateLimiter, authenticator, authorizer)
bridges.AddHttpRoutes(apiGroup)
```

The App layer wires composites — you rarely interact with them directly.

## Transit

`bridge/transit/` contains shared utilities that all bridge packages import:

- **`httpmid/`** — HTTP middleware: authentication, authorization, rate limiting, logging, panic recovery, body size limits, tenant extraction, and more. All middleware uses the standard `func(http.Handler) http.Handler` signature. See [Middleware](./middleware.md).

- **`fop/`** — Response envelope types (`PageResponse[T]`, `RecordResponse[T]`) and a post-filter authorization helper for cases where prefiltering is impractical. See [FOP](./fop.md).

## How It All Connects

A typical request flows through the bridge like this:

1. **Route match** — `sdk/web` dispatches to the bridge handler registered in `routes.go`
2. **Middleware chain** — Middleware defined in `bridge.yml` runs in order: authentication, rate limiting, authorization, etc.
3. **Handler** — The handler (generated or custom) parses the request, calls a Core repository or case method, and builds a response
4. **Response** — The handler writes a typed response envelope (`PageResponse`, `RecordResponse`, or a custom shape)

Error mapping happens automatically at the handler level. Core returns domain errors (`users.ErrUserNotFound`), which unwrap to `sdk/errs` sentinels (`errs.ErrNotFound`). Generated handlers map these to HTTP status codes — 404, 409, 422, etc. — via `web.RespondJSONDomainError`.

## Packages

| Package | Purpose |
|---|---|
| **Auth** | |
| [Authentication](./auth/authentication.md) | Login, registration, sessions, OAuth, password management |
| [Invitations](./auth/invitations.md) | Invitation workflow (create, accept, decline, cancel) |
| **Cases** | |
| [Cases](./cases.md) | Hand-written case bridge patterns |
| **Repositories** | |
| [Repositories](./repositories.md) | Generated CRUD bridge walkthrough |
| **Transit** | |
| [Middleware](./middleware.md) | HTTP middleware reference |
| [FOP](./fop.md) | Filter, order, pagination & response envelopes |
