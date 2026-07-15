# Phase 2 — atomic stores and migrations

Status: DRAFT; ready after phases 0–1.
Depends on: phase-0 mutation contract and phase-1 read semantics.

## Goal

Make mutation/revision/idempotency invariants real in memory, PostgreSQL, and
Turso before the service promotes the new API.

## Task AZ3-2.1 — canonical migrations, scope revisions, and mutation receipts

Touch: both authorization migration trees and migration parity tests.

Implement:

- At preflight, re-check tags. With no tags, fold the final greenfield schema
  cleanly; with tags, stop and design append-only migrations.
- Preserve exact userset relation as NOT NULL empty/non-empty text and update
  indexes for the ratified one-relation rule without conflating relation state.
- Add `iam_scopes` revision anchors keyed by scope kind/type/ID.
  Resource scopes serialize relationships and scoped roles; subject scopes
  serialize global roles.
- Add `iam_mutations` receipts keyed by MutationID, storing scope,
  operation, versioned payload digest, resulting revision, domain outcome, and
  applied schema digest, and created/expiry time under the ratified retention
  policy. Replay is computed by receipt lookup and returned as `Replayed=true`;
  it is not stored as an outcome.
  Store no display data, secrets, request headers, or unbounded payload.
- Add constraints for non-empty structural columns, valid scope kind, nonnegative
  revision, and consistent global/scoped role pairs.
- Keep pgx/turso migration filename sets identical and add schema inventory tests.
- Draft the old-v1-to-v3 data conversion: detect invalid/missing userset relations
  before applying, resolve silent-conflict rows deliberately, seed scope revisions,
  and never guess `member` for ambiguous data.

Verify:

```sh
cd features/authorization/stores/pgx && go test -count=1 ./... -run 'Migration|Schema|Probe'
cd features/authorization/stores/turso && go test -count=1 ./... -run 'Migration|Schema|Probe'
make guard
```

Acceptance: dialect trees express the same constraints and every ambiguous v1
row is a runbook decision, not an automatic broad grant.

## Task AZ3-2.2 — reference-memory atomic mutation repositories

Depends on: AZ3-2.1 contract shape.
Touch: memstore mutation/revision implementation and storetest reference.

Implement:

- Implement Apply under one mutex covering receipt lookup, current state,
  dependency-tracking guard evaluation, dependency revision validation,
  invariant evaluation, row changes, revision bump, and receipt persistence.
  The view passed to the guard reads the held snapshot without recursively
  locking and records the same logical scope dependencies as SQL stores.
- Exact replay returns the original receipt; same MutationID with a different
  payload digest returns the stable MutationID-mismatch command error and
  changes nothing.
- Prove exact replay still returns the original receipt after constructing the
  service with a newer schema that rejects the original relation; a new command
  with that relation is rejected.
- Implement grant, revoke, replace, purge, role assign/unassign, expected
  revision, and no-op semantics.
- Make last-owner/guardian protection part of Apply, not a helper calling count.
- Add high-contention tests for two last owners, stale writers, replay storms,
  and mixed-kind scoped mutation.

Verify:

```sh
cd features/authorization && go test -race -count=20 ./memstore ./storetest -run 'Mutation|Revision|Owner|Concurrent|Replay'
make guard
```

Acceptance: reference memory proves every atomic promise with one lock and no
service orchestration.

## Task AZ3-2.3 — pgx atomic relationship and role mutation repositories

Depends on: AZ3-2.1, AZ3-2.2.
Touch: pgx repositories and tests.

Implement:

- Apply inside one transaction. Create missing scope anchors safely, then read
  the command state and expected revision.
- Run the supplied guard against a dependency-tracking decision reader. Record
  every authorization scope and revision read by the guard. Before mutation,
  sort the mutation scope and all dependency keys lexically, insert any missing
  revision-0 anchors in that order, lock every anchor in that order, re-read
  revisions, and return stale with no domain write if any observed revision
  changed. A scope observed without an anchor records revision 0; a concurrent
  first writer therefore becomes a detectable 0→1 change, not a phantom.
