# Auth Middleware Reference

The `httpmid` package (`bridge/protocol/httpmid/`) provides HTTP middleware for authentication and authorization enforcement. All middleware functions return standard `func(http.Handler) http.Handler` wrappers and compose with any Go HTTP stack.

## Authenticate

`httpmid.Authenticate` validates the `Authorization: Bearer <token>` header and sets the authenticated subject in the request context.

```go
jsonErrs := httpmid.JSONErrors{}

// Default -- accept both JWT and API key
mux.Handle("GET /api/posts", httpmid.Authenticate(auth, log, jsonErrs)(handler))

// User JWT only, signature validation only (fast, no DB)
mux.Handle("GET /api/settings", httpmid.Authenticate(auth, log, jsonErrs, httpmid.UserOnly())(handler))

// Full session validation with DB lookup
mux.Handle("POST /api/me/password", httpmid.Authenticate(auth, log, jsonErrs, httpmid.WithUserSession())(handler))

// Service account API keys only
mux.Handle("POST /api/webhooks", httpmid.Authenticate(auth, log, jsonErrs, httpmid.ServiceAccountOnly())(handler))
```

### Token Type Detection

The middleware auto-detects the token type from its format:
- **JWT** -- three base64url segments separated by dots (e.g., `eyJhbG...eyJzdW...sig`)
- **API key** -- anything else

### Options

| Option | Accepts | Validation | Context Data |
|---|---|---|---|
| (default) | JWT or API key | JWT signature / API key hash | Subject, SubjectType |
| `UserOnly()` | JWT only | Signature only (fast, no DB) | Subject, SubjectType |
| `WithUserSession()` | JWT only | DB session validation | Subject, SubjectType, User, Session, SessionID |
| `ServiceAccountOnly()` | API key only | API key hash lookup | Subject, SubjectType |

**`WithUserSession()`** is the most important option. It implies `UserOnly()` and requires the authenticator to implement `SessionAuthenticator` (which `authentication.Authenticator` does). Use this for:
- Authentication endpoints (password change, logout, /me)
- Endpoints that need full user data in the handler
- When immediate session revocation detection is required

Without `WithUserSession()`, only the JWT signature is validated. This is fast (no DB hit) but accepts stale tokens for up to the access token expiry (default 30 minutes).

### Authenticator Interfaces

The middleware defines its own interfaces to decouple from concrete implementations:

```go
type JWTAuthenticator interface {
    AuthenticateJWT(ctx context.Context, token string) (authentication.Claims, error)
}

type SessionAuthenticator interface {
    JWTAuthenticator
    AuthenticateSession(ctx context.Context, token string) (authentication.User, authentication.Session, error)
}

type APIKeyAuthenticator interface {
    AuthenticateAPIKey(ctx context.Context, key string) (serviceAccountID string, err error)
}
```

`authentication.Authenticator` satisfies all three interfaces structurally.

### Context Values Set

After successful authentication:
- `Subject` -- always set. Format: `"user:{id}"` or `"service_account:{id}"`
- `SubjectType` -- always set. `SubjectTypeUser` or `SubjectTypeServiceAccount`
- `SessionID` -- only with `WithUserSession()`
- `User` -- only with `WithUserSession()`. Full `authentication.User` struct
- `Session` -- only with `WithUserSession()`. Full `authentication.Session` struct

## Authorize

Authorization middleware checks permissions via the `PermissionChecker` interface:

```go
type PermissionChecker interface {
    Check(ctx context.Context, req authorization.CheckRequest) (authorization.CheckResult, error)
}
```

The `authorization.Authorizer` satisfies this interface structurally.

### AuthorizeParam

Checks permission on a resource identified by a URL path parameter:

```go
mux.Handle("GET /posts/{id}",
    httpmid.AuthorizeParam(authorizer, log, jsonErrs, "post", "read", "id")(handler))

mux.Handle("DELETE /tenants/{tenant_id}",
    httpmid.AuthorizeParam(authorizer, log, jsonErrs, "tenant", "delete", "tenant_id")(handler))
```

Arguments: `authorizer, logger, errorRenderer, resourceType, permission, paramName`

The middleware calls `r.PathValue(paramName)` to extract the resource ID, then checks `authorizer.Check(ctx, CheckRequest{Subject, Permission, Resource})`.

### AuthorizeType

