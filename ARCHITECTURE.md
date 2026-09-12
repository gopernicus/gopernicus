# gopernicus — architecture

Two things live here: a **framework** (`sdk` + `pockets/` + `integrations/`) and the
**hexagonal app pattern** that apps built on it follow — demonstrated by the
worked example `examples/cms`.

## Repository layout — a multi-module monorepo

```
<repo>/
  go.work                                # ties the modules together for local dev (dev-only)
  sdk/                    module github.com/gopernicus/gopernicus/sdk                         — stdlib-only, LAYERED (sdk-layering 2026-07-10): the root package holds common vocabulary and primitives (errors/faults, context, identity, IDs, pointers, slugs); pkg/ = agnostic mechanism/vocabulary (imports the root only, flat); capabilities/ = behavioral ports + policy, defaults optional (import root+pkg, never each other)
  integrations/
    cryptids/bcrypt/      module …/integrations/cryptids/bcrypt         — a connector (x/crypto bcrypt)
    cryptids/golang-jwt/  module …/integrations/cryptids/golang-jwt     — a connector (golang-jwt/jwt v5)
    cryptids/google-uuid/ module …/integrations/cryptids/google-uuid    — a connector (google/uuid v4/v7)
    datastores/firestore/ module …/integrations/datastores/firestore    — a connector (cloud.google.com/go/firestore; Native mode: documents, transactions, index manifest)
    datastores/pgxdb/       module …/integrations/datastores/pgxdb          — a connector (jackc/pgx v5; multi-port: transaction.Transactor + ratelimiter)
    datastores/turso/     module …/integrations/datastores/turso        — a connector (sdk + libsql)
    email/sendgrid/       module …/integrations/email/sendgrid          — a connector (sendgrid/sendgrid-go)
    filestorage/gcs/      module …/integrations/filestorage/gcs         — a connector (cloud.google.com/go/storage)
    filestorage/s3/       module …/integrations/filestorage/s3          — a connector (aws-sdk-go-v2 service/s3; S3-compatible endpoints)
    kvstores/goredis/     module …/integrations/kvstores/goredis        — a connector (redis/go-redis v9; multi-port: events bus + cacher + ratelimiter)
    oauth/github/         module …/integrations/oauth/github            — a connector (GitHub's OAuth API contract; zero external libs)
    oauth/google/         module …/integrations/oauth/google            — a connector (coreos/go-oidc v3)
    scheduling/robfig-cron/ module …/integrations/scheduling/robfig-cron — a connector (robfig/cron v3)
    tracing/otel/         module …/integrations/tracing/otel            — a connector (OpenTelemetry family; stdout/OTLP exporters, R-KV1)
  pockets/               module github.com/gopernicus/gopernicus/pockets — shared host contract; concrete child modules: focused public logic/ services and owned contracts, optional public inbound/http, in-module stores/storetest and optional stores/memory, separate driver/view modules
    authentication/       module github.com/gopernicus/gopernicus/pockets/authentication               — session-auth hexagon (datastore-free)
      stores/pgx/         module …/pockets/authentication/stores/pgx             — auth's pgx store adapter
      stores/turso/       module …/pockets/authentication/stores/turso           — auth's Turso store adapter
      stores/firestore/   module …/pockets/authentication/stores/firestore       — auth's Firestore store adapter (Native mode; index manifest instead of migrations; joins no ambient transaction)
    authorization/        module github.com/gopernicus/gopernicus/pockets/authorization              — IAM hexagon: independently wireable kinds (relationships/ReBAC + roles; datastore-free; public stores/memory)
      stores/pgx/         module …/pockets/authorization/stores/pgx            — authorization's pgx store adapter
      stores/turso/       module …/pockets/authorization/stores/turso          — authorization's Turso store adapter
      stores/firestore/   module …/pockets/authorization/stores/firestore      — authorization's Firestore store adapter (Native mode; index manifest instead of migrations; joins no ambient transaction)
    cms/                  module github.com/gopernicus/gopernicus/pockets/cms                — the CMS hexagon (datastore-free)
      stores/pgx/         module …/pockets/cms/stores/pgx              — the CMS pocket's pgx store adapter
      stores/turso/       module …/pockets/cms/stores/turso            — the CMS pocket's Turso store adapter
      views/goth/         module …/pockets/cms/views/goth              — cms's bundled default views (ui/goth; FS3 sibling)
    events/               module github.com/gopernicus/gopernicus/pockets/events             — durable outbox + SSE gateway hexagon (datastore-free)
      stores/pgx/         module …/pockets/events/stores/pgx           — events' pgx store adapter
      stores/turso/       module …/pockets/events/stores/turso         — events' Turso store adapter
    jobs/                 module github.com/gopernicus/gopernicus/pockets/jobs               — durable queue + schedules hexagon (datastore-free; public stores/memory)
      stores/pgx/         module …/pockets/jobs/stores/pgx             — jobs' pgx store adapter
      stores/turso/       module …/pockets/jobs/stores/turso           — jobs' Turso store adapter
  workshop/
    gopernicus/           module github.com/gopernicus/gopernicus/workshop/gopernicus       — the scaffolding CLI (init / new pocket / db verbs; stdlib-only; emits the anatomies below, never links them — guard G11)
  examples/
    cms/                  module github.com/gopernicus/gopernicus/examples/cms                — a host app: pockets/cms on Turso
      cmd/  internal/theme  workshop/migrations
    minimal/               module github.com/gopernicus/gopernicus/examples/minimal           — a host app: pockets/cms on an in-memory store
      cmd/  internal/memstore
    auth-cms/              module github.com/gopernicus/gopernicus/examples/auth-cms          — a host app: auth + cms + events + the authorization flagship composed, in-memory (rule 6, live)
      cmd/  internal/authmem  internal/memstore
    jobs-minimal/          module github.com/gopernicus/gopernicus/examples/jobs-minimal      — a host app: pockets/jobs on stores/memory, zero drivers
      cmd/
```

