---
sidebar_position: 10
title: Web
---

# SDK — Web

`sdk/web` is the HTTP layer foundation. It wraps `net/http`, `encoding/json`, `io`, and `io/fs` from the standard library — no external router or framework.

## Handler and routing

`WebHandler` wraps `http.ServeMux` and adds global and per-route middleware support:

```go
handler := web.NewWebHandler(
    web.WithLogging(log),
    web.WithCORS([]string{"https://myapp.com"}),
    web.WithDefaultHeaders(map[string]string{"X-Frame-Options": "DENY"}),
)

handler.GET("/health", healthCheck)
handler.POST("/v1/users", createUser, authMiddleware)
```

Routes are registered with `GET`, `POST`, `PUT`, `PATCH`, `DELETE` convenience methods, or the lower-level `Handle(method, path, handler, ...middleware)`.

### Route groups

Groups share a path prefix and middleware. They can be nested:

```go
v1 := handler.Group("/v1", loggingMiddleware)

users := v1.Group("/users", authMiddleware)
users.GET("", listUsers)
users.GET("/:user_id", getUser)
users.POST("", createUser)

// nested group — /v1/users/:user_id/posts
posts := users.Group("/:user_id/posts")
posts.GET("", listPosts)
```

Middleware applies in order: global → group → route.

## Request decoding

`DecodeJSON` reads and unmarshals the request body, then calls `Validate() error` on the result if the type implements it:

```go
req, err := web.DecodeJSON[CreateUserRequest](r)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

If `Validate()` returns a `web.FieldErrors`, the error response includes per-field detail. Otherwise the error message is used directly.

Path and query params:

```go
id     := web.Param(r, "user_id")
search := web.QueryParam(r, "search")
```

## Responding

**JSON:**

```go
web.RespondJSONOK(w, user)       // 200
web.RespondJSONCreated(w, user)  // 201
web.RespondJSONAccepted(w, user) // 202
web.RespondNoContent(w)          // 204
```

**Errors:**

```go
web.RespondJSONError(w, web.ErrNotFound("user not found"))
web.RespondJSONError(w, web.ErrBadRequest("invalid input"))
web.RespondJSONDomainError(w, err) // maps errs sentinels → HTTP status
```

**Other:**

```go
web.RespondText(w, http.StatusOK, "ok")
web.RespondHTML(w, http.StatusOK, "<h1>Hello</h1>")
web.RespondRaw(w, http.StatusOK, "image/png", data)
web.RespondStream(w, http.StatusOK, "application/pdf", reader)
web.RespondFile(w, r, embedFS, "report.pdf")
web.RespondRedirect(w, r, "/login", http.StatusFound)
```

## Errors

`web.Error` carries an HTTP status, a message, a machine-readable code, and optional field-level detail:

```go
// Named constructors — status + code set automatically
web.ErrBadRequest("missing name")          // 400 bad_request
web.ErrUnauthorized("token expired")       // 401 unauthenticated
web.ErrForbidden("insufficient role")      // 403 permission_denied
web.ErrNotFound("user not found")          // 404 not_found
web.ErrConflict("email already taken")     // 409 already_exists
web.ErrGone("invite expired")              // 410 expired
web.ErrTooManyRequests("slow down")        // 429 rate_limit_exceeded
web.ErrInternal("unexpected failure")      // 500 internal
web.ErrUnavailable("try again later")      // 503 unavailable
```

`ErrValidation` converts a `DecodeJSON` error into a 400 response. If the error contains `FieldErrors` (from a `Validate()` method), the response includes per-field detail. Otherwise the error message is used directly:

```go
req, err := web.DecodeJSON[CreateUserRequest](r)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

`ErrFromDomain` maps `sdk/errs` sentinels to the appropriate HTTP error — used by `RespondJSONDomainError` as a catch-all after explicit error handling:

```go
if errors.Is(err, authentication.ErrEmailNotVerified) {
    web.RespondJSONError(w, web.ErrForbidden("email not verified"))
    return
}
web.RespondJSONDomainError(w, err) // generic fallback
```

