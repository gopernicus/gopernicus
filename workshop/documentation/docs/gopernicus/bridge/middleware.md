---
sidebar_position: 5
title: Middleware
---

# Bridge — Middleware

HTTP middleware lives in `bridge/transit/httpmid/`. All middleware uses the standard `func(http.Handler) http.Handler` signature and composes with any Go HTTP stack.

Middleware is applied per-route (via `bridge.yml` or inline in `AddHttpRoutes`) or globally (on a route group in the app layer).

## Authenticate

**File:** `authenticate.go`

Validates the request's authentication token and sets subject context. Tokens are extracted from the `Authorization: Bearer` header first, falling back to the session cookie.

```go
httpmid.Authenticate(authenticator, log, jsonErrs)
httpmid.Authenticate(authenticator, log, jsonErrs, httpmid.UserOnly())
httpmid.Authenticate(authenticator, log, jsonErrs, httpmid.WithUserSession())
httpmid.Authenticate(authenticator, log, jsonErrs, httpmid.ServiceAccountOnly())
```

### Options

| Option | Accepts | Validation | Context Set |
|---|---|---|---|
| *(default)* | JWT or API key | JWT signature / API key lookup | Subject, SubjectType |
| `UserOnly()` | JWT only | Signature only (fast, no DB) | Subject, SubjectType |
| `WithUserSession()` | JWT only | DB session validation | Subject, SubjectType, SessionID, User, Session |
| `ServiceAccountOnly()` | API key only | API key hash lookup | Subject, SubjectType |

### Token Detection

Tokens are auto-detected: JWTs have three base64url parts separated by dots; anything else is treated as an API key.

### bridge.yml Syntax

```yaml
middleware:
  - authenticate: any                    # default — JWT or API key
  - authenticate: user                   # UserOnly()
  - authenticate: user_session           # WithUserSession()
  - authenticate: service_account        # ServiceAccountOnly()
```

`WithUserSession()` requires full session validation (DB lookup), so use it only on routes that need the full user/session context (e.g., `/me`, password change).

## Authorize

**File:** `authorize.go`

Checks permission via the authorizer. Must run after `Authenticate`.

### Variants

```go
// Check permission on a specific resource via path param
httpmid.AuthorizeParam(authorizer, log, jsonErrs, "user", "read", "user_id")

// Check permission on a resource type (uses "*" as resource ID)
httpmid.AuthorizeType(authorizer, log, jsonErrs, "user", "list")

// Check platform:main#admin
httpmid.RequirePlatformAdmin(authorizer, log, jsonErrs)

// Custom resource ID extraction
httpmid.Authorize(authorizer, log, jsonErrs, "post", "read",
    func(r *http.Request) string { return r.PathValue("id") })
```

### Relationship Tracking

When the authorization check succeeds, the middleware stores which relation granted access in the context. Handlers can retrieve it via `GetRelationship()` — useful for varying behavior based on role (e.g., owners see more fields than viewers).

### bridge.yml Syntax

```yaml
middleware:
  # Check permission on resource from path param
  - authorize:
      permission: read
      param: user_id

  # Prefilter — resolve authorized IDs before querying (for list endpoints)
  - authorize:
      pattern: prefilter
      permission: read
```

## Rate Limit

**File:** `rate_limit.go`

Per-route rate limiting with auth-aware keying.

```go
httpmid.RateLimit(limiter, log)
httpmid.RateLimit(limiter, log, httpmid.WithLimit(ratelimiter.PerMinute(60)))
httpmid.RateLimit(limiter, log, httpmid.WithIPKey())
httpmid.RateLimit(limiter, log, httpmid.WithKeyPrefix("login"))
```

### Default Behavior

Rate limit keys are auth-aware: `user:{id}` or `service_account:{id}` when authenticated, `ip:{client_ip}` when not. This prevents one user from exhausting limits for another behind a shared IP.

### Options

| Option | Purpose |
|---|---|
| `WithLimit(limit)` | Explicit limit (bypasses resolver) |
| `WithKeyFunc(fn)` | Custom key extraction |
| `WithIPKey()` | Force IP-based keying |
| `WithKeyPrefix(prefix)` | Namespace keys (e.g., `"login:"`) |
| `WithFailOpen()` | Allow requests on limiter errors (default: fail-closed) |
| `WithSkipFunc(fn)` | Skip limiting for certain requests |

### Response Headers

Every rate-limited response includes:

```
X-RateLimit-Limit: {requests}
X-RateLimit-Remaining: {count}
X-RateLimit-Reset: {unix_timestamp}
Retry-After: {seconds}           (only on 429)
```

### Nil Limiter

If the limiter is `nil`, the middleware is a no-op. This allows disabling rate limiting in development without changing wiring.

## MaxBodySize

**File:** `body_limit.go`

Limits request body size. Returns 413 Payload Too Large if exceeded.

```go
httpmid.MaxBodySize(1 << 20) // 1 MB
```

Constants for common sizes:

| Constant | Size |
|---|---|
| `DefaultBodySize` | 1 MB |
| `SmallBodySize` | 64 KB |
| `LargeBodySize` | 50 MB |
| `ExtraLargeSize` | 200 MB |

## Logger

**File:** `logger.go`

Structured request logging with timing and status code.

```go
httpmid.Logger(log)
```

Log levels: `INFO` for 2xx/3xx, `WARN` for 4xx, `ERROR` for 5xx. Includes method, path, status, elapsed time, and client IP (if set by `TrustProxies`).

## Panics

**File:** `panics.go`

Recovers from panics and returns a 500 JSON error. Logs the panic value and stack trace.

