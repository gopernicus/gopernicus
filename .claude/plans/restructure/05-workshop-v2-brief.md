# Phase 5 — workshop v2 brief (scope document ONLY — not an implementation plan)

Status: READY — ratified 2026-07-02
Depends on: 04-capability-map.md

## Goal

One document — `.claude/plans/restructure/workshop-v2-brief.md` — that scopes the
future codegen milestone so it can be planned properly later. Per the standing
rule (and this milestone's charter): **codegen follows design**; nothing in this
phase writes a generator.

## Why a brief and not a plan

The original's generator caused its architecture flaw: it generated driven
adapters (`userspgx/`), cache decorators, and even composition-root wiring
(`generated_composite.go`) *into `core/`*, because generation targets were an
emergent property of "where the generator happened to put files" rather than a
designed placement. Workshop v2 must instead generate INTO the ratified structure
(phases 1–4). Until that structure has survived the auth feature build, a codegen
plan would be speculative.

## What the brief must contain

### 1. Generation targets in the new world (each maps to a ratified home)

- **Host scaffold** (`gopernicus init`): cmd/server composition root wiring chosen
  features + integrations, Makefile with guards (phase 1's four), workshop/
  migrations runner, .env.example. Replaces the original's `app/` template
  emission.
- **App-local domain scaffold** (`gopernicus new domain`): `internal/core/domains/
  <domain>` (entity + service + port) + `internal/outbound` store skeleton — the
  ARCHITECTURE.md app pattern (hexagon named `internal/core` per D5), never inside
  a feature.
- **Feature skeleton** (`gopernicus new feature`): the charter anatomy
  (features/README.md) as a compilable skeleton incl. `stores/<dialect>` module
  and the minimal-host proof harness.
- **Store adapter emission**: from an entity spec, emit the `stores/<dialect>`
  implementation of a domain's ports — INTO the store module, never the core.
  Candidate engine: the original's `infrastructure/database/crud`
  Spec/Store/Dialect design (already proven against postgres+sqlite;
  `specstore.go` shows spec-from-queries.sql extraction works). Decide there:
  runtime generic engine vs fully-emitted code vs hybrid.
- **Migrations tooling**: `db migrate/status/create` + reflect, aligned with the
  scaffold-and-own ledger model (D4).

### 2. What carries over from the original (by reference, with paths)

- `queries.sql` annotation language (`@func/@filter/@order/@search/@cache/@event`)
  and the parser (`workshop/codegen/generators/parse.go`, `resolve.go`).
- The bootstrap-vs-generated split + `// gopernicus:start|end` surgical markers.
- Determinism + drift-as-CI-failure.
- The crud Spec/Dialect engine and its sqlite fixtures.
- Conformance suites as generated-adapter acceptance tests.

### 3. What explicitly dies

- Generating driven adapters, cache decorators, or composite wiring into the
  domain core.
- Ports owned by the implementor's layer.
- The single-module dependency tax (generation must respect module boundaries —
  a generated store lands in its own module with its own go.mod).

### 4. Open questions for the future milestone (list, don't answer)

Bridge/handler generation vs the registry-driven-routes pattern (cms needed no
route generation — does anything?); TS/OpenAPI client generation placement;
`bridge.yml`-style HTTP config vs code; how generated feature stores version
against their feature core; whether `doctor` returns.

## Acceptance

Brief exists, covers all four sections, every carried-over item cites an original
path, every dead item names its replacement. `make check` green (no code
touched).

## Real-interaction check

Standing check from 00-overview.md.

## Execution log

### 2026-07-02 — phase 5 executed (inherited draft finished)

A 207-line draft of `workshop-v2-brief.md` was inherited from a mid-tier
executor stopped just before completion. Reviewed against this phase file,
the constitution, `capability-map.md`, `auth-feature-design.md`, and
`features/README.md`; every cited original-repo path re-verified read-only
against `gopernicus-original`. Kept: the overall four-section structure, all
of §1's target→home mappings, all of §3, and most of §4. Changed:

- **Corrected a false verification claim in §2**: the draft stated the
  determinism/drift discipline was enforced by "each template paired with a
  determinism-checking test (60+ files)". Verified false — 19 `*_tmpl.go`
  files, 25 test files, with the two most load-bearing templates
  (`repository_tmpl.go`, `pgxstore_tmpl.go`) untested. The real enforcement
  is `.github/workflows/ci.yml`'s "Verify no drift" step (`git diff --cached
  --exit-code` after regeneration); the brief now cites that and flags the
  coverage gap for the future milestone's test budgeting.
- **Corrected §2's annotation-source count** ("four sibling `queries.sql`
  files" → ten under `core/repositories/auth/`) and recorded the verified
  full annotation vocabulary (`@fields`/`@max`/`@returns`/`@type`/`@check`/
  `@scan`/`@fixture` beyond the phase file's six examples).
- **Corrected the `sqlguard` path** in §4's `doctor` question:
  `workshop/codegen/sqlguard/sqlguard.go`, not
  `workshop/codegen/generators/sqlguard.go`. NOTE for jrazmi:
  `capability-map.md`'s workshop-tooling row cites the wrong
  (`generators/`) path — flagged here, not edited there (out of this
  phase's file scope).
- **Added three open questions** the draft missed, each a genuine
  cross-module consequence of D2 that nothing ratified answers: (1) where
  the annotated `queries.sql` spec lives when one spec drives artifacts in
  two modules with a hard import boundary (plus the generation-time
  live-database dependency of schema reflection); (2) which generation
  surfaces still need the `// gopernicus:start|end` markers under D2's
  scaffold-once vs. regenerate-forever split; (3) where drift-as-CI-failure
  runs in a multi-module (`go.work`) and multi-repo (scaffolded host apps)
  world, and whether generated output must pass the four layering guards.
- **Added an explicit preconditions list** ("before the codegen milestone
  can even be planned") distilling W4 items 1–5 + the YOUR CALL rows that
  gate generation scope, per this phase's dependency on
  `auth-feature-design.md`'s build order.
- **Smaller**: cited `ExportMigrations`
  (`features/cms/stores/turso/turso.go`) as the existing scaffold seam D2's
  rationale names; made the postgres reflector path precise
  (`workshop/codegen/database/postgres/pgx/{reflector,migrator}.go`);
  replaced the draft's trailing Acceptance/Real-interaction/Execution-log
  placeholder sections (which belonged to this phase file, not the
  deliverable) with a short self-check note; status DRAFT → FINAL. Final
  length 256 lines; still a scope brief — no generator design, no code.

**Acceptance**: `make check` at repo root — all 6 modules build/vet/test
green, all 4 guards pass ("all checks passed").

**Real-interaction check**: `examples/minimal` booted via `go run
./cmd/server` (localhost:8081). `GET http://localhost:8081/` → 200,
`<!doctype html>...<title>Home</title>`. `GET
http://localhost:8081/products/widget-3000` → 200,
`<title>Widget 3000</title>` (seed slug verified in `main.go` first).
Server killed; port 8081 confirmed free (lsof empty, connection refused).

Files touched this phase: `workshop-v2-brief.md` and this file only. No
code, no other docs. Phase 5 complete — milestone `restructure` fully
executed pending jrazmi's ratification.
