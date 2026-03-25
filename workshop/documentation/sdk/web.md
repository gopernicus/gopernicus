# sdk/web -- Web Framework Reference

Package `web` provides an HTTP framework built on the Go standard library `net/http.ServeMux` with support for route groups, middleware, JSON helpers, SSE streaming, and OpenAPI spec generation.

**Import:** `github.com/gopernicus/gopernicus/sdk/web`

## WebHandler

`WebHandler` wraps `http.ServeMux` and adds global and per-route middleware.

```go
handler := web.NewWebHandler(
    web.WithLogging(log),
    web.WithCORS([]string{"https://example.com"}),
    web.WithDefaultHeaders(map[string]string{"X-Frame-Options": "DENY"}),
)

handler.Use(myMiddleware) // global middleware, call before registering routes
```

### Registering Routes

Handler functions use the standard `http.HandlerFunc` signature. Optional per-route middleware can be appended.

```go
handler.GET("/healthz", healthHandler)
handler.POST("/users", createUser, authMiddleware)
handler.PUT("/users/{user_id}", updateUser, authMiddleware)
handler.DELETE("/users/{user_id}", deleteUser, authMiddleware)
handler.PATCH("/users/{user_id}", patchUser, authMiddleware)
```

`Handle(method, path, handler, middleware...)` is also available for dynamic method registration. `HandleRaw(pattern, handler)` bypasses all middleware.

## RouteGroup

Groups routes under a shared prefix with shared middleware. Groups can be nested.

```go
api := handler.Group("/api/v1", loggingMiddleware)
api.GET("/users", listUsers)

admin := api.Group("/admin", adminOnlyMiddleware)
admin.DELETE("/users/{user_id}", deleteUser)
```

## Middleware

Type: `func(http.Handler) http.Handler` -- compatible with any standard Go middleware.

Built-in middleware:

- `CORSMiddleware(origins []string)` -- handles CORS headers and OPTIONS preflight.
- `DefaultHeadersMiddleware(headers map[string]string)` -- sets default response headers.

## Request Helpers

### Param / QueryParam

```go
userID := web.Param(r, "user_id")       // path parameter via r.PathValue
status := web.QueryParam(r, "status")   // query string parameter
```

### DecodeJSON

Generic function that reads JSON from the request body and auto-validates if the target type implements `Validate() error`.

```go
req, err := web.DecodeJSON[CreateUserRequest](r)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

`Decode[T]` is an alias for `DecodeJSON[T]`.

## Response Helpers

| Function | Status | Description |
|---|---|---|
| `RespondJSON(w, status, v)` | any | JSON response with explicit status |
| `RespondJSONOK(w, v)` | 200 | JSON response |
| `RespondJSONCreated(w, v)` | 201 | JSON response for POST |
| `RespondJSONAccepted(w, v)` | 202 | JSON response for async operations |
| `RespondNoContent(w)` | 204 | Empty response |
| `RespondJSONError(w, *Error)` | varies | JSON error from `*web.Error` |
| `RespondJSONDomainError(w, err)` | varies | Maps `errs.*` sentinel to HTTP error |
| `RespondText(w, status, text)` | any | Plain text response |
| `RespondHTML(w, status, html)` | any | HTML response |
| `RespondRaw(w, status, ct, data)` | any | Custom content type |
| `RespondRedirect(w, r, url, status)` | any | HTTP redirect |
| `RespondStream(w, status, ct, reader)` | any | Stream from `io.Reader` |
| `RespondFile(w, r, fsys, name)` | 200 | Serve file from `fs.FS` |

## Error Handling

`web.Error` represents an HTTP error with status, message, code, and optional field-level detail.

```go
web.ErrBadRequest("invalid email")     // 400
web.ErrUnauthorized("token expired")   // 401
web.ErrForbidden("not allowed")        // 403
web.ErrNotFound("user not found")      // 404
web.ErrConflict("email taken")         // 409
web.ErrGone("link expired")            // 410
web.ErrTooManyRequests("slow down")    // 429
web.ErrInternal("something broke")     // 500
web.ErrUnavailable("try later")        // 503
```

`ErrFromDomain(err)` maps `errs.*` sentinels to `*web.Error` with safe, generic messages. `ErrValidation(err)` converts `DecodeJSON` errors (including `FieldErrors`) to 400 responses.

### FieldErrors

```go
var fe web.FieldErrors
fe.Add("email", "is required")
fe.AddErr("name", validation.Required("name", req.Name))
return fe.Err()
```

## SSE Streaming

### Channel-based: SSEStream

```go
events := make(chan web.SSEEvent)
stream := web.NewSSEStream(events)
go stream.ServeHTTP(w, r)

events <- web.SSEEvent{Event: "message", Data: "hello"}
close(events) // ends the stream
```

### Imperative: StreamWriter

```go
sw := web.NewStreamWriter(w)
sw.SendJSON("token", map[string]string{"text": "hello"})
sw.SendData("raw data")
```

`AcceptsStream(r)` checks for `text/event-stream` in the Accept header.

## WebServer

Wraps `http.Server` with configuration from `ServerConfig`.

```go
srv := web.NewServer(cfg, web.WithHandler(handler), web.WithPort("8080"))
srv := web.NewServerDefault(web.WithHandler(handler)) // defaults: 0.0.0.0:8080
```

`ServerConfig` fields: Host, Port, EnableDebug, ReadTimeout (30s), WriteTimeout (10s), IdleTimeout (120s), ShutdownTimeout (20s).

## OpenAPI Spec Generation

Register `RouteSpec` slices from bridge layers, then serve a combined OpenAPI 3.1.0 spec.

```go
handler.ServeOpenAPI("/openapi.json",
    web.OpenAPIInfo{Title: "My API", Version: "1.0.0"},
    authBridges.OpenAPISpec(),
    userBridges.OpenAPISpec(),
)
```

`RouteSpec` fields: Method, Path, Summary, Description, Tags, Authenticated, Paginated, RequestBody (zero-value struct for reflection), ResponseBody, StatusCode.

## Static File Server

```go
static := web.NewStaticFileServer(embeddedFS, web.WithSPAMode(), web.WithAssetPrefix("assets/"))
static.AddRoutes(handler, "/app")
```

## ResponseCapture

Wraps `http.ResponseWriter` to capture response metrics (status code, bytes written). Implements `http.Flusher` and `http.Hijacker` when the underlying writer supports them.

```go
rc := web.NewResponseCapture(w)
// ... use rc as ResponseWriter ...
fmt.Println(rc.StatusCode, rc.BytesWritten)
```

## Related

- [sdk/errs](../sdk/errs.md) -- domain error sentinels mapped by `ErrFromDomain`
- [sdk/validation](../sdk/validation.md) -- validators used in `Validate()` methods
- [sdk/fop](../sdk/fop.md) -- pagination parsed from query parameters
