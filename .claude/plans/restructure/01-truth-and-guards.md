# Phase 1 — truth & guards: make the record and the gate match the tree

Status: READY — ratified 2026-07-02
Depends on: 00-overview.md (read it first — the constitution and loop protocol apply)

## Goal

The code is two restructurings ahead of its own documentation and tooling. After this
phase: every top-level doc describes the actual 6-module tree, `make check` builds /
vets / tests **all six** modules, the layering guards enforce the *current*
boundaries (and demonstrably fail on violations), and no doc comment points at a
directory that no longer exists.

## Context an executor needs (verified 2026-07-02)

- The repo is a `go.work` workspace of **6 modules**: `sdk`,
  `integrations/datastores/turso`, `features/cms`, `features/cms/stores/turso`,
  `examples/cms`, `examples/minimal`. All build/vet/test clean.
- `Makefile` has `MODULES = sdk integrations/datastores/turso examples/cms` — it
  silently skips half the repo, including the feature module. Its layering guard
  still greps for `examples/cms/internal/sol`, a path that no longer exists (the
  hexagon moved into `features/cms/{content,taxonomy,menus,media,messaging,theme}`
  + `features/cms/internal/*`), so that guard passes vacuously.
- `README.md` (repo root) is the **old CMS app readme** — its Layout section
  describes `internal/sol/domains` etc. living inside the app. Wrong twice over.
- `ARCHITECTURE.md` says "**Three modules today**" (~line 20) and never mentions
  `sdk/feature` — the contract that makes features work — nor `examples/minimal`.
  Its lower sections (port-placement table, sdk-vs-sol test, tier rules, Registry
  model, naming) are **good and current** — preserve them.
- `sdk/README.md` opens with "**Sol** is the dependency leaf and shared core of the
  cms" (stale name, stale scope). Its package table omits `feature` and `slug` and
  wrongly claims `cacher`/`filestorage` are "not wired in v0.1" (both are actively
  wired in every example).
- Four files — `features/cms/{media,menus,taxonomy,messaging}/repository.go` —
  carry the doc comment "Implemented in outbound/repositories/turso", a location
  that does not exist in this repo.
- `integrations/datastores/turso/turso.go`'s package doc says "Located in cms for
  now; destined to become its own module... The only thing blocking the module
  split today is the sol/errs import" — it already IS its own module and the
  "blocker" was never one (sdk/errs is a legal inward dependency).
- `NOTES.md` is a decision log ending at the "Post-v0.2 restructure" (sol→sdk
  rename). It predates the extraction of the hexagon into `features/cms` — that
  move has no entry.
- `.claude/plans/` was empty until this milestone; the plans NOTES.md references
  (`v0.1-cms.md`, `v0.2-cms.md`) are not in the repo. Do NOT try to recreate them —
  just stop referencing them as if a reader could open them.

## Preconditions (verify before starting)

1. `git status` — if this becomes a git repo by execution time, ensure a clean
   tree; if still not a repo, note that in the execution log (no commits possible).
2. All 6 modules build/test clean (baseline loop from 00-overview). If not, STOP
   and flag — this phase must not paper over a broken baseline.
3. `grep -n "MODULES" Makefile` still shows the stale 3-module list. If someone
   already fixed it, skip item W1 and log that.

## Work items (in order)

**D5 applies throughout this phase**: the app hexagon is named `internal/core`
(ratified 2026-07-02; formerly `internal/sol`). Every doc this phase writes or
rewrites uses `internal/core/{domains,compositions}`; `sol` appears only in
NOTES.md's historical entries (leave those verbatim) and in explanations of the
rename itself. No code directory exists under either name today — the name takes
effect in docs now and in real code whenever an app/scaffold next creates the
hexagon. Where docs mention it, add one sentence distinguishing this
`internal/core` (pure hexagon, imports only sdk) from the ORIGINAL repo's `core/`
layer (which embedded adapters — the flaw this design fixes).

### W1 — Makefile: cover all six modules

- Read `Makefile` fully first.
- Set `MODULES` to all six: `sdk integrations/datastores/turso features/cms
  features/cms/stores/turso examples/cms examples/minimal`.
