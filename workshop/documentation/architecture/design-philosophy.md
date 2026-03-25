# Design Philosophy

Core principles that guide decisions across the Gopernicus framework.

## Convention Over Configuration

Gopernicus makes opinionated choices so you spend time writing business logic instead of
glue code. When you run `gopernicus new repo auth/users`, you get a fully wired
repository package with CRUD operations, a pgx store, integration tests, fixtures,
ordering defaults, and cache wrappers -- all derived from a single `queries.sql` file
and a reflected database schema. The conventions include:

- Directory names map to table names (e.g. `invitations/` matches the `invitations` table).
- Domain grouping follows directory structure (`core/repositories/rebac/invitations/`).
- Generated composites wire all entities in a domain together automatically.
- Data annotations in `queries.sql` (`@func`, `@filter`, `@cache`, `@max`) drive
  repository generation. Protocol and auth annotations live in `bridge.yml`.
- Test fixtures, integration tests, and E2E tests are generated from the same source
  of truth.

There is almost nothing to configure. The `gopernicus.yml` manifest declares databases
and features; `bridge.yml` files declare routes and middleware; everything else is
derived from your SQL and schema.

## Generated CRUD, Hand-Written Business Logic

The framework draws a hard line between what the generator owns and what you own.

**Generated (always regenerated, never edit directly):**

- `generated.go` -- entity structs (`Entity`, `CreateEntity`, `UpdateEntity`), filter
  structs, error sentinels, ordering fields (`OrderByFields`), and Repository methods
  (List, Get, Create, etc.) on `*Repository`.
- `generated_cache.go` -- cache-aware wrappers for generated store methods.
- `generated_composite.go` -- domain-level wiring that connects all repositories (or
  bridges) with `Repositories` (or `Bridges`) structs directly.
- `generated_authschema.go` -- authorization schema derived from `bridge.yml` auth
  annotations. Lives in the bridge composite directory.
- `invitationspgx/generated.go` -- pgx store implementation.
- `invitationspgx/generated_test.go` -- integration tests for generated store methods.
- `core/testing/fixtures/generated.go` -- cross-domain test fixture factories.

**Hand-written (created once, never overwritten):**

- `repository.go` -- your `Repository` struct with fields (store, generateID, bus), the
  `Storer` interface with `// gopernicus:start` / `// gopernicus:end` markers, custom
  options, and any methods that override or extend generated behavior.
- `fop.go` -- ordering, default sort direction, and default page size. Customize by
  changing values; the generator provides sensible defaults.
- `cache.go` -- cache bootstrap for custom cache methods.
- `store.go` -- the pgx store bootstrap for custom SQL queries.
- `store_test.go` -- test bootstrap for custom integration tests.
- `bridge.yml` -- route definitions, ordered middleware arrays, and auth schema.
- `bridge.go` -- flat Bridge struct with all fields, constructor.
- `routes.go` -- `AddHttpRoutes()` calling `addGeneratedRoutes()` plus custom routes.

The Cases (`core/auth/invitations/case.go`) and case Bridges
(`bridge/auth/invitations/bridge.go`) are always hand-written. They contain your
business rules and HTTP wiring respectively.

## Hexagonal Architecture

Dependencies point inward. The core never imports the bridge or infrastructure layers.

```
App (main, server wiring)
  -> Bridge (HTTP handlers, middleware)
    -> Core (cases, repositories, domain types)
      -> SDK (fop, errs, validation, web)
  -> Infrastructure (database, cache, events, email)
```

The key rule: **accept interfaces, return structs**.

- A `Case` accepts a `Storer` interface and an `events.Bus` interface. It returns
  concrete result types.
- A `Repository` accepts a `Storer` interface. The pgx implementation satisfies it.
- A `Bridge` accepts a concrete `*Repository`. It returns HTTP responses.

This means the core layer defines the contracts (interfaces) that infrastructure must
satisfy. Swapping postgres for a test double means providing a different `Storer` --
no changes to core code.

## Go Idioms

Gopernicus follows standard library patterns. No reflection magic, no struct tag
parsing at runtime, no global registries.

- **net/http** -- bridges produce `http.Handler` values. No proprietary router
  abstractions leak into your code.
- **log/slog** -- structured logging via the standard `*slog.Logger`, passed
  explicitly.