```go
httpmid.Panics(log)
```

This catches intentional panics from `MustGetSubject()` and `MustGetTenantID()`, converting them to error responses instead of crashing the server.

## TrustProxies

**File:** `trust_proxies.go`

Resolves the real client IP from proxy headers using the rightmost-minus-N algorithm on `X-Forwarded-For`.

```go
httpmid.TrustProxies(1)  // one proxy (e.g., load balancer)
httpmid.TrustProxies(2)  // two proxies (e.g., CDN + load balancer)
httpmid.TrustProxies(0)  // trust only RemoteAddr
```

Falls back to `X-Real-IP` if `X-Forwarded-For` is absent. The resolved IP is stored in context and retrieved via `GetClientIP()`.

Wire this before `ClientInfo`, `Logger`, and `RateLimit` — they all read the client IP from context.

## ClientInfo

**File:** `client_info.go`

Injects `authentication.ClientInfo` (IP address, user agent) into the request context for security event logging.

```go
httpmid.ClientInfo()
```

Uses `GetClientIP()` if `TrustProxies` has already run, otherwise falls back to `RemoteAddr`. Wire after `TrustProxies`.

## TelemetryMiddleware

**File:** `telemetry.go`

Creates OpenTelemetry server spans for each request.

```go
httpmid.TelemetryMiddleware(tracer)
```

Span names use the Go 1.22+ route pattern when available (e.g., `HTTP IN GET /users/{user_id}`) for low cardinality. Records method, host, user agent, status code, and response size.

## Tenant

**File:** `tenant.go`

Multi-tenancy context injection.

```go
// Extract tenant ID from URL path param
httpmid.ExtractTenantID(log, "tenant_id")

// Fixed tenant ID for single-tenant deployments
httpmid.InjectDefaultTenant(log)
```

The tenant ID is stored in context and retrieved via `GetTenantID()` or `MustGetTenantID()`.

## UniqueToID

**File:** `unique_to_id.go`

Resolves unique fields (e.g., slug) to a resource ID before the request reaches the handler. The resolved ID is injected via `SetPathValue`, making it available to subsequent middleware (like `AuthorizeParam`) and handlers.

```go
// Simple — globally unique slug
httpmid.UniqueToID(
    func(ctx context.Context, p map[string]string) (string, error) {
        return store.GetIDBySlug(ctx, p["slug"])
    },
    "tenant_id",  // path value to write
    "slug",       // path value to read
)

// Composite — slug unique within a scope
httpmid.UniqueToID(
    func(ctx context.Context, p map[string]string) (string, error) {
        return store.GetIDBySlug(ctx, p["slug"], p["tenant_id"])
    },
    "group_id",
    "slug", "tenant_id",
)
```

Returns 404 if the resolver returns `ErrUniqueNotFound`, 500 for other errors.

### bridge.yml Syntax

```yaml
middleware:
  - unique_to_id:
      resolver: GetIDBySlug
      param: slug
```

## Error Rendering

**File:** `errors.go`

Middleware that writes error responses accepts an `ErrorRenderer` interface, making the response format configurable per-route:

```go
type ErrorRenderer interface {
    RenderError(w http.ResponseWriter, r *http.Request, kind ErrorKind)
}
```

Error kinds: `ErrKindUnauthenticated`, `ErrKindBadRequest`, `ErrKindInternal`, `ErrKindForbidden`.

`JSONErrors{}` is the built-in renderer for API routes. For HTML routes, implement your own — for example, redirecting unauthenticated users to `/login` or rendering a template for forbidden errors.

## Context Helpers

**File:** `httpmid.go`

Context helpers read values set by middleware. All getters return zero values when the key is not set.

### Subject (set by Authenticate)

| Function | Returns |
|---|---|
| `GetSubject(ctx)` | Full subject string (`"user:abc123"`) |
| `GetSubjectID(ctx)` | ID portion only (`"abc123"`) |
| `GetSubjectInfo(ctx)` | `SubjectInfo{Type, ID}` |
| `MustGetSubject(ctx)` | Subject or panic (caught by `Panics` middleware) |
| `IsUserAuth(ctx)` | `true` if subject is a user |
| `IsServiceAccountAuth(ctx)` | `true` if subject is a service account |

### Session & User (set by Authenticate with WithUserSession)

| Function | Returns |
|---|---|
| `GetSessionID(ctx)` | Session ID |
| `GetUser(ctx)` | `*authentication.User` |
| `GetSession(ctx)` | `*authentication.Session` |

### Other

| Function | Set By | Returns |
|---|---|---|
| `GetClientIP(ctx)` | `TrustProxies` | Resolved client IP |
| `GetRelationship(ctx)` | `Authorize` | `RelationshipInfo{Relation, ResourceType, ResourceID}` |
| `GetTenantID(ctx)` | `ExtractTenantID` / `InjectDefaultTenant` | Tenant ID |
| `MustGetTenantID(ctx)` | `ExtractTenantID` / `InjectDefaultTenant` | Tenant ID or panic |

## Typical Global Stack

```go
webHandler.Use(
    httpmid.Panics(log),
    httpmid.TelemetryMiddleware(provider.Tracer()),
    httpmid.TrustProxies(1),
    httpmid.ClientInfo(),
    httpmid.Logger(log),
)
```

Order matters:
- `Panics` is outermost to catch panics from any middleware
- `TelemetryMiddleware` wraps everything in a trace span
- `TrustProxies` must run before middleware that reads the client IP
- `ClientInfo` injects IP + user agent into auth context (needs `TrustProxies` first)
- `Logger` is innermost so it can log the resolved client IP