- Investigate `templ` generation: find which modules contain `.templ` sources
  (`find . -name '*.templ' -not -path './.git/*'`) and which go.mod carries the
  `tool` directive for `a-h/templ/cmd/templ`. Make `make generate` run templ for
  every module with `.templ` sources, and make `make check` fail on generation
  drift (generate, then `git diff --exit-code` if git exists; otherwise compare
  checksums of `*_templ.go` before/after).

### W2 — Layering guards that enforce the CURRENT boundaries

Replace the stale guard(s) with these four, each a named make target under `guard:`
(all must print nothing / exit 0 on the clean tree):

```sh
# G1: sdk is stdlib-only
grep -rnE '"(github\.com|cloud\.google\.com|golang\.org/x|gopkg\.in)/' --include='*.go' sdk/ && exit 1
# G2: the feature core never imports integrations, examples, or its own stores
grep -rn --include='*.go' -E '"gopernicus/(integrations|examples|features/cms/stores)' features/cms --exclude-dir=stores && exit 1
# G3: sdk never imports outward (features/integrations/examples)
grep -rn --include='*.go' -E '"gopernicus/(features|integrations|examples)' sdk/ && exit 1
# G4: no references to the original framework's module path
grep -rn --include='*.go' 'github.com/gopernicus/gopernicus' . && exit 1
```

