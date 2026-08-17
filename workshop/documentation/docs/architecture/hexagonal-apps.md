---
title: Hexagonal host applications
description: Organize app-local domains, inbound delivery, outbound adapters, and composition.
---

# Hexagonal host applications

Not every domain should become a reusable Gopernicus feature. A host's unique business rules belong in its own hexagon, usually under `internal`.

```text
cmd/
  server/                 composition root
internal/
  logic/
    domains/<domain>/     entities, ports, and domain services
    app/                  workflows spanning domains
  inbound/
    http/                 global router/middleware plumbing
    domains/<domain>/     domain handlers
    views/                optional host-owned pages and view models
  outbound/
    database/             app repositories
    providers/            vendor adapters
```

Directory names are guidance; the dependency direction is the contract.

## Logic has two tiers

### Domain logic

`internal/logic/domains/<domain>` owns the language and invariants of one domain:

- entities and value types;
- repository/provider ports the domain consumes;
- domain services and use cases;
- domain-specific errors;
- tests using fakes for the declared ports.

Keep related entities, ports, and services together until size creates a real reason to split them. Hexagonal architecture does not require one package per noun.

### Application workflows

`internal/logic/app` coordinates several domains or performs host-level policy:

- onboarding that creates an account, organization, and membership;
- a publication workflow spanning content, authorization, and notifications;
- transactional coordination over several repositories;
- a host-facing service boundary consumed by multiple transports.

An app workflow imports domain packages inward. Domains do not import `app` back.

## Where a port lives

Put a port with the code that **consumes** it.

```go
// internal/logic/domains/orders
type PaymentAuthorizer interface {
    Authorize(ctx context.Context, accountID string, cents int64) error
}
```

The outbound payment adapter implements this shape implicitly. Do not place the interface next to the vendor client merely because the adapter implements it; that makes policy depend on infrastructure's vocabulary.

Promote a port into the SDK only when it has multiple real implementations or a broad test-seam need, is narrow and stable, and carries shared policy or vocabulary. Otherwise a one-method app-local interface is healthy.

## Inbound is an adapter

HTTP handlers translate between transport and use cases:

1. parse path, query, form, or JSON input;
2. call a logic service;
3. map domain errors to an HTTP result;
4. render a view or serialize a response.

Handlers should not write SQL, choose providers, or hold domain invariants. Use `sdk/foundation/web` for HTTP mechanism without moving application routes into the SDK.

A useful inbound split is:

```text
internal/inbound/
  http/                   router construction and global middleware
  domains/orders/         order handlers and transport models
  views/
    components/           reusable host components
    layouts/              page chrome
    pages/                route-level rendering
```

Feature cores use a related but library-safe anatomy: their inbound code remains internal, while third-party view implementations live in sibling modules.

## Outbound owns technology

Outbound packages turn domain ports into database queries or provider calls. They may know both the consumer's contract and the chosen technology.

```text
internal/outbound/database/orderspgx/
internal/outbound/providers/stripe/
```

Use a reusable Gopernicus integration where its generic seam fits. Keep app-specific mapping in the host. A database connector may own transactions and error mapping; an order repository still owns order SQL.

## Composition belongs in `cmd`

The composition root should make these choices visible:

- environment and deployment posture;
- logger and tracing implementation;
- database and external clients;
- app repositories and feature store modules;
- logic services;
- feature services and config;
- router and middleware ordering;
- background runtimes;
- startup probes and shutdown order.

Passing a large global dependency bag into every package hides this graph. Prefer constructors with narrow dependencies and small local builder functions when `main` becomes long.

## When to extract a feature

Keep a domain app-local by default. Extract a reusable feature when:

- multiple hosts need the whole capability;
- the public rim can be kept stable and datastore-neutral;
- hosts need meaningful configure/replace/inject/extend seams;
- the core can require only SDK;
- repository behavior can be expressed as a conformance suite;
- a zero-infrastructure host can prove the feature without a bundled store.

Then follow the stricter [Feature contract](feature-contract.md).
