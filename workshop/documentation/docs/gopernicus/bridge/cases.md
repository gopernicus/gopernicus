---
sidebar_position: 3
title: Cases
---

# Bridge — Cases

Case bridges are hand-written HTTP handlers for business logic that goes beyond CRUD. They translate HTTP requests into calls to [Core Cases](../core/cases.md).

Unlike generated repository bridges, case bridges are entirely hand-written. They handle complex request parsing, multi-step workflows, dynamic authorization, custom error mapping, and event subscribers.

## Directory Structure

User-defined case bridges live under `bridge/cases/`. The framework also ships case bridges under `bridge/auth/` for authentication and invitations.

```
bridge/cases/
└── checkoutbridge/
    ├── bridge.go       # Bridge struct, constructor, dependencies
    ├── http.go         # AddHttpRoutes() and HTTP handlers
    ├── model.go        # Request/response types
    └── subscribers.go  # Event subscribers (if needed)
```

Scaffold with `gopernicus new case <name>`, which creates both the core case and bridge case packages.

Each bridge package mirrors its core counterpart — `core/cases/checkout/` maps to `bridge/cases/checkoutbridge/`.

## Bridge Struct

A case bridge holds its core case, shared infrastructure, and auth dependencies:

```go
type Bridge struct {
    checkout      *checkout.Case
    log           *slog.Logger
    rateLimiter   *ratelimiter.RateLimiter
    authenticator *authentication.Authenticator
    authorizer    *authorization.Authorizer
    jsonErrors    httpmid.ErrorRenderer
}

func New(
    log *slog.Logger,
    checkout *checkout.Case,
    rateLimiter *ratelimiter.RateLimiter,
    authenticator *authentication.Authenticator,
    authorizer *authorization.Authorizer,
) *Bridge {
    return &Bridge{
        checkout:      checkout,
        log:           log,
        rateLimiter:   rateLimiter,
        authenticator: authenticator,
        authorizer:    authorizer,
        jsonErrors:    httpmid.JSONErrors{},
    }
}
```

Not every case bridge needs all of these — include only what the case requires. A case that doesn't need authorization can omit the authorizer.

## Route Registration

Case bridge routes are mounted under `/api/v1/cases/` to avoid conflicts with generated CRUD routes:

```go
// app wiring
cases := api.Group("/cases")
checkoutBridge.AddHttpRoutes(cases)
// → /api/v1/cases/checkout/...
```

Inside the bridge, middleware is wired inline per-route:

```go
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
    group.POST("/checkout/start", b.httpStart,
        httpmid.MaxBodySize(httpmid.DefaultBodySize),
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.RateLimit(b.rateLimiter, b.log),
    )

    group.POST("/checkout/{checkout_id}/confirm", b.httpConfirm,
        httpmid.MaxBodySize(httpmid.DefaultBodySize),
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.RateLimit(b.rateLimiter, b.log),
        httpmid.AuthorizeParam(b.authorizer, b.log, b.jsonErrors, "checkout", "manage", "checkout_id"),
    )
}
```

Case bridges don't use `bridge.yml` — routes, middleware, and authorization are all defined in code.

## Handler Pattern

Case bridge handlers follow the same conventions as generated handlers but with case-specific error mapping:

```go
func (b *Bridge) httpStart(w http.ResponseWriter, r *http.Request) {
    req, err := web.DecodeJSON[StartCheckoutRequest](r)
    if err != nil {
        web.RespondJSONError(w, web.ErrValidation(err))
        return
    }

    userID := httpmid.GetSubjectID(r.Context())

    result, err := b.checkout.Start(r.Context(), checkout.StartInput{
        UserID: userID,
        Items:  req.Items,
    })
    if err != nil {
        switch {
        case errors.Is(err, checkout.ErrInsufficientStock):
            web.RespondJSONError(w, web.ErrConflict("insufficient stock"))
        case errors.Is(err, checkout.ErrEmptyCart):
            web.RespondJSONError(w, web.ErrBadRequest("cart is empty"))
        default:
            web.RespondJSONDomainError(w, err)
        }
        return
    }

    web.RespondJSON(w, http.StatusCreated, toCheckoutResponse(result))
}
```

Key patterns:
- **Decode and validate** with `web.DecodeJSON` (calls `Validate()` automatically)
- **Extract auth context** with `httpmid.GetSubjectID()`, `httpmid.GetUser()`, etc.
- **Map domain errors** to specific HTTP responses with `switch` / `errors.Is()`
- **Fall through** to `web.RespondJSONDomainError()` for errors that map to standard status codes via `sdk/errs` sentinels

## Request/Response Models

Models live in `model.go` with `Validate()` methods:

```go
type StartCheckoutRequest struct {
    Items []CheckoutItem `json:"items"`
}

func (r *StartCheckoutRequest) Validate() error {
    var errs validation.Errors
    errs.Add(validation.MinLength("items", len(r.Items), 1))
    return errs.Err()
}
```

Response types convert from core types to the API shape. Keep the conversion in helper functions:

```go
func toCheckoutResponse(c checkout.Checkout) CheckoutResponse {
    return CheckoutResponse{
        CheckoutID: c.CheckoutID,
        Status:     c.Status,
        Total:      c.Total,
    }
}
```

## Event Subscribers

Case bridges can include event subscribers for side effects like sending emails or push notifications. The pattern matches what the [authentication](./auth/authentication.md) and [invitations](./auth/invitations.md) bridges use:

```go
type Subscribers struct {
    emailer *emailer.Emailer
    log     *slog.Logger
    subs    []events.Subscription
}

func NewSubscribers(e *emailer.Emailer, log *slog.Logger) *Subscribers {
    return &Subscribers{emailer: e, log: log}
}

func (s *Subscribers) Register(bus events.Bus) error {
    sub, err := bus.Subscribe(checkout.EventTypeOrderConfirmed, events.TypedHandler(s.handleOrderConfirmed))
    if err != nil {
        return fmt.Errorf("subscribe to %s: %w", checkout.EventTypeOrderConfirmed, err)
    }
    s.subs = append(s.subs, sub)
    return nil
}
```

Subscribers live in the bridge rather than core because they handle delivery concerns (email templates, notification formatting) — the core case emits the event without knowing how it's delivered.

## Authorization

Authorization for case bridges is handled via middleware or in-handler checks, depending on the endpoint:

- **Static resource authorization** — use `AuthorizeParam` or `AuthorizeType` middleware when the resource type and ID are known from the URL
- **Dynamic resource authorization** — call `authorizer.Check()` in the handler when the resource type comes from request data (see the [invitations bridge](./auth/invitations.md#authorization-model) for this pattern)

By the time a core case method is called, the request has already been authorized. Cases never call the authorizer directly.

## Framework-Provided Case Bridges

Gopernicus ships with two case bridges under `bridge/auth/`:

| Package | Core Case | Purpose |
|---|---|---|
| [Authentication](./auth/authentication.md) | `core/auth/authentication` | Login, registration, sessions, OAuth, passwords |
| [Invitations](./auth/invitations.md) | `core/auth/invitations` | Invitation workflow (create, accept, decline, cancel) |

These follow the same patterns described above. They serve as reference implementations for how to bridge complex case logic to HTTP.

See also: [Core Cases](../core/cases.md) for the domain-side counterpart.