### Field-level validation errors

`FieldErrors` produces structured per-field error responses:

```go
func (r *CreateUserRequest) Validate() error {
    var errs web.FieldErrors
    errs.AddErr("email", validation.Required("email", r.Email))
    errs.AddErr("email", validation.Email("email", r.Email))
    errs.AddErr("password", validation.PasswordStrength("password", r.Password))
    return errs.Err()
}
```

Response body when `FieldErrors` is returned:

```json
{
  "message": "validation failed",
  "code": "validation_failed",
  "fields": [
    {"field": "email", "message": "email is required"},
    {"field": "password", "message": "password must contain at least 8 characters"}
  ]
}
```

## Middleware

`Middleware` is `func(http.Handler) http.Handler` — compatible with any standard Go middleware. Built-ins:

```go
web.CORSMiddleware(origins)           // CORS headers + OPTIONS handling
web.DefaultHeadersMiddleware(headers) // sets response headers on every request
```

`ResponseCapture` wraps `http.ResponseWriter` to capture status code and bytes written — useful for logging middleware:

```go
rc := web.NewResponseCapture(w)
next.ServeHTTP(rc, r)
log.Info("request", "status", rc.StatusCode, "bytes", rc.BytesWritten)
```

## Server

`WebServer` wraps `http.Server` with env-tag-based config:

```go
cfg := web.ServerConfig{} // reads HOST, PORT, timeouts, etc. via environment tags
srv := web.NewServer(cfg, web.WithHandler(handler))
// or with defaults (0.0.0.0:8080)
srv := web.NewServerDefault(web.WithHandler(handler))
```

## OpenAPI

`ServeOpenAPI` builds and serves an OpenAPI 3.1 spec from `RouteSpec` declarations exported by bridge packages:

```go
handler.ServeOpenAPI("/openapi.json",
    web.OpenAPIInfo{Title: "My API", Version: "1.0.0"},
    usersBridge.OpenAPISpec(),
    authBridge.OpenAPISpec(),
)
```

Schema reflection is driven by the struct types passed as `RequestBody` and `ResponseBody` on each `RouteSpec`. Non-pointer fields without `omitempty` are marked required.

## SSE

`SSEStream` streams Server-Sent Events from a channel:

```go
events := make(chan web.SSEEvent)
stream := web.NewSSEStream(events)

handler.GET("/events", func(w http.ResponseWriter, r *http.Request) {
    stream.ServeHTTP(w, r)
})

// send an event
events <- web.SSEEvent{Event: "update", Data: payload}
```

The stream closes when the channel closes or the client disconnects.

## StreamWriter

`StreamWriter` provides the handler with direct control over SSE writes, unlike `SSEStream` which reads from a channel. This enables the "respond with JSON or upgrade to a stream" pattern used by AI/LLM APIs and progress endpoints:

```go
handler.POST("/generate", func(w http.ResponseWriter, r *http.Request) {
    if web.AcceptsStream(r) {
        sw := web.NewStreamWriter(w)
        sw.SendJSON("token", map[string]string{"text": "hello"})
        sw.SendJSON("token", map[string]string{"text": " world"})
        return
    }
    web.RespondJSONOK(w, fullResponse)
})
```

`AcceptsStream(r)` checks for `text/event-stream` in the `Accept` header. `NewStreamWriter` returns `nil` if the `ResponseWriter` does not support flushing.

## Static files

`StaticFileServer` serves embedded or disk-based static assets with optional SPA fallback:

```go
srv := web.NewStaticFileServer(embedFS,
    web.WithSPAMode(),              // unknown paths → index.html
    web.WithAssetPrefix("assets/"), // long cache headers for hashed assets
)
```

Registration uses `HandleRaw`, which bypasses all global middleware — including panic recovery. Wrap the server manually with any middleware it needs before registering:

```go
var h http.Handler = srv
h = recoveryMiddleware(h)
h = loggingMiddleware(h)
handler.HandleRaw("/", h)
```
