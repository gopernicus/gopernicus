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

Rule ids on this page are the **H series** (H = host), H0–H14, one series in
the FS/G tradition. The repo's own Makefile guards keep the `G` series: those
police the framework, these police a host. `gopernicus guard --list` prints
these fifteen sentences verbatim (the verb ships in `workshop/gopernicus`
v0.4.0, §7), so the binary and this page cannot drift.

A second series, the **P series** (P = production posture), P0–P9, is opt-in:
`gopernicus guard --posture` adds it to the H run, and `guard --list --posture`
prints its ten sentences after the fifteen (§3b, `workshop/gopernicus`
v0.5.0; P9 from v0.6.0). The H series is the layout every host keeps; the P
series proves a composition root asks for the production posture the pockets
and connectors enforce at construction.

**Prefix discipline.** Bare `sdk` always means the framework's module; the
host's own vocabulary root is always written "the host's `sdk/`". The same
holds for the two other roots a host shares a name with: rule text says "the
host's `integrations/`" and "the host's `ui/`", while
`github.com/gopernicus/gopernicus/integrations/…` (or "a framework
integration") and framework `ui/*` are the framework's. Paths drawn inside a
`<host>/` tree, and paths like `internal/…` or `pockets/<n>/…` written
relative to the host module, are the host's.

## 2. The layout (H0)

```
<host>/
  cmd/<binary>/                 composition root — concrete construction/wiring, behavior-free (H10);
                                  a CLIENT BINARY may own more (H10 carve-out, §6)
  sdk/                          OPTIONAL: host-local vocabulary — stdlib + framework sdk only (H13, §6)
  integrations/<capability>[/<vendor>]/
                                OPTIONAL: host technology adapters with NO domain knowledge, named only by cmd (H11, §6)
  internal/
    logic/                      the hexagon — imports sdk (and the host's sdk/) only (H1)
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
  pockets/<name>/               a HOST POCKET — this app's wrap/extension/bridge of framework pockets (§5)
    logic/  inbound/  outbound/   any non-empty subset; never an empty directory
  ui/                           OPTIONAL: host presentation packages with no domain (H14)
  workshop/                     the host's developer-time tools (migration runner + ledger, testdb, steward, …)
```

**H0 is a positive check, never inert.** A host has at least one production Go
package under `cmd/<binary>/`, and every production Go package in the host
lives under `cmd/`, `sdk/`, `integrations/`, `internal/`, `pockets/`, `ui/`,
or `workshop/`; arbitrary code-bearing roots such as `app/`, `pkg/`, or `src/`
are findings. This closes the otherwise-trivial escape where a host omits
every guarded directory and passes clean. Documentation, deployment, and plan roots with no Go packages
are out of H0's scope entirely.

Inside those roots the shape is fixed:

- if `internal/` exists it contains only `logic`, `inbound`, `outbound`. The
  former `internal/` slot for technology adapters is retired — the reference
  host deleted it on 2026-08-28 and moved its adapters to top-level
  `integrations/` — and a host adapter now lives in the host's
  `integrations/`;
- the host's `integrations/` is keyed `<capability>[/<vendor>]` exactly like
  the framework's own `integrations/<category>/<tech>` tree; the `<vendor>`
  segment is optional because a vendor client with no capability siblings
  sits at depth one (the reference's `integrations/emailer` beside
  `integrations/filestorage/{s3,gcs}`);
- the host's `sdk/` is the home for repo-local vocabulary that the host's
  integrations and outbound adapters share; its internal layering (`pkg/`,
  `capabilities/`, or flat) is the host's choice — the contract fixes only its
  imports and content (H13);
- the host's `ui/` holds Go presentation packages with no domain — a kit
  showcase, a host-wide component set — and imports only what H14 lists;
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
  the seven sanctioned Go-bearing roots; `cmd/` is constrained further by H7
  and H10.

## 3. The rules — H0 through H14

Every rule gets one canonical sentence. These fifteen lines are what
`gopernicus guard --list` prints, and they are the wording a finding is
argued against.

| id | the rule, in one line |
|---|---|
| **H0** | A host's production Go packages live only under `cmd/`, `sdk/`, `integrations/`, `internal/{logic,inbound,outbound}`, `pockets/<name>/{logic,inbound,outbound}`, `ui/`, or `workshop/`, and at least one package sits under `cmd/<binary>/`. |
| **H1** | `internal/logic/**` imports only the standard library, framework `sdk/...`, the host's own `sdk/...`, and its own `internal/logic/...` — an allow-list, with no escape valve. |
| **H2** | A logic domain never imports a composition, and never imports another logic domain. |
| **H3** | A composition declares a `Dependencies` struct whose every field is a non-empty `*Service` interface declared in that same package, imports at least one logic domain, and holds no repository port, no storage import, and no transaction handle. |
| **H4** | `internal/inbound/**` may reach `internal/logic`, framework and host `sdk`, framework pocket cores and their `views/<pkg>`, `ui/*`, and a host pocket's `logic` and `inbound` — never `internal/outbound`, the host's `integrations/`, or a host pocket's `outbound`. |
| **H5** | `internal/outbound/domains/<d>` imports, among host packages, only `internal/logic/domains/<d>`, the host's `sdk/`, and host pockets — never inbound, never another logic domain, never the host's `integrations/` (it declares the narrow port; `cmd` injects the integration). |
| **H6** | A host pocket extends at least one framework pocket, its `logic` stays on stdlib + `sdk` + the host's `sdk/` + framework pocket rims, and no part of it imports `internal/*` or another host pocket. |
| **H7** | Only `cmd/**`, `internal/outbound/**`, the host's `integrations/**`, a host pocket's `outbound/**`, and `workshop/**` may import a framework pocket's `stores/*`, a framework integration, or a recognized database driver. |
| **H8** | Every `internal/inbound/domains/<d>`, `internal/inbound/compositions/<c>`, and `internal/outbound/domains/<d>` has its `internal/logic` counterpart — the implication runs one way only. |
| **H9** | `.Underlying()` is called nowhere outside the host's `integrations/` and `workshop/`, and `RowToStructByNameLax` appears nowhere. |
| **H10** | In production files, `cmd/**` contains no SQL statement literal, and a composition root under `cmd/<binary>/` declares no interface type; a client binary — a `cmd/<binary>/` tree that imports nothing under `internal/`, `pockets/`, or the host's `integrations/`, and no framework pocket, store, integration, or database driver — may own its commands, API client, wire types, and interfaces. |
| **H11** | A host integration `integrations/<capability>[/<vendor>]` holds no domain: it imports only the standard library, framework `sdk/...`, the host's `sdk/...`, and exactly one connector or vendor client library plus the companion modules its API forces; it never imports `internal/*` or a pocket of either scope, and only `cmd/**` and `workshop/**` import it — tests included. |
| **H12** | The outer fence, which H1, H4, H6 and H7 tighten and never loosen: `internal/**` and `pockets/**`, tests included, import only the standard library, the host module, and the framework's `sdk`, `pockets`, `integrations` and `ui` modules; recognized database drivers are additionally allowed under `internal/outbound/**` and a host pocket's `outbound/**`, recognized view technology under `internal/inbound/**` and a host pocket's `inbound/**`; production files never import the host's `workshop/`. |
| **H13** | The host's `sdk/**` imports only the standard library, framework `sdk/...`, and its own `sdk/...`, and holds no domain entity, repository port, or `Service` — a type two domains both need is still each domain's own. |
| **H14** | The host's `ui/**` imports only the standard library, framework `sdk/...`, framework `ui/*`, recognized view technology, the host's `sdk/...`, and its own `ui/...` — never `internal/*`, a pocket of either scope, or an integration of either scope. |

In H10, "wire types" means the client's own request/response structs; the
server's HTTP+JSON conventions stay in `internal/inbound/wire` (§2, §4). In
H4, `ui/*` is the framework's kit family and the host's own `ui/` alike — the
dependency runs inbound → presentation, never back (H14).

### The import law (H1, H2, H4–H8, H11–H14)

This table is the normative form of the import rules. "Framework" means `sdk`,
`integrations/*`, `pockets/*` (cores, stores, views), and `ui/*`; **host**
paths are relative to the host's own module path, and the host's own `sdk/`,
`integrations/`, and `ui/` roots are always written with "the host's" (§1,
prefix discipline). Production files unless a row says otherwise — `_test.go`
exemptions are named here, never implied.

| id | package family | may import | never imports |
|---|---|---|---|
| **H1** the one rule | `internal/logic/**` | stdlib, framework `sdk/...`, the host's `sdk/...`, `<module>/internal/logic/...` | anything else. This is an ALLOW-list, not a deny-list: no third-party library enters the hexagon, and a pure algorithm goes to framework `sdk` or to a small util outside logic — the host's `sdk/` is that util's named home. There is no escape valve in v1 — a host that needs one files a lesson, and the rule grows an echoed allow-list in a later minor if the demand is real. |
| **H2** two tiers | `internal/logic/domains/<d>` | as H1 | `internal/logic/compositions/*`, and **another domain's package**. Structural typing gives a domain the narrow interface it needs without the import; the reference host has zero domain→domain edges. **This absolute form is a HOST rule** — ARCHITECTURE.md §"The tier rules" keeps its "or only a narrow read-port / by ID" allowance for POCKET interiors. |
| **H3** compositions | `internal/logic/compositions/<c>` | as H1, plus the five rules below | — |
| **H4** inbound | `internal/inbound/**` | `internal/logic`, framework `sdk`, the host's `sdk/`, framework pocket cores + their `views/<pkg>`, framework `ui/*` and the host's `ui/`, host `pockets/*/{logic,inbound}`, recognized view technology | `internal/outbound/**`, the host's `integrations/**` (`_test.go` exempt from this clause only; H11 still applies), host `pockets/*/outbound`. An inbound composition importing a host pocket's *inbound* is sanctioned — the reference's client-hub composition does exactly that. |
| **H5** driven side | `internal/outbound/domains/<d>` | among HOST packages: only `internal/logic/domains/<d>`, the host's `sdk/`, and host `pockets/*` (for example, an adapter for a domain-declared notification port); framework freely — `sdk`, `integrations/*`, framework pocket cores and stores; recognized database drivers (H12) | `internal/inbound/**`; any OTHER `internal/logic/domains/<x>` — an adapter serves one domain, and SQL joining across domains is exactly the anti-pattern the peers rule exists for; the host's `integrations/` — the adapter declares the narrow port it needs and `cmd` injects the integration, which satisfies it structurally (§6). `_test.go` and `workshop/**` are exempt from this row; H11 and H12 still apply to them. |
| **H6** host pockets | `<host-module>/pockets/<n>/*` | §5 | §5; and across all its production packages a host pocket must carry at least one framework-pocket rim import. |
| **H7** naming adapters | everything EXCEPT `cmd/**`, `internal/outbound/**`, the host's `integrations/**`, host `pockets/*/outbound/**`, `workshop/**` | — | framework `pockets/*/stores/*`, `github.com/gopernicus/gopernicus/integrations/...`, and the recognized database drivers. `workshop/**` is exempt because migration runners, seeders, and test harnesses are REQUIRED to name adapters — migrations are host-owned and applied pre-boot. Rule text and implementation list the same recognized drivers (below); an unknown driver is a review finding until a later minor adds it. |
| **H8** the mirror | directories | — | `internal/inbound/domains/<d>` ⇒ `internal/logic/domains/<d>` exists; `internal/inbound/compositions/<c>` ⇒ `internal/logic/compositions/<c>`; `internal/outbound/domains/<d>` ⇒ `internal/logic/domains/<d>`. One-directional in all three: a logic domain may legitimately have no API and no storage yet. The converse ("inbound implies outbound too") is NOT ratified — the reference's `clienthub` has inbound + logic and no outbound, correctly. |
| **H11** host integrations | the host's `integrations/<capability>[/<vendor>]/**` | stdlib, framework `sdk/...`, the host's `sdk/...`, and exactly ONE connector or vendor client library (a framework `integrations/<category>/<tech>` connector, or a vendor SDK with no upstream connector) plus the companion modules that library's API forces on its caller | `internal/*`, host `pockets/*`, and framework `github.com/gopernicus/gopernicus/pockets/*` — a technology adapter that knows a domain or a pocket is a driven adapter wearing the wrong hat. **Imported only by `cmd/**` and `workshop/**`, tests included**: nothing under `internal/` or `pockets/` names it. |
| **H12** the fence | `internal/**` and `pockets/**`, tests included | stdlib; the host module; the framework's `sdk`, `pockets`, `integrations` and `ui` modules; recognized database drivers under `internal/outbound/**` and host `pockets/*/outbound/**` only; recognized view technology under `internal/inbound/**` and host `pockets/*/inbound/**` only | every other module, `golang.org/x/...` included; and, from production files, the host's `workshop/`. H1, H4, H6 and H7 are tighter and always win. A test that needs a real backend or a third-party test helper lives in the integration's own package tests, in a `cmd/**` `_test.go`, or under `workshop/`. |
| **H13** host sdk | the host's `sdk/**` | stdlib, framework `sdk/...`, its own `sdk/...` | everything else; and it holds no domain entity, repository port, or `Service` (reviewer-enforced) — a type two domains both need is still each domain's own. |
| **H14** host ui | the host's `ui/**` | stdlib, framework `sdk/...`, framework `ui/*`, recognized view technology, the host's `sdk/...`, its own `ui/...` | `internal/*`, a pocket of either scope, and an integration of either scope. |

This table REPLACES the four-row import column that
[`ARCHITECTURE.md`](../ARCHITECTURE.md) §"The app pattern (hexagonal)" carried
before 2026-08-27. ARCHITECTURE keeps the framework-wide doctrine that pockets
also cite — §"The one rule", §"Where a port lives", §"sdk vs internal/logic",
§"Inside internal/logic — two tiers", §"The tier rules", §"Where a 'doesn't
fit one domain' thing goes". This page cites those sections; it does not
restate them.

### Recognized lists (H3, H7, H12, H14)

Rule text and implementation list the same names. There is **one** database
driver list, shared by H3 item 2, H7, and H12, so a Turso-backed host's
app-local repositories are never caught by one rule and freed by another:

- **database drivers:** `github.com/jackc/pgx/v5`,
  `github.com/tursodatabase/libsql-client-go`, `modernc.org/sqlite`,
  `github.com/lib/pq`. `database/sql` is the standard library and is not a
  driver; a composition that reaches for it still needs one of these, and is
  caught there.
- **view technology:** `github.com/a-h/templ` — the one view library
  `ui/goth` pins.
- **not the standard library:** an import path whose first element contains a
  dot is third-party, so `golang.org/x/...` (`x/sync/errgroup`, `x/net/html`),
  `go-cmp`, and testcontainers under `internal/**` or `pockets/**` are H12
  findings, tests included — deliberately.

### H3 — the composition rule, in full

A composition is **composed of domains and owns nothing stored**. Nobody takes
another domain's repositories: compositions and domains alike receive the
domain SERVICE through a narrow service interface they declare themselves. For
every package under `internal/logic/compositions/<name>`, walked recursively:

1. **No repository port anywhere.** No `type XRepository`, and no field,
   parameter, or result whose type name ends in `Repository` — through
   pointers, slices, maps, funcs, variadics, channels, generics
   (`ReferenceRepository[media.Reference]`), and qualified selectors.
2. **No storage import.** Nothing under `internal/outbound/`, the host's
   `integrations/`, `github.com/gopernicus/gopernicus/integrations/`,
   `pockets/*/stores/*`, or a recognized database driver (the one list above:
   `github.com/jackc/pgx/v5`, `github.com/tursodatabase/libsql-client-go`,
   `modernc.org/sqlite`, `github.com/lib/pq`).
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
   `Transactor` (`sdk/pkg/list`) or `Tx`. A composition owns the
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

`.Underlying()` is called nowhere except under the host's `integrations/` and
`workshop/`, and `RowToStructByNameLax` appears nowhere: the `transaction.Transactor`
seam is the only route to a raw pool, and struct scanning is strict. Both are
checked on the AST (a `CallExpr` whose selector is `Underlying`; the
identifier `RowToStructByNameLax`), so comments and split-token tricks are
irrelevant in both directions. These mirror this repo's own G9/G10.

## 3b. P0–P9 — production posture, opt-in via `--posture`

A pocket or connector refuses a development-only or unprovable option at
construction when the host passes `production`; these rules prove, before
boot, that the composition root passes the mode, wires the HTTP floor, and
writes every waiver as a literal. They read the H7 adapter-naming trees —
`cmd/**`, `internal/outbound/**`, the host's `integrations/**`, a host pocket's
`outbound/**` — plus `workshop/**` main packages, production files only, and
`.env.example` at the host root; P9 reads the host's `go.mod`, the stores'
`migrations/` and the host ledger. Runtime refusal stays the pocket's job; the
guard proves the composition asks for it. These ten lines are what
`gopernicus guard --list --posture` prints after the H fifteen.

| id | the rule, in one line |
|---|---|
| **P0** | The host resolves its runtime mode from the process environment only: through `environment.LoadEnvForMode`, `environment.ParseMode`, or an `environment.Mode`-typed env field tagged `required:"true"` with no `default:`; scanned production files call `environment.LoadEnv(`/`LoadPath(` nowhere; `ModeDevelopment`/`ModeProduction` appear only as an operand of `==`/`!=` or a `case`. |
| **P1** | For every recognized `Config{` literal or `var x <pkg>.Config` in a scanned function, that function names `RuntimeMode:` or `x.RuntimeMode =`; every `authentication.New` call passes a mode argument and every `authorization.New` call passes `WithRuntimeMode(`. (The guard proves the mode is NAMED; the pocket proves it is honored.) |
| **P2** | `web.TrustProxies(` is the first argument of the first `.Use(` call on the handler the composition root passes to `web.Run`/`HTTPServer`. |
| **P3** | `web.SecurityHeaders(` or `web.SecurityHeadersWithConfig(` appears in a `.Use(` call on that handler. |
| **P4** | A host that builds a `web.ServerConfig` calls its `ValidateProduction()`. |
| **P5** | Every recognized acknowledgement in scanned files is a boolean literal (`true` or `false`), never another expression; an option-form acknowledgement is a direct argument of the `New(` call; `--posture` prints one `ack` line (file:line, flag) per `true`, and `ack (conditional)` when it sits under `if`/`switch` — informational, exit code unchanged. |
| **P6** | A host that mounts authentication builds its keys through `authentication.NewKeys(` with a `KeysInput` filled by `environment.ParseEnvTags` in a scanned file; `--posture` prints one informational `rand` line per `crypto/rand` use in scanned files so a review sees any other key minting (no ban: non-auth keys such as auth-cms's cursor key, `documents.go:43`, are legitimate). |
| **P7** | `.env.example`, when present, carries no **uncommented** `sslmode=disable`/`DB_SSLMODE=disable` line; mentions the host's mode key ONLY in a comment (`# APP_RUNTIME_MODE=…` — an uncommented assignment, even empty, is a finding, because `LoadEnvForMode` refuses it); and names `TRUSTED_PROXY_COUNT` and `PUBLIC_BASE_URL`. |
| **P8** | Every `cms.Config` literal in scanned files names `AdminMiddleware` or `AdminUngatedAcknowledged` (the flip is not a boot refusal, so downstream hosts need the pre-deploy signal; G31 covers only this repo). |
| **P9** | For every store the host mounts, every store migration file has a byte-identical copy in the host ledger; an immutability trigger file may be declined by omitting it, and the posture report says so. |

P0, P1, P5, P6 and P8 report `no scope` unless a scanned file imports a
package on the list below (or a package beneath one); P2 and P3 report it
without a `web.Run` or `HTTPServer` call, P4 without a built
`web.ServerConfig`, P7 without a `.env.example`, and P9 when no store is
mounted or none ships a `migrations/` directory. The `ack` and `rand`
lines are review evidence, never findings: a waiver a host chose stays
visible in every CI log, and so does every other place it mints key material.

**P9 in full.** A store is mounted when the host's `go.mod` requires a
framework store module `github.com/gopernicus/gopernicus/pockets/<pocket>/stores/<dialect>`
and a production file imports it (a store named only by tests is not mounted);
it is in scope when its module ships a `migrations/` directory (the pgx and turso
stores do; the memory, goredis and firestore stores do not). `--posture`
resolves the module directory with `go list -m -f '{{.Dir}}' <module>` in the
host root — a `go.work`, a `replace` or the module cache answers, bounded by a
timeout — and a resolution error is a finding naming the module. The host
ledger is `workshop/migrations/primary/`. For each `NNNN_<name>.sql` in the
store's `migrations/` (`_`-prefixed files skipped), the ledger holds a file with
the same `<name>` after any numeric prefix (hosts renumber; the version is the
filename, so a renumbered copy keeps its bytes) and the same bytes. A missing or
edited copy is one finding per store file; the fix is a re-export — copy the
store file into the ledger, never edit a copy. The one optional file is an
immutability trigger file (`*_immutable.sql`): a host that declines the
triggers omits it from its ledger before applying, and the P9 status line
names it — `P9 ok (2 stores; declined: 0022_security_events_immutable.sql)`.
A trigger file the ledger does hold must still be byte-identical.

### Recognized posture surfaces (P0, P1, P5, P6, P8)

Rule text and implementation name the same identifiers, maintained like §3's
recognized lists: a rule sentence names this list, the list names the
identifiers, and a new surface is a list edit shipped in a workshop minor. The
guard matches them as text in the AST; it never imports the packages that
declare them.

- **mode-accepting constructors and `RuntimeMode` records:**
  `authentication.New` (the mode is its third, positional argument),
  `authorization.WithRuntimeMode(` (an option of `authorization.New`),
  `cms.Config.RuntimeMode`, `pgxdb.Config.RuntimeMode`,
  `tursodb.Config.RuntimeMode`, `otel.Config.RuntimeMode` — the packages
  `github.com/gopernicus/gopernicus/pockets/{authentication,authorization,cms}`,
  `…/integrations/datastores/{pgxdb,turso}`, and `…/integrations/tracing/otel`,
  matched by import path whatever the local name.
- **acknowledgements:** `AuditDisabledAcknowledged` (option form
  `WithAuditDisabledAcknowledged()` in both the authentication and the
  authorization pocket), `IdentityConfig.SingleFactorAcknowledged`,
  `DeliveryConfig.OpportunisticTLSAcknowledged`, `DeliveryJobsAcknowledged`,
  `DeliveryEphemeralAcknowledged`, `AdminUngatedAcknowledged`,
  `TLSUnverifiedAcknowledged`, `TLSDisabledAcknowledged`,
  `AppendOnlyWritableAcknowledged`,
  `MaintenanceConfig.MaintenanceRunnerAcknowledged`.
- **the key constructor:** `authentication.NewKeys`, fed an
  `authentication.KeysInput`.

`--posture` requires workshop/gopernicus ≥ v0.5.0; an older pin refuses the flag — the binary
exits 2, and a `go run …@<old>` invocation prints `exit status 2` and returns 1 — so CI treats any
non-zero status as a broken invocation, never a pass.

## 4. Inbound anatomy — inside `internal/inbound/` (ratified 2026-07-08)

Inbound owns application access policy: resolve the caller and actual command or
query, authorize it, then invoke logic. Domain services do not invoke host role or
permission callbacks. They still enforce validation, tenant restrictions and
business invariants. Collection handlers arrange permitted data restrictions;
storage applies those restrictions before pagination without choosing policy.
Every entry point owns principal admission. Authorization's tuple writers
serialize data integrity and committed changes; they do not repeat permission
checks. Ordinary route admission does not hold a snapshot through subsequent
application work.

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
  `pockets/events/inbound/http/adapter.go` is the blessed
  example. The never-split rule constrains the route *table*, not the
  co-residence of a few handlers.
- **Pockets mirror the file anatomy, not the tree** (D1, ratified
  2026-07-08). A pocket is its one domain, so the `domains/` level
  flattens to public `inbound/http/` in the audited framework pockets. CMS
  retains `internal/inbound/cms/` until its deferred audit. The adapter carries the same file anatomy
  with a `Mount` dispatcher in `routes.go` and per-resource
  deny-by-absence `mountX` helpers living in their resource files. `middleware/`
  keeps one meaning on both sides of the line — plumbing — and a pocket
  has no `middleware/` until real plumbing appears. The global views tree and
  co-located `templates/` are **app-only**: a pocket core requires sdk
  only (FS1), so its render port lives in the core and its bundled default
  is the `views/<pkg>` sibling module (FS3); the pocket theming seam is
  embed-the-sibling-default (live override: `examples/cms/pockets/cms/inbound/theme/`).
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
alone — principals, resources, permissions, roles, and the unified `decisions.Model` the
authorization engine runs; `inbound` is whoami plus steward-only role
administration; `outbound` is the framework's pgx stores placed in the host's
`auth` schema.

**The test — host pocket, app-local domain, or both:**

- The package imports a framework pocket's public rim
  (`github.com/gopernicus/gopernicus/pockets/<p>`, `.../pockets/<p>/logic/...`,
  or its public `inbound/http`; CMS retains its earlier domain paths)
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
  `sdk/...`, the host's `sdk/...`, framework pocket CORES
  (`github.com/gopernicus/gopernicus/pockets/<p>`,
  `.../pockets/<p>/logic/...`; CMS retains its domain paths) and its own
  `<host-module>/pockets/<n>/logic/...`. A framework pocket's
  `stores/memory` and `stores/storetest` are allowed from `_test.go` only — the
  reference's `pockets/auth/logic/model_test.go` imports authorization memory to test
  the model against the real engine, and that is sanctioned. Never `internal/*`, never framework
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

## 6. `cmd`, `workshop`, `integrations`, and `sdk`

**`integrations/<capability>[/<vendor>]/`** holds the host's **technology
adapters with no domain in them** — a mail seam over the framework's SendGrid
connector, an S3 store carrying the host's key-prefix policy, a vendor client
with no upstream module worth publishing — and is the one place outside
`workshop/` that `.Underlying()` is allowed (H9). Its imports are H11: the
standard library, framework `sdk/...`, the host's `sdk/...`, and exactly one
connector or vendor client library plus the companion modules that library's
API forces — never `internal/*`, never a pocket of either scope. It returns
standard-library, framework-`sdk`, or host-`sdk/` types and **never a domain
type**; that clause is reviewer-enforced, and its structural floor is "it
imports no `internal/logic`". **Only `cmd` names it** (H11, tests included):
the consumer — an outbound adapter or a logic domain — declares its own narrow
port, and `cmd` injects the integration, which satisfies that port
structurally (the reference's `cmd/server/email.go` and
`cmd/server/filestorage.go`). Its real-backend tests live in its own package,
in `cmd/**` tests, or under `workshop/` — never in `internal/outbound` tests,
which H11 and H12 keep off it. How a host integration differs from a driven
adapter is §4's "`internal/outbound` vs `integrations`". An "integration" that
imports two logic domains is not a technology adapter — it is a driven adapter
of one of them, and it belongs in `internal/outbound/domains/<d>` with the
other domain's types travelling through that domain's own vocabulary or a
domain-declared interface.

**`sdk/`** holds the host's repo-local vocabulary shared by its integrations
and outbound adapters — a value type, an error kind, or a small stdlib-only
helper two adapters both need. Its imports and content are H13: the standard
library, framework `sdk/...`, and its own `sdk/...`; no domain entity, no
repository port, no `Service`. A host that has nothing to share has no `sdk/`.
ARCHITECTURE.md's five-point sdk admission test governs the *framework*
`sdk` only; the host's `sdk/` is H1's "small util outside logic", named.
Whether a package there later moves up into the framework or stays local is a
host decision.

**`cmd/`** is the composition root and nothing else (**H10**). The
machine-checkable floor is narrow: a composition root declares no interface
types — a root wires, it declares nothing for others to implement — and no
`cmd/**` file contains an SQL statement literal (a string literal whose
trimmed, upper-cased prefix is `SELECT`, `INSERT`, `UPDATE`, `DELETE`,
`CREATE`, `ALTER`, or `DROP`). Both clauses read production files only: a
live-leg test's fixture reset behind a build tag is a test, not wiring. The
normative rule is wider than its floor: provider behavior — a mutation guard,
a membership resolver, a purge routine, a host-registered content type's
renderer — belongs in `pockets/<n>/{logic,inbound,outbound}`,
`internal/outbound`, or the host's `integrations/`. `main.go` constructs and
connects.

**A client binary is the H10 carve-out.** A `cmd/<binary>/` tree that imports
nothing under `internal/`, `pockets/`, or the host's `integrations/`, and no
framework pocket, store, integration, or recognized database driver, is a
**client binary**: the classification is by imports, so the guard never
guesses. A client binary may own its commands, its API client, its wire types
(the client's own request/response structs — the server's HTTP+JSON
conventions stay in `internal/inbound/wire`), and the interfaces its command
packages declare over that client — the reference's
`cmd/gps360/{cli,client,exit,read,write}`, whose command packages declare
narrow ports over the client exactly as consumers are taught to above. It
still contains no SQL statement literal. Every other `cmd/<binary>/` is a
composition root and keeps the no-interface clause. `cmd/<binary>/views/` (the single-binary theme override of §4)
remains allowed: presentation, not provider behavior. The floor is achievable
in practice — the reference host's `cmd/` has zero interface declarations and
zero SQL literals — but passing it is not proof that every function up there
is wiring. Reviewers enforce the behavior-free clause; a guarded example in
this repo must satisfy it in substance, not merely pass the AST floor.

**`workshop/`** is the host's developer-time tool tree: the migration runner
and its host-owned ledger, a test-database helper, a seeder, a steward
command. It is exempt from H5 and H7 — a tool opens pools and names adapters
by design, and migrations are host-owned and applied pre-boot, never by the
framework at startup — from H11's "only `cmd`" clause (a `workshop/mailcheck`
legitimately names an integration), and from H0's internal-shape rules. It is
still one of the seven sanctioned Go-bearing roots, and H9 and H12 still
apply: nothing under `internal/` or `pockets/` links it from production code.

**The production composition root (P0–P9).** One shape serves every
environment; the mode decides what is refused:

```go
func main() {
	// The mode has one source, the process environment: .env is loaded in
	// development and never opened in production, and a .env that names the
	// key is refused.
	mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")
	if err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}
	// … logger, signal context …
	if err := run(ctx, log, mode); err != nil { /* … */ }
}

func run(ctx context.Context, log *slog.Logger, mode environment.Mode) error {
	srv := web.ServerConfig{}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
	}
	if mode.IsProduction() {
		if err := srv.ValidateProduction(); err != nil { // https PUBLIC_BASE_URL, a bare origin
			return err
		}
	}
	db, err := pgxdb.Open(ctx, pgxdb.Config{ /* … */ RuntimeMode: mode })
	// … every recognized surface gets the same mode (§3b) …
	router := web.NewWebHandler()
	router.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(),
		web.RequestID(), web.Logger(log), web.Panics(log))
	// … mount pockets, host routes, /healthz …
	return web.Run(ctx, router, srv, log)
}
```

- **TLS terminates at the edge** — the load balancer, ingress, or platform
  router — and `web.Run` serves plain HTTP behind it. **HSTS is the app's to
  send**: `web.SecurityHeaders()` writes `Strict-Transport-Security` with the
  browser defaults (`nosniff`, `Referrer-Policy`, `X-Frame-Options: DENY`,
  `Permissions-Policy`), and browsers ignore HSTS received over plain HTTP, so
  development on `http://localhost` is unaffected. A page that owns a stricter
  policy (the authentication pages' CSP and `no-referrer`) keeps it;
  `web.SecurityHeadersWithConfig` adjusts the preset for a host that embeds
  its pages or still has plain-HTTP sibling subdomains.
- **`TrustProxies` is outermost**, so every later layer — the logger, rate
  limits, and the audit rail — sees the client IP resolved from the
  `TRUSTED_PROXY_COUNT` rightmost `X-Forwarded-For` hops, never a spoofable
  leftmost one.
- **A host that mounts authentication installs its client-attribution stamp**,
  `router.Use(authenticationComponents.HTTP.ClientInfoMiddleware())`, after the
  `TrustProxies` `Use` and once the adapter exists, so audit rows and
  `RateLimitByIP` on host routes see the client; the pocket's own routes carry
  it already. Installed before `TrustProxies`, it would stamp the peer address.
  Attribution is only as good as `TRUSTED_PROXY_COUNT` and a locked-down origin:
  one reachable solely through those proxies.
- **`ValidateProduction` runs under production only**: development keeps
  `Origin()`'s `http://HOST:PORT` fallback.
- **The mode comes from the process environment**: `make run` exports
  `APP_RUNTIME_MODE=development`; production sets it through systemd
  `Environment=`/`EnvironmentFile=`, `docker --env-file`, or platform secrets.
  The same mode is passed to every recognized surface, and every
  acknowledgement is a literal beside the thing it waives (§3b).

## 7. Retrofit an existing host

The three real hosts adopt this contract by retrofit, not by scaffolding. The
`gopernicus guard` verb ships in `workshop/gopernicus` **v0.4.0**
(host-contract-reconciliation, issue #90); the recipe below describes the
state a host lands in.

1. **Pin the guard in `go.mod`** — a developer-time tool, never a runtime
   import:

   ```
   tool github.com/gopernicus/gopernicus/workshop/gopernicus
   ```

   with the matching `require … v0.4.0`. A host still working against an
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
adopts them by bumping its pin — `go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@<next>`.

## 8. Which examples conform

| host | role |
|---|---|
| `examples/cms` | conforming worked example — the Turso host of `pockets/cms`; its store fills `InquiryMaintenance` |
| `examples/minimal` | conforming worked example — the zero-infra host, no driver in its module graph; its memstore fills `InquiryMaintenance` |
| `examples/jobs-minimal` | conforming worked example — the jobs pocket with host-owned handlers and the jobs-schedule form of retention (a `retention` kind over `PurgeTerminalJobs`); `cmd/` only, conforming as it stands |
| `examples/auth-cms` | conforming to the H floor since the host-contract-reconciliation package moves (issue #90); still the **authentication conformance harness**, not a layout reference — its `cmd/` carries provider behavior the H10 reviewer clause covers; its retention host pocket's runner ticks the maintenance pass, the operation expiry and the inquiry purge |
| `examples/goth-showcase` | conforming to the H floor since its showcase package moved to the host's `ui/showcase`; the `ui/goth` kit showcase, not a layout reference |
| `examples/external-identity` | conforming worked example — the external-only identity host: the authentication pocket's external engine over an in-memory host-pocket outbound, a host `integrations/` dev issuer, no driver, signer, sessions or mail |
| `examples/production-baseline` | conforming worked example — the auditable reference host: the production posture of §3b with zero waivers and one acknowledgement (`MaintenanceRunnerAcknowledged`: it runs retention in process); external OIDC identity, role administration, user erasure and both audit rails on the pgx stores, triggers and the rail's purge function applied, two-role posture documented; `cmd/`, a host `integrations/` dev issuer, a host pocket bridging authorization denials to the audit rail, a host pocket running retention with a disk archive and its `/healthz` detail, `workshop/migrations` |

`examples/minimal` also demonstrates application-data caching at
`GET /catalog.json`: a host service caches a public product projection from CMS
for 30 seconds, with an explicit namespace, bounded Memory and a host error hook.
Run `go run ./cmd/server` from that module, then request
`http://localhost:8081/catalog.json`. Swap its data store for `cacher.Noop{}` to
exercise source loading without caching. Data freshness is TTL-based unless the
host invalidates the logical `published:` prefix after a write; the runnable
HTTP proof covers that invalidation and never includes draft products. The JSON
route sets HTTP no-store independently of the service's data cache.

All seven run under this repo's own `make guard`: G30 (H0–H14) and G32
(P0–P9 under `--posture`); G34 additionally holds `production-baseline`'s
ledger to its stores' migration files.
A green guard over `auth-cms` is the floor, not the clause: its composition
root's provider behavior is named debt (below), not H10 compliance.

**`examples/auth-cms` is a harness, not a model.** It exists to prove the
authentication and authorization surface end to end — OAuth, machine
identity, JWT bearer, security-event audit, ReBAC-decoupled invitations, the
identity/challenge rail, two delivery modes — with zero infrastructure, and
it is the most heavily tested host in this repo. Read it for what the auth
pockets can do and for how their seams are wired together.

**Do not read it for layout.** Its composition root carries provider behavior
(`demo.go`, `purge.go`, `membership.go`, `product.go`, `oauthfake.go`) that
this contract places in a host pocket or an outbound adapter; the ad-hoc
packages it once grew directly under `internal/` now live in its host
pockets' `inbound`/`outbound` (host-contract-reconciliation). The provider
behavior in `cmd/` is a known, dated debt, not a pattern to copy: the shape to copy is §2, and the worked
examples are `cms`, `minimal`, `jobs-minimal`, `external-identity`, and
`production-baseline`.

**Reference findings.** Where this document and the reference host disagree,
the disagreement is recorded here until one side moves:

- **`ui/`** (`ui/{spa,ts}`) — settled by sanction: the host's `ui/` is a
  contract root (H0, H14).
- **`manual/`, `harness/`** — stdlib-only leaves shared by `cmd/gps360` and
  `cmd/mcp`. Not sanctioned as roots: under this contract a stdlib-only leaf
  two binaries share is the host's `sdk/` root's job (`sdk/manual`,
  `sdk/harness`). H0 finding.
- **`server/`** — a committed built binary, not a Go package root; out of
  H0's scope, a `.gitignore` matter for the reference.
- **`pockets/auth/views`** — a host pocket holds only `logic`, `inbound`,
  `outbound` (§2); the blessed slot is `pockets/auth/inbound/views` (§5). Its
  test's `golang.org/x/net/html` import is outside the fence (H12). H0 and
  H12 findings.
- **`internal/inbound/domains/manual`** imports `workshop/testweb` from
  production code. H12 finding.

## Rate-limit policy example

The minimal host includes a runnable domain example at
[`ratelimit_example_test.go`](minimal/internal/logic/domains/catalog/ratelimit_example_test.go).
It resolves host-owned account budgets before calling the SDK's Allower port,
propagates lookup failures without consuming quota, and uses the same resolved
policy with Acquire for worker work. Run it from `examples/minimal` with
`go test ./internal/logic/domains/catalog -run 'Example_hostRateLimitPolicy|TestPolicyFailure' -v`.
The example has no account-schema or tier policy in the SDK and adds no route to
the running CMS host.
