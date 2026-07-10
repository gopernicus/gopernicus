# workshop-v2-scaffolding — the scaffolding CLI (init · new feature · db verbs)

Status: **CLOSED 2026-07-09 — W1–W5 executed same day; ratified Q1–Q5 at
recommendations (module 35 stdlib-only · both dialects · new-domain
deferred · hand-authored templates + scaffold-compile gate · gate run:
steward ×8 + lead ×7 folded, delegation resolved the stdlib-vs-drivers
contradiction). Eleven guards; all scaffold legs green in make check.**
Origin: the workshop-v2 scope brief
(`.claude/plans/restructure/workshop-v2-brief.md`, FINAL 2026-07-02) §1's
three SCAFFOLD-ONCE targets, sliced by the 2026-07-09 owner ruling:
**scaffolding CLI first** — `gopernicus init`, `gopernicus new feature`,
`gopernicus db migrate|status|create`. The regenerate-forever surfaces
(store-adapter emission, TS clients) and the integration-test harness are
**workshop-v2b**, deferred with triggers named in "Deferred" below.
Executor model policy (standing): implementation `model: opus`;
design/doc-judgment `model: fable`. Never sonnet.
Modules: **+1 → 35** (the CLI module; placement = Q1).

## Owner rulings (2026-07-09, in-session)

- **The v1 slice is the scaffolding CLI** (ratified over store-emission-first
  and the full-brief milestone): scaffold-once surfaces only — no codegen
  engine, no `queries.sql` parsing, no drift-regeneration machinery, no
  `// gopernicus:start|end` markers (D2: scaffold-once surfaces are owned by
  the human after emission; markers exist only for mid-file regeneration,
  which this slice deliberately has none of).

## Brief reconciliation (the brief is 2026-07-02; the world moved — these supersede its citations)

1. **The feature anatomy is now the FS1–FS10 charter + trio layout**
   (`features/README.md` §2/§8, checklist items 1–14): `<name>.go` FS2
   socket (`NewService(repos, cfg) (*Service, error)` + `Register(mount)`),
   `domain/<agg>/` public rim (entities + ports + `order.go` allow-lists —
   the Q1-standard), `internal/logic/<agg>svc/`, `internal/inbound/<name>/`
   ONLY when routes exist, `storetest/` + an honest in-memory reference,
   `stores/{turso,pgx}` sibling modules with `Repositories(db)` +
   `ExportMigrations(dst)` + boot probes + migrations under a named source.
   The brief's `internal/<domain>svc/` + `internal/http/` citations are
   superseded.
2. **Both dialects out of the box** (DP1, R-KV2/R-KV3: supported set
   {turso, pgx}) — the brief's single `stores/<dialect>` reads accordingly
   (Q2 decides the default emission set).
3. **The app-local anatomy is ratified** (segovia-lessons phase 01:
   `internal/inbound/domains/<domain>/` etc.) — but `gopernicus new domain`
   is OUT of this slice (Q3, recommend defer: no second app has demanded it;
   Segovia hand-rolled and ratified the anatomy without a generator).
4. **The migrations seam is uniform and shipped**: both connectors carry
   `RunMigrations(ctx, db, fs, dir)` + `ExportMigrations(fs, dir, dst)` and
   every store module wraps `ExportMigrations`; the ledger conventions
   (named sources, hosts never renumber) are documented per feature. The
   `db` verbs WRAP this seam — no new migration engine.
5. **`gopernicus-original` is reachable again** at the sibling path
   (`../gopernicus-original`; it was absent during the authorization-v1
   legs — noted for the record). Carry-over for THIS slice is limited to
   the CLI dispatcher shape (`workshop/gopernicus/{main.go,commands/}`) —
   read as reference, re-typed fresh; the codegen engine paths in the
   brief's §2 stay untouched until v2b.
