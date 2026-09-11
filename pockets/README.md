# Pockets — the gopernicus pocket contract

A **pocket** (`pockets/<name>`) is a pluggable, datastore-free domain module —
the Django-app / Rails-engine unit that lets independent hosts mount the same
domain logic on different infrastructure. `pockets/cms` is the worked example
this charter generalizes from; `examples/cms` (Turso) and `examples/minimal`
(in-memory, zero libsql in its module graph) both mount it. This document is
the contract the *next* pocket (auth, phase 4+) is held to.

The root `pockets` package owns `Mount`, `RouteRegistrar`, `PrefixRegistrar` and
`Group`. It is an independent module over SDK. Concrete pockets keep their own
modules in direct subdirectories; importing the shared contract imports none of
them. SDK never imports this module.

## 1. The definition, and the dial

Rule ids on this page are historical identifiers cross-referenced from ratified
plans and are not renamed: FS = the former "feature standard" series.

A pocket exposes explicitly constructed services and optional inbound adapters
(FS2, amended by the owner on 2026-09-11):

- **Explicit dependencies.** The host supplies repository ports and configuration.
  Every public constructor validates the dependencies it requires; direct component
  construction must be as safe as construction through the pocket root.
- **Focused public services.** Real use cases live in `logic/<concern>` packages,
  alongside the types and ports they own. A host may construct those services
  directly or use the root's convenience constructor. Private fields and helpers
  encapsulate implementation; a forwarding façade is not required.
- **Convenient composition.** A root constructor may return the one service it
  builds, or a `Components` value with named services, adapters and runtimes. A
  component bundle represents real assembly and lifecycle or capability boundaries;
  it is not another universal service. No component starts background workers
  implicitly unless its construction contract explicitly says so.
- **Public optional transport.** `inbound/http` owns HTTP middleware, handlers,
  response mapping and bundled route registration. Hosts may mount its routes,
  use its middleware or supported handlers on their own routes, or call services
  from other transports. Route registration uses `pockets.Mount`/`RouteRegistrar`;
  a transport-free pocket needs no placeholder `Register` method.

There is no `init()` registration, global registry, or service locator. Logic
imports neither the root composition package nor inbound. Assembly points inward
to services and adapters, and adapters consume narrowly declared logic contracts.

### Authorization capabilities

Authorization's ordinary mutation service retains actor guards and atomic
model/guardian validation. `RelationshipWriter` and `SystemMutator` are separate
trusted capabilities handed out by the composition root only to code that needs
them. They must remain unreachable through ordinary decision, relationship-read,
role-read and guarded-mutation services. Splitting role and relationship services
must not turn raw store assignment methods into ordinary guarded use cases or
split one atomic guard/write/audit unit into independent writes.

This separation does not require every pocket to have the same components.
CMS retains its earlier public API and directory layout until its deferred audit.

## 2. Anatomy

Pockets share the application's inbound / logic / outbound responsibilities,
with public contracts for external hosts and adapter authors. The public API is
deliberate; a second service consisting only of forwarding methods is not required.

| path | contents | visibility |
|---|---|---|
| root Go files | dependencies/configuration and construction of named components | small public assembly surface |
| `logic/<concern>/` | real services, workflows, owned types and consumed ports | public supported APIs; private fields and helpers |
| `inbound/http/` | HTTP middleware, supported handlers, response mapping and route registration | public adapter with validated construction |
| `internal/<concern>/` | genuinely private engines, helpers or test support, when needed | internal; not a mandatory layer |
| `stores/memory/` | optional usable memory adapter | public package in the pocket core module |
| `stores/storetest/` | shared conformance suite and test-only reference helpers | public test-support package in the core module |
| `stores/<driver>/` | driver adapter, queries, canonical migrations/indexes and export helpers | separate module; never imported by core production code |
| `views/<pkg>/` | optional bundled implementation of a public Views port | separate module; never imported by core production code |

