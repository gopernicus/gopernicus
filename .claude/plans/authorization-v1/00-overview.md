# authorization-v1 — milestone overview

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Milestone: `authorization-v1` — `features/authorization`: the first-party
ReBAC engine (the flagship of the ratified three-posture ruling), model
DSL, memstore + storetest with the named adversarial sub-runners, both
dialect stores, the consumer-seam proof, and docs/guards.
Design of record: `.claude/plans/roadmap/auth-v2-feature-design.md` —
**RATIFIED 2026-07-07 (jrazmi), all AV defaults** (§§2, 10, 13 Z-table;
NOTES.md 2026-07-07 entry is the record). Nothing that design decides is
re-decided here; this plan phases and operationalizes its Z1–Z5 table
(~lines 794–798) with every embedded review-gate amendment, and absorbs
the post-design landings enumerated in "Post-design drift, reconciled"
below.
Cut precedent: `.claude/past/auth-v2/` (overview + phase files) for shape;
`.claude/past/events-v1/plan.md` for operational discipline (per-task
verify blocks, real-interaction protocol, live-store gating, execution-log
stubs). Per the events precedent, **the tier-review question re-runs at
this plan cut** (design §13 plan-cut requirement 1) — see Recommended
reviews.

Executor model policy (jrazmi, standing since jobs-v1): implementation
phases on `model: opus`; design/doc-judgment phases on `model: fable`.
Never sonnet.

## Inherited law

The constitution (`restructure/00-overview.md`, rules 1–8), the roadmap
rulings (R1–R10), the trio layout (`logic/<domain>` public rim;
`internal/logic/<svc>` + `internal/inbound` interior; `stores/` sibling
modules), store posture C with the supported set **{turso, pgx}**
(R-KV2/R-KV3), R3 (memstore placement — here the **public in-core
`memstore/`** allowance applies: substantial + host-needed, group
expansion re-implemented in Go; never a `stores/memory` module), R4
(`storetest` port-set sub-runners), the feature-standard charter FS1–FS10
(ratified 2026-07-07), and `features/README.md` checklist items 1–12 all
apply unchanged.

The **2026-07-06 authorization ruling** is this milestone's spine: ReBAC
**supported, never required** — three postures, all first-class (design
§2.1). The ratified AV table (design §12) is not relitigated; the rows
this milestone executes: AV1 (own module `features/authorization`), AV2
(consumer seams are Check-only; everything else is concrete-engine API),
AV5 (no principals registry — actor references are
`(subject_type, subject_id)` string pairs; `sdk/identity.Principal` is
the vocabulary, post-A-I1). Naming rule stands: authorization/authorizer,
authentication/authenticator — never abbreviated.

## Post-design drift, reconciled (the design predates these landings — absorbed, not re-decided)

1. **The cross-feature guard already exists.** Z5's "G5 feature→feature
   guard (new Makefile target, prove-can-fail)" landed 2026-07-08 as
   **G7 `guard-feature-no-cross-feature`** (events-v1 task-13; the "G5"
   label was already taken by the FS1 guard). G7 is a per-feature generic
   loop over `features/*/`, so `features/authorization` is
   **auto-covered with zero edits** the day the module exists. The
   remaining guard work is exactly two items: (a) add
   `features/authorization` to the **FS1 guard's hardcoded module list**
   (Makefile `guard-feature-core-sdk-only`, the "G5" slot — currently
   `for f in features/authentication features/cms features/events
   features/jobs`) — **lands at Z1 task-1** (review-gate fold, steward
   minor 4: leaving the core machine-unchecked across the store phases
   is the exact window the guard exists for); and (b) the design-§10
   **add-or-consciously-defer decision on the store-module-glue guard**
   ("store modules never import another feature's modules") — open
   question Q3 below, executed at Z5.
2. **FS2 method form (feature-standard, RATIFIED 2026-07-07).** The
   socket is `NewService(repos, cfg) (*Service, error)` with
   construction-time validation, plus `(*Service) Register(m
   feature.Mount) error` mounting routes only. The design's Z1 "socket
   (`New`/`Register`)" and §2.3's `New(repos, cfg) (*Authorizer, error)`
   / `Register(mount, repos, cfg)` read accordingly (cut refinement 1
   below: the driving surface is the conventional `*Service`; hosts name
   the variable `authorizer`). Born conforming: FS1 (go.mod = sdk only),
   FS9 (sdk/web responders — moot in v1, no routes), route tables as data
   (moot in v1), storetest + memstore-honest reference conventions.
