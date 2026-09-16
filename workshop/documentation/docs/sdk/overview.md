---
title: SDK overview
description: The stdlib-only Gopernicus kernel, what to import, how it is layered, and what it refuses to own.
---

# SDK overview

`github.com/gopernicus/gopernicus/sdk` is the kernel every other Gopernicus module
builds on. Its `go.mod` has no `require` block, so importing it adds no third-party
dependency to your build. It needs Go 1.26 or newer.

## What you get

| Import | Use it for | Page |
|---|---|---|
| `sdk` (root) | error classes, request/trace/span context, principals, entity IDs, pointer and slug helpers | this page |
| `sdk/pkg/...` | pure mechanism: env config, logging, listing, validation, crypto primitives, worker pools, async tasks | [SDK packages](pkg.md) |
| `sdk/pkg/web` | the `net/http` transport kit: router, middleware, JSON and HTML responders, SSE, static files, server lifecycle | [Web package](web.md) |
| `sdk/capabilities/...` | behavioral ports with shared policy and optional stdlib defaults: cache, events, file storage, notify, rate limiting, tracing, transactions, OAuth, keyed work | [Capabilities](capabilities.md) |

The SDK is not an interfaces-only abstraction layer. It owns vocabulary,
mechanism and observable policy; interfaces are the seams where integrations
and pocket adapters plug into that policy. Concrete drivers such as PostgreSQL,
Redis, S3, SendGrid and OpenTelemetry live under `integrations/` and are
imported only by hosts that choose them.

## Three tiers, one direction

```text
sdk/                       kernel: vocabulary and small primitives
├── pkg/                   pure mechanism, no service semantics
└── capabilities/          behavioral ports plus shared policy
```

| Tier | May import | Never imports |
|---|---|---|
| kernel (root) | standard library | any SDK subpackage |
| `pkg/*` | kernel | a sibling under `pkg/` |
| `capabilities/*` | kernel and `pkg/*` | a sibling capability |

A capability may have cohesive subpackages: `notify/email` owns typed email and
adapts it into `notify.Delivery`. The rules are checked, not just described.
`make guard-sdk-layering` fails on a wrong-direction import,
`make guard-sdk-stdlib` fails on any third-party import under `sdk/`, and
`make guard-sdk-no-outward` fails if the SDK ever imports a pocket, integration
or example.

The host mounting contract lives in the separate `pockets` module, which
depends on the SDK. Pocket modules opt into that contract independently.

## Root API

Everything below is in the root package and needs no other import.

| Concern | API |
|---|---|
| Error classes | `ErrNotFound`, `ErrInvalidInput`, `ErrConflict`, `ErrUnavailable`, `ErrUnauthorized`, `ErrForbidden`; `ValidationError` and `Violation` for field problems |
| Correlation context | request, trace and span IDs in context, for example `WithRequestID` |
| Caller context | `Principal`, `WithPrincipal`, `PrincipalFromContext` |
| Identity projection | `IdentityInfo`, `IdentityAddress`, `IdentityResolver` |
| Conventional strings | `PrincipalTypeUser`, `PrincipalTypeServiceAccount`, `AddressKindEmail`, `AddressKindPhone` |
| Entity IDs | `IDGenerator`, `IDGenerateFunc`, `NewIDGenerator`, `NanoID`, `DatabaseID`, `DefaultIDAlphabet`, `DefaultIDLength` |
| Optional values | `Deref`, `DerefOr` |
| URL slugs | `Slugify` |

**Errors.** Domain code returns or wraps the root error classes. The web
package maps them to HTTP status codes, so domain packages never import HTTP.
Validation collects field problems into `sdk.ValidationError`, which matches
`sdk.ErrInvalidInput` through `errors.Is` even after wrapping.

**Context.** Request, trace and span identifiers travel in context so logging,
tracing and HTTP share one vocabulary without importing one another. The
principal uses a separate private key; both `Type` and `ID` must be present.
Authentication establishes the caller, and each endpoint decides whether one is
required.

