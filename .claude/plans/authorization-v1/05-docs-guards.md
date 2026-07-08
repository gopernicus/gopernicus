# Phase Z5 — docs sync + guards + milestone close

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: fable (task-1 is Makefile mechanics — opus)
Depends on: all (Z1, Z2a, Z2b, Z4; and Q1/Q3/Q4 answers)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §13 Z5 —
**as reconciled by overview drift item 1**: the design's "G5
feature→feature guard (new Makefile target, prove-can-fail)" ALREADY
EXISTS — it landed 2026-07-08 as **G7 `guard-feature-no-cross-feature`**
(events-v1 task-13; the "G5" name was taken by the FS1 guard), a
per-feature generic loop that has auto-covered `features/authorization`
since Z1 task-1. **This phase does NOT build a cross-feature guard.**
The FS1 guard-list add moved to **Z1 task-1** at the review-gate fold
(steward minor 4); this phase's guard work is exactly one item:
executing the Q3 decision on the store-module-glue guard.

## DoD

- `features/authorization` verified present in the FS1 guard's
  hardcoded list (landed Z1 task-1 with its prove-can-fail — review-gate
  fold, steward minor 4); `make guard` green.
- Q3 executed: the store-glue guard added (prove-can-fail recorded;
  incl. the store→`examples/` alternation or its consciously named skip
  — steward minor 6) or the guard's conscious deferral recorded in
  NOTES + the feature README.
- `features/authorization/README.md` shipped — **opening with the §2.1
  three-posture decision table before any model-DSL/engine content**
  (design §13 Z5, verbatim requirement), stating the
  cms-admin-gating-stays-coarse boundary explicitly (a documented
  boundary, not a gap), with item-12 nil-semantics tables and the
  per-capability wiring page.
- Registration artifacts consistent: `go.work`, Makefile
  `MODULES`/`STORE_MODULES`/`test-stores` (landed in Z1/Z2a/Z2b —
  verified here), RELEASING.md module enumeration, ARCHITECTURE.md tree
  + count (**33**), README.md counts.
