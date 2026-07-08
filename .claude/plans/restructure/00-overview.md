# gopernicus restructure — overview & constitution

Status: **RATIFIED by jrazmi 2026-07-02** — all decisions D1–D9 resolved (see log);
phases are ready for execution in order.
Date: 2026-07-02
Milestone: `restructure` (design ratification + hardening; **codegen explicitly out of scope** — see phase 5 brief)

## Verdict this milestone encodes

The multi-module architecture in this repo is **correct and demonstrated** — it fixes
the original gopernicus's outbound-adapter ambiguity structurally (module boundaries,
not convention), achieves per-dependency module hygiene, and proves feature
pluggability with two hosts (`examples/cms` on Turso, `examples/minimal` on an
in-memory store with zero libsql in its module graph). We are **ratifying and
hardening this design, not rethinking it.** The debt is: (1) the written record lags
the code by two restructurings, (2) the automated gate covers 3 of 6 modules, (3) the
kernel has untested packages and dead stubs, (4) the feature contract works but is
undocumented and has unratified edges, (5) there is no map from the original's
capabilities (auth, jobs, events, telemetry, integrations, codegen) into the new
structure.

## The constitution (the rules every phase preserves)

These are the invariants. Any executor change that would violate one is a bug in the
plan — stop and flag instead of proceeding.

1. **`sdk` imports only the standard library.** Enforced structurally: `sdk/go.mod`
   has no `require` block. Third-party types cross into sdk only via structural
   typing seams (e.g. `sdk/web.Renderer` satisfied by `templ.Component`;
   `sdk/feature.RouteRegistrar` satisfied by `web.WebHandler`).
2. **One external dependency ⇒ its own module.** `integrations/<category>/<tech>`
   wraps exactly one third-party library. A stdlib-only implementation of an sdk
   port ships *inside* sdk as a default (`cacher.Memory`, `filestorage.Disk`,
   `email.SMTP`/`Console`).
3. **Ports live with their consumer, never their implementor.** Facility ports in
   `sdk/<concern>`; domain ports in the feature/app package that calls them.
   (This deliberately inverts the original repo, where `infrastructure/` owned
   ports like `oauth.Provider` and `cache.Cacher` that core consumed.)
4. **A feature is: a datastore-free core module + store-adapter modules.**
   `features/<name>` (ports + entities public, services + HTTP internal) never
   imports `integrations/`, `examples/`, or its own `stores/`. Each
   `features/<name>/stores/<dialect>` is its own module owning that dialect's SQL
   and canonical migrations.
5. **No init() registration, no service locator.** All wiring is explicit calls in
   a host's `main`. A feature's needs are data: a `Repositories` struct the host
   fills, a `Config` struct for overrides, a narrow `feature.Mount`
   (Router / Migrations / Logger) for registration.
