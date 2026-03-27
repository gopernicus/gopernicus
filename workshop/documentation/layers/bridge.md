# Bridge Layer

The bridge layer sits between HTTP and the domain. It translates HTTP requests
into core repository and case calls, and core responses back into HTTP
responses. Bridges are the only layer that imports both `sdk/web` and core
packages. The bridge layer also provides HTTP middleware for authentication,
authorization, rate limiting, and observability.

---

## Generated Repository Bridges (`bridge/repositories/`)

For each repository entity, gopernicus generates a bridge package (e.g.,
`usersbridge/`) with these files:

| File | Generated? | Purpose |
|------|-----------|---------|
| `generated.go` | Always | HTTP handler methods on `*Bridge` (`httpList`, `httpGet`, `httpCreate`, `httpUpdate`, `httpDelete`), `addGeneratedRoutes()` (private), `OpenAPISpec`. |
| `bridge.yml` | Once (not overwritten) | Route definitions, ordered middleware arrays, auth schema (`auth_relations`, `auth_permissions`). |
| `bridge.go` | Once (not overwritten) | `Bridge` struct with ALL fields directly (repository, log, rateLimiter, authenticator, authorizer, jsonErrors). No embedding. |
| `routes.go` | Once (not overwritten) | `AddHttpRoutes()` (public) that calls `addGeneratedRoutes()` + custom routes. |
| `http.go` | Once | Custom HTTP handlers. |
| `fop.go` | Once | Query parameter parsing, filter construction, order-by parsing. |

**Flat struct pattern:** `Bridge` has all its fields directly -- no embedding
of a generated type:

```go
type Bridge struct {
    repository    *users.Repository
    log           *slog.Logger
    rateLimiter   *ratelimiter.RateLimiter
    authenticator *authentication.Authenticator
    authorizer    *authorization.Authorizer
    jsonErrors    httpmid.ErrorRenderer
}
```

**Route registration pattern:** `routes.go` has `AddHttpRoutes()` (public)
which calls `addGeneratedRoutes()` (private, from generated.go):

```go
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
    b.addGeneratedRoutes(group)
    // Custom routes below
}
```

**Generated handler pattern** (example for list):

1. Parse query parameters (limit, cursor, order, filters)
2. Prefilter: call `authorizer.LookupResources` to get authorized IDs
3. Call `repository.List` with filter, order, and page
4. Convert domain entities to API response types
5. Respond with `PageResponse[T]{Data, Pagination}`

**Response envelopes** are defined in `bridge/fop/response.go`:

```go
type RecordResponse[T any] struct {
    Record       T        `json:"record"`
    Relationship string   `json:"relationship,omitempty"`
    Permissions  []string `json:"permissions,omitempty"`
}

type PageResponse[T any] struct {
    Data       []T             `json:"data"`
    Pagination fop.Pagination  `json:"pagination"`
}
```

---

## bridge.yml Configuration

Each bridge package has a `bridge.yml` that declares routes, middleware, and
auth schema. This replaces the old pattern of putting `@http:json`,
`@authenticated`, `@authorize`, and `@auth.*` annotations in `queries.sql`:

```yaml
routes:
  - func: List
    path: /users
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read

  - func: Get
    path: /users/{user_id}
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: read
          param: user_id

auth_relations:
  - "owner(user, service_account)"

auth_permissions:
  - "list(owner)"
  - "read(owner)"
  - "update(owner)"
  - "delete(owner)"
```

**Middleware array:** Routes have explicit ordered middleware arrays. Known types:
`authenticate`, `authorize`, `rate_limit`, `max_body_size`, `unique_to_id`,
`with_permissions`. Custom middleware can be specified via raw Go strings.

---

## Composite Bridges

Each domain has a composite that groups all entity bridges:

```go
// bridge/repositories/authreposbridge/generated_composite.go
type Bridges struct {
    User    *usersbridge.Bridge
    Session *sessionsbridge.Bridge
    // ...
}
```

The `Bridges` struct is defined directly in `generated_composite.go` -- no
`GeneratedBridges` type or type alias. The composite constructs all entity
bridges and provides `AddHttpRoutes(group)` and `OpenAPISpec()` methods.

Auth schema files (`generated_authschema.go`, `authschema.go`) also live in the
bridge composite directory.

---

## Case Bridges (`bridge/auth/`)

