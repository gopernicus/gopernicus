# Hosts — the gopernicus host contract

A **host** is an application that composes `sdk` + `integrations` + `pockets`
in a `cmd/` composition root. `examples/*` are hosts, and this document is the
contract every one of them is held to — the sibling of
[`pockets/README.md`](../pockets/README.md), which does the same job for the
modules a host mounts. Hosts in this repo live under `examples/` exactly as
pockets live under `pockets/`, so the charter sits where the things it governs
sit; there is no `hosts/` directory and this page is not a description of how
the examples directory is organized.

**The reference host is [`gps-360-go`](https://github.com/jimkottmeyer/gps-360-go)**
— a private production host built on this framework, whose layout the rules
below generalize from. Where this document and the reference disagree, the
disagreement is a finding to be settled, not a style choice; the reference is
the living evidence that the shape works.

## 1. The three nouns

The one word `pocket` appears at two scopes, and the import path — never a
second noun — says which:

- a **framework pocket** is an imported module under
  `github.com/gopernicus/gopernicus/pockets/<name>` — the pluggable,
  datastore-free domain module of `pockets/README.md`;
- a **host pocket** is *this application's* local wrap, extension, or bridge of
  one or more framework pockets, under `<host-module>/pockets/<name>`;
- a **host** is the application that mounts them.

Both are pockets. The retired third-tier noun this framework used before
2026-08-27 is never revived to name the local one — see
[`plans/rename-features-to-pockets.md`](../plans/rename-features-to-pockets.md)
for the rename, and the root `Makefile`'s G20 guard for the floor that keeps
it retired.

**A thin host has no local `pockets/` at all.** A host pocket exists only when
the host WRITES code against a pocket's ports or rim. Mounting a framework
pocket with its bundled store and views is `cmd` wiring and nothing else —
there is no "one local pocket per mounted pocket" rule, and inventing one is
the misreading this paragraph exists to pre-empt.

Rule ids on this page are the **H series** (H = host), H0–H10, one series in
the FS/G tradition. The repo's own Makefile guards keep the `G` series: those
police the framework, these police a host. `gopernicus guard --list` prints
these eleven sentences verbatim, so the binary and this page cannot drift.

## 2. The layout (H0)

```
<host>/
  cmd/<binary>/                 composition root — concrete construction/wiring, behavior-free (H10)
  internal/
    logic/                      the hexagon — imports sdk only (H1)
      domains/<domain>/           entities + repository ports + Service
      compositions/<name>/        cross-domain orchestration over domain SERVICES (H3)
    inbound/                    driving adapters
      domains/<domain>/           routes.go + handlers (the Inbound anatomy, §4)
      compositions/<name>/        the HTTP side of a composition
      middleware/                 host-custom HTTP middleware only — never a package named `http` (reference)
      wire/                       the API's HTTP+JSON conventions (one flat package; reference)
      views/                      the host's private presentation tree
    outbound/                   driven adapters implementing domain ports
      domains/<domain>/           the pgx/turso/memory repositories for ONE domain
    integrations/<tech>/        OPTIONAL: host-local technology adapters with NO domain knowledge (§6)
  pockets/<name>/               a HOST POCKET — this app's wrap/extension/bridge of framework pockets (§5)
    logic/  inbound/  outbound/   any non-empty subset; never an empty directory
  workshop/                     the host's developer-time tools (migration runner + ledger, testdb, steward, …)
```

**H0 is a positive check, never inert.** A host has at least one production Go
package under `cmd/<binary>/`, and every production Go package in the host
lives under `cmd/`, `internal/`, `pockets/`, or `workshop/`; arbitrary
code-bearing roots such as `app/`, `pkg/`, or `src/` are findings. This closes
the otherwise-trivial escape where a host omits every guarded directory and
passes clean. Documentation, deployment, and plan roots with no Go packages
are out of H0's scope entirely.

Inside those roots the shape is fixed:

- if `internal/` exists it contains only `logic`, `inbound`, `outbound`,
  `integrations`;
- `internal/logic` contains only `domains/` and `compositions/`;
  `internal/inbound` only `domains/`, `compositions/`, `middleware/`, `wire/`, `views/`;
  `internal/outbound` only `domains/` — **`internal/outbound/compositions/`
  does not exist** (§3, H3: a composition owns nothing stored, so it has no
  driven side);
- a Go package anywhere else under those roots is a finding, reported with the
  home it belongs in;
- `pockets/<name>/` contains only `logic`, `inbound`, `outbound` — at least
  one of them, never an empty directory, and never `internal/`, `stores/`,
  `views/`, `domain/`, or `migrations/`;
- `workshop/` is shape-exempt (a tool tree grows what it needs) but is one of
  the four sanctioned Go-bearing roots; `cmd/` is constrained further by H7
  and H10.

## 3. The rules — H0 through H10

Every rule gets one canonical sentence. These eleven lines are what
`gopernicus guard --list` prints, and they are the wording a finding is
argued against.

| id | the rule, in one line |
|---|---|
| **H0** | A host's production Go packages live only under `cmd/`, `internal/{logic,inbound,outbound,integrations}`, `pockets/<name>/{logic,inbound,outbound}`, or `workshop/`, and at least one package sits under `cmd/<binary>/`. |
| **H1** | `internal/logic/**` imports only the standard library, `sdk/...`, and its own `internal/logic/...` — an allow-list, with no escape valve. |
| **H2** | A logic domain never imports a composition, and never imports another logic domain. |
| **H3** | A composition declares a `Dependencies` struct whose every field is a non-empty `*Service` interface declared in that same package, imports at least one logic domain, and holds no repository port, no storage import, and no transaction handle. |
| **H4** | `internal/inbound/**` may reach `internal/logic`, `sdk`, framework pocket cores and their `views/<pkg>`, `ui/*`, and a host pocket's `logic` and `inbound` — never `internal/outbound`, `internal/integrations`, or a host pocket's `outbound`. |
| **H5** | `internal/outbound/domains/<d>` imports, among host packages, only `internal/logic/domains/<d>`, `internal/integrations/*`, and host pockets — never inbound, never another logic domain — and `internal/integrations/<tech>` holds no domain, importing no logic, inbound, outbound, or pocket of either scope. |
| **H6** | A host pocket extends at least one framework pocket, its `logic` stays on stdlib + `sdk` + framework pocket rims, and no part of it imports `internal/*` or another host pocket. |
| **H7** | Only `cmd/**`, `internal/outbound/**`, `internal/integrations/**`, a host pocket's `outbound/**`, and `workshop/**` may import a framework pocket's `stores/*`, a framework integration, or a database driver. |
| **H8** | Every `internal/inbound/domains/<d>`, `internal/inbound/compositions/<c>`, and `internal/outbound/domains/<d>` has its `internal/logic` counterpart — the implication runs one way only. |
| **H9** | `.Underlying()` is called nowhere outside `internal/integrations/` and `workshop/`, and `RowToStructByNameLax` appears nowhere. |
| **H10** | `cmd/**` is wiring only — it declares no interface type and contains no SQL statement literal. |

### The import law (H1, H2, H4, H5, H6, H7, H8)

This table is the normative form of the import rules. "Framework" means `sdk`,
`integrations/*`, `pockets/*` (cores, stores, views), and `ui/*`; **host**
paths are relative to the host's own module path. Production files unless a
row says otherwise — `_test.go` exemptions are named here, never implied.

| id | package family | may import | never imports |
|---|---|---|---|
| **H1** the one rule | `internal/logic/**` | stdlib, `sdk/...`, `<module>/internal/logic/...` | anything else. This is an ALLOW-list, not a deny-list: no third-party library enters the hexagon, and a pure algorithm goes to `sdk` or to a small util outside logic. There is no escape valve in v1 — a host that needs one files a lesson, and the rule grows an echoed allow-list in a later minor if the demand is real. |
| **H2** two tiers | `internal/logic/domains/<d>` | as H1 | `internal/logic/compositions/*`, and **another domain's package**. Structural typing gives a domain the narrow interface it needs without the import; the reference host has zero domain→domain edges. **This absolute form is a HOST rule** — ARCHITECTURE.md §"The tier rules" keeps its "or only a narrow read-port / by ID" allowance for POCKET interiors. |
| **H3** compositions | `internal/logic/compositions/<c>` | as H1, plus the five rules below | — |
| **H4** inbound | `internal/inbound/**` | `internal/logic`, `sdk`, framework pocket cores + their `views/<pkg>`, `ui/*`, host `pockets/*/{logic,inbound}`, third-party view technology | `internal/outbound/**`, `internal/integrations/**` (`_test.go` exempt), host `pockets/*/outbound`. An inbound composition importing a host pocket's *inbound* is sanctioned — the reference's client-hub composition does exactly that. |
| **H5** driven side | `internal/outbound/domains/<d>` | among HOST packages: only `internal/logic/domains/<d>`, `internal/integrations/*`, and host `pockets/*` (the adapter for a domain-declared authorization or notification port); framework freely — `sdk`, `integrations/*`, framework pocket cores and stores | `internal/inbound/**`; any OTHER `internal/logic/domains/<x>` — an adapter serves one domain, and SQL joining across domains is exactly the anti-pattern the peers rule exists for. `_test.go` and `workshop/**` are exempt. |
| **H5** (cont.) | `internal/integrations/<tech>` | stdlib, `sdk`, third-party, framework `integrations/*` | `internal/logic`, `internal/inbound`, `internal/outbound`, host `pockets/*`, and framework `github.com/gopernicus/gopernicus/pockets/*` — a technology adapter that knows a pocket is a driven adapter wearing the wrong hat. |
| **H6** host pockets | `<host-module>/pockets/<n>/*` | §5 | §5; and across all its production packages a host pocket must carry at least one framework-pocket rim import. |
| **H7** naming adapters | everything EXCEPT `cmd/**`, `internal/outbound/**`, `internal/integrations/**`, host `pockets/*/outbound/**`, `workshop/**` | — | framework `pockets/*/stores/*`, `github.com/gopernicus/gopernicus/integrations/...`, and the recognized database drivers. `workshop/**` is exempt because migration runners, seeders, and test harnesses are REQUIRED to name adapters — migrations are host-owned and applied pre-boot. Rule text and implementation list the same recognized drivers; an unknown driver is a review finding until a later minor adds it. |
| **H8** the mirror | directories | — | `internal/inbound/domains/<d>` ⇒ `internal/logic/domains/<d>` exists; `internal/inbound/compositions/<c>` ⇒ `internal/logic/compositions/<c>`; `internal/outbound/domains/<d>` ⇒ `internal/logic/domains/<d>`. One-directional in all three: a logic domain may legitimately have no API and no storage yet. The converse ("inbound implies outbound too") is NOT ratified — the reference's `clienthub` has inbound + logic and no outbound, correctly. |

This table REPLACES the four-row import column that
[`ARCHITECTURE.md`](../ARCHITECTURE.md) §"The app pattern (hexagonal)" carried
before 2026-08-27. ARCHITECTURE keeps the framework-wide doctrine that pockets
also cite — §"The one rule", §"Where a port lives", §"sdk vs internal/logic",
§"Inside internal/logic — two tiers", §"The tier rules", §"Where a 'doesn't
fit one domain' thing goes". This page cites those sections; it does not
restate them.

### H3 — the composition rule, in full

A composition is **composed of domains and owns nothing stored**. Nobody takes
another domain's repositories: compositions and domains alike receive the
domain SERVICE through a narrow service interface they declare themselves. For
every package under `internal/logic/compositions/<name>`, walked recursively:

1. **No repository port anywhere.** No `type XRepository`, and no field,
   parameter, or result whose type name ends in `Repository` — through
   pointers, slices, maps, funcs, variadics, channels, generics
   (`ReferenceRepository[media.Reference]`), and qualified selectors.
2. **No storage import.** Nothing under `internal/outbound/`,
   `internal/integrations/`, `github.com/gopernicus/gopernicus/integrations/`,
   `pockets/*/stores/*`, or a database driver (`github.com/jackc/pgx`,
   `github.com/tursodatabase/`, `database/sql`).
3. **A `Dependencies` struct** whose every field is a `*Service`-suffixed
   **interface declared in the same package** — the consumer-owned narrow
   slice of a domain service — and none of them empty. A type ALIAS
   (`type DirectoryService = directory.Service`) is a finding: the whole point
   is that the composition declares the narrow shape itself.
4. **At least one `internal/logic/domains/<domain>` import.** The DEFINITION
   is "spans domains" (ARCHITECTURE.md §"Where a 'doesn't fit one domain'
   thing goes"); the guard FLOOR is one, because no AST can judge whether a
   one-domain composition is thin sequencing that belongs in a handler. The
   doc says which; the guard holds the floor.
5. **No transaction handle.** No field, parameter, or result typed
   `Transactor` (`sdk/foundation/crud`) or `Tx`. A composition owns the
   cross-domain workflow, its invariant, and its sequencing — never a
   repository port, a storage import, or a transaction handle. A workflow
   that needs an atomic write across two domains is the signal those
   aggregates belong in ONE domain.

And for every package under `internal/logic/domains/`: no import of a
composition (H2). The inbound mirror: `internal/inbound/compositions/<name>`
never imports `internal/outbound/` in production code — `_test.go` may
construct adapters.

**Not a composition.** Thin sequencing (a handler calls A then B) lives in the
inbound handler. A host's wrap of framework pockets is a host pocket (§5). An
adapter that satisfies one pocket's port using another pocket is a host
pocket's OUTBOUND, not an "outbound composition" — that slot does not exist.
A note, not a rule: embedding a domain's whole `Service` interface inside a
declared `*Service` interface passes the AST and defeats "narrow"; reviewers
watch for it.

### H9 — hygiene

`.Underlying()` is called nowhere except under `internal/integrations/` and
`workshop/`, and `RowToStructByNameLax` appears nowhere: the `crud.Transactor`
seam is the only route to a raw pool, and struct scanning is strict. Both are
checked on the AST (a `CallExpr` whose selector is `Underlying`; the
identifier `RowToStructByNameLax`), so comments and split-token tricks are
irrelevant in both directions. These mirror this repo's own G9/G10.

## 4. Inbound anatomy — inside `internal/inbound/` (ratified 2026-07-08)

> **Amendment 2026-08-27 (host-layout-contract, ruling 1 — the reference wins):** in the tree and prose below, the transport-plumbing package is `middleware/` (gps-360-go's charter forbids a package named `http`) and `wire/` — the API's HTTP+JSON conventions, one flat package — is a sanctioned sibling. Those two names are the only edits to the 2026-07-08 text.


This section was RATIFIED 2026-07-08 in `ARCHITECTURE.md` and moved here
verbatim on 2026-08-27, when the host contract got its own home. Dates and
ratification lines are the original ones.

Adopted from Segovia v2 (segovia-lessons flag #1, 2026-07-08) — the host
app built in tandem with this framework and the living reference
implementation. The future `gopernicus new domain` scaffold (workshop-v2)
should emit this shape; until that scaffold exists, adoption is by hand:
read this section, apply it, use Segovia v2 as the worked example.

```
internal/inbound/
  domains/<domain>/     # one package per app-local domain
    routes.go           #   the ONE readable route table — never split
    api.go              #   JSON handlers (single-resource degenerate form)
    html.go             #   HTML page handlers (fragments.go when htmx lands)
    views.go            #   the render PORT — methods return web.Renderer
    templates/          #   bundled default implementation (templ), co-located
  middleware/           # host-custom HTTP middleware only — never handlers
  wire/                 # the API's HTTP+JSON conventions (one flat package)
  views/                # the GLOBAL presentation tree: shared Shell/layouts,
                        #   the host's PRIVATE kit — the app-local theme root
                        #   (the reusable, importable kit is top-level ui/)
```

- **The render port (FS3 scaled to app-local).** `views.go` defines the
  domain's presentation port; methods return `web.Renderer`. templ is the
  default, never the contract: `templates/` is the bundled implementation,
  implements the port structurally, and never imports the transport.
  View-tech dependencies ride the app module's go.mod and touch only
  `internal/inbound` — `internal/logic` stays sdk-only (the one rule).
- **The theming seam.** `internal/inbound/views/` holds the shared
  `Shell`/layouts and the host's private kit, consumed by every domain's
  templates; the reusable, importable counterpart is the top-level `ui/`
  family (`ui/goth`), which a host imports instead of growing a bespoke
  kit here (the UI-implementation module kind, GOTH-0.2). A themed kit is
  a new implementation of the ports plus one
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
  `pockets/events/internal/inbound/events/routes.go` is the blessed
  example. The never-split rule constrains the route *table*, not the
  co-residence of a few handlers.
- **Pockets mirror the file anatomy, not the tree** (D1, ratified
  2026-07-08). A pocket is its one domain, so the `domains/` level
  flattens to `internal/inbound/<pocket>/`
  (`pockets/cms/internal/inbound/cms/`), carrying the same file anatomy
  with a `Mount` dispatcher in `routes.go` and per-resource
  deny-by-absence `mountX` helpers living in their resource files. `middleware/`
  keeps one meaning on both sides of the line — plumbing — and a pocket
  has no `middleware/` until real plumbing appears. The global views tree and
  co-located `templates/` are **app-only**: a pocket core requires sdk
  only (FS1), so its render port lives in the core and its bundled default
  is the `views/<pkg>` sibling module (FS3); the pocket theming seam is
  embed-the-sibling-default (live override: `examples/cms/internal/theme/`).
  See `pockets/README.md` §2.

**`internal/outbound` vs `integrations`.** An `integration` is the *reusable*
connection/client to an external system (the turso connector). `internal/outbound`
is the *app-specific* code that implements a domain port using one (the post
repository's SQL + schema). A connector that fully implements an `sdk` facility
port (e.g. a gcs filestore → `sdk/capabilities/filestorage`) needs **no** `internal/outbound`
code — the app just wires it in `cmd`.

**Repositories: app-specific vs pocket store adapter.** A repository is either
*app-specific* (its SQL belongs to one app → `internal/outbound`) **or** a
*pocket store adapter* for a reusable domain (its SQL belongs to the pocket →
`pockets/<name>/stores/<package>`, its own module). The moment a domain becomes a
reusable pocket module, its store is **not** host-app code — it is a pocket
store adapter module, so a host that brings a different datastore never pulls the
pocket's driver into its module graph. The CMS pocket demonstrates this: the
datastore-free `pockets/cms` core depends on its repository ports, and
`pockets/cms/stores/turso` is the separate module that supplies the libSQL
implementation + migrations.

## 5. Host pocket vs domain (H6)

A **host pocket** `pockets/<name>/{logic,inbound,outbound}` is this
application's wrap, extension, or bridge of one or more FRAMEWORK POCKETS,
laid out as its own hexagon even where that is a little redundant. The
reference host's `pockets/auth` is the worked example: `logic` is `model.go`
alone — principals, resources, permissions, roles, and the `RoleModel()` the
authorization engine runs; `inbound` is whoami plus steward-only role
administration; `outbound` is the framework's pgx stores placed in the host's
`auth` schema.

**The test — host pocket, app-local domain, or both:**

- The package imports a framework pocket's public rim
  (`github.com/gopernicus/gopernicus/pockets/<p>`, `.../pockets/<p>/domain/...`)
  and owns **no aggregate of its own** — its durable state is the pocket's, or
  sibling tables keyed to the pocket's records and owned by the host pocket's
  outbound → **host pocket**.
- The package declares a repository port for records the HOST owns and imports
  no pocket → **app-local domain**, under `internal/logic/domains/<d>`.
- Both are true → **split**. The domain owns the records and declares the
  ports; the host pocket's outbound adapts the framework pocket into them, or
  `cmd` injects the framework pocket's Service into a domain-declared
  interface.
- Pure presentation over a pocket — a theme override embedding the pocket's
  `views/<pkg>` default — is the host pocket's **inbound**
  (`pockets/cms/inbound/theme`).
- An in-memory or bespoke implementation of a pocket's repository ports is the
  host pocket's **outbound** (`pockets/cms/outbound/memstore`). A host memstore
  keeps its zero-infra proof role (`pockets/README.md` §2); only its placement
  is fixed here.

**An outbound-only or inbound-only host pocket is legal.** The trio is the
shape, not a minimum — `logic/`, `inbound/`, `outbound/` in any non-empty
subset, and never an empty directory.

**H6 — host-pocket isolation.**

- `<host-module>/pockets/<n>/logic` imports only the standard library,
  `sdk/...`, framework pocket CORES
  (`github.com/gopernicus/gopernicus/pockets/<p>`,
  `.../pockets/<p>/domain/...`; a framework pocket's public `memstore/` and
  `storetest/` from `_test.go` ONLY — the reference's
  `pockets/auth/logic/model_test.go` imports `authorization/memstore` to test
  the model against the real engine, and that is sanctioned), and its own
  `<host-module>/pockets/<n>/logic/...`. Never `internal/*`, never framework
  `pockets/*/{stores,views}/*` from production code, never `integrations/*`,
  never another host pocket.
- `<host-module>/pockets/<n>/inbound` and `<host-module>/pockets/<n>/outbound`
  never import `internal/logic`, `internal/inbound`, `internal/outbound`, or
  another host pocket. The dependency runs host-internal → host pocket, never
  back, or the pocket stops being liftable. They MAY import their own host
  pocket's `logic`. A host pocket's outbound MAY import several framework
  pockets and their stores and views — it IS the cross-pocket bridge (a
  delivery adapter putting authentication's work on the jobs pocket is the
  canonical case). **Two host pockets meet only in `cmd`.**
- Across all production packages in `pockets/<n>`, **at least one framework
  pocket rim import is required**. This is the machine-checkable floor for "a
  host pocket extends a framework pocket"; a local package with no such edge
  belongs under `internal/` instead. The no-host-owned-aggregate clause stays a
  review obligation — no AST can determine aggregate ownership.
- **`internal/logic` never imports host `pockets/`** (H1's allow-list makes
  this structural). A domain that needs an authorization decision declares its
  own interface; the adapter lives in THAT domain's outbound, where H5 permits
  the host-pocket import, or `cmd` injects the engine. `internal/inbound` and
  `cmd` MAY import a host pocket's `logic` and `inbound` — the reference's
  inbound domains import `auth` for gate vocabulary, and H4 sanctions an
  inbound composition importing a host pocket's inbound.

## 6. `cmd`, `workshop`, and `internal/integrations`

**`internal/integrations/<tech>/`** holds host-local **technology adapters
with no domain in them** — a driver wrapper, a vendor client not worth an
upstream module, the one place `.Underlying()` is allowed (H9). Its imports
are H5's second row. Always write the prefix: bare `integrations/…` means the
framework's shared connectors, `internal/integrations/…` means this host's.
An "integration" that imports two logic domains is not a technology adapter —
it is a driven adapter of one of them, and it belongs in
`internal/outbound/domains/<d>` with the other domain's types travelling
through that domain's own vocabulary or a domain-declared interface.

**`cmd/`** is the composition root and nothing else (**H10**). The
machine-checkable floor is narrow: it declares no interface types — a root
wires, it declares nothing for others to implement — and contains no SQL
statement literal (a string literal whose trimmed, upper-cased prefix is
`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `ALTER`, or `DROP`). The
normative rule is wider than its floor: provider behavior — a mutation guard,
a membership resolver, a purge routine, a host-registered content type's
renderer — belongs in `pockets/<n>/{logic,inbound,outbound}`,
`internal/outbound`, or `internal/integrations`. `main.go` constructs and
connects. `cmd/<binary>/views/` (the single-binary theme override of §4)
remains allowed: presentation, not provider behavior. The floor is achievable
in practice — the reference host's `cmd/` has zero interface declarations and
zero SQL literals — but passing it is not proof that every function up there
is wiring. Reviewers enforce the behavior-free clause; a guarded example in
this repo must satisfy it in substance, not merely pass the AST floor.

**`workshop/`** is the host's developer-time tool tree: the migration runner
and its host-owned ledger, a test-database helper, a seeder, a steward
command. It is exempt from H5 and H7 — a tool opens pools and names adapters
by design, and migrations are host-owned and applied pre-boot, never by the
framework at startup — and from H0's internal-shape rules. It is still one of
the four sanctioned Go-bearing roots, and H9 still applies.

## 7. Retrofit an existing host

The three real hosts adopt this contract by retrofit, not by scaffolding. The
`gopernicus guard` verb ships in `workshop/gopernicus` **v0.3.0**; the recipe
below describes the state a host lands in once it does.

1. **Pin the guard in `go.mod`** — a developer-time tool, never a runtime
   import:

   ```
   tool github.com/gopernicus/gopernicus/workshop/gopernicus
   ```

   with the matching `require … v0.3.0`. A host still working against an
   unpublished checkout adds a local `replace` alongside it; the pin is what
   makes a guard update a version bump instead of a Makefile copy.

2. **Run it:** `go tool gopernicus guard`. It prints one status line per rule —
   including the rules that found nothing to check, so an inert guard can
   never read as a clean one — then one line per finding, then a summary.
   Exit 0 clean, 1 findings, 2 for an invocation or input failure.

3. **Fix the findings.** There is no `--skip`, no allow file, and no config: a
   skip flag is drift by configuration and could never be removed. A
   retrofitting host runs the guard red until it conforms, and adds it to
   `make guard` when it is green.

4. **Collapse the copied guard targets** into one:

   ```make
   guard:
   	go tool gopernicus guard
   ```

   Host-specific rules with no H-equivalent stay as their own targets beside
   it. Everything that merely mirrors this repo's guards goes.

5. **Delete the drifted greps.** A copied `guard-one-rule` regex is H1, a
   copied two-tier grep is H2/H3, the hygiene pair is H9, and a grep that
   still names a path retired by a rename is dead code that passes for the
   wrong reason. One binary, one wording, one version.

New H-rules ship in a minor version of `workshop/gopernicus`, and a host
adopts them by bumping its pin — `go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@v0.3.1`.

## 8. Which examples conform

| host | role |
|---|---|
| `examples/cms` | conforming worked example (after PR-B's host-pocket moves) — the Turso host of `pockets/cms` |
| `examples/minimal` | conforming worked example (after PR-B) — the zero-infra host, no driver in its module graph |
| `examples/jobs-minimal` | conforming worked example (after PR-B) — the jobs pocket with host-owned handlers |
| `examples/auth-cms` | the **authentication conformance harness**, not a layout reference |
| `examples/goth-showcase` | the `ui/goth` kit showcase, not a layout reference |

The first three run under this repo's own `make guard` once the guard verb
ships; `auth-cms` and `goth-showcase` carry named, dated exemptions.

**`examples/auth-cms` is a harness, not a model.** It exists to prove the
authentication and authorization surface end to end — OAuth, machine
identity, JWT bearer, security-event audit, ReBAC-decoupled invitations, the
identity/challenge rail, two delivery modes — with zero infrastructure, and
it is the most heavily tested host in this repo. Read it for what the auth
pockets can do and for how their seams are wired together.

**Do not read it for layout.** Its composition root carries provider behavior
that this contract places in a host pocket or an outbound adapter, and it
grows several ad-hoc packages directly under `internal/` that H0 makes
findings. That is a known, dated debt with a named follow-up plan, not a
pattern to copy: the shape to copy is §2, and the worked examples are `cms`,
`minimal`, and `jobs-minimal`.