6. **Features never import other features.** Cross-feature needs are ports declared
   by the consuming feature; the host wires an implementation (which may be backed
   by another feature's service). (Ratified here; exercised by the auth feature in
   phase 4's design sketch.)
7. **Naming:** ports are behavior-named (`Storer`, `Sender`), never `Port`;
   services are domain nouns; adapters are named for the technology. `auth` things
   are always authentication/authorization/authenticator/authorizer, never
   authz/authn.
8. **Dependencies point inward.** Within an app: `cmd` → everything;
   `internal/inbound` and `internal/outbound` → `internal/logic` → `sdk`. Within the
   ecosystem: examples → features/integrations → sdk. Never the reverse.

## Decision log

| # | Decision | Status | Rationale / default |
|---|---|---|---|
| D1 | Keep the sdk / integrations / features / examples architecture as-is | **RATIFIED 2026-07-02** | All 6 modules green; dependency hygiene and pluggability both *demonstrated*, not asserted |
| D2 | Delivery model: **hybrid** — framework + features are imported, versioned modules; host glue and app-local domains get scaffolded by a future workshop v2 | **RATIFIED 2026-07-02** | The original's generate-into-core model caused the adapter-placement mess; the import model is proven here. `ExportMigrations` is already scaffold-shaped |
| D3 | Pluggability dial: explicit `Repositories` struct + narrow `Mount` registration (current position) | **RATIFIED 2026-07-02** | Django-app mount feel without magic; both hosts exercise it end-to-end |
| D4 | Migrations: scaffold-and-own (`ExportMigrations` → host's tree, shared ledger keyed `(source, version)`) is the recommended path; collect-and-apply registrar stays as turnkey convenience | **RATIFIED 2026-07-02** | Keeps transaction/order control with the host; namespaced ledger prevents cross-feature collisions |
| D5 | Name of the app hexagon | **RATIFIED 2026-07-02, AMENDED same day: `internal/logic`** (history: `internal/sol` → briefly `internal/core` → `internal/logic`) | jrazmi's calls: "Sol" collided with an OpenAI model name; then `logic` chosen over `core` to align with the settled convention in `/Users/jrazmi/code/gps/gps-360` (`src/internal/{inbound,logic,outbound}`, with `logic/{domains,compositions}`) — one convention across the ecosystem, and `logic` avoids any echo of the original repo's flawed `core/` layer. Cost was zero both times: the hexagon exists in no code in this repo, only docs |
| D6 | `sdk/ratelimiter`: currently designed but dormant (zero usage, no default impl) | **RATIFIED 2026-07-02: keep + add `Memory` default** (phase 2 W5) | Rate limiting is genuinely foreseen (original wired it into auth middleware; auth is the next feature) |
| D7 | `features/cms` core carries templ/goldmark/bluemonday (view deps) | **RATIFIED 2026-07-02: accept for now** → **SUPERSEDED 2026-07-07 by feature-standard FS1** (`.claude/plans/feature-standard/00-charter.md`) via this row's own revisit clause — the headless host materialized. Feature cores now require sdk only; view deps move to `views/<pkg>` sibling modules (convergence B1 pulls goldmark/bluemonday, B2 extracts templ) | "Datastore-free" is the hygiene rule that matters; a headless host is hypothetical. Revisit if one materializes |
| D8 | Module path rename `gopernicus/*` → real `github.com/...` path | **RATIFIED 2026-07-02: deferred** — do at first tagged release, not during restructure | Touches every go.mod/import for zero design signal; required before tags, pointless before them |
| D9 | Remove `sdk/cacher` tracer stub (`Cache.tracer` field + `WithTracer` no-op) | **RATIFIED 2026-07-02** | Dead stub; OTEL story returns deliberately via the phase-4 capability map, not via a vestigial field |

### Phase 3 contract decisions (C1–C4)

| # | Decision | Status | Rationale / default |
|---|---|---|---|
| C1 | Route namespacing: (a) every feature documents its claimed route namespace; (b) `feature.PrefixRegistrar` (`sdk/feature/prefix.go`) wraps a `RouteRegistrar` to relocate a feature's routes under a host-chosen prefix | **RATIFIED 2026-07-02** | Unit-tested (`sdk/feature/prefix_test.go`); real-interaction-verified against `examples/minimal` + `features/cms`: prefixed routes serve correctly (200s), but cms's own views hardcode absolute (un-prefixed) links, so in-page navigation breaks under a non-root prefix. Documented as a known limitation in `features/README.md` §4, not half-fixed — fixing it is real scope (base-path threading through every view), out of this phase |
| C2 | Cross-feature dependencies: features never import features (constitution rule 6); the consuming feature declares a narrow port in its own public package, the host wires an implementing feature's service | **RATIFIED 2026-07-02** | Documented with a worked (illustrative, code-free) example in `features/README.md` §5, keyed to the future `auth` feature (phase 4). Corollary: only genuinely shared vocabulary (not a feature's domain port) may graduate into `sdk`, per `sdk/README.md`'s admission policy |
| C3 | `feature.Mount` evolution: grows only by adding narrow, single-purpose ports (the `Router`/`Migrations`/`Logger` pattern); never a service locator or a field carrying a concrete type; pre-v1, a new named field is a compatible change | **RATIFIED 2026-07-02** | Documented in `features/README.md` §6 with two named-but-not-built candidates (a jobs registrar, an event bus port) — built only when a real feature needs one |
| C4 | Release & versioning: nested-module semver tags (`sdk/vX.Y.Z`, `features/cms/vX.Y.Z`, …); `go.work` and inter-module `replace` directives are workspace-dev-only and must be dropped/pinned before tagging; D8's module-path rename executes at first tag | **RATIFIED 2026-07-02** | Written procedure only in `RELEASING.md` (repo root); no tags cut this phase |

## Phases

Execute in order; each phase file is self-contained for a mid-tier executor model.

| Phase | File | What | Type |
|---|---|---|---|
| 1 | `01-truth-and-guards.md` | Make the written record and the automated gate match the real 6-module tree | Code + docs |
| 2 | `02-kernel-hardening.md` | Tests for untested sdk packages, conformance suites, D6/D9 execution | Code |
| 3 | `03-feature-contract.md` | Ratify + document the feature contract (features/README charter, route namespacing, versioning story) | Docs + small code |
| 4 | `04-capability-map.md` | Classify every original-gopernicus capability into sdk/integration/feature/workshop/drop; auth feature design sketch | Analysis + docs |
| 5 | `05-workshop-v2-brief.md` | Scope brief for future codegen milestone (NOT an implementation plan) | Docs |

Phases 1→2 are strictly ordered (2's `make check` relies on 1's fixed gate).
Phase 3 depends on 1 (docs it extends). Phase 4 depends on 3 (contract it maps onto).
Phase 5 depends on 4.

## Loop protocol (for /loop or manual execution)

Each loop leg = one phase. The executor must:

1. Read this file **and** the phase file completely before touching anything.
2. Verify preconditions listed in the phase file (they fail silently otherwise).
3. Do the work items in order. Surgical diffs only — every changed line traces to a
   work item. Do not refactor adjacent code.
4. Run the phase's **Acceptance** commands; all must pass.
5. End with the **real-interaction check** (below) — green tests alone never close a
   phase.
6. Append a dated entry to the phase file's `## Execution log` section: what was
   done, what passed, anything skipped or flagged.
7. Stop. Do not start the next phase in the same leg — jrazmi ratifies between
   phases.

### Real-interaction check (every phase ends with this)

`examples/minimal` needs zero external infrastructure, so it is the standing
end-to-end proof:

```sh
cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus
for m in sdk integrations/datastores/turso features/cms features/cms/stores/turso examples/cms examples/minimal; do
  (cd $m && go build ./... && go vet ./... && go test ./...) || exit 1
done
cd examples/minimal && go run ./cmd/server &   # read cmd/server/main.go first for HOST/PORT defaults (minimal defaults to localhost:8081; examples/cms owns 8080)
# then, against the running server:
curl -fsS http://localhost:8081/            # public home — expect 200 + HTML
curl -fsS http://localhost:8081/products/widget-3000     # a seeded custom-type public page (verify the slug in main.go's seed data)
# kill the server; report exact URLs hit and status codes in the execution log
```

If the server does not boot or a route 500s, the phase is NOT done regardless of
test results.

## Verification baseline (as of 2026-07-02, pre-phase-1)

All 6 modules `go build` / `go vet` / `go test` clean. `make check` passes but only
covers `sdk`, `integrations/datastores/turso`, `examples/cms` and its layering guard
greps a path that no longer exists — trust the per-module loop above, not `make
check`, until phase 1 lands.
