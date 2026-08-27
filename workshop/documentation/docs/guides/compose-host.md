---
title: Compose a host
description: Build a Gopernicus composition root with explicit dependencies and lifecycle.
---

# Compose a host

A host is the application. It chooses Gopernicus parts, owns app-local domains, and controls the process lifecycle. Start from Workshop or an example, then keep the composition explicit.

## 1. Scaffold or create a module

```bash
gopernicus init \
  --module github.com/acme/myapp \
  --db pgx \
  ./myapp
```

`--db none` creates an SDK-only host. `init` mounts no pockets and therefore makes no product decisions for you.

## 2. Load posture and create cancellation

Load environment before reading config. Choose a required deployment posture rather than inferring “development” from missing input. Derive a process context from interrupt and termination signals.

```go
_ = environment.LoadEnv()

mode, err := environment.ParseMode(os.Getenv("APP_MODE"))
if err != nil {
    return err
}

ctx, stop := signal.NotifyContext(
    context.Background(),
    os.Interrupt,
    syscall.SIGTERM,
)
defer stop()
```

## 3. Construct outbound infrastructure

Build concrete clients before repositories and services:

```text
database / Redis / cloud clients
        ↓
pocket stores + host outbound adapters
        ↓
pocket services + app logic
        ↓
HTTP handlers and background runtimes
```

Every constructor error should fail boot. Defer or coordinate `Close` calls in reverse dependency order.

## 4. Build the host router

```go
router := web.NewWebHandler(web.WithLogging(log))
router.Use(
    web.RequestID(),
    tracing.Middleware(tracer),
    web.Logger(log),
    web.Panics(log),
)

mount := pocket.Mount{
    Router: router,
    Logger: log,
    Events: bus,
}
```

Add CORS/default security headers according to the host's browser/API posture. If client IP affects auditing or rate limits, wire `TrustProxies` with the exact number of trusted proxy hops.

## 5. Construct pockets in dependency order

Pockets cannot import one another, but services may still need host wiring. Build providers first, then bridge their public seams.

```go
authSvc, err := authentication.NewService(authRepos, authConfig)
if err != nil {
    return err
}

if err := authSvc.Register(mount); err != nil {
    return err
}

if err := cms.Register(mount, cmsRepos, cms.Config{
    Views: cmsViews,
    AdminMiddleware: []web.Middleware{
        authSvc.RequireUser,
        requireCMSAdmin,
    },
}); err != nil {
    return err
}
```

When service signatures do not structurally match, write a small adapter in `cmd` or host `internal` code. Do not solve the mismatch by making pocket cores import one another.

## 6. Start host-owned runtimes

Build jobs, delivery, and outbox pollers during composition, but start them only after construction is complete. Capture their errors and use the same cancellation tree.

```go
runtimeErr := make(chan error, 1)
go func() {
    runtimeErr <- jobsRuntime.Run(ctx)
}()
```

Production code may use `errgroup` or a host supervisor. The invariant is ownership: a pocket constructor or `Register` call should not hide a goroutine you cannot stop.

## 7. Add host health routes

Health is composition-specific. A memory host may report liveness when the handler runs; a database host should probe its database and return unavailable on failure. Keep probes outside user authentication gates.

```go
router.GET("/healthz", func(w http.ResponseWriter, r *http.Request) {
    if err := pgxdb.StatusCheck(r.Context(), db); err != nil {
        _ = web.RespondJSON(w, http.StatusServiceUnavailable, status{"unavailable"})
        return
    }
    _ = web.RespondJSONOK(w, status{"ok"})
})
```

## 8. Serve and shut down

```go
return web.Run(ctx, router, web.ServerConfig{
    Host:            "localhost",
    Port:            "8080",
    ReadTimeout:     15 * time.Second,
    WriteTimeout:    15 * time.Second,
    IdleTimeout:     120 * time.Second,
    ShutdownTimeout: 10 * time.Second,
}, log)
```

Choose shutdown order deliberately. Stop new admission, drain runtimes/pollers, stop event production, close buses/providers, then close datastores. Use a fresh bounded context for exporter flush when the process context is already canceled.

## Composition review

Before shipping, verify:

- every production security mode and key is explicit;
- optional routes are present only when intended;
- migrations run before store boot probes;
- runtime goroutines have cancellation and surfaced errors;
- logger/tracer/provider/database shutdown is ordered;
- health checks reflect required dependencies;
- no development-only transport or query logging reaches production;
- only selected adapters appear in `go.mod`.
