---
sidebar_position: 2
title: Design Philosophy
---

# Design Philosophy

Gopernicus is a Go framework for building production APIs. It generates the repetitive parts of a web application — CRUD repositories, HTTP handlers, request validation, cursor-based pagination, authorization wiring — while leaving business logic to hand-written code.

These are the principles that guide how the framework is organized and how it expects applications to be structured. Gopernicus has strong opinions about its own internals, but applications built on it are free to adopt or adapt these patterns as they see fit.

## Standing on the Shoulders of Gophers

Gopernicus is ultimately a collection of industry patterns stitched together to get up and running quickly. Very little here is novel — I just wanted these pieces in one place so I could stop spending my time glueing stuff together.

The code organization principles — the layer hierarchy, package-oriented design, explicit dependency injection, file-level ordering — came from working through [Bill Kennedy's](https://www.ardanlabs.com/) [Ultimate Go Course](https://www.ardanlabs.com/training/ultimate-go/). If you are getting started with Go, I'd recommend that course before anything else. It builds the mental model that makes patterns like these feel natural rather than imposed.

The "accept interfaces, return structs" rule and other design heuristics draw from [Rob Pike's Go Proverbs](https://www.youtube.com/watch?v=PAAkCSZUG1c) and the broader Go community's collective wisdom. If you haven't watched that talk, it is well worth the time.

The authorization system is inspired by [Google Zanzibar](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/) — the relationship-based access control (ReBAC) model that powers Google Drive, YouTube, and other Google services. The code generation approach for repositories owes a debt to [sqlc](https://sqlc.dev/), which proved that SQL can be the source of truth for type-safe Go code. I plan to integrate sqlc directly in the future.

I am not affiliated any of the projects mentioned above. These are personal recommendations and I'm sure I owe many more — they were instrumental in getting Gopernicus off the ground.

## Layer Hierarchy

Code is organized into layers, each in its own top-level directory. The directory names are chosen so that **alphabetical order defines the import rule**.

| Layer          | Directory         | Responsibility                                                            |
| -------------- | ----------------- | ------------------------------------------------------------------------- |
| Aesthetics     | `aesthetics/`     | Frontend UI — React web, React Native, or Go templates                    |
| App            | `app/`            | Composition root — wires dependencies, reads config, starts the server    |
| Bridge         | `bridge/`         | HTTP transport — handlers, middleware, request/response models, routes    |
| Core           | `core/`           | Domain logic — repositories, cases, authentication, authorization         |
| Infrastructure | `infrastructure/` | External adapters — database, cache, events, email, storage, cryptography |
| SDK            | `sdk/`            | Shared utilities — errors, validation, logging, pagination, web helpers   |
| Telemetry      | `telemetry/`      | Observability — tracing exporters, metrics                                |
| Workshop       | `workshop/`       | Development tooling — migrations, Docker, test fixtures, documentation    |

**The import rule:** a package can import any package whose directory sorts after it alphabetically. `bridge/` can import `core/`, `infrastructure/`, and `sdk/`. `core/` can import `infrastructure/` and `sdk/`. `sdk/` imports nothing from the framework.

This is a best-effort convention, not an enforced constraint. The goal is that when someone sees an import, they can reason about whether it belongs without consulting a diagram. If you are working in `core/` and see an import from `bridge/`, you know it is wrong — `bridge` sorts before `core`.

Two packages sit outside the strict hierarchy:

- **`app/`** is the composition root. It imports everything to wire the dependency graph — this is the one place where all layers converge.
- **`aesthetics/`** depends on the frontend strategy — an embedded SPA in the app server, a separate application, or server-rendered Go templates. Its import relationships vary and need to be flexible.

### Import Flow

```
aesthetics ─┐
  ↑         |
  |         ├──→ app
  |         |
bridge ─────┘
  ↑
  │
core
  ↑
  │
infrastructure
  ↑
  │
sdk
  ↑
  │
telemetry
```

Reading bottom-to-top: SDK provides the foundation. Infrastructure builds on SDK to provide concrete adapters. Core imports both to implement domain logic. Bridge imports Core, Infrastructure, and SDK to expose that logic over HTTP. App wires them all together — it imports every layer to construct the dependency graph, connect infrastructure implementations to core interfaces, and start the server.

Each layer can import any layer below it — not just the one directly beneath. App sits at the top and imports all layers to wire the dependency graph.

### Within a Layer

The same alphabetical convention applies to the top-level packages inside each layer, as a best-effort guide. Subdirectories within a package (e.g., `core/auth/authentication/satisfiers/`) are not bound by the rule — only the main packages matter.

**Within `core/`:**

```

core/
├── auth/ # Authentication, authorization, invitations (framework-provided)
├── cases/ # Hand-written business logic (user-defined)
└── repositories/ # Data access layer

```

The domain packages follow the alphabetical convention:

- `auth/` (a) can import `cases/`, `repositories/`
- `cases/` (c) can import `repositories/`
- `repositories/` (r) is the foundation — imports nothing within core

Satisfiers live within the package that needs them: `auth/authentication/satisfiers/` and `auth/authorization/satisfiers/`. They are implementation details inside their parent packages, not top-level core packages.

This works because authentication and authorization are deliberately decoupled from repositories. The `Authenticator` defines its own repository interfaces (`UserRepository`, `SessionRepository`, etc.) and never imports generated repository packages directly. The `Authorizer` defines its own `Storer` interface — designed so that alternative backends like OpenFGA or SpiceDB can satisfy it without touching the repository layer. Satisfiers in sub-packages (e.g., `core/auth/authentication/satisfiers/`) bridge auth interfaces to generated repositories, but these are implementation details that don't affect the top-level import graph.

Cases don't need to import auth. Authorization checks happen at the bridge layer via middleware — by the time a case method is called, the request has already been authorized. If a case needs authorization-aware logic, it follows Go convention: define a narrow interface in the case package and let the caller inject an implementation.

**Within `bridge/`:**

```

bridge/
├── auth/ # Framework-provided HTTP auth handlers
├── cases/ # User-defined case HTTP handlers
├── repositories/ # Generated CRUD HTTP handlers
└── transit/ # HTTP middleware, authorization-aware pagination, error rendering

```

The same structure applies. Domain packages (`auth/`, `cases/`, `repositories/`) handle HTTP for specific concerns. `transit/` houses the shared HTTP utilities — middleware composition (`httpmid`), authorization-aware pagination helpers (`fop`), and error rendering — that all domain packages import.

## Where Authorization Lives

Core is ignorant of authorization. Repositories and cases do not check permissions — they trust that the caller has already verified access. This is deliberate: authorization decisions depend on HTTP context (who is the authenticated user, what resource are they targeting), and that context belongs in the bridge layer.

The bridge gates access via middleware before the request reaches core:

```
HTTP Request
    → [bridge/transit/httpmid] Authenticate → extract user identity
    → [bridge/transit/httpmid] Authorize   → check permission against resource
    → [bridge] Handler                     → call core (already authorized)
    → [core] Repository or Case            → execute business logic
```

This means core packages never import `authorization`. If a core case needs authorization-aware behavior (e.g., filtering a list by what the user can see), it accepts a narrow interface or pre-filtered input from the bridge — it does not call the authorizer directly.

The authorization _system_ lives in core (`core/auth/authorization/`), but the authorization _decisions_ happen in bridge. Core defines the rules; bridge enforces them.

## Accept Interfaces, Return Structs

This is the architectural rule that makes the layer boundaries work. Inspired by Go proverbs and hexagonal architecture (ports and adapters), every package follows the same pattern:

- A `Repository` accepts a `Storer` interface. The pgx implementation satisfies it.
- A `Case` accepts repository interfaces and an `events.Bus`. It returns concrete result types.
- A `Bridge` accepts a concrete `*Repository` or `*Case`. It returns HTTP responses.

The interfaces define the contracts between layers. Swapping PostgreSQL for a test double means providing a different `Storer` — no changes to domain code.

```go
// Core defines the contract
type Storer interface {
    Get(ctx context.Context, id string) (Widget, error)
    Create(ctx context.Context, input CreateWidget) (Widget, error)
}

// Infrastructure satisfies it
type Store struct { querier pgxdb.Querier }
func (s *Store) Get(ctx context.Context, id string) (Widget, error) { ... }

// Core uses it without knowing the implementation
type Repository struct { store Storer }
```

## Convention Over Configuration

Gopernicus makes opinionated choices so you spend time writing business logic instead of glue code. When you run `gopernicus new repo auth/users`, you get a fully wired repository package with CRUD operations, a pgx store, integration tests, fixtures, ordering defaults, and cache wrappers — all derived from a single `queries.sql` file and a reflected database schema.

- Directory names map to table names (`invitations/` matches the `invitations` table).
- Domain grouping follows directory structure (`core/repositories/rebac/invitations/`).
- Generated composites wire all entities in a domain together automatically.
- Data annotations in `queries.sql` (`@func`, `@filter`, `@cache`, `@max`) drive repository generation. Protocol and auth annotations live in `bridge.yml`.
- Test fixtures, integration tests, and E2E tests are generated from the same source of truth.

There is almost nothing to configure. The `gopernicus.yml` manifest declares databases and features. `bridge.yml` files declare routes and middleware. Everything else is derived from your SQL and schema.

## Generated CRUD, Hand-Written Business Logic

The framework draws a hard line between what the generator owns and what you own.

**Generated (always regenerated, never edit directly):**

- `generated.go` — entity structs, input/update/filter structs, error sentinels, ordering fields, and Repository methods.
- `generated_cache.go` — cache-aware wrappers for generated store methods.
- `generated_composite.go` — domain-level wiring that connects all repositories (or bridges).
- `generated_authschema.go` — authorization schema derived from `bridge.yml` auth annotations.
- `*pgx/generated.go` — pgx store implementation.
- `*pgx/generated_test.go` — integration tests for generated store methods.
- `workshop/testing/fixtures/generated.go` — cross-domain test fixture factories.

Generated files carry the header `// Code generated by gopernicus. DO NOT EDIT.` Never edit them directly. If the generated output is wrong, fix the `queries.sql` annotations, the `bridge.yml` configuration, or the reflected schema — then regenerate.

**Hand-written (created once by the generator, never overwritten):**

- `repository.go` — `Repository` struct, `Storer` interface with `// gopernicus:start` / `// gopernicus:end` markers, constructor.
- `fop.go` — ordering defaults, page size.
- `cache.go` / `store.go` / `store_test.go` — custom cache, store, and test logic.
- `bridge.yml` — route definitions, ordered middleware arrays, auth schema.
- `bridge.go` / `routes.go` / `http.go` — bridge struct, routes, custom handlers.

These are called **bootstrap files**. They are your extension points. Add custom methods above the `// gopernicus:start` marker in the `Storer` interface, implement them in the pgx store, and the generator leaves your code alone on subsequent runs.

Cases and case bridges are always hand-written. They contain your business rules and HTTP wiring respectively.

Use `gopernicus generate --force-bootstrap` to regenerate bootstrap files (this overwrites your customizations, so use with care).

## The Workshop

The `workshop/` directory holds everything that supports building and running the application but is not part of the deployed binary:

- `workshop/migrations/` — SQL migration files and reflected schema JSON. These are the source of truth that the generator reads from.
- `workshop/dev/` — local development tooling (docker-compose files for databases, caches, and other services).
- `workshop/testing/` — test fixtures, setup helpers, and shared test infrastructure. Individual test files still live alongside the code they test — the workshop holds the cross-cutting test support.
- `workshop/documentation/` — architecture guides, CLI references, and how-to guides.

Workshop is explicitly separated from application code so that build tools, editors, and CI can treat it differently (e.g., exclude from production images). It sits at the end of the alphabet intentionally — it depends on application code for fixture generation, but application code never imports workshop.

## Go Standards

Gopernicus follows standard library patterns. No reflection magic, no struct tag parsing at runtime, no global registries.

- **net/http** — bridges produce `http.Handler` values. No proprietary router abstractions.
- **log/slog** — structured logging via the standard `*slog.Logger`, passed explicitly.
- **context.Context** — threaded through every method. No request-scoped globals.
- **Explicit dependency injection** — constructors take their dependencies as arguments. No DI containers, no service locators.
- **Functional options** — `Option func(*Case)` for optional configuration.
- **Error wrapping** — sentinel errors wrap SDK base errors using `fmt.Errorf`.

## Code Organization

Within any Go file, follow this ordering convention:

1. **Package-level variables and constants** at the top.
2. **Interfaces** immediately after.
3. **Struct definitions**, constructors, and methods below.

This makes scanning a file predictable. You always know where to find the contracts a package exposes.

### Naming Conventions

- Never abbreviate `authentication` as `authn`. Always use `authentication`, `authenticator`, or `Authenticator`.
- Never abbreviate `authorization` as `authz`. Always use `authorization`, `authorizer`, or `Authorizer`.
- Package names follow Go convention: short, lowercase, no underscores.
- Generated types use their final names directly (`Invitation`, `Storer`, `Repository`, `Bridge`). There is no `Generated` prefix.

## Request Lifecycle

```
HTTP Request
    │
    ▼
[sdk/web] WebHandler.ServeHTTP
    │
    ▼
[bridge/transit/httpmid] Global middleware (logging, panic recovery, telemetry)
    │
    ▼
[bridge/transit/httpmid] Route middleware (from bridge.yml: authenticate, rate_limit, authorize)
    │
    ▼
[bridge] HTTP handler (generated or hand-written)
    │── Parses request, validates input, converts to domain types
    │
    ▼
[core] Repository or Case method
    │── Business logic, ID generation, event emission
    │
    ▼
[infrastructure] Store (e.g., userspgx.Store)
    │── SQL execution via pgxdb.Querier
    │
    ▼
[infrastructure] PostgreSQL / Redis / etc.
```

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

Running `gopernicus generate` reads this manifest, reflects your database schema, and produces all generated files across Core and Bridge layers.
