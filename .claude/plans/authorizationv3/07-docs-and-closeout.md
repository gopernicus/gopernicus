# Phases 4 and 5 — proof host, then documentation and closeout

File/phase map: file 07 holds phases 4 and 5; files 05 and 06 are deferred
follow-up packets (effects; generic admin), not phases of this milestone.

Status: DRAFT; phase 4 is ready after phases 0–3; phase 5 is ready only after
the phase-4 proof gate passes.
Depends on: every v3 correctness-kernel implementation phase and its execution
log. Deferred effects/admin packets are not prerequisites.

## Phase 4 — proof host

### Goal

Prove guarded and `SystemMutator` host composition and the exact-semantics/
concurrency behavior of the finished kernel through the auth-cms proof host.

Gate rule: a defect found during proof reopens the owning implementation phase
(0–3), never closeout; phase 5 does not begin until the phase-4 proof gate
passes.

### Task AZ3-4.1 — auth-cms guarded and SystemMutator composition

Touch: auth-cms composition root/demo/tests only, plus its self-contained
`go.mod` when store dependencies change.

Implement/prove:

- adapt `identity.Principal` directly to authorization `Actor`/
  `PrincipalRef` at the host;
- compose a host `MutationGuard` from schema-declared `manage_access` and the
  separately composed platform-admin recipe, using only the supplied
  dependency-tracking decision view;
- prove invitation acceptance runs through the deliberately held `SystemMutator`
  with its stable MutationID derived from the invitation operation. The
  API/compile-site transition itself is owned by AZ3-3.4; this task proves the
  composed host behavior only;
- remove or disable the current session-only role/relationship mutation demo
  endpoints. Do not claim recent-auth protection until authentication exports a
  public sensitive-operation protector. Be honest about the consequence:
  examples/auth-cms intentionally loses its browser-driven role-assignment
  surface until the deferred AZADM packet lands — the proof here is host code
  plus tests, not a browser flow;
- keep the no-authorization and host-authored-closure postures demonstrable; and
- prove ordinary host code has no raw write method or constructible system-actor
  synonym.

Verify:

```sh
cd examples/auth-cms && go test -race ./... -run 'Authorization|Guard|SystemMutator|Invitation|Posture'
make guard
```

Acceptance: untrusted service calls cannot self-grant; invitation acceptance is
visibly trusted and idempotent; no shipped HTTP route mutates authorization with
session presence alone.

### Task AZ3-4.2 — exact-semantics and concurrency proof protocol

Depends on: AZ3-4.1.

Drive and record through service/host composition tests:

1. ordinary member cannot self-grant;
2. authorized manager grants an exact `group#member` subject and a concrete
   member gains access;
3. a `group#admin` grant does not authorize an ordinary member;
4. decision APIs reject userset-valued callers;
5. a navigational Through relation rejects userset targets at schema compile;
6. non-self Through-root hierarchy Check and Lookup return the same descendants;
7. global role appears in effective role enumeration;
8. scoped revoke while global remains reports `same_role_grant_remains`;
9. two concurrent last-owner revokes produce one success/one invariant block;
10. stale revision and MutationID payload mismatch return stable errors;
11. exact retry returns the original outcome with `Replayed=true`; and
12. resource teardown is possible only through the separately held
    `SystemMutator` command with a recorded reason.

Acceptance: the transcript includes command/results, receipts/revisions, stored
rows, and audit observations without secrets. It claims no effects delivery or
generic admin API.

### Phase 4 acceptance

- Guarded and `SystemMutator` composition is proven in auth-cms host code and
  tests, and the exact-semantics/concurrency transcript exists.
- No defect found during proof remains open: each one reopened and closed its
  owning implementation phase (0–3), never closeout.
- `make check` and `make guard` pass.

## Phase 5 — documentation and closeout

### Goal

Turn the proven implementation into an adoptable live-proven milestone and run
one post-implementation review/remediation gate. Phase 5 does not begin until
the phase-4 proof gate passes.

### Task AZ3-5.1 — final migration parity and execute upgrade runbook

Implement:

- compare pgx/turso migration filename sets and schema inventories;
- run the upgrade audit against a populated v1 fixture containing direct groups,
  valid usersets, deliberately ambiguous usersets, relationships, global/scoped
  roles, and last owners;
- prove ambiguous rows stop the upgrade until explicitly repaired;
- apply the adopter path, boot v3, compare expected access, then exercise rollback
  only to the documented boundary; and
- publish the runbook in the canonical release/feature documentation.

Acceptance: no upgrade step relies on resetting a real adopter database or
silently interpreting missing relation as `member`.

### Task AZ3-5.2 — public README/API/migration documentation

Document:

- three adoption postures and independent kinds;
- exact userset semantics with member/admin examples;
- immutable schema compilation/digest and all validation failures;
- evaluation limits, indeterminate errors, Check/Lookup parity, and fail-closed
  caller guidance;
- actor/guard/SystemMutator APIs, dependency revision validation, MutationID,
  receipt retention, outcomes versus replay, last-owner/teardown invariants,
  and legacy migration;
- the constructor-shape amendment in `features/README.md`: the
  `Components{Service, SystemMutator}` bundle versus FS2's
  `svc, err := NewService(repos, cfg)`, stating whether the bundle is the new
  sanctioned shape or an authorization-only exception (that determination
  happens at ratification/execution, not here);
- raw versus effective role listing;
- migration source/order and live-store commands; and
- explicit non-goals: ABAC policy kind, role implication, cache, templ UI; and
- deferred follow-ups: effects and generic admin, including the missing public
  authentication sensitive-operation prerequisite.

Acceptance: a host can wire safely without reading internal code or this plan.

### Task AZ3-5.3 — release and compatibility inventory

Record:

- breaking public API/port/schema changes and next tag floor, using the
  per-module tag floors now recorded in `RELEASING.md` as the vocabulary;
- consumer changes in auth invitations, events authorization closure, auth-cms,
  and external host recipes;
- canonical migration rewrite versus append-only decision at actual preflight;
- the settled jobs/work dependency: `sdk/capabilities/work` is a new first-tag
  module and `features/jobs` carries a MINOR floor from the delivery refactor —
  record what authorization adds on top, if anything;