- **context.Context** -- threaded through every method. No request-scoped globals.
- **Explicit dependency injection** -- constructors like `NewRepository(store, opts...)`
  take their dependencies as arguments. No DI containers, no service locators.
- **Functional options** -- `Option func(*Case)` and `BridgeOption func(*Bridge)`
  for optional configuration, following the established Go pattern.
- **Error wrapping** -- sentinel errors like `ErrInvitationNotFound` wrap SDK base
  errors using `fmt.Errorf("invitation: %w", errs.ErrNotFound)`.

## The Workshop Concept

The `workshop/` directory is the development workbench. It holds everything that
supports building and running the application but is not part of the deployed binary:

- `workshop/migrations/` -- SQL migration files and reflected schema JSON. These are
  the source of truth that the generator reads from.
- `workshop/dev/` -- local development tooling (docker-compose files for databases,
  caches, and other services).
- `workshop/documentation/` -- this documentation. Architecture guides, CLI references,
  and how-to guides live here, close to the code they describe.

The workshop directory is explicitly separated from application code so that build
tools, editors, and CI can treat it differently (e.g. exclude from production images).

## Bootstrap Files

Bootstrap files are created once by `gopernicus generate` and never overwritten on
subsequent runs. They are your extension points.

Every generated package includes bootstrap files alongside its generated files:

| Bootstrap file    | Purpose                                               |
|-------------------|-------------------------------------------------------|
| `repository.go`   | Storer interface (with markers), Repository struct, NewRepository |
| `fop.go`          | Ordering defaults, page size                          |
| `cache.go`        | Custom cache methods                                  |
| `store.go`        | Custom SQL queries in the pgx store                   |
| `store_test.go`   | Custom integration test helpers                       |
| `bridge.yml`      | Routes, ordered middleware arrays, auth schema         |
| `bridge.go`       | Flat Bridge struct with all fields, constructor        |
| `routes.go`       | `AddHttpRoutes()` calling generated + custom routes   |
| `http.go`         | Custom HTTP handlers                                  |
| `fixtures.go`     | Custom fixture factory logic                          |
| `setup_test.go`   | E2E test setup (created once per test suite)          |

The pattern is consistent: the `Storer` interface in `repository.go` uses
`// gopernicus:start` / `// gopernicus:end` markers for generated methods, with
custom methods placed above the start marker. The `Repository` struct has its
fields directly (no embedding). The `Bridge` struct has all fields directly.
You extend by adding methods or fields. You override by defining a method with
the same signature.

Use `gopernicus generate --force-bootstrap` to regenerate bootstrap files (this
overwrites your customizations, so use with care).

## Generated Files

Generated files are prefixed with `generated_` or named `generated.go`. They carry
the header:

```go
// Code generated by gopernicus. DO NOT EDIT.
// This file is regenerated every time 'gopernicus generate' runs.
```

Never edit these files directly. If the generated output is wrong:

1. Fix the `queries.sql` annotations, the `bridge.yml` configuration, or the
   reflected schema.
2. If the generator itself has a bug, flag it as an issue in gopernicus.
3. Re-run `gopernicus generate`.

Your customizations belong in the bootstrap files or in entirely hand-written
packages (cases, bridges, adapters).

## File Organization

Within any Go file in the project, follow this ordering convention:

1. **Package-level variables and constants** at the top of the file.
2. **Interfaces** immediately after.
3. **Struct definitions**, constructors, and methods below.

This makes scanning a file predictable. You always know where to find the contracts
a package exposes.

## Naming Conventions

Naming is strict to keep the codebase searchable and unambiguous:

- Never abbreviate `authentication` as `authn`. Always use `authentication`,
  `authenticator`, or `Authenticator`.
- Never abbreviate `authorization` as `authz`. Always use `authorization`,
  `authorizer`, or `Authorizer`.
- Package names follow Go convention: short, lowercase, no underscores
  (`invitations`, `cryptids`, `httpmid`).
- Generated types use their final names directly (`Invitation`, `Storer`,
  `Repository`, `Bridge`, `OrderByFields`). There is no `Generated` prefix on
  types. The only generated private function in bridge packages is
  `addGeneratedRoutes()`.

## Related

- [Architecture Overview](overview.md)
- [Cases](cases.md)
- [Extending Generated Code](extending-generated-code.md)
- [Error Handling](error-handling.md)
