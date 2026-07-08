# Features — the gopernicus feature contract

A **feature** (`features/<name>`) is a pluggable, datastore-free domain module —
the Django-app / Rails-engine unit that lets independent hosts mount the same
domain logic on different infrastructure. `features/cms` is the worked example
this charter generalizes from; `examples/cms` (Turso) and `examples/minimal`
(in-memory, zero libsql in its module graph) both mount it. This document is
the contract the *next* feature (auth, phase 4+) is held to.

## 1. The definition, and the dial

A feature reaches its host through exactly three things (FS2, ratified
2026-07-07 — supersedes this section's earlier two-thing form, whose single
`Register(mount, repos, cfg)` rebuilt the service a host may already hold):

- **Explicit dependencies as data.** A `Repositories` struct of the outbound
  ports the feature needs (the host, or a `stores/<package>` adapter module,
  fills it) and a `Config` struct for view/infrastructure overrides and
  host-registered extensions.
- **One build.** `NewService(repos, cfg) (*Service, error)` validates the
  wiring once and returns the feature's public `Service` — the **driving
  surface**: the use-cases, promoted by thin delegation from the sealed
  interior. This is what a host's own handlers, another feature's port, or
  the shipped transport all consume.
- **Narrow mount, optional.** `svc.Register(mount feature.Mount) error`
  mounts the feature's shipped HTTP surface — an optional convenience
  adapter over the Service. A host may mount it, mount part of it
  (subsystem deny-by-absence), or skip it entirely and write its own
  handlers against the Service. `feature.Mount{Router, Logger}`
  (`sdk/feature`) is the only host context the feature can reach — no
  service locator, no global registry.

There is deliberately **no `init()` registration and no service locator**
(constitution rule 5, `00-overview.md`). This mirrors ordinary Go idiom:
composition roots (`main`) do wiring explicitly, and structural typing (Go
interfaces are satisfied implicitly) means a host's concrete router or
router plugs into `feature.Mount` without either side importing the other's
type. A global registry would hide wiring order and make two hosts
silently share state through package-level variables; explicit `Repositories`
+ `Config` + `Register` keeps every dependency visible at the call site.

## 2. Anatomy

Mirrors `features/cms` and `features/auth`, generalized (trio layout,
ratified 2026-07-02 — `.claude/plans/roadmap/feature-trio-relayout.md`):

| path | contents | visibility |
|---|---|---|
| `<name>.go` | the feature's host-facing exported surface: `Repositories`, `Config`, `NewService(repos, cfg) (*Service, error)`, and the `Service` driving surface with its `Register(mount) error` mount method (FS2) — plus whatever additional exported types host-facing needs require (e.g. auth's `PasswordHasher` port, its `Principal` alias) | public — the socket |
| `logic/<domain>/` (e.g. `logic/content/`, `logic/user/`) | the hexagon's public rim: entities + repository ports (interfaces store adapters and host stores implement) | public **by necessity** — hosts and store modules import these across module boundaries, and Go forbids importing another module's `internal/` |
| `internal/logic/<domain>svc/` | domain services: business rules over the ports, no HTTP/SQL — the hexagon's sealed interior | internal |
| `internal/inbound/http/` | driving adapter: route table (`Mount`), handlers — thin delegations to the Service, writing responses through `sdk/web` responders only (FS9). Views are consumed through the feature's `Views` port, never hardcoded (FS3; cms converges at feature-standard B2) | internal |
| `stores/<package>/` | a **separate module** — the store implementation written against one driver package's API (`stores/pgx`, `stores/turso`; R-KV3), owning its SQL, canonical migrations, and `ExportMigrations` | public API, but never imported by the feature core |
| `storetest/` | the exported conformance suite (`Run(t, newRepos)`) + the test-scoped reference in-memory implementation; every store implementation runs it | public test-support package inside the feature core (stdlib + sdk only — G2 keeps drivers out) |
| `views/<pkg>` (per-concern, only if the feature has HTML) | a **separate module** — the bundled default implementation of the feature core's `Views` port, named for the package it's built on (`views/templ`; R-KV2). The core defines the port (domain-typed params, `web.Renderer` returns) and registers its HTML surface only when `Config.Views` is non-nil — uniform nil → HTML off (FS3). A host wires the default with one import + one Config field, implements the port itself (`html/template` via `web.Template` works in three lines), or wires nothing and runs API-only with zero view tech in its graph. cms's in-core `theme/` (`PublicViews` + `Default()`) is the reference implementation that proved the shape; it migrates to `views/templ` at feature-standard B2 | public API, never imported by the feature core |

