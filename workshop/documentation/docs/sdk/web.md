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
| serve embedded or SPA assets | `NewStaticFileServer`, `WithAssetPrefix`, `WithSPAMode`, `WithIndex`, `WithReservedPrefixes` |
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

`Routes()` returns a copy of every registration in order, as
`web.Route{Method, Pattern, Raw}`: `Method` is upper-cased (`""` for any
method), `Pattern` is the final path including any group prefix, and `Raw`
marks a `HandleRaw` registration, recorded with its raw pattern. It never
exposes handlers or middleware. A host route-inventory test can compare it
against the routes it expects. Read it at boot, like `Use`. Routes served
behind a `HandleRaw` subtree (a nested mux, a static file server) are
invisible to it.

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

**Body limits.** `DecodeJSON[T]` reads at most `DefaultBodyLimit` (1 MiB);
an outer `http.MaxBytesHandler` with a larger limit no longer governs it. A
route that needs another bound calls `DecodeJSONLimit[T](r, n)`; `n <= 0` is
refused, so there is no unbounded mode. Decode failures wrap
`sdk.ErrInvalidInput` with `ReadBody`'s fixed sentences ("request body is
empty", "request body must be a JSON object"), never `encoding/json`'s text,
which names Go types and fields; a `Validate()` error keeps its own. There is
no content-type check. A body the handler reads itself, such as an upload,
stays the host's to bound:

```go
limitUploads := func(next http.Handler) http.Handler {
    return http.MaxBytesHandler(next, uploadLimit)
}
uploads := router.Group("/uploads", limitUploads)
uploads.POST("/", upload)
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
`WithSPAMode` serves `index.html` for missing paths, directories and the root
(never under the asset prefix or a reserved prefix, below),
always with no-store headers. Seekable files get range and conditional
responses through `http.ServeContent`; non-seekable files stream without
ranges. MIME types follow the standard library.

- **`WithIndex`** serves an `index.html` rewritten at boot (runtime config in a
  data block) in place of the file in the `fs.FS`. The bytes are public and go
  through `ServeContent`, so `Content-Length`, `HEAD` and ranges work. The host
  still detects an absent bundle itself.
- **Stale chunks.** In SPA mode a missing path under the asset prefix is a
  `no-store` 404, never the page. The SPA owns recovery: listen for Vite's
  `vite:preloadError` (or the bundler's equivalent) and `location.reload()`.
  Keep `WithAssetPrefix` equal to the bundler's asset dir, ending in `/`; a
  client route under it is unreachable by deep link. `WithAssetPrefix("")`
  turns the zone off and the immutable caching with it.
- **Other mounts beside the SPA — pick one:**
  - Mounted with `AddRoutes` on the same `WebHandler` as the API, and the API
    owns its 404 shape (JSON): register `GET /api/{path...}` *with its method*
    answering the API's 404. Mux precedence ranks it above `GET /{path...}`,
    and because it carries a method a wrong method on a real API route still
    gets 405. `WithReservedPrefixes` is not needed.
  - Mounted as a method-less root catch-all (`HandleRaw("/", …)`), or the
    reserved list is wanted as one testable value: `WithReservedPrefixes`. The
    answer is always a plain-text `no-store` 404. Keep the list in step with
    the root mounts; a route-census test is the pattern.
- **The catch-all caveat.** A method-less `/` catch-all turns a wrong method on
  any registered route from 405 into whatever the catch-all answers, because
  ServeMux reports 405 only when no pattern matches at all. `AddRoutes` avoids
  this by registering `GET`; a host that mounts with `HandleRaw("/", …)` must
  answer non-GET/HEAD itself. Do not "fix" it with a method-less `/api/` 404
  handler: that recreates the same 405→404 loss one prefix down.

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
