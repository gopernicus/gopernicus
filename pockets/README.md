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
  handlers against the Service. `feature.Mount{Router, Logger, Events}`
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

### FS2 amendment — the authorization `Components` bundle (AUTHORIZATION-SPECIFIC)

`features/authorization` (v3) is the one sanctioned deviation from FS2's
`svc, err := NewService(repos, cfg)` return: its constructor returns a
**`Components{Service, RelationshipWriter, SystemMutator}`** bundle instead of a bare `*Service`
(ratified default #4, authorizationv3). This is an **authorization-specific
amended shape, not a general replacement of FS2** — a sanctioned variant for the
narrow case where a feature has a **separately-held trusted capability** that
must not be reachable from its ordinary driving surface:

- `Components.Service` is the ordinary FS2 driving surface — decisions, lists,
  and actor-facing *guarded* mutations. HTTP handlers and consumer seams receive
  only this. `svc.Register(mount)` is unchanged.
- `Components.RelationshipWriter` is the normal trusted application-side ReBAC
  state capability (schema-valid create/delete and atomic desired-state
  reconciliation). It is independent of the advanced mutation repository.
- `Components.SystemMutator` is the optional high-integrity actor-free command
  capability (revisions, guardian invariants, receipts, audit, teardown). Both are
  **structurally unreachable from `Service`** and is handed by the composition
  root only to code that legitimately needs it. This is the whole point of the
  bundle: capabilities that must be *held apart* cannot be one
  `*Service`, and stapling the trusted methods onto `Service` (or gating them
  behind an `Actor{Kind: system}` flag) would put a self-grant one reflection or
  one constructed-value away.

Why this stays an exception, not the new default (the ruling, with reasoning):
**no other feature in the repo has a SystemMutator-like separately-trusted
capability.** cms, authentication, events, and jobs all return the bare FS2
`*Service` and have no second, deliberately-partitioned surface — a survey of
their `<name>.go` constructors confirms `*Service`-only returns and no
system/trusted sibling. Generalizing `Components` would tax every conforming
feature with a one-member bundle for a capability it does not have. The rule a
future feature applies: **return the bare `*Service` unless you have a second
surface that MUST be partitioned from the driving surface by construction** — in
which case a named `Components{Service, …}` bundle is the sanctioned shape, and
the extra member is a real trust/lifecycle boundary, never mere grouping.

## 2. Anatomy

Mirrors `features/cms` and `features/authentication`, generalized (trio layout,
ratified 2026-07-02 — `.claude/plans/roadmap/feature-trio-relayout.md`):

| path | contents | visibility |
|---|---|---|
| `<name>.go` | the feature's host-facing exported surface: `Repositories`, `Config`, `NewService(repos, cfg) (*Service, error)`, and the `Service` driving surface with its `Register(mount) error` mount method (FS2) — plus whatever additional exported types host-facing needs require (e.g. auth's `PasswordHasher` port, its `Principal` alias) | public — the socket |
| `domain/<domain>/` (e.g. `domain/content/`, `domain/user/`) | the hexagon's public rim: entities + repository ports (interfaces store adapters and host stores implement) | public **by necessity** — hosts and store modules import these across module boundaries, and Go forbids importing another module's `internal/` |
| `internal/logic/<domain>svc/` | domain services: business rules over the ports, no HTTP/SQL — the hexagon's sealed interior | internal |
| `internal/inbound/<feature>/` (e.g. `internal/inbound/cms/` — D1, segovia-lessons phase 01, 2026-07-08) | driving adapter wearing the ratified file anatomy: `routes.go` is the ONE readable route table (a `Mount` dispatcher; per-resource deny-by-absence `mountX` helpers live in their resource files — the authentication shape); per-resource files at resource #2 (`entries.go`, `media.go`, …); transport-named `api.go`/`html.go` only as the single-resource degenerate form; the maximal flatten (single resource, small handler set → handlers stay in `routes.go`; `features/events/internal/inbound/events/routes.go` is the blessed example); **never** `/api`/`/html`/`/htmx` subdirectories. Handlers are thin delegations to the Service, writing responses through `sdk/foundation/web` responders only (FS9); views are consumed through the feature's `Views` port, never hardcoded (FS3; cms converged at feature-standard B2, 2026-07-07). `internal/inbound/http/` means transport plumbing only (middleware), mirroring the app pattern — a feature has none until real plumbing appears | internal |
| `stores/<package>/` | a **separate module** — the store implementation written against one driver package's API (`stores/pgx`, `stores/turso`; R-KV3), owning its SQL, canonical migrations, and `ExportMigrations` | public API, but never imported by the feature core |
| `storetest/` | the exported conformance suite (`Run(t, newRepos)`) + the test-scoped reference in-memory implementation; every store implementation runs it | public test-support package inside the feature core (stdlib + sdk only — G2 keeps drivers out) |
| `views/<pkg>` (per-concern, only if the feature has HTML) | a **separate module** — the bundled default implementation of the feature core's `Views` port, named for the package it's built on (`views/goth`, the ui/goth adapter; R-KV2). The core defines the port (domain-typed params, `web.Renderer` returns) and registers its HTML surface only when `Config.Views` is non-nil — uniform nil → HTML off (FS3). A host wires the default with one import + one Config field, implements the port itself (`html/template` via `web.Template` works in three lines), or wires nothing and runs API-only with zero view tech in its graph. cms's in-core `theme/` (`PublicViews` + `Default()`) was the reference implementation that proved the shape; it migrated to `views/templ` at feature-standard B2 (2026-07-07; the in-core `theme/` is now deleted) | public API, never imported by the feature core |