**How a feature maps onto the app hexagon** (`internal/{inbound,logic,
outbound}` — ARCHITECTURE.md's app pattern). A feature is the same hexagon,
library-shaped: everted at the port layer, with outbound pushed out of the
module entirely.

| app pattern | feature equivalent | why it moved |
|---|---|---|
| `cmd/` (composition root) | the HOST's `main` + `<name>.go`'s socket | features are composed *by* hosts; `Register` is the wiring point |
| `internal/logic/domains/<d>` (entities + ports + services together) | split by visibility: entities + ports → public `logic/<domain>/`; services → `internal/logic/<domain>svc/` | store modules and hosts must import the ports; services stay sealed so the API surface is exactly the rim |
| `internal/inbound` | `internal/inbound/http/` | same role, same privacy |
| `internal/outbound` | `stores/<package>/` — separate modules | stronger than a directory split: drivers stay out of the core's go.mod entirely (guard G2) |

Reading rule: **`logic/` is what outsiders implement, `internal/` is the
interior wearing the app pattern's names, `stores/` is outbound
module-ized.** The dependency arrows of constitution rule 8 are identical
on both sides.

**Extension model (the four tiers, ratified 2026-07-07, feature-standard
FS1–FS10; extends the `internal/` discipline ratified 2026-07-02):** hosts
customize via deliberate seams — never via interior reach-ins. Every host
need lands in exactly one of four tiers:

1. **Configure** — nil-safe `Config` fields with safe defaults (auth's nil
   `RateLimiter` → in-memory) and **deny-by-absence subsystems** (no
   `Providers` → no OAuth routes; no `Granter` → no invitation routes).
   Absent field = subsystem off, structurally.
2. **Replace a component** — interface-valued `Config` fields with bundled
   defaults (`Views`, FS3) and registered data (cms `Types`/`Templates`,
   jobs `Handlers`). The port lives in the core; defaults that carry a
   dependency ship as sibling modules (FS1/FS4).
3. **Inject at the seams** — middleware fields (cms `AdminMiddleware`
   taking auth's `RequireUser`; structural typing, zero imports either
   way — the C2 pattern, §5).
4. **Extend past the feature** — the public `Service` driving surface
   (FS2): a host writes its own handlers, routes, or flows calling the
   promoted use-cases directly; the shipped transport is never the only
   door.

Configuration is always a `Config` struct with documented nil semantics —
never functional options (FS6; two idioms would deliver nothing the struct
doesn't). Route tables are built as data (`[]feature.Route`) internally;
the public per-route override hook is deliberately unshipped until a real
host hits the gap tiers 1+4 don't cover (FS7). Behavior hooks
(on-register, on-login) ride the events rail when it lands; a sync hook in
`Config` earns a place only with veto/mutate semantics, argued case by
case (FS8). If a host legitimately needs something `internal/` seals, that
is the signal to **add a seam** (a Config field, a port — the
`AdminMiddleware` precedent), not to export the interior. Every exported
symbol is a compatibility promise; the sealed interior is what lets a
feature refactor safely.

**Memory-store placement** (ratified R3, 2026-07-02): the reference in-memory
implementation lives inside `storetest` (test-scoped) by default, and host
memstores (`examples/minimal` pattern) keep the zero-infra proof role. A
feature MAY instead ship its reference as a public in-core package (e.g.
`features/jobs/memstore`) when the implementation is substantial and
host-needed — never as a `stores/memory` module (it isolates no external
dependency, failing constitution rule 2's earn-your-module test). The
2026-07-06 taxonomy amendment widens what an integration may isolate — a
third-party library or an external vendor's live API contract — but a memory
store isolates neither, so this refusal stands.

## 3. The rules

- **sdk-only core** (FS1, ratified 2026-07-07 — supersedes D7's "accept for
  now" via its own revisit clause). A feature core module's `go.mod`
  requires exactly `github.com/gopernicus/gopernicus/sdk` — nothing else
  (the dev-only relative `replace` is permitted pre-tag; a `tool` directive
  counts as a require). Same structural move as sdk's empty `go.mod`.
  Anything carrying a third-party dependency ships as a sibling module:
  persistence → `stores/<pkg>`, presentation → `views/<pkg>`. Sibling
  modules are **per-concern, never mandatory** (FS4): jobs, with no HTTP
  and no views, is a fully conforming feature. Machine-checked in `make
  check`.
- **Datastore-free core.** `features/<name>` never imports `integrations/`,
  `examples/`, or its own `stores/` (or `views/`). Guard G2
  (`make guard-feature-isolation`) enforces this by grep; violating it is a
  build-time architecture bug, not a style note.
- **Transport uses sdk/web** (FS9). Handlers respond through `web.Respond*`
  / `web.Render` / `web.Err*`; a feature-local write helper is a red flag
  in review and a guard failure. When the sdk is missing a capability a
  feature needs, the fix lands in the sdk if it passes `sdk/README.md`'s
  admission policy; otherwise the feature keeps one named local helper
  with a comment citing the failed admission test — never a silent fork of
  an existing sdk responder.
- **Multi-datastore out of the box** (ratified 2026-07-02, DP1 —
  `.claude/plans/roadmap/datastore-portability.md` §2). The supported
  store-implementation set is **{turso, pgx}** — a named, amendable list
  (store modules are named for the driver package they're built on, R-KV3;
  the underlying SQL dialects remain SQLite and PostgreSQL). Every feature ships
  `stores/turso` AND `stores/pgx` (each its own module) plus a reference
  in-memory implementation, all passing the feature's `storetest` conformance
  suite. Parity gates the feature's **v1-milestone close**, not phase order —
  turso-first sequencing inside a milestone is fine. Each `stores/<package>`
  exports the same migration/repository surface (`Repositories(db)`,
  `ExportMigrations(dst)`, plus `MigrationsFS`/`MigrationsDir`), so a host
  switches store by one import and one `Open` call. A feature's dialect trees
  carry an **identical migration version (filename) set — gaps reproduced**;
  after export, the host owns the final ordering in `workshop/migrations/{db}`.
  Live-store conformance is env-gated (`POSTGRES_TEST_DSN`; turso's
  `-tags=integration` + `TURSO_*`) with loud skips — `make check` stays
  hermetic, `make test-stores` expects the env vars, and milestone close
  requires a recorded live run per dialect (a dated NOTES.md artifact), never
  a hermetic green. **Store posture (C, ratified 2026-07-02):** the shipped
  dialect stores are framework-maintained *reference implementations* —
  a feature provably works with ZERO of them (any host may satisfy
  `Repositories` itself; the in-memory proof hosts are the standing
  evidence), and workshop v2's brief gains store *scaffolding* so hosts
  can choose import-vs-own; the migrations and `storetest` suites are the
  durable assets under either delivery mode.
- **Ports public, services internal.** Entities and repository interfaces
  (`content.EntryRepository`, …) are what a store adapter or a host's own
  store implements — they must be importable from outside the module.
  Services (`internal/<domain>svc`) and HTTP (`internal/http`) are
  implementation and stay unexported from the module's public API.
- **No feature → feature imports** (constitution rule 6). Cross-feature needs
  are ports the *consuming* feature declares in its own public package; the
  host wires an implementation, which may be backed by another feature's
  service. See §5 (C2) for the worked example.
- **Migrations are host-owned** (D4 scaffold-and-own). Store adapters expose
  canonical SQL and `ExportMigrations`, but the host merges those files into
  its own per-database directory (`workshop/migrations/{db}`), resolves filename
  conflicts there, and applies that stream pre-boot.
- **Route surface documented + prefixable** (C1, §4 below). A feature must not
  assume it owns `/`; document your claimed namespace and expect a host to be
  able to relocate it.
- **Provable with a zero-infra host.** Every feature must be demonstrable end
  to end without any external infrastructure — an in-memory `Repositories`
  implementation and `go run` is enough (the `examples/minimal` pattern). This
  is what proves the feature is actually datastore-free rather than
  datastore-free in name only.

## 4. C1 — route namespacing

Routes are registered on the host's mux with absolute paths from the
feature's point of view (`r.Handle("GET", "/terms", ...)`), and nothing stops
two features from colliding on a path if a host mounts both at the root. The
contract:

1. **Every feature documents its route surface** and claims a conventional
   namespace. `features/cms`'s convention: admin routes live under each
   registered type's `AdminBase()` (derived from the type's plural, e.g.
   `/articles`) plus fixed paths for taxonomy/menus/media/contact
   (`/terms`, `/menus`, `/media`, `/contact`, …); public routes live under
   each routable type's `PublicBase()` (`/products/{slug}`, or flat at the
   root for hierarchical types with no `RoutePrefix`, e.g. `/{slug}` for
   pages) plus a fixed `GET /{$}` home. See
   `features/cms/internal/http/router.go`'s `Mount` for the literal table.
2. **`feature.PrefixRegistrar`** (`sdk/feature/prefix.go`) wraps a
   `RouteRegistrar` and prefixes every path a feature registers through it, so
   a host *can* mount a feature under `/x/` without the feature's cooperation:

   ```go
   mount := feature.Mount{
       Router: feature.PrefixRegistrar{Prefix: "/blog", Next: router},
       Logger: log,
   }
   svc.Register(mount) // FS2 shape (auth, jobs). cms still takes the
                       // pre-FS2 cms.Register(mount, repos, cfg) until its
                       // public Service lands (feature-standard B3).
   ```

   It normalizes the slash bookkeeping (trailing slash on the prefix, a
   missing leading slash, Go 1.22+ ServeMux's `"{$}"` exact-match suffix for a
   feature's root route) so a host doesn't have to. `""` or `"/"` as `Prefix`
   is a deliberate no-op. Unit-tested in `sdk/feature/prefix_test.go`.
3. **Hosts resolve collisions.** A feature must not assume it owns `/`; if a
   host mounts two features, or a feature alongside its own app-local routes,
   the host is responsible for choosing non-overlapping prefixes (or a single
   feature at the root).

**Known limitation (verified 2026-07-02, real-interaction check).**
`PrefixRegistrar` only changes the path a handler is *registered* under — it
does not rewrite anything the feature's views render. `features/cms`'s public
templates and admin views build links as host-relative absolute paths (e.g.
`href="/articles"`, the menu seed's `"/"`/`"/about"` URLs) rather than
relative to a mount point. Mounting `cms` under a non-root prefix serves the
prefixed routes correctly (confirmed: `GET /demo-prefix/articles` returns 200
and the seeded admin list), but in-page links generated by cms's own views
still point at the un-prefixed root and 404 when followed. This is a real gap,
not a `PrefixRegistrar` bug — fixing it means threading a base-path/URL-builder
through every view, which is real scope for a future milestone (candidate for
phase 5's workshop v2 brief or an auth-adjacent hardening pass), not a
half-fix here. Until then: **treat `PrefixRegistrar` as sound for relocating a
feature that owns its whole route surface (as both current hosts do — cms
mounted at the root)**, and treat prefixed multi-feature mounting as
unproven/unsupported for cms specifically.

## 5. C2 — cross-feature dependencies

Constitution rule 6 (`00-overview.md`): **features never import other
features.** Cross-feature needs are ports the *consuming* feature declares in
its own public package; the host wires an implementation, which may be backed
by another feature's service. Neither feature imports the other — only the
host imports both.

**Worked example — now REAL, not illustrative** (2026-07-02): `features/auth`
exists, and `examples/auth-cms` is the living proof — the host builds
`authSvc, _ := auth.NewService(...)` and passes `authSvc.RequireUser` into
`cms.Config.AdminMiddleware`; cms's admin surface is auth-gated with neither
feature importing the other (verified: the greps in both directions are
empty; the five-step login flow passes over live HTTP). The middleware seam
was the shape v1 actually needed; the narrow-port variant below remains the
general pattern for data-shaped needs (e.g. attributing an inquiry to the
current user):

```go
// features/cms/identity.go (illustrative — not yet built)
type CurrentUser interface {
    CurrentUser(ctx context.Context) (UserID string, ok bool)
}
```

`cms.Config` (or `Repositories`, if it's better modeled as a port the
feature always needs rather than an optional override) carries a value of
this interface. The future `auth` feature's service satisfies it — structural
typing means `auth` never needs to know `cms`'s interface exists, and `cms`
never imports `auth`. The **host**, in its `main`, is the only place that
knows both features exist; it builds the `auth` service and passes it into
`cms.Config` (or wraps it if the shapes don't line up 1:1).

**Corollary: what may graduate into `sdk`.** Per `sdk/README.md`'s admission
policy, the *only* thing allowed to move from a feature-declared port into
`sdk` is genuinely shared **vocabulary** multiple features need identically —
e.g. an identity-in-context convention, or an error sentinel — never a
feature's domain-specific port. `cms`'s `CurrentUser` port above stays in
`cms` unless a second, unrelated feature needs the *identical* shape and the
admission policy's three tests (plurality, narrow + stable, real shared
policy) all hold.

## 6. C3 — Mount evolution policy

`feature.Mount` (`sdk/feature/feature.go`) grows **only** by adding narrow,
single-purpose ports, following the existing `Router` / `Logger` pattern —
one field, one capability, no concrete types. It must never
become a service locator (a field that is itself a bag of unrelated
capabilities) or carry a concrete struct a feature would need to know the
shape of.

Pre-v1, adding a field to
`Mount` is a **compatible change**: hosts construct `Mount` with named struct
fields (`feature.Mount{Router: r, Logger: log}`), so a new zero-value field
never breaks an existing call site.

**Candidate future fields** — named as candidates only, not built
speculatively, added the day a real feature needs them:

- A **jobs registrar** (background/async work a feature wants to schedule),
  if a real feature needs to contribute work registrations to the host.
- An **event bus port** (a feature publishing domain events other features or
  the host might react to), if a genuine multi-feature use case appears —
  today's `cms` has no cross-feature consumer, so this stays speculative.

Do not add either until a feature's design sketch calls for it by name.

## 7. C4 — release & versioning

See `RELEASING.md` at the repo root for the full procedure. Summary: each
module (`sdk`, `integrations/<category>/<tech>`, `features/<name>`,
`features/<name>/stores/<package>`, …) is tagged independently
(`sdk/vX.Y.Z`, `features/cms/vX.Y.Z`, `features/cms/stores/turso/vX.Y.Z`, …).
`go.work` and the store modules' relative `replace` directives are
**workspace-dev-only** and must be dropped (replaced with `require` + a
pinned version) before a module is tagged. Module paths are already rooted at
`github.com/gopernicus/gopernicus/...`; no tags are cut by this phase.

## 8. Authoring checklist (the rails for the next feature)

A literal checklist an executor building a new feature (auth, phase 4+) can
cite by item number:

1. Module compiles standalone (`cd features/<name> && go build ./...`) with
   its own `go.mod`.
2. `go.mod` requires **only** `sdk` (FS1; the D7 view-deps allowance is
   superseded). Datastore drivers live in `stores/<package>`, view tech in
   `views/<package>` — separate modules each.
3. `features/<name>` never imports `integrations/`, `examples/`, or
   `features/<name>/stores/*` (guard G2 covers this once the guard's module
   list is extended to the new feature — flag if it isn't).
4. The feature exposes the FS2 trio: `NewService(repos, cfg) (*Service,
   error)` (loud validation), the `Service` driving surface (use-cases by
   thin delegation), and `svc.Register(mount feature.Mount) error` that
   only reaches `mount.Router` / `mount.Logger`. The shipped transport is
   an optional adapter over the Service — a host must be able to skip it
   and still reach every use-case.
5. Each `stores/<package>` adapter module exposes `Repositories(db)` and
   `ExportMigrations(dst)`; it does not register or apply migrations itself.
6. A minimal-host proof exists: a `go run`-able host with an in-memory (or
   otherwise zero-external-infra) `Repositories` implementation, mirroring
   `examples/minimal`.
7. The feature's own `README.md` (or a section of this charter, for now)
   documents: its route surface + claimed namespace (§4), its `Config`
   fields and what each defaults to, and the ports in `Repositories` a host
   or store adapter must satisfy.
8. No `init()` registration, no package-level mutable registry — everything
   reachable only via `Register`'s explicit parameters.
9. No feature → feature imports (§5); any cross-feature need is a port
   declared in this feature's own public package.
10. A `storetest` conformance package exists and the reference in-memory
    implementation passes it in the feature's own `go test ./...`.
11. Every `stores/<package>` in the supported set ({turso, pgx}, §3)
    exists and passes `storetest`, with the live run recorded as a dated
    NOTES.md artifact (suite, store, DSN class, result) at milestone close.
12. Every optional `Repositories`/`Config`/`Mount` port documents its nil
    semantics — what degraded mode a host gets by not wiring it. Safe
    degradation defaults silently (cms's nil `Cache` disables public
    caching); unsafe degradation errors loudly at construction (auth's nil
    `Hasher`/`Mailer` precedent). A feature never forks into variants —
    optional capability is always a nil-safe port, decided in the host's
    `main`.
