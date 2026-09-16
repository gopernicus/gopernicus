---
title: Web package
description: "The net/http transport kit: router, middleware, decode and respond, HTML, static files, SSE and server lifecycle."
---

# Web package

`sdk/pkg/web` is a `net/http`-native transport kit. It supplies HTTP mechanism
and policy without owning application routes, pocket schemas or view
technology. Everything composes with plain `http.Handler` values.

| Need | Use |
|---|---|
| a router with global, group and per-route middleware | `NewWebHandler`, `Group`, `GET`/`POST`/... |
| pure HTTP middleware | `RequestID`, `Logger`, `Panics`, `TrustProxies`, `CORSMiddleware`, `DefaultHeadersMiddleware` |
| decode and validate a JSON body | `DecodeJSON[T]`, `ReadBody` |
| respond | `RespondJSON*`, `RespondJSONError`, `RespondJSONDomainError`, `Render`, redirect and no-content helpers |
| serve embedded or SPA assets | `NewStaticFileServer`, `WithAssetPrefix`, `WithSPAMode` |
| server-sent events | `NewSSEStream`, `WithHeartbeat` |
| run the server | `Run`, `ServerConfig` |

Middleware that needs a capability lives with that capability:
`ratelimiter.Middleware`, `cacher.Pages`, `tracing.Middleware`. Authentication
and authorization middleware come from their pockets.

## Router and middleware

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

`WebHandler` wraps `http.ServeMux`. Global middleware wraps the entire mux,
including redirects, 404s, 405s and `HandleRaw` registrations. Middleware runs
outermost-first, so put tracing outside logging and access logs carry the traced
context. Route groups copy their middleware; later changes to the caller's slice
cannot alter an existing group. Registration and `Use` are boot-time operations.

## Decode, validate, respond

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

`DecodeJSON[T]` rejects empty or invalid bodies and top-level `null`, accepts
unknown fields, works with `T` or `*T`, and calls `Validate() error` once when
the value or its address implements it. `RespondJSONDomainError` maps root SDK
errors to status codes and records original 5xx causes without exposing them.
JSON responders report write failures through `RecordError` as well as their
return value. For text, bytes, files and plain streaming, use `net/http` and
`io` directly.

**Body limits are the host's.** There is no implicit global limit or
content-type check. Wrap a route or group:

```go
limitJSON := func(next http.Handler) http.Handler {
    return http.MaxBytesHandler(next, apiBodyLimit)   // uploads can use a different limit
}
api := router.Group("/api", limitJSON)
api.POST("/widgets", create)
```

Limit errors reach `ErrValidation` as HTTP 413. For a stricter DTO contract,
use a bounded `json.Decoder` with `DisallowUnknownFields` and require EOF after
one value. `ReadBody` is a separate bounded object reader (1 MiB) with
`Body.Has` and typed getters for exact declared keys and presence tracking; it
rejects malformed trailing content and does not replace domain-owned PATCH or
null semantics.

## HTML responses

`Renderer` is one standard-library method, `Render(context.Context, io.Writer)
error`. `templ.Component` satisfies it implicitly, and `web.Template(t, name, data)`
adapts an `html/template`, so the SDK renders either without importing a view
library.
An API-only host never touches this seam.

```go
web.Render(r.Context(), w, http.StatusOK, page)
```

Choose the status before rendering: once headers are sent, a mid-stream failure
cannot change it. `Render` records the failure and `cacher.Pages` refuses to
cache that response. Panic recovery writes an HTML 500 only before the response
starts; a later panic aborts the response, and `http.ErrAbortHandler` passes
through without a stack log. Access logs keep the first recorded cause, mark
`aborted: true` or `hijacked: true`, and report status 0 when no final status
was observed. Use `http.NewResponseController(w)` to flush or hijack through the
wrappers.

## Static and SPA files

```go
static := web.NewStaticFileServer(assets.FS, web.WithAssetPrefix("dist/"))
static.AddRoutes(router, "/assets/goth")
```

`StaticFileServer` serves any `fs.FS`, applies immutable caching below the
chosen asset prefix, and can be mounted directly as an `http.Handler`. The GOTH
UI module exposes its embedded filesystem this way while the host picks the
public route. Only versioned assets belong under an immutable prefix.
`WithSPAMode` serves `index.html` for missing paths, directories and the root,
always with no-store headers. Seekable files get range and conditional
responses through `http.ServeContent`; non-seekable files stream without
ranges. MIME types follow the standard library.

## SSE and streaming

```go
web.NewSSEStream(events, web.WithHeartbeat(15*time.Second)).ServeHTTP(w, r)
```

`SSEStream` reads a channel of `SSEEvent`. Strings and byte slices are raw
data; other values are JSON encoded; multiline data stays one event. Event names
reject CR/LF and IDs reject CR/LF/NUL. Invalid metadata or serialization ends
the stream with a recorded error before any bytes of that event are written.
The pocket or host owns stream authorization, filtering, connection age,
producer cancellation and content negotiation. For plain byte streaming, write
to the response and flush with `http.NewResponseController(w).Flush()`.

## Host-owned OpenAPI

The SDK infers no schemas or route metadata. Serve a checked, host-owned
document with ordinary routing:

```go
router.GET("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _, err := w.Write(openAPIDocument)
    web.RecordError(w, err)
})
```

Generation tooling, if any, is a host or integration dependency, keeping the
SDK stdlib-only.

## Server lifecycle

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

`ServerConfig` carries environment tags, so a host can parse it with
`environment.ParseEnvTags` and then apply `web.TrustProxies(cfg.TrustedProxyCount)`.
`Run` returns on a startup failure or when the host cancels its context.
Cancellation starts a graceful drain bounded by `ShutdownTimeout`; if it
expires, remaining connections are closed, which cancels their request
contexts, and the shutdown error is returned. `Run` does not manage hijacked
connections or wait for handler goroutines that ignore cancellation. Stop
producers and background runtimes in an order that prevents work from being
acknowledged after its consumers have closed.