`stores/` groups outbound persistence packages; it has no package or go.mod.
Memory and conformance introduce no drivers or additional module versions.
CMS remains on its earlier layout while its audit is deferred.

Domain vocabulary belongs with its owning logic package. A shared inward model
package is useful only where distinct services need a common independent contract;
`domain/` is not a compulsory directory. Avoid duplicate transport commands whose
only purpose is to work around root/inbound import cycles.

**Application-to-pocket mapping:**

| app responsibility | pocket location |
|---|---|
| composition root | host `cmd/` plus the pocket root's assembly |
| domain contracts and use cases | public `logic/<concern>/` |
| inbound adapters | public `inbound/http/` |
| outbound persistence | `stores/memory/` or separate `stores/<driver>/` modules |
| presentation implementation | optional `views/<pkg>/` module |

Go's `internal` restriction follows the parent import tree, not the go.mod
boundary. External hosts cannot import private packages. Dependency guards still
protect inward direction, including public logic and in-module store support.
Exporting a package does not require exporting its handler structs, state, or
implementation helpers. Export only the construction and behavior hosts should use.

**Extension model (the four tiers, ratified 2026-07-07, feature-standard
FS1–FS10; extends the `internal/` discipline ratified 2026-07-02):** hosts
customize via deliberate seams — never via interior reach-ins. Every host
need lands in exactly one of four tiers:

1. **Configure** — options and policy fields with documented defaults (auth's nil
   `RateLimiter` → in-memory) and **deny-by-absence subsystems** (no
   `Providers` → no OAuth routes; no `Granter` → no invitation routes).
   Each optional capability documents its disabled/default behavior.
2. **Replace a component** — interface-valued options and policy fields with bundled
   defaults (`Views`, FS3) and registered data (cms `Types`/`Templates`,
   jobs `Handlers`). The port lives in the core; defaults that carry a
   dependency ship as sibling modules (FS1/FS4).
3. **Inject at the seams** — middleware fields (cms `AdminMiddleware`
   taking auth's `RequireUser`; structural typing, zero imports either
   way — the C2 pattern, §5).
4. **Compose public components** — a host uses focused logic services or public
   inbound adapters from its own handlers, routes, and workflows. The shipped
   route table is optional; a host can use its middleware independently.

Construction keeps required dependencies explicit. Independent optional settings
use typed `WithFoo(...)` options over private construction settings; coherent
policy and model records remain data supplied through named feature options,
such as `WithPassword(PasswordConfig{...})`. Feature options replace whole records,
including zero fields. Required modes, signers, buses and handler registries stay
explicit; named input records can group related required ports. The
owner's constructor audit (2026-09-11) replaces FS6's former blanket prohibition
on functional options. Document defaults, nil arguments and ordering; validate
the resolved configuration before returning a usable component. Options are
construction inputs, not setters on running services. See
[pocket-constructor-options.md](../plans/pocket-constructor-options.md) for the
owner-approved follow-up. Use `New` / `NewX` for functions and **constructor**
for the noun and dedicated filenames (`constructor.go`). Short constructors may
stay in their service files; no file split is required just for naming uniformity.

Route tables are direct `Handle` calls through the
`RouteRegistrar` seam (`r.Handle("POST", "/auth/login", h.login)`) — the
stringly form is deliberate signposting that a pocket registers as a
guest through a one-method port, where an app-local domain uses the
concrete `web.WebHandler`'s verb helpers it owns (ruled 2026-07-08,
segovia-lessons phase 02: a `pocket.Methods` verb-sugar wrapper was
built, live-proven, and DECLINED same day as cosmetics-only sdk surface —
resurrect trigger: real host-developer demand). [FS7's `[]pocket.Route`
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
`AdminMiddleware` precedent), or deliberately support a public component. Every exported symbol is a
compatibility promise; public packages still keep implementation details private.

