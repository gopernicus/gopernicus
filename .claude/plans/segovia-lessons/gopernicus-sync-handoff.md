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

## Flags raised during the sync (Leg 0) — 2026-07-10

Sweep ran 2026-07-10 against gopernicus @ 5f500a5, Segovia v2 (module
`github.com/gpsimpact/segovia/v2`, replaces → this repo's `sdk` +
`integrations/datastores/pgxdb`). Import inventory: `sdk/errs` (18 files),
`sdk/web` (15), `pgxdb` (8), `sdk/identity` (5), `sdk/cryptids` (5),
`sdk/filestorage` (2), `sdk/environment` (2), `sdk/logging` (1).
Items applied: 1 (already in-tree from the 2026-07-09 note), 2, 3.
Items n/a: 4 (no evicted-middleware or workers usage), 5 (no turso),
6 (no direct crud imports; pgxdb symbols used — Config/DB/Open/Tx/
MapError/RunMigrations/StatusCheck — all stable). Item 7 verified live
(`/health` reports a real DB round-trip).

Outcome: **zero code-level flags.** The checklist was accurate and
complete for Segovia's surface — both seds landed, `go build ./...` was
green on the first post-re-path build, no compile errors at any point.
Neither named trap fired (no local `errs` variables; no turso usage).
`make check` (build+vet+test+layer-guards+templ-drift) green; DB-backed
pgx store tests green against the dev DB; app live-driven over HTTP
(spaces/secrets/datasources/grants flow-down resolve/static + ad-report
dashboards incl. manifest CAS + publish) with a clean server log.

1. **Docs nit — "byte-identical output" overclaims in the flag-#2
   resolution note.** The upstream note dropped into Segovia's
   `.planning/gopernicus-v2/04-gopernicus-flags.md` (2026-07-09) calls
   step 1 "mechanical, byte-identical output", but the same file's flag
   #2 row describes `sdk/id` as 26-char lowercase base32 while
   `cryptids.IDGenerator{}` zero-value emits 21-char mixed-case nanoid
   (observed live: `hlC46vc88KlbbF1P4TfTN`). Call sites are mechanical;
   the *output shape* is not identical. Harmless for Segovia v2 (dev DB
   is disposable pre-cutover) but the wording could mislead a host with
   stored IDs into skipping a shape audit. Expected: "mechanical swap;
   ID shape changes from 26-char base32 to 21-char nanoid — audit stored
   IDs if any." No code change wanted.

## The loop protocol reminder

Friction → a numbered flag in `00-overview.md`'s ledger here → ratify →
a phase file → the fix lands in gopernicus with its usual gates. Do not
patch around framework problems inside Segovia — that is the exact
failure mode this intake exists to prevent.
