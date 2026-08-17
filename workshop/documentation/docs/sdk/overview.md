---
title: SDK overview
description: The stdlib-only Gopernicus kernel and its internal layering law.
---

# SDK overview

`github.com/gopernicus/gopernicus/sdk` is the dependency-free kernel used by the package collection. Its `go.mod` has no `require` block, so the standard-library boundary is structural rather than conventional.

The SDK is not an interfaces-only abstraction layer. It owns reusable vocabulary, mechanism, and observable policy. Interfaces are the seams that let concrete integrations and feature adapters plug into that policy.

## Four physical tiers

```text
sdk/                       kernel
├── context.go             request / trace / span context vocabulary
├── errors.go              transport-agnostic error classes
├── foundation/            pure mechanism and data vocabulary
├── capabilities/          behavioral ports and shared policy
└── feature/               host↔feature composition
```

| Tier | Meaning | Import rule |
|---|---|---|
| kernel | vocabulary every tier may need | standard library only; no SDK subpackage |
| foundation | pure mechanism, no service semantics | kernel only; no foundation siblings |
| capabilities | behavioral ports + observable policy | kernel + foundation; no capability siblings |
| feature | explicit package composition contract | sanctioned composer |

Cross-capability composition leaves the SDK. For example, adapting email delivery into notifications belongs in `integrations/notify/mailer`, not in either capability package.

## Kernel vocabulary

The root package exposes stable error classes such as not found, invalid input, conflict, unavailable, unauthorized, and forbidden. Foundation web maps these to transport status without domain packages importing HTTP.

It also owns request/trace/span identifiers in context so logging, tracing, HTTP, and higher tiers share one vocabulary without importing one another.

## Admission test

Add a concern to the SDK only when all three conditions hold:

1. **Plurality or broad test seam**—at least two real implementations exist or are genuinely expected, or many packages must fake it in tests.
2. **Narrow, stable port**—the contract does not leak backend-specific capability flags.
3. **Shared policy or vocabulary**—there is behavior, error mapping, lifecycle, or data language worth centralizing.

Keep the concern app-local when it has one implementation unlikely to vary, when backend details cannot be hidden honestly, or when wrapping a concrete handle adds no policy. A small interface declared by its consumer is often the right answer.

## Naming by behavior

Architectural roles do not appear in type names:

| Role | Naming rule | Examples |
|---|---|---|
| consumer interface | capability noun or `-er` when natural; never `Port` | `Storer`, `Resolver`, `SignedURLer` |
| service used by applications | domain noun | `Cache`, `FileStore`, `RateLimiter` |
| implementation | technology is the package name | `goredis`, `gcs`, `pgxdb` |

Optional backend behavior is represented by segregated interfaces. File storage, for example, has a core `Storer` plus optional `SignedURLer` and `ResumableUploader`; a backend does not implement meaningless methods that return “not supported.”

## Defaults are optional

A capability is defined by contract and policy, not by whether SDK can implement it with the standard library.

- cache, email, events, file storage, notification, rate limiting, and tracing ship useful defaults;
- OAuth has no vendor-neutral default;
- keyed work has no in-SDK implementation—the jobs feature is its implementation of record;
- identity resolution is foundation vocabulary plus a port; authentication is the first real implementation.

See [Foundation](foundation.md) and [Capabilities](capabilities.md) for the package catalogs.

## Testing contracts

Capabilities with interchangeable implementations publish conformance suites, including cache, events, file storage, rate limiting, and work. Integrations run these suites so behavior is pinned above driver-specific unit tests.

The same pattern repeats at feature scale: a feature's `storetest` package is the executable specification for every datastore implementation.

## What the SDK does not own

- application routes or page designs;
- concrete database/cloud clients;
- feature aggregates and schemas;
- a global dependency container;
- feature-to-feature orchestration;
- migrations or process startup;
- an interface over every concrete type.

Those responsibilities remain in features, integrations, or the host where their policy is visible.
