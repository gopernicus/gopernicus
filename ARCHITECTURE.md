# gopernicus — architecture

Two things live here: a **framework** (`sdk` + `integrations/`) and the
**hexagonal app pattern** that apps built on it follow — demonstrated by the
worked example `examples/cms`.

## Repository layout — a multi-module monorepo

```
<repo>/
  go.work                                # ties the modules together for local dev (dev-only)
  sdk/                    module github.com/gopernicus/gopernicus/sdk                         — framework kernel (stdlib only; facilities incl. workers)
  integrations/
    cryptids/bcrypt/      module …/integrations/cryptids/bcrypt         — a connector (x/crypto bcrypt)
    cryptids/golang-jwt/  module …/integrations/cryptids/golang-jwt     — a connector (golang-jwt/jwt v5)
    cryptids/google-uuid/ module …/integrations/cryptids/google-uuid    — a connector (google/uuid v4/v7)
    datastores/pgxdb/       module …/integrations/datastores/pgxdb          — a connector (jackc/pgx v5)
    datastores/turso/     module …/integrations/datastores/turso        — a connector (sdk + libsql)
    email/sendgrid/       module …/integrations/email/sendgrid          — a connector (sendgrid/sendgrid-go)
    filestorage/gcs/      module …/integrations/filestorage/gcs         — a connector (cloud.google.com/go/storage)
    filestorage/s3/       module …/integrations/filestorage/s3          — a connector (aws-sdk-go-v2 service/s3; S3-compatible endpoints)
    kvstores/goredis/     module …/integrations/kvstores/goredis        — a connector (redis/go-redis v9; multi-port: events bus + cacher + ratelimiter)
    oauth/github/         module …/integrations/oauth/github            — a connector (GitHub's OAuth API contract; zero external libs)
    oauth/google/         module …/integrations/oauth/google            — a connector (coreos/go-oidc v3)
    scheduling/robfig-cron/ module …/integrations/scheduling/robfig-cron — a connector (robfig/cron v3)
    tracing/otel/         module …/integrations/tracing/otel            — a connector (OpenTelemetry family; stdout/OTLP exporters, R-KV1)
  features/                                                             — each: domain/ (public ports+entities) + internal/logic (+ internal/inbound where the feature registers routes — jobs v1 has none) + storetest/ + per-concern sibling modules (stores/<pkg>; views/<pkg> where the feature has HTML — FS3)
    authentication/       module github.com/gopernicus/gopernicus/features/authentication               — session-auth hexagon (datastore-free)
      stores/pgx/         module …/features/authentication/stores/pgx             — auth's pgx store adapter
      stores/turso/       module …/features/authentication/stores/turso           — auth's Turso store adapter
    authorization/        module github.com/gopernicus/gopernicus/features/authorization              — IAM hexagon: independently wireable kinds (relationships/ReBAC + roles; datastore-free; public memstore/)
      stores/pgx/         module …/features/authorization/stores/pgx            — authorization's pgx store adapter
      stores/turso/       module …/features/authorization/stores/turso          — authorization's Turso store adapter
    cms/                  module github.com/gopernicus/gopernicus/features/cms                — the CMS hexagon (datastore-free)
      stores/pgx/         module …/features/cms/stores/pgx              — the CMS feature's pgx store adapter
      stores/turso/       module …/features/cms/stores/turso            — the CMS feature's Turso store adapter
      views/templ/        module …/features/cms/views/templ             — cms's bundled default views (templ; FS3 sibling)
    events/               module github.com/gopernicus/gopernicus/features/events             — durable outbox + SSE gateway hexagon (datastore-free)
      stores/pgx/         module …/features/events/stores/pgx           — events' pgx store adapter
      stores/turso/       module …/features/events/stores/turso         — events' Turso store adapter
    jobs/                 module github.com/gopernicus/gopernicus/features/jobs               — durable queue + schedules hexagon (datastore-free; public memstore/)
      stores/pgx/         module …/features/jobs/stores/pgx             — jobs' pgx store adapter
      stores/turso/       module …/features/jobs/stores/turso           — jobs' Turso store adapter
  workshop/
    gopernicus/           module github.com/gopernicus/gopernicus/workshop/gopernicus       — the scaffolding CLI (init / new feature / db verbs; stdlib-only; emits the anatomies below, never links them — guard G11)
  examples/
    cms/                  module github.com/gopernicus/gopernicus/examples/cms                — a host app: features/cms on Turso
      cmd/  internal/theme  workshop/migrations
    minimal/               module github.com/gopernicus/gopernicus/examples/minimal           — a host app: features/cms on an in-memory store
      cmd/  internal/memstore
    auth-cms/              module github.com/gopernicus/gopernicus/examples/auth-cms          — a host app: auth + cms + events + the authorization flagship composed, in-memory (rule 6, live)
      cmd/  internal/authmem  internal/memstore
    jobs-minimal/          module github.com/gopernicus/gopernicus/examples/jobs-minimal      — a host app: features/jobs on its memstore, zero drivers
      cmd/
```