- the sdk graduation decision: re-run the three-gate sdk graduation protocol for
  the authorization check/decision vocabulary — this milestone fires the
  ARCHITECTURE protocol-table trigger ("authorizationv3 settles its semantics")
  — and record graduate/re-defer with reasons;
- every accepted premise adaptation and deferred item; and
- module/go.work/Makefile/guard changes: commit to a sixteenth layering guard,
  `guard-authorization-no-delivery-repo` — the `guard-auth-no-delivery-repo`
  shape pointed at `features/authorization` migrations/repositories, proving no
  authorization-specific jobs/delivery table exists — numbered consistently
  with the existing fifteen.

Acceptance: release notes distinguish semantic access changes from source-only
renames.

### Task AZ3-5.4 — final adversarial and race audit

Audit tests/code for:

- any ignored `Subject.Relation`/`subject_relation` or hard-coded `member`;
- mutable schema references;
- read/count/write and delete/create mutation sequences;
- decision/lookup divergence and partial list success;
- unbounded recursion/fan-out/batches;
- missing actor/guard; `SystemMutator` unreachability from ordinary HTTP code
  must be covered by a named permanent regression test, not a one-time audit
  check;
- MutationID replay/payload mismatch and revision gaps;
- last-owner race and effective global role fallback;
- dependency scopes locked without canonical order or revisions left
  unvalidated;
- effects/admin code accidentally pulled into the v3 kernel;
- unsafe logs/metrics and unbounded IDs as metric labels;
- a shipped session-only authorization mutation route; and
- goroutines started by authorization Register.

Use `go test -race` for core/memory/concurrency and repeated live dialect races.
Every finding is fixed, rejected with evidence, or explicitly safely deferred.

### Task AZ3-5.5 — implementation-complete hermetic/live gate

Run and record:

```sh
make check
make guard
cd features/authorization && go test -race ./...
```

Plus full pgx and Turso live conformance, repeated concurrent mutation tests,
upgrade protocol, and the guarded/SystemMutator proof-host protocol. Live skips
do not close the task. Live legs follow the auth v3 recipe:
`authv3-pg` (C-collation) and `authv3-libsql` containers, fresh/reset databases,
explicit `-count=1`, and harness margins that let concurrent attempts reach
terminal results before database teardown under `-race` slowdown.

### Task AZ3-5.6 — post-implementation reviewer gate

Depends on: AZ3-5.5.

Run one review wave over the completed implementation — the auth v3 shape (one
post-implementation wave, then owner-gated remediation in AZ3-5.7) — focusing on:

- ReBAC/userset semantics and graph correctness;
- database atomicity/isolation and migration safety;
- guarded/system capability and host identity boundaries;
- effects/admin deferral boundaries;
- public API compatibility and documentation; and
- proof-host realism.

Consolidate findings by root cause and severity. No PR is opened yet.

### Task AZ3-5.7 — accepted remediation, reverification, and PR-ready handoff

Depends on: AZ3-5.6.

Apply accepted findings without weakening the packet's invariants. Add regression
tests, rerun AZ3-5.5 gates, update runbook/docs/release inventory, and produce the
final accepted/rejected/deferred finding table plus exact verification evidence.

Acceptance: no required work or accepted finding remains.

### Phase 5 acceptance

- Migration/runbook, docs, release inventory, adversarial audit, and guarded
  proof-host evidence are complete.
- Hermetic, race, dual-store live, upgrade, and host-composition gates pass.
- Review remediation is reverified.
- Authorization v3 is ready for a separately authorized PR/tag workflow.

## Stop conditions (both phases)

- A live database leg is unavailable or failing.
- Upgrade ambiguity remains unresolved.
- A reviewer finds a potential overgrant, double mutation, system-capability
  leak, or shipped session-only mutation path not covered by a regression test.
- Effects or generic admin work is pulled into this milestone without a
  separately ratified contract and prerequisites.

## Execution log

Append only during execution; tag each entry with its phase and task ID.

### 2026-07-14 — Phase 4 / AZ3-4.1 — auth-cms guarded and SystemMutator composition — PASS (real boot-and-drive verified twice)

Outcome: auth-cms composes a real host MutationGuard and runs the guarded
actor-mutation posture; ALL session-only authorization-mutation HTTP routes
removed; invitation acceptance proven trusted + idempotent; guardian posture
upgraded from the AZ3-3.4 empty policy to the ratified owner minimum. Touch
confined to examples/auth-cms. NO REOPEN findings — phases 0–3 code supported
the composition as-is.