Hand-written bridges for business-logic cases that go beyond CRUD. These are
not generated -- they are framework-provided (authentication, invitations) or
written by the user.

**Authentication bridge** (`bridge/auth/authentication/`):
- `Bridge` struct wraps `*authentication.Authenticator`
- `http.go` registers routes: register, login, refresh, verify, password
  reset, OAuth flows
- `subscribers.go` subscribes to auth events (verification codes, password
  resets) and sends emails via the emailer
- `templates/` contains HTML and text email templates

**Invitations bridge** (`bridge/auth/invitations/`):
- `Bridge` wraps `*invitations.Inviter`
- Routes: create, accept, decline, cancel, resend, list

---

## Middleware (`bridge/protocol/httpmid/`)

HTTP middleware and request context helpers. All middleware returns standard
`func(http.Handler) http.Handler` signatures.

### Authentication Middleware

```go
httpmid.Authenticate(authenticator, log, jsonErrors)           // JWT + API key
httpmid.Authenticate(authenticator, log, jsonErrors, UserOnly())        // JWT only, fast
httpmid.Authenticate(authenticator, log, jsonErrors, WithUserSession()) // JWT + DB session validation
httpmid.Authenticate(authenticator, log, jsonErrors, ServiceAccountOnly()) // API key only
```

Sets context values: `Subject` ("user:{id}" or "service_account:{id}"),
`SubjectType`, and optionally `User`, `Session`, `SessionID`.

### Authorization Middleware

```go
httpmid.AuthorizeParam(authorizer, log, jsonErrors, "post", "read", "id")  // check permission on path param
httpmid.AuthorizeType(authorizer, log, jsonErrors, "user", "list")         // check type-level permission
httpmid.RequirePlatformAdmin(authorizer, log, jsonErrors)                  // require platform admin
```

### UniqueToID Middleware

Resolves a unique field (e.g., slug) to a resource ID. Configured via
`unique_to_id` in the `bridge.yml` middleware array.

### Other Middleware

| Middleware | Purpose |
|-----------|---------|
| `Logger(log)` | Structured request logging with duration, status, method, path. |
| `Panics(log)` | Recovers panics and returns 500. |
| `TrustProxies(depth)` | Resolves client IP from `X-Forwarded-For`. |
| `ClientInfo()` | Injects `authentication.ClientInfo` into context for security events. |
| `BodyLimit(maxBytes)` | Limits request body size. |
| `RateLimit(limiter, ...)` | Per-route rate limiting. |
| `Telemetry(tracer)` | OTEL span creation for requests. |
| `ExtractTenantID(...)` | Extracts tenant ID from path/header into context. |

### Context Helpers

```go
subject := httpmid.GetSubject(r.Context())        // "user:abc123"
userID  := httpmid.GetSubjectID(r.Context())       // "abc123"
info    := httpmid.GetSubjectInfo(r.Context())     // SubjectInfo{Type: "user", ID: "abc123"}
user    := httpmid.GetUser(r.Context())            // *authentication.User (with WithUserSession)
ip      := httpmid.GetClientIP(r.Context())        // resolved client IP
rel, ok := httpmid.GetRelationship(r.Context())    // RelationshipInfo from Authorize
tenantID := httpmid.GetTenantID(r.Context())       // tenant ID from middleware
```

### Error Rendering

`ErrorRenderer` interface controls how auth/authz errors are presented.
`JSONErrors{}` is the default for API routes. Implement custom renderers for
HTML routes.

---

## Post-filter Authorization (`bridge/fop/`)

When prefilter (LookupResources) is not practical, `PostfilterLoop` provides
post-query authorization filtering:

```go
records, pagination, err := fop.PostfilterLoop(
    ctx, authorizer, subject, "read", "user",
    func(rec User) string { return rec.UserID },
    func(ctx context.Context, p fop.PageStringCursor) ([]User, fop.Pagination, error) {
        return repo.List(ctx, filter, orderBy, p)
    },
    page,
)
```

It fetches pages with 2x overfetch, batch-checks authorization via
`FilterAuthorized`, and accumulates until the target limit is reached or data
is exhausted.

---

## Related

- [SDK Layer](sdk.md) -- `web` package that bridges build on
- [Core Layer](core.md) -- repositories and cases that bridges call
- [Infrastructure Layer](infrastructure.md) -- services used by middleware
- [App Layer](app.md) -- wires bridges and registers routes
