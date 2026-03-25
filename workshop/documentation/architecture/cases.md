# Use Case Pattern

When and how to use cases for domain logic beyond simple CRUD.

## When to Use Cases vs Generated CRUD

Generated repositories handle straightforward data access: create, read, update, delete, and list with filtering and pagination. Use a **case** when the operation involves:

- **Multi-step workflows** -- e.g., creating an invitation involves token generation, user lookup, conditional direct-add vs pending-invite, relationship creation, and event emission.
- **Cross-repository coordination** -- operations that touch multiple repositories or external services in a single logical unit.
- **Domain rules and validation** -- business logic that goes beyond what the database enforces (status transitions, duplicate checks, authorization decisions).
- **Event-driven side effects** -- operations that need to emit domain events for downstream consumers (email sending, audit logging).

If the operation is a direct data read/write with no special logic, rely on the generated repository methods and their bridge-layer wiring.

## Directory Layout

Cases live in two places: the core layer (business logic) and the bridge layer (HTTP/protocol adaptation).

**Foundational cases** are part of the framework itself:

```
core/auth/{name}/          # Business logic (authentication, authorization, invitations)
bridge/auth/{name}/        # HTTP bridge
```

**Domain-specific cases** are for application-level features:

```
core/cases/{name}/         # Business logic
bridge/cases/{name}/       # HTTP bridge
```

For example, the invitations case:

```
core/auth/invitations/
    case.go               # Case struct, dependencies, operations (Create, Accept, Decline, Cancel)
    errors.go             # Domain-specific sentinel errors
    events.go             # Domain event types (InvitationSentEvent, MemberAddedEvent)

bridge/auth/invitations/
    bridge.go             # Bridge struct, constructor, options
    http.go               # HTTP route registration and handler methods
```

## The Case Struct Pattern

A case is a struct that holds its dependencies and exposes operations as methods. Each operation defines its own `Input` and `Result` types.

```go
// Case provides invitation business logic.
type Case struct {
    invitations *invitationsrepo.Repository
    authorizer  *authorization.Authorizer
    hasher      *cryptids.SHA256Hasher
    bus         events.Bus
    lookupUser  UserLookup
    checkMember MemberCheck
}
```

Dependencies fall into two categories:

- **Required dependencies** are constructor parameters (repositories, authorizer, event bus).
- **Optional dependencies** use the functional options pattern (`Option func(*Case)`).

```go
type Option func(*Case)

func WithUserLookup(fn UserLookup) Option {
    return func(c *Case) { c.lookupUser = fn }
}

func WithMemberCheck(fn MemberCheck) Option {
    return func(c *Case) { c.checkMember = fn }
}

func New(
    invitationRepo *invitationsrepo.Repository,
    authorizer *authorization.Authorizer,
    bus events.Bus,
    opts ...Option,
) *Case {
    c := &Case{
        invitations: invitationRepo,
        authorizer:  authorizer,
        hasher:      cryptids.NewSHA256Hasher(),
        bus:         bus,
    }
    for _, opt := range opts {
        opt(c)
    }
    return c
}
```

Dependencies that are function types (like `UserLookup` and `MemberCheck`) follow the "accept interfaces" principle -- they decouple the case from concrete implementations:

```go
type UserLookup func(ctx context.Context, email string) (subjectType, subjectID string, err error)
type MemberCheck func(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) (bool, error)
```

## Operations with Input/Result Types

Each operation on a case defines its own typed input and result structs. This keeps method signatures clean, makes the API self-documenting, and avoids parameter sprawl.

```go
type CreateInput struct {
    ResourceType   string
    ResourceID     string
    Relation       string
    Identifier     string
    IdentifierType string
    InvitedBy      string
    AutoAccept     bool
}

type CreateResult struct {
    DirectlyAdded bool
    Invitation    *invitationsrepo.Invitation
}

func (c *Case) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
    // Multi-step business logic:
    // 1. Resolve user by identifier
    // 2. If AutoAccept + known user -> direct add
    // 3. Otherwise -> create pending invitation with token
    // 4. Emit appropriate event
}
```

Constants related to the domain live at the top of `case.go`:

```go
const tokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
const tokenLength = 32
const InvitationExpiryDays = 7

const (
    StatusPending   = "pending"
    StatusAccepted  = "accepted"
    StatusDeclined  = "declined"
    StatusCancelled = "cancelled"
    StatusExpired   = "expired"
)
```

## Domain Errors

Cases define domain-specific errors in `errors.go`. Each error wraps a sentinel from `sdk/errs` so the bridge layer can map them to appropriate HTTP status codes:

