---
sidebar_position: 1
title: Overview
---

# Core

Core is the domain logic layer. It contains the data contracts, business rules, and authentication/authorization systems that define what an application does — without any knowledge of how it is exposed (HTTP, CLI, etc.) or which specific backends store the data.

Core imports from Infrastructure and SDK. It never imports Bridge or App.

## Package Structure

```
core/
├── auth/                    # Authentication, authorization, invitations
│   ├── authentication/      # Login, sessions, passwords, OAuth, API keys
│   │   └── satisfiers/      # Adapters bridging generated repos to auth interfaces
│   ├── authorization/       # ReBAC permission checking, resource lookup
│   │   └── satisfiers/      # Adapters bridging generated repos to authz interfaces
│   └── invitations/         # Invitation workflow (create, accept, decline)
├── cases/                   # Hand-written business logic (user-defined)
├── repositories/            # Generated data access
│   ├── auth/                # Auth domain (users, sessions, apikeys, etc.)
│   ├── rebac/               # ReBAC domain (groups, invitations, relationships)
│   ├── events/              # Events domain (transactional outbox)
│   └── tenancy/             # Tenancy domain (tenants)
```

## What Lives Here

**Repositories** are the data access layer. Each database entity gets a repository with a `Storer` interface, entity types, error sentinels, and a pgx store implementation. The generator bootstraps the common parts; you extend by adding custom methods to the `Storer` interface and implementing them in the pgx store. Repositories are the foundation of core — everything else imports them.

**Cases** are hand-written business logic that orchestrates multiple repositories, emits domain events, and enforces business rules that go beyond single-entity CRUD. User-defined cases live under `cases/`. If an operation spans multiple entities, requires a multi-step workflow, or encodes business rules, it belongs in a case.

**Auth** ships with Gopernicus and provides authentication, authorization, and invitations out of the box:

## Auth

| Package | Purpose |
|---|---|
| [Authentication](./auth/authentication.md) | Login, registration, sessions, password management, OAuth, API keys, security events |
| [Authorization](./auth/authorization.md) | Relationship-based access control (ReBAC) — permission checking, resource lookup, membership management |
| [Invitations](./auth/invitations.md) | Resource invitation workflow — create, accept, decline, cancel, auto-accept on registration |

These packages are deliberately decoupled from the repository layer. Authentication defines its own repository interfaces (`UserRepository`, `SessionRepository`, `PasswordRepository`, etc.) and never imports generated repository packages directly. Authorization defines its own `Storer` interface, designed so alternative backends like OpenFGA or SpiceDB can satisfy it without touching the repository layer.

This decoupling is bridged by **satisfiers** — adapter types in `auth/authentication/satisfiers/` and `auth/authorization/satisfiers/` that translate between the auth interfaces and the generated repository types. The app layer wires them together.

## Error Handling

Every repository package defines domain-specific error sentinels that wrap base errors from `sdk/errs`:

```go
var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)
var ErrInvitationAlreadyExists = fmt.Errorf("invitation: %w", errs.ErrAlreadyExists)
```

This lets callers check errors at either level — `errors.Is(err, users.ErrUserNotFound)` for domain-specific handling, or `errors.Is(err, errs.ErrNotFound)` for generic handling. The bridge layer uses the generic level to map errors to HTTP status codes.

The bridge layer uses the generic level to map domain errors to HTTP status codes automatically.

## Domain Events

Repositories and cases emit domain events via an `events.Bus` (defined in Infrastructure). Events signal state changes — a user was created, a verification code was requested, an invitation was sent — and subscribers handle side effects like sending emails, updating caches, or triggering downstream workflows.

Events are fire-and-forget from the emitter's perspective. For events that must not be lost, the `@event: ... outbox` annotation writes the event atomically with the business data in the same database transaction. A poller reads committed outbox rows and publishes them to the bus. The repository creates the typed event (domain concern); the store persists it atomically (infrastructure concern).

See [Infrastructure Events](../infrastructure/events.md) for the bus implementations and transactional outbox pattern.

## Packages

| Package | Purpose |
|---|---|
| [Authentication](./auth/authentication.md) | Login, sessions, passwords, OAuth, API keys, security events |
| [Authorization](./auth/authorization.md) | ReBAC permission checking, resource lookup, membership |
| [Invitations](./auth/invitations.md) | Resource invitation workflow |
| [Cases](./cases.md) | Hand-written business logic |
| [Repositories](./repositories.md) | Data access layer |
