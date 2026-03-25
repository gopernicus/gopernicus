# SDK Layer

The SDK is the lowest-dependency layer in a Gopernicus application. Every other
layer imports from the SDK; the SDK imports nothing from core, infrastructure,
bridge, or app. It provides transport-agnostic primitives: HTTP handling, error
sentinels, validation, pagination, logging, background workers, and more.

All packages are stdlib-only (no external dependencies).

---

## Package: `sdk/web`

HTTP handler toolkit built on Go 1.22 `http.ServeMux`. Key types:

| Type | Purpose |
|------|---------|
| `WebHandler` | Mux wrapper with global and per-route middleware. |
| `RouteGroup` | Groups routes under a shared prefix with stacked middleware. |
| `WebServer` | `http.Server` wrapper with `ServerConfig` (env-tag driven). |
| `Middleware` | `func(http.Handler) http.Handler` -- standard Go middleware. |
| `RouteSpec` | Declarative route description used to generate OpenAPI 3.1 specs. |
| `SSEStream` / `StreamWriter` | Server-Sent Events for channel-based and handler-controlled streaming. |
| `StaticFileServer` | Static file serving with SPA fallback and immutable asset caching. |

```go
handler := web.NewWebHandler(web.WithLogging(log), web.WithCORS([]string{"*"}))
api := handler.Group("/api/v1")
api.GET("/users", listUsers)
api.POST("/users", createUser, authMiddleware)
```

**Request helpers** -- `web.Param(r, "id")`, `web.QueryParam(r, "status")`,
`web.Decode[T](r)` (decodes JSON and calls `Validate()` if implemented).

**Response helpers** -- `web.RespondJSON`, `web.RespondJSONCreated`,
`web.RespondNoContent`, `web.RespondJSONDomainError` (maps `errs.*` sentinels
to HTTP status codes automatically).

**Error types** -- `web.Error` carries `Status`, `Message`, `Code`, and optional
`Fields` for per-field validation detail. Factory functions: `ErrBadRequest`,
`ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`, `ErrGone`,
`ErrInternal`. `ErrFromDomain(err)` maps `sdk/errs` sentinels to the correct
HTTP error.

**OpenAPI** -- `handler.ServeOpenAPI("/openapi.json", info, specs...)` builds
and serves an OpenAPI 3.1 spec from `RouteSpec` slices exported by bridges.

---

## Package: `sdk/fop`

Filter, Order, Pagination primitives used by repositories and bridges.

| Type | Purpose |
|------|---------|
| `Order` | Field + direction (`ASC`/`DESC`). |
| `OrderField` | Maps API-facing names to DB columns with optional `CastLower`. |
| `PageStringCursor` | Limit + opaque cursor string for keyset pagination. |
| `Pagination` | Response metadata: `HasPrev`, `NextCursor`, `PreviousCursor`, `PageTotal`. |
| `Cursor` | Base64-encoded keyset position with stale-cursor detection. |

```go
page, err := fop.ParsePageStringCursor(r.URL.Query().Get("limit"), r.URL.Query().Get("cursor"), 100)
order, err := fop.ParseOrder(users.GeneratedOrderByFields, r.URL.Query().Get("order"), defaultOrder)
```

---

## Package: `sdk/errs`

Transport-agnostic sentinel errors. Domain code wraps these; boundary code
checks with `errors.Is()`.

`ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidReference`, `ErrInvalidInput`,
`ErrUnauthorized`, `ErrForbidden`, `ErrConflict`, `ErrExpired`.

```go
var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)
```

---

## Package: `sdk/validation`

Reflection-free validators that all follow the same signature:
`func(field, value string) error`. Empty values pass all validators except
`Required` -- compose with `Required` for mandatory fields.

`Errors` collector accumulates errors; `Err()` returns a combined error or nil.

```go
var errs validation.Errors
errs.Add(validation.Required("email", req.Email))
errs.Add(validation.Email("email", req.Email))
errs.Add(validation.MinLength("password", req.Password, 8))
if err := errs.Err(); err != nil {
    return web.ErrBadRequest(err.Error())
}
```

Validators: `Required`, `MinLength`, `MaxLength`, `OneOf`, `Email`, `UUID`,
`URL`, `Slug`, `Matches`, `PasswordStrength`, `PasswordsMatch`, `Min`, `Max`,
`Range`, `Positive`, `NotEmpty`, `MinItems`, `MaxItems`. Each has a `*Ptr`
variant for optional pointer fields. `IfSet[T]` runs a custom validator only
when the pointer is non-nil.

---

## Package: `sdk/logger`

Returns a plain `*slog.Logger` -- no wrapper. Helpers parse env config:

```go
log := logger.New(logger.Options{Level: "DEBUG", Format: "json"})
log := logger.NewDefault(logger.WithTracing()) // injects trace_id, span_id
```

---

## Package: `sdk/async`

Bounded goroutine pool with panic recovery for fire-and-forget tasks.

```go
pool := async.NewPool(async.IOPreset()..., async.WithLogger(log))
pool.Go(func() { invalidateCache(key) })
defer pool.Close(ctx)
```

`IOPreset()` = 1000 concurrency, drop on full. `CPUPreset()` = GOMAXPROCS,
blocking.

---

## Package: `sdk/workers`

Polling worker pool for background job processing. Separates orchestration
(WorkerPool) from business logic (Runner).

`WorkerPool` manages N goroutines polling a `WorkFunc` with adaptive intervals.
`Runner[T Job]` handles the job lifecycle: Checkout -> PreHooks -> Process
(with retry) -> PostHooks -> Complete/Fail.

```go
runner := workers.NewRunner(store, processFunc, log, workers.WithMaxRetries(3))
pool := workers.NewPool(runner.WorkFunc(), cfg)
pool.Start(ctx)
```

---

## Other Packages

| Package | Purpose |
|---------|---------|
| `sdk/conversion` | Case conversion: `ToPascalCase`, `ToCamelCase`, `ToSnakeCase`, `ToKebabCase`. Handles acronyms (ID, URL, API). |
| `sdk/environment` | Loads `.env` files, `GetEnvOrDefault`, namespaced env lookups. |
| `sdk/notify` | `Notifier` interface and `Notification` struct -- infrastructure adapters implement this. |

---

## Related

- [Core Layer](core.md) -- domain logic that imports SDK primitives
- [Infrastructure Layer](infrastructure.md) -- adapters that use SDK types
- [Bridge Layer](bridge.md) -- HTTP handlers built with `sdk/web`
- [App Layer](app.md) -- composition root that wires everything