- Capability-map ReBAC rows marked BUILT; design status header amended
  (executed-via note + the overview's staleness findings); NOTES.md
  milestone entry with both live-store artifacts and the Z4 protocol
  results.
- Fresh full `make check` green (33 modules, all guards).

## Preconditions

- Z2a/Z2b live-run transcripts present in their execution logs (this
  phase turns them into the dated NOTES artifacts).
- Z4's protocol transcript + commit hashes present.

## Tasks

### task-1: guards — the Q3 decision (FS1 list add landed at Z1)

- **depends_on:** []
- **model:** opus
- **files:** [Makefile]
- **verify:** `make guard` green on a clean tree; verify `features/authorization` is in the FS1 guard loop (landed Z1 task-1); if Q3 = ADD, prove-can-fail (A4 practice): temporarily add a `features/events` import to a `features/authorization/stores/turso` file, observe the new guard fail, revert, green — and repeat with an `examples/` import if the alternation is included; then full `make check`
- **description:** Execute Q3 (the FS1 guard-list add moved to Z1
  task-1 — review-gate fold, steward minor 4; this task only VERIFIES
  it): if ADD, new target `guard-store-no-foreign-feature` — for each
  `features/<x>/stores/*` subtree, grep for
  `"github.com/gopernicus/gopernicus/features/<y>` (y ≠ x), print
  nothing and exit 0 clean, loud error otherwise (match the existing
  targets' shape; G7's self-import filter is the template); **per
  steward minor 6, the pattern gains one extra alternation so it also
  catches store→`examples/` imports (currently unguarded by anything)
  — or the skip is consciously named in the target's comment and the
  NOTES entry**; wire it into the `guard` aggregate and the header
  comment ("runs all eight"). If Q3 = DEFER, no Makefile change — the
  deferral lands in task-2's README note + task-3's NOTES entry,
  alongside the standing deferred-rail acceptance grep.

### task-2: `features/authorization/README.md` — postures first, then the wiring page

- **depends_on:** [task-1]
- **model:** fable
- **files:** [features/authorization/README.md]
- **verify:** `make guard` (docs-only); the fresh-eyes pass (events gate-edit-4 practice): the wiring tour verified line-for-line against `examples/auth-cms/cmd/server/main.go` (its commit-2 state IS the executable twin); read-back confirms the three-posture table is the FIRST substantive section
- **description:** The feature README (auth/jobs/events READMEs are the
  shape), in this order — the order is the Z5 mandate:
  (1) **The §2.1 three-posture table, reprinted first** (none /
  host-authored / flagship — what the host does, modules pulled,
  migrations), before any model-DSL or engine content, with the AV2 line:
  consumer seams are Check-only; everything else on `Service` is
  flagship API, never a seam; graduation trigger recorded (two features,
  identical vocabulary).
  (2) **The cms boundary, explicit:** cms admin gating stays coarse
  (`AdminMiddleware` = session-level `RequireUser`) — a documented
  boundary of this milestone, not a gap; fine-grained cms authorization
  is future demand-gated work.
  (3) Anatomy + socket (FS2 form), the `/authorization/*`
  claimed-unregistered namespace (C1), the model DSL with the
  zero-migration registered-data point.
  (4) **Item-12 nil/required-semantics tables** for every port
  (review-gate fold, major 1 — no `PlatformAdmin` row exists):
  `Repositories.Relationships` (required, loud error), `Config.Model`
  (required, validated loudly), `Config.MaxTraversalDepth` (optional —
  `<= 0` ⇒ default 10, never an error; the SHARED bound across
  engine/memstore/CTEs), plus a **platform-admin section documenting the
  DATA convention**: the `platform:main#admin@<type>:<id>` tuple over a
  host-declared `platform` resource type — no bypass exists unless the
  host declares the type and creates the tuple; `Unrestricted`
  consequences spelled out. Then the consumer-side
  rows (a nil seam in a CONSUMING feature = deny-by-absence — pointing
  at auth's `Granter` and events' `Authorize` rows as the live
  examples).
  (5) Store parity: {turso, pgx} + memstore, one storetest, the **named
  adversarial sub-runners listed by name** with one sentence on why the
  Count assertion is a security assertion (§2.5); the recursive-CTE vs
  graph-walk note; the migration source `"authorization"` +
  scaffold-and-own prerequisite + boot probe; the Q4 outcome (metadata
  table present-with-divergence-note, or trimmed-with-return-trigger).
  (6) **The wiring page** (design §13 plan-cut requirement 3): one ascii
  diagram of the five stops — model declaration → `NewService` →
  Granter closure → Check closure into `AuthorizeStream` →
  `LookupResources` — and ONE complete `main.go` listing that is the
  auth-cms commit-2 twin (FS2 method form everywhere); the store-module
  swap (memstore → `stores/turso` + the migration step) as an explicit
  labeled snippet. The three postures each get a pointer to their
  living/recorded artifact (examples/minimal wires nothing; the Z4
  commit-1 protocol; the Z4 commit-2 host).
  (7) Non-goals reprinted as cut lines (§11): no sdk port, no
  PostfilterLoop (with the paired-seam constraint from §2.6), no groups
  (Q1 outcome + return trigger), no routes in v1.

### task-3: repo docs sync + records + milestone close

- **depends_on:** [task-2]
- **model:** fable
- **files:** [ARCHITECTURE.md, README.md, RELEASING.md, Makefile,
  features/README.md, .claude/plans/roadmap/auth-v2-feature-design.md,
  .claude/plans/restructure/capability-map.md, NOTES.md]
- **verify:** full `make check` (33 modules, all guards) then `grep -rn 'Thirty modules\|30 modules' ARCHITECTURE.md README.md RELEASING.md Makefile` returns nothing unintentional; go.work ↔ MODULES ↔ STORE_MODULES ↔ RELEASING enumerations agree
- **description:** (1) ARCHITECTURE.md: module tree gains the
  authorization trio; "Thirty modules today" → thirty-three; taxonomy
  examples updated where features are enumerated; **while in the tree,
  sweep the stale `auth/` directory label at ~line 27 to
  `authentication/` (pre-existing A-R1 staleness — review-gate fold,
  steward minor 11)**. (2) README.md +
  RELEASING.md enumerations → 33. (3) Makefile header count (verified —
  landed incrementally in Z1/Z2a/Z2b). (4) features/README.md:
  checklist-trace touch-ups for authorization (the §5 C2 section can
  cite the Granter-swap + Check-closure wiring as a second REAL worked
  example). (5) Design status header amendment: Z1–Z5 executed via
  `.claude/plans/authorization-v1/` (Q1–Q4 outcomes named), plus the
  overview's staleness findings recorded (14-method Storer; §2.2's
  user-shaped-seam note overtaken by the shipped Principal seam; stale
  module counts). (6) capability-map ReBAC/authorization rows → BUILT
  with pointers. (7) NOTES.md dated milestone entry: what shipped, the
  cut refinements + Q outcomes, **both live-store artifacts**
  (suite/dialect/DSN-class/result — every named adversarial sub-runner
  covered), the Z4 protocol results verbatim (incl. the commit-1
  clean-graph capture and both mandated demonstrations with commit
  hashes), guard changes (the Z1 FS1 list add; Q3 outcome +
  prove-can-fail record or deferral note), open flags for jrazmi. (8) Plans
  housekeeping at close: `.claude/plans/authorization-v1/` →
  `.claude/past/`, archive README row — per standing practice.

## Acceptance

```sh
make check     # 33 modules, all guards (eight if Q3 = ADD)
make guard
```

Docs greps per task-3's verify; the NOTES entry complete; rule-6 +
deferred-rail greps re-run once at close:

```sh
grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/   # → empty
grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/features/authorization' features/authentication/ features/cms/ features/events/ features/jobs/   # → empty
```

## Real-interaction check

Standing check (a) after the final commit: `make check` green (33);
`examples/minimal` :8081 → 200s; kill; port free. Plus one fresh boot of
`examples/auth-cms` re-running Z4 protocol steps 5–6 (the docs phase must
not close on a stale memory of the proof).

## Execution log

(append dated entries here)