- New cmd/server/guard.go: `hostMutationGuard` — (1) platform-admin
  short-circuit via view.CheckRelation(platform:main#admin) composed IN the
  host guard; (2) global (ScopeSubject) mutations refused (trusted-only
  blast radius); (3) else manage relation (`owner`, backing the schema's new
  `manage_access` permission) on the mutated resource scope. ONLY the
  dependency-tracking DecisionView is read — both tuples become
  revision-tracked dependencies re-validated under the repository lock.
  `hostActor` adapts identity.Principal → Actor at the boundary. Noted:
  DecisionView exposes relation/role reads, not permission evaluation, so
  the guard reads manage_access's backing relation directly.
- New cmd/server/authorization.go: authzSchema (adds manage_access =
  AnyOf(Direct(owner)) to project), authzGuardianPolicy (owner/min-1
  NARROWED to project — platform is a flat admin-list type with no owner
  relation; a global minimum would invariant-block the platform-admin tuple;
  the sanctioned host-narrowing path, documented), newAuthorization
  (Config.Guard wired), seedAuthorization (boot-seeds project:demo#owner
  FIRST, then platform:main#admin, via SystemMutator + DeriveMutationID —
  resolves the AZ3-3.4 empty-policy flag to the honest default posture).
- Demo endpoints REMOVED (were SystemMutator writes behind session-only
  routes — the forbidden shipped session-only mutation path): POST
  /demo/roles/assign, /demo/roles/unassign, /demo/admin/bootstrap → 404.
  Read routes retained. Browser role-assignment surface honestly deferred to
  AZADM (code comments + README; stale curl walkthroughs rewritten).
- Host tests (9, all -race): TestAuthorizationCompositionGuardedPosture,
  TestHostMutationGuardManageAccessAllowsAndDenies,
  TestGuardedActorCannotSelfGrant,
  TestHostMutationGuardPlatformAdminShortCircuit,
  TestHostMutationGuardGlobalMutationTrustedOnly,
  TestInvitationAcceptanceTrustedAndIdempotent,
  TestHostSystemMutatorHeldApartFromService,
  TestHostAuthorizationHasNoRawWriteOrSystemActor (reflection),
  TestAuthorizationPosturesDemonstrable (three postures).
- Real interaction — agent drove PORT=8099; ORCHESTRATOR INDEPENDENTLY
  re-drove PORT=8098 with a fresh binary: healthz 200, / 200 (public read),
  /demo/whoami 401 (read route gated), POST /demo/roles/assign 404, POST
  /demo/admin/bootstrap 404, process killed, port released, no boot errors.
- Verify — agent ran all; orchestrator re-ran: auth-cms build/vet + full
  `-race -count=1` green (7 pkgs); 5 keystone host tests fresh PASS;
  `make check` "all checks passed"; guard exit 0. No go.mod change needed.
- Premise adaptations: guardian narrowing (above); actor boundary proven by
  tests not browser flow (AZADM-deferred surface — the task's own language);
  README walkthrough rewrites within touch scope, deeper doc rework →
  AZ3-5.2.

### 2026-07-14 — Phase 4 / AZ3-4.2 — exact-semantics and concurrency proof protocol — PASS (phase 4 gate CLOSED)

Outcome: all twelve protocol points driven and RECORDED over the auth-cms
host composition (AZ3-4.1 bundle + hostMutationGuard + separately held
SystemMutator) with a capturing AuditSink. Durable artifact checked in:
examples/auth-cms/cmd/server/testdata/az3-proof-transcript.md (12 sections,
real observed values — commands, receipt outcome/revision/replayed/digest,
stored-row checks, audit lines; explicitly claims NO effects delivery and NO
generic admin API; no secrets — orchestrator secret-scanned). NO REOPEN
findings: every point demonstrable against frozen phase-0–3 code.

- Point highlights: (1) self-grant ErrForbidden before Apply, no row, audit
  denied. (2) exact group#member grant → member true/stranger false, digest
  match. (3) group#admin grant does NOT authorize a member. (4) userset
  callers structurally impossible + driven denial. (5) userset-Through
  rejected at compile (sdk.ErrInvalidInput). (6) non-self Through-root
  Check/Lookup parity [leaf mid root]. (7) global role in effective
  enumeration provenance=global. (8) scoped unassign
  same_role_grant_remains=true, HasRole still true. (9) 16 rounds × two
  concurrent last-owner revokes: applied=16/invariant_blocked=16, one owner
  remains each round. (10) ErrStaleRevision + ErrMutationMismatch stable;
  audit failed/accepted/failed with reasons. (11) exact retry
  replayed=true, same revision, no bump. (12) teardown only on
  SystemMutator (reflection), empty reason rejected, applied teardown zeroes
  rows, audit carries the recorded reason in Detail.
- Test: TestAZ3ProofProtocol (12 subtests, -race). Capturing sink via
  Config.Audit in proofComposition (mirrors newAuthorization; setup seeded
  via SystemMutator so the sink captures exactly the act under test).
- Premise adaptations: points 2/3/4/6 need userset/Through schema shapes the
  live host schema lacks — run over the SAME composition pattern
  (hostMutationGuard + guardian-policy'd bundle) with proof schemas adding
  group/doc + org/space types (task-sanctioned); points 1/7/8/9/10/11/12 on
  the live authzSchema(). Deterministic DeriveMutationID for a reproducible
  artifact (stated in the transcript; production actor callers mint
  NewMutationID; possession is never authority).
- Verify — agent ran all; orchestrator re-ran: 12/12 subtests PASS fresh
  (-race -count=1); transcript 12 sections confirmed, secret-scan clean;
  auth-cms build/vet + full `-race -count=1` green (7 pkgs); `make check`
  "all checks passed"; guard exit 0.

### Phase 4 acceptance — 2026-07-14 — GREEN (gate closed)

Guarded and SystemMutator composition proven in auth-cms host code and tests
(AZ3-4.1, boot-and-drive verified twice). Exact-semantics/concurrency
transcript exists and is checked in (AZ3-4.2). No defect found during proof —
zero phase 0–3 reopens. `make check` + `make guard` pass (orchestrator
re-ran). Phase 5 may begin.

### 2026-07-14 — Phase 5 / AZ3-5.1 — final migration parity and execute upgrade runbook — PASS (executed live)

Outcome: the v1→v3 runbook EXECUTED end-to-end against a populated v1 fixture
on live PostgreSQL (C-collation scratch) + libSQL/sqlite3; v3 Service booted
over the converted pgx store; gain/lose/retain verdicts, last-owner
invariant, and rollback boundary all verified. UPGRADE.md flipped
DRAFT → EXECUTED/VALIDATED with an evidence section; linked from
features/authorization/README.md (new "UPGRADE NOTE — v1 → v3" section).
Repeatable env-gated live test: stores/pgx/upgrade_runbook_test.go
(TestUpgradeRunbook — fixture build → detection → blocked-until-repaired →
repair → conversion + anchor seeding → v3 boot → access comparison →
rollback boundary).

- v1 baseline: reconstructed verbatim from git d11c7a2 (authorization-v1) —
  same columns, no ck_* constraints, no iam_scopes/iam_mutations, v1
  silent-conflict unique index.
- Fixture categories → verdicts proven at boot: RETAIN concrete ✓; RETAIN
  #member ✓; LOSE concrete-group (v1 member-expansion emulation confirmed v1
  reached it; v3 does not) ✓; LOSE non-member userset (while exact #admin
  holder RETAINS) ✓; global role HasRole ✓; last-owner revoke →
  OutcomeInvariantBlocked ✓. Detection 1a–1e found exactly the planted
  malformed/ambiguous rows; repairs deleted only structurally-invalid rows;
  meaning-changing rows left as stored — nothing defaulted to member.
- Blocking proven: with malformed rows present the constraint-add FAILED on
  both dialects; after repair, re-detection clean and the add succeeded.
- Rollback boundary: constraint rejects v1-style malformed reintroduction
  (repairs not hand-reversible → restore-from-backup); first committed v3
  mutation persists a receipt + advances an anchor past seeded 0 — the
  documented past-the-line desync point for a resumed v1 binary.
- DEFECT FOUND + CORRECTED (logged as AZ3-2.6 addendum in
  03-atomic-stores.md): original §7 text claimed 0001/0002 add the v3
  constraints to existing tables — false (CREATE TABLE IF NOT EXISTS
  no-ops). §7a now uses explicit ALTER TABLE ADD CONSTRAINT (pg) /
  table-rebuild (libSQL). Canonical migration SQL unchanged (correct for
  greenfield). Not a phase 0–3 code reopen — a phase-2 documentation
  deliverable corrected under AZ3-5.1's publish mandate.
- Parity re-verified: inventory/parity/constraint tests green both dialects;
  live pgx TestSchemaProbe green; zero authorization tags re-confirmed.
- Verify — agent ran all; orchestrator re-ran: TestUpgradeRunbook PASS live
  on its own scratch DB (-race -count=1); pgx + turso Migration|Schema|Probe
  green; UPGRADE.md status flip + 8 ALTER TABLE statements + README link
  confirmed; `make check` "all checks passed"; guard exit 0; scratch
  resources dropped; containers untouched.
- Premise adaptations: pgx-only Go boot (turso dialect validated at SQL
  level via sqlite3 — the AZ3-2.6 precedent, avoiding container conformance
  disturbance); TestUpgradeRunbook owns+drops iam_* tables within the given
  DSN (self-contained, re-runnable). CONVERSION.md's own "DRAFT" label left
  for AZ3-5.2's doc sweep (queries fully validated).

### 2026-07-14 — Phase 5 / AZ3-5.2 — public README/API/migration documentation — PASS

Outcome: documentation-only. features/authorization/README.md fully rewritten
to the landed v3 reality (the v1-vintage README documented removed raw
methods and the old Subject vocabulary — doc drift, not code defects; NO
REOPEN findings). features/README.md gained the constructor-shape amendment.
CONVERSION.md label flipped DRAFT → VALIDATED with the AZ3-5.1 evidence link.

- README covers all task bullets: three postures; independent kinds with the
  guarded-only v3 Service surface; exact userset semantics with
  member/admin/concrete-group examples + concrete-principal boundary;
  immutable compiled schema/digest + full validation-failure list;
  EvaluationLimits table w/ defaults, ErrEvaluationLimit→503, Check/Lookup
  parity, fail-closed guidance; full mutation lifecycle (Components matrix,
  Actor/MutationGuard/DecisionView, SystemMutator surface, MutationID
  vocabulary, dependency-revision validation, outcomes vs Replayed, receipt
  retention, guardian/teardown, legacy migration, audit); raw vs effective
  role listing + SameRoleGrantRemains; GuardianPolicy store-construction
  seam; store parity + live-store commands; auth-cms proof host + transcript
  link; non-goals; deferred follow-ups incl. the missing authentication
  sensitive-operation prerequisite; UPGRADE NOTE.
- Constructor RULING documented in features/README.md: the Components bundle
  is an AUTHORIZATION-SPECIFIC amended shape (sanctioned variant where a
  feature holds a separately-held trusted capability that must be
  structurally partitioned), NOT a general FS2 replacement. Repo evidence
  surveyed: cms/authentication/events/jobs all return bare *Service; no
  contradicting evidence. Future-feature rule stated.
- Verify — agent ran + orchestrator spot-checked: 35 Components/
  SystemMutator/GuardianPolicy citations in the README, FS2 amendment at
  features/README.md:42, CONVERSION.md header VALIDATED, PrincipalFrom
  wiring examples present; every cited identifier grep-verified by the
  agent; `make check` "all checks passed"; guard exit 0; module -race suite
  green.
- Flags: stores READMEs/memstore doc still describe CONVERSION.md as a
  "draft" descriptor (marginally stale after the label flip) — one-word
  cleanups left for AZ3-5.7 remediation or reviewer discretion, not silently
  changed (outside sanctioned scope).

### 2026-07-14 — Phase 5 / AZ3-5.3 — release and compatibility inventory — PASS (16th guard live)

Outcome: inventory recorded per the auth-v3 convention; the 16th layering
guard is the only code-adjacent change; sdk graduation decision = RE-DEFER
(recorded, no code moved); NO never-logged breaking change found.

- Inventory locations: RELEASING.md new keyed note "features/authorization +
  both store modules — next tag: authorization v3 correctness kernel
  (BREAKING; FIRST tags)" (semantic-vs-source-only taxonomy, greenfield
  decision, jobs/work adds-nothing axis, per-module tag table, consumer
  changes, graduation decision); NOTES.md dated entry with the consolidated
  premise-adaptation/deferred-item table (~40 adaptations swept).
- Tag floors: all three authorization modules FIRST tags, breaking-vintage;
  the never-cut middleware-consolidation minor floor absorbed; auth-cms
  never tagged; greenfield rewrite justified by zero tags at actual
  preflight (re-confirmed).
- Consumers: invitation Granter (done, cited); features/events needs NO
  change (AuthorizeStream is a host closure); auth-cms fully migrated;
  external recipe documented in the feature README.
- sdk graduation RE-DEFER per-gate: sdk/README admission FAILS plurality
  (one honest implementation; closures aren't implementations);
  ARCHITECTURE five-point FAILS points 1+3 (no multiple adapters;
  storetest feature-coupled); features/README §5 FAILS criteria 1+2 (no
  separate-module consumer; not canonical). Semantics settled — recommended
  protocol-table row reason update left to the OWNER (ARCHITECTURE.md
  untouched). YOUR CALL at review.
- G16: `guard-authorization-no-delivery-repo` — greps delivery/job-table
  shapes + bespoke deliveryjob package under features/authorization (zero-
  hit); .PHONY + aggregate + comment updated. No go.work/MODULES changes.
- Verify — agent ran; orchestrator re-ran: `make guard` 16 guard lines exit
  0 (G16 last, green); RELEASING.md:367 + NOTES.md:2401 confirmed;
  `make check` "all checks passed".

### 2026-07-14 — Phase 5 / AZ3-5.4 — final adversarial and race audit — PASS (no reopens)

Outcome: all 13 checklist items dispositioned (audit table in NOTES.md under
the AZ3-5.3 entry); NO overgrant, double mutation, system-capability leak, or
shipped session-only mutation path; no stop condition; no REOPEN. Live
confirmation legs re-run per dialect at -count=2 (agent + orchestrator).

- 13-item summary: items 1–5, 7–11, 13 SAFE with grep/test evidence (exact
  relation-matching CTEs; deep snapshots; single-tx atomicity; parity;
  budget; replay/mismatch/gap tests; guardian race; canonical lock order;
  sdk-only go.mod + G16; no metrics, bounded log fields; no Register
  goroutines). Item 6 FIXED: existing reflection test covered only the
  Service surface — NEW named permanent regression test
  TestNoSessionOnlyAuthorizationMutationRoute (auth-cms
  route_no_mutation_test.go) drives registerDemoRoutes over httptest,
  asserts retired mutation paths 404 + retained read route 405. Item 12
  SAFE and now permanently pinned by that test.
- Deferral dispositions: (a) orphaned internal engine raw methods —
  REJECT-WITH-EVIDENCE, keep: internal/, structurally unreachable
  (reflection-pinned); removal cascades into the public
  `authorization.Config.IDs` field (its sole live consumer) — a breaking
  change not in the AZ3-5.3 inventory; REOPEN-grade, OWNER-GATED (YOUR
  CALL at review). (b) demo.go raw role listing — REJECT-WITH-EVIDENCE,
  keep: deliberate README-narrated raw-vs-effective teaching contrast,
  read-only; AZ3-5.7/owner call. (c) none further.
- Verify — agent ran all; orchestrator re-ran: new regression test PASS
  fresh; module `-race -count=1` green (7 pkgs); NOTES.md audit record
  confirmed (line 2509); `make check` "all checks passed"; 16 guards green;
  LIVE pgx `-count=2` Concurrent filter PASS (fresh C-collation DB,
  dropped); LIVE turso `-count=2` PASS. Containers left running.

### 2026-07-14 — Phase 5 / AZ3-5.5 — implementation-complete hermetic/live gate — PASS (IMPLEMENTATION COMPLETE)

Gate run by the verifier agent (fix-nothing protocol), then key legs re-run
independently by the orchestrator. ZERO failures, ZERO unexplained skips,
ZERO retries. Authorization v3's correctness kernel (AZ3-0.1 … AZ3-5.5, 23
tasks, 5 phases) is implementation-complete.

CONSOLIDATED HERMETIC EVIDENCE
- `make check` "all checks passed" (all MODULES vet/build/test, templ drift,
  integration-tag vet, 16 guards) — 23.3s.
- `make guard` standalone: 16/16 guards incl. the new
  guard-authorization-no-delivery-repo — exit 0.
- features/authorization `go test -race -count=1 ./...` — 7 packages ok.
- stores/pgx + stores/turso hermetic build/vet(+integration vet)/test — ok.
- examples/auth-cms build/vet/`go test -race -count=1 ./...` — 7 packages ok.

CONSOLIDATED LIVE EVIDENCE (executed-not-skipped confirmed with -v; 0 SKIP)
- pgx full conformance on fresh C-collation DB: 10 top-level tests PASS —
  TestConformance (71 subtest PASS lines: Relationship, Adversarial, Budget,
  Parity, Roles incl. effective family, Mutations = 23 direct subtests),
  TestMigration{Inventory,Parity,ConstraintParity},
  TestMutationAbsentAnchorNoPhantom, TestMutationGuardPanicRollsBack,
  TestMutationStormForensics, TestExportMigrations, TestSchemaProbe,
  TestUpgradeRunbook — 20.3s. Orchestrator re-ran full package on its own
  fresh DB: ok 19.8s.
- pgx repeated concurrency `-race -count=10` Concurrent filter: 80/80 PASS,
  38.4s. Upgrade protocol re-run: PASS 2.5s. Scratch DBs dropped, absence
  confirmed.
- turso full conformance `-tags=integration -race -count=1`: 9 top-level
  tests PASS (no TestUpgradeRunbook — pgx-only by design per AZ3-5.1),
  71 conformance subtest PASS lines, Mutations 23 confirmed — 16.2s.
  Orchestrator re-ran: ok 9.8s.
- turso repeated concurrency `-count=10`: 80/80 PASS, 52.6s.
- Proof host: TestAZ3ProofProtocol 12/12 subtests PASS; transcript artifact
  at examples/auth-cms/cmd/server/testdata/az3-proof-transcript.md (12
  sections; orchestrator confirmed). Guarded/SystemMutator host protocol:
  10/10 named tests PASS incl.
  TestNoSessionOnlyAuthorizationMutationRoute.

REPRODUCIBLE LIVE-GATE RECIPE (for AZ3-5.6/5.7 reverification)
- Containers: authv3-pg (localhost:5432, superuser postgres/postgres) and
  authv3-libsql (http://127.0.0.1:8080, token local-dev) — both up 40+ hours,
  left running.
- pgx legs: `PGPASSWORD=postgres psql -h localhost -U postgres -c "CREATE
  DATABASE <name> TEMPLATE template0 LC_COLLATE 'C' LC_CTYPE 'C'"` (C-
  collation MANDATORY), then POSTGRES_TEST_DSN='postgres://postgres:
  postgres@localhost:5432/<name>?sslmode=disable' with explicit -count
  (never cached across resets); drop the DB after.
- turso legs: TURSO_DATABASE_URL + TURSO_AUTH_TOKEN env, -tags=integration,
  explicit -count; the conformance suite truncates iam_* per run.
- Concurrency filter: -run 'TestConformance/.*/Concurrent' (8 subtests; the
  plan's TestConformance_Postgres/_Turso names do not exist — verify filters
  are non-empty via -v before counting a green).

WHAT AZ3-5.6 / AZ3-5.7 NEED (owner-controlled sessions; NOT started)
- AZ3-5.6 review-wave focus areas per the plan, plus these recorded owner
  calls: (1) sdk graduation RE-DEFER recorded in RELEASING.md — the
  ARCHITECTURE protocol-table row update awaits owner ratification; (2)
  orphaned internal engine raw methods kept — removal cascades into the
  public Config.IDs field (REOPEN-grade, inventoried); (3) teardown reason
  rides log+audit Detail, not a receipt column (cost of the alternative
  recorded in the AZ3-3.2 log); (4) demo.go raw-vs-effective teaching
  listing kept; (5) stores READMEs' one-word "draft" descriptor staleness.
- Evidence artifacts for reviewers: this log + per-phase execution logs;
  NOTES.md AZ3-5.3 inventory + AZ3-5.4 audit table; RELEASING.md tag-floor
  note; az3-proof-transcript.md; UPGRADE.md executed evidence;
  TestUpgradeRunbook (repeatable).
- Everything remains UNCOMMITTED on main per the loop protocol (no commits/
  tags/PRs). Sensible commit point: the entire milestone as one review unit
  before AZ3-5.6 — owner's call.

### 2026-07-14 — Phase 5 / AZ3-5.6 + AZ3-5.7 — reviewer wave + remediation (IN PROGRESS)

Owner review (2026-07-14) did NOT approve the PR candidate: five accepted
findings, all confirmed against code by the orchestrator before remediation.
Per the proof-defect reopen rule each fix lands in its owning phase's layer,
is re-verified forward, and AZ3-5.5's gate must be RE-RUN clean over the fixed
tree (its 2026-07-14 green is invalidated — the conformance suite did not
exercise these adversarial shapes; an honest miss the review caught).

Findings (severity / reopens):
- F1 (P1, AZ3-3.2/0.5): actor-facing ApplyMutation accepts OpTeardown +
  caller-set purge bounds → an ordinary guarded actor can teardown/zero the
  last owner (reviewer repro: outcome=applied rows_left=0).
- F2 (P1, AZ3-2.3/2.4): pgx/turso decisionView records only the requested
  scope; group-expansion memberships (other resource scopes) + global-role
  subject scope go unrecorded → under read-committed a concurrent revoke
  commits without invalidating the guarded mutation.
- F3 (P1, AZ3-0.4/2.2): Command.Validate permits repeated subject refs;
  memstore grantLocked checks pre-state only → two rows/one receipt vs SQL
  unique-index divergence.
- F4 (P1, AZ3-1.3/1.1): check-path group expansion contractually unbounded,
  called without budget → graph-size DoS, contradicting phase acceptance.
- F5 (P2, AZ3-1.1): memstore hasSubjectResource omits subject_relation,
  collapsing #member/#admin; SQL keeps both.

#### F3 + F5 — FIXED & re-verified (2026-07-14)

- F5: memstore `hasSubjectResource` key now
  `(resourceType, resourceID, subjectType, subjectID, subjectRelation)`,
  matching idx_iam_relationships_unique_subject exactly; CreateRelationships
  doc corrected to the subject-relation-inclusive key.
- F3: `Command.Validate` (dialect-agnostic, before any evaluator) rejects
  intra-command duplicate subject references on relationship ops (same key
  (Type,ID,Relation), same- or different-relation) and exact-duplicate role
  rows on role ops, as ErrInvalidCommand. Distinct roles for one subject in
  one command stay legal (roles opaque, no one-relation invariant).
- Shared storetest cases (all three backends):
  Mutations/IntraCommandDuplicateSubjectRejected{DifferentRelations,
  SameRelation} (Apply path — ErrInvalidCommand, nil receipt, nothing
  persisted, anchor unmoved, MutationID unconsumed);
  Relationship/UsersetSubjectRelationsCoexistOnResource (raw
  CreateRelationships of group#member + group#admin on ONE resource → both
  persist, independently observable); Roles/DistinctAssignmentsCoexist.
  Evidence recorded: the pre-existing adversarial MemberAdminUsersetSeparation
  used DISTINCT resource IDs through the engine Check path, so it never
  exercised the same-resource raw-create collision — which is why F5 hid.
- Files: memstore/memstore.go, domain/mutation/mutation.go (+_test),
  storetest/mutations.go, storetest/storetest.go.
- Verify (agent + orchestrator independently re-ran): module `-race
  -count=1` green (7 pkgs); new cases confirmed RUN via -v on memstore, LIVE
  pgx (fresh C-collation DB, dropped), and LIVE turso (-tags=integration);
  `make check` "all checks passed"; guard exit 0. No legitimate command
  newly rejected; no migration/generated/sdk change.

#### F1 — FIXED & re-verified (2026-07-14)

- Defect: the actor-facing generic write seam `Service.ApplyMutation` was
  EXPORTED and accepted any valid Command including `OpTeardown` (which
  deliberately bypasses the guardian last-owner invariant in the store
  evaluators) — an ordinary guarded actor could zero the final owner
  (reviewer repro: outcome=applied, rows_left=0). It also trusted a
  caller-supplied `MaxAffectedRows`, defeating the EvaluationLimits purge
  ceiling. Reopened AZ3-3.2/0.5.
- Fix (defense in depth, both landed):
  1. Unexported the generic seam `ApplyMutation` → `applyMutation`. Repo-wide
     grep confirmed NO external caller (only same-package typed methods +
     tests; auth-cms uses typed methods + SystemMutator). No public
     generic-Command actor seam remains; no migration needed.
  2. Guardrails inside `applyMutation` (before actor.Validate / digest /
     ApplyGuarded): reject `OpTeardown` → `ErrTrustedOperationRequired`;
     normalize the purge bound deterministically — `OpPurge` forced to
     `s.maxBatchSize`, every other op forced to 0 — so a caller can neither
     widen nor set the bound. Safe for idempotency: `MaxAffectedRows` is
     excluded from the payload digest and ignored on replay (verified in
     domain/mutation/mutation.go:316-343 + digest.go).
  3. Also tightened the TRUSTED seam: `SystemMutator.Apply` now rejects
     `OpTeardown` → `ErrTeardownViaTypedMethod`, forcing teardown through
     `TeardownAuthorizationScope` (the only path that records the mandated
     non-empty reason + teardown audit). Safe because
     `TeardownAuthorizationScope` calls `m.mutations.Apply` DIRECTLY, not
     `m.Apply`, and no internal caller passes OpTeardown to `m.Apply`.
- Sentinels: `ErrTrustedOperationRequired`, `ErrTeardownViaTypedMethod` —
  both wrap `sdk.ErrInvalidInput` (precondition refusal → HTTP 400, NOT
  Forbidden/Unavailable: no Actor can ever succeed, teardown is a held
  capability not a principal; matches the ErrMutationsNotConfigured precedent).
- Regression tests (mutation_service_test.go): TestActorSeamRejectsTrusted
  Teardown, TestServiceExposesNoActorTeardownMethod (reflection),
  TestActorPurgeBoundNormalizedToMaxBatchSize, TestActorPurgeCannotWidenBound
  (end-to-end over memstore: crafted MaxAffectedRows=999999 overwritten with
  ceiling 2 → OutcomeInvariantBlocked, nothing removed),
  TestSystemMutatorApplyRejectsTeardown.
- Files: mutation_service.go, relationship_mutations.go, role_mutations.go,
  authorization.go (doc), mutation_service_test.go (+5 tests),
  system_mutator_trusted_test.go (comments).
- Verify (agent + orchestrator independently re-ran): module `-race
  -count=1` green; the 5 new tests confirmed RUN+PASS via -v; pgx + turso
  stores build/vet (incl. `-tags=integration`)/test green; auth-cms (proof
  host) build/vet/`-race` green; `make check` "all checks passed"; `make
  guard` exit 0. Service/API-surface change only — no store SQL or evaluator
  behavior touched, so hermetic legs suffice per the finding.

#### F2 — FIXED & re-verified (2026-07-14)

- Defect: the guarded-mutation `DecisionView` under-recorded dependencies in
  ALL THREE backends (pgx/turso mutations_eval.go, memstore mutations.go), so
  a guarded mutation could COMMIT on a stale authorization decision.
  (a) CheckRelation expands through group memberships but recorded only the
  QUERIED resource scope — the intermediate group resource scopes whose
  membership edges were traversed were not recorded, so a concurrent
  membership revoke (bumping the group's scope revision) did not invalidate
  the guard. (b) HasRole's global fallback reads global roles (which serialize
  into the SUBJECT scope) but recorded only the resource scope, so a concurrent
  global-role revoke did not invalidate. Reopened AZ3-2.3/2.4.
- Fix (behaviorally EQUIVALENT across dialects; NO decision boolean changed —
  only the recorded dependency set grew):
  - CheckRelation records a ScopeResource for every distinct (atype,aid) in
    the reachable/expansion set. pgx/turso: new `recordExpansionScopes` runs
    `reachableCTE + "SELECT DISTINCT atype, aid FROM reachable"` in the same
    tx, DRAINS rows first (pgx forbids a second active query while `record`
    itself queries), then records each. memstore:
    `checkRelationExpandLocked` → `checkRelationExpandScopesLocked` returns the
    visited-scope set; the view records each. Recording the seed as a resource
    scope is a harmless over-record; UNDER-recording was the safety bug.
  - HasRole records `ScopeSubject{subjectType,subjectID}` WHENEVER the global
    fallback is consulted (scope.Kind==ScopeResource && exact-resource failed),
    regardless of the fallback result; NOT when the exact-resource role already
    matched; not needed when scope.Kind==ScopeSubject (queried scope already IS
    the subject scope). memstore: `hasRoleEffectiveLocked` →
    `hasRoleEffectiveScopesLocked` reports whether global was consulted.
  - Engine (non-guard) check/lookup paths untouched — the `*Locked` helpers
    had only the decisionView as caller; the engine uses the public locking
    CheckRelationWithGroupExpansion.
- Deterministic proof = per-store WHITE-BOX Dependencies() tests (decisionView
  is unexported): memstore `mutations_deps_test.go` (hermetic), pgx
  `mutations_deps_live_test.go` (POSTGRES_TEST_DSN-gated), turso
  `mutations_deps_live_test.go` (//go:build integration). Each asserts
  CheckRelation(doc:1, editor, alice via group:eng) records BOTH
  ScopeResource{doc,1} AND ScopeResource{group,eng}; HasRole fallback records
  BOTH ScopeResource{doc,1} AND ScopeSubject{user,alice}; negative: exact-role
  match does NOT record the subject scope.
- Shared conformance case `Mutations/ConcurrentCrossScopeGuardRevokeRaces
  GuardedMutation` (storetest/mutations.go) is CORROBORATION ONLY, not a
  deterministic F2 proof — HONESTLY CLASSIFIED: under full-write-serialization
  stores (memstore mutex, turso BEGIN IMMEDIATE) the guard read and revoke
  cannot interleave so the stale window is architecturally precluded; only
  pgx's per-anchor locking has the window, and post-hoc row inspection can't
  distinguish a fixed from a stale-allow commit through the public API. It
  asserts the safety invariant (revoke commits cleanly; guarded write is
  applied XOR cleanly aborted, never torn, never (nil,nil)) under -count=20.
- Files: stores/pgx/mutations_eval.go, stores/turso/mutations_eval.go,
  memstore/{memstore.go,roles.go,mutations.go}, memstore/mutations_deps_test.go,
  stores/pgx/mutations_deps_live_test.go,
  stores/turso/mutations_deps_live_test.go, storetest/mutations.go.
- Verify (agent + ORCHESTRATOR INDEPENDENTLY re-ran): module `-race -count=1`
  green (7 pkgs); memstore white-box tests PASS fresh; LIVE pgx (fresh
  C-collation DB authz_f2_reverify, DSN verified C|C, DB DROPPED): white-box
  `*Live` tests PASS, cross-scope conformance subtest confirmed executing (-v)
  and PASS at -count=20; LIVE turso (-tags=integration, libsql container):
  white-box `*Live` PASS, conformance subtest PASS at -count=20; auth-cms
  `-race` green; `make check` "all checks passed"; `make guard` exit 0. No
  decision boolean changed; no migration/reachableCTE/engine-path change.

#### F4 — FIXED & re-verified (2026-07-14)

- Defect: the check-path group expansion was contractually UNBOUNDED
  (relationship.go `# Bounding` declared CheckRelationWithGroupExpansion /
  CheckBatchDirect "UNBOUNDED BUT CYCLE-SAFE"). Cycle-safe (UNION/visited-set
  dedup) is NOT size-bounded: an adversarial membership graph makes each Check
  do O(graph-size) work; the engine (checkDirectRelation, checkBatchOptimized)
  called these with NO budget, unlike the Through path (MaxRelationTargets/
  MaxThroughDepth) and Lookup path (MaxLookupResults+1). Reopened AZ3-1.3/1.1.
- Fix (behaviorally EQUIVALENT across dialects; NO within-budget bool changed):
  - Port signature: both methods gained `maxExpansionStates int`
    (`<= 0` = UNBOUNDED, opt-out for non-engine callers; engine always passes
    resolved positive `s.limits.MaxGraphStates`).
  - Overflow sentinel `relationship.ErrExpansionBudgetExceeded` (wraps
    sdk.ErrUnavailable). Engine maps it at the store-call boundary via new
    `mapExpansionBudget` → `ErrEvaluationLimit` (both wrap ErrUnavailable, so
    host-facing 503/fail-closed identical); domain never imports the engine.
  - pgx/turso: new `boundedReachableCTE` (the original `reachableCTE` const is
    LEFT UNTOUCHED — Lookup path + guard-path decisionView keep using it).
    Bounds work two ways: a `depth` column with `WHERE depth < maxExpansionStates`
    caps recursion levels (a deep chain cannot run away; cycles still terminate),
    and a `capped` CTE `SELECT DISTINCT ... LIMIT maxExpansionStates+1` makes the
    join scan at most cap+1 states — `state_count > maxExpansionStates` is the
    deterministic overflow signal, decided independently of the match (never a
    truncated bool). Correctness pin: any within-budget state has a shortest
    path shorter than its distinct-state count ≤ cap, so depth<cap never cuts a
    within-budget state.
  - memstore: `expandReachable(..., maxExpansionStates)` returns
    `(set, overflow bool)`, stopping the instant a new distinct state would
    exceed the cap (genuine growth cap, not post-hoc). Guard/Lookup callers pass
    0 (unbounded), preserving their behavior.
  - Rewrote the relationship.go `# Bounding` contract to declare the check-path
    expansion BOUNDED by `maxExpansionStates`, overflow = ErrExpansionBudget
    Exceeded → engine ErrEvaluationLimit; never a deny, never a truncated bool.
- Tests: shared conformance `Budget/GroupExpansionOverflowIsIndeterminate/
  {Overflow,WithinBudgetNoFalseOverflow}` (7-state chain vs cap=3 → overflow on
  BOTH check methods; within-budget still returns correct allow + clean deny)
  runs on all three backends; engine
  `TestCheckGroupExpansionOverflowMapsToEvaluationLimit` (errors.Is
  ErrEvaluationLimit + sdk.ErrUnavailable; sentinel not leaked; direct + batch
  paths). All test fakes/stubs + call sites updated to the new arity.
- Files: domain/relationship/relationship.go, internal/logic/authorizersvc/
  service.go (+_test), memstore/memstore.go, stores/pgx/relationships.go,
  stores/turso/relationships.go, storetest/budget.go, and signature-only
  updates to authorization_test.go, authorizersvc/{limits_test via shared
  fakeStore,middleware_test}.go, domain/relationship/relationship_test.go,
  memstore/{memstore_test,mutations_test}.go, storetest/storetest.go.
- Verify (agent + ORCHESTRATOR INDEPENDENTLY re-ran): module `-race -count=1`
  green; engine mapping test + memstore overflow conformance subtest confirmed
  executing (-v) and PASS; LIVE pgx (fresh C-collation DB authz_f4_reverify,
  verified C|C, DB DROPPED): full store test PASS (7.78s), overflow subtest
  (Overflow + WithinBudget) confirmed executing and PASS; LIVE turso
  (-tags=integration): full PASS (5.24s), overflow subtest confirmed executing
  and PASS; auth-cms `-race` green; `make check` "all checks passed"; `make
  guard` exit 0. No within-budget decision boolean changed; original
  reachableCTE, migrations, and guard-path decisionView untouched.
- FOLLOW-UP (owner call, NOT one of the 5 accepted findings, NOT required for
  AZ3-5.7): the guard-path decisionView expansion (mutations_eval.go /
  memstore checkRelationExpandScopesLocked) shares the same unbounded-expansion
  vector but is reachable only by an already-authorized mutation actor (blast
  radius = one mutation). Threading a budget there needs a store-construction
  expansion-budget seam (the guard reads inside the tx, not via Config.Limits),
  so it is deliberately left as a separately-ratified packet, not fixed here.

All five accepted findings (F1–F5) fixed and independently re-verified. The
AZ3-5.5 implementation-complete gate is RE-RUN below over the remediated tree.

#### AZ3-5.5 gate — RE-RUN over the fully-remediated tree (2026-07-14) — GREEN

Orchestrator ran the full implementation-complete gate over the tree with all
five remediations applied (this re-establishes the AZ3-5.5 green invalidated by
the review):
- Hermetic: `features/authorization` `go build ./... && go vet ./... && go test
  -race -count=1 ./...` — all 7 packages ok. gofmt drift: none
  (`gofmt -l features/authorization` empty). `git diff --check` clean.
- LIVE pgx (fresh C-collation DB `authz_az355_regate`, verified `C|C`, DB
  DROPPED after): repeated race proof `-run 'TestConformance/.*/Concurrent'
  -count=10` — 90 Concurrent-subtest PASS lines (9 subtests × 10), package ok
  (19.28s). Full store suite green in the F4 leg (7.78s).
- LIVE turso (-tags=integration, libsql container): same `-count=10` race proof
  — 90 Concurrent-subtest PASS lines, package ok (21.57s). Full integration
  suite green in the F4 leg (5.24s).
- Proof host: `examples/auth-cms` `go build ./... && go vet ./... && go test
  -race ./...` — all ok.
- Root: `make check` "all checks passed" (templ drift + per-module build/vet/
  test + integration-tag vet + 16 guards); `make guard` exit 0.

AZ3-5.5 is GREEN again on the remediated tree. AZ3-5.7 remediation COMPLETE:
all five accepted findings fixed, each reopened phase re-verified forward, live
legs run on both dialects, one honest follow-up (guard-path expansion budget)
recorded as an owner-call packet. Milestone is PR-ready pending owner review;
NO commits/tags/PR made (owner-owned). Recommended commit point: a single
AZ3-5.7 remediation commit over the files listed in the F1–F5 + gate entries
above plus the phase-log/TASKS updates.