```go
var (
    ErrInvitationNotFound      = fmt.Errorf("invitation not found: %w", errs.ErrNotFound)
    ErrInvitationExpired       = fmt.Errorf("invitation expired: %w", errs.ErrConflict)
    ErrInvitationAlreadyUsed   = fmt.Errorf("invitation already used: %w", errs.ErrConflict)
    ErrInvitationCancelled     = fmt.Errorf("invitation cancelled: %w", errs.ErrConflict)
    ErrInvitationInvalidStatus = fmt.Errorf("invitation invalid status: %w", errs.ErrConflict)
    ErrIdentifierMismatch      = fmt.Errorf("identifier does not match invitation: %w", errs.ErrForbidden)
    ErrAlreadyMember           = fmt.Errorf("already a member: %w", errs.ErrAlreadyExists)
    ErrPendingInvitationExists = fmt.Errorf("pending invitation already exists: %w", errs.ErrAlreadyExists)
)
```

The pattern is: descriptive message + `%w` wrapping a `sdk/errs` sentinel (`ErrNotFound`, `ErrConflict`, `ErrForbidden`, `ErrAlreadyExists`, etc.). The bridge layer uses `errors.Is` against these sentinels to determine HTTP status codes without leaking domain details.

## Domain Events

Cases emit events for side effects that should happen asynchronously or in other bounded contexts. Events are defined in `events.go` and embed `events.BaseEvent`:

```go
type InvitationSentEvent struct {
    events.BaseEvent
    InvitationID string `json:"invitation_id"`
    ResourceType string `json:"resource_type"`
    ResourceID   string `json:"resource_id"`
    Relation     string `json:"relation"`
    Identifier   string `json:"identifier"`
    Token        string `json:"token"`
    InvitedBy    string `json:"invited_by"`
}

func (e InvitationSentEvent) Type() string { return "invitation.sent" }
```

Events are emitted via the `events.Bus` dependency:

```go
c.bus.Emit(ctx, InvitationSentEvent{
    BaseEvent: events.NewBaseEvent("invitation.sent"),
    // ...fields...
})
```

Subscribers handle delivery concerns (sending emails, writing audit logs) outside the case.

## The Bridge Pattern

The bridge layer adapts a case to a transport protocol (HTTP). It lives in a parallel directory structure and follows a consistent pattern.

The `Bridge` struct holds the case plus protocol-specific dependencies:

```go
type Bridge struct {
    log           *slog.Logger
    invitations   *invitationscore.Inviter
    authorizer    *authorization.Authorizer
    authenticator *authentication.Authenticator
    rateLimiter   *ratelimiter.RateLimiter
    jsonErrors    httpmid.ErrorRenderer
    htmlErrors    httpmid.ErrorRenderer
}
```

Optional bridge dependencies use `BridgeOption`:

```go
type BridgeOption func(*Bridge)

func WithJSONErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
    return func(b *Bridge) { b.jsonErrors = r }
}
```

Routes are registered via `AddHttpRoutes` on a `*web.RouteGroup`. Each route applies middleware (authentication, authorization, rate limiting) and delegates to a handler method:

```go
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
    group.POST("/{resource_type}/{resource_id}", b.httpCreate,
        httpmid.MaxBodySize(httpmid.DefaultBodySize),
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.RateLimit(b.rateLimiter, b.log),
    )
    group.GET("/{resource_type}/{resource_id}", b.httpListByResource,
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.RateLimit(b.rateLimiter, b.log),
    )
    group.POST("/{invitation_id}/cancel", b.httpCancel,
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.AuthorizeParam(b.authorizer, b.log, b.jsonErrors, "invitation", "manage", "invitation_id"),
    )
    // ...
}
```

## Route Convention

Case routes are mounted under `/cases/{kebab-name}/` for domain-specific cases. Foundational cases (auth) mount under their own namespace (e.g., `/invitations/`). The bridge's `AddHttpRoutes` method receives a `RouteGroup` already scoped to this prefix.

## Scaffolding

To create a new case:

```
gopernicus new case <name>
```

This generates the directory structure with starter files for both the core case and the bridge.

## Foundational vs Domain-Specific Cases

**Foundational cases** live under `core/auth/` and handle cross-cutting concerns that most applications need:

- `core/auth/authentication/` -- session management, token verification
- `core/auth/authorization/` -- permission checks, relationship management
- `core/auth/invitations/` -- resource invitation workflows

Their bridges live under `bridge/auth/`.

**Domain-specific cases** live under `core/cases/` and handle application-level business logic unique to your domain. Their bridges live under `bridge/cases/`. The separation ensures foundational logic remains stable and reusable across projects.

## Related

- [Architecture Overview](overview.md)
- [Design Philosophy](design-philosophy.md)
- [Events](events.md)
- [Repositories](repositories.md)
- [Error Handling](error-handling.md)
