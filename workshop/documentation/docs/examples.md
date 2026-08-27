---
title: Worked examples
description: Executable Gopernicus hosts and what each one proves.
---

# Worked examples

Every example is a real host module, not documentation-only pseudocode. Together they demonstrate optional dependencies, explicit cross-pocket wiring, background-runtime ownership, datastore substitution, and the optional GOTH UI surface.

| Example | Run | What it proves |
|---|---|---|
| `examples/minimal` | `go run ./cmd/server` | CMS + optional GOTH on an in-memory store; no external infrastructure |
| `examples/cms` | `make migrate && go run ./cmd/server` from its module | CMS on Turso, disk media, custom theme, optional OpenTelemetry |
| `examples/auth-cms` | `go run ./cmd/server` | authentication + authorization + CMS + events + jobs composed in one host |
| `examples/jobs-minimal` | `go run ./cmd/server` | queue, retry, dead-letter, interval schedule, wake, and graceful drain in memory |
| `examples/goth-showcase` | `go run ./cmd/server` | GOTH catalog and browser behavior without a pocket dependency |

The repository does not currently include a React host module. The [React and TanStack guide](ui/react.md) shows the corresponding API boundary and client code.

`examples/cms`, `examples/minimal`, and `examples/jobs-minimal` are the conforming worked examples: once their host-pocket moves land they are the hosts held to the [Host contract](architecture/host-contract.md) by this repository's own guards, and they are the layouts to copy. `examples/auth-cms` and `examples/goth-showcase` are named, dated exemptions — read them for what they prove, not for how they are arranged.

## Minimal CMS

Start here. `examples/minimal` gives the CMS pocket a host-local memory store and registers a custom Product content type without a migration. It also demonstrates embedded GOTH asset serving.

```bash
cd examples/minimal
go run ./cmd/server
# http://localhost:8081
```

Read [the quickstart](getting-started/quickstart.md) for a guided tour.

## CMS on Turso

`examples/cms` replaces the memory repositories with `pockets/cms/stores/turso`, uses `integrations/datastores/turso` for the connection, and owns the merged migration ledger under `examples/cms/workshop/migrations`.

It also shows:

- `filestorage.Disk` for media;
- console or SMTP email selected by environment;
- `tracing.Noop` versus the OpenTelemetry integration;
- a host theme embedding the bundled CMS/GOTH views;
- a database-aware readiness check.

This example needs Turso credentials. Copy the repository `.env.example`, fill the required values, and apply migrations before boot.

## Auth + CMS composition

`examples/auth-cms` is the most complete composition proof. It wires pocket-to-pocket needs without any pocket importing another pocket:

- authentication middleware gates CMS admin routes;
- authentication's identity resolver supplies principal information;
- authorization evaluates relationships and roles;
- the events gateway consumes the same bus that pockets emit through;
- jobs provides durable delivery work through SDK-owned work protocol interfaces;
- host adapters bridge seams that intentionally remain consumer-declared.

Use this example when you need exact ordering for service construction, middleware, event subscriptions, worker pools, and shutdown.

**It is the authentication conformance harness, not a layout reference** (ratified 2026-08-27). It exists to prove the authentication and authorization surface end to end — OAuth, machine identity, JWT bearer, security-event audit, ReBAC-decoupled invitations, the identity/challenge rail, two delivery modes — with zero infrastructure, and it is the most heavily tested host in the repository.

Do not read it for layout. Its composition root carries provider behavior that the [Host contract](architecture/host-contract.md) places in a host pocket or an outbound adapter, and it grows several ad-hoc packages directly under `internal/` that H0 makes findings. That is known, dated debt with a named follow-up plan, not a pattern to copy: the shape to copy is the contract's layout section, and the worked examples are `cms`, `minimal`, and `jobs-minimal`.

## Jobs in memory

`examples/jobs-minimal` uses the public `pockets/jobs/memstore` and starts the runtime explicitly. Its demo protocol exercises a normal job, a retry, a dead-letter, and a recurring schedule.

```bash
cd examples/jobs-minimal
go run ./cmd/server
```

Jobs registers no HTTP routes today. The host owns the runtime goroutine and cancels its context to drain.

## GOTH showcase

`examples/goth-showcase` renders the GOTH component catalog independently of a pocket. The sibling `e2e` project contains Playwright and accessibility coverage for Chromium, Firefox, and WebKit.

Use it to inspect components, theme behavior, narrow layouts, right-to-left behavior, CSP-safe interaction, HTMX mechanics, and dark mode.

:::note Example code is intentionally explicit

Composition roots are verbose because they are the one place dependencies and lifecycle should be visible. Extract local builders when a host grows, but keep provider selection and ownership discoverable from `cmd`.

:::