**Forty-two modules today.** `sdk` is the kernel; `integrations/*` are reusable
third-party connectors (one external dependency each, each its own module);
`pockets/<name>` is a datastore-free pocket core with its driver adapters as
sibling modules. Memory and store conformance are packages in the core module
under `stores/memory` and `stores/storetest`; `examples/*` are host apps that
consume them — `examples/cms` (Turso), `examples/minimal` (in-memory, zero
libsql in its module graph), and `examples/auth-cms` (auth + cms + events +
the authorization flagship composed, all in-memory;
constitution rule 6 demonstrated live). Pockets share the app hexagon's
responsibilities: public logic services and owned contracts, private implementation
details, public inbound adapters, and outbound stores.
There is no mandatory forwarding facade. Driver adapters isolate dependencies
in separate modules; memory and conformance do not need their own modules.
The full app↔pocket mapping lives in `pockets/README.md` §2; CMS retains its prior
layout while deferred. See the
**Pockets** section below for the mount contract. `go.work` resolves the
modules locally for development; real consumers would pin tagged versions, not
the workspace. Module paths are rooted at `github.com/gopernicus/gopernicus`.


## The sdk layering law (sdk-layering, 2026-07-10; tests included 2026-08-27)

```
sdk/                      ROOT package sdk — shared errors/faults, context,
                          identity, ID generation, pointer reads and slugs.
                          Imports stdlib only; never an SDK subpackage.
                          Subject areas live in separate root files.
  pkg/<pkg>        coherent mechanism/vocabulary (web, workers, list,
                          cryptids, validation, logging, async, environment).
                          May import the ROOT only — FLAT, no
                          pkg→pkg edges.
  capabilities/<pkg>      behavioral ports + observable policy, pinned
                          by conformance tests; first-party defaults
                          OPTIONAL (cacher, tracing, notify/email, notify,
                          oauth, filestorage, ratelimiter, events,
                          work — oauth and work ship no default). May
                          import root + pkg and their own
                          subsystem subpackages, never another capability.

pockets/                  Separate shared-contract module, imports SDK only.
  <name>/                 Independent concrete pocket modules; may import SDK
                          and shared pockets, never another concrete pocket.
```

A capability may contain cohesive subpackages: notify/email adapts typed email
into notify.Delivery inside the notification subsystem. This stays stdlib-only
and needs no separate integration module. Capabilities otherwise import root and
pkg, never a different capability. Capability-owned middleware stays with
its capability (for example cacher.Pages). Guard G12 enforces this
law over production and test code alike; tests use root/stdlib fakes or
package-local contract checks rather than importing a peer tier. G13 keeps
integrations pointing outward-only. G21 (web-crud-list-request, 2026-08-27)
pins one stdlib edge the module-path greps cannot see: `pkg/list` may
import `net/url` (its list-query parser reads `url.Values`) but never
`net/http`, because every store adapter imports `list` and carries whatever
it imports.

The crypto package name is `pkg/cryptids`, meaning "cryptography tidbits",
by owner choice (2026-09-10). A small package may retain a deliberate name;
clarity is judged from its documented responsibility, not naming formality.
Root SDK now includes pointer/slug/ID/identity primitives by owner decision
(2026-09-10), recorded in [sdk-root-promotion.md](plans/sdk-root-promotion.md).
Package boundaries serve API clarity rather than a minimum size or one-operation
rule. A root addition must be stdlib-only, broadly useful, clearly named without
a subsystem qualifier, and free of application/transport lifecycle. Shared
vocabulary and small primitives both qualify; prior use by two packages under pkg
packages is not required. Validation and environment retain coherent namespaces;
ID generation and caller identity are independent root files, not one subsystem.
The import directions remain unchanged.

## Constructor configuration (2026-09-11)

Required dependencies and inputs stay explicit in constructor signatures. Use
trailing typed functional options for independent optional behavior:
`New(store, WithNamespace("orders"))`. Use `Option` for one constructor family,
or a target-specific name such as `RunnerOption` when several families share a
package. Zero-configuration constructors and ordinary domain/value builders do
not need an options API. Coherent connection, credential, model and policy
records remain useful `Config` values; the host can load and validate them as
data. Do not duplicate every record field with another setter API.

Public non-CMS pocket assembly, service, runtime and HTTP constructors follow the
same options convention. Supply optional coherent records through named feature
options, such as `WithPassword(PasswordConfig{...})` or
`WithRuntimePolicy(RuntimePolicy{...})`. Required modes, signers, buses and handler
registries stay explicit. A named repositories/readers/services input can group
related required ports. A feature option replaces its entire record, including
zero/nil fields; it does not merge only nonzero fields with earlier options.

Use `New` / `NewX` for construction functions, and **constructor** for the noun in
documentation and filenames (`constructor.go`, never `construct.go`). Short
constructors can stay beside their service; file uniformity does not justify
splitting them out. `options.go` and `config.go` describe options and policy data
when those files help navigation. Existing clear `NewService` / `NewRuntime`
names need no cosmetic API rename.

