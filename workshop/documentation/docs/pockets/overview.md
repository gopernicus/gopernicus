---
title: Pockets
description: Choose and compose complete Gopernicus domain capabilities.
---

# Pockets

Pockets are optional, reusable hexagons. Each core is datastore-free and requires only the SDK; store and view implementations live in sibling modules so a host imports exactly what it chooses.

## Catalog

| Pocket | Capability | HTTP surface | Durable stores | Memory posture |
|---|---|---|---|---|
| [Authentication](authentication.md) | human/machine identity, sessions, credentials, recovery, OAuth, delivery | `/auth/*` JSON; optional HTML | pgx, Turso | example-local full reference |
| [Authorization](authorization.md) | relationship/ReBAC and roles, guarded mutations | none today; namespace reserved | pgx, Turso | public `memstore` |
| [CMS](cms.md) | content registry, taxonomy, menus, media, inquiries | JSON + optional HTML/admin | pgx, Turso | example-local reference |
| [Events](events.md) | durable outbox drain + authenticated SSE gateway | `/events` streams | pgx, Turso | `storetest` reference |
| [Jobs](jobs.md) | durable queue, schedules, keyed/fenced work | none today; namespace reserved | pgx, Turso | public `memstore` |

## Pockets are optional in both directions

No host must use a pocket, and no pocket may import another pocket. This creates three legitimate postures for any capability:

1. do not use it;
2. satisfy a consumer's narrow seam with host code;
3. compose the flagship pocket module.

Authorization makes this especially explicit: a host can leave authorization absent, use a closure over its own data, or wire the full authorization pocket. Other pockets accept check/middleware seams rather than requiring the flagship module.

## The socket pattern

Most pockets follow this shape:

```go
repos := store.Repositories(db)

svc, err := name.NewService(repos, name.Config{
    // host collaborators and policy
})
if err != nil {
    return err
}

if err := svc.Register(pocket.Mount{
    Router: router,
    Logger: log,
    Events: bus,
}); err != nil {
    return err
}
```

Variations are intentional and documented:

- CMS currently builds internally in `cms.Register(mount, repos, cfg)`;
- authorization returns a `Components` bundle because trusted and guarded mutation surfaces must be distinct;
- events subscribes its hub at `NewService`, while the optional outbox poller remains host-driven;
- jobs separates `Service` from `Runtime`, and `Register` starts nothing.

## Choose only needed adapters

A core's public `Repositories` can be filled by:

- a shipped `stores/pgx` module;
- a shipped `stores/turso` module;
- a public or example memory implementation;
- your own adapter.

The host also decides whether to include a sibling `views/goth` implementation. `Views == nil` disables a pocket's HTML surface rather than pulling templ into the core.

## Cross-pocket examples

```go
cms.Config{
    AdminMiddleware: []web.Middleware{authSvc.RequireUser},
}
```

The assignment works because both sides speak the standard `web.Middleware` shape. Neither pocket imports the other.

Authentication's jobs delivery is more involved but follows the same law: authentication depends on SDK work ports; jobs implements them; a host also adapts authentication's execution callbacks onto the jobs fenced runtime.

## Configuration posture

Every field should fall into one of three categories:

- **required**: missing is a construction error;
- **safe default**: zero selects documented behavior;
- **deny by absence**: nil/empty means the subsystem and its routes are off.

There is no default-allow authorization or half-mounted optional subsystem. Read each pocket page before wiring production; authentication in particular has deliberate mode- and subsystem-dependent requirements.

For authoring rules, see the [Pocket contract](../architecture/pocket-contract.md).