**Memory-store placement** (amended by the owner, 2026-09-11): usable memory
adapters live at `stores/memory` in the pocket core module. They are optional
imports, useful to tests and hosts without external infrastructure. Shared
conformance lives alongside them at `stores/storetest` in the same module.
Reference fakes that exist only for tests need not become advertised adapters.
Neither package gets its own go.mod: neither isolates an external dependency.
The core service depends on domain ports and never imports a concrete memory
store. Core tests may import their own memory/conformance
packages. Test support and memory remain covered by the core dependency rules.

## 3. The rules

- **SDK and shared-contract dependencies only** (FS1). A concrete pocket core's
  `go.mod` may require `github.com/gopernicus/gopernicus/sdk` and the exact
  `github.com/gopernicus/gopernicus/pockets` module. The shared module requires
  SDK only and never imports concrete pockets. Development-only local replaces
  are permitted pre-tag; a `tool` directive counts as a dependency.
  Anything carrying a third-party dependency ships as a sibling module:
  persistence → `stores/<pkg>`, presentation → `views/<pkg>`. Sibling
  modules are **per-concern, never mandatory** (FS4): jobs, with no HTTP
  and no views, is a fully conforming pocket. Machine-checked in `make
  check`.
- **Datastore-free core.** `pockets/<name>` never imports `integrations/`,
  `examples/`, or concrete `stores/` / `views/` adapters in production code.
  Memory and conformance are core-module packages but outbound/test concerns;
  only those packages and core tests may import that store support.
  Guard G2 follows actual module boundaries and checks parsed imports, including
  the core packages nested under `stores/`.
- **Transport uses sdk/pkg/web** (FS9). Handlers respond through `web.Respond*`
  / `web.Render` / `web.Err*`; a pocket-local write helper is a red flag
  in review and a guard failure. When the sdk is missing a capability a
  pocket needs, the fix lands in the sdk if it passes `sdk/README.md`'s
  admission policy; otherwise the pocket keeps one named local helper
  with a comment citing the failed admission test — never a silent fork of
  an existing sdk responder.
- **Multi-datastore out of the box** (ratified 2026-07-02, DP1 —
  `.claude/plans/roadmap/datastore-portability.md` §2). The supported
  store-implementation set is **{turso, pgx}** — a named, amendable list
  (store modules are named for the driver package they're built on, R-KV3;
  the underlying SQL dialects remain SQLite and PostgreSQL). Every pocket ships
  `stores/turso` AND `stores/pgx` (each its own module) plus a reference
  in-memory implementation, all passing the pocket's `storetest` conformance
  suite. Parity gates the pocket's **v1-milestone close**, not phase order —
  turso-first sequencing inside a milestone is fine. Each `stores/<package>`
  exports the same migration/repository surface (`Repositories(db)`,
  `ExportMigrations(dst)`, plus `MigrationsFS`/`MigrationsDir`), so a host
  switches store by one import and one `Open` call. A pocket's dialect trees
  carry an **identical migration version (filename) set — gaps reproduced**;
  after export, the host owns the final ordering in `workshop/migrations/{db}`.
  **Firestore is an OPTIONAL third family, not a DP1 requirement**
  (firestore-stores, 2026-09-09): `{turso, pgx}` remains the parity bar every
  pocket must clear, and `stores/firestore` ships only where a host demand
  exists — authentication and authorization in that milestone; cms, events, and
  jobs get none. It is also the family that does NOT join a host's ambient
  `transaction.Transactor` transaction (ruling R1): a Firestore transaction sees none
  of its own pending writes, so `storetest.RunTransactional` skips loudly there
  and a store method handed a connector transaction fails loud rather than
  splitting atomicity. Each store README states which family a host gets.
  Live-store conformance is env-gated (`POSTGRES_TEST_DSN`; turso's
  `-tags=integration` + `TURSO_*`; firestore's `-tags=integration` +
  `FIRESTORE_EMULATOR_HOST`, with a separate live-project leg) with loud skips
  — `make check` stays hermetic, `make test-stores` expects the env vars, and milestone close
  requires a recorded live run per dialect (a dated NOTES.md artifact), never
  a hermetic green. **Store posture (C, ratified 2026-07-02):** the shipped
  dialect stores are framework-maintained *reference implementations* —
  a pocket provably works with ZERO of them (any host may satisfy
  `Repositories` itself; the in-memory proof hosts are the standing
  evidence), and workshop v2's brief gains store *scaffolding* so hosts
  can choose import-vs-own; the migrations and `storetest` suites are the
  durable assets under either delivery mode.