- All v3 mutation paths, including trusted paths, must advance the same scope
  anchors; raw legacy writes are unavailable to ordinary host code. Prove that
  an absent scope anchor cannot create a phantom bypass.
- Bound the guard callback by context; document that it may use only the supplied
  view. Recovering a guard panic, if supported, rolls back and returns a coarse
  infrastructure error. Do not keep a transaction open across network or
  unrelated-store I/O.
- Use row/scope locking so two last-owner revokes cannot both commit.
- Make exact replay return its stored receipt without another row change or
  revision bump.
- Return explicit semantic conflict for a second relation where replacement was
  not requested; implement replacement without a delete/create visibility gap.
- Map unique/serialization errors into stable sdk/authorization errors.

Verify:

```sh
cd features/authorization/stores/pgx && go test -race -count=1 ./...
make guard
```

Acceptance: live store tests can demonstrate one commit winner and atomic
mutation+receipt rollback.

## Task AZ3-2.4 — turso atomic relationship and role mutation repositories

Depends on: AZ3-2.1, AZ3-2.2.
Touch: turso repositories, transaction integration as necessary, tests.

Implement the pgx contract in libSQL/SQLite without weakening it:

- use the connector's immediate/write-serialized transaction behavior proven by
  auth v3 rather than a deferred read-then-write transaction;
- run the guard through a dependency-tracking reader under that same write
  transaction, with no callback escape to the outer Service;
- record the same logical dependency scopes as pgx, validate their revisions in
  canonical order, and prove parity even though `BEGIN IMMEDIATE` already
  serializes writers;
- condition writes on current revision and receipt absence;
- make loser outcomes deterministic (`stale`, replay, or invariant blocked), not
  raw `SQLITE_BUSY` where the contract promises an application outcome;
- prove rollback leaves relationship/role, revision, and receipt unchanged.

Verify:

```sh
cd features/authorization/stores/turso && go test -tags=integration -race -count=1 ./...
make guard
```

Acceptance: Turso matches the port under repeated contention; a dialect
limitation is a blocker, not permission to emulate atomicity in Go.

## Task AZ3-2.5 — shared conformance and repeated dual-dialect race proof

Depends on: AZ3-2.2 through AZ3-2.4.
Touch: storetest and live test harnesses.

Implement named sub-runners for:

- exact userset member/admin isolation;
- mutation replay and payload mismatch;
- stale revision single winner;
- guard-grant revoke racing the guarded mutation (the revoke or mutation may win,
  but a detached stale allow may not commit afterward);
- two-owner concurrent revoke (exactly one commits, one owner remains);
- atomic replace with no absent/intermediate state;
- scoped role revoke with global effective fallback;
- batch rollback;
- rejection of a cross-scope batch before any row/revision/receipt change;
- receipt/revision agreement, replay metadata, retention-boundary behavior; and
- context cancellation and infrastructure error mapping.

Run live concurrency cases at least ten times per dialect under `-race`. Inspect
stored rows for invalid usersets, revision gaps, duplicate receipts, and scope
anchors that disagree with row state.

Live environment recipe (the auth v3 AV3-9.8 lessons; do not relearn them):

- run against the standing `authv3-pg` (C-collation — pgx test databases must be
  C-collation) and `authv3-libsql` containers with fresh/reset databases per leg;
- always pass an explicit `-count` on live legs so the test cache cannot replay
  results across database resets; and
- harness phases must drive every mutation attempt to a terminal result before
  teardown, and wait margins must respect lock/retry timing under `-race`
  slowdown — the auth v3 live flakes were often harness margins, and a repeat
  here would mask (or fake) a real race defect.

Verify:

