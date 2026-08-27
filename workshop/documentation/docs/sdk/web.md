---
title: Web foundation
description: Routing, middleware, request/response helpers, JSON and HTML responses, streams, OpenAPI, static files, and server lifecycle.
---

# Web foundation

`sdk/foundation/web` is a `net/http`-native transport kit. It provides reusable HTTP mechanism and policy without owning application routes, pocket schemas, or view technology.

## Router and middleware

`WebHandler` wraps Go's `http.ServeMux` and supports global, group, and per-route middleware.

```go
router := web.NewWebHandler(web.WithLogging(log))
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

Middleware runs outermost-first. Put tracing outside logging when access logs should carry the traced context and render/response errors should reach the logger's status recorder.

Available pure HTTP middleware includes panic recovery, structured access logging, request IDs, proxy-aware client IP resolution, CORS, and default headers. Rate limiting, caching, and tracing middleware live with their capability owners.

## Decode, validate, respond

`DecodeJSON[T]` rejects empty/invalid bodies and automatically calls `Validate() error` when the decoded pointer implements it.

```go
type createWidget struct {
    Name string `json:"name"`
}

func (in *createWidget) Validate() error {
    var fields web.FieldErrors
    fields.AddErr("name", validation.Required("name", in.Name))
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

The responder set covers JSON success/error, plain text, raw bytes, HTML, streams, files, redirects, and no-content responses. `RespondJSONDomainError` maps SDK error classes to status codes and records original 5xx errors for the request logger without leaking them to clients.

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

Choose the status before rendering. Once the response header is sent, a mid-stream render failure can be recorded but cannot change the HTTP status.

## Static and SPA files

`StaticFileServer` serves any `fs.FS`, supports immutable caching below a chosen asset prefix, and optionally falls back to `index.html` for an SPA.

```go
static := web.NewStaticFileServer(
    assets.FS,
    web.WithAssetPrefix("dist/"),
)
static.AddRoutes(router, "/assets/goth")
```

GOTH uses this exact seam: the UI module exposes its embedded filesystem, while the host chooses its public route.

## SSE and response streaming

The package offers two related surfaces:

- `SSEStream` for channels of SSE events with optional heartbeat;
- `StreamWriter` for incremental data/JSON frames and content-negotiation helpers.

Both preserve flushing through the global middleware wrappers. A pocket still owns stream authorization, event filtering, connection age, and domain semantics.

## App-driven OpenAPI

`BuildOpenAPISpec` and `ServeOpenAPI` build deterministic OpenAPI 3.1 JSON from explicit `[]RouteSpec` values. The router does not introspect itself.

```go
routes := []web.RouteSpec{{
    Method:       http.MethodPost,
    Path:         "/widgets",
    Summary:      "Create a widget",
    Tags:         []string{"widgets"},
    Authenticated: true,
    RequestBody:  createWidget{},
    ResponseBody: widgetResponse{},
}}

router.ServeOpenAPI(
    "/openapi.json",
    web.OpenAPIInfo{Title: "Widgets", Version: "1.0.0"},
    routes,
)
```

Schema reflection is intentionally small. Route ownership and documentation remain app-driven, which avoids magic registration and keeps internal routes explicit.

## Server lifecycle

`web.Run` blocks until the supplied context is canceled, then gracefully shuts down within `ServerConfig.ShutdownTimeout`.

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
