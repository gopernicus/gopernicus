---
title: Pockets
description: Choose and compose complete Gopernicus domain capabilities.
---

# Pockets

Pockets are optional, reusable hexagons. Each core is datastore-free and requires only SDK and the shared pockets contract; store and view implementations live in sibling modules so a host imports exactly what it chooses.

## Catalog

| Pocket | Capability | HTTP surface | Durable stores | Memory posture |
|---|---|---|---|---|
| [Authentication](authentication.md) | human/machine identity, sessions, credentials, recovery, OAuth, delivery | `/auth/*` JSON; optional HTML | pgx, Turso | example-local full reference |
| [Authorization](authorization.md) | relationship/ReBAC and roles, guarded mutations | optional role administration and reusable permission middleware | pgx, Turso, Firestore | public `stores/memory` |
| [CMS](cms.md) | content registry, taxonomy, menus, media, inquiries | JSON + optional HTML/admin | pgx, Turso | example-local reference |
| [Events](events.md) | durable outbox drain + authenticated SSE gateway | `/events` streams | pgx, Turso | `storetest` reference |
| [Jobs](jobs.md) | durable queue, schedules, keyed/fenced work | none today; namespace reserved | pgx, Turso | public `stores/memory` |

## Pockets are optional in both directions

No host must use a pocket, and no pocket may import another pocket. This creates three legitimate postures for any capability:

1. do not use it;
2. satisfy a consumer's narrow seam with host code;
3. compose the flagship pocket module.

Authorization makes this especially explicit: a host can leave authorization absent, use a closure over its own data, or wire the full authorization pocket. Other pockets accept check/middleware seams rather than requiring the flagship module.

## Public services and optional adapters

Each audited pocket exposes focused services under `logic/` and a small root
constructor that assembles named components. HTTP pockets expose `inbound/http`:
mount bundled routes, use supported handlers or middleware on host routes, or
call the logic services directly from another transport.

- Authentication assembles authentication, invitations, delivery and HTTP.
- Authorization separates decisions, relationship reads, role reads and guarded
  mutations from its separately held trusted writers and public HTTP adapter.
- Events assembles a filtered streams service and optional HTTP adapter; its
  outbox poller remains host-driven.
- Jobs assembles queue and schedule services; the host constructs and runs workers.
- CMS retains `cms.Register(mount, repos, cfg)` and its earlier layout pending audit.

A pocket with only one service can return it directly. A component bundle earns
its place through assembly, lifecycle or capability separation, not uniformity.

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
    AdminMiddleware: []web.Middleware{authenticationComponents.HTTP.RequireAccessToken()},
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
