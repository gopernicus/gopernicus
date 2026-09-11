---
title: Web package
description: Routing, middleware, request/response helpers, JSON and HTML responses, SSE, static files, and server lifecycle.
---

# Web package

`sdk/pkg/web` is a `net/http`-native transport kit. It provides reusable HTTP mechanism and policy without owning application routes, pocket schemas, or view technology.

## Router and middleware

`WebHandler` wraps Go's `http.ServeMux` and supports global, group, and per-route middleware.

```go
router := web.NewWebHandler()
router.Use(
    web.RequestID(),
    tracing.Middleware(tracer),
    web.Logger(log),
    web.Panics(log),
)

router.GET("/healthz", healthz)

admin := router.Group("/admin", requireUser)
admin.GET("/reports", listReports)
admin.POST("/reports", createReport, requirePermission)
```

Global middleware wraps the entire mux, including redirects, 404s, 405s, and `HandleRaw` registrations. Registration and `Use` are boot-time operations; do not mutate a handler after serving begins.

Middleware runs outermost-first. Put tracing outside logging so access logs carry the traced context. Recorders forward errors through nested wrappers. Route groups copy their middleware, so siblings and later caller-slice changes cannot alter an existing group.

Available pure HTTP middleware includes panic recovery, structured access logging, request IDs, proxy-aware client IP resolution, CORS, and default headers. Rate limiting, caching, and tracing middleware live with their capability owners.

## Decode, validate, respond

`DecodeJSON[T]` rejects empty/invalid bodies and top-level JSON `null`. It supports both `Request` and `*Request` targets and calls `Validate() error` once when the decoded value or its address implements it. Request and domain validation both collect field problems with `sdk.ValidationError`.

```go
type createWidget struct {
    Name string `json:"name"`
}

func (in *createWidget) Validate() error {
    var fields sdk.ValidationError
    fields.AddViolation(validation.Required("name", in.Name))
    return fields.Err()
}

func create(w http.ResponseWriter, r *http.Request) {
    in, err := web.DecodeJSON[createWidget](r)
    if err != nil {
        web.RespondJSONError(w, web.ErrValidation(err))
        return
    }

    widget, err := service.Create(r.Context(), in.Name)
    if err != nil {
        web.RespondJSONDomainError(w, err)
        return
    }

    _ = web.RespondJSONCreated(w, widget)
}
```

JSON responders report serialization/write failures through `RecordError` as well as their returned errors. `RespondJSONDomainError` maps SDK errors to status codes and records original 5xx causes without exposing them to clients. Redirect and no-content helpers remain available. Use standard `net/http` and `io` operations for text, bytes, files, and ordinary reader streaming.

`DecodeJSON` accepts unknown fields. The host chooses body limits, for example per route or group:

```go
// apiBodyLimit is chosen by the host; uploads can have a different limit.
limitJSON := func(next http.Handler) http.Handler {
    return http.MaxBytesHandler(next, apiBodyLimit)
}
api := router.Group("/api", limitJSON)
api.POST("/widgets", create)
```

Limit errors reach `ErrValidation` as HTTP 413. There is no implicit global limit or content-type check in `DecodeJSON`. For a stricter DTO contract, use a bounded `json.Decoder`, enable `DisallowUnknownFields`, and require EOF after one value; keep validation and client-safe error handling explicit.

`ReadBody` is a separate bounded object reader for exact declared keys and presence tracking. It retains its 1-MiB limit and `Body.Has`/typed getters. It rejects malformed trailing content; it does not replace domain-owned PATCH/null semantics.

## HTML responses and optional view packages

`Renderer` uses only standard-library types:

```go
type Renderer interface {
    Render(context.Context, io.Writer) error
}
```

`templ.Component` satisfies it implicitly, as does `web.Template` around `html/template`. The SDK can render either without importing the view library; an API-only host does not need to use this seam.

```go
web.Render(r.Context(), w, http.StatusOK, page)
```

Choose the status before rendering. Once the response header is sent, a mid-stream render failure cannot change the HTTP status. `Render` records the failure, and `cacher.Pages` refuses to cache that response.

Panic recovery writes HTML 500 only before the response starts. A later panic aborts the response; `http.ErrAbortHandler` passes through without a panic stack log. Access logging retains the first recorded cause, including for a failed 200 response. Aborted requests include `aborted: true`; hijacked requests include `hijacked: true`. Status 0 means no final status was observed before abort or hijacking. Use `http.NewResponseController(w)` for flushing/hijacking through wrappers.

## Static and SPA files

`StaticFileServer` serves any `fs.FS`, supports immutable caching below a chosen asset prefix, and optionally falls back to `index.html` for an SPA.

```go
static := web.NewStaticFileServer(
    assets.FS,
    web.WithAssetPrefix("dist/"),
)
static.AddRoutes(router, "/assets/goth")
```

GOTH uses this seam: the UI module exposes its embedded filesystem, while the host chooses its public route. The server can also be mounted directly as an `http.Handler`, resolving `r.URL.Path`.

`WithSPAMode` serves `index.html` for missing paths, directories, and the root; index responses always have no-store headers, including direct `/index.html`. Other filesystem failures return 500. Only put versioned assets under an immutable asset prefix. MIME types follow the standard library and its platform registrations. Seekable files support range/conditional responses through `http.ServeContent`; non-seekable files stream without range support.

## SSE and response streaming

`SSEStream` reads a channel of `SSEEvent` values with optional heartbeats:

```go
web.NewSSEStream(events, web.WithHeartbeat(15*time.Second)).ServeHTTP(w, r)
```

Strings and byte slices are raw event data; other values are JSON encoded. Multiline data remains one event. Event names reject CR/LF; IDs reject CR/LF/NUL. Invalid metadata or serialization ends the stream with a recorded error before any bytes of that event are written. I/O can still interrupt a frame. Flushing and per-write deadlines work through the SDK response wrappers.

The pocket or host owns stream authorization, event filtering, connection age, producer cancellation, and content negotiation. For ordinary byte streaming, write to the response and use `http.NewResponseController(w).Flush()` as needed.

## Host-owned OpenAPI

Serve a checked, host-owned OpenAPI document using ordinary routing. The SDK does not infer schemas or route metadata:

```go
// openAPIDocument is the host's validated JSON document.
router.GET("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _, err := w.Write(openAPIDocument)
    web.RecordError(w, err)
})
```

Choose optional generation tooling when an application needs it; its dependency belongs in the host or an integration, keeping the SDK stdlib-only.

## Server lifecycle

`web.Run` starts serving and returns on a startup failure or host cancellation. Cancellation begins a graceful drain using `ServerConfig.ShutdownTimeout`. If draining expires, it closes remaining connections and returns the shutdown error. Closing connections cancels their request contexts; normal draining does not.

```go
cfg := web.ServerConfig{
    Host:            "localhost",
    Port:            "8080",
    ReadTimeout:     15 * time.Second,
    WriteTimeout:    15 * time.Second,
    IdleTimeout:     120 * time.Second,
    ShutdownTimeout: 10 * time.Second,
}

return web.Run(ctx, router, cfg, log)
```

The host owns the cancellation context and should stop producers/background runtimes in an order that prevents work from being acknowledged after its consumers have closed.

Run does not manage hijacked connections or wait for handler goroutines that ignore cancellation. Hosts retain responsibility for those lifecycles.