```sh
cd features/authorization/stores/pgx && go test -race -count=10 -run 'TestConformance_Postgres/.*/Concurrent' ./...
cd features/authorization/stores/turso && go test -tags=integration -race -count=10 -run 'TestConformance_Turso/.*/Concurrent' ./...
make check
make guard
```

Acceptance: all three stores produce the same receipt and final state for every
named race.

## Task AZ3-2.6 — host upgrade runbook draft

Depends on: AZ3-2.1.
Touch: new authorization v3 upgrade-runbook draft.

Document:

- backup, maintenance window, old binary stop, invalid-row audit, data repair,
  migration export/apply, revision seeding, v3 binary boot, and rollback boundary;
- queries that find concrete subjects stored where usersets are required,
  non-`member` usersets previously evaluated as member, empty IDs, and relation
  conflicts;
- migration-source ordering for authorization; a future effects packet owns
  composed authorization/events ordering;
- no mixed old/new serving while semantics differ; and
- destructive reset path for example/dev hosts versus data-preserving adopter
  path.

Acceptance: an adopter can learn whether current v1 data would gain, lose, or
retain access before deploying v3.

## Phase acceptance

- Canonical migrations and filename parity are green.
- Memory, pgx, and turso pass one mutation/read conformance suite.
- Both live dialect concurrency legs are recorded, not skipped.
- Upgrade draft exists before public service promotion.
- `make check` and `make guard` pass.

## Stop conditions

- A store cannot provide atomic Apply/last-owner semantics.
- A migration guesses an ambiguous userset relation.
- Mutation replay can bump revision or change the original domain outcome.
- Live race evidence is replaced by a hermetic fake.

## Execution log

Append only during execution.

### 2026-07-14 — AZ3-2.1 — canonical migrations, scope revisions, and mutation receipts — PASS (live-proven both dialects)

Outcome: zero tags re-confirmed at task preflight → final v3 greenfield schema
folded cleanly. Both trees carry the identical 4-file set:
0001_iam_relationships, 0002_iam_roles, 0003_iam_scopes (NEW: revision
anchors, `(scope_kind, scope_type, scope_id)` PK + revision ≥ 0 DEFAULT 0 —
absent anchor = revision 0 by contract), 0004_iam_mutations (NEW: receipts
keyed by mutation_id PK — scope triple, operation, payload_encoding+digest,
outcome, resulting revision, schema_digest, created_at, nullable expires_at;
digest not payload; Replayed deliberately NOT a column).

- Constraints (byte-identical expressions both dialects):
  ck_iam_relationships_nonempty, ck_iam_roles_nonempty,
  ck_iam_roles_scope_pair (global = both scope cols empty, scoped = both
  non-empty), ck_iam_scopes_kind/nonempty/revision,
  ck_iam_mutations_kind/outcome/revision/nonempty. ck_iam_mutations_outcome
  restricts to the persisted set ('applied','no_change','not_found') —
  enforces the AZ3-0.4 persistence contract at the storage layer (added
  beyond the literal list; justified). Dialect differences: only primitive
  spellings already established (BIGINT vs INTEGER, TIMESTAMPTZ vs TEXT,
  uuid defaults). Nothing weakened either side.
- Indexes: no churn needed — AZ3-1.1 CTE + AZ3-1.5 effective-role
  secondaries already serve the ratified paths; the one-relation arbiter
  `idx_iam_relationships_unique_subject` (exact SubjectRef incl.
  subject_relation, without relation) preserved, comment updated to the v3
  explicit-semantic_conflict contract. iam_scopes/iam_mutations are pure PK
  access. subject_relation stays NOT NULL DEFAULT '' exact text.
- Conversion draft: features/authorization/stores/CONVERSION.md (linked from
  both store READMEs) — detection queries for empty structural columns,
  half-populated role scope pairs, concrete-where-userset-required shapes,
  non-member userset rows whose meaning changes, silent-conflict export;
  revision-0 scope seeding (ON CONFLICT DO NOTHING); NEVER guess member;
  receipts not backfilled; dev destructive reset vs adopter path; full
  runbook deferred to AZ3-2.6.