(Adjust grep syntax to whatever the Makefile idiom already uses — match existing
style; the invariant, not the exact flags, is what's ratified.)

**Prove each guard can fail**: for each guard, pipe a synthetic violating line
through the same pattern (e.g. `echo '"gopernicus/integrations/x"' | grep -E ...`)
and confirm it matches. Record the four proofs in the execution log. Do NOT insert
violations into real source files.

### W3 — README.md becomes the framework readme; CMS content moves home

- Create/overwrite `examples/cms/README.md` with the CMS-app content currently in
  the root README (layout, env table, make targets, integration-test note,
  layering-guard examples) — **corrected** for the current tree (the hexagon lives
  in `features/cms`, the app is a thin host: `cmd/server`, `internal/theme`,
  `workshop/migrations`).
- Rewrite root `README.md` as the framework readme: what gopernicus is (one
  paragraph), the 6-module map with one line each, the sdk/integration/feature
  rules (link ARCHITECTURE.md), and a quickstart that points at `examples/minimal`
  (zero-infra) and `examples/cms` (Turso). Keep it under ~80 lines.

### W4 — ARCHITECTURE.md: six modules, features first-class

Surgical edits, preserving the good lower sections:

- Fix the repository-layout block and "Three modules today" to list all six
  modules with the features layer as a first-class row.
- Add a **"Features"** section (place after "The framework" section) covering:
  the `sdk/feature` contract (quote the `Mount` / `RouteRegistrar` /
  `MigrationRegistrar` types from `sdk/feature/feature.go`), the feature anatomy
  in one paragraph (datastore-free core module; ports + entities public; services
  + HTTP internal; `Register(mount, repos, cfg)`; `stores/<dialect>` adapter
  modules), the migrations scaffold model (D4), and one sentence naming
  `examples/minimal` as the standing proof that a host can adopt a feature with no
  datastore driver in its module graph.
- Update the "Repositories: app-specific vs feature store adapter" paragraph if it
  contradicts anything above (it largely doesn't — verify).
- Do NOT rewrite the port-placement table, the "sdk vs internal/sol — the test"
  section, the tier rules, or the Registry-model section except where they name
  stale paths — but DO apply D5 to them: retitle the test section to
  "sdk vs internal/core — the test" and replace `internal/sol` with
  `internal/core` wherever it names the current pattern.

### W5 — sdk/README.md refresh

- Kill the "Sol ... shared core of the cms" framing; open with: sdk is the
  stdlib-only kernel of the gopernicus framework (empty go.mod = structural
  enforcement).
- Package table: add `feature` (the pluggability contract — one line + pointer to
  ARCHITECTURE.md's Features section), `slug`, `email`. Correct `cacher` and
  `filestorage` to "wired defaults" status. `ratelimiter` stays listed as dormant
  pending D6 (phase 2).
- Keep the naming criteria and admission policy sections (they're current).

### W6 — stale doc comments in code

- In `features/cms/media/repository.go`, `features/cms/menus/repository.go`,
  `features/cms/taxonomy/repository.go`, `features/cms/messaging/repository.go`:
  replace "Implemented in outbound/repositories/turso" with wording like:
  "Implemented by feature store adapters (features/cms/stores/turso) or any
  host-provided implementation (see examples/minimal's memstore)."
- In `integrations/datastores/turso/turso.go`: delete the "Located in cms for
  now... blocking the module split" paragraph; replace with a current one-liner
  (own module; depends only on sdk + libsql).
- Sweep for others: `grep -rn "outbound/repositories" --include='*.go' .` and
  `grep -rn "sol/" --include='*.go' . | grep -v internal/sol` — fix any further
  stale path references found (log each).

### W7 — NOTES.md: record the features extraction

Append a dated section ("2026-07 — features extraction (retro-recorded)") stating:
the hexagon moved from `examples/cms/internal/sol` into `features/cms` (public
domain packages + internal services/http), the store SQL moved to the
`features/cms/stores/turso` module, `sdk/feature` (Mount/RouteRegistrar/
MigrationRegistrar) was introduced, and `examples/minimal` was added as the opt-out
proof. Also record D5: the app-hexagon directory name is now `internal/core` —
the `sol` name is retired ("Sol" collided with an OpenAI model name). Mark the
whole section explicitly as reconstructed after the fact. Also note that the
referenced v0.1/v0.2 plan files are not in this repo.

### W8 — fresh-eyes cross-check

Spawn a fresh subagent (or, if executing solo, re-read with clean eyes) whose ONLY
input is the four docs (README, ARCHITECTURE, sdk/README, examples/cms/README) and
whose task is to verify every checkable claim against the tree (module lists, paths,
wired/dormant status, guard commands). Zero contradictions allowed; fix and re-check
until clean.

## Acceptance

```sh
make check          # must build+vet+test ALL SIX modules and run all four guards
make generate       # must be a no-op on a clean tree (no drift)
```

Plus the four guard-can-fail proofs (W2), plus W8 reporting zero contradictions.

## Real-interaction check

Run the standing check from 00-overview.md (build/test loop + boot
`examples/minimal` + curl `/` and one seeded public entry page, expect 200s).
Record URLs and status codes.

## Out of scope

- Any behavior change to sdk/features/examples code (doc comments only).
- Module path rename (D8 — deferred to first tagged release).
- New tests (phase 2).

## Execution log

### 2026-07-02 — phase 1 executed

**Preconditions.** Repo is not a git repository (confirmed via `git rev-parse
--is-inside-work-tree`) — not a git repo, no commits possible. All 6 modules
built/vetted/tested clean before starting (baseline confirmed). `grep -n
"MODULES" Makefile` showed the stale 3-module list — W1 proceeded as written.

**W1 — Makefile covers all six modules.** `MODULES` now lists all six.
Investigated templ generation: `.templ` sources exist only under
`features/cms/internal/http/views/` (11 files), but the `tool
github.com/a-h/templ/cmd/templ` directive previously lived only in
`examples/cms/go.mod` — a directory with no `.templ` sources of its own, so
the old `generate: cd examples/cms && go tool templ generate` target was
already a silent no-op that happened to work only because nothing needed
regenerating there. Added the `tool` directive to `features/cms/go.mod`
(templ was already a direct `require` there) and repointed `generate` at
`cd features/cms && go tool templ generate`. Verified via before/after
checksums of all 11 `*_templ.go` files that this produces byte-identical
output to the previous (accidentally-inert) target — zero drift. `make check`
now checks drift via `git diff` when `.git` exists, else before/after
checksums of `*_templ.go` (this repo: checksum path, since not a git repo).

**W2 — four layering guards.** Replaced the two stale guards (legacy-import +
`internal/sol`-that-no-longer-exists) with four named targets under
`make guard` (`guard-sdk-stdlib`, `guard-feature-isolation`,
`guard-sdk-no-outward`, `guard-no-legacy-path`), matching G1–G4 from the plan.
`make check` now depends on `make guard`. All four print nothing / exit 0 on
the clean tree. Guard-can-fail proofs (synthetic violating lines piped
through the same patterns, no source files touched):
- G1 (sdk stdlib-only): `echo '"github.com/foo/bar"' | grep -rnE
  '"(github\.com|cloud\.google\.com|golang\.org/x|gopkg\.in)/'` → matched.
- G2 (feature isolation): `echo '"gopernicus/integrations/x"' | grep -nE
  '"gopernicus/(integrations|examples|features/cms/stores)'` → matched.
- G3 (sdk no outward): `echo '"gopernicus/features/cms"' | grep -nE
  '"gopernicus/(features|integrations|examples)'` → matched.
- G4 (no legacy path): `echo 'import "github.com/gopernicus/gopernicus/foo"'
  | grep -n 'github.com/gopernicus/gopernicus'` → matched.

**W3 — README split.** Created `examples/cms/README.md` with the CMS-app
content (layout, env table, make targets, integration-test path corrected to
`features/cms/stores/turso` where the live test actually lives, layering-guard
examples), corrected for the current tree (hexagon in `features/cms`; this app
is `cmd/server` + `internal/theme` + `workshop/migrations`). Rewrote root
`README.md` as the framework readme: 6-module map, the five constitution
rules (condensed), and a quickstart pointing at `examples/minimal`
(zero-infra) and `examples/cms` (Turso). 64 lines, under the ~80-line target.

**W4 — ARCHITECTURE.md.** Fixed the repository-layout block and "Three
modules today" → "Six modules today" with all six listed, features as a
first-class row. Added a **Features** section (after "The framework") quoting
`RouteRegistrar`/`MigrationRegistrar`/`Mount` from `sdk/feature/feature.go`
verbatim, the feature anatomy paragraph, the D4 migrations-scaffold model, and
the `examples/minimal` no-datastore-driver sentence. Applied D5 throughout:
retitled "sdk vs internal/sol — the test" → "sdk vs internal/core — the
test", renamed `internal/sol` → `internal/core` in the one-rule section, the
port-placement table, the tier-rules header/tree, and the Registry-model
paragraph (content only — no other rewriting). Renamed "The app pattern
(hexagonal) — see `examples/cms`" → "…for a host's own app-local domains" and
added a sentence noting no host in this repo currently instantiates it (both
examples are thin hosts around `features/cms`) plus the required one-sentence
distinction from the original repo's adapter-embedding `core/` layer. Did not
touch the "Repositories: app-specific vs feature store adapter" paragraph —
verified it doesn't contradict the new Features section.

**W5 — sdk/README.md.** Replaced the "Sol… shared core of the cms" opening
with the stdlib-only-kernel framing. Package table: added `feature`, `slug`,
`email`; corrected `cacher`/`filestorage` from "not wired in v0.1" to "wired
defaults" (with a fresh-eyes-driven correction: `filestorage.Disk` is used by
`examples/cms` but **not** `examples/minimal`, which leaves blob storage
unset — the table now says so precisely rather than "used by every example").
`ratelimiter` stays listed dormant pending D6. Kept naming-criteria and
admission-policy sections verbatim. Also fixed one more stale path found in
the same file's "Not responsible for" section (`internal/inbound/http` →
`features/cms/internal/http`) — in scope of this phase's "no doc comment
points at a directory that no longer exists" goal even though not itemized
verbatim in W5's bullets.

**W6 — stale doc comments in code.** Fixed the four
`features/cms/{media,menus,taxonomy,messaging}/repository.go` doc comments
(`outbound/repositories/turso` → feature store adapters / host-provided
wording) and `integrations/datastores/turso/turso.go`'s package doc (removed
the "blocking the module split" paragraph — it already is its own module).
Swept `grep -rn "outbound/repositories" --include='*.go' .` (clean after
fixes) and `grep -rn "sol/" --include='*.go' . | grep -v internal/sol`, which
surfaced 11 more doc-comment references to the pre-rename kernel name (`sol` →
`sdk`, e.g. `sol/errs`, `sol/filestorage`, `sol/config`) across
`sdk/{repository,web,logging,email}` and `features/cms/{media,internal/http/views}`
and `integrations/datastores/turso` — fixed all, including correcting one
factually-obsolete detail (`sdk/repository`'s doc said providers write SQL
"against sol/sqldb", a package that no longer exists in any form; corrected to
name the actual current providers). Extended the sweep one step further with
`grep -rnw "sol" --include='*.go' . | grep -v internal/sol` (whole-word, no
trailing slash) since the literal `sol/` pattern missed two prose references
(`sdk/slug/slug.go`, `sdk/web/server.go`) — fixed both for consistency. Full
build/vet/test of all six modules re-confirmed clean after all doc-comment
edits.

**W7 — NOTES.md.** Appended a dated, explicitly-marked-reconstructed "2026-07
— features extraction (retro-recorded)" section: the hexagon's move from
`examples/cms/internal/sol` into `features/cms`, the store SQL's move to
`features/cms/stores/turso`, `sdk/feature`'s introduction, `examples/minimal`
as the opt-out proof, and decision D5 (internal/core, the OpenAI-name
collision rationale, and that it's docs-only pending a future app-local
hexagon). Noted the v0.1/v0.2 plan files are not in this repo. Left all
historical entries above (which say `internal/sol`) verbatim, as instructed.

**W8 — fresh-eyes cross-check.** Spawned a subagent with only the four docs
(README.md, ARCHITECTURE.md, sdk/README.md, examples/cms/README.md) as input,
tasked with verifying every checkable claim against the live tree. It found
one real contradiction: `sdk/README.md`'s package table claimed
`filestorage.Disk` is "used by every example," but `examples/minimal`'s
`cmd/server/main.go` never imports `sdk/filestorage` and leaves
`cms.Config.Blobs` unset. Fixed (see W5 above) and independently re-verified
with `grep -n "filestorage\|Blobs" examples/minimal/cmd/server/main.go
examples/cms/cmd/server/main.go`. No other contradictions found — six-module
list, go.mod dependency shapes, all `make` targets, all four guard commands,
the quoted `sdk/feature` types, directory existence, `.env.example`
cross-check, and internal doc cross-references all checked out clean.

**Acceptance.**
- `make check` — PASS (all six modules build+vet+test clean; drift check via
  checksum fallback found zero drift; all four guards print nothing / exit 0;
  final line `all checks passed`).
- `make generate` — PASS, no-op on the clean tree (verified via before/after
  checksum of all 11 `*_templ.go` files — byte-identical).
- Guard-can-fail proofs — see W2 above, all four confirmed.
- W8 — zero contradictions after one fix (see above).

**Real-interaction check.** Read `examples/minimal/cmd/server/main.go`: its
`serverConfig()` defaults `PORT` to **`8081`**, not `8080` as the standing
check in `00-overview.md` assumes for `examples/minimal` specifically
(`examples/cms` does default to 8080; `examples/minimal` does not) — **flagging
this divergence** rather than forcing the plan's literal port. Read the seed
data in `seed()`: a `product` custom type entry titled "Widget 3000" is
created, and `content.NewEntry` slugifies the title via `slug.Make` →
`widget-3000`; the type's `PublicBase()` resolves to `slug.Make("Products")` =
`"products"`, so the seeded public URL is `/products/widget-3000`.

Booted `cd examples/minimal && go run ./cmd/server` in the background (logged
`server starting address=localhost:8081`), then:
- `curl http://localhost:8081/` → **200**, real HTML (`<title>Home</title>`,
  public home chrome).
- `curl http://localhost:8081/products/widget-3000` → **200**, real HTML
  (`<title>Widget 3000</title>`, `The flagship widget.` in the meta
  description — the seeded product entry rendering through the host's
  `productRenderer`).

Killed the server (`pkill` targeting the `go run` wrapper, then an explicit
`kill` of the actual listening `server` binary child process it spawned —
`go run`'s child survives the parent's death) and confirmed via `lsof -i
:8081` that the port was free afterward.

**Flags for jrazmi.** (1) The real-interaction check's default port
assumption (8080) in `00-overview.md` doesn't hold for `examples/minimal`
(8081) — no code change made per this phase's out-of-scope rule (no behavior
changes), just documented here since a future loop leg re-running the standing
check verbatim would need to know this. (2) Added a `tool` directive to
`features/cms/go.mod` (one line) as part of W1 — this is a go.mod change, not
a doc comment, but was required to make `make generate` actually regenerate
the `.templ` sources that live in that module (the previous target pointed at
a directory with none); flagging in case this reads as outside the phase's
"doc comments only" out-of-scope note, though W1 explicitly calls for fixing
generation. No other code behavior was changed — all other edits were doc
comments.

**2026-07-02, orchestrator addendum (post-executor):** the `tool` directive added
to `features/cms/go.mod` in W1 left the module untidy (templ CLI's own deps not in
the require list — surfaced by gopls, not by `go build`, because the workspace
resolved them). Fixed with `go mod tidy` in `features/cms`; `make check` re-run at
root: all 6 modules + all 4 guards green. Also corrected 00-overview.md's standing
real-interaction check to `examples/minimal`'s actual default port (8081, not 8080)
per the executor's flag #1.