**How a feature maps onto the app hexagon** (`internal/{inbound,logic,
outbound}` — ARCHITECTURE.md's app pattern). A feature is the same hexagon,
library-shaped: everted at the port layer, with outbound pushed out of the
module entirely.

| app pattern | feature equivalent | why it moved |
|---|---|---|
| `cmd/` (composition root) | the HOST's `main` + `<name>.go`'s socket | features are composed *by* hosts; `Register` is the wiring point |
| `internal/logic/domains/<d>` (entities + ports + services together) | split by visibility: entities + ports → public `domain/<domain>/`; services → `internal/logic/<domain>svc/` | store modules and hosts must import the ports; services stay sealed so the API surface is exactly the rim |
| `internal/inbound/domains/<domain>/` (+ `inbound/http/` plumbing, `inbound/views/` global tree — ARCHITECTURE.md §Inbound anatomy) | `internal/inbound/<feature>/` — the one domain, flattened out of `domains/` | same role, same privacy. The deliberate deltas (FS1/FS3): feature templates never co-locate (templ is third-party, the core is sdk-only) — the render port lives in the core and the bundled default is the `views/<pkg>` sibling module; and there is no feature `inbound/views/` tree — the feature theming seam is embed-the-sibling-default (live override: `examples/cms/internal/theme/`) |
| `internal/outbound` | `stores/<package>/` — separate modules | stronger than a directory split: drivers stay out of the core's go.mod entirely (guard G2) |

Reading rule: **`domain/` is what outsiders implement, `internal/` is the
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
doesn't). Route tables are direct `Handle` calls through the
`RouteRegistrar` seam (`r.Handle("POST", "/auth/login", h.login)`) — the
stringly form is deliberate signposting that a feature registers as a
guest through a one-method port, where an app-local domain uses the
concrete `web.WebHandler`'s verb helpers it owns (ruled 2026-07-08,
segovia-lessons phase 02: a `feature.Methods` verb-sugar wrapper was
built, live-proven, and DECLINED same day as cosmetics-only sdk surface —
resurrect trigger: real host-developer demand). [FS7's `[]feature.Route`
data form SUPERSEDED 2026-07-08: shipped at feature-standard with zero
consumers, cut as premature; it returns when a real host needs a
declarative route table.] The public per-route override hook remains
deliberately unshipped — a host that needs to deny/replace/re-path a
single route wraps the registrar (§4 item 3), which covers the gap tiers
1+4 don't. Behavior hooks
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
- **Transport uses sdk/foundation/web** (FS9). Handlers respond through `web.Respond*`
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
  Services (`internal/logic/<domain>svc`) and HTTP
  (`internal/inbound/<feature>`) are implementation and stay unexported
  from the module's public API. A feature MAY additionally export HTTP
  middleware gates as **root-package re-exports of internal implementations**
  (`authentication.RequireUser`, `authorization.RequirePermission`) — the root
  package writes no HTTP itself; the handler body lives in `internal/logic`, so
  this reinforces rather than amends the internal-HTTP rule
  (middleware-consolidation, 2026-07-11).
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
   `features/cms/internal/inbound/cms/routes.go`'s `Mount` for the literal
   table.
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
3. **Per-route override — wrap the registrar** (the route-level face of
   extension tier 4; segovia-lessons phase 02, 2026-07-08). `Mount.Router`
   is a one-method interface precisely so a host can interpose on
   registrations in code it cannot edit: deny a route, swap its handler,
   re-path it, or add middleware to exactly one. ~8 lines of host code, no
   framework support needed:

   ```go
   type inviteOnly struct{ feature.RouteRegistrar }

   func (o inviteOnly) Handle(method, path string, h http.HandlerFunc, mw ...web.Middleware) {
       if method == "POST" && path == "/auth/register" {
           return // invite-only app: the route is never mounted
       }
       o.RouteRegistrar.Handle(method, path, h, mw...)
   }
   ```

   Pass it as `Mount.Router`; it composes freely with `PrefixRegistrar` and
   `Group`, since each wrapper is itself a `RouteRegistrar`. For
   anything bigger than a route or two, use tier 4 proper — skip
   `svc.Register` and hand-route over the public `Service`. This wrapper
   pattern is why FS7's public override hook stays unshipped.
