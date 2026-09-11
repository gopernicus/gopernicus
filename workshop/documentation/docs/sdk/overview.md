---
title: SDK overview
description: The stdlib-only Gopernicus kernel and its internal layering law.
---

# SDK overview

`github.com/gopernicus/gopernicus/sdk` is the dependency-free kernel used by the package collection. Its `go.mod` has no `require` block, so the standard-library boundary is structural rather than conventional.

The SDK is not an interfaces-only abstraction layer. It owns reusable vocabulary, mechanism, and observable policy. Interfaces are the seams that let concrete integrations and pocket adapters plug into that policy.

## Four physical tiers

```text
sdk/                       kernel
├── context.go             request / trace / span context vocabulary
├── errors.go / faults.go  errors and validation/write faults
├── identity.go            principals, projection and context
├── id.go                  configurable entity-ID generation
├── pointer.go / slug.go   small general-purpose helpers
├── pkg/            pure mechanism and data vocabulary
└── capabilities/          behavioral ports and shared policy
```

| Tier | Meaning | Import rule |
|---|---|---|
| kernel | common vocabulary and small, clearly named primitives | standard library only; no SDK subpackage |
| pkg | pure mechanism, no service semantics | kernel only; no pkg siblings |
| capabilities | behavioral ports + observable policy | kernel + pkg; no capability siblings |

Each capability may have cohesive subpackages. `notify/email` owns typed email and adapts it into `notify.Delivery` within the same subsystem. Different capabilities remain independent; provider libraries live in integrations.

The shared host mounting contract is in the separate `pockets` module, which
depends on SDK. Concrete pocket modules opt into that contract independently.

## Kernel vocabulary

The root package exposes stable error classes such as not found, invalid input, conflict, unavailable, unauthorized, and forbidden. The web package maps these to transport status without domain packages importing HTTP.

It also owns request/trace/span identifiers in context so logging, tracing, HTTP, and higher tiers share one vocabulary without importing one another.

## Root API

Import `github.com/gopernicus/gopernicus/sdk` for these primitives. They live in
separate root files and need no additional package imports.

| Concern | API |
|---|---|
| Optional values | `Deref`, `DerefOr` |
| URL slug generation | `Slugify` |
| Entity-ID strategy | `IDGenerator`, `IDGenerateFunc`, `NewIDGenerator`, `NanoID`, `DatabaseID` |
| Default ID vocabulary | `DefaultIDAlphabet`, `DefaultIDLength` |
| Caller context | `Principal`, `WithPrincipal`, `PrincipalFromContext` |
| Identity projection | `IdentityInfo`, `IdentityAddress`, `IdentityResolver` |
| Conventional subject/address strings | `PrincipalTypeUser`, `PrincipalTypeServiceAccount`, `AddressKindEmail`, `AddressKindPhone` |

## Pointer values

`sdk.Deref` returns the pointed-to value, or the type's zero value when the
pointer is nil. `sdk.DerefOr` uses a caller-supplied fallback. A present
empty string, zero, or false is kept; only nil selects the fallback.

```go
import "github.com/gopernicus/gopernicus/sdk"

func displayName(name *string) string {
    return sdk.DerefOr(name, "Anonymous")
}
```

Use Go's built-in `new(value)` to construct a pointer to a copy of a value.
For example, `new(int64(1))` produces an `*int64` containing 1.

## URL slugs

`sdk.Slugify` lowercases text, folds a limited set of accented letters, and
collapses characters outside `[a-z0-9]` into hyphens. It can return an empty
string and does not enforce uniqueness. It does not normalize Unicode:
`"école"` becomes `"ecole"`, while a decomposed accent in `"e\u0301cole"`
produces `"e-cole"`.

Consumers decide when to regenerate stored identifiers and how to preserve old
URLs. CMS currently regenerates entry and term slugs during edits and menu
slugs during renames; registered content types also derive route bases from
their plural names. An algorithm change could affect a stored slug on its
next edit as well as a route derived at runtime. This audit preserves the
existing algorithm and output.

## Entity IDs

The zero-value `sdk.IDGenerator` emits 21-character nanoid-shaped identifiers.
Hosts configure the strategy once, at composition time:

