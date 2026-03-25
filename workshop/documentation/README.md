# Gopernicus Documentation

> A Go framework for building production-ready APIs with code generation, hexagonal architecture, and built-in auth.

## Quick Start

| I want to... | Go here |
|---|---|
| Understand the architecture | [Architecture Overview](architecture/overview.md) |
| Set up a new project | [CLI: Init](cli/init.md) |
| Add a new database entity | [Guide: Adding an Entity](guides/adding-new-entity.md) |
| Add business logic beyond CRUD | [Guide: Adding a Use Case](guides/adding-use-case.md) |
| Understand the auth system | [Auth: Overview](auth/overview.md) |
| Learn the code generator | [Generators: Overview](generators/overview.md) |

## Architecture

Core design decisions and patterns.

- [Overview](architecture/overview.md) — Hexagonal architecture, layers, dependency flow
- [Design Philosophy](architecture/design-philosophy.md) — Principles and trade-offs
- [Repositories](architecture/repositories.md) — Generated data access pattern
- [Cases](architecture/cases.md) — Use case pattern for business logic
- [Caching](architecture/caching.md) — Cache layer and invalidation
- [Events](architecture/events.md) — Domain events and the outbox pattern
- [Error Handling](architecture/error-handling.md) — Error types, wrapping, HTTP mapping
- [Testing](architecture/testing.md) — Integration tests, fixtures, E2E
- [Extending Generated Code](architecture/extending-generated-code.md) — Customizing without editing generated files

## Layers

Each layer of the hexagonal architecture explained.

- [SDK](layers/sdk.md) — Shared utilities (web, fop, errs, validation, logger)
- [Core](layers/core.md) — Domain logic: repositories, cases, auth
- [Infrastructure](layers/infrastructure.md) — External adapters: database, cache, events, storage
- [Bridge](layers/bridge.md) — HTTP transport: generated bridges, middleware, case bridges
- [App](layers/app.md) — Composition root: server wiring, configuration

## Auth

Authentication, authorization, and the ReBAC system.

- [Overview](auth/overview.md) — Auth architecture and how the pieces fit together
- [Authentication](auth/authentication.md) — Registration, login, sessions, tokens, OAuth
- [Authorization (ReBAC)](auth/authorization.md) — Relationship-based access control
- [Schema Definition](auth/schema-definition.md) — Defining permissions with annotations
- [Middleware](auth/middleware.md) — Authenticate, Authorize, SubjectInfo middleware

## Code Generation

The `gopernicus generate` pipeline.

- [Overview](generators/overview.md) — What gets generated, what doesn't
- [Query Annotations](generators/query-annotations.md) — @func, @http:json, @auth, @filter, etc.
- [YAML Configuration](generators/yaml-configuration.md) — gopernicus.yml reference
- [Generated File Map](generators/generated-file-map.md) — Which files are generated vs bootstrap

## CLI Reference

All `gopernicus` commands.

- [Init](cli/init.md) — Bootstrap a new project
- [Generate](cli/generate.md) — Run code generation
- [New](cli/new.md) — Scaffold repos, cases, adapters
- [DB](cli/db.md) — Database operations (reflect, migrate)
- [Boot](cli/boot.md) — Batch scaffolding
- [Doctor](cli/doctor.md) — Project health checks

## Guides

Step-by-step walkthroughs for common tasks.

- [Adding a New Entity](guides/adding-new-entity.md) — From table to working API endpoint
- [Adding a Use Case](guides/adding-use-case.md) — Business logic beyond CRUD
- [Adding Auth to an Entity](guides/adding-auth-to-entity.md) — Permissions and authorization
- [Adding an Infrastructure Adapter](guides/adding-adapter.md) — Integrating external services

## SDK Reference

Utility packages available to all layers.

- [Web Framework](sdk/web.md) — RouteGroup, handlers, middleware, OpenAPI, SSE
- [Filter/Order/Pagination](sdk/fop.md) — Structured query parameters
- [Errors](sdk/errs.md) — Error sentinels and domain error wrapping
- [Validation](sdk/validation.md) — Input validation framework
- [Logger](sdk/logger.md) — Structured logging with slog

## Infrastructure Reference

Adapters for external systems.

- [Database (PostgreSQL)](infrastructure/database.md) — pgxdb, connection pooling, Querier interface
- [Database Transactions](database/transactions.md) — Transaction patterns and tiers
- [Cache](infrastructure/cache.md) — Memory and Redis cache backends
- [Events](infrastructure/events.md) — Event bus: memory, Redis Streams, outbox
- [Storage](infrastructure/storage.md) — File storage: disk, GCS, S3
- [Email](infrastructure/email.md) — Email delivery and templates
- [Rate Limiting](infrastructure/rate-limiting.md) — Rate limiter configuration
- [Cryptography](infrastructure/cryptography.md) — JWT, bcrypt, AES-GCM, ID generation
- [OAuth](infrastructure/oauth.md) — OAuth 2.0 client with PKCE
- [Tracing](infrastructure/tracing.md) — OpenTelemetry integration
