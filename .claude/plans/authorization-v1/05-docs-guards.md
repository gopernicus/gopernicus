# Phase Z5 — docs sync + guards + milestone close

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: fable (task-1 is Makefile mechanics — opus)
Depends on: all (Z1, Z2a, Z2b, Z4; and Q1/Q3/Q4/Q5 answers)
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
  three-posture decision table, then the KINDS table, before any
  model-DSL/engine content**
  (design §13 Z5, verbatim requirement + the owner direction), stating
  the cms-admin-gating-stays-coarse boundary explicitly (a documented
  boundary, not a gap), with item-12 nil-semantics tables per kind, the
  **policy-seam section** (overview: "The policy seam"), and the
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
  shape), in this order — the order is the Z5 mandate as amended by the
  owner direction:
  (1) **The §2.1 three-posture table, reprinted first** (none /
  host-authored / flagship — what the host does, modules pulled,
  migrations), before any model-DSL or engine content, with the AV2 line:
  consumer seams are Check-only; everything else on `Service` is
  flagship API, never a seam; graduation trigger recorded (two features,
  identical vocabulary).
  (2) **The KINDS table (owner direction):** relationships / roles /
  policy(deferred) — one row per kind: what it expresses, its
  `Repositories` field, its table, its Service method family, its nil
  semantics (kind OFF structurally). States plainly: kinds are
  independently wireable; ReBAC is one kind, not the feature's identity;
  kinds are port-optional but schema is wholesale (the §2.1 bounding
  rule applied intra-feature) — **including the roles-only adopter line
  (re-review note 15): a roles-only host still applies the FULL
  `"authorization"` source, `iam_relationships` included, and both boot
  probes expect both tables**; no composed Check facade — hosts compose
  kinds in their own closures (refinement 13). **Terminology guard
  (re-review note 13), one clarifying line:** a KIND is a nil-safe port
  family WITHIN one feature module — never a module or a taxonomy row
  (ARCHITECTURE.md's R6 "Kinds of module" table is unrelated
  vocabulary).
  (3) **The cms boundary, explicit:** cms admin gating stays coarse
  (`AdminMiddleware` = session-level `RequireUser`) — a documented
  boundary of this milestone, not a gap; fine-grained cms authorization
  is future demand-gated work.
  (4) Anatomy + socket (FS2 form), the `/authorization/*`
  claimed-unregistered namespace (C1), the model DSL with the
  zero-migration registered-data point (relationship kind only; roles
  are opaque strings, no model).
  (5) **Item-12 nil/required-semantics tables** per kind
  (review-gate fold, major 1 — no `PlatformAdmin` row exists):
  `Repositories.Relationships` (nil = relationship kind off; wired ⇔
  `Config.Model` set — partial wiring is a loud error),
  `Repositories.Roles` (nil = roles kind off), zero kinds = loud
  `ErrNoKindConfigured`, unwired-kind methods = loud per-kind sentinel,
  `Config.Model`
  (required with Relationships, validated loudly),
  `Config.MaxTraversalDepth` (optional —
  `<= 0` ⇒ default 10, never an error; relationship-kind-scoped; the
  SHARED bound across
  engine/memstore/CTEs). **One sentence on the asymmetry (re-review note
  14):** an orphaned `Model` errors loudly because it is
  capability-defining (a model with no engine is a misconfiguration
  trap), while an orphaned `MaxTraversalDepth` is ignored-with-note
  because it is a tuning knob (the auth MailFrom precedent). Plus a
  **platform-admin section documenting the
  DATA convention**: the `platform:main#admin@<type>:<id>` tuple over a
  host-declared `platform` resource type — no bypass exists unless the
  host declares the type and creates the tuple; `Unrestricted`
  consequences spelled out; and the Q5 role-scope rule (global fallback
  or its ratified alternative) stated as the one documented rule,
  **together with the lead-major-3 limitation named explicitly:
  enumeration and decision diverge — `ListRoleAssignmentsByResource`
  surfaces direct-scope assignments only and never shows
  globally-granted subjects `HasRole` would allow; an
  accepted-and-documented v1 limitation (the count-pin precedent),
  "effective grants for a resource" a named deferred item**. Then
  the consumer-side
  rows (a nil seam in a CONSUMING feature = deny-by-absence — pointing
  at auth's `Granter` and events' `Authorize` rows as the live
  examples).
  (6) **The policy-seam section** (owner direction ruling 1 — the
  overview's "The policy seam" section reprinted verbatim in intent):
  what the seam looks like when it lands (`domain/policy` rim, one
  nil-safe `Repositories.Policies` field, possibly `iam_policies`), the
  named data-driven vs code-registered design question, and the demand
  trigger — the deferral is a documented seam, not a gap.
  (7) Store parity: {turso, pgx} + memstore, one storetest, the **named
  adversarial sub-runners listed by name** + the `Roles/*` family, with
  one sentence on why the
  Count assertion is a security assertion (§2.5); the recursive-CTE vs
  graph-walk note; the migration source `"authorization"` +
  `0001_iam_relationships.sql`/`0002_iam_roles.sql` +
  scaffold-and-own prerequisite + boot probes; the Q4 outcome (metadata
  table present-with-divergence-note, or trimmed-with-return-trigger).
  (8) **The wiring page** (design §13 plan-cut requirement 3): one ascii
  diagram of the stops — model declaration → `NewService` (both kinds) →
  Granter closure → Check closure into `AuthorizeStream` →
  `LookupResources` → the role-gated check — and ONE complete `main.go`
  listing that is the
  auth-cms commit-2+task-4 twin (FS2 method form everywhere); the
  store-module
  swap (memstore → `stores/turso` + the migration step) as an explicit
  labeled snippet; a labeled roles-only wiring snippet
  (`Repositories{Roles: …}`, no model — the kind independence made
  visible); and **one labeled composed-kinds closure snippet (re-review
  steward minor 7, the refinement-13 reference pattern)** — Check OR
  HasRole, **fail-closed on error**, with the anti-pattern named
  explicitly: an `allowed, _ :=` closure is a silent fail-OPEN. The
  three postures each get a pointer to their
  living/recorded artifact (examples/minimal wires nothing; the Z4
  commit-1 protocol; the Z4 commit-2 host).
  (9) Non-goals reprinted as cut lines (§11 + the owner direction): no
  sdk port, no
  PostfilterLoop (with the paired-seam constraint from §2.6), no groups
  (Q1 outcome + return trigger), no composed Check facade, no role
  registry/vocabulary, no policy kind (the §6 seam section is its
  ledger), no routes in v1.

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
  example). (5) Design status header amendment: extend the dated
  2026-07-08 multi-kind amendment line with the execution record — Z1–Z5
  executed via
  `.claude/plans/authorization-v1/` (Q1–Q5 outcomes named), plus the
  overview's staleness findings recorded (14-method Storer; §2.2's
  user-shaped-seam note overtaken by the shipped Principal seam; stale
  module counts). (6) capability-map ReBAC/authorization rows → BUILT
  with pointers (noting the multi-kind shape: relationships + roles
  shipped, policy seam deferred). (7) NOTES.md dated milestone entry:
  what shipped (both kinds), the
  cut refinements + Q outcomes + the owner direction, **both live-store
  artifacts**
  (suite/dialect/DSN-class/result — every named adversarial sub-runner
  AND the `Roles/*` family covered), the Z4 protocol results verbatim
  (incl. the commit-1
  clean-graph capture, both mandated demonstrations with commit
  hashes, and the roles leg), guard changes (the Z1 FS1 list add; Q3
  outcome +
  prove-can-fail record or deferral note), the policy-seam deferral +
  trigger (the deferral-ledger entry), **the kind-boundary enforcement
  note (re-review note 13): kind boundaries are enforced BEHAVIORALLY
  (construction/sentinel tests + storetest), not guard-shaped —
  deliberate, since kinds are intra-module and invisible to import
  guards**, open flags for jrazmi. (8) Plans
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
`examples/auth-cms` re-running Z4 protocol steps 5–6 and 10 (the docs
phase must not close on a stale memory of the proof — both kinds).

## Execution log

(append dated entries here)