4. **Hosts resolve collisions.** A feature must not assume it owns `/`; if a
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

**Worked example — now REAL, not illustrative** (2026-07-02): `features/authentication`
exists, and `examples/auth-cms` is the living proof — the host builds
`authSvc, _ := auth.NewService(...)` and passes `authSvc.RequireUser` into
`cms.Config.AdminMiddleware`; cms's admin surface is auth-gated with neither
feature importing the other (verified: the greps in both directions are
empty; the five-step login flow passes over live HTTP). The middleware seam
was the shape v1 actually needed; the narrow-port variant below remains the
general pattern for data-shaped needs (e.g. attributing an inquiry to the
current user).

**Second worked example (2026-07-09, authorization-v1 Z4):** the same host
wires `features/authorization` into TWO consumer-declared seams with zero
feature→feature imports — auth's `Granter` (a host-local adapter whose
`Grant` calls `authorizer.CreateRelationships`; invitation-accept writes a
real ReBAC tuple) and events' `Authorize` (a closure delegating to
`authorizer.Check` gates the resource-scoped SSE stream). Both seams are
Check-only shapes any host closure can satisfy instead — the middle
posture, recorded as the Z4 commit-1 artifact (`2e1e5eb`):

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
feature's domain-specific port. **CASHED for identity-in-context, 2026-07-08
(events-v1 A-I1): `sdk/foundation/identity` — authentication stashes the Principal, the
events gateway reads it; vocabulary only, fails closed.** The `CurrentUser`
port above stays as the general C2 pattern for domain-shaped needs.
**Grown 2026-07-10 (identity-resolution):** the display/contact
projection of a principal is now `sdk/foundation/identity.Resolver` (host-wired;
authentication implements it) and delivery is `sdk/capabilities/notify` — but
domain-shaped needs (e.g. `CurrentUser`) STAY consumer-declared C2
ports; the graduation bar is unchanged. `cms`'s `CurrentUser` port above stays in
`cms` unless a second, unrelated feature needs the *identical* shape and the
admission policy's three tests (plurality, narrow + stable, real shared
policy) all hold.

**Amended 2026-07-13 (sdk-work-protocol): the ratified-platform-protocol
graduation path.** Consumer-declared ports remain the DEFAULT — this
amendment adds an exception, it does not weaken the rule. A shape may
graduate into sdk only as a **ratified platform protocol**, meeting ALL five
criteria:

1. a real producer and a real consumer in separate modules,
2. semantics meant to be canonical across gopernicus,
3. no feature aggregate or persistence model in the contract,
4. narrow enough for independent implementations,
5. a conformance suite can describe observable behavior.

