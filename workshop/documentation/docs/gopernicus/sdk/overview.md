---
sidebar_position: 1
title: Overview
---

# SDK

The SDK is Gopernicus's foundation layer — a set of packages that wrap Go's standard library and nothing else. No framework dependencies, no third-party imports. Every other layer can use the SDK; the SDK depends on none of them.

## Why stdlib only?

Keeping the SDK dependency-free means it can be imported from anywhere in the stack without pulling in framework concerns. It also makes the packages easy to reason about, test, and reuse independently of Gopernicus.

Infrastructure-level concerns (database clients, cloud SDKs, observability exporters) live in the [Infrastructure](../infrastructure/overview.md) layer, not here.

## Dependency inversion at the boundary

When an SDK package needs outside functionality — tracing, for example — it defines its own interface rather than importing an external library. The caller supplies a satisfier (an adapter that bridges the external implementation to the SDK's interface).

The `workers` package is a good example. It can't import OpenTelemetry without violating the stdlib-only rule, so it defines its own `Tracer` and `SpanFinisher` interfaces:

```go
// Defined in sdk/workers — no external imports required
type Tracer interface {
    StartSpan(ctx context.Context, operationName string) (context.Context, SpanFinisher)
}
```

The Infrastructure or App layer then provides an adapter that satisfies `workers.Tracer` using its real OTel client. The SDK stays clean; the wiring happens outside.

## Packages

| Package | Purpose |
|---|---|
| [Async](./async.md) | Bounded goroutine pool with panic recovery |
| [Conversion](./conversion.md) | Type conversions, pointer helpers, date parsing, slice utilities |
| [Environment](./environment.md) | Struct-tag-driven env var parsing |
| [Errs](./errs.md) | Sentinel errors for domain and data layers |
| [FOP](./fop.md) | Cursor-based pagination, ordering, and filter types |
| [Logger](./logger.md) | Structured logging with tracing integration |
| [Validation](./validation.md) | Field validation, custom rules, error formatting |
| [Web](./web.md) | HTTP server, routing, middleware, JSON, OpenAPI |
| [Workers](./workers.md) | Background job workers |

## A quick look

**Sentinel errors** are defined once in `errs` and wrapped at the domain layer:

```go
// domain package defines a typed error
var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)

// bridge layer checks at any level of wrapping
if errors.Is(err, errs.ErrNotFound) {
    return web.ErrNotFound("user not found")
}
```

**Async pool** for fire-and-forget concurrency with configurable backpressure:

```go
pool := async.NewPool(async.IOPreset()...)
defer pool.Close(ctx)

pool.Go(func() {
    invalidateCache(userID)
})
```

See each package page for full usage.
