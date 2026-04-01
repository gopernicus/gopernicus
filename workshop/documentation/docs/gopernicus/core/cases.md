---
sidebar_position: 2
title: Cases
---

# Core — Cases

Cases are hand-written business logic that orchestrates multiple repositories, emits domain events, and enforces rules that go beyond single-entity CRUD. They are never generated — you write them, you own them.

User-defined cases live under `core/cases/`. The framework also ships its own cases under `core/auth/` (authentication, authorization, invitations) — these follow the same patterns but are framework-provided rather than user-written.

## When to Use a Case

Use a case when your operation:

- **Orchestrates multiple repositories** — e.g., creating a project also provisions default permissions and emits a notification
- **Implements a multi-step workflow** — e.g., invitations (create → email → accept → provision)
- **Emits domain events** — the operation triggers side effects handled by other parts of the system
- **Encodes business rules** — logic that doesn't belong in a single repository and shouldn't live in the bridge layer

If an operation is a simple create, read, update, or delete on a single entity, the generated repository handles it — no case needed.

## Directory Structure

Cases follow a parallel structure across core and bridge:

```
core/cases/
└── checkout/
    ├── case.go         # Case struct, constructor, business logic
    ├── errors.go       # Domain error sentinels
    └── events.go       # Domain event types

bridge/cases/
└── checkoutbridge/
    ├── bridge.go       # Bridge struct, constructor
    ├── routes.go       # AddHttpRoutes
    ├── http.go         # HTTP handlers
    └── model.go        # Request/response models
```

Scaffold both with `gopernicus new case <name>`.

## Anatomy of a Case

A case is a struct with injected dependencies and methods that implement the business logic:

```go
// core/cases/checkout/case.go

type Case struct {
    orders     *orders.Repository
    inventory  *inventory.Repository
    payments   paymentGateway
    bus        events.Bus
}

type Option func(*Case)

func WithEventBus(bus events.Bus) Option {
    return func(c *Case) { c.bus = bus }
}

func NewCase(orders *orders.Repository, inventory *inventory.Repository, payments paymentGateway, opts ...Option) *Case {
    c := &Case{
        orders:    orders,
        inventory: inventory,
        payments:  payments,
    }
    for _, opt := range opts {
        opt(c)
    }
    return c
}
```

Cases accept their dependencies through constructor injection. Required dependencies are constructor parameters; optional ones use the functional options pattern.

## Dependency Interfaces

When a case needs a capability from another layer, it defines a narrow interface for exactly what it needs:

```go
// core/cases/checkout/case.go

type paymentGateway interface {
    Charge(ctx context.Context, amount int, currency string) (ChargeResult, error)
}
```

This follows the Go convention of defining interfaces at the point of use, not at the point of implementation. The concrete implementation (Stripe, mock, etc.) is wired in the app layer.

## Authorization

Cases do not perform authorization checks. Authorization happens at the [bridge layer via middleware](../design-philosophy.md#where-authorization-lives) — by the time a case method is called, the request has already been authorized. If a case needs authorization-aware data (e.g., a filtered list of resources the user can see), the bridge passes pre-filtered input rather than the case calling the authorizer directly.

## Satisfiers

When a case defines its own repository interface — separate from the generated repository types — a **satisfier** adapter bridges the gap:

```go
// core/auth/authentication/satisfiers/users.go

type UserSatisfier struct {
    repo usersRepo
}

func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
    u, err := s.repo.GetByEmail(ctx, email)
    // ... map generated User → authentication.User
    return authentication.User{
        UserID:        u.UserID,
        Email:         u.Email,
        EmailVerified: u.EmailVerified,
        Active:        u.RecordState == "active",
    }, err
}
```

Satisfiers live in a `satisfiers/` subdirectory within the case package. The app layer wires them together at startup. This pattern keeps cases decoupled from generated types — the case defines what it needs, the satisfier provides it.

Not every case needs satisfiers. If your case works directly with generated repository types (which is common for user-defined cases), you can inject the repositories directly.

## Route Prefix

Case bridge routes are mounted under `/api/v1/cases/` to avoid conflicts with generated CRUD routes:

```go
// app wiring
cases := api.Group("/cases")
checkoutBridge.AddHttpRoutes(cases)
// → /api/v1/cases/checkout/...
```

## Framework-Provided Cases

The `core/auth/` packages are framework-provided cases that follow the same patterns:

| Package | Purpose |
|---|---|
| [Authentication](./auth/authentication.md) | Login, registration, sessions, passwords, OAuth, API keys |
| [Authorization](./auth/authorization.md) | ReBAC permission checking, resource lookup, membership |
| [Invitations](./auth/invitations.md) | Resource invitation workflow |

These are more complex than typical user-defined cases because they handle security-critical flows, but the structural patterns are identical: dependency injection, event emission, satisfiers for repository decoupling.

See also: [Bridge Cases](../bridge/cases.md) for the HTTP-side counterpart.