Checks permission on a resource type rather than a specific instance. Uses `"*"` as the resource ID. Useful for list and create operations:

```go
mux.Handle("GET /users",
    httpmid.AuthorizeType(authorizer, log, jsonErrs, "user", "list")(handler))
```

### Authorize (generic)

The base function accepts a custom resource ID extractor:

```go
mux.Handle("GET /posts/{id}",
    httpmid.Authorize(authorizer, log, jsonErrs, "post", "read",
        func(r *http.Request) string { return r.PathValue("id") },
    )(handler))
```

### RequirePlatformAdmin

Checks that the subject has the `platform:main#admin` relationship:

```go
mux.Handle("GET /admin/users",
    httpmid.RequirePlatformAdmin(authorizer, log, jsonErrs)(handler))
```

### Relationship Info in Context

When authorization succeeds, the middleware stores the relationship that granted access in the context. The `Reason` field from `CheckResult` is parsed to extract the direct relation (e.g., `"owner"` from `"direct:owner"` or `"through:tenant->direct:admin"`):

```go
info, ok := httpmid.GetRelationship(r.Context())
// info.Relation = "owner", info.ResourceType = "post", info.ResourceID = "abc123"
```

## ErrorRenderer

Every middleware that writes error responses accepts an `ErrorRenderer` interface:

```go
type ErrorRenderer interface {
    RenderError(w http.ResponseWriter, r *http.Request, kind ErrorKind)
}
```

Error kinds: `ErrKindUnauthenticated`, `ErrKindBadRequest`, `ErrKindInternal`, `ErrKindForbidden`.

The built-in `JSONErrors{}` renderer writes JSON error responses. For HTML routes, implement your own renderer:

```go
type HTMLErrors struct{}

func (HTMLErrors) RenderError(w http.ResponseWriter, r *http.Request, kind httpmid.ErrorKind) {
    switch kind {
    case httpmid.ErrKindUnauthenticated:
        http.Redirect(w, r, "/login", http.StatusSeeOther)
    case httpmid.ErrKindForbidden:
        w.WriteHeader(http.StatusForbidden)
        // render forbidden template
    }
}
```

## Context Helpers

### Reading Subject Information

```go
// Full subject string: "user:abc123" or "service_account:xyz"
subject := httpmid.GetSubject(ctx)

// Just the ID portion: "abc123"
userID := httpmid.GetSubjectID(ctx)

// Parsed type and ID
info := httpmid.GetSubjectInfo(ctx)
// info.Type = "user", info.ID = "abc123"

// Panics if not set (caught by Panics middleware, becomes 500)
subject := httpmid.MustGetSubject(ctx)
```

### Checking Subject Type

```go
if httpmid.IsUserAuth(ctx) {
    // subject is a user (authenticated via JWT)
}
if httpmid.IsServiceAccountAuth(ctx) {
    // subject is a service account (authenticated via API key)
}
```

### Reading Session Data (WithUserSession only)

```go
user := httpmid.GetUser(ctx)       // *authentication.User or nil
session := httpmid.GetSession(ctx) // *authentication.Session or nil
sessionID := httpmid.GetSessionID(ctx)
```

### Other Context Values

```go
clientIP := httpmid.GetClientIP(ctx)   // set by TrustProxies middleware
tenantID := httpmid.GetTenantID(ctx)   // set by ExtractTenantID middleware
```

## Middleware Ordering

Authentication must run before authorization. The authentication middleware sets the subject in context, which the authorization middleware reads to build the `CheckRequest`.

Recommended order in your middleware chain:

1. `httpmid.Panics` -- recover from panics
2. `httpmid.TrustProxies(n)` -- resolve real client IP (optional)
3. `httpmid.ClientInfo()` -- inject IP and User-Agent into context for security events
4. `httpmid.Telemetry` -- request tracing
5. `httpmid.Logger` -- request logging
6. `httpmid.RateLimit` -- rate limiting
7. `httpmid.BodyLimit` -- request body size limit
8. `httpmid.Authenticate` -- authentication
9. `httpmid.AuthorizeParam` / `httpmid.AuthorizeType` -- authorization (per-route)

Authentication is typically applied at the route group level. Authorization is applied per-route because each route has different resource type and permission requirements.

## Related

- [Auth Architecture Overview](overview.md)
- [Authentication](authentication.md)
- [Authorization](authorization.md)
- [Bridge Layer](../layers/bridge.md)
