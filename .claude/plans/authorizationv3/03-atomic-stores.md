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
- Add `iam_authorization_scopes` revision anchors keyed by scope kind/type/ID.
  Resource scopes serialize relationships and scoped roles; subject scopes
  serialize global roles.
- Add `iam_authorization_mutations` receipts keyed by MutationID, storing scope,
  operation, payload digest, resulting revision, disposition, and created time.
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
  transaction-bound guard evaluation, invariant evaluation, row changes,
  revision bump, and receipt persistence. The view passed to the guard reads the
  held snapshot without recursively locking.
- Exact replay returns the original receipt; same MutationID with a different
  payload digest returns conflict and changes nothing.
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
Touch: pgx repositories, tests, optional events appender seam compile slot.

Implement:

- Apply inside one transaction. Lock/create the scope anchor, verify expected
- Run the supplied guard against a transaction-bound decision reader. Lock each
  authorization scope it reads (or use an equivalently proven serializable
  strategy) so concurrent revocation of the actor's grant cannot commit between
  guard allow and mutation.
- Use row/scope locking so two last-owner revokes cannot both commit.
- Make exact replay return its stored receipt without another event or revision.
- Return explicit semantic conflict for a second relation where replacement was
  not requested; implement replacement without a delete/create visibility gap.
- Provide the dialect-typed optional `AppendTx` seam used in events mode, but do
  not import the events feature into the core.
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
- run the guard through a transaction-bound reader under that same write
  transaction, with no callback escape to the outer Service;
- condition writes on current revision and receipt absence;
- make loser outcomes deterministic (`stale`, replay, or invariant blocked), not
  raw `SQLITE_BUSY` where the contract promises an application outcome;
- append an optional event row inside the same transaction; and
- prove rollback leaves relationship/role, revision, receipt, and event unchanged.

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
- receipt/revision/event agreement; and
- context cancellation and infrastructure error mapping.

Run live concurrency cases at least ten times per dialect under `-race`. Inspect
stored rows for invalid usersets, revision gaps, duplicate receipts, and orphaned
events.

Live environment recipe (the auth v3 AV3-9.8 lessons; do not relearn them):

- run against the standing `authv3-pg` (C-collation — pgx test databases must be
  C-collation) and `authv3-libsql` containers with fresh/reset databases per leg;
- always pass an explicit `-count` on live legs so the test cache cannot replay
  results across database resets; and
- harness phases must drive every mutation/job to a durable terminal state
  before stopping runtimes or pools, and wait margins must respect lease/lock
  TTLs against `-race` slowdown — the auth v3 livedelivery flakes were harness
  margins, and a repeat here would mask (or fake) a real race defect.

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
- migration-source ordering with optional events mode (`authorization` and
  `events` tables must both exist before the composed store constructs);
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
- Mutation replay can bump revision or duplicate an event.
- Live race evidence is replaced by a hermetic fake.

## Execution log

Append only during execution.
