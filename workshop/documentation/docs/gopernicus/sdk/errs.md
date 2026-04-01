---
sidebar_position: 5
title: Errs
---

# SDK — Errs

`sdk/errs` defines transport-agnostic sentinel errors for the domain and data layers. It wraps only the `errors` package from the standard library.

The sentinels carry no HTTP or gRPC knowledge — that mapping happens at the bridge layer. This keeps domain code independent of the transport it's served over.

## Sentinels

| Error | Meaning |
|---|---|
| `ErrNotFound` | The requested entity does not exist |
| `ErrAlreadyExists` | An entity with the same unique key already exists |
| `ErrInvalidReference` | A foreign key reference is invalid |
| `ErrInvalidInput` | Input violates a constraint (CHECK, NOT NULL, etc.) |
| `ErrUnauthorized` | The caller is not authenticated |
| `ErrForbidden` | The caller lacks permission for the operation |
| `ErrConflict` | A state conflict — invalid transition or optimistic lock failure |
| `ErrExpired` | A time-bound resource has expired (token, invite, etc.) |

## Wrapping at the domain layer

Domain packages define typed errors by wrapping a sentinel. This preserves the sentinel for checking at boundaries while adding context:

```go
// in your domain or repository package
var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)
var ErrEmailTaken   = fmt.Errorf("user: %w", errs.ErrAlreadyExists)
```

Callers can check at either level:

```go
errors.Is(err, ErrUserNotFound)  // specific
errors.Is(err, errs.ErrNotFound) // sentinel
```

## IsExpected

`IsExpected` reports whether an error wraps any known sentinel. The bridge layer uses this to distinguish errors that have a defined HTTP status from unexpected ones that should return 500 and be logged.

```go
if !errs.IsExpected(err) {
    log.Error("unexpected error", "err", err)
}
```

See [Core Error Handling](../core/error-handling.md) for how domain errors are structured, and [Bridge Overview](../bridge/overview.md) for how they map to HTTP responses.
