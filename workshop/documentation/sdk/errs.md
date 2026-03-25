# sdk/errs -- Error Sentinels Reference

Package `errs` provides transport-agnostic sentinel errors for the domain and data layers. These errors are designed to be wrapped with domain context and checked at boundaries using `errors.Is()`.

**Import:** `github.com/gopernicus/gopernicus/sdk/errs`

## Sentinel Errors

| Sentinel | Meaning | HTTP mapping |
|---|---|---|
| `ErrNotFound` | Entity does not exist | 404 Not Found |
| `ErrAlreadyExists` | Unique key collision | 409 Conflict |
| `ErrInvalidReference` | Foreign key reference is invalid | 400 Bad Request |
| `ErrInvalidInput` | Violates a CHECK or NOT NULL constraint | 400 Bad Request |
| `ErrUnauthorized` | Caller is not authenticated | 401 Unauthorized |
| `ErrForbidden` | Caller lacks permission | 403 Forbidden |
| `ErrConflict` | State conflict (optimistic locking, invalid transition) | 409 Conflict |
| `ErrExpired` | Time-bound resource has expired (tokens, invites) | 410 Gone |

## Defining Domain Errors

Wrap a sentinel with domain context using `fmt.Errorf`:

```go
var (
    ErrUserNotFound    = fmt.Errorf("user: %w", errs.ErrNotFound)
    ErrEmailTaken      = fmt.Errorf("user email: %w", errs.ErrAlreadyExists)
    ErrInvalidOrgRef   = fmt.Errorf("organization reference: %w", errs.ErrInvalidReference)
    ErrInviteExpired   = fmt.Errorf("invite: %w", errs.ErrExpired)
)
```

Because `%w` creates an error chain, `errors.Is(ErrUserNotFound, errs.ErrNotFound)` returns true.

## Checking Errors at the Bridge Layer

The bridge (HTTP handler) layer checks sentinels to produce the correct HTTP response.

### Generic fallback with ErrFromDomain

`web.ErrFromDomain(err)` and `web.RespondJSONDomainError(w, err)` map any error wrapping an `errs.*` sentinel to a safe HTTP error with a generic message:

```go
if err != nil {
    web.RespondJSONDomainError(w, err)
    return
}
```

### Explicit handling for user-facing messages

When you need a specific message, handle the error before the generic fallback:

```go
if errors.Is(err, errs.ErrAlreadyExists) {
    web.RespondJSONError(w, web.ErrConflict("a user with that email already exists"))
    return
}
web.RespondJSONDomainError(w, err)
```

## Generated Repository Errors

Generated repositories map PostgreSQL errors to `errs.*` sentinels automatically:

| PostgreSQL error code | Infrastructure sentinel | Domain sentinel |
|---|---|---|
| `23505` unique_violation | `pgxdb.ErrDBDuplicatedEntry` | `errs.ErrAlreadyExists` |
| `23503` foreign_key_violation | `pgxdb.ErrDBForeignKeyViolation` | `errs.ErrInvalidReference` |
| `23514` check_violation | `pgxdb.ErrDBCheckViolation` | `errs.ErrInvalidInput` |
| `23502` not_null_violation | `pgxdb.ErrDBNotNullViolation` | `errs.ErrInvalidInput` |
| no rows | `pgxdb.ErrDBNotFound` | `errs.ErrNotFound` |

The `pgxdb.HandlePgError()` function converts raw PostgreSQL errors to infrastructure sentinels. Generated stores then wrap those into domain errors.

## Full Mapping Table (ErrFromDomain)

| Domain sentinel | HTTP status | Code | Message |
|---|---|---|---|
| `errs.ErrNotFound` | 404 | `not_found` | "not found" |
| `errs.ErrAlreadyExists` | 409 | `already_exists` | "already exists" |
| `errs.ErrUnauthorized` | 401 | `unauthenticated` | "unauthorized" |
| `errs.ErrForbidden` | 403 | `permission_denied` | "forbidden" |
| `errs.ErrInvalidInput` | 400 | `bad_request` | "invalid input" |
| `errs.ErrInvalidReference` | 400 | `bad_request` | "invalid reference" |
| `errs.ErrConflict` | 409 | `conflict` | "conflict" |
| `errs.ErrExpired` | 410 | `expired` | "expired" |
| (unrecognized) | 500 | `internal` | "internal error" |

## Related

- [sdk/web](../sdk/web.md) -- `ErrFromDomain`, `RespondJSONDomainError`, and HTTP error constructors
- [infrastructure/database](../infrastructure/database.md) -- `pgxdb.HandlePgError` and infrastructure-level sentinels