```go
ids := sdk.IDGenerator{}
dbIDs := sdk.NewIDGenerator(sdk.DatabaseID)
uuidIDs := sdk.NewIDGenerator(googleuuid.V7())
```

`DatabaseID` returns an empty ID so a supporting store assigns it; it does not
connect to a database. `NanoID(alphabet, size)` returns an `IDGenerateFunc`,
validating unique ASCII alphabet bytes and size at construction. Empty alphabet
and zero size select `DefaultIDAlphabet` and `DefaultIDLength`. Custom generators
and the UUID integration use the same function type. The default alphabet, ID
shape and generation methods are unchanged by their move to root.

## Caller identity

`sdk.Principal{Type, ID}` identifies the effective caller. `WithPrincipal` and
`PrincipalFromContext` share a private key distinct from request/trace/span keys;
both principal fields must be present. Authentication establishes the caller,
and each endpoint decides whether one is required.

`IdentityResolver` returns `IdentityInfo`: a principal, display name and
`[]IdentityAddress`. Each address has `Kind` and `Value`; conventional kinds are
`AddressKindEmail` and `AddressKindPhone`. Resolved addresses do not establish
permission to notify them. User records, credentials and eligibility remain with
the owning pocket or application. ID generation is independent of this caller
vocabulary; an entity identifier need not represent a person or account.

## Root versus a named package

Put common vocabulary and small, clearly named primitives in root. Keep a named
package when its qualifier explains a coherent API: `validation.Email` is an
input check, and `environment.LoadPath` loads configuration. Root imports only
stdlib and never an SDK subpackage. Neither application lifecycle nor global
configuration belongs there. Prior use by two packages under `pkg/` is not a
requirement for a root primitive.

## Admission test

For a replaceable capability or service, require all three conditions below.
Small root primitives follow the root criteria above and need no artificial
interface or alternative implementation.

1. **Plurality or broad test seam**—at least two real implementations exist or are genuinely expected, or many packages must fake it in tests.
2. **Narrow, stable port**—the contract does not leak backend-specific capability flags.
3. **Shared policy or vocabulary**—there is behavior, error mapping, lifecycle, or data language worth centralizing.

Keep the concern app-local when it has one implementation unlikely to vary, when backend details cannot be hidden honestly, or when wrapping a concrete handle adds no policy. A small interface declared by its consumer is often the right answer.

## Naming by behavior

Architectural roles do not appear in type names:

| Role | Naming rule | Examples |
|---|---|---|
| consumer interface | capability noun or `-er` when natural; never `Port` | `Storer`, `Resolver`, `SignedURLer` |
| service with shared behavior | domain noun | `Cache` |
| implementation | technology is the package name | `goredis`, `gcs`, `pgxdb` |

A capability can expose its adapter directly through a consumer interface.
File storage and rate limiting do not need delegating service wrappers.

Optional backend behavior is represented by segregated interfaces. File storage, for example, has a core `Storer` plus optional `SignedURLer` and `ResumableUploader`; a backend does not implement meaningless methods that return “not supported.”

## Defaults are optional

A capability is defined by contract and policy, not by whether SDK can implement it with the standard library.

- cache, email, events, file storage, notification, rate limiting, and tracing ship useful defaults;
- OAuth has no vendor-neutral default;
- keyed work has no in-SDK implementation—the jobs pocket is its implementation of record;
- identity resolution is root vocabulary plus a port; authentication is the first real implementation.

See [SDK packages](pkg.md) and [Capabilities](capabilities.md) for the package catalogs.

## Testing contracts

Capabilities with interchangeable implementations publish conformance suites, including cache, events, file storage, rate limiting, and work. Integrations run these suites so behavior is pinned above driver-specific unit tests.

The same pattern repeats at pocket scale: a pocket's `storetest` package is the executable specification for every datastore implementation.

## What the SDK does not own

- application routes or page designs;
- concrete database/cloud clients;
- pocket aggregates and schemas;
- a global dependency container;
- pocket-to-pocket orchestration;
- migrations or process startup;
- an interface over every concrete type.

Those responsibilities remain in pockets, integrations, or the host where their policy is visible.