**Thirty-five modules today.** `sdk` is the kernel; `integrations/*` are reusable
third-party connectors (one external dependency each, each its own module);
`features/<name>` is a datastore-free feature core with its store adapters as
sibling modules — one per supported store implementation; `examples/*` are host apps that
consume them — `examples/cms` (Turso), `examples/minimal` (in-memory, zero
libsql in its module graph), and `examples/auth-cms` (auth + cms + events +
the authorization flagship composed, all in-memory;
constitution rule 6 demonstrated live). Features wear the app hexagon's
names (trio layout, 2026-07-02): `domain/<domain>` is the public rim
(entities + ports — public by necessity, since hosts and store modules
import them across module boundaries), `internal/{logic,inbound}` is the
sealed interior, `stores/` is the outbound tier module-ized; the full
app↔feature mapping table lives in `features/README.md` §2. See the
**Features** section below for the mount contract. `go.work` resolves the
modules locally for development; real consumers would pin tagged versions, not
the workspace. Module paths are rooted at `github.com/gopernicus/gopernicus`.

## Kinds of module — the taxonomy

Six kinds of thing live in this ecosystem (ratified 2026-07-02, R6 —
`.claude/plans/roadmap/00-intersections.md` §1; amended 2026-07-07,
feature-standard FS3, adding the views-module row; amended 2026-07-09,
workshop-v2-scaffolding W5, adding the workshop row):

| kind | definition | examples | swap unit |
|---|---|---|---|
| **sdk facility** | a capability **port** + a first-party stdlib default + a conformance suite; its state is opaque to the host (no host-owned schema, no migrations, no routes) | `cacher`+`Memory`, `email`+`Console`/`SMTP`, `notify`+`Console`/`MailerBridge`, `ratelimiter`+`Memory`, `filestorage`+`Disk`, `workers` (pool + `Runner[T]`) | a config value — the swap is invisible outside the process |
| **integration** | a third-party backend for a port; isolates exactly one external dependency — a third-party library or an external vendor's live API contract; one module | `datastores/turso`, `datastores/pgxdb`, `kvstores/goredis` | a module import in the host's `main` |
| **feature** | a mountable domain module: own entities, **own durable schema + migrations**, and/or **own route surface**; its core module requires **sdk only** (FS1, 2026-07-07) | `cms`, `auth`, `jobs`; next: `events` | `NewService` + a `svc.Register` call |
| **store module** | a feature's store implementation — SQL + migrations written against one driver package's API (`stores/<package>`) | `cms/stores/turso`, `cms/stores/pgx` | a module import + one `Open` call |
| **views module** | a feature's bundled presentation default — the implementation of the core's `Views` port, written against one view package's API (`views/<package>`; FS3, 2026-07-07 — amends R6's four-kind table). Nil `Config.Views` → the feature's HTML surface is not registered, uniformly | `cms/views/templ` (landed at feature-standard B2, 2026-07-07) | a module import + one `Config` field |
| **workshop tool** | a developer-time tool that EMITS the other kinds' anatomies and never links them (guard G11: nothing imports `workshop/`, workshop imports no feature/example); its output is verified by scaffold-compile tests inside `make check`, not by runtime coupling | `workshop/gopernicus` (the scaffolding CLI: `init` / `new feature` / `db` verbs; workshop-v2-scaffolding, 2026-07-09) | a `go install` — never a runtime dependency |

