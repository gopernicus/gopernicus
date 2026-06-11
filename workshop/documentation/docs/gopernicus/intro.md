---
sidebar_position: 1
title: Intro
---

# Getting Started with Gopernicus

Gopernicus is a Go framework for building production-ready APIs with code generation, hexagonal architecture, and built-in auth.

## What is Gopernicus?

Gopernicus generates the structural boilerplate of your API from two source-of-truth files:

- **`queries.sql`** — Annotated SQL queries that describe your data access layer
- **`bridge.yml`** — HTTP configuration that describes your API surface

From these, the generator produces repositories, bridges, fixtures, E2E tests, and more — while leaving you in control of your business logic via [bootstrap files](./design-philosophy.md#generated-crud-hand-written-business-logic).

## Quick Start

| I want to...                   | Go here                                                   |
| ------------------------------ | --------------------------------------------------------- |
| Set up a new project           | [CLI: init](../cli/init.md)                               |
| Understand the architecture    | [Design Philosophy](./design-philosophy.md)               |
| Add a new database entity      | [Guide: Adding an Entity](../guides/adding-new-entity.md) |
| Add business logic beyond CRUD | [Guide: Adding a Use Case](../guides/adding-use-case.md)  |

## Core Concepts

### Layer Hierarchy

Gopernicus organizes code into layers where [alphabetical order defines the import rule](./design-philosophy.md#layer-hierarchy):

| Layer              | Responsibility                                                         |
| ------------------ | ---------------------------------------------------------------------- |
| **Aesthetics**     | Frontend UI — React web, React Native, or Go templates                 |
| **App**            | Composition root — server wiring                                       |
| **Bridge**         | HTTP transport — generated handlers, middleware                        |
| **Core**           | Domain logic — repositories, cases, auth                               |
| **Infrastructure** | External adapters — database, cache, events, storage                   |
| **SDK**            | Shared utilities — web, pagination, errors, validation, logging        |
| **Telemetry**      | Observability — tracing exporters, metrics                             |
| **Workshop**       | Development tooling — migrations, Docker, test fixtures, documentation |

Each layer can import packages that sort after it alphabetically. Bridge imports Core. Core imports Infrastructure. Infrastructure imports SDK.

### Code Generation

The `gopernicus generate` command reads your annotated SQL and bridge.yml, then generates:

- Repository interfaces and implementations
- HTTP bridge handlers
- Test fixtures and E2E tests
- Auth scaffolding

### Bootstrap Files

Generated files are never edited directly. Instead, Gopernicus generates [bootstrap files](./design-philosophy.md#generated-crud-hand-written-business-logic) at each layer where you add your custom logic — and these are never overwritten.

## Installation

There is nothing to install. Bootstrap a project straight from the module proxy:

```bash
go run github.com/gopernicus/gopernicus/workshop/gopernicus@latest init myapp
cd myapp
```

Inside the project, every command runs through the version-pinned tool:

```bash
go tool gopernicus generate
go tool gopernicus db migrate
```

The project's `go.mod` carries a `tool` directive pinning the generator to the
same framework version the project links, so generated code and runtime can
never drift.

See [CLI: init](../cli/init.md) for all initialization options (authentication, authorization, tenancy, events).