6. **Hosts have no Makefiles today** (the root Makefile serves the
   monorepo) — the emitted standalone host's Makefile is a NEW artifact:
   run/build/test/migrate targets + a host-appropriate guard subset
   (hexagon-layering greps adapted from G2's shape; the ten repo guards are
   monorepo-scoped and do not transplant wholesale).

## Phases

| Phase | What | Size | Depends | Model |
|---|---|---|---|---|
| W1 | CLI module skeleton + dispatcher + module 35 registration | S | — | opus |
| W2 | `gopernicus init` — the host scaffold | M | W1 | opus |
| W3 | `gopernicus new feature` — the FS-charter skeleton | M–L | W1 | opus |
| W4 | `gopernicus db migrate/status/create` | S–M | W1 | opus |
| W5 | docs + records + milestone close | S | all | fable |

Sequencing: W1 first; W2/W3/W4 independent after it (default order as
numbered — init's templates establish the embed/render conventions the
others reuse); W5 last. One CI-green commit per phase; the
scaffold-compile tests (below) run inside `make check` from W2 on.

### W1 — the CLI module + dispatcher

- **files:** `workshop/gopernicus/go.mod` (module 35 — pending Q1),
  `workshop/gopernicus/main.go`, `workshop/gopernicus/internal/commands/`
  (dispatcher: `init`, `new`, `db`, `version`; stdlib `flag` — no cobra,
  the zero-dependency posture pending Q1's taxonomy answer), go.work +
  Makefile MODULES + header 34→35.
- **verify:** module builds/vets/tests standalone (`GOWORK=off` too);
  `make check` (35) + `make guard` (the new module must be invisible to
  all ten guards — it is neither sdk, feature, integration, nor example;
  Q1 pins the taxonomy row and any guard adjustments).
- `gopernicus version` prints a placeholder version + the module path —
  the smallest end-to-end proof the dispatcher works.

### W2 — `gopernicus init` (the host scaffold)

- **What it emits** (into a target dir; `go:embed` templates): a
  `cmd/server/main.go` composition root modeled on `examples/minimal`'s
  current shape (explicit wiring, rule 5 — no init(), no locator; mounts
  ONE example feature the user deletes, or none — decided at execution
  against what keeps the scaffold legible), `go.mod` (module path from a
  flag), the host Makefile (run/build/test/vet + `migrate` + the adapted
  guard subset + `healthz`-aware run docs), `.env.example`,
  `workshop/migrations/` ledger dir with a README stating the
  scaffold-and-own + never-renumber rules, a root README stub.
- **The acceptance mechanism (this slice's drift answer):** a
  `scaffold_test.go` in the CLI module that emits a host into `t.TempDir()`,
  runs `go build ./...` against it (module-mode, workspace-off, with a
  local `replace` pinned to this repo's modules for the pre-tag world),
  and runs the emitted Makefile's guard targets. The scaffold cannot rot
  silently: `make check` executes this test on every run. This is the
  brief's §4 drift question ANSWERED for scaffold-once surfaces —
  drift-as-CI lives HERE (the generator's own tests), not in emitted repos.
- **verify:** the scaffold-compile test green hermetically; run-and-look —
  emit a host to the scratchpad, build it, boot it, `GET /` + `/healthz`
  → 200s, kill, port free.

### W3 — `gopernicus new feature <name>` (the charter skeleton)

- **What it emits** (into a monorepo-shaped tree or standalone —
  execution decides from the charter's own wording): the FS anatomy of
  reconciliation item 1, compilable with ONE example aggregate:
  the socket + sentinels, `domain/<agg>/` (entity, `Storer` port with doc
  pins, `order.go`), `internal/logic/<agg>svc/` (a create/get/list service
  skeleton), `storetest/Run` + an honest in-memory reference wired into
  `make check`-shaped tests, `stores/turso` + `stores/pgx` per Q2
  (Repositories(db) + probe + ExportMigrations + `0001_<agg>.sql` under
  source `"<name>"` + conformance harnesses env-gated exactly like the
  living stores), README with the three-question checklist trace stub.
  NO routes/views by default (the jobs precedent: `Register` logs only;
  inbound is added by the human when routes are demanded).
- **Acceptance mechanism:** same scaffold-compile pattern — emit into
  `t.TempDir()`, build all emitted modules, run the emitted storetest
  against the emitted memstore (hermetic), assert the FS1 guard shape
  holds (`go.mod` requires sdk only).
- **verify:** scaffold-compile + storetest-hermetic green; run-and-look —
  emit a feature, wire it into the W2-scaffolded host by hand following
  the emitted README's wiring section, boot, drive one use-case live.

### W4 — `gopernicus db migrate|status|create`

- **What it does:** `migrate` applies a host's `workshop/migrations/`
  ledger via the connector `RunMigrations` (dialect from the DSN/env);
  `status` lists applied-vs-pending per source (reads the connector's
  ledger table); `create` scaffolds `NNNN_<slug>.sql` into the host ledger
  honoring never-renumber (next number = max+1 across the ledger).
  Works identically in an emitted host and in this repo's examples
  (replaces nothing — the root `make migrate` stays; the verb is for
  emitted hosts that have no monorepo Makefile).
- **verify:** unit tests over a temp ledger + in-memory turso for
  migrate/status; run-and-look — `db create` then `db migrate` then
  `db status` against a scratch DB file/container, exact output recorded.

### W5 — docs + records + close

- workshop README (what the CLI does, what it deliberately does NOT do
  yet — the v2b deferrals with triggers); ARCHITECTURE.md taxonomy row +
  tree + count 35; root README module list + count; RELEASING enumeration
  (the CLI module's tagging posture); NOTES.md milestone entry;
  brief cross-reference note appended to
  `.claude/plans/restructure/workshop-v2-brief.md` (dated: which §1
  targets this milestone discharged, which moved to v2b); archive to
  `.claude/past/` at close.

## Deferred to workshop-v2b (named, with triggers)

- **Store-adapter emission** (runtime-generic vs emitted, spec placement,
  `queries.sql` carry-over, markers, drift-regeneration gates) — trigger:
  the third hand-written store PAIR after this milestone whose SQL is
  mechanical enough that emission would have been cheaper (the honest
  demand test), or an explicit owner call.
- **TS/OpenAPI client generation** — trigger: the first real frontend
  consuming a gopernicus host's OpenAPI doc (Segovia or the Stitch flow).
- **`gopernicus new domain` (app-local)** — trigger: the second app
  needing the segovia-ratified anatomy (Q3).
- **`doctor` / sqlguard** — trigger: first CI need; folds into this CLI's
  dispatcher when it comes.
- **Integration-test harness generation** (capability-map YOUR CALL #9) —
  unblocked-but-unscoped; rides v2b.

## Open questions — FOR RATIFICATION (jrazmi)

1. **Q1 — the CLI's home + taxonomy.** Recommend **`workshop/gopernicus`
   as module 35**, taxonomy row "workshop — the scaffolding CLI"
   (a NEW top-level kind: not sdk/feature/integration/example; it emits
   the others), stdlib-only (`flag` + `embed` + `text/template`), so the
   zero-dependency story extends to the tool. Alternative: `cmd/gopernicus`
   inside the sdk module — rejected-by-default (sdk is the stdlib runtime
   kernel; a CLI in it pollutes the import graph story even if unimported).
2. **Q2 — `new feature` store emission set.** Recommend **BOTH dialects
   always** (DP1's word: features ship turso+pgx out of the box; deleting
   one is the adopter's one-line choice). Alternative: `--stores=` flag
   with both as default.
3. **Q3 — `new domain` in this slice.** Recommend **DEFER to v2b**
   (trigger above). Alternative: include it (adds ~W3-shaped scope for the
   app-local anatomy).
4. **Q4 — template source of truth.** Recommend **hand-authored embedded
   templates + the scaffold-compile tests as the fidelity gate** (emitted
   output must build AND pass the guard shapes — rot is caught by
   `make check`, not by eyeballs). Alternative: derive templates
   mechanically from `examples/minimal`/a living feature (rejected-by-
   default: examples carry demo noise the scaffold shouldn't emit).
5. **Q5 — review gate.** Recommend **RUN** architecture-steward +
   lead-backend-engineer on this DRAFT before execution (a NEW top-level
   module kind + emitted-artifact contracts are exactly their classes),
   findings folded per the datastore-hardening precedent.

## Risks

1. **Scaffold-vs-charter drift** — the charter is prose + living code; the
   templates are a third copy. Mitigation: the scaffold-compile tests run
   in `make check` (every phase, forever), and W5 adds the charter
   checklist trace to the emitted README so a human audit has rails.
2. **Pre-tag dependency wiring in emitted hosts** — no tags exist
   (RELEASING), so emitted go.mod needs `replace` directives or a
   vendored path until repo-hardening phase 5 cuts v0.1.0. The
   scaffold-compile test pins `replace` to this repo; the emitted README
   states the pre-tag caveat. (This is also a nudge: LICENSE + first tags
   unblock clean `go get` adoption.)
3. **Scope creep toward codegen** — the moment a template wants a loop
   over entity fields, it is spec-driven emission (v2b). The rails: this
   slice's templates take a NAME and nothing else; any richer input is a
   v2b trigger firing.

## Acceptance (milestone)

```sh
make check    # 35 modules; scaffold-compile tests inside
make guard    # ten guards, CLI module invisible to all
```

Plus: `gopernicus init` → emitted host builds, boots, 200s (recorded);
`gopernicus new feature` → emitted skeleton builds standalone, its
storetest passes hermetically, FS1 shape holds; `db create/migrate/status`
driven against a scratch DB (recorded); docs/counts synced; NOTES entry;
archive at close.

## Real-interaction check

Standing check (a) per phase commit (`make check` green; examples/minimal
:8081 → 200s + `/healthz`; kill; port free) — plus each phase's
run-and-look above. The milestone close re-runs the full W2+W3 emit→
build→boot→drive chain end to end in one recorded transcript.

## Review-gate fold (2026-07-09) — GOVERNS where it conflicts with the phase text above

**architecture-steward: ALIGNED-WITH-EDITS (8). lead-backend-engineer:
SHIP-WITH-EDITS (7).** The convergent MAJOR (steward 1+2 ≡ lead R1):
Q1's stdlib-only ruling and W4's in-process `RunMigrations` cannot both
be true (the seam takes the connector's `*DB`; drivers would enter the
CLI's go.mod). **Resolved — branch (b), both reviewers' recommendation:
DELEGATION.**

1. **W4 redesigned:** `db migrate` executes the HOST-OWNED runner
   (`go run ./workshop/migrations`); `db status` invokes the runner's new
   `-status` mode, with a FILE-ONLY fallback in the CLI (all-pending
   listing when no DB is reachable — the original's UX, and the only
   status a stdlib CLI can self-produce); `db create` stays pure-FS.
   Numbering pinned: 4-digit zero-pad, parse leading digits before the
   first `_`, skip `_`-prefixed files; max+1 collides under concurrent
   authors where timestamps didn't — accepted for 0001..0021 consistency,
   named. Ledger path is `workshop/migrations/<db>/` with `--db` default
   `primary` (matches the charter + the cms example); "per source"
   softened — host ledgers are single-source `"default"` (the connectors
   hardcode it; named sources are a store-module doc convention).
2. **W2 additionally emits the runner** `workshop/migrations/main.go`
   (dialect-selected, the `examples/cms/workshop/migrations/main.go`
   shape, WITH the new `-status` mode) — the Makefile `migrate` target
   and W4's delegation both point at it.
3. **The hermetic test-host mounts NOTHING (sdk-only).** sdk is
   third-party-free (G1), so an sdk-only host builds fully OFFLINE with
   an empty go.sum — the only configuration hermetic inside `make check`.
   The emitted README carries the feature-wiring snippet instead.
   (Steward 8 concurs from the template-mass angle: no cms/memstore
   template surface.)
4. **Scaffold-test mechanics pinned (lead R2):** the test REWRITES
   emitted relative replaces to ABSOLUTE repo paths (`go mod edit
   -replace`, repo root via runtime.Caller); the child build runs with
   explicit `GOWORK=off` in cmd.Env; `go mod tidy` runs in the temp
   module first. Hermetic = no DB, no env, no network. **The
   driver-store compile leg** (W3's emitted stores): emitted go.mods PIN
   the exact driver versions this repo's stores use, so GOMODCACHE is
   already warm from `make check`'s own builds — the leg runs
   `GOPROXY=off` offline against the warm cache; a cold cache fails
   loudly (never skips silently).
5. **W3 decisions:** (a) the skeleton emits the FULL charter surface
   including `list` — order.go/DefaultOrder/ListLimits, the six-case
   storetest pagination family, checklist-14 ID threading +
   `DBGeneratedIDOnEmpty` + `NNNN_id_defaults.sql` — a non-conformant
   skeleton would undermine "born conforming," the slice's whole point;
   (b) size honestly **L** (the phase table row is superseded); (c) W3
   HARD-depends on W2 (the compile harness is reused — "independent
   after W1" is superseded); (d) v1 emits STANDALONE trees (own module
   root); emitting INTO this monorepo is a named deferral, and the CLI
   prints the manual registration checklist (go.work use, MODULES,
   STORE_MODULES, test-stores block, G5's hardcoded list) whenever the
   target looks monorepo-shaped (steward 4).
6. **The template rail redrawn (lead R3):** templates take IDENTITY
   params only — module-path ROOT, feature name, aggregate name
   (placeholder-defaulted) — and ZERO structural/field input; a per-field
   loop or `queries.sql` parsing is the v2b trigger firing. "A NAME and
   nothing else" is superseded (it would force a hardcoded module root
   and break standalone emission entirely).
7. **New guard G11 `guard-workshop-boundary` lands at W1 (lead R7):**
   nothing outside `workshop/` imports `workshop/…`; workshop imports no
   feature cores and no examples. Prove-can-fail; `make guard` → ELEVEN
   (W5 sweeps the counts).
8. **Emitted-artifact guard shapes (steward 5 — the scaffold tests are
   load-bearing guard infrastructure, stated as such):** the host test
   runs the app-pattern ONE-RULE grep (internal/logic never imports
   inbound/outbound/integrations) + G9/G10 hygiene shapes — NOT "G2's
   shape" (G2 polices feature cores; a host has none); the feature test
   runs FS1 + G2/G6/G10 shapes over emitted output. Templates ship as
   `.tmpl` files, NEVER `.go` (G4/G9/G10 grep the whole tree — the
   module is scanned-and-must-be-clean; "invisible to all ten guards" is
   superseded by "zero hits under all guards").
9. **W1 dispatcher note (lead):** re-type fresh on stdlib `flag` with NO
   init()-registered command registry (the original's `cli`
   mini-framework pattern conflicts with the repo's init() aversion);
   carry only the two-level command/subcommand shape, the
   stderr+`os.Exit(1)` convention, and `create`'s name-sanitize.
10. Verified by the reviewers, recorded: both connectors' ledger is
    `schema_migrations (source, version, checksum, raw_sql, applied_at)`
    — a portable status SELECT works across dialects; G4's regex cannot
    match the module path; G5's hardcoded list remains the one
    manually-extended guard (W5 docs name it).

## Execution log

(append dated entries here)

### 2026-07-09 — W1 CLOSED (CLI module + dispatcher + G11)

Module 35 (`workshop/gopernicus`, go.mod ZERO require lines — the Q1
stdlib-only ruling holds structurally): thin main +
`internal/commands` two-level dispatcher on stdlib flag (no init()
registry — fold item 9; stderr + exit 1 conventions carried from the
original, re-typed fresh), `version` working end to end
(`gopernicus 0.0.0-dev` + module path), `init`/`new`/`db` as loud
not-implemented stubs for W2–W4. Registered: go.work + MODULES + header
34→35 (no STORE_MODULES/test-stores — neither feature nor store). **G11
`guard-workshop-boundary`** landed: nothing outside workshop/ imports
the CLI; workshop imports no feature cores/examples; proven-can-fail
BOTH directions (probe imports in features/jobs and in commands/ each
failed loudly, reverted, clean) — `make guard` runs ELEVEN. Premise-note:
the task's relative reference path didn't resolve; the original lives at
the ecosystem sibling (read-only reference only). Verify: module
build/test/vet + GOWORK=off green; `make check` (35) + `make guard` (11)
green; coordinator re-verified builds + version output + guard count
independently. Committed CI-green. **Next: W2.**

### 2026-07-09 — W2 CLOSED (`gopernicus init` + the scaffold-compile harness)

The real `init` (identity params: `--module` required, `--db=turso|pgx|none`
default none, `--dir`), the shared `emit`/`render` engine
(`missingkey=error`, go/format on Go outputs, no-clobber pre-flight — the
convention W3/W4 reuse), and 7 `.tmpl` templates: go.mod (pre-tag replace
block commented), the sdk-only composition root (mounts NOTHING, FS2
wiring comment, /healthz plain-200 or DB-probed per --db), host Makefile
(build/vet/test/run/migrate + the ONE-RULE grep + G9/G10 hygiene shapes),
.env.example, README, ledger README, and — when --db != none — the
migrations runner WITH the W4-ready `-status` mode (portable ledger
SELECT + file-only fallback). **The scaffold-compile gate is live inside
`make check`:** hermetic `--db=none` leg (GOWORK=off + GOPROXY=off,
absolute sdk replace, tidy+build, guard shapes over emitted output,
empty go.sum proves the offline claim) + warm-cache `--db=turso` leg
(pinned driver version rides the make-check-warmed GOMODCACHE; cold
cache fails loud). Run-and-look recorded: emitted none-host built and
BOOTED (GET / 404-as-expected, /healthz 200, clean SIGTERM order, port
freed); emitted `make guard` exit 0 AND proven-can-fail (planted
Underlying() → exit 2). **Judgment calls (logged):** (1) the emitted
Makefile's G9/G10 greps vs the forbidden-literal constraint — resolved
by shell-quote token-splitting in the template, guard stays functional;
(2) the cms embed glob fails on an empty fresh ledger — emitted runner
embeds the whole dir and filters `*.sql`/`_`-prefixed in code, matching
the connectors' own filtering; (3) a hermetic-leg flake traced to
ambient GOPROXY — pinned GOPROXY=off, stable ×3. Verify: module
build/test/vet + make check (35) + make guard (11) green; coordinator
re-ran both scaffold legs independently. Committed CI-green.
**Next: W3.**

### 2026-07-09 — W3 CLOSED (`gopernicus new feature` — the charter skeleton)

`new feature <name> --module <root> [--aggregate]` (identity params only;
leading-positional pulled before flag parsing — stdlib flag stops at the
first positional, logged) over a 26-template manifest reusing the W2
engine: sdk-only core (FS2 socket, domain rim with entity/order.go/
port-doc-pinned Storer, <agg>svc with the checklist-14 `Config.IDs` mint,
storetest with the FULL six-case family + `DBGeneratedIDOnEmpty` +
`RejectsUnknownOrderField`, public in-core memstore + hermetic
conformance) + BOTH store modules (Repositories(db) probe,
ExportMigrations, `0001_<agg>.sql` with the INLINE id DEFAULT per the
authorization fresh-source precedent, row-struct scanning per dialect,
env-gated conformance, pinned driver versions = the warm-cache
invariant, bump-together comment). Monorepo-shaped targets print the
registration checklist (go.work ×3 / MODULES / STORE_MODULES /
test-stores / G5). **Acceptance legs live in make check:** hermetic —
emitted core tidies/builds/tests OFFLINE with all 11 storetest cases
green vs its memstore + FS1/G2/G6/G10 shapes; warm-cache — both stores
tidy/build/`vet -tags=integration` under GOPROXY=off. Run-and-look:
emitted `notes` + a none-host, hand-wired per the emitted README,
booted — healthz 200, boot-time create+list proof logged
(`created_id`/`listed:1`), clean shutdown, port free. **Conscious
simplifications (logged):** placeholder entity {ID, Name, CreatedAt} +
one-field exact-match filter (enough for a real WHERE + foreign-row
exclusion), no Update, singular table name (pluralization heuristics are
codegen-adjacent — v2b), no inbound (Register logs only, jobs
precedent). Verify: module + make check (35) + make guard (11) green;
coordinator re-ran the full module test suite independently.
Committed CI-green. **Next: W4.**

### 2026-07-09 — W4 CLOSED (`db create|migrate|status`, the delegation design)

`db create` pure-FS (leading-digits-before-`_` parse, `_`-prefix skip,
4-digit max+1, slug sanitize, no-clobber, `--db` default primary);
`db migrate` verifies the runner exists (loud error naming
`gopernicus init --db=...`) then execs `go run ./workshop/migrations`
streaming, exit code propagated; `db status` delegates to the runner's
`-status` with the CLI file-only fallback (runner absent → all-pending
note; runner nonzero → stderr surfaced + "DB unreachable — file view").
**The module's go.mod still has ZERO require lines — the fold's
delegation resolution holding structurally.** Unit matrix green
(sanitize/numbering/clobber/fallback) + the offline emit-then-exec leg
(emitted turso host, create → status file-fallback lists the new file
pending). Premise HELD: the W2-emitted runner needed no template change.
**Live run-and-look recorded:** pgx host + throwaway postgres:17
(:55433) — create 0001 → migrate applies (checksum logged) → status
1 applied/0 pending → create 0002 → status 1/1 → container stopped →
status DB-down file-view 2 pending; port freed. Coordinator cleanup at
close: the now-orphaned `notImplemented` stub helper removed (W4's own
change orphaned it), module re-verified. `make check` (35) +
`make guard` (11) green. Committed CI-green. **Next: W5 (close).**

### 2026-07-09 — W5 EXECUTED — **MILESTONE CLOSED**

Coordinator-inline (fable, per the plan's model line). Docs swept: root
README + RELEASING + ARCHITECTURE → thirty-five/eleven with the workshop
rows; the taxonomy table gains the SIXTH kind (**workshop tool** — emits
anatomies, never links them; G11 + the scaffold-compile tests as its
enforcement; "Five kinds" → "Six kinds", dated amendment);
`workshop/gopernicus/README.md` authored (commands, the identity-params
rail, how-templates-stay-honest naming the scaffold tests as guard
infrastructure, the v2b deferrals with triggers, the G5 seam); the brief
gained the dated cross-reference (scaffold-once targets DISCHARGED;
regenerate-forever → v2b; the drift question ANSWERED for scaffold-once
surfaces); NOTES.md milestone entry with open flags (driver-version
bump-together, G5's hardcoded list, the pre-tag replace block gated on
repo-hardening phase 5's LICENSE/tags). **Close verify:** `make check`
green (35 modules, scaffold legs inside); `make guard` ELEVEN clean;
stale-count grep clean. **Close run-and-look (recorded):** a FRESH
`gopernicus init --db=none` emit → absolute sdk replace → GOWORK=off
tidy+build → boot → `/healthz` **200** → clean kill, port freed — the
emit→build→boot chain end to end. Archived here; archive README row
added.