The two litmus tests: **if swapping the adapter changes what the host must
migrate, it's a store module per implementation; if the swap is invisible outside
the process boundary, it's one port with swappable backends.** And: **needs
its own migrations or routes → feature; pure behavior a consumer calls →
sdk facility.** Features never fork into variants — optional capability is a
nil-safe port field, wired (or not) in the host's `main`.

## The framework: `sdk` + `integrations`

- **`sdk/` — the kernel.** Stdlib-only. It holds the facility **ports** (`Storer`,
  `Sender`, `cacher.Storer`, the generic `crud` CRUD shape), the **services**
  (`web`, `logging`, `config`, `errs`, `cryptids`, `slug`, `identity` — the
  request-identity vocabulary + the Resolver port, A-I1 as grown at
  identity-resolution 2026-07-10), **and a zero-dependency
  default implementation of each facility port, shipped right next to it**
  (slog-style): `cacher.Memory`, `filestorage.Disk`, `email.SMTP` + `email.Console`.
  Its `go.mod` has **no `require` block** — "imports only the standard library" is
  enforced by the module boundary, not just a grep.
- **`integrations/<category>/<tech>` — connectors.** Each isolates exactly **one
  external dependency** — a third-party library or an external vendor's live
  API contract — and is **its own module**: today, `datastores/turso` (libsql).

**The rule that decides sdk-default vs. integration:**

> A stdlib-only, **vendor-neutral** implementation of an `sdk` port → **ship it
> in `sdk` as a default**. Needs a third-party library **or speaks one vendor's
> live API** → it's an **`integration`, its own module**.

That's why `smtp`/`disk`/`memory`/`console` are sdk defaults (stdlib) while
`turso` (libsql) and `pgx` (jackc/pgx v5) are integrations. Every integration
module earns its existence by isolating exactly one external dependency — a
third-party library **or an external vendor's live API contract** (amended
2026-07-06). Vendor-specific connectors are never sdk defaults even when
stdlib-implementable: sdk defaults must be vendor-neutral, and a vendor
connector's surface churns on the vendor's schedule, not sdk's. The amendment
does not soften the test's other edge: a module that isolates nothing external
(the ruled-out `stores/memory` case, R3) is still forbidden.

A single integration module may implement **several** sdk facility ports when
one client library serves them — `kvstores/goredis` backs the events bus, the
cacher, and the ratelimiter from one go-redis client; the module unit is the
**library**, not the port (R-KV1, 2026-07-06). Category naming follows:
capability category by default (`oauth/`, `scheduling/`, `cryptids/`),
tech-family category (`kvstores/`) when the library is genuinely multi-port.

## Features

A **feature** (`features/<name>`) is a datastore-free core module plus one
store-adapter module per supported implementation — the pluggability unit that lets
hosts with different datastores (`examples/cms` on Turso, `examples/minimal`
in-memory, any Postgres host) mount the same domain logic. The supported
store-implementation set is **{turso, pgx}**, shipped out of the box at each
feature's v1 with behavioral parity proven by the feature's `storetest`
conformance suite rather than asserted (ratified DP1 — the charter's §3 has
the full rule). `features/cms` demonstrates it: content, taxonomy, menus,
media, and messaging, with ports and entities public, services + HTTP
internal (`features/cms/internal/*`), and both store implementations passing one
suite.

