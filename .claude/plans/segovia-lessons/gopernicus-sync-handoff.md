# Segovia v2 ← gopernicus framework-sync handoff (as of 2026-07-10)

Status: HANDOFF — written at the close of the 2026-07-09/10 milestone run
(authorization-v1 · datastore-hardening · workshop-v2-scaffolding ·
identity-resolution · sdk-layering; gopernicus @ 36 modules / 13 guards).
Owner ruling 2026-07-10: **local `replace` directives stay** until Segovia
v2 is deploy-ready; no tags yet. Consequence: Segovia builds against
gopernicus main, so ALL of the below lands at once on its next build.
The Segovia session runs Leg 0 first; every friction point it hits is a
segovia-lessons flag (this directory is the intake).

## Leg 0 — the breaking-changes checklist (mechanical, do in this order)

1. **`sdk/id` is GONE** (pre-existing owed migration, segovia-lessons
   phase 03/04): `id.New()` → `cryptids.IDGenerator{}.MustGenerate()`
   per-domain, or thread a `Config.IDs` strategy. The cryptids package
   is now at `sdk/foundation/cryptids`.
2. **`sdk/errs` is GONE — it is the root package now** (sdk-layering P1):
   `errs "…/sdk/errs"` imports → `"…/sdk"`; `errs.ErrNotFound` →
   `sdk.ErrNotFound`; `errs.IsExpected` → `sdk.IsExpected`. Watch local
   variables named `errs` (validation-style code) — scope replacements
   to files that imported the package.
3. **Every other sdk import re-pathed** (sdk-layering P3; package NAMES
   unchanged — only import paths):
   - `sdk/{web,workers,identity,crud,cryptids,validation,logging,conversion,slug,async,environment}`
     → `sdk/foundation/<same>`
   - `sdk/{cacher,tracing,email,notify,oauth,filestorage,ratelimiter,events}`
     → `sdk/capabilities/<same>`
   - `sdk/feature` unchanged. Mechanical sed + goimports.
4. **Evicted middlewares renamed** (sdk-layering P2), if Segovia used
   them: `web.CachePages(...)` → `cacher.Pages(...)`; `web.Tracing(t)`
   → `tracing.Middleware(t)`; `web.RequestID/Logger/Panics` unchanged.
   `workers.WithTracer`/`workers.TracingMiddleware` are DELETED (zero
   consumers existed in gopernicus; if Segovia used them, flag it —
   the reintroduction trigger fires and a decorator lands in
   `capabilities/tracing`).
5. **Turso connector write-helpers renamed** (datastore-hardening P5),
   if Segovia hand-writes turso stores: `turso.NullTime(t)` →
   `turso.FormatNullTime(t)`, `turso.NullTimePtr` → `FormatNullTimePtr`
   (`turso.NullTime` is now a scan-side Scanner TYPE). Also: `turso.List`
   now VALIDATES order/PK identifiers strictly — raw expressions are
   rejected (derive a column in a subquery, the roles-store pattern) —
   and `ListQuery.Scan` may be nil for db-tagged row structs
   (`ScanStruct` + `turso.Time`/`turso.NullTime`/`turso.Bool`).
6. **pgx-crud-v1 List standards** (2026-07-08, if not yet absorbed):
   `ListPage[T]` is deleted → `List[T]`/`ListQuery[T]`; order
   allow-lists live in the domain rim (`order.go`); explicit
   `crud.Strategy`; `crud.Limits` per aggregate. `crud` is at
   `sdk/foundation/crud`.
7. **StatusCheck now does a real round-trip** (Ping + SELECT 1) — if
   Segovia wired readiness on it, behavior improves (a dead remote DB
   finally reports); nothing to change.

After the sweep: `go build ./...` in Segovia, then drive the app — the
gopernicus side is fully live-verified, so any residual break is a
missed rename or a Segovia-side assumption (either way: a lessons flag).

## New capabilities available to v2 (adopt on demand, not obligation)

- **`features/authorization`** — the IAM domain: ReBAC relationships +
  roles, independently wireable, three postures (a plain closure remains
  first-class — don't force the flagship). Tenants/spaces/dashboards
  sharing is the natural fit. `features/authorization/README.md` opens
  with the posture decision table.
- **`identity.Resolver`** (`sdk/foundation/identity`) — principal →
  display/contact projection; `auth.Service` implements it. For "who has
  access" UI without sharing the User record.
- **`sdk/capabilities/notify`** + kind-aware invitations — phone/slack/
  any-kind invites, deny-by-absence per wired notifier; email always-on.
  A real SMS provider = the first `integrations/notify/<tech>` module
  (a named trigger Segovia likely fires).
- **The scaffolding CLI** (`workshop/gopernicus`) — `new feature` emits
  the full charter skeleton; `db create/migrate/status` for host
  ledgers. Segovia's next domain is its first real-use test.

## The loop protocol reminder

Friction → a numbered flag in `00-overview.md`'s ledger here → ratify →
a phase file → the fix lands in gopernicus with its usual gates. Do not
patch around framework problems inside Segovia — that is the exact
failure mode this intake exists to prevent.