The five criteria are CONJUNCTIVE WITH — never a substitute for —
`sdk/README.md`'s admission policy and ARCHITECTURE.md's five-point
sdk-vs-logic test: a graduation must pass all three gates, so this path can
never become an admission back door. The first graduation under this path is
the keyed-work submission protocol, `sdk/capabilities/work`, implemented by
`features/jobs` as the implementation of record; its lifecycle vocabulary is
the frozen seven-value status set (`pending`/`running`/`completed`/`failed`/
`dead_letter`/`canceled`/`superseded` — `failed` is non-terminal), adopted
verbatim from the persisted jobs strings. Authorization's check/decision
vocabulary explicitly **fails criterion 2 today** and stays consumer-declared
(deferred; trigger: authorizationv3 settles its semantics).

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

**Built from this list** (C3's sanctioned process — added the day a real
feature needed it):

- The **event bus port** — `Events events.Emitter`, added at events-v1 when
  cms's first emit call landed (the SSE gateway + a host-side subscriber
  are the multi-feature consumers that ended its speculative status).
  Emit-only and **best-effort at-most-once** (`Mount.Events` is never
  transactional — an event is lost on a crash between commit and emit);
  durable delivery rides a feature's own `Repositories` (the events
  feature's outbox), never this field. Nil → the feature emits nothing.

**Candidate future fields** — named as candidates only, not built
speculatively, added the day a real feature needs them:

- A **jobs registrar** (background/async work a feature wants to schedule),
  if a real feature needs to contribute work registrations to the host.

Do not add it until a feature's design sketch calls for it by name.

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
   For a MULTI-KIND store with boot-time table probes the accepted surface is
   `Repositories(db) (<feature>.Repositories, error)` — the bundle name WITH
   an error return (authorization-v1 refinement 11; a deliberate hybrid of
   jobs' error-less bundle and events' probing single-Store `New(db)`).
   A store never calls a connector's `Underlying()` (guard G10's sibling G9
   enforces it); a future cross-repository transaction consumes the
   scaffolded `crud.Transactor` seam (`sdk/foundation/crud/tx.go`) instead.
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
13. Every crud-paginated list port follows the pgx-crud-v1 standards
    (`sdk/foundation/crud`'s package doc is normative): the aggregate declares its
    order allow-list (`map[string]crud.OrderField`) + default `crud.Order`
    in its feature-core domain package (indexed spine columns only — EAV
    fields are never sortable); the storetest suite carries the standard
    six-case family per paginated port (`Order`, `PrevPage`, `OffsetMode`,
    `WithCount`, `StaleCursorOrderChange`, `CursorOffsetExclusive`); and
    any HTTP list endpoint parses the standard query-param vocabulary —
    `limit`, `cursor`, `offset`, `count`, `order=field:direction` — strict
    (400) at JSON edges, clamp/fallback at SSR edges. Store adapters build
    on the connector `List[T]` helpers (`pgxdb.List` / `turso.List`), never
    hand-rolled pagination. An aggregate whose resource needs non-default
    page sizes declares a `var ListLimits = crud.Limits{…}` beside its
    `OrderFields`/`DefaultOrder`, and its stores and handlers pass it to
    `req.NormalizedLimit` / `crud.ListParams.Limits`; the zero value keeps
    `crud`'s `DefaultLimit`/`MaxLimit`.
14. Entity-ID strategy (segovia-lessons phase 04, amended D9/D10): the
    feature `Config` carries `IDs cryptids.IDGenerator` (zero value → the
    nanoid default) and threads it to every ENTITY-KEY constructor; opaque
    secrets (session tokens, verification codes, minted key material)
    never follow it — they keep a package-private unconditional random
    generator with a doc saying why. Every entity-keyed store `Create`
    honors the empty-ID convention: empty ID in → omit the id column, read
    the schema default back with `RETURNING id` (the dialect's
    `NNNN_id_defaults.sql` migration supplies the default); the storetest
    suite carries a `DBGeneratedIDOnEmpty` case per entity family, and the
    reference/mem implementations assign at insert. IDs are `string` end
    to end — a resource that genuinely needs an int key models it as an
    explicit int field (future, per-resource), never via casting or
    generics.