**The contract (`sdk/feature`).** A feature reaches its host only through a
narrow route mount plus explicit dependencies — no service locator, no `init()`
registration:

```go
// RouteRegistrar is the inbound mount point a feature uses to register its HTTP
// routes. web.WebHandler satisfies it implicitly, so the host passes its router
// without the feature importing the concrete handler.
type RouteRegistrar interface {
	Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware)
}

// Mount is the narrow, typed context handed to a feature's Register.
type Mount struct {
	Router RouteRegistrar
	Logger *slog.Logger
}
```

(quoted from `sdk/feature/feature.go`)

**Feature anatomy.** A feature module (`features/<name>`) requires **sdk
only** in its `go.mod` (FS1, ratified 2026-07-07 — the same structural move
as sdk's empty go.mod, machine-checked in `make check`) and never imports
`integrations/`, `examples/`, or its own `stores/`/`views/`; its public
packages are ports + entities, its services and HTTP are `internal/`.
Anything carrying a third-party dependency ships as a per-concern sibling
module: persistence defaults in `stores/<package>`, presentation defaults in
`views/<package>` (FS3/FS4 — a feature has a `views/` only if it has HTML;
nil `Config.Views` → the HTML surface is not registered). The host supplies
a `Repositories` struct (a store adapter module or its own implementation —
see `examples/minimal`'s `internal/memstore`) and a `Config` struct for
view/infrastructure overrides, then builds and mounts per FS2 (ratified
2026-07-07, superseding the earlier single-`Register(mount, repos, cfg)`
contract): `svc, err := name.NewService(repos, cfg)` — the public `Service`
is the feature's **driving surface**, its use-cases promoted by thin
delegation — and `svc.Register(mount)` mounts the shipped HTTP layer, an
optional convenience adapter a host may skip entirely in favor of its own
handlers over the Service.

**Migrations (D4: scaffold-and-own).** A feature store's SQL is scaffolded
into the host's own migration tree (e.g. `examples/cms/workshop/migrations`)
and applied by the host's own runner, pre-boot — never by the framework at
startup. The host owns the merged, ordered migration stream for each database
directory, following the original `workshop/migrations/{db}` model.

`examples/minimal` is the standing proof that a host can adopt a feature with
**no datastore driver in its module graph at all** — its `Repositories` are
backed by an in-memory store.

**The full contract, ratified.** [`features/README.md`](features/README.md) is
the charter: feature anatomy, the authoring checklist for the next feature, and
the four contract decisions closed in phase 3 — route namespacing
(`feature.PrefixRegistrar` lets a host relocate a feature under a prefix;
verified working for a feature that owns its whole route surface, with a
documented limitation where a feature's own views hardcode absolute links),
cross-feature dependencies (never import another feature — declare a port,
the host wires an implementation), `Mount`'s compatible-growth policy (narrow
ports only, named candidates, never a service locator), and the nested-module
release/tagging procedure (see [`RELEASING.md`](RELEASING.md)).

## The app pattern (hexagonal) — for a host's own app-local domains

Not every domain belongs in a reusable feature module — a host may have
app-local domains of its own, built the same hexagonal way. (No host in this
repo currently has one; both `examples/cms` and `examples/minimal` are thin
hosts around the `features/cms` feature. This section documents the pattern
for when one appears.) Within an app, dependencies point **inward** to the
hexagon (`internal/logic`); everything ultimately stands on `sdk`. This
`internal/logic` is a **pure hexagon that imports only `sdk`** — distinct from
the *original* gopernicus repo's `core/` layer, which embedded adapters
directly (the ambiguity this design fixes structurally via module boundaries).

```
   internal/inbound ──►  internal/logic  ◄── internal/outbound ──► integrations
                              │                    │                  │
                              ▼                    ▼                  ▼
                             sdk  ◄──────────────────────────────── sdk

   cmd ──► wires the chosen implementations together (dependency injection lives here)
```

| dir (within the app) | role | hexagonal name | imports |
|---|---|---|---|
| `cmd/` | composition root — `main`, wiring; the only place that names concrete adapters | — | everything |
| `internal/inbound/` | driving adapters: HTTP (admin + public), CLI, cron, queue consumers | driving / primary | `internal/logic`, `sdk` |
| `internal/logic/` | the hexagon — `domains/` (services + ports) + `compositions/` (cross-domain orchestration) | the center | `sdk` |
| `internal/outbound/` | **app-specific** driven adapters implementing domain ports: `repositories/`, … | driven / secondary | `internal/logic`, `sdk`, `integrations` |

Go's `internal/` keeps the app's hexagon and adapters private to the app; the
framework modules (`sdk`, `integrations/*`, `features/*`) never reach into them.

### Inbound anatomy — inside `internal/inbound/` (ratified 2026-07-08)

Adopted from Segovia v2 (segovia-lessons flag #1, 2026-07-08) — the host
app built in tandem with this framework and the living reference
implementation; no host in this repo has app-local domains yet, and the
future `gopernicus new domain` scaffold (workshop-v2) should emit this
shape. Until that scaffold exists, adoption is by hand: read this
subsection, apply it, use Segovia v2 as the worked example.

```
internal/inbound/
  domains/<domain>/     # one package per app-local domain
    routes.go           #   the ONE readable route table — never split
    api.go              #   JSON handlers (single-resource degenerate form)
    html.go             #   HTML page handlers (fragments.go when htmx lands)
    views.go            #   the render PORT — methods return web.Renderer
    templates/          #   bundled default implementation (templ), co-located
  http/                 # transport plumbing only (middleware) — never handlers
  views/                # the GLOBAL presentation tree: shared Shell/layouts,
                        #   the future UI kit — the theme root
```

- **The render port (FS3 scaled to app-local).** `views.go` defines the
  domain's presentation port; methods return `web.Renderer`. templ is the
  default, never the contract: `templates/` is the bundled implementation,
  implements the port structurally, and never imports the transport.
  View-tech dependencies ride the app module's go.mod and touch only
  `internal/inbound` — `internal/logic` stays sdk-only (the one rule).
- **The theming seam.** `internal/inbound/views/` holds the shared
  `Shell`/layouts and the future UI kit, consumed by every domain's
  templates; a themed kit is a new implementation of the ports plus one
  `cmd` wiring change. **Partial override via embedding:** the default is
  a concrete exported struct, so a host (or a single binary —
  `cmd/<binary>/views/`) embeds it and overrides individual port methods;
  method promotion supplies the rest. Override granularity is the port
  method (the page), deliberately — reuse comes from exported building
  blocks (Shell, kit primitives), never exported page internals.
- **The growth rule (multi-resource domains).** The file axis flips from
  transport to RESOURCE at resource #2: `grants.go` holds that resource's
  api+html (`grants_api.go`/`grants_html.go`/`grants_fragments.go` only
  when one grows heavy); `routes.go` stays singular — one domain, one
  readable route table. Transport-named `api.go`/`html.go` are the
  single-resource degenerate form. **Never `/api`, `/html`, or `/htmx`
  subdirectories** — a subdirectory means a new contract (own
  schema/vocabulary) or a swappable implementation behind a port
  (`templates/`), never mere file count; a domain wanting its own package
  tree is two domains. The same axis mirrors in `logic/domains/<domain>/`
  and `templates/{resource}.templ`.
- **The maximal flatten** (a gopernicus-side clarification of the Segovia
  text): a single-resource, single-transport domain with a small handler
  set may keep its handlers in `routes.go` itself —
  `features/events/internal/inbound/events/routes.go` is the blessed
  example. The never-split rule constrains the route *table*, not the
  co-residence of a few handlers.
- **Features mirror the file anatomy, not the tree** (D1, ratified
  2026-07-08). A feature is its one domain, so the `domains/` level
  flattens to `internal/inbound/<feature>/`
  (`features/cms/internal/inbound/cms/`), carrying the same file anatomy
  with a `Mount` dispatcher in `routes.go` and per-resource
  deny-by-absence `mountX` helpers living in their resource files. `http/`
  keeps one meaning on both sides of the line — plumbing — and a feature
  has no `http/` until real plumbing appears. The global views tree and
  co-located `templates/` are **app-only**: a feature core requires sdk
  only (FS1), so its render port lives in the core and its bundled default
  is the `views/<pkg>` sibling module (FS3); the feature theming seam is
  embed-the-sibling-default (live override: `examples/cms/internal/theme/`).
  See `features/README.md` §2.

**`internal/outbound` vs `integrations`.** An `integration` is the *reusable*
connection/client to an external system (the turso connector). `internal/outbound`
is the *app-specific* code that implements a domain port using one (the post
repository's SQL + schema). A connector that fully implements an `sdk` facility
port (e.g. a gcs filestore → `sdk/capabilities/filestorage`) needs **no** `internal/outbound`
code — the app just wires it in `cmd`.

**Repositories: app-specific vs feature store adapter.** A repository is either
*app-specific* (its SQL belongs to one app → `internal/outbound`) **or** a
*feature store adapter* for a reusable domain (its SQL belongs to the feature →
`features/<name>/stores/<package>`, its own module). The moment a domain becomes a
reusable feature module, its store is **not** host-app code — it is a feature
store adapter module, so a host that brings a different datastore never pulls the
feature's driver into its module graph. The CMS feature demonstrates this: the
datastore-free `features/cms` core depends on its repository ports, and
`features/cms/stores/turso` is the separate module that supplies the libSQL
implementation + migrations.

## The one rule

`internal/logic/` (the hexagon) and `sdk/` (the kernel) **never** import
`internal/inbound/`, `internal/outbound/`, or `integrations/`. Ports are defined
inward; adapters implement them. The day a domain imports a concrete driver, the
architecture is broken — a one-line `grep` (in `make check`) catches it. `sdk`
goes further: it imports **only** the standard library — and its empty `go.mod`
makes that structural.

## Where a port lives

| the port is… | defined in… | default impl | external impl |
|---|---|---|---|
| framework facility (cache, file storage, email, …) | `sdk/<concern>` | `sdk/<concern>` (stdlib default — `cacher.Memory`, `filestorage.Disk`, `email.SMTP`/`Console`) | `integrations/<category>/<tech>` (own module, e.g. gcs/redis/SaaS) |
| app contract (post repository, asset store with CMS rules) | `internal/logic/domains/<domain>` | — | `internal/outbound/<kind>/<tech>` (app-specific) **or** `features/<name>/stores/<package>` (feature store adapter module, for a reusable domain) |

The contract lives with the code that **consumes** it, never with the code that
implements it.

## sdk vs internal/logic — the test

Put a contract in `sdk` only if **all** hold (otherwise it's an `internal/logic`
domain port):

1. Multiple adapters can honestly implement it.
2. `sdk` can define observable behavior, not just method names.
3. You can write a conformance suite for it.
4. Most apps would benefit from the same vocabulary.
5. It stays useful without knowing CMS-specific domain concepts.

`sdk` is meant to be **opinionated** about platform semantics and defaults
(context-first, error kinds, cursor pagination, cache TTL/miss semantics, file
storage baseline + optional capabilities, a starter CRUD `Repository` shape) —
and to **ship working stdlib defaults** so an app boots with zero external
infrastructure. It is **not** meant to decide every app's domain shape — CRUD is
a convenience, never a tax. Domains embed, narrow, or ignore it.

## Inside internal/logic/ — two tiers

`internal/logic` splits into the bounded contexts and the orchestrations that
compose them:

```
internal/logic/
  domains/                  # bounded contexts — independent peers
    catalog/                # e.g. a genuinely structured, queryable domain
      product.go            # entity (+ behavior), real columns/indexes/FKs
      service.go            # CatalogService — use cases within the domain
      repository.go         # ProductRepository port (interface)
    media/
      asset.go
      service.go            # MediaService
      repository.go         # AssetRepository port (metadata)
  compositions/             # application layer — orchestrates across domains
    publishing.go           # imports domains; owns the cross-domain workflow (planned)
```

### Content vs. structured data — the Registry model

CMS **content** does not follow the typed-aggregate shape above. It uses the
**Registry model** (see `features/cms`, plan `cms-content-engine`): all content —
Articles, Pages, and host-registered custom types — is one dynamic
`content.Entry` on a frozen spine (`entries`) plus EAV custom fields
(`entry_fields`). Content **types** are registered as data in Go (`Article` and
`Page` ship as seed registrations; a host adds `Product` via `cms.Config.Types`),
so adding a type or a field is a code change with **zero database migration** —
the tables never change shape. The cost is no compile-time typing at the content
core; presentation stays typed via dev-authored `templ` templates bound through
the `content.Registry`.

The rule: **content rides the shared Entry/EAV rail; genuinely structured,
queryable data (real columns, indexes, FKs) is a normal typed app domain** built
the hexagonal way shown above (`internal/logic/domains/<domain>` + `internal/outbound`
+ its own migrations), *outside* the CMS feature. If a custom field needs to be
filtered/sorted in SQL, it has outgrown EAV — promote it to a spine concern or
build it as a real app domain. Taxonomy/menus/media/messaging stay typed.

### The tier rules

- **Domains are independent peers.** A domain never imports another domain
  (or only a narrow read-port / by ID). Coordination never flows sideways.
- **Compositions depend downward** on multiple domains and own the cross-domain
  workflow, transaction boundary, and sequencing. Domains stay ignorant of each
  other; a composition is the only place that knows the seam.
- **Repository per aggregate root**, not per table. Default to one service per
  domain; split by responsibility only when it grows. Never one-service-per-entity.

### Where a "doesn't fit one domain" thing goes

1. **No domain knowledge** (pure algorithm: diff, slug codec, cursor encode) →
   `sdk` if framework-generic (e.g. `sdk/foundation/slug`), else a small `internal/` util.
   Not in a domain.
2. **Domain knowledge, one domain** → that domain (a domain service is fine).
3. **Spans domains** → `compositions/`.

Don't create a composition for thin sequencing (handler calls A then B) — that
lives in the `internal/inbound` handler. Extract a composition only when the
coordination has its own invariant, transaction, multi-step workflow, or is
reused by more than one inbound adapter.

## Naming conventions

- **Ports** are named for behavior, `-er` where natural (`Storer`, `Sender`,
  `Reader`) — never `Port`. Port-ness comes from position, not the name.
- **Services** are domain nouns (`ContentService`, `MediaService`).
- **Defaults** (in `sdk`) and **connectors** (in `integrations/`) and app
  **adapters** (in `internal/outbound/`) are named for the technology; the type or
  package name carries the meaning (`Memory`, `Disk`, `SMTP`, `turso`).
- **Concrete modules are named for the third-party package they're built on**
  (`pgx`, `goredis`, `robfig-cron`, future `sqlx`), never the generic protocol
  (`postgres`) — a store or connector is written against one package's custom
  API, and that API is why the package was chosen (R-KV2/R-KV3, 2026-07-06).
  Implementation-independence lives in the feature's **ports**, never in
  adapter naming: a future sqlx-based store is a new `stores/sqlx` module, not
  an in-place rewrite of `stores/pgx`.

See `sdk/README.md` for the framework kernel's own charter.
