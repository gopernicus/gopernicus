---
title: Zero-infrastructure quickstart
description: Run a complete Gopernicus feature host with an in-memory store.
---

# Zero-infrastructure quickstart

The `examples/minimal` host uses the CMS feature with in-memory repositories. It includes HTTP endpoints, optional HTML views, custom content types, caching, console email, and embedded GOTH assets without a database or container. CMS is one feature; the same host pattern applies to the other feature modules and to app-local routes.

## Requirements

- Go 1.26.1 or the version named by the repository's `go.work`
- a checkout of `github.com/gopernicus/gopernicus`

## Run it

From the repository root:

```bash
cd examples/minimal
go run ./cmd/server
```

Open [http://localhost:8081](http://localhost:8081). The host seeds articles, an About page, a menu, and a custom Product type on startup. `GET /healthz` is the host-owned liveness route.

Stop the server with `Ctrl+C`; the host cancels its context and lets `web.Run` perform graceful shutdown.

## Follow the composition root

The useful file is `examples/minimal/cmd/server/main.go`. Its `run` function performs the complete composition:

1. construct an in-memory implementation of `cms.Repositories`;
2. construct the host's `web.WebHandler` and middleware stack;
3. create a `feature.Mount`;
4. build a `ui/goth` bundle and serve its embedded assets;
5. create the CMS view adapter;
6. call `cms.Register` with repositories and host-selected capabilities;
7. register the host's health route;
8. run the server until its context is canceled.

The core wiring looks like this:

```go
store := memstore.New()
repos := store.Repositories()

router := web.NewWebHandler(web.WithLogging(log))
router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

mount := feature.Mount{Router: router, Logger: log}

bundle, err := uigoth.New(uigoth.Config{
    AssetBasePath: "/assets/goth",
})
if err != nil {
    return err
}

views, err := cmsgoth.New(bundle)
if err != nil {
    return err
}

if err := cms.Register(mount, repos, cms.Config{
    Views:  views,
    Cache:  cacher.NewMemory(),
    Mailer: email.NewConsole(log),
}); err != nil {
    return err
}
```

The real example also serves the bundle assets, registers custom content and templates, seeds the repositories, and supplies contact-form addresses. Treat the source as the executable version of this abbreviated listing. Remove the view bundle and its asset route when the host should expose an API only.

## What this proves

Inspect `examples/minimal/go.mod`. The host does not require libSQL, pgx, Redis, OpenTelemetry, or a cloud SDK. The CMS feature is datastore-free because the host satisfies its repository ports directly.

That is Gopernicus' opt-in dependency promise in concrete form: **unused adapters do not enter the module graph**.

## Next steps

- Compare the same CMS on Turso in [`examples/cms`](../examples.md#cms-on-turso).
- Read [React and TanStack](../ui/react.md) for a separate web client consuming a Gopernicus API.
- Learn why the packages are split this way in [Repository layout](../architecture/repository-layout.md).
- Build a new composition root with [Compose a host](../guides/compose-host.md).
- Let Workshop create a bare host with [`gopernicus init`](../workshop/commands.md#init).

:::tip Keep the example open

The documentation explains contracts. The examples pin exact imports, lifecycle order, and error handling against current code. Use both together while the public surface is pre-stable.

:::
