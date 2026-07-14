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