- **Deliberate public API, private implementation details.** Entities and repository interfaces
  (`content.EntryRepository`, …) are what a store adapter or a host's own
  store implements — they must be importable from outside the module.
  Focused public logic services own use cases; root constructors assemble them.
  HTTP handlers, middleware and response mapping live in public `inbound/http`.
  Production logic never imports HTTP, SDK web, root composition or inbound
  packages (G25; CMS's structure remains deferred).
- **No pocket → pocket imports** (constitution rule 6). Cross-pocket needs
  are ports the *consuming* pocket declares in its own public package; the
  host wires an implementation, which may be backed by another pocket's
  service. See §5 (C2) for the worked example.
- **Migrations are host-owned** (D4 scaffold-and-own). Store adapters expose
  canonical SQL and `ExportMigrations`, but the host merges those files into
  its own per-database directory (`workshop/migrations/{db}`), resolves filename
  conflicts there, and applies that stream pre-boot.
- **Route surface documented + prefixable** (C1, §4 below). A pocket must not
  assume it owns `/`; document your claimed namespace and expect a host to be
  able to relocate it.
- **Provable with a zero-infra host.** Every pocket must be demonstrable end
  to end without any external infrastructure — an in-memory `Repositories`
  implementation and `go run` is enough (the `examples/minimal` pattern). This
  is what proves the pocket is actually datastore-free rather than
  datastore-free in name only.

## 4. C1 — route namespacing

Routes are registered on the host's mux with absolute paths from the
pocket's point of view (`r.Handle("GET", "/terms", ...)`), and nothing stops
two pockets from colliding on a path if a host mounts both at the root. The
contract:

1. **Every pocket documents its route surface** and claims a conventional
   namespace. `pockets/cms`'s convention: admin routes live under each
   registered type's `AdminBase()` (derived from the type's plural, e.g.
   `/articles`) plus fixed paths for taxonomy/menus/media/contact
   (`/terms`, `/menus`, `/media`, `/contact`, …); public routes live under
   each routable type's `PublicBase()` (`/products/{slug}`, or flat at the
   root for hierarchical types with no `RoutePrefix`, e.g. `/{slug}` for
   pages) plus a fixed `GET /{$}` home. See
   `pockets/cms/internal/inbound/cms/routes.go`'s `Mount` for the literal
   table.