3. **A-R1:** the sibling feature is `features/authentication` (renamed
   2026-07-08, events-v1 task-0). Every path and grep in this plan reads
   the new name.
4. **The events consumer seam's real shape.** `events.AuthorizeStream`
   is `func(ctx context.Context, principal identity.Principal,
   resourceType, resourceID string) (bool, error)`
   (`features/events/events.go:68` — a logged post-A-I1 divergence from
   the design's `userID string` sketch). Z4's "wire Authorizer.Check
   closures into events' AuthorizeStream" adapts to THAT signature:
   `identity.Principal{Type, ID}` maps onto
   `authorization.Subject{Type: p.Type, ID: p.ID}` unadapted (the exact
   convergence A-I1 predicted).
5. **Connector helpers exist (feature-standard D2–D6)** and the Z2 stores
   use them: `ExportMigrations`, `Scanner`/`Querier`, the timestamp
   bundles + `NullTime`/`NullTimePtr` pairs, keyset `ListPage[T]`,
   `ExecAffecting` — in both `integrations/datastores/turso` and
   `integrations/datastores/pgxdb`. Migrations source `"authorization"`.
   The **boot-time table probe** precedent (events stores' constructors)
   is **adopted**: both store constructors probe `rebac_relationships`
   and error before the host serves traffic.
6. **Module count is 30 today.** This milestone adds core + two stores →
   **33** (design §10's "+3 → 29" counts are stale). Z3's `groups`
   aggregate, if kept, lives inside the core module (no count change) —
   and Z3 is a named trim candidate, presented for ratification (Q1).
7. **Live-store gates** mirror events-v1/auth-v2 verbatim — see "Live-
   store gates" below: turso = the authorized playground DB only (URL
   asserted pre-run), pgx = docker postgres:17.

**Design-staleness findings beyond the seven (verified in code/salvage
2026-07-08 while cutting this plan; absorbed, flagged for the design's
status-header amendment at Z5):**

- **§2.5's `Storer` enumeration is incomplete.** The original's port
  (`gopernicus-original/core/auth/authorization/model.go:246`) has
  **14 methods** (count corrected at the review-gate fold): the design's
  list omits `CheckRelationExists` (the
  platform-admin tuple check) and the three LookupResources primitives
  (`LookupResourceIDs`, `LookupResourceIDsByRelationTarget`,
  `LookupDescendantResourceIDs` — the last a recursive-CTE transitive
  walk). Z1 salvages the full surface; the abbreviated design list was
  illustrative, not a trim.
- **§2.2's shape note is overtaken.** "events' shipped seam is
  user-shaped, and machine principals cannot flow through it" was true at
  design time; post-A-I1 the shipped seam takes `identity.Principal`
  (pair-shaped) — machine principals flow today. The graduation-shape
  worry is already satisfied; no action, recorded so nobody "fixes" it.
- **The metadata table has zero engine consumers post-AV4.** The
  original's `Storer` never touches `rebac_relationship_metadata`; its
  consumer was the invitation-as-resource bookkeeping AV4 deleted. §2.5
  pins the table (+ the pgx GIN divergence) as ratified scope, so
  trimming it is a jrazmi call, not a cut refinement — open question Q4.

## Cut-time refinements (operationalizations, logged per the jobs/auth-v2 precedent — none is a design change)

1. **FS2 socket naming.** The driving surface is
   `authorization.Service` (`NewService(repos, cfg) (*Service, error)`,
   `(*Service) Register(mount) error`) for uniformity with
   auth/jobs/events; the design's `*Authorizer` type name is superseded
   by FS2 (which postdates the design). Hosts write
   `authorizer, err := authorization.NewService(...)` — the prose noun
   survives as the variable name. All §2.3 engine methods are promoted on
   `Service` by thin delegation from `internal/logic/authorizersvc`.
2. **Z2 split into Z2a/Z2b** (turso then pgx), per the A7a/A7b precedent
   the design itself offers ("split into Z2a/Z2b at plan cut per the A7
   precedent if preferred") — the canonical migration filename set is
   authored once, in Z2a.
3. **Migration filename pinned:** `0001_rebac.sql` (source
   `"authorization"`), carrying `rebac_relationships` + its indexes
   (+ `rebac_relationship_metadata` only if Q4 = KEEP), matching the
   salvage shape (`0002_rebac.sql` there; 0001 here — new source).
4. **Two-layer storetest suite.** `storetest.Run(t, newStore)` runs
   (a) store-level port-contract cases against `relationship.Storer`
   directly, and (b) **engine-over-store cases** — it constructs
   `authorization.NewService` with a fixture model over the store under
   test and asserts authorization *outcomes*. The five named adversarial
   sub-runners are layer (b) plus the direct-count assertion in layer (a):
   that is how "the memstore and the recursive-CTE stores provably
   authorize identically" (design §2.3) is proven rather than asserted.
   (storetest lives in the core module and may import the root package —
   no cycle; root never imports storetest.)
5. **DSL/engine type placement.** Public-rim split of the original's
   one-package layout: tuple-level types + the `Storer` port →
   `logic/relationship` (stores implement them across the module
   boundary); engine API types (`Subject`, `CheckRequest`, `CheckResult`,
   `LookupResult`, `Schema`/`NewSchema`/`ResourceSchema`,
   `PermissionRule` builders) → `internal/logic/authorizersvc`, aliased
   at the root package (the `auth.Granter = invitationsvc.Granter`
   precedent). Verified feasible: the original's `Storer` signatures take
   strings + tuple types only — no engine type crosses into the rim.
6. **`Config` fields pinned (corrected at the review-gate fold —
   salvage-verified):** `Model Schema` (required — nil/empty → loud
   `ErrModelRequired`; schema-validated at `NewService`, invalid model =
   loud error) and `MaxTraversalDepth int` (the original's ONLY Config
   field, `authorizer.go:16` — default 10; `<= 0` ⇒ 10, never an
   error). **There is no `Config.PlatformAdmin`** — the earlier draft
   invented it; in the salvage, platform-admin is a **data tuple**
   `platform:main#admin@<type>:<id>` checked via
   `store.CheckRelationExists(ctx, "platform", "main", "admin",
   subj.Type, subj.ID)` (`authorizer.go:244`), requiring a `platform`
   resource type declared in the host's schema. Faithful salvage is the
   ruling here: a config-level bypass would amend ratified §2.5 and is
   NOT this plan's to decide. No `Config.Logger` (the events precedent:
   keep the enumerated set exact; `Register` reaches `mount.Logger`).
   `Repositories{Relationships relationship.Storer}` — required, nil →
   loud `ErrRelationshipsRequired` (the Hasher/Mailer precedent).
7. **v1 registers no routes** (the jobs precedent): `Register(mount)`
   touches `mount.Logger` only; the `/authorization/*` namespace is
   claimed-unregistered for a future admin surface (documented in the
   README, Z5).
8. **`explain.go` + `cache_store.go` are salvage-if-free** (design §2.5)
   — never acceptance criteria; Z1 logs build-or-skip.
9. **Registration surfaces staged as events-v1 did — amended at the
   review-gate fold:** Z1 registers the core in `go.work` + Makefile
   `MODULES` (make check must iterate it) **and adds
   `features/authorization` to the FS1 guard list with its own
   prove-can-fail** (steward minor 4 — supersedes the earlier
   defer-to-Z5 staging); Z2a/Z2b register their store modules +
   `STORE_MODULES` + `test-stores` legs; Z5 keeps only the Q3 guard
   decision.
10. **Clean-graph captures are workspace-independent:** every
   module-graph proof (Z4's middle-posture capture, the standing
   zero-driver/libsql claims) uses **`GOWORK=off go list -m all`** (the
   events-v1/auth-v2 recorded form) — under go.work the plain form lists
   every workspace module, so the middle-posture artifact would
   false-fail the moment Z1 registers the module.
11. **Store constructor pinned:** `Repositories(db)
   (authorization.Repositories, error)` with the boot probe inside
   (charter checklist item 5; `features/jobs/stores/turso/turso.go:29`
   precedent) — identical in Z2a and Z2b.

## Phases (design §13 Z-numbering kept)

| Phase | File | What | Size | Depends on | Model | Modules after |
|---|---|---|---|---|---|---|
| Z1 | `01-core.md` | `features/authorization` core: rim + `Storer`, model DSL + schema validator, engine salvage (check/self-check/through/cycle guards/batch/lookup/membership/platform-admin-tuple), FS2 socket, `memstore/`, `storetest` with the **named adversarial sub-runners**; FS1 guard-list add | L | — | opus | **31** |
| Z2a | `02a-store-turso.md` | `stores/turso`: 0001 migrations (canonical set authored here), recursive-CTE expansion, boot probe, conformance + live leg | L | Z1 | opus | **32** |
| Z2b | `02b-store-pgx.md` | `stores/pgx`: identical version filename set, recursive-CTE expansion, (GIN divergence if Q4 = KEEP), conformance + live leg | M | Z2a | opus | **33** |
| Z3 | — (no file cut) | `groups` aggregate — **TRIM RECOMMENDED (Q1)**; disposition block below; `03-groups.md` is cut from design §2.5/§13 only if jrazmi overrides | M | Z1 | opus | 33 |
| Z4 | `04-consumer-proof.md` | Consumer seams + proof host: model declaration, toy-Granter → `CreateRelationships` swap, Check closure into events' `AuthorizeStream` (real signature, drift 4), **the two mandated demonstrations** (middle-posture clean-graph; `LookupResources` exercised), full real-interaction protocol | M–L | Z1 (hard), Z2 (default order), auth-v2 (shipped) | opus | 33 |
| Z5 | `05-docs-guards.md` | Docs + guards: feature README (**three-posture table first**; cms-gating boundary), wiring page, the Q3 store-glue guard decision (FS1 list add moved to Z1), registration artifacts, ARCHITECTURE/README/RELEASING counts, capability-map ReBAC rows, design status-header amendment, NOTES artifacts | S–M | all | fable | 33 |

Sequencing: Z1 first (everything stands on it). Z2a → Z2b (the filename
set is authored once). Z4 hard-depends only on Z1 + a shipped auth-v2 —
its proof runs zero-infra on `memstore/` (the events phase-4/5
independence precedent) — but default order keeps store conformance ahead
of the demo; Z4 may swap forward if a store phase blocks. Z5 last. Every
task boundary leaves all modules building and lands as a CI-green commit
before the next leg (events discipline; the repo is a git repo with CI).

**Z3 disposition (pending Q1; the struck-A8 row-kept precedent).** Scope
if kept: `logic/group` (name/slug aggregate + membership sugar over
tuples) + store tables in both dialects + storetest cases + a
`03-groups.md` phase file cut from design §2.5. The engine needs no
groups *table* — expansion is pure tuples
(`group:{id}#member@user:{x}`); Z4's demo as planned needs no named
groups. Recommendation: **TRIM** — return trigger: the first host/demo
that wants named-group UX (an admin surface listing "who's in
Engineering"), at which point it lands as a follow-on with its own
migration (0002+).

## Module / API impact

- **+3 modules, 30 → 33**: `features/authorization` (Z1),
  `features/authorization/stores/turso` (Z2a),
  `features/authorization/stores/pgx` (Z2b). Each: own `go.mod`,
  sibling-replace pattern, registered in `go.work` + Makefile `MODULES`
  in its phase; the stores also join `STORE_MODULES` (8 → 10) and gain
  `test-stores` legs (pgx plain, turso `-tags=integration`).
- **No sdk changes, no `Mount` changes, no new sdk port** (design §11
  non-goal 1; the graduation trigger — two features needing the identical
  authorize vocabulary — is recorded, not cashed).
- `features/authorization/go.mod` requires exactly `sdk` (FS1) at every
  task boundary.
- Public API born at Z1: the FS2 socket, the `logic/relationship` rim,
  the root-aliased engine vocabulary, `memstore`, `storetest`. Zero tags
  exist (RELEASING.md), so no version-bump obligation; RELEASING's module
  enumeration updates at Z5.
- `examples/auth-cms/go.mod` gains `features/authorization` (+ replace)
  at Z4 — see Q2 for the posture-demonstration consequence.

## Schema / datastore impact

- **New migration source `"authorization"`, 0001+** — collision-free next
  to `"cms"`/`"auth"`/`"jobs"`/`"events"` in a host's merged ledger
  (hosts must not renumber scaffolded files — the auth-v2 docs-phase
  language applies verbatim).
- **`rebac_relationships`** (salvage shape, `0002_rebac.sql` there):
  resource_type, resource_id, relation, subject_type, subject_id,
  subject_relation (the optional userset relation — `group#member`-style
  subjects); unique-tuple index; secondary indexes on resource, subject,
  and (resource_type, relation). Executor verifies columns/indexes
  against the original file and logs any divergence.
- **`rebac_relationship_metadata`** — pending Q4. If kept: JSON metadata
  keyed to a tuple; **pgx carries JSONB + a GIN index; turso a plain JSON
  TEXT column** — a documented index-capability divergence, same
  migration filenames (design §2.5).
- **Recursion pushed to the store** (design §2.5): `CheckRelationWith
  GroupExpansion` and `LookupDescendantResourceIDs` are **recursive CTEs**
  in both SQL stores and a Go graph walk in `memstore` — the one place
  the flagship could authorize differently per backend (design risk 3).
  The named adversarial sub-runners (Z1, run per-dialect in Z2a/Z2b) are
  the acceptance criteria, not nice-to-haves. CTE termination against
  cyclic data is itself asserted (the membership-cycle case).
- **`CountByResourceAndRelation` counts direct tuples only** — never
  expanded membership (design §2.5 pin; a count divergence is a
  **security divergence** — it feeds last-owner protection). The diamond-
  dedup storetest case carries the explicit Count assertion.
- **Boot-time probe** in both store constructors (drift 5).
- **No changes to any other feature's schema or the EAV spine.**

## Generated-artifact impact

None. This milestone has no HTML surface — no `.templ` sources are
touched. `make check`'s templ-drift gate still runs every phase; never
hand-edit `*_templ.go`.

## Loop protocol

Same as auth-v2/events-v1: one phase per leg; read this overview + the
phase file + **the design doc** fully; preconditions → tasks in order →
acceptance → real-interaction check → dated execution-log entry → commit
CI-green → stop. Surgical diffs; goimports; premise-false → closest
correct thing + log divergence; constitution/ratified-decision conflict →
STOP and flag.

**Standing real-interaction check (a) — every phase:** `make check` green
(the then-current module set + all seven guards — eight if Q3 adds the
store-glue guard at Z5), boot `examples/minimal` (:8081), `GET /` and
`GET /products/widget-3000` → 200s, kill, port free.

**Authorization-flow check (b) — Z4 (and any phase touching
`examples/auth-cms`):** the auth-cms cookie-jar flow with
`RequireVerifiedEmail=true` (register → verify code from the
console-mailer log → login), then the Z4 protocol legs
(`04-consumer-proof.md`). Report exact codes.

## Live-store gates (Z2a/Z2b)

Turso leg `-tags=integration` + `TURSO_*` — the ONLY authorized database
is `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
(**verify the env URL matches before ANY run**; the .env may point
elsewhere); pgx leg env-gated on `POSTGRES_TEST_DSN`
(docker, postgres:17). Loud skips mid-milestone are fine; milestone close
requires one recorded live conformance run per store — **covering every
named adversarial sub-runner** — as dated NOTES.md artifacts, never a
hermetic green. Live legs run manually/locally by the loop executor; the
playground token never enters CI logs.

## Goal

A host can run gopernicus in any of the three ratified postures — none,
host-authored closure, or the mounted `features/authorization` ReBAC
engine — and the flagship provably authorizes identically across
memstore, turso, and pgx, wired into real consumer seams
(invitations' `Granter`, events' `AuthorizeStream`) with zero feature→
feature imports.

## Definition of Done (milestone)

- `features/authorization` compiles standalone with `go.mod` = sdk only;
  `NewService` validates loudly (nil Relationships, nil/invalid Model);
  `Register` mounts nothing and touches `mount.Logger` only;
  `/authorization/*` claimed-unregistered.
- The five **named** adversarial sub-runners (membership cycle, ≥3-level
  nesting, diamond dedup **with the Count assertion**, nested userset,
  `LookupResult.Unrestricted`) green against memstore hermetically in
  `make check` AND against both dialect stores' live legs, recorded as
  dated NOTES.md artifacts per dialect.
- Both store modules: identical migration version filename sets, source
  `"authorization"`, boot-time probes, connector-helper conventions
  (drift 5), `Repositories(db)` + `ExportMigrations(dst)`.
- Z4's two mandated demonstrations recorded: (a) the middle posture —
  a host satisfying a Check seam with a plain ownership closure and **no
  ReBAC in its module graph** (`GOWORK=off go list -m all` output
  captured, clean — cut refinement 10);
  (b) a `LookupResources`-backed "list what this subject may view" call
  exercised over live HTTP. Plus the full protocol: invite → accept →
  tuple exists → Check allows → gated surface 200s, and denies without
  the tuple. Green tests alone close nothing user-facing.
- Rule 6 clean both directions (import-anchored):
  `grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/`
  → empty, and the reverses (each other feature grepped for
  `features/authorization`) → empty. G7 enforces this continuously.
- Guard work landed: `features/authorization` in the FS1 guard list
  (Z1, prove-can-fail); the Q3 store-glue guard added at Z5
  (prove-can-fail) or its conscious deferral recorded in NOTES + the
  feature README.
- Docs synced: feature README opening with the three-posture decision
  table, nil-semantics rows for every port (item 12), the wiring page,
  module count 33 everywhere, capability-map ReBAC rows BUILT, design
  status header amended, NOTES.md milestone entry with live artifacts.

## Out of scope (design §11, restated as cut lines)

- No `sdk/authorization` port; no `Config.Identity`-style consumer
  pairing (identity rides `sdk/identity`).
- No tenancy; no `PostfilterLoop` (§2.6 demand gate — and the recorded
  constraint: a future enumeration-shaped consumer seam must ship paired
  with it); no groups admin UI; no ReBAC caching decorator or `explain`
  as acceptance criteria (salvage-if-free).
- No routes, no HTML, no generated CRUD bridges; no new `Mount` fields.
- No changes to `features/authentication`, `features/events`, or their
  stores (Z4 touches host wiring only — rule 8).

## Risks (ordered)

1. **Silent divergence between memstore's Go graph walk and the stores'
   recursive CTEs** (design risk 3) — the flagship authorizing
   differently per backend is a security failure. Mitigation: the
   two-layer storetest (cut refinement 4) with the five named adversarial
   sub-runners as per-dialect acceptance; §2.5's direct-count pin
   asserted in the diamond case.
2. **Engine salvage mass in Z1** (~2,000 LOC non-test + a 2,650-line
   behavioral reference suite). Mitigation: the design's signatures are
   pre-verified against the original; tasks split rim/DSL/engine/socket;
   the sdk-parity bar (design ported, code re-typed fresh) with
   divergences logged per task.
3. **Recursive-CTE non-termination or dialect skew on cyclic data** —
   SQLite and PostgreSQL recursive CTEs differ in cycle behavior.
   Mitigation: the membership-cycle sub-runner runs live per dialect;
   Z2a/Z2b tasks name cycle-safety (bounded recursion / UNION dedup) as
   an implementation requirement, not an afterthought.
4. **Proof-host posture entanglement (Q2)** — wiring the flagship into
   `examples/auth-cms` puts ReBAC in the only events-mounting host's
   graph, so the middle-posture demonstration needs the two-commit
   protocol (or a new example). Mitigation: Q2 decides it at
   ratification; both options are fully specified in `04-consumer-proof.md`.

## Open questions — FOR RATIFICATION (jrazmi)

1. **Q1 — Z3 groups trim.** Recommend **TRIM** (no `03-groups.md` cut;
   disposition block above; return trigger = first named-group UX
   demand). The design names Z3 "a trim candidate at plan cut: build only
   if Z4's demo wants named groups" — Z4 as planned does not.
2. **Q2 — Z4 host shape.** Recommend **Option A: extend
   `examples/auth-cms` with the flagship, and record the middle-posture
   demonstration as a two-commit protocol** — commit 1 wires a plain
   ownership closure into `events.Config.Authorize` with NO
   `features/authorization` anywhere in the graph (`GOWORK=off go list
   -m all` captured clean — cut refinement 10, protocol driven live,
   recorded); commit 2 lands the
   flagship swap (toy Granter → `CreateRelationships`, Check closure,
   `LookupResources` route). Cost: zero new modules (33 stands); the
   middle-posture artifact is a recorded protocol + a permanent git
   commit rather than a living host. **Option B:** a new example host
   (module 34) as the permanent living middle-posture artifact — rejected
   as default because a flagship host needs invitations
   (= `features/authentication` = duplicating the substantial `authmem`
   in-memory store), a cost the design never priced. Note: the design
   itself directs the swap ("authorization-v1's proof host later swaps in
   `authorizer.CreateRelationships`"), consciously retiring auth-cms's
   living AV4 demonstration into git history — Option A follows that.
3. **Q3 — the store-module-glue guard (design §10).** Recommend **ADD**
   at Z5: `guard-store-no-foreign-feature` — every
   `features/<x>/stores/*` subtree imports no `features/<y>` (y ≠ x),
   prove-can-fail; per the review-gate fold (steward minor 6) the
   pattern gains one extra alternation so it also catches
   store→`examples/` imports (currently unguarded by anything) — or the
   skip is consciously named. There is no legitimate future exception: even the
   AV10-deferred appender seam is consumer-declared and never imports
   `features/events` (its acceptance grep says exactly that). Cheap
   (~10 Makefile lines mirroring G7 over the stores subtrees G7
   excludes). Defer-consequence if declined: the deferred rail's
   acceptance grep remains the only enforcement, named in NOTES + README.
4. **Q4 — the `rebac_relationship_metadata` table.** Recommend **TRIM
   from 0001** (amends ratified §2.5, hence a jrazmi call): the engine's
   `Storer` never touches it (verified against the original — its
   consumer was the invitation-bookkeeping AV4 deleted), so v1 would
   scaffold a dead table + a port-less GIN index into every adopting
   host. Return trigger: the first metadata consumer; it lands as 0002
   with the pgx-GIN/turso-TEXT divergence exactly as §2.5 documents. If
   jrazmi keeps it: 0001 carries both tables, Z1 salvages the original's
   metadata repository surface onto the `Storer` (or a second
   `Repositories` field — executor pins against the original), and
   storetest gains a metadata round-trip case.

## Consultation notes

No lead consulted while cutting this draft — deliberately, the events-v1
precedent: the design of record already carries a pre-write
`lead-backend-engineer` review (its Consultation notes: own-module
rationale, Check-only seam, memstore conformance honesty all confirmed
there), and the mandated plan-cut gate below re-runs the tier-review
question on THIS text. The load-bearing verifications a consult would
have covered were done directly against code and salvage (the seven drift
items + the 14-method `Storer` finding + the metadata dead-table finding).

### Review-gate fold (2026-07-08)

Both mandated review passes returned and their consolidated findings are
folded in place (this section is the record; status stays DRAFT —
jrazmi ratification still owed):

- **architecture-steward: ALIGNED-WITH-EDITS. lead-backend-engineer:
  SHIP-WITH-EDITS.**
- **Majors adopted (both reviewers / salvage-verified):**
  1. **`Config.PlatformAdmin` deleted — the draft invented it.** The
     original's Config is `{MaxTraversalDepth int}` only
     (`authorizer.go:16`); platform-admin is the data tuple
     `platform:main#admin@<type>:<id>` via `CheckRelationExists`
     (`authorizer.go:244`), requiring a `platform` resource type in the
     schema. Re-specced in cut refinement 6, 01-core tasks 3/4/6, Z4
     protocol step 8, Z5's nil-semantics table. A config-level bypass
     would amend ratified §2.5 — not this plan's call.
  2. **Clean-graph proofs made workspace-independent:**
     `GOWORK=off go list -m all` everywhere (cut refinement 10) — the
     plain form under go.work lists every workspace module and would
     false-fail commit 1 the moment Z1 registers the module.
  3. **Storer method count corrected 13 → 14** (model.go:246; the
     task-1 method list was already complete — the label was the error).
- **Steward minors adopted:** (4) FS1 guard-list add moved Z5 → Z1
  task-1 with its own prove-can-fail (the store phases were an unguarded
  window); (5) store constructor pinned
  `Repositories(db) (authorization.Repositories, error)` with the boot
  probe inside (jobs turso.go:29 precedent; the "or New(db)" hedge
  dropped) — cut refinement 11; (6) Q3's guard spec gains the
  store→`examples/` alternation (or a consciously named skip).
- **Lead refinements adopted:** (7) root aliases gain `Resource`
  (Z4 constructs `authorization.Resource{…}`), plus an executor check
  that CheckBatch/FilterAuthorized argument types need no further
  aliases; (8) `MaxTraversalDepth` named as the SHARED bound memstore
  and both SQL CTEs honor identically, with a storetest depth-boundary
  case (chain at the bound and at bound+1) in the DeepNesting family — a
  bound skew is a per-backend security divergence the ≥3-level case
  cannot detect; (9) `checkSelf` (self-grant on read/update/delete when
  subject == resource for user/service_account types, authorizer.go:~250)
  explicitly in Z1 task-3's salvage scope, with storetest fixtures and
  Z4's model accounting for it; (10) Z4's Granter swap wording fixed —
  `auth.Granter` is a one-method interface
  (`features/authentication/authentication.go:103`), so the host adds a
  small host-local adapter type in membership.go; (11) Z5's ARCHITECTURE
  tree edit also sweeps the stale `auth/` directory label (~line 27,
  pre-existing A-R1 staleness).
- **Endorsements recorded:** steward endorses Q2 Option A
  (boundary-clean, GOWORK-fixed), Q4 TRIM (the schema-ownership
  argument: under scaffold-and-own a dead table becomes permanent
  host-owned schema; KEEP would widen the `Storer` with consumer-less
  methods), and Q3 ADD. The lead verified the D2–D6 helper citations,
  the `AuthorizeStream`/`identity.Principal` signatures
  character-for-character, and G7's auto-coverage of the new feature.

## Recommended reviews (the plan-cut gate — run before jrazmi ratifies)

- **architecture-steward + lead-backend-engineer** — the tier-review
  question verbatim: "is any piece in the wrong tier, and is the host
  wiring tour acceptable?" Plus: the cut refinements (FS2 naming, the
  two-layer storetest, rim/engine type placement), and Q2's two-commit
  posture protocol.
- **data-integration-reviewer** — recursive-CTE parity + cycle safety
  per dialect, the named sub-runners' coverage vs the port docs, the
  direct-count pin, migration shape vs salvage, Q4.
- **platform-sre** — migration phasing (new source 0001+), live-leg
  gating + playground discipline, guard coverage (Q3), module
  registration hygiene (go.work/MODULES/STORE_MODULES/test-stores).
- **product-manager** — scope: Q1 (groups trim), Q2 (host shape vs
  module count), whether Z4's demo keeps the three postures legible to a
  host developer.

## Execution log

(planning-leg and cross-phase entries here; per-phase logs in each file)

### 2026-07-08 — planning leg: milestone cut (DRAFT)

Cut `00-overview.md` + phases Z1, Z2a, Z2b, Z4, Z5 from the ratified
design's §13 Z-table (Z3 not cut — trim recommended, Q1; the struck-A8
row-kept precedent). No code touched; planning-only leg. Drift items 1–7
absorbed per the cutting brief; two additional staleness findings logged
above (the 14-method `Storer`, the consumer-less metadata table → Q4).
Cut-time refinements 1–9 recorded. Next: the plan-cut review gate, then
jrazmi ratification (Q1–Q4), then leg 1 = Z1 (`01-core.md`, opus).

### 2026-07-08 — review-gate fold applied

architecture-steward (aligned-with-edits) + lead-backend-engineer
(ship-with-edits) findings folded across all five files — see the
"Review-gate fold (2026-07-08)" consultation-notes section for the
itemized record (3 majors: no `Config.PlatformAdmin` — data-tuple
platform-admin + `MaxTraversalDepth`; `GOWORK=off` graph captures;
14-method count. 3 steward minors, 5 lead refinements, endorsements on
Q2-A/Q3-ADD/Q4-TRIM). Cut refinements now 1–11. Status unchanged: DRAFT,
awaiting jrazmi ratification of Q1–Q4.