- Tests: TestMigrationInventory, TestMigrationParity,
  TestMigrationConstraintParity (hermetic, both stores); TestSchemaProbe
  (live: applies migrations, probes tables, asserts every constraint rejects
  a violating row + accepts permanent-retention NULL expires_at).
- Verify — agent ran all; orchestrator re-ran: tags still 0; identical
  filename sets confirmed; hermetic parity tests green both stores; LIVE pgx
  TestSchemaProbe PASS (fresh C-collation DB, dropped after); LIVE turso
  TestSchemaProbe PASS; `make check` "all checks passed"; guard exit 0.
  No example carries scaffolded authorization migrations (grep iam_* empty;
  auth-cms in-memory; examples/cms holds only cms sources).
- Premise adaptations: (1) shared turso container carried a stale
  schema_migrations ledger + pre-v3 iam_* tables — agent reset the
  authorization schema via sqld /v2/pipeline (dropped 4 tables + their
  ledger rows) before the live leg, per the fresh/reset-per-leg recipe;
  adopters follow CONVERSION.md, never this dev-reset. (2) iam_scopes kept
  minimal (key+revision, no timestamps) so AZ3-2.3/2.4 can seed bare
  revision-0 anchors. (3) No Go consumers of the new tables yet — Apply is
  AZ3-2.2/2.3/2.4; probe tests prove the shape.

### 2026-07-14 — AZ3-2.2 — reference-memory atomic mutation repositories — PASS

Outcome: reference memstore proves every atomic promise under ONE lock with no
service orchestration. NOTE: the implementer agent stalled (stream watchdog,
600s) after completing the implementation but before its final report; the
orchestrator verified the work directly from the tree and ran every gate —
nothing below is claimed from an agent report.

- Shape: new `memstore.Store` bundle — Relationships, Roles, and new
  Mutations over ONE shared mutex-guarded state (`memstore.New(opts...)`), so
  an Apply is immediately visible to raw reads and Apply serializes against
  every read/write under the same lock. This is the documented reference
  shape SQL stores mirror operationally (shared tables + write-serializing
  transaction). memstore/mutations.go (558 lines) implements
  Apply/ApplyGuarded: receipt lookup, payload-digest mismatch, expected +
  dependency revision validation (view records logical scope deps without
  recursive locking via non-locking `*Locked` read cores), guardian
  invariant, row changes, exactly-once revision bump, receipt persistence —
  all inside the critical section.
