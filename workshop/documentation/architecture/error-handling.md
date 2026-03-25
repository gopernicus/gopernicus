# Error Handling

Gopernicus uses a layered error strategy: domain-agnostic sentinel errors in
`sdk/errs`, domain-specific errors that wrap them, and bridge-layer mapping to
HTTP status codes.  The approach relies on Go's standard `errors.Is` and
`fmt.Errorf` wrapping -- no custom error frameworks.

## Sentinel errors (`sdk/errs`)

The `sdk/errs` package defines transport-agnostic sentinel errors:

```go
var (
    ErrNotFound         = errors.New("not found")
    ErrAlreadyExists    = errors.New("already exists")
    ErrInvalidReference = errors.New("invalid reference")
    ErrInvalidInput     = errors.New("invalid input")
    ErrUnauthorized     = errors.New("unauthorized")
    ErrForbidden        = errors.New("forbidden")
    ErrConflict         = errors.New("conflict")
    ErrExpired          = errors.New("expired")
)
```

These sentinels are the only errors the bridge layer needs to know about.
Core packages never import HTTP types; they return wrapped sentinels instead.

## Domain errors

Each use-case or repository package defines its own error variables that wrap
the sentinels with domain context:

```go
// core/auth/invitations/errors.go
var (
    ErrInvitationNotFound    = fmt.Errorf("invitation not found: %w", errs.ErrNotFound)
    ErrInvitationExpired     = fmt.Errorf("invitation expired: %w", errs.ErrConflict)
    ErrInvitationAlreadyUsed = fmt.Errorf("invitation already used: %w", errs.ErrConflict)
    ErrIdentifierMismatch    = fmt.Errorf("identifier does not match invitation: %w", errs.ErrForbidden)
    ErrAlreadyMember         = fmt.Errorf("already a member: %w", errs.ErrAlreadyExists)
)
```

The `%w` verb preserves the chain so `errors.Is(err, errs.ErrNotFound)` works
from any layer.

## Generated repository errors

The code generator also produces error variables in every repository's
`generated.go`:

```go
var (
    ErrInvitationNotFound         = fmt.Errorf("invitation: %w", errs.ErrNotFound)
    ErrInvitationAlreadyExists    = fmt.Errorf("invitation: %w", errs.ErrAlreadyExists)
    ErrInvitationInvalidReference = fmt.Errorf("invitation: %w", errs.ErrInvalidReference)
    ErrInvitationInvalidInput     = fmt.Errorf("invitation: %w", errs.ErrInvalidInput)
)
```

The pgx store maps Postgres error codes (unique violation, foreign key
violation, not-null violation) to these error variables.  Repository callers
only see the domain-level error, not the database error.

## Bridge-layer mapping

### `web.ErrFromDomain`

The `sdk/web` package provides `ErrFromDomain(err)` which maps any error
wrapping an `sdk/errs` sentinel to a `*web.Error` with the correct HTTP
status:

| Sentinel             | HTTP Status | Code               |
|----------------------|-------------|---------------------|
| `ErrNotFound`        | 404         | `not_found`         |
| `ErrAlreadyExists`   | 409         | `already_exists`    |
| `ErrUnauthorized`    | 401         | `unauthenticated`   |
| `ErrForbidden`       | 403         | `permission_denied` |
| `ErrInvalidInput`    | 400         | `bad_request`       |
| `ErrInvalidReference`| 400         | `invalid reference` |
| `ErrConflict`        | 409         | `conflict`          |
| `ErrExpired`         | 410         | `expired`           |
| (anything else)      | 500         | `internal`          |

The messages returned by `ErrFromDomain` are intentionally generic ("not
found", "conflict") to avoid leaking internal details.

### `web.RespondJSONDomainError`

This is the standard one-liner for bridge handlers:

```go
record, err := b.invitationRepository.Get(r.Context(), id)
if err != nil {
    web.RespondJSONDomainError(w, err)
    return
}
```

It calls `ErrFromDomain` internally and writes the JSON error response.

### `web.RespondJSONError`

For cases where you need a specific message or status, construct a `*web.Error`
directly and pass it to `RespondJSONError`:

```go
web.RespondJSONError(w, web.ErrBadRequest("invalid pagination: " + err.Error()))
web.RespondJSONError(w, web.ErrNotFound("invitation not found"))
web.RespondJSONError(w, web.ErrForbidden("email not verified"))
```

### Handling specific errors before the generic fallback

When the handler needs to return a user-facing message for a particular error,
check for it explicitly before falling through to `RespondJSONDomainError`:

```go
if errors.Is(err, invitations.ErrIdentifierMismatch) {
    web.RespondJSONError(w, web.ErrForbidden("identifier does not match"))
    return
}
web.RespondJSONDomainError(w, err) // generic fallback
```

## Validation errors

`web.FieldErrors` accumulates per-field validation errors.  The `Validate()`
method on bridge request types returns `FieldErrors` which
`web.ErrValidation(err)` converts to a 400 response with the `fields` array:

```go
req, err := web.DecodeJSON[CreateRequest](r)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

Response shape:

```json
{
    "message": "validation failed",
    "code": "validation_failed",
    "fields": [
        {"field": "email", "message": "is required"},
        {"field": "password", "message": "must be at least 8 characters"}
    ]
}
```

## Convenience constructors

`sdk/web` provides typed constructors for common HTTP errors:
`ErrBadRequest`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`,
`ErrConflict`, `ErrGone`, `ErrTooManyRequests`, `ErrUnavailable`,
`ErrInternal`.  Each sets the appropriate status code and machine-readable
code string.

---

## Related

- `sdk/errs/errs.go` -- sentinel error definitions
- `sdk/web/errors.go` -- `ErrFromDomain`, `FieldErrors`, HTTP error constructors
- `sdk/web/response.go` -- `RespondJSONError`, `RespondJSONDomainError`
- `core/auth/invitations/errors.go` -- domain error example
- `core/repositories/rebac/invitations/generated.go` -- generated repository errors
- [Architecture Overview](overview.md)
- [Design Philosophy](design-philosophy.md)
- [Cases](cases.md)
