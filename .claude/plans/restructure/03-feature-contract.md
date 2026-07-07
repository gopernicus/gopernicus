# Phase 3 — the feature contract, ratified and written down

Status: READY — ratified 2026-07-02
Depends on: 01-truth-and-guards.md (docs baseline). Runs fine before/after phase 2.

## Goal

The feature system works (two hosts prove it) but its contract exists only as code.
After this phase: a `features/README.md` charter defines what a feature IS, the
open contract edges (route namespacing, cross-feature dependencies, Mount
evolution, release/versioning) have ratified answers, and a feature-authoring
checklist exists so the *next* feature (auth, phase 4+) has rails.

## Context an executor needs (verified 2026-07-02)

- The contract as implemented: `sdk/feature/feature.go` defines `RouteRegistrar`
  (Handle(method, path, handler, ...middleware)), `MigrationSource{Name, FS, Dir}`,
  `MigrationRegistrar` (collect-only — cannot trigger apply), and
  `Mount{Router, Migrations, Logger}`. No service locator, no init() anywhere.
- A feature's public surface today (`features/cms/cms.go`): a `Repositories`
  struct of the five domain ports the host fills, a `Config` struct (Views, Types,
  Templates, Cache, Blobs, Mailer, addresses), and
  `Register(m feature.Mount, repos Repositories, cfg Config) error`.
- Route generation is registry-driven (`features/cms/internal/http/router.go`
  iterates `Registry.Types()` and emits admin CRUD + public routes per routable
  type) — adding a content type adds routes with zero route code.
- Migrations: `features/cms/stores/turso.ExportMigrations(dst)` copies canonical
  SQL into the host's tree (host owns + applies pre-boot, e.g.
  `examples/cms/workshop/migrations`); shared ledger keyed `(source, version)`.
  A collect-and-apply Registrar path also exists for turnkey hosts. D4 ratified
  scaffold-and-own as the recommended model.
- `features/cms/theme` exposes `PublicViews` (Home/Archive/Single/Error →
  `web.Renderer`) + `Default()`; hosts override site chrome via `Config.Views`.
- Routes are registered on the host's mux with absolute paths — nothing today
  prevents two features from colliding on a path, and there is no prefixing
  mechanism (contract gap C1 below).
- Nothing today says how feature A uses feature B (contract gap C2). Constitution
  rule 6 (00-overview.md) pre-ratifies the answer at the principle level:
  features never import features; cross-feature needs are ports the consuming
  feature declares, wired by the host.
- Module paths are provisional (`gopernicus/...`); go.work is dev-only; the store
  module's `replace` directives are relative paths (`../../../../sdk`). There is
  no documented release/tagging procedure (contract gap C4).

## Preconditions

1. Phase 1 merged (ARCHITECTURE.md already has the short Features section this
   phase deepens; `make check` covers 6 modules).
2. Decision statuses D3/D4 in 00-overview.md still stand.

## Work items

### W1 — resolve the contract gaps (write decisions into 00-overview's log, then implement/document)

**C1 — route namespacing.** Ratify: (a) every feature documents its route surface
and claims a conventional namespace (cms: admin routes under the type's
`AdminBase()`, public routes per routable type); (b) add a small
`web.PrefixRegistrar` (or `feature.PrefixRegistrar` — put it where `RouteRegistrar`
lives so it needs no new imports; read both packages and pick the one that avoids
an import in the wrong direction) that wraps a `RouteRegistrar` and prefixes every
path, so a host CAN mount a feature under `/x/` without feature cooperation.
Implement it + a unit test (fake registrar records paths). Document: hosts resolve
collisions; features must not assume they own `/`.
**Recommended default:** helper + convention as above. **Flag YOUR CALL** only if
implementation reveals the cms public routes break under prefixing (e.g. hardcoded
absolute links in views) — in that case document the limitation honestly instead
of half-fixing it, and log it as a known issue for the next milestone.