Options configure private construction state. Apply them in order, then resolve
documented defaults and validate the final configuration before acquiring owned
resources or starting work. Scalar setters replace earlier values; collection
and instrumentation options document their append behavior. Snapshot retained
configuration without copying or taking ownership of host clients and services.
Reusable options must not mutate captured settings when applied or reconfigure
an already running object. Use local types and straightforward functions, not a
shared generic options framework.

A nil option is invalid programming input. Constructors that already return an
error reject it using their existing error vocabulary; other constructors panic
with a specific diagnostic. A valid option with a nil argument can still mean
default/disabled when explicitly documented. Keep existing scalar defaults and
security validation; changing construction syntax does not authorize policy
changes. Context remains an explicit first argument where supported for startup
I/O, never a `WithContext` option or stored request context.

This owner-approved convention replaces pockets FS6's blanket Config-only rule.
The reviewed families, migration scope and retained exceptions are recorded in
[constructor-options.md](plans/constructor-options.md).
Its retained pocket Config exceptions are superseded by the owner's
[pocket constructor follow-up](plans/pocket-constructor-options.md).

## Protocols and pocket relationships (sdk-work-protocol, 2026-07-13)

sdk owns the **interoperability grammar** — vocabulary, narrow contracts, and
conformance semantics; pockets either IMPLEMENT that grammar or BUILD ON it
while retaining their own aggregates, schema, lifecycle, and routes. The
relationship is not uniform:

| sdk contract | pocket | relationship |
|---|---|---|
| `sdk.IdentityResolver` | `pockets/authentication` | IMPLEMENTS the Resolver; users/credentials stay pocket-owned |
| `sdk/capabilities/events.Bus` | `pockets/events` | BUILDS ON the Bus (durable outbox + SSE gateway); is not the Bus implementation |
| `sdk/capabilities/work` — the keyed-work submission protocol | `pockets/jobs` | IMPLEMENTS the protocol as the **implementation of record** — a capability with NO default (the oauth precedent); the durable `Job` aggregate and fenced runtime stay pocket-owned |
| authorization check/decision vocabulary | `pockets/authorization` | **deferred** — stays consumer-declared (fails graduation criterion 2 of `pockets/README.md` §5 today; trigger: authorizationv3 settles its semantics) |

Placement within sdk is determined by what the package MEANS and may depend
on, never by where a current implementation happens to live: pure
mechanism/vocabulary → `pkg/`; behavioral port + observable policy
pinned by a conformance suite → `capabilities/`. A capability need not ship a
default (`oauth`, `work`). This keeps the tier predictive if an implementation
later moves or another implementation appears. Executor-side mechanics
(claim/lease/checkpoint/fencing) are not part of the work protocol: the
mechanism stays `sdk/pkg/workers`, the domain contract stays
`pockets/jobs/logic/queue.FencedQueueRepository`. The work protocol's lifecycle
vocabulary is the frozen seven-value set — `pending` / `running` / `completed`
/ `failed` (non-terminal) / `dead_letter` / `canceled` / `superseded` — locked
to its persisted strings by the sdk package's own literal test.

Worker and job middleware are intentional SDK extension points, including for
custom queues that do not adopt jobs. `Middleware` wraps a polling iteration;
`JobMiddleware[T]` wraps `ProcessFunc[T]` after claim and before persistence.
The first wrapper supplied is outermost. Temporary job gates return `DeferUntil`
and permanent gates return `Reject`; nil means successful processing. Deferral
uses optional store ports for an atomic release, with a fenced attempt refund.
These executor mechanics do not belong in the consumer-facing `work` protocol.
Host policy supplies gate decisions; the SDK supplies composition and outcomes.
Lack of current adoption alone does not invalidate an agreed extension point.

A shape graduates from a pocket-declared port into an sdk protocol only by
passing **all three gates**: `sdk/README.md`'s admission policy, the
five-point sdk-vs-logic test below, and the five graduation criteria of
`pockets/README.md` §5 — the criteria are conjunctive with, never a
substitute for, the other two gates.

## Where middleware lives (middleware-consolidation, 2026-07-11)

HTTP middleware sorts onto the same three tiers, ratified so nobody
"consolidates" a gate into the wrong layer later:

| middleware | lives in | why |
|---|---|---|
| **pkg — pure HTTP mechanism** | `sdk/pkg/web` | `Panics`, `Logger`, `RequestID`, `TrustProxies`, `CORSMiddleware`/`CORSWithConfig`, `DefaultHeadersMiddleware`, `NoStore` — no capability port behind them, stdlib only, FLAT. `CORSMiddleware`/`DefaultHeadersMiddleware` are AVAILABLE host middleware, kept deliberately (owner call, 2026-07-11): expected wiring for any API-serving or browser-facing host, NOT prune candidates even though no example wires them yet. |
| **capability×pkg composition** | the capability that owns the semantics | `cacher.Pages`, `tracing.Middleware`, `ratelimiter.Middleware` — a capability producing a `web.Middleware`; web stays agnostic of the capability, the capability legally depends on web (capability → pkg). |
| **identity/authorization gate** | the owning pocket's public `inbound/http` adapter | `authentication.RequireAccessToken()` and `authorization.RequirePermission` assemble HTTP adapters over transport-independent credential/decision logic. Hosts inject them through config seams — `[]web.Middleware` (`cms.Config.AdminMiddleware`, `events.Config.StreamMiddleware`) or a SINGULAR `web.Middleware` (`authentication.Config.MachineRoutesGate`) — or at route registration. Singular vs slice is a deliberate posture choice, not drift: with a slice, nil and an empty slice both mean "mounted, ungated", which is fine for a surface that mounts regardless; the singular seam is for a surface whose routes should NOT mount without a policy — nil is the unambiguous "no policy" (the machine lifecycle routes stay unmounted), where an empty slice would have meant "mounted, ungated". |
| **host recipe** | a host closure | platform-admin/self-access short-circuits (auth-cms's `isPlatformAdmin`, composed by its `requireMembership` gate) — each authorization engine evaluates only what its own model declares; bypasses are host composition, run first in the host's own closure, and fail closed. The roles kind's model does not change this: a globally held role is DATA that grants exactly the role-owned permissions whose model entries name it, never relationship-owned or later-added ones, so a universal admin bypass stays a host recipe and the pocket ships no `Superuser` primitive. |

Response-writer wrappers preserve `http.ResponseController` operations and
forward errors through `web.RecordError`/`Unwrap`. Shared StatusRecorders retain
the first cause, distinguish informational/final responses, and observe flushing
and hijacking. Failed renders/transfers do not become cached pages. Hosts choose
JSON body limits and strictness; the web SDK does not infer OpenAPI schemas.
See [web package](workshop/documentation/docs/sdk/web.md) and
[AUDIT-006](AUDIT.md#audit-006-web-correctness-and-api-cleanup).

**Deliberately opposite fail postures (D-D).** `ratelimiter.Middleware` fails
OPEN — availability of a public route beats a limiter outage — and does so
SILENTLY by design: it takes no logger and emits nothing on the limiter-error
branch. A host wanting visibility wraps its `Allower` with a logging/metrics
decorator (the `Allower` seam is the observability point) and/or relies on
limiter-side alerting; silent fail-open must never be mistaken for a monitored
state. `authorization.RequirePermission` fails CLOSED (engine or resolver error
→ 500). The opposition is intentional — do not harmonize them.

## Host HTML cross-origin posture (CSRF, ratified 2026-07-20)

How host-rendered HTML mutations are protected against cross-origin request
forgery. Ratified from the first `ui/goth` adopter's upstream trip (Segovia
flag #11); the decisions are D1–D4 in that trip's plan.

**The posture (D1): host HTML forms are origin-checked, not token-carrying.**
A host mounts Go's `net/http.CrossOriginProtection` (Go 1.25+) on its
browser-HTML route groups; host-rendered unsafe methods ride that check and do
NOT require a framework-wide hidden token. The canonical construction is
direct stdlib use (D4) — `(*http.CrossOriginProtection).Handler` already
structurally satisfies `web.Middleware`, and `SetDenyHandler` takes the host's
styled denial page — so there is deliberately NO sdk wrapper: a pass-through
wrapper would add API without capability. The compiled reference is
`sdk/pkg/web/crossorigin_example_test.go`, which pins both the
construction and the exact accept/reject table below.

**Scope — read this before citing the posture.** It covers modern-browser
HTML routes on a host, not a blanket API-router wrapper and not every sdk
consumer:

- The stdlib check passes GET/HEAD/OPTIONS unconditionally — the posture
  therefore RESTS on the invariant that safe methods never mutate state.
  A host that mutates on GET has no protection here (or anywhere).
- `Sec-Fetch-Site: same-origin` and `none` pass; `same-site`, `cross-site`,
  and other non-empty values reject unless explicitly exempted. Rejecting
  `same-site` is the point: it closes the untrusted-sibling gap that
  `SameSite` cookies alone leave under a shared registrable domain.
- When `Sec-Fetch-Site` is absent, an `Origin` whose host equals
  `Request.Host` passes, as does an exact trusted origin added via
  `AddTrustedOrigin`. Missing BOTH headers passes — the stdlib treats that as
  "assume same-origin or non-browser", an assumption, not a proof. Headerless
  clients (legacy browsers, some WebViews, non-browser tooling) therefore
  FAIL OPEN; the posture is ratified for modern-browser staff/admin surfaces
  and must not be presented as universally safe for WebView-embedding or
  legacy-browser hosts.
- The `Origin` fallback cannot distinguish an old-browser HTTP→HTTPS scheme
  transition (`Request.Host` has no scheme); HSTS — owned at the host/edge,
  never by a pocket — is the mitigation.
- Session-cookie delivery is the other layer: auth access/refresh cookies are
  `SameSite=Lax`, so an ordinary cross-site unsafe request on an
  authenticated host route generally arrives without the session cookie.
  That statement is scoped to authenticated host routes; public
  credential-establishment endpoints have no session gate.
- CORS is a separate concern: `web.CORSMiddleware` grants cross-origin READ
  access to declared origins; it is not, and never substitutes for,
  cross-origin mutation protection.

**What pockets do (D2).** The authentication pocket keeps its own split,
unchanged by this ratification: credential-establishment endpoints and logout
are origin-only (`browserOriginAllowed`); authenticated account mutations
carry origin + the pocket's own plain double-submit token. Pocket-owned
forms may carry tokens when their owning pocket requires them; removing
auth's tokens would be a separate security change with its own review.

**No mechanical convergence (D3).** Do not replace auth's
`browserOriginAllowed` with the stdlib check as a cleanup: the existing gate
bypasses bearer-only requests, and `Sec-Fetch-Site: none` / unknown-value
behavior differ. Convergence requires an explicit compatibility matrix and a
deliberate ruling on each behavior, or it does not happen.

**No new generic header middleware rides this posture.**
`web.DefaultHeadersMiddleware` already carries `nosniff`, `Referrer-Policy`,
CSP, and other host defaults while letting handlers override them.

## Kinds of module — the taxonomy

Seven kinds of thing live in this ecosystem (ratified 2026-07-02, R6 —
`.claude/plans/roadmap/00-intersections.md` §1; amended 2026-07-07,
feature-standard FS3, adding the views-module row; amended 2026-07-09,
workshop-v2-scaffolding W5, adding the workshop row; amended 2026-07-17,
ui-goth GOTH-0.2, adding the UI-implementation row):

| kind | definition | examples | swap unit |
|---|---|---|---|
| **sdk facility** | a capability **port** + a conformance suite, usually with a first-party stdlib default — defaults are OPTIONAL (sdk-work-protocol, 2026-07-13): the implementation of record may instead be an integration (`oauth`) or a pocket (`work` → `pockets/jobs`); its state is opaque to the host (no host-owned schema, no migrations, no routes) | `cacher`+`Memory`, `notify/email`+`Console`/`SMTP`, `notify.Send`+selected `Delivery` values, `ratelimiter`+`Memory`, `filestorage`+`Disk`, `workers` (pool + `Runner[T]`), `work` (no default) | a config value — the swap is invisible outside the process |
| **integration** | a third-party backend for a port; isolates exactly one external dependency — a third-party library or an external vendor's live API contract (never importing pockets/, examples/, or another integration; guard G13); one module. The isolation unit is the vendor client library **plus the companion modules its API forces on the caller** (`google.golang.org/api/option`, `grpc/codes`+`status` beside `cloud.google.com/go/firestore`) — never a second backend | `datastores/turso`, `datastores/pgxdb`, `datastores/firestore`, `kvstores/goredis` | a module import in the host's `main` |
| **pocket** | a domain module: own entities, **own durable schema + migrations**, and/or **own route surface**; its core module requires **SDK and shared pockets only** (FS1) | `cms`, `authentication`, `authorization`, `jobs`, `events` | explicit service/component construction and optional HTTP registration |
| **store module** | a pocket's store implementation — persistence written against one driver package's API (`stores/<package>`) plus its scaffold artifact: SQL migrations for the SQL families, an index manifest (`firestore.indexes.json`, exported and boot-probed) for Firestore (R5) | `cms/stores/turso`, `cms/stores/pgx`, `authorization/stores/firestore` | a module import + one `Open` call |
| **views module** | a pocket's bundled presentation default — the implementation of the core's `Views` port, written against one view package's API (`views/<package>`; FS3, 2026-07-07 — amends R6's four-kind table). Nil `Config.Views` → the pocket's HTML surface is not registered, uniformly | `cms/views/goth` (landed at feature-standard B2, 2026-07-07; migrated templ→ui/goth at ui-goth GOTH-7.3, 2026-07-18) | a module import + one `Config` field |
| **workshop tool** | a developer-time tool that EMITS the other kinds' anatomies and never links them (guard G11: nothing imports `workshop/`, workshop imports no pocket/example); its output is verified by scaffold-compile tests inside `make check`, not by runtime coupling | `workshop/gopernicus` (the scaffolding CLI: `init` / `new pocket` / `db` verbs; workshop-v2-scaffolding, 2026-07-09) | a `go install` — never a runtime dependency |
| **UI implementation** | a reusable presentation system for ONE rendering/runtime family (ui-goth GOTH-0.2, 2026-07-17): it owns view-library dependencies, semantic tokens, primitives/components, interaction controllers, and distributable assets, and owns NO domain schema and NO routes. Its `go.mod` may require its own view/runtime libraries (templ and its pinned inputs) plus `sdk`; it never imports a pocket, integration, example, or workshop package (guard G17). A pocket reaches a UI implementation only through that pocket's own `views/<pkg>` adapter module — never the reverse; the UI implementation never registers routes, installs middleware, or writes HTTP response headers (the host composes assets + route registration) | `ui/goth` (templ + plain CSS + Alpine + optional HTMX); later `ui/react`, `ui/vue` | a host/view-adapter import plus theme/bundle configuration |

The two litmus tests: **if swapping the adapter changes what the host must
migrate, it's a store module per implementation; if the swap is invisible outside
the process boundary, it's one port with swappable backends.** And: **needs
its own migrations or routes → pocket; pure behavior a consumer calls →
sdk facility.** Pockets never fork into variants — optional capability is a
nil-safe port field, wired (or not) in the host's `main`.

**UI-implementation dependency arrows (ui-goth GOTH-0.2, 2026-07-17).** The
seventh kind sits at the top-level `ui/` family — neither a pocket nor an
integration — because it is a reusable presentation system that owns no domain
schema and no routes. The arrows only ever point outward-and-down:

```
pocket core  <--- pocket views/<pkg> adapter ---> ui/goth ---> templ/runtime
      ^                       ^                        ^
      |                       |                        |
      +---------------------- host -------------------+
                              |
                              +-- sdk web static serving / route registration
```

- a UI implementation → its own view/runtime libraries (templ + pinned inputs)
  and `sdk`, never a pocket/integration/example/workshop (guard G17);
- a pocket's `views/<pkg>` adapter → its pocket core + `sdk` + `ui/goth`;
- a host → selected pockets, selected view adapters, `ui/goth`, and `sdk`;
- only the host registers an asset route and chooses the public asset base URL,
  and only the host/owning pocket writes HTTP response/security headers — the
  UI implementation exposes assets, renderers, and requirements, never routes. A
  host maps the bundle's deterministic `Requirements` into its own CSP header; a
  security-sensitive pocket (authentication) instead has its `views/goth` adapter
  map `Requirements` into the pocket's technology-neutral resource policy
  (`HTMLPolicy()`), so the pocket core stays view-technology-free. See
  `ui/goth/README.md` §11 for the adopter recipes.

**`ui/` versus app-local `internal/inbound/views/`.** The Inbound anatomy
(`examples/README.md` §4) reserves `internal/inbound/views/` as a host's PRIVATE presentation tree — its
shared `Shell`/layouts and its own bespoke kit, sealed by Go's `internal/`. The
top-level `ui/` family is the REUSABLE, importable counterpart: a host that wants
the shared kit imports `ui/goth` (and the relevant pocket `views/goth` adapters)
instead of growing a private kit under `internal/inbound/views/`. The two are not
in tension — a host may consume `ui/goth` and still keep app-local overrides in
`internal/inbound/views/` — but the importable theme root is `ui/`, and the
app-local tree is the escape hatch, not the framework's shared UI kit.

## The framework: `sdk` + `integrations`

- **`sdk/` — the kernel.** Stdlib-only. It holds the facility **ports** (`Storer`,
  `Sender`, `cacher.Storer`, `transaction.Transactor`), the **services**
  (common error/context/identity/ID/helper vocabulary in root; `pkg/{web, logging,
  environment, cryptids, validation, list, …}`; `capabilities/{cacher, email, notify, transaction, …}` — the
  layered shape, sdk-layering 2026-07-10), **and — where a vendor-neutral
  stdlib implementation honestly exists — a zero-dependency default
  implementation of the facility port, shipped right next to it**
  (slog-style): `cacher.Memory`, `filestorage.Disk`, `email.SMTP` +
  `email.Console`. Defaults are optional, not definitional: `oauth` and
  `work` ship none (their implementations of record live in
  `integrations/oauth/*` and `pockets/jobs` respectively).
  Its `go.mod` has **no `require` block** — "imports only the standard library" is
  enforced by the module boundary, not just a grep.
- **`integrations/<category>/<tech>` — connectors.** Each isolates exactly **one
  external dependency** — a third-party library or an external vendor's live
  API contract — and is **its own module**: today, `datastores/turso` (libsql).

**The rule that decides sdk-default vs. integration:**

> A useful stdlib-only, **vendor-neutral** implementation may ship next to its
> SDK port. A port does not require an SDK implementation. Code needing a
> third-party library or speaking a vendor API belongs in an integration module.

For example, `cryptids.JWTSigner` has one library-backed implementation in
`integrations/cryptids/golang-jwt`. Keeping the SDK stdlib-only does not require
maintaining a second handwritten JWT parser. Root `sdk` keeps identifier strategies
separate from encryption and token signing. Published integration module paths
retain their existing `cryptids/` category.

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

## Pockets

A **pocket** (`pockets/<name>`) is a datastore-free core module plus one
store-adapter module per supported implementation — the pluggability unit that lets
hosts with different datastores (`examples/cms` on Turso, `examples/minimal`
in-memory, any Postgres host) mount the same domain logic. The supported
store-implementation set is **{turso, pgx}**, shipped out of the box at each
pocket's v1 with behavioral parity proven by the pocket's `storetest`
conformance suite rather than asserted (ratified DP1 — the charter's §3 has
the full rule). Firestore is an OPTIONAL third family rather than a DP1
requirement, and it does not join the host's ambient `transaction.Transactor`: a
Firestore transaction requires every read to precede every write and never
observes its own pending writes, so its stores return no transactor and fail
loud when handed one, which is a documented family difference rather than a
defect (ruling R1 — see [`pockets/README.md`](pockets/README.md)).
`pockets/cms` demonstrates it: content, taxonomy, menus,
media, and messaging, with ports and entities public, services + HTTP
internal (`pockets/cms/internal/*`), and both store implementations passing one
suite.

**The contract (`pockets`).** A pocket reaches its host only through a
narrow route mount plus explicit dependencies — no service locator, no `init()`
registration:

```go
// RouteRegistrar is the inbound mount point a pocket uses to register its HTTP
// routes. web.WebHandler satisfies it implicitly, so the host passes its router
// without the pocket importing the concrete handler.
type RouteRegistrar interface {
	Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware)
}

// Mount is the narrow, typed context handed to a pocket's Register.
type Mount struct {
	Router RouteRegistrar
	Logger *slog.Logger
	Events events.Emitter
}
```

(quoted from `pockets/mount.go`)

**Pocket anatomy.** A pocket module (`pockets/<name>`) may require **SDK and
the shared pockets contract only** in its `go.mod` (FS1, machine-checked in
`make check`) and never imports
`integrations/`, `examples/`, or concrete `stores/`/`views/` adapters from
production service code. Public packages expose deliberate use cases and
contracts. Focused services and their owned types/ports live in public
`logic/<concern>` packages; `internal` is reserved for genuine private helpers
or engines. Public `inbound/http` adapters own HTTP middleware, handlers and
registration. Production logic has no HTTP/web, inbound or root-composition
dependency. Memory/conformance packages and core tests may use their own store
support packages. CMS retains its existing layout until its deferred audit.

Anything carrying a third-party dependency ships as a per-concern sibling
module: persistence defaults in `stores/<package>`, presentation defaults in
`views/<package>` (FS3/FS4). Root constructors assemble named components from
host-supplied repositories/configuration. Hosts can also construct focused
services and HTTP adapters directly; every public constructor validates the
inputs it needs. A single-service pocket may return that service without a
bundle. A multi-component pocket returns named `Components`, preserving separate
trust and lifecycle responsibilities instead of forwarding all use cases through
one root service. Middleware and supported handlers are reusable on host routes;
bundled route registration is optional, and transport-free pockets need no stub
`Register` method. The owner-approved FS2 amendment is in `pockets/README.md`.

**Migrations (D4: scaffold-and-own).** A pocket store's SQL is scaffolded
into the host's own migration tree (e.g. `examples/cms/workshop/migrations`)
and applied by the host's own runner, pre-boot — never by the framework at
startup. The host owns the merged, ordered migration stream for each database
directory, following the original `workshop/migrations/{db}` model.

`examples/minimal` is the standing proof that a host can adopt a pocket with
**no datastore driver in its module graph at all** — its `Repositories` are
backed by an in-memory store.

**The full contract, ratified.** [`pockets/README.md`](pockets/README.md) is
the charter: pocket anatomy, the authoring checklist for the next pocket, and
the four contract decisions closed in phase 3 — route namespacing
(`pockets.PrefixRegistrar` lets a host relocate a pocket under a prefix;
verified working for a pocket that owns its whole route surface, with a
documented limitation where a pocket's own views hardcode absolute links),
cross-pocket dependencies (never import another pocket — declare a port,
the host wires an implementation), `Mount`'s compatible-growth policy (narrow
ports only, named candidates, never a service locator), and the nested-module
release/tagging procedure (see [`RELEASING.md`](RELEASING.md)).

## The app pattern (hexagonal) — for a host's own app-local domains

Not every domain belongs in a reusable pocket module — a host may have
app-local domains of its own, built the same hexagonal way. Within an app,
dependencies point **inward** to the hexagon (`internal/logic`); everything
ultimately stands on `sdk`. This `internal/logic` is a **pure hexagon that
imports only `sdk`** — distinct from the *original* gopernicus repo's `core/`
layer, which embedded adapters directly (the ambiguity this design fixes
structurally via module boundaries).

```
   internal/inbound ──►  internal/logic  ◄── internal/outbound ──► integrations
                              │                    │                  │
                              ▼                    ▼                  ▼
                             sdk  ◄──────────────────────────────── sdk

   cmd ──► wires the chosen implementations together (dependency injection lives here)
```

The import law, as the host rules H1–H8 state it (host-relative paths;
"framework" = `sdk`, `integrations/*`, `pockets/*`, `ui/*`; production files
unless a row says otherwise):

| id | package family | may import | never imports |
|---|---|---|---|
| **H1** | `internal/logic/**` | stdlib, `sdk/...`, `<module>/internal/logic/...` | anything else — an allow-list, with no escape valve |
| **H2** | `internal/logic/domains/<d>` | as H1 | `internal/logic/compositions/*`; another domain's package |
| **H3** | `internal/logic/compositions/<c>` | as H1 | a repository port, a storage import, or a transaction handle |
| **H4** | `internal/inbound/**` | `internal/logic`, `sdk`, framework pocket cores + `views/<pkg>`, `ui/*`, host `pockets/*/{logic,inbound}`, view technology | `internal/outbound/**`, `internal/integrations/**` (`_test.go` exempt), host `pockets/*/outbound` |
| **H5** | `internal/outbound/domains/<d>` | among host packages: `internal/logic/domains/<d>`, `internal/integrations/*`, host `pockets/*`; framework freely | `internal/inbound/**`; any other `internal/logic/domains/<x>` (`_test.go`, `workshop/**` exempt) |
| **H5** | `internal/integrations/<tech>` | stdlib, `sdk`, third-party, framework `integrations/*` | `internal/logic`, `internal/inbound`, `internal/outbound`, host `pockets/*`, framework `pockets/*` |
| **H6** | `<host-module>/pockets/<n>/*` | stdlib, `sdk`, framework pocket rims, its own host pocket | `internal/*`, another host pocket; at least one framework-pocket rim import is required |
| **H7** | everything except `cmd/**`, `internal/outbound/**`, `internal/integrations/**`, host `pockets/*/outbound/**`, `workshop/**` | — | framework `pockets/*/stores/*`, framework `integrations/...`, database drivers |
| **H8** | directories | — | an inbound/outbound directory with no `internal/logic` counterpart (one-directional) |

`cmd/` is the composition root and the only place that names concrete
adapters (**H10**); `workshop/` is the host's developer-time tool tree, exempt
from H5/H7. Go's `internal/` keeps the app's hexagon and adapters private to
the app; the framework modules (`sdk`, `integrations/*`, `pockets/*`) never
reach into them.

**The full host contract is [`examples/README.md`](examples/README.md)** — the
sibling of [`pockets/README.md`](pockets/README.md): the layout tree, H0–H10
in full with the normative wording of the table above, the Inbound anatomy,
host pockets (`<host-module>/pockets/<name>`), `cmd`/`workshop`/
`internal/integrations`, and the retrofit recipe. This section is the
framework-side summary; that page is normative for hosts, and
`gopernicus guard` is its executable floor.

### Inbound anatomy

MOVED 2026-08-27 to [`examples/README.md`](examples/README.md) §4, verbatim
and with its 2026-07-08 ratification intact — along with the "`internal/outbound`
vs `integrations`" and "repositories: app-specific vs pocket store adapter"
notes that closed it.

## The one rule (host id: H1)

`internal/logic/` (the hexagon) and `sdk/` (the kernel) **never** import
`internal/inbound/`, `internal/outbound/`, or `integrations/`. Ports are defined
inward; adapters implement them. The day a domain imports a concrete driver, the
architecture is broken — a one-line `grep` (in `make check`) catches it. `sdk`
goes further: it imports **only** the standard library — and its empty `go.mod`
makes that structural.

## Where a port lives (host ids: H1, H5)

| the port is… | defined in… | default impl | external impl |
|---|---|---|---|
| framework facility (cache, file storage, email, …) | `sdk/<concern>` | `sdk/<concern>` (stdlib default — `cacher.Memory`, `filestorage.Disk`, `email.SMTP`/`Console`) | `integrations/<category>/<tech>` (own module, e.g. gcs/redis/SaaS) |
| app contract (post repository, asset store with CMS rules) | `internal/logic/domains/<domain>` | — | `internal/outbound/<kind>/<tech>` (app-specific) **or** `pockets/<name>/stores/<package>` (pocket store adapter module, for a reusable domain) |

The contract lives with the code that **consumes** it, never with the code that
implements it — as the DEFAULT. The one exception is a **ratified platform
protocol** (sdk-work-protocol, 2026-07-13): a shape that passes all three
graduation gates (`sdk/README.md` admission, the five-point test below, and
`pockets/README.md` §5's five criteria) moves into sdk as canonical
vocabulary + contract, and a pocket may be its implementation of record —
`sdk/capabilities/work` implemented by `pockets/jobs` is the exemplar.

## sdk vs internal/logic — the test (host id: H1)

Put a contract in `sdk` only if **all** hold (otherwise it's an `internal/logic`
domain port):

1. Multiple adapters can honestly implement it.
2. `sdk` can define observable behavior, not just method names.
3. You can write a conformance suite for it.
4. Most apps would benefit from the same vocabulary.
5. It stays useful without knowing CMS-specific domain concepts.

This five-point test is one of the three conjunctive graduation gates for a
shape leaving a pocket-declared port (see "Protocols and pocket
relationships" above); passing it alone never admits a contract into sdk.

`sdk` is meant to be **opinionated** about platform semantics and defaults
(context-first, error kinds, cursor pagination, cache TTL/miss semantics, file
storage baseline + optional capabilities, shared listing vocabulary) —
and to **ship working stdlib defaults** so an app boots with zero external
infrastructure. Domain packages own narrow repository methods and update
semantics; the SDK supplies shared listing data without prescribing CRUD ports.

## Inside internal/logic/ — two tiers (host ids: H2, H3)

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
**Registry model** (see `pockets/cms`, plan `cms-content-engine`): all content —
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
+ its own migrations), *outside* the CMS pocket. If a custom field needs to be
filtered/sorted in SQL, it has outgrown EAV — promote it to a spine concern or
build it as a real app domain. Taxonomy/menus/media/messaging stay typed.

### The tier rules

- **Domains are independent peers (H2).** A domain never imports another domain
  (or only a narrow read-port / by ID). Coordination never flows sideways. The
  parenthesized allowance is for POCKET interiors: for a HOST, H2 is absolute —
  a domain never imports another domain at all (`examples/README.md` §3).
- **Compositions depend downward (H3)** on domains and own the cross-domain
  workflow, its invariant, and sequencing — never a repository port, a storage
  import, or a transaction handle; a workflow that needs an atomic write across
  two domains is the signal those aggregates belong in one domain. Domains stay
  ignorant of each other; a composition is the only place that knows the seam.
- **Repository per aggregate root**, not per table. Default to one service per
  domain; split by responsibility only when it grows. Never one-service-per-entity.

### Where a "doesn't fit one domain" thing goes (host id: H3)

1. **No domain knowledge** (pure algorithm: diff, slug codec, cursor encode) →
   `sdk` if framework-generic (e.g. `sdk.Slugify`), else a small `internal/` util.
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
  Implementation-independence lives in the pocket's **ports**, never in
  adapter naming: a future sqlx-based store is a new `stores/sqlx` module, not
  an in-place rewrite of `stores/pgx`.

See `sdk/README.md` for the framework kernel's own charter.

### OAuth flow and tracing policy (AUDIT-015)

OAuth providers report protocol identity/email evidence. Host authentication
configuration decides email trust, callback registrations and browser/native
transports. Authorization state is bound to an independent initiating-client
proof; cookies are the browser transport's mechanism. Native authorization-code
completion uses JSON with exact callback allowlisting; device authorization is a
separate future grant. The SDK exposes optional OIDC and refresh interfaces.

Tracing owns shared HTTP completion; optional HTTP metadata seams allow an
integration to start native server spans and receive typed status. OTel owns
export, root/parent sampling and explicitly configured inbound W3C trust. No
vendor types, global propagator, automatic baggage or tracing DSL enter the SDK.

### Authorization mutation ownership

Authorization tuples and role assignments are plain facts with natural keys.
The pocket keeps host guards and guardian rules inside its mutation transaction.
Supported raw writers participate in transaction isolation but remain trusted
capabilities that do not enforce the guard or guardian rules. The host places
these capabilities explicitly.

Optional store `WithAudit()` records actual committed fact changes in `iam_audit`
(or the corresponding Firestore collection), in the same transaction as the
facts. Attribution is actor or explicit system metadata; the host owns retention
and reader access. Scope revision counters, request receipt ledgers and the old
best-effort audit sink are removed (AUDIT-026). This stays in the authorization
pocket, not a generic SDK audit subsystem.
