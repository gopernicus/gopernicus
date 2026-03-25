# Core Layer

The core layer is the domain heart of a Gopernicus application. It contains
repositories (generated data access), authentication and authorization logic,
business-logic cases, and domain events. The core depends only on the SDK and
infrastructure interfaces -- never on HTTP, database drivers, or other external
packages.

**Rule: core code accepts interfaces, returns structs.**

---

## Repositories (`core/repositories/`)

Repositories are organized by domain: `auth/`, `rebac/`, `events/`, `tenancy/`,
and any user-defined domains. Each entity gets its own package (e.g.,
`auth/users/`) with this structure:

| File | Generated? | Purpose |
|------|-----------|---------|
| `generated.go` | Always | Entity struct (`User`), create/update structs (`CreateUser`, `UpdateUser`), error sentinels, `OrderByFields`, Repository methods on `*Repository`. |
| `repository.go` | Once (not overwritten) | `Storer` interface (with `// gopernicus:start` / `// gopernicus:end` markers), `Repository` struct with fields, `NewRepository`. |
| `fop.go` | Once | Filter types and order-by field maps for list queries. |
| `cache.go` / `generated_cache.go` | Mixed | Cache key patterns and generated cache-aside wrapper. |
| `queries.sql` | Once | SQL queries parsed by gopernicus to generate the store layer (data annotations only). |
| `{entity}pgx/` | Always | Generated pgx store implementation (`store.go` + `generated.go`). |

Note: there is no `model.go`. Types are defined directly in `generated.go`
with final names (`User`, `CreateUser`, `UpdateUser`) -- no `Generated` prefix.

**Extending repositories:** Add custom methods above the `// gopernicus:start`
marker in the `Storer` interface in `repository.go` and implement them in
`store.go` inside the pgx subdirectory. Override any generated Repository
method by defining it in `repository.go` with the same signature.

```go
// repository.go -- created once, never overwritten
type Storer interface {
    // Custom methods above markers
    GetByEmail(ctx context.Context, email string) (User, error)

    // gopernicus:start
    List(ctx context.Context, ...) ([]User, error)
    Get(ctx context.Context, userID string) (User, error)
    // ... generated methods
    // gopernicus:end
}

type Repository struct {
    store      Storer
    generateID func() string
    bus        events.Bus
}
```

**Generated errors** wrap `sdk/errs` sentinels with domain context:

```go
var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)
```

**Composite repositories** group all entity repositories in a domain into a
single struct for convenient dependency injection. Each domain has
`generated_composite.go` (regenerated) with a `Repositories` struct directly --
no type aliases or `GeneratedRepositories` pattern.

---

## Authentication (`core/auth/authentication/`)

The `authentication` package owns all security-critical auth flows. It is
framework-provided code that user projects import and configure -- not generated.

**Key type: `Authenticator`** -- constructed via `NewAuthenticator()` with
dependency injection of repositories, a password hasher, a JWT signer, an event
bus, and config.

**Flows provided:**
- Registration (email + password, returns verification code)
- Login (email + password, returns JWT access + refresh tokens)
- Token refresh with rotation and reuse detection
- Session validation (JWT-only fast path and full DB session check)
- Email verification (6-digit codes with brute-force protection)
- Password reset (opaque token via email)
- OAuth (initiate flow, handle callback, link/unlink accounts)
- API key authentication

**Repository interfaces** define what the Authenticator needs -- the user's
generated repositories satisfy these via thin adapter (satisfier) types in
`core/auth/authentication/satisfiers/`:

```go
type UserRepository interface {
    Get(ctx context.Context, id string) (User, error)
    GetByEmail(ctx context.Context, email string) (User, error)
    Create(ctx context.Context, input CreateUserInput) (User, error)
    SetEmailVerified(ctx context.Context, id string) error
    SetLastLogin(ctx context.Context, id string, at time.Time) error
}
```

**Crypto interfaces** (`PasswordHasher`, `JWTSigner`, `TokenEncrypter`) are
structurally compatible with `infrastructure/cryptids` adapters -- no import
required.

**Events emitted:** `EventTypeVerificationCodeRequested`,
`EventTypePasswordResetRequested`, `EventTypeOAuthLinkVerificationRequested`.
Subscribers in the bridge layer handle email rendering and sending.

---

## Authorization (`core/auth/authorization/`)

Relationship-based access control (ReBAC). The `Authorizer` evaluates
permission checks against a `Schema` that maps relations to permissions.

**Check evaluation order:** platform admin bypass -> self-access -> schema
rules (direct relations + through-relation traversal with cycle detection).

```go
result, _ := authorizer.Check(ctx, authorization.CheckRequest{
    Subject:    authorization.Subject{Type: "user", ID: userID},
    Permission: "edit",
    Resource:   authorization.Resource{Type: "project", ID: projectID},
})
```

**Schema DSL:** `Direct("owner")`, `Through("org", "admin")`, `AnyOf(...)`.

**LookupResources** returns all resource IDs a subject can access -- powers the
prefilter pattern for list endpoints.

**Storer interface** is implemented by a pgx-backed store via a satisfier
adapter in `core/auth/authorization/satisfiers/`.

---

## Invitations (`core/auth/invitations/`)

Generic resource invitation business logic. The `Case` type orchestrates the
flow: create invitation -> emit event with plaintext token -> invitee accepts
-> ReBAC relationship created. Supports auto-accept for known users and
`ResolveOnRegistration` for pending invitations when a user verifies their
email.

---

## Events (`core/events/`)

Domain event satisfiers. The `OutboxWriterSatisfier` in
`core/events/satisfiers/` bridges the generated `eventoutbox` repository to the
infrastructure `outbox.OutboxWriter` interface, enabling transactional event
persistence.

---

## Testing (`core/testing/`)

`core/testing/fixtures` provides generated test fixture factories for all
domain entities.

---

## What Goes in Core vs Other Layers

| Belongs in Core | Does NOT belong in Core |
|-----------------|------------------------|
| Domain entity types | HTTP handlers or routes |
| Repository interfaces and implementations | Database driver imports (pgx is in the pgx subdir) |
| Business logic cases | Email rendering or sending |
| Domain error sentinels | Cache backend selection |
| Domain events | Middleware |
| -- | Authorization schemas (moved to bridge layer in v2) |
| -- | Configuration loading |

---

## Related

- [SDK Layer](sdk.md) -- primitives that core depends on
- [Infrastructure Layer](infrastructure.md) -- adapters that satisfy core interfaces
- [Bridge Layer](bridge.md) -- HTTP handlers that call core
- [App Layer](app.md) -- wires core with infrastructure