- Guardian invariant input (what SQL stores mirror): NEW
  domain/mutation/guardian.go — `GuardianPolicy{Rules []GuardianRule{
  ResourceType, Relation, MinAnchors}}` supplied at repository CONSTRUCTION
  (policy, never Command payload); post-state rule "≥ MinAnchors DIRECT
  anchors remain" under the scope lock; direct anchor = exact concrete
  SubjectRef with EMPTY relation (group#member owner is NOT one); empty
  ResourceType = every type; the post-state form uniformly enforces
  establish-first (member/role-first on fresh scope blocked; legacy orphan
  scope blocked until owner-establishing repair); only OpTeardown exempt
  (capability gating deferred to service layer per plan).
  `DefaultGuardianPolicy()` = owner/min-1 on every type (default #10);
  `memstore.WithGuardianPolicy` overrides.
- storetest Mutations suite now REAL (was nil-skip stubs) and extended to 16
  cases: the six AZ3-0.4 specs (ExactReplayReturnsOriginalReceipt,
  MutationIDPayloadMismatchChangesNothing, StaleRevisionRejected,
  RollbackLeavesNoTrace, NoPartialBatch, ConcurrentSingleWinner) +
  GrantRevokeReplaceRevisions, PurgeBlockedTeardownClears,
  RoleAssignUnassignScopes, ExpectedRevisionAndNoOp, ReplayAfterSchemaChange
  (exact stored replay returns original receipt under a newer schema that
  rejects the old relation; a NEW command with that relation is rejected —
  via the SemanticValidator callback), GuardianEstablishesMinimum,
  GuardedViewReadsAndDenies, ConcurrentReplayStorm,
  ConcurrentStaleWriterStorm, ConcurrentMixedKindScopedMutation. memstore
  unit tests: TestMutationApplyVisibleToReads (state-sharing proof),
  TestMutationGuardianDirectAnchorOwner, TestMutationRevisionScopeIsolation,
  TestMutationEmptyGuardianPolicyAllowsMemberFirst.
- Verify (ALL run by orchestrator): module build+vet green; all 16
  TestConformance/Mutations subtests PASS (-race -count=1 -v); contention
  suite `go test -race -count=20 ./memstore ./storetest -run
  'Mutation|Revision|Owner|Concurrent|Replay'` PASS; full module `-race
  -count=1` green (7 pkgs); auth-cms full build+test green; stores/pgx and
  turso hermetic behavior unchanged (pgx conformance skips whole suite
  without DSN; turso integration-tagged); `make check` "all checks passed";
  `make guard` exit 0.
- Premise adaptations (orchestrator-observed): (1) guardian input =
  construction-time policy object (not per-call callback) — matches "keep it
  out of the Command payload" and gives SQL stores a mirrorable shape. (2)
  Two lint "unused method" flags (checkRelationExpandLocked,
  hasRoleEffectiveLocked) verified FALSE — both are the mutation view's
  non-locking read cores, used by mutations.go. (3) auth-cms wiring NOT
  changed to the new Store bundle in this task (its authmem wiring is
  AZ3-3.4/4.1 territory); its build+tests remain green.

### 2026-07-14 — AZ3-2.3 — pgx atomic relationship and role mutation repositories — PASS (live-proven)

Outcome: pgx implements `mutation.MutationRepository` atomically over the
shared `iam_*` tables + `iam_scopes` anchors + `iam_mutations` receipts. All
16 shared Mutations conformance subtests + 2 pgx-specific tests RUN and PASS
live under `-race -count=1` (orchestrator re-ran on its own fresh C-collation
DB, twice).

- Files: stores/pgx/mutations.go (mutationStore, one-transaction applyTx,
  canonical lock-set, anchor insert + FOR UPDATE, receipt lookup/insert,
  revision bump, guard-panic recovery, error mapping), mutations_eval.go
  (per-op evaluators mirroring memstore, guardian check, dependency-tracking
  decisionView), mutations_live_test.go, postgres.go (wires Mutations,
  probes all four iam_* tables, `Repositories(db, ...Option)` +
  WithGuardianPolicy defaulting to DefaultGuardianPolicy), conformance
  truncate list + README.
- Design: one tx per Apply via db.InTx. Lock set = mutation scope ∪ guard
  dependency scopes, deduped, sorted by ScopeKey.Canonical(); per scope:
  INSERT ON CONFLICT DO NOTHING (materializes revision-0 anchor, serializes
  concurrent first inserter) then SELECT ... FOR UPDATE — global order
  prevents deadlock. Ordered contract: guard (records deps + observed
  revisions) → lock anchors → receipt dedup → dependency re-validation →
  semantic validate → expected-revision → evaluate/apply → bump exactly once
  (UPDATE revision+1 RETURNING) → insert receipt. Replay returns the stored
  receipt verbatim (Replayed=true, no row change/bump); digest mismatch →
  ErrPayloadMismatch. Replace = in-place UPDATE on the exact SubjectRef (no
  delete/create gap; unique-subject index guarantees one row).
  Non-persisted outcomes (semantic_conflict/invariant_blocked) write no
  rows/receipt but COMMIT (releases locks; bare revision-0 anchors are
  semantically identical to absent). Errors: 40001/40P01 → ErrStaleRevision;
  receipt unique violation (cross-scope MutationID reuse) →
  ErrPayloadMismatch; guard panic → rollback + coarse sdk.ErrUnavailable.
- Phantom-bypass proof: guard reading an anchorless scope records revision
  0; the repo materializes + locks that anchor, so a concurrent first writer
  is a detectable 0→1 mismatch → ErrStaleRevision, nothing committed.
  TestMutationAbsentAnchorNoPhantom drives it deterministically;
  TestMutationGuardPanicRollsBack proves panic → no relationship/bump/
  receipt.
- Verify — agent ran all; orchestrator re-ran: hermetic store
  build/vet/test green; core module `-race -count=1` green (7 pkgs);
  `make check` "all checks passed"; guard exit 0; LIVE fresh C-collation
  `az3_verify`: all 16 Mutations subtests + both pgx tests PASS (-v),
  full package re-run PASS, DB dropped; container left running.
- Premise adaptations: (1) `Repositories` gained variadic `...Option`
  (backward-compatible; no existing callers break). (2) Receipt
  `schema_digest` uses store-local sentinel "unset" (ck_ constraint forbids
  empty; memstore leaves it empty — FLAGGED for AZ3-2.5/phase-3: reconcile
  the digest source when the service wires the compiled schema through
  Apply; storetest is per-backend so no cross-comparison breaks today). (3)
  Four-table probe fails at wiring time, not first Apply.

### 2026-07-14 — AZ3-2.4 — turso atomic relationship and role mutation repositories — PASS (live-proven)

Outcome: turso implements the full MutationRepository contract with no
weakening. All 16 shared Mutations subtests + 2 turso-specific tests RUN and
PASS live under `-race -count=1` (orchestrator re-ran independently).

- Files: stores/turso/mutations.go (mutationStore; Apply/ApplyGuarded →
  retryBusy-wrapped InTx → applyTx single ordered critical section),
  mutations_eval.go (per-op evaluators mirroring pgx, guardian check,
  dependency-tracking decisionView over the shared reachableCTE + global
  role fallback), busy.go (isBusy + bounded retryBusy — the jobs-turso
  pattern), turso.go (wires Mutations, four-table probe,
  `Repositories(db, ...Option)` + WithGuardianPolicy, best-effort PRAGMA
  busy_timeout), integration-tagged mutations_live_test.go, conformance
  truncate list + README.
- Transaction design: connector InTx issues BEGIN IMMEDIATE — one Apply =
  one write-serialized transaction (the auth v3 precedent; the honest libSQL
  mirror of memstore's mutex / pgx's FOR UPDATE). Anchors still materialized
  (INSERT ON CONFLICT DO NOTHING) and re-read in Canonical() order; since
  serialization precludes mid-transaction concurrent commits, canonical-order
  dependency re-validation is the required defense-in-depth PARITY mirror,
  with single-winner/stale/replay guarantees carried by serialization +
  revision CAS + receipt dedup.
- Busy mapping decision (recorded): bounded busy-retry of the whole
  idempotent transaction, NOT busy→stale (which would wrongly error a
  replay-storm loser that must get its stored receipt). Raw SQLITE_BUSY
  never leaks; loser outcomes deterministic via revision CAS
  (ErrStaleRevision), receipt dedup (replay/ErrPayloadMismatch), guardian
  post-state (invariant_blocked).
- Parity vs pgx: semantics identical (ordered contract, persisted-outcome
  set, replay verbatim, in-place UPDATE replace, "unset" schema-digest
  sentinel, canonical-order recording); mechanics differ (BEGIN IMMEDIATE vs
  FOR UPDATE, `?` args, TEXT timestamps, retryBusy vs 40001/40P01 mapping).
- Premise adaptations: (1) TestMutationAbsentAnchorNoPhantom adapted — the
  pgx mechanism (mid-guard concurrent 0→1 write) is architecturally
  impossible under BEGIN IMMEDIATE (would self-deadlock; STRONGER, not
  weaker); turso's version proves the parity building blocks
  deterministically, and the live single-winner proof rides the shared
  Concurrent* cases. (2) retryBusy kept store-local (jobs-turso precedent).
  (3) ORCHESTRATOR FIX during verification: gofmt drift in BOTH
  stores' relationships.go doc comments (AZ3-1.1-introduced `''` sequences
  trigger gofmt's doc-comment quote conversion) — rephrased to "empty
  arelation"/"must be empty" in pgx + turso; gofmt now clean; both stores
  re-tested green.
- Verify — agent ran all; orchestrator re-ran after the gofmt fix: hermetic
  store build/vet (+integration vet)/test green; core module `-race
  -count=1` green (7 pkgs); LIVE turso `-tags=integration -race -count=1
  -v`: all 16 Mutations subtests + TestMutationAbsentAnchorNoPhantom +
  TestMutationGuardPanicRollsBack PASS; `make check` "all checks passed";
  guard exit 0; container left running.

### 2026-07-14 — AZ3-2.5 — shared conformance and repeated dual-dialect race proof — PASS (×10 live both dialects)

Outcome: every named race in the task checklist is a shared storetest case;
all three stores produce the same receipt and final state for each; live
concurrency ran ≥10× per dialect under -race with post-storm forensics.

- New shared sub-runners (storetest/mutations.go, now 22 Mutations cases):
  ConcurrentGuardRevokeRacesGuardedMutation (guard-grant revoke racing the
  guarded mutation — losers stale/denied, never a committed detached stale
  allow), ConcurrentTwoOwnerRevokeRounds (N explicit rounds, exactly one
  commits, one owner remains), ConcurrentReplaceNoAbsentState (concurrent
  snapshot readers always see exactly one relation),
  ConcurrentReceiptRevisionForensics (gapless revision run agreeing with
  final anchor; replay metadata; permanent retention),
  CrossScopeBatchRejectedNoStateChange (scope/rows disagreement + op/rows
  mismatch → ErrInvalidCommand, zero state change, MutationID unconsumed),
  ContextCancellationNoStateChange. Existing cases mapped to the remaining
  checklist bullets (member/admin isolation via adversarial suite;
  replay/mismatch; stale single winner; scoped-role global fallback; batch
  rollback; infra mapping via per-dialect guard-panic tests).
- Forensics, two honest layers: port-level shared helpers (anchorRevision
  no-op probe + assertGaplessRevisions + replay-verbatim loop) run on all
  three backends; SQL-level TestMutationStormForensics per dialect (anchor
  revision == committed rows == N; no duplicate receipts; no revision
  collisions; every expires_at NULL; no phantom usersets on concrete
  grants) — split because storetest cannot issue SQL and the projection
  omits subject_relation.
- Premise adaptations: (1) real top-level test name is TestConformance (plan
  said TestConformance_Postgres/_Turso) — filter `TestConformance/.*/
  Concurrent` verified non-empty via -v (8 subtests) before counting a
  green. (2) Guard-revoke race asserts outcome set {applied, stale, denied}
  with losers writing nothing (the forbidden interleaving leaves no distinct
  row signature; the mechanism — shared-scope dependency + revision CAS —
  is the guarantee). (3) SchemaDigest "unset" sentinel untouched (per-backend
  comparisons only) — still the phase-3 reconciliation item.
- Verify — agent ran all; ORCHESTRATOR INDEPENDENTLY RE-RAN: pgx (fresh
  C-collation DB): 8 Concurrent subtests confirmed executing (-v),
  `-race -count=10` PASS (37.5s), StormForensics PASS, DB dropped; turso:
  8 confirmed (-v), `-tags=integration -race -count=10` PASS (43.2s),
  StormForensics PASS; memstore `-race -count=10` Mutations+Parity PASS;
  `make check` "all checks passed"; guard exit 0; containers left running.

### 2026-07-14 — AZ3-2.6 — host upgrade runbook draft — PASS (SQL live-validated both dialects)

Outcome: data-preserving host upgrade runbook drafted at
features/authorization/stores/UPGRADE.md, wrapping CONVERSION.md with the
operational cutover and the gain/lose/retain access-change assessment.
Acceptance met: an adopter can classify v1 data's access fate before
deploying v3.

- Sections: pre-tag posture re-check (zero-tag gate); access-change
  assessment first; backup; maintenance window + old-binary stop; NO mixed
  old/new serving (hard single cutover, reasons stated); invalid-row audit +
  repair (links CONVERSION.md 1a–1e); apply v3 schema then seed revisions
  (export/apply/seed/boot); rollback boundary; migration-source ordering
  (documents BOTH realized host merge patterns — merged primary/ ledger
  (examples/cms) and per-feature MigrationsFS streams (auth-cms); iam_*
  filenames never collide; composed authorization↔events ordering explicitly
  owned by the future effects packet); destructive dev-reset path;
  validation status.
- Assessment design: per-shape decision table off CONVERSION.md 1c —
  concrete + #member retain; non-member usersets and concrete-group grants
  that v1 expanded lose the over-granted access; malformed rows block until
  repaired. Honest limits stated: no automatic gains under v3; ambiguous
  rows are operator decisions, never a guessed member.
- REAL BUG found + fixed in CONVERSION.md by live validation: scope-seeding
  INSERTs listed 4 target columns with 3 SELECT expressions (Postgres
  error). Fixed to 3-column form letting revision DEFAULT 0 fill (matches
  intent); re-validated live. Also retired three stale "AZ3-2.6" forward
  references (CONVERSION.md + both store READMEs → UPGRADE.md links).
- SQL validated LIVE: pg scratch C-collation DB on authv3-pg with a seeded
  v1-shaped fixture (detection queries returned expected rows; corrected
  seeding produced revision-0 anchors; scratch dropped); sqlite3 for the
  turso dialect (detection + both seeding variants clean).
- Verify — orchestrator re-ran: UPGRADE.md present (14.4KB), corrected
  3-column seeding SQL confirmed in CONVERSION.md, `make check` "all checks
  passed", guard exit 0.
- Premise adaptations: placement = stores/UPGRADE.md sibling to
  CONVERSION.md (no authentication-side runbook precedent exists; the
  forward references already pointed here); README/CONVERSION link updates
  are the minimal anti-orphaning wiring.

### 2026-07-14 — AZ3-2.6 ADDENDUM (defect found + corrected during AZ3-5.1 execution)

AZ3-5.1's live execution proved the original §7 data-preserving procedure
text WRONG: canonical 0001/0002 use `CREATE TABLE IF NOT EXISTS`, a no-op
against pre-existing v1 tables — zero CHECK constraints added, malformed
rows NOT blocked (proven live: NOTICE "relation already exists, skipping").
The canonical migration SQL itself is correct for greenfield and unchanged.
Correction landed within AZ3-5.1's publish mandate: UPGRADE.md §7a now adds
the three constraints via explicit `ALTER TABLE … ADD CONSTRAINT`
(PostgreSQL) / table-rebuild (libSQL — no ADD CONSTRAINT support), and that
explicit add is the enforced ambiguity block. Re-executed and proven live on
both dialects. See the AZ3-5.1 entry in 07-docs-and-closeout.md.

### Phase 2 acceptance — 2026-07-14 — GREEN

All six tasks logged PASS. Canonical migrations + filename parity green
(2.1). Memory, pgx, turso pass ONE shared mutation/read conformance suite —
22 Mutations cases + Parity oracle + Budget + Adversarial (2.2–2.5). Both
live dialect concurrency legs RECORDED, not skipped: ×10 -race on each
dialect + SQL storm forensics, independently re-run by the orchestrator
(2.5). Upgrade draft exists before public service promotion (2.6, before
phase 3). `make check` "all checks passed"; `make guard` exit 0. No stop
condition encountered. Phase 3 may begin.