2. **`pockets.PrefixRegistrar`** (`pockets/routes.go`) wraps a
   `RouteRegistrar` and prefixes every path a pocket registers through it, so
   a host *can* mount a pocket under `/x/` without the pocket's cooperation:

   ```go
   mount := pockets.Mount{
       Router: pockets.PrefixRegistrar{Prefix: "/blog", Next: router},
       Logger: log,
   }
   svc.Register(mount) // FS2 shape (auth, jobs). cms still takes the
                       // pre-FS2 cms.Register(mount, repos, cfg) until its
                       // public Service lands (feature-standard B3).
   ```

   It normalizes the slash bookkeeping (trailing slash on the prefix, a
   missing leading slash, Go 1.22+ ServeMux's `"{$}"` exact-match suffix for a
   pocket's root route) so a host doesn't have to. `""` or `"/"` as `Prefix`
   is a deliberate no-op. Unit-tested in `pockets/routes_test.go`.
3. **Per-route override — wrap the registrar** (the route-level face of
   extension tier 4; segovia-lessons phase 02, 2026-07-08). `Mount.Router`
   is a one-method interface precisely so a host can interpose on
   registrations in code it cannot edit: deny a route, swap its handler,
   re-path it, or add middleware to exactly one. ~8 lines of host code, no
   framework support needed:

   ```go
   type inviteOnly struct{ pockets.RouteRegistrar }

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
4. **Hosts resolve collisions.** A pocket must not assume it owns `/`; if a
   host mounts two pockets, or a pocket alongside its own app-local routes,
   the host is responsible for choosing non-overlapping prefixes (or a single
   pocket at the root).

**Known limitation (verified 2026-07-02, real-interaction check).**
`PrefixRegistrar` only changes the path a handler is *registered* under — it
does not rewrite anything the pocket's views render. `pockets/cms`'s public
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
pocket that owns its whole route surface (as both current hosts do — cms
mounted at the root)**, and treat prefixed multi-pocket mounting as
unproven/unsupported for cms specifically.

## 5. C2 — cross-pocket dependencies

Constitution rule 6 (`00-overview.md`): **pockets never import other
pockets.** Cross-pocket needs are ports the *consuming* pocket declares in
its own public package; the host wires an implementation, which may be backed
by another pocket's service. Neither pocket imports the other — only the
host imports both.

**Worked example — now REAL, not illustrative** (2026-07-02): `pockets/authentication`
exists, and `examples/auth-cms` is the living proof — the host builds
`authSvc, err := auth.New(...)`, handles construction errors, and passes
`authSvc.HTTP.RequireAccessToken()` into
`cms.Config.AdminMiddleware`; cms's admin surface is auth-gated with neither
pocket importing the other (verified: the greps in both directions are
empty; the five-step login flow passes over live HTTP). The middleware seam
was the shape v1 actually needed; the narrow-port variant below remains the
general pattern for data-shaped needs (e.g. attributing an inquiry to the
current user).

**Second worked example (2026-07-09, authorization-v1 Z4):** the same host
wires `pockets/authorization` into TWO consumer-declared seams with zero
pocket→pocket imports — auth's `Granter` (a host-local adapter whose
`Grant` calls `authorizer.CreateRelationships`; invitation-accept writes a
real ReBAC tuple) and events' `Authorize` (a closure delegating to
`authorizer.Check` gates the resource-scoped SSE stream). Both seams are
Check-only shapes any host closure can satisfy instead — the middle
posture, recorded as the Z4 commit-1 artifact (`2e1e5eb`):

```go
// pockets/cms/identity.go (illustrative — not yet built)
type CurrentUser interface {
    CurrentUser(ctx context.Context) (UserID string, ok bool)
}
```

`cms.Config` (or `Repositories`, if it's better modeled as a port the
pocket always needs rather than an optional override) carries a value of
this interface. The future `auth` pocket's service satisfies it — structural
typing means `auth` never needs to know `cms`'s interface exists, and `cms`
never imports `auth`. The **host**, in its `main`, is the only place that
knows both pockets exist; it builds the `auth` service and passes it into
`cms.Config` (or wraps it if the shapes don't line up 1:1).

**Corollary: what may graduate into `sdk`.** Per `sdk/README.md`'s admission
policy, the *only* thing allowed to move from a pocket-declared port into
`sdk` is genuinely shared **vocabulary** multiple pockets need identically —
e.g. an identity-in-context convention, or an error sentinel — never a
pocket's domain-specific port. **CASHED for identity-in-context, 2026-07-08
(events-v1 A-I1): `sdk` — authentication stashes the Principal, the
events gateway reads it; vocabulary only, fails closed.** The `CurrentUser`
port above stays as the general C2 pattern for domain-shaped needs.
**Grown 2026-07-10 (identity-resolution):** the display/contact
projection of a principal is now `sdk.IdentityResolver` (host-wired;
authentication implements it) and delivery is `sdk/capabilities/notify` — but
domain-shaped needs (e.g. `CurrentUser`) STAY consumer-declared C2
ports; the graduation bar is unchanged. `cms`'s `CurrentUser` port above stays in
`cms` unless a second, unrelated pocket needs the *identical* shape and the
admission policy's three tests (plurality, narrow + stable, real shared
policy) all hold.

**Amended 2026-07-13 (sdk-work-protocol): the ratified-platform-protocol
graduation path.** Consumer-declared ports remain the DEFAULT — this
amendment adds an exception, it does not weaken the rule. A shape may
graduate into sdk only as a **ratified platform protocol**, meeting ALL five
criteria:

1. a real producer and a real consumer in separate modules,
2. semantics meant to be canonical across gopernicus,
3. no pocket aggregate or persistence model in the contract,
4. narrow enough for independent implementations,
5. a conformance suite can describe observable behavior.

The five criteria are CONJUNCTIVE WITH — never a substitute for —
`sdk/README.md`'s admission policy and ARCHITECTURE.md's five-point
sdk-vs-logic test: a graduation must pass all three gates, so this path can
never become an admission back door. The first graduation under this path is
the keyed-work submission protocol, `sdk/capabilities/work`, implemented by
`pockets/jobs` as the implementation of record; its lifecycle vocabulary is
the frozen seven-value status set (`pending`/`running`/`completed`/`failed`/
`dead_letter`/`canceled`/`superseded` — `failed` is non-terminal), adopted
verbatim from the persisted jobs strings. Authorization's check/decision
vocabulary explicitly **fails criterion 2 today** and stays consumer-declared
(deferred; trigger: authorizationv3 settles its semantics).

## 6. C3 — Mount evolution policy

`pockets.Mount` (`pockets/mount.go`) grows **only** by adding narrow,
single-purpose ports, following the existing `Router` / `Logger` pattern —
one field, one capability, no concrete types. It must never
become a service locator (a field that is itself a bag of unrelated
capabilities) or carry a concrete struct a pocket would need to know the
shape of.

Pre-v1, adding a field to
`Mount` is a **compatible change**: hosts construct `Mount` with named struct
fields (`pockets.Mount{Router: r, Logger: log}`), so a new zero-value field
never breaks an existing call site.

**Built from this list** (C3's sanctioned process — added the day a real
pocket needed it):

- The **event bus port** — `Events events.Emitter`, added at events-v1 when
  cms's first emit call landed (the SSE gateway + a host-side subscriber
  are the multi-pocket consumers that ended its speculative status).
  Emit-only and **best-effort asynchronous notification** (`Mount.Events` is never
  transactional — an event is lost on a crash between commit and emit);
  durable delivery rides a pocket's own `Repositories` (the events
  pocket's outbox), never this field. Nil → the pocket emits nothing.

**Candidate future fields** — named as candidates only, not built
speculatively, added the day a real pocket needs them:

- A **jobs registrar** (background/async work a pocket wants to schedule),
  if a real pocket needs to contribute work registrations to the host.

Do not add it until a pocket's design sketch calls for it by name.

## 7. C4 — release & versioning

See `RELEASING.md` at the repo root for the full procedure. Summary: each
module (`sdk`, `integrations/<category>/<tech>`, `pockets/<name>`,
`pockets/<name>/stores/<package>`, …) is tagged independently
(`sdk/vX.Y.Z`, `pockets/cms/vX.Y.Z`, `pockets/cms/stores/turso/vX.Y.Z`, …).
`go.work` and the store modules' relative `replace` directives are
**workspace-dev-only** and must be dropped (replaced with `require` + a
pinned version) before a module is tagged. Module paths are already rooted at
`github.com/gopernicus/gopernicus/...`; no tags are cut by this phase.

## 8. Authoring checklist (the rails for the next pocket)

A literal checklist an executor building a new pocket (auth, phase 4+) can
cite by item number:

1. Module compiles standalone (`cd pockets/<name> && go build ./...`) with
   its own `go.mod`.
2. `go.mod` requires **only SDK and shared pockets** (FS1; the D7 view-deps allowance is
   superseded). Datastore drivers live in `stores/<package>`, view tech in
   `views/<package>` — separate modules each.
3. Core production code never imports `integrations/`, `examples/`, or concrete
   adapters. Memory/conformance packages and core tests may use
   their own `stores/memory` and `stores/storetest` support; driver modules remain
   separate. New immediate pocket modules are discovered by the boundary checker.
4. The pocket exposes validated public construction and focused logic services.
   A small root assembles them where helpful. Public inbound adapters expose
   optional bundled routes and reusable middleware/handlers, so hosts can use
   their own transport without losing access to use cases. A transport-free
   pocket needs no registration stub. See amended FS2 in §1.
5. Each `stores/<package>` adapter module exposes `Repositories(db)` and
   `ExportMigrations(dst)`; it does not register or apply migrations itself.
   For a MULTI-KIND store with boot-time table probes the accepted surface is
   `Repositories(db) (<pocket>.Repositories, error)` — the bundle name WITH
   an error return (authorization-v1 refinement 11; a deliberate hybrid of
   jobs' error-less bundle and events' probing single-Store `New(db)`).
   A store never calls a connector's `Underlying()` (guard G10's sibling G9
   enforces it); a future cross-repository transaction consumes the
   scaffolded `transaction.Transactor` seam (`sdk/capabilities/transaction`) instead.
6. A minimal-host proof exists: a `go run`-able host with an in-memory (or
   otherwise zero-external-infra) `Repositories` implementation, mirroring
   `examples/minimal`.
7. The pocket's own `README.md` (or a section of this charter, for now)
   documents: its route surface + claimed namespace (§4), its `Config`
   fields and what each defaults to, and the ports in `Repositories` a host
   or store adapter must satisfy.
8. No `init()` registration, no package-level mutable registry — everything
   reachable only via `Register`'s explicit parameters.
9. No pocket → pocket imports (§5); any cross-pocket need is a port
   declared in this pocket's own public package.
10. A `stores/storetest` conformance package exists and the reference in-memory
    implementation passes it in the pocket's own `go test ./...`.
11. Every `stores/<package>` in the supported set ({turso, pgx}, §3)
    exists and passes `storetest`, with the live run recorded as a dated
    NOTES.md artifact (suite, store, DSN class, result) at milestone close.
12. Every optional `Repositories`/`Config`/`Mount` port documents its nil
    semantics — what degraded mode a host gets by not wiring it. Safe
    degradation defaults silently (cms's nil `Cache` disables public
    caching); unsafe degradation errors loudly at construction (auth's nil
    `Hasher`/`Mailer` precedent). A pocket never forks into variants —
    optional capability is always a nil-safe port, decided in the host's
    `main`.
13. Every crud-paginated list port follows the pgx-crud-v1 standards
    (`sdk/pkg/list`'s package doc is normative): the aggregate declares its
    order allow-list (`map[string]list.OrderField`) + default `list.Order`
    in its pocket-core domain package (indexed spine columns only — EAV
    fields are never sortable); the storetest suite carries the standard
    six-case family per paginated port (`Order`, `PrevPage`, `OffsetMode`,
    `WithCount`, `StaleCursorOrderChange`, `CursorOffsetExclusive`); and
    any HTTP list endpoint parses the standard query-param vocabulary —
    `limit`, `cursor`, `offset`, `count`, `order=field:direction` — strict
    (400) at JSON edges, clamp/fallback at SSR edges. Store adapters build
    on the connector `List[T]` helpers (`pgxdb.List` / `turso.List`), never
    hand-rolled pagination. An aggregate whose resource needs non-default
    page sizes declares a `var ListLimits = list.Limits{…}` beside its
    `OrderFields`/`DefaultOrder`, and its stores and handlers pass it to
    `req.NormalizedLimit` / `list.Params.Limits`; the zero value keeps
    `list`'s `DefaultLimit`/`MaxLimit`.
14. Entity-ID strategy (segovia-lessons phase 04, amended D9/D10): the
    pocket `Config` carries `IDs sdk.IDGenerator` (zero value → the
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