**Identity resolution.** `IdentityResolver` returns `IdentityInfo`: a principal,
a display name and addresses with `Kind` and `Value`. A resolved address is not
permission to notify it. User records, credentials and eligibility stay with the
owning pocket or application. The authentication pocket is the first real
implementation.

**Entity IDs.** The zero-value `sdk.IDGenerator` emits 21-character
nanoid-shaped identifiers. Hosts choose a strategy once, at composition time:

```go
ids := sdk.IDGenerator{}                         // default nanoid
dbIDs := sdk.NewIDGenerator(sdk.DatabaseID)       // empty ID; the store assigns it
uuidIDs := sdk.NewIDGenerator(googleuuid.V7())    // integrations/cryptids/google-uuid
```

`NanoID(alphabet, size)` returns an `IDGenerateFunc` and validates its unique
ASCII alphabet and size at construction; empty inputs select the defaults. An
entity ID need not represent a person or account.

**Pointers and slugs.** `sdk.Deref` returns the pointed-to value or the zero
value for nil; `sdk.DerefOr` takes a fallback, and only nil selects it. Use Go's
`new(value)` to build a pointer to a copy. `sdk.Slugify` lowercases, folds a
limited set of accented letters and collapses everything outside `[a-z0-9]` to
hyphens. It can return an empty string, does not enforce uniqueness and does not
normalize Unicode: `"école"` becomes `"ecole"`, but a decomposed accent yields
`"e-cole"`. Consumers decide when to regenerate stored slugs; CMS regenerates
entry and term slugs on edit and derives route bases from plural names.

## Defaults are optional

A capability is defined by its contract and policy, not by whether the standard
library can implement it.

- cache, email, events, file storage, notification, rate limiting and tracing ship useful defaults;
- OAuth has no vendor-neutral default;
- keyed work has no in-SDK implementation; the jobs pocket is its implementation of record;
- identity resolution is root vocabulary plus a port; authentication implements it.

Capabilities with interchangeable implementations publish conformance suites
(cache, events, file storage, rate limiting, work). Integrations run those suites
so behavior is pinned above driver-specific tests. Pockets repeat the pattern:
a pocket's `storetest` package is the executable specification for every store.

## What the SDK does not own

- application routes or page designs;
- concrete database or cloud clients;
- pocket aggregates and schemas;
- a global dependency container;
- pocket-to-pocket orchestration;
- migrations or process startup;
- an interface over every concrete type.

Those live in pockets, integrations or the host, where their policy is visible.

## Rules for adding to the SDK

These matter when you contribute; skip them when you are only consuming.

**Root versus a named package.** Common vocabulary and small, clearly named
primitives go in root. A named package earns its qualifier when it explains a
coherent API: `validation.Email` is an input check, `environment.LoadPath` loads
configuration. Neither application lifecycle nor global configuration belongs
in root.

**Admission test for a capability.** Require all three:

1. plurality or a broad test seam: at least two real implementations exist or are genuinely expected, or many packages must fake it in tests;
2. a narrow, stable port that does not leak backend-specific capability flags;
3. shared policy or vocabulary worth centralizing: behavior, error mapping, lifecycle or data language.

Otherwise keep the concern app-local. A small interface declared by its
consumer is often the right answer.

**Naming by behavior.** Architectural roles never appear in type names.

| Role | Rule | Examples |
|---|---|---|
| consumer interface | capability noun or `-er`; never `Port` | `Storer`, `Resolver`, `SignedURLer` |
| service with shared behavior | domain noun | `Cache` |
| implementation | the technology is the package name | `goredis`, `gcs`, `pgxdb` |

Optional backend behavior is a segregated interface. File storage has a core
`Storer` plus optional `SignedURLer` and `ResumableUploader`; a backend never
implements a method that only returns "not supported".