**C2 — cross-feature dependencies.** Document constitution rule 6 concretely with
the canonical worked example (no code yet — auth doesn't exist): if `cms` needs
"who is the current user", cms declares a narrow port (e.g. an
identity-from-context func or a `CurrentUser(ctx)` interface) in its own public
package; the auth feature's service satisfies it; the HOST wires them in `main`.
Neither feature imports the other. Also state the corollary: shared *vocabulary*
that multiple features need (identity-in-context, error sentinels) is the only
thing that may graduate into `sdk` — per sdk/README's admission policy.

**C3 — Mount evolution policy.** Ratify and document: `Mount` grows only by adding
narrow, single-purpose ports (the Router/Migrations/Logger pattern); it never
becomes a service locator or carries concrete types; pre-v1, adding a field is a
compatible change because hosts construct `Mount` themselves with named fields.
Candidate future fields (jobs registrar, event bus) are named as *candidates* in
the charter, added only when a real feature needs them — not speculatively.

**C4 — release & versioning story.** Document (in the charter + a short
`RELEASING.md` at repo root): nested-module tagging scheme (`sdk/vX.Y.Z`,
`integrations/datastores/turso/vX.Y.Z`, `features/cms/vX.Y.Z`,
`features/cms/stores/turso/vX.Y.Z`); `replace` directives are workspace-dev-only
and must be dropped/require-pinned at tag time; D8 (real module path) executes at
first tag. No tags are cut in this phase — this is the written procedure only.

### W2 — features/README.md: the charter

Write the definitive "what is a gopernicus feature" doc (~150 lines):

1. **Definition + the dial** (D3): explicit deps as data (`Repositories`,
   `Config`) + narrow registration (`Mount`). One paragraph on why not
   init()/service-locator (mirrors Go idiom: composition roots, structural
   typing).
2. **Anatomy** (mirror cms, generalized): module layout table — root `<name>.go`
   (Repositories/Config/Register), public domain packages (entities + ports),
   `internal/<domain>svc` services, `internal/http` (handlers/views),
   `stores/<dialect>` adapter modules (SQL + canonical migrations +
   ExportMigrations), optional `theme`/views seam.
3. **The rules**: datastore-free core (guard G2 enforces it); ports public,
   services internal; no feature→feature imports (C2); migrations namespaced by
   feature name in the shared ledger (D4); route surface documented + prefixable
   (C1); every feature must be provable with a zero-infra host (the
   examples/minimal pattern).
4. **Authoring checklist** (the rails for the auth feature): a literal checkbox
   list an executor can walk — module compiles standalone; `go.mod` has no
   datastore driver; conformance to `sdk/feature` interfaces asserted at compile
   time; migration namespace unique; minimal-host proof exists; README documents
   route surface + config + ports.

### W3 — sync the other docs

- ARCHITECTURE.md: link the charter from its Features section; fold in C1–C4
  outcomes in one sentence each (charter holds the detail).
- `sdk/README.md`: `feature` package row links the charter.
- 00-overview.md decision log: record C1–C4 outcomes with dates.

### W4 — fresh-eyes cross-check

Same protocol as phase 1 W8: a clean-context reader takes ONLY
features/README.md + ARCHITECTURE.md and verifies every claim against the tree
(paths, type names, guard behavior, the PrefixRegistrar behavior). Zero
contradictions.

## Acceptance

```sh
make check                                    # green, all modules
go test ./sdk/... (or wherever PrefixRegistrar landed)   # includes its test
test -f features/README.md && test -f RELEASING.md
```

Charter checklist is concrete enough that phase 4's auth sketch can cite items by
number.

## Real-interaction check

Standing check from 00-overview.md. Additionally, if C1's PrefixRegistrar landed:
in a scratch copy of `examples/minimal/cmd/server/main.go` (do not commit), mount
the cms feature under a prefix and confirm the admin list page serves under the
prefixed path — then discard the scratch change. Record what worked and any
absolute-link breakage observed (feeds the C1 YOUR CALL if triggered).

## Out of scope

- Building the auth feature (phase 4 sketches it; a later milestone builds it).
- Cutting any release tags (C4 documents the procedure only).
- New Mount fields (C3 names candidates only).

## Execution log

### 2026-07-02 — phase 3 executed

**Preconditions.** Phase 1 + 2 execution logs confirmed complete; ARCHITECTURE.md
has the Features section this phase deepens; `make check` covers all 6 modules +
4 guards. D3/D4 statuses in 00-overview.md unchanged (RATIFIED 2026-07-02). Repo
is not a git repository — **no commits possible**; all changes are working-tree
only. Baseline: all 6 modules build/vet/test clean before starting.

**W1 — contract gaps C1–C4 resolved.** All four recorded in 00-overview.md as an
appended "Phase 3 contract decisions (C1–C4)" subsection directly below the
D1–D9 table (existing rows untouched).

- **C1 (route namespacing):** implemented `feature.PrefixRegistrar` in
  `sdk/feature/prefix.go` (+ `prefix_test.go`, 8 table cases + a method
  pass-through test + a compile-time `RouteRegistrar` satisfaction assertion).
  Placed in `sdk/feature` (where `RouteRegistrar` lives) rather than `sdk/web`:
  it wraps and produces a `RouteRegistrar`, so putting it next to the interface
  needs zero new imports in either direction (`feature` already imports `web`
  for the `Middleware` type; `web` stays ignorant of `feature`). Handles: prefix
  with/without trailing slash, missing leading slash, `""`/`"/"` as deliberate
  no-op, Go 1.22+ `"{$}"` exact-match patterns (verified against the actual
  patterns `features/cms/internal/http/router.go` registers), wildcard segments,
  nested prefixes. Stdlib-only — guard G1 green. Convention + "hosts resolve
  collisions; features must not assume they own /" documented in
  `features/README.md` §4. **The C1 YOUR CALL triggered** — see the
  real-interaction section below: cms views hardcode absolute links, so the
  limitation is documented honestly in the charter (§4 known-limitation note)
  and in the C1 decision row; no half-fix attempted.
- **C2 (cross-feature deps):** constitution rule 6 documented concretely in
  `features/README.md` §5 with the illustrative (code-free) `CurrentUser(ctx)`
  worked example — cms declares the port, future auth's service satisfies it
  structurally, only the host imports both. Corollary on sdk graduation
  (vocabulary only, per sdk/README's admission policy) included.
- **C3 (Mount evolution):** documented in `features/README.md` §6 — narrow
  single-purpose ports only, never a service locator or concrete types; pre-v1
  a new named field is a compatible change (hosts use named struct fields).
  Candidates named (jobs registrar, event bus port) as candidates only.
- **C4 (release/versioning):** `RELEASING.md` created at repo root —
  nested-module tag scheme (`sdk/vX.Y.Z`, `features/cms/vX.Y.Z`,
  `features/cms/stores/turso/vX.Y.Z`, …), `go.work` + relative `replace`
  directives are workspace-dev-only and must be dropped/require-pinned at tag
  time, D8 rename executes at first tag. No tags cut. Summarized in
  `features/README.md` §7.

**W2 — features/README.md charter.** Created (232 lines): definition + the dial
(D3, why no init()/service-locator), anatomy table generalized from cms, the
rules (datastore-free core / G2; ports public, services internal; no
feature→feature imports; namespaced migrations / D4; documented + prefixable
route surface / C1; zero-infra provability), C1–C4 sections, and a 9-item
numbered authoring checklist phase 4's auth sketch can cite by item number.

**W3 — doc sync.** ARCHITECTURE.md Features section: added a "full contract,
ratified" paragraph linking the charter + RELEASING.md with one-sentence C1–C4
outcomes. `sdk/README.md`: `feature` package row now lists `PrefixRegistrar`
and links the charter. 00-overview.md: C1–C4 rows appended (see W1). No go.mod
touched anywhere (PrefixRegistrar is stdlib + sdk-internal imports only), so no
tidy needed.

**W4 — fresh-eyes cross-check: PASS, zero contradictions.** Clean-context
subagent given only features/README.md + ARCHITECTURE.md verified every
checkable claim against the tree: all paths/types/signatures exact; guard G2's
grep matches the claimed enforcement; router.go's route table matches the
documented namespace convention; prefix.go's behavior matches the doc claims
case-for-case; the hardcoded-absolute-link limitation confirmed in source
(`views/error.templ` `href="/articles"`, layout/public/menus/terms/contact
templates, `web.RespondRedirect` absolute paths in handlers, minimal's menu
seed `"/"`/`"/about"`); RELEASING.md and the admission-policy paraphrase match.
Only non-verifiable item: the manual `GET /demo-prefix/articles → 200` claim,
which is by design a real-interaction result, not an automated test.

**Acceptance: all green.**
```
make check                     # PASS — templ no-drift, 6 modules vet+build+test, 4 guards
(cd sdk && go test ./feature/...)   # PASS — includes 6 PrefixRegistrar/Mount tests
test -f features/README.md && test -f RELEASING.md   # both exist
```

**Real-interaction check, part (a) — standing check: PASS.**
```
cd examples/minimal && go run ./cmd/server    # localhost:8081 (defaults from main.go)
GET http://localhost:8081/                        -> 200 (<title>Home</title>)
GET http://localhost:8081/products/widget-3000    -> 200 (<title>Widget 3000</title>)
```
Server killed; `lsof -i :8081` empty (port free).

**Real-interaction check, part (b) — scratch prefix-mount experiment.**
`examples/minimal/cmd/server/main.go` sha256 taken before the edit
(`c59655f0…a48a52`); a temporary one-line change wrapped the router:
`feature.Mount{Router: feature.PrefixRegistrar{Prefix: "/demo-prefix", Next: router}, Logger: log}`.
Results against the booted scratch server:
```
GET /                                   -> 404   (root correctly relinquished)
GET /demo-prefix/                       -> 200   (home under prefix — "{$}" handled)
GET /demo-prefix                        -> 307   (ServeMux redirect to /demo-prefix/)
GET /demo-prefix/articles               -> 200   (admin list, seeded articles render)
GET /demo-prefix/products/widget-3000   -> 200   (public custom-type page)
GET /articles/new                       -> 404   (the admin page's own link target — BREAKS)
GET /demo-prefix/articles/new           -> 200   (the correctly prefixed equivalent)
```
**Route registration under a prefix works end-to-end; in-page links break**, as
the plan predicted: the served admin HTML contains `href="/"` and
`href="/articles/new"` — absolute, un-prefixed — so following any cms-rendered
link escapes the mount and 404s. This is the C1 YOUR CALL condition: per the
plan's instruction, documented as a known limitation in `features/README.md` §4
and the C1 decision row (fix = base-path/URL-builder threading through every
view; logged for a future milestone, not half-fixed here). Server killed, port
8081 confirmed free. **main.go restored from the pre-edit copy; post-restore
sha256 `c59655f088d38c70bc9c47d52168cf61f12ce911fe3072fbec2cf190a2a48a52`
matches the pre-edit checksum exactly (byte-identical), and
`examples/minimal` rebuilds clean.**

**Out-of-scope confirmed untouched:** no auth feature code, no tags cut, no new
Mount fields (candidates named only), and the three OPEN phase-2 production
findings (email Console nil-logger panic; memstore term/menu uniqueness) left
exactly as they were, awaiting jrazmi's ruling.

**Not started:** phase 4 (per protocol — stop after one phase, jrazmi ratifies
next).

**2026-07-02, orchestrator addendum (post-executor):** independently re-verified:
all four new artifacts present, `go test -count=1 ./feature/...` green,
`examples/minimal` rebuilds clean (main.go restore held), full `make check` green.
The C1 prefix-navigation limitation (cms views hardcode absolute links) is recorded
in the C1 decision row and charter §4; the base-path/URL-builder fix is
future-milestone scope. Phase-2's three OPEN production findings remain untouched,
still awaiting jrazmi's ruling.
