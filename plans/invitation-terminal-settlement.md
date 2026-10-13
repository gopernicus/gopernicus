# invitation terminal settlement — fence, receipt recovery, bounded active listing

**Status:** RELEASED 2026-10-13 — six tags published and cold-verified (table in "Release
record"); PRs #71, #72, #73 (code) and #74 (pins) merged by the owner; release-record docs PR
open. Owner authorized merge + release in-session ("merge them and then release"). RATIFIED
2026-10-13 (owner, in-session: "ratified with your other defaults"; R1, R2,
R4, R5 all as recommended; **R6 added by owner ruling: firestore out of scope** — "we don't need
to worry about firestore anything"). Implementation authorized 2026-10-13.
Direct follow-up to [invitation-grant-operation-ledger.md](invitation-grant-operation-ledger.md);
closes its "Known gaps, follow-up (R5)" item. Drafted from Segovia's upstream request
`segovia/.claude/plans/v2-personal-invitation-recovery-followup.md` (personal invitations stay
gated on it). Owner decided to build it 2026-10-13; design direction accepted in-session.
Reviewed 2026-10-13 by lead-backend-engineer and data-integration-reviewer (both
ship-with-edits; findings folded, see "Consultation notes").

## Context

The 2026-10-06 ledger train (authorization v0.23.0, stores pgx v0.17.0 / turso v0.16.0,
authentication v0.17.0; Segovia pins those plus authentication stores/pgx v0.7.0) made a
**committed** grant safe to retry. Three gaps survived (ledger plan R5; README "Known recovery
limits"):

1. An `accepting` claim cannot be cancelled: `Service.Cancel`
   (`pockets/authentication/logic/invitations/service.go:603`) returns `ErrNotPending`, and a
   first grant that has not committed yet can still land after a manager "removed" the person
   (Segovia's `TestLivePersonalRemovalReportsClaimConflictBeforeLaterFirstGrant`).
2. A host precondition that runs before `Apply` (Segovia's `TenantOf`, auth-cms's existence
   probe) can turn permanently false after the commit; the claim stays `accepting` forever.
3. `ListByResource` returns every status; Segovia caps collection at 5000 rows, so terminal
   history can hide a live pending/accepting row during removal.

The serialization point exists: `iam_operations`. `recordOperation` is `INSERT … ON CONFLICT
DO NOTHING` under the store's write lock (pgx `lockAuthorization` = `LOCK TABLE iam_tuples IN
SHARE ROW EXCLUSIVE MODE`, turso `ownedTransaction` = `BEGIN IMMEDIATE`, memory `state.write`),
and `replayOperation` short-circuits on an existing row. A fence is a ledger row written first.

## Problem

Build, in order: (1) a durable fence on an invitation's operation ID so an in-flight or future
first grant is impossible, or an authoritative "already committed" so the host revokes instead;
(2) a read that lets a host skip its own preflight for an already-recorded operation; (3) a
cursor-paged active-only listing. Same contract on memory, Postgres and Turso; survives
restarts and separate processes; `sdk` untouched; no locked decision relitigated.

## Design

### D1 — `OutcomeFenced` and the fence row (authorization core)

- `mutations.Outcome` gains `OutcomeFenced = "fenced"`; `Valid()` accepts it.
- `mutations.FenceFingerprint` = 64 ASCII `0`s. A fence binds no command; the column stays
  hex-valid so **`ck_iam_operations_fingerprint` is untouched** and `encoding` stays
  `operation/v1`. Only the outcome CHECK widens (D4).
- `replayOperation` (pgx, turso) and the memory lookup: check encoding, then **branch on
  `outcome == fenced` before the fingerprint compare** (never on the sentinel fingerprint),
  skip `readFacts`, return `Result{Outcome: OutcomeFenced, Replayed: true, Superseded: true}`.
  Nothing written, no validator. Segovia's `Replayed && Superseded → ErrGrantSuperseded`
  mapping works unchanged; D6 adds the sharper one.
- **Mixed-version safety:** a v0.23 binary meeting a fenced row compares the fingerprint
  first and returns `ErrOperationMismatch`, which Segovia and auth-cms already map to
  `ErrGrantSuperseded` — no access is granted during a rolling deploy.

### D2 — the `OperationLedger` sibling port, not a wider `MutationRepository`

```go
// logic/mutations/repository.go
type FenceResult struct {
	Outcome  Outcome // OutcomeFenced when this call (or an earlier fence) holds; else the committed outcome
	Recorded bool    // a row already existed; this call wrote nothing
}
// OperationLedger reads and fences the record MutationRepository writes, over the same authority.
type OperationLedger interface {
	// Fence records operation_id as fenced under the write lock; an existing row is returned as is.
	// Refuses an ambient transaction (ErrMutationInsideTransaction). Idempotent: safe to retry after an unknown-outcome commit.
	Fence(ctx context.Context, operationID string) (*FenceResult, error)
	// Recorded reads on the pool, unlocked. false is never proof the operation will not commit later; only Fence is.
	Recorded(ctx context.Context, operationID string) (Outcome, bool, error)
}
```

Why a sibling: `MutationRepository` keeps its one-method shape, so third-party implementers
and the contract tests that pin it are not broken a second train in a row, and the ledger is a
capability that only means something where `OperationID` is honored. Wiring is explicit:
`authorization.Repositories.Operations mutations.OperationLedger` (optional; must be the same
authority as `Mutations`; `Operations` without `Mutations` → `ErrOperationsWithoutMutations`;
added to the typed-nil check list in `constructor.go:44`). `pgx.Repositories`,
`turso.Repositories` populate it with the mutation store value; `memory.Store` gains an
`Operations()` accessor and every hand-built factory sets the field. `mutations.Service` gains
`WithLedger(OperationLedger)`, `Fence`, `Recorded`; without a ledger both return
`ErrOperationLedgerNotConfigured` (wraps `sdk.ErrInvalidInput`). IDs validate as
`Command.OperationID` does (`tuples.ValidateRefField`). The storetest family **fails** (not
skips) when `Mutations` is wired and `Operations` is nil.

### D3 — fence semantics (every store)

Exactly `Apply`'s order and boundary. pgx: refuse ambient tx → `SET TRANSACTION … READ
COMMITTED` → `lockAuthorization` → INSERT → SELECT, inside `retryMutation`. turso: refuse
ambient tx → `ownedTransaction` (`BEGIN IMMEDIATE`). memory: its own path under `s.mu` (not
`state.write`, which demands an audit source).

1. `INSERT (operation_id, 'operation/v1', FenceFingerprint, 'fenced', now) … ON CONFLICT DO NOTHING`.
2. 1 row → `{OutcomeFenced, Recorded: false}`.
3. 0 rows → `SELECT outcome` → `{outcome, Recorded: true}`; **no row after a conflict is an
   error**, never success. `fenced` = idempotent success; `applied`/`no_change`/`not_found` =
   the grant already committed (`not_found` committed nothing; a revoke then finds nothing).

The lock is load-bearing: without it an Apply that passed its replay lookup before the fence
committed would hit `recordOperation`'s `RowsAffected != 1` — the new `ErrConcurrentMutation`
path this plan says cannot exist. With it, Fence and Apply are totally ordered and an in-flight
Apply sees the fence at its **replay lookup**. Fence writes no tuple, no audit row, wakes no
cache, needs no `audit.WithSource`. Ambient transactions are refused because an in-tx caller
would block on its own locks (pgx) or spin 200 `retryBusy` attempts (turso). `Recorded` on a
turso replica route can be stale-false (safe direction; it re-runs the preflight) — the doc
says to use a primary route. Retention unchanged: fence rows are never deleted.

### D4 — migration `0004_iam_operations_fenced.sql` (pgx + turso) — a one-way door

- pgx, **one statement**: `ALTER TABLE iam_operations DROP CONSTRAINT ck_iam_operations_outcome,
  ADD CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change',
  'not_found', 'fenced'));` — unqualified; `pgxdb.WithSchema`'s `SET LOCAL search_path`
  resolves it; no `NOT VALID` (the probe rejects `!convalidated`). New catalog constant in
  `schema.go`: `CHECK ((outcome = ANY (ARRAY['applied'::text, 'no_change'::text,
  'not_found'::text, 'fenced'::text])))`.
- turso, **table rebuild** inside the host's migration transaction (`RunMigrations` runs the
  stream in one `BEGIN IMMEDIATE`): `CREATE TABLE main.iam_operations_next (…widened CHECK, else
  byte-identical to 0003…)`, `INSERT INTO … (explicit columns) SELECT (explicit columns)`,
  `DROP TABLE main.iam_operations`, `ALTER TABLE main.iam_operations_next RENAME TO iam_operations`
  — create/copy/drop/rename in that order (renaming the old table first redirects references).
  SQLite stores the header as `CREATE TABLE "iam_operations" (`; the probe slices from the
  first `(`, so **`schema.go` must take `iam_operations`' canonical body from 0004's
  `iam_operations_next` CREATE** (regex and the exactly-3 count change; `operations=false` for
  `RelationshipRepository` still counts correctly). Caveats for UPGRADE.md: a host view/trigger
  naming `iam_operations` fails 0004 loudly; host-added indexes on it are dropped; hosted sqld
  bounds interactive transactions (verify the current limit) — the copy is one server-side
  statement, so only very large ledgers care.
- **Hard cutover.** Both probes are exact-match: after 0004 a v0.23/v0.17/v0.16 instance
  refuses construction on restart; the CHECK cannot be narrowed back once a fenced row exists,
  and deleting fences re-opens grants. UPGRADE.md + handoff: apply 0004 in the same deploy as
  the new binaries, no old-instance restarts afterward, no downgrade once fenced (R5). Required
  only for `Repositories` users; `RelationshipRepository` probes with `operations=false`.
- Numbering 0004 in both stores (0002 is `tuple_cache_migrations/`). Hosts copy it verbatim
  as their next ledger entry (Segovia: `0023`).

### D5 — `Fencer`, the optional host seam beside `Granter` (authentication)

```go
type FenceInput struct{ OperationID string } // the RAW invitation ID, the value GrantInput.OperationID carries
type FenceOutcome string
const (
	FenceOutcomeFenced    FenceOutcome = "fenced"    // fenced now or earlier: no first grant can ever commit
	FenceOutcomeCommitted FenceOutcome = "committed" // a grant committed under this ID (applied, no_change or not_found)
)
type Fencer interface{ Fence(ctx context.Context, in FenceInput) (FenceOutcome, error) }
```

The pocket passes the raw ID; the host applies its namespace (`"invitation:" + id`) through
**one helper shared with its Granter** — different namespaces fence nothing, and the doc says a
host proves it with a fence-then-grant → `ErrGrantFenced` test (task 5 does). The host detaches
any ambient transaction first, as its Granter does. Wiring: `invitations.WithFencer(Fencer)`;
`auth.InvitationsConfig.Fencer`; Fencer without Granter → `ErrFencerWithoutGranter`. Mapping:
`OutcomeFenced → FenceOutcomeFenced`, else `FenceOutcomeCommitted`. Without a Fencer, Cancel on
an accepting row keeps `ErrNotPending` (decided; the README names the Fencer as the remedy).

### D6 — `Cancel` on an `accepting` claim; two new sentinels

`ErrAlreadyGranted` and `ErrGrantFenced`, both wrapping `sdk.ErrConflict`.

| row (re-read, not the snapshot) | Fencer | Cancel does | returns |
|---|---|---|---|
| pending | any | `UpdateStatus` → cancelled (today) | nil |
| pending snapshot, `UpdateStatus` → `ErrConflict` | any | **re-`Get`**; same token and now `accepting` → the accepting rows below; else | `ErrConflict`/`ErrNotPending` as today |
| accepting | nil | nothing | `ErrNotPending` |
| accepting | set, `Fence` errors | nothing (claim untouched) | the error |
| accepting | set → fenced | `CancelAcceptance` (D7) with the row's own claim; management event `TypeInvitationCancelled` details `claim_status=accepting, fence=fenced` | nil |
| accepting | set → committed | `CompleteAcceptance` with the row's claim (`accepted` is the true terminal state and releases the tuple); no Granted event, no member-added notice; management event `TypeInvitationCancelled` `StatusBlocked` reason `already_granted` | `ErrAlreadyGranted` — host revokes via its ordinary path (also for `no_change`: the host, not the pocket, knows whether access predates the invitation) |
| anything else | any | nothing | `ErrNotPending` |

`PreparedManagement.checked` returns the pinned snapshot (`management.go:57`), so the
pending→accepting race (the case this plan exists for) is handled by the re-read row, never
by returning 409. After a fenced result, `CancelAcceptance` may conflict because a racing
accept retry finished first; re-`Get`: `cancelled` ∧ matching claim → nil (idempotent);
`accepted` → `ErrAlreadyGranted` (fails toward a revoke, never a silent leak: this is the
degraded posture below, or a namespace mis-wiring); anything else → the conflict.

`acceptClaim`: a Granter returning **`ErrGrantFenced`** (recommended mapping for a replay with
`Outcome == OutcomeFenced`) is checked **before** the `ErrGrantSuperseded` test at
`service.go:548`; the service calls `CancelAcceptance`, records `TypeInvitationGranted`
`StatusBlocked` with reason `fenced` (`recordGrant` takes a reason), sends no notice, returns
`ErrGrantFenced`. A host still mapping fenced → `ErrGrantSuperseded` (every un-upgraded host
today) finalizes the row as `accepted` with no access — safe, but the degraded posture;
README + handoff push the `ErrGrantFenced` mapping. `ResolveInvitations` keeps
warn-and-continue for both. The bundled `POST /auth/invitations/{id}/cancel` route keeps
`RespondJSONDomainError` (409 for every conflict); hosts needing the revoke-instead branch call
`Service.Cancel` directly, as Segovia does.

### D7 — repository: `CancelAcceptance`, not a wider `StatusUpdate`

No store has a status CHECK on `invitations`; the gate is `UpdateStatus`'s own
`WHERE status IN ('pending','expired')`, whose "unclaimed" contract stays true. Mirror
`CompleteAcceptance` instead:

```go
// CancelAcceptance terminates only the matching durable claim: accepting → cancelled, keeping the
// bound subject and token, AcceptedAt stays zero, no expiry predicate (an accepting row past expiry
// cancels). A matching cancelled row is returned unchanged. accepted, pending, declined, cancelled-
// via-UpdateStatus, another token or subject are ErrConflict; unknown id is ErrNotFound.
CancelAcceptance(ctx context.Context, id string, claim Acceptance) (Invitation, error)
```

`lifecycle.go` gets `Invitation.CancelAcceptance(a Acceptance)`. pgx/turso: one conditional
`UPDATE … WHERE status='accepting' AND token_hash=? AND resolved_subject_type=? AND
resolved_subject_id=? RETURNING`; the fallback read **must not reuse** `acceptanceResult(…,
complete bool)` as is (it returns a matching `accepted` row as success) — give it a target-status
parameter that accepts only a matching `cancelled` row. firestore: compile-only stub
returning `errors.ErrUnsupported` (R6); the shared conformance suite skips the
`CancelAcceptance*` cases when the adapter reports unsupported. The partial unique index frees
the tuple for `cancelled`. **No authentication migration.**

### D8 — `ListActiveByResource` (struct input, fixed active statuses)

```go
type ActiveListInput struct {
	ResourceType, ResourceID string // required; Validate() → ErrInvalidInput when blank
	Relation                 string // optional exact filter
}
ListActiveByResource(ctx context.Context, in ActiveListInput, req list.Request) (list.Page[Invitation], error)
```

Repository and service (policy-free like `ListByResource`, which is unchanged); `Validate()`
lives in core and every store calls it (pinned by conformance). Filter is the **stored** status
— never the wall clock: an expired-but-pending row still reserves the tuple (`Active()`, the
partial index, firestore's claim doc all agree) and must be listed so the host can cancel it.
SQL writes the statuses as **literals in `BaseSQL`** — `… AND status IN ('pending','accepting')
[AND relation = ?]` — never bound, or neither planner can match the partial index
`idx_invitations_pending_tuple` (0018) whose predicate this is; `idx_invitations_resource`
remains a competing full-history plan, so R4 "no index" is conditional on EXPLAIN tests (turso
local-file `EXPLAIN QUERY PLAN` names the partial index; pgx EXPLAIN after seeding + ANALYZE,
`explain_live_test.go` precedent). Count and Search parity: counts apply the same filter;
non-blank Search is refused on all five stores. Keyset newest-first will not see a row created
mid-loop (documented; the host's final cancel loop is bounded by the active set, not history).
Firestore (R6): compile-only stub returning `errors.ErrUnsupported`; no composites, no
`indexes.json`/`SCHEMA.md` change; conformance skips `ListActiveByResource*` for it.

### Proofs — Segovia's seven, mapped

| # | proof | tests (module) |
|---|---|---|
| 1 | cancel before claim: old token rejected, no ledger side effect | `TestCancelPendingThenAcceptRejects` (authentication `logic/invitations`; fake Fencer asserts zero calls) |
| 2 | pause after claim / before grant: cancel wins, resumed grant fenced | `TestCancelAcceptingFencesPausedGrant`, `TestCancelPendingSnapshotRacedByClaimFences` (authentication; blocking fake Granter + fake Fencer over one fake ledger); storetest `OperationLedger/FenceBeforeApply`, `FenceSeenAtReplayLookup` (memory, pgx, turso); `TestInvitationCancelDuringPausedGrantThroughHTTP` (auth-cms) |
| 3 | pause after grant / before completion: revoke, retry settles, nothing recreated | existing `TestInvitationCommittedGrantRecoveryThroughHTTP` (auth-cms); `TestCancelAcceptingAfterCommittedGrantReturnsAlreadyGranted` (authentication; no Granted event, no notice); storetest `OperationLedger/FenceAfterApply` |
| 4 | grant vs cancel in separate connections: one durable ordering | `TestFenceContentionTwoConnections` in pgx and turso `mutations_live_test.go` (same build tag as the file): two handles; a raw tx on A holds the write lock; `Fence` (B) and `Apply` (A, second conn) launched. pgx: poll `pg_locks WHERE NOT granted` on the schema-qualified `iam_tuples` regclass until both wait, then release. turso: no wait observability — run K iterations, hold the lock well under hosted sqld's transaction limit. Assert exactly one of {applied ∧ fence→committed, fenced ∧ apply→replayed fenced} **and** final state (ledger outcome, tuple present/absent); plus both pure orderings |
| 5 | resource missing before recovery: recorded receipts settle, uncommitted cannot grant, restore never revives | `TestInvitationRecordedOperationSettlesAfterResourceDeleted`, `TestFencedInvitationNotRevivedByResourceRestore` (auth-cms: Granter consults `Recorded` before the existence probe); storetest `OperationLedger/RecordedAfterTeardown`, `RecordedAbsent` |
| 6 | role replacement / mismatch: preserve new standing, reject mismatch | storetest `OperationLedger/FenceIgnoresCommandShape` (any command under a fenced ID → fenced replay, never `ErrOperationMismatch`, no write); existing `Mismatch`, `ReplayAfter/*`; conformance `Invitations/CancelAcceptanceMismatch` (wrong token/subject **and** wrong status: accepted, pending, declined, cancelled-via-UpdateStatus → `ErrConflict`, row unchanged); `TestFencerNamespaceMatchesGranter` (auth-cms fence-then-grant → `ErrGrantFenced`) |
| 7 | restart at each boundary, duplicate cancels/accepts, resend | storetest `OperationLedger/FenceIdempotent`, `FenceInsideTransactionRefused`, `FenceConcurrentWithApply` (~4 goroutines); `TestFenceRestart` (pgx, turso; fresh handle replays fenced); conformance `CancelAcceptanceIdempotent`, `CancelAcceptancePastExpiry`, `CancelThenCompleteConflicts`, `ConcurrentCompleteCancel` (one winner, terminal ∈ {accepted, cancelled}); `TestCancelAcceptingTwiceIsIdempotent`, `TestResendAfterFencedCancelNotPending` (authentication) |

Listing: conformance `Invitations/ListActiveByResourceHidesTerminalHistory` (~8 terminal rows,
`Limit: 2`, an expired-but-pending row that must appear, exact set/order, `AcceptedAt` zero after
cancel-from-accepting) and `ListActiveByResourceRelationFilter`, on pgx/turso/
reference/authmem (firestore skipped, R6). Volume is not the proof; Segovia's >5000-row proof is downstream.

## Out of scope

- `Apply` joining an ambient transaction; any change to the baseline `RelationshipWriter`.
- Ledger purge/retention; recording requested facts on the row (ledger plan R6).
- Cancelling an `accepting` claim without a Fencer (stays `ErrNotPending`).
- HTTP exposure of fence/recorded on bundled routes; a bundled revoke-instead response shape.
- A `not_found` committed outcome that hosts treat as failure (replays as an error forever) —
  README note only.
- The authorization firestore store (unmerged); if it lands it owes `OperationLedger`.
- **Firestore authentication store behavior (R6).** It stays in the workspace, so the two new
  repository methods get compile-only stubs returning `errors.ErrUnsupported`; the conformance
  suite skips those cases for it; no composites, no SCHEMA.md, no firestore tag. A host on
  firestore keeps today's `ErrNotPending`-on-accepting posture; a later plan owes the real
  implementation if a firestore host ever needs it.
- The Segovia adoption itself (handoff below).

## Tasks

Each task ends with `go build ./... && go test ./... && go vet ./...` in the touched module,
goimports clean, `make guard`; `make check` closes tasks 5 and 6. Turso remote suites run
~25 min: authorization splits `-run 'TestConformance$'` / `-skip 'TestConformance$'`
(`-timeout 45m`, `AUTHORIZATION_TURSO_DISPOSABLE_URL` must name the one truncate-safe
playground URL); the **authentication** turso suite is `go test -tags=integration -run
TestConformance_Turso` and has **no** disposable guard — confirm `TURSO_DATABASE_URL` by hand.

### task-1: authorization core — fenced outcome, `OperationLedger`, memory store, conformance

- **depends_on:** []
- **model:** opus
- **files:** `pockets/authorization/logic/mutations/result.go`, `repository.go`, `operation.go`
  (`FenceFingerprint`), `service.go` (`ErrOperationLedgerNotConfigured`), `mutation_service.go`
  (`WithLedger`, `Fence`, `Recorded`), `pockets/authorization/config.go`
  (`Repositories.Operations`), `constructor.go` (`ErrOperationsWithoutMutations`, typed-nil
  list), `stores/memory/memory.go` (`Operations()`), `mutations.go` (fence under `s.mu`, fenced
  replay branch, `Recorded`), `stores/memory/conformance_test.go`, `lookup_snapshot_test.go`,
  `stores/storetest/storetest.go`, `operation_ledger.go` (the proofs-table cases),
  `mutation_service_test.go`, `README.md` ("Durable operation replay": fence/recorded, mapping
  table, mixed-version note, `not_found` note), `stores/UPGRADE.md` (0004 entry, hard cutover).
- **verify:** `cd pockets/authorization && go build ./... && go test -race ./... && go vet ./... && cd ../.. && make guard`
- **description:** Add `OutcomeFenced` + `FenceFingerprint`, the port and `FenceResult`,
  service methods and `Repositories.Operations` wiring, the memory implementation and fenced
  replay, and the storetest family pgx/turso inherit in task 2.

### task-2: authorization stores pgx + turso — 0004, fence/recorded, probes, two-connection proofs

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/authorization/stores/pgx/migrations/0004_iam_operations_fenced.sql`,
  `operations.go`, `mutations.go` (`Fence`/`Recorded` on `mutationStore`), `postgres.go`
  (`Operations:`), `schema.go` + `canonical_schema_test.go` (new catalog constant, rejection
  of the 0003-only CHECK), `migrations_test.go`, `fresh_schema_test.go`, `conformance_test.go`,
  `mutations_live_test.go` (`TestFenceContentionTwoConnections`, `TestFenceRestart`,
  `TestFenceRefusesAmbient`); turso twins under `pockets/authorization/stores/turso/`
  (`migrations/0004_iam_operations_fenced.sql` rebuild, `operations.go`, `mutations.go`,
  `turso.go`, `schema.go` canonical-from-0004 + regex/count, canonical fixture,
  `migrations_test.go` incl. 0001+0003→seed each outcome→0004 byte-exact upgrade test and a
  host-view-fails case, `fresh_schema_test.go`, `conformance_test.go`, `mutations_live_test.go`);
  `pockets/authorization/stores/UPGRADE.md`.
- **verify:** pgx `POSTGRES_TEST_DSN=… go test -race ./...` (and with `POSTGRES_TEST_SCHEMA`);
  turso local-file suite, then remote split as above; both `go vet -tags=integration ./...`; `make guard`.
- **description:** Land migration + probe fixtures first (an un-migrated database fails loudly
  from the first commit), then the SQL `OperationLedger` under the existing lock, then the
  two-handle contention and restart tests.

### task-3: authentication core — `Fencer`, `Cancel` on accepting, `CancelAcceptance`, `ListActiveByResource`

- **depends_on:** []
- **model:** opus
- **files:** `pockets/authentication/logic/invitations/service.go` (`Fencer`, `FenceInput`,
  `FenceOutcome`, `ErrAlreadyGranted`, `ErrGrantFenced`, `Cancel` with re-read, `acceptClaim`
  fenced branch before `:548`, `recordGrant` reason, `ListActiveByResource`, `Granter` doc),
  `options.go` (`WithFencer`), `constructor.go`, `repository.go` (`CancelAcceptance`,
  `ListActiveByResource`, `ActiveListInput` + `Validate`), `lifecycle.go` + `lifecycle_test.go`,
  `recovery_test.go`, `management_test.go`, `service_test.go` (fake repo), `order.go` doc,
  `pockets/authentication/config.go` (`InvitationsConfig.Fencer`), `constructor.go`
  (`ErrFencerWithoutGranter`), `auth_test.go` (fake repo), `logic/authentication/securityevent/
  securityevent.go` (doc of new details/reasons; no new type), `stores/storetest/reference_test.go`,
  `storetest.go`, `invitation_lifecycle.go` (the conformance cases in the proofs table),
  `pockets/authentication/README.md` ("Known recovery limits" → "Terminal settlement": Fencer
  wiring + shared namespace helper + detach, `ErrGrantFenced` vs `ErrGrantSuperseded`,
  `ErrAlreadyGranted` semantics incl. `no_change`, `ListActiveByResource`, the 0004 cutover).
- **verify:** `cd pockets/authentication && go build ./... && go test -race ./... && go vet ./... && cd ../.. && make guard`
- **description:** Seam, sentinels, lifecycle transition, port methods, service behavior per
  D5–D8, conformance cases proven by the reference store, README rewrite. No migration.

### task-4: authentication stores pgx + turso + authmem — port parity (firestore stubs only)

- **depends_on:** [task-3]
- **model:** opus
- **files:** `pockets/authentication/stores/pgx/invitations.go` (+ EXPLAIN live test),
  `pockets/authentication/stores/turso/invitations.go` (+ `EXPLAIN QUERY PLAN` test),
  `pockets/authentication/stores/firestore/invitations.go` (two `errors.ErrUnsupported`
  stubs so the workspace compiles — nothing else, R6);
  `examples/auth-cms/internal/authmem/ports_v2.go` and the fake in
  `examples/auth-cms/cmd/server/invitation_recovery_test.go` (**in this PR**, or workspace
  `make check` is red until task 5).
- **verify:** pgx `POSTGRES_TEST_DSN=… go test -race ./...`; turso local + `-tags=integration
  -run TestConformance_Turso` remote; firestore `go build ./... && go vet ./...` only;
  `go vet` incl. integration tags; `cd examples/auth-cms && go build ./... && go test ./...`; `make guard`.
- **description:** Implement `CancelAcceptance` (target-status fallback) and
  `ListActiveByResource` (literal status predicate) on pgx, turso and authmem so the
  shared conformance suite and the EXPLAIN tests pass; firestore gets the two stubs.

### task-5: examples/auth-cms — reference adoption (Fencer, `Recorded`-first Granter, proofs 2, 5, 6)

- **depends_on:** [task-1, task-2, task-3, task-4]
- **model:** opus
- **files:** `examples/auth-cms/cmd/server/membership.go` (one `invitationOperationID` helper;
  `integrityOperationFencer` over `components.Mutations.Fence`; Granter consults `Recorded`
  before the existence probe; `OutcomeFenced` replay → `ErrGrantFenced`),
  `cmd/server/authorization.go` (memory factory sets `Operations`), `cmd/server/main.go` (wiring
  only: `InvitationsConfig.Fencer`), `cmd/server/invitation_recovery_test.go` (pausable Granter;
  the auth-cms tests in the proofs table), `examples/auth-cms/README.md` (Leg 4, "What it proves").
- **verify:** `cd examples/auth-cms && go build ./... && go test -race ./cmd/server -run 'TestInvitation|TestFencer' -count=1 && go test ./... && go vet ./...`; `make guard`; then `make check`. **Run-and-look:** `make run`, invite, accept with the Granter paused (test hook), cancel from the admin list → 200; release → access absent, invitation `cancelled`; committed-then-cancel variant → 409 `already_granted`, invitation `accepted`, host revoke clears access.
- **description:** The first-party host exercises the whole contract over real HTTP, including
  paused-grant cancellation, deleted-resource settlement and the namespace self-check.

### task-6: release

- **depends_on:** [task-5]
- **model:** opus
- **files:** `RELEASING.md`, pins in authorization stores, authentication stores,
  `examples/auth-cms`; `plans/invitation-terminal-settlement.md` (tracked copy) +
  `plans/invitation-terminal-settlement-release-manifest.json`.
- **verify:** `make check` on each exact tag commit; cold verification per RELEASING.md
  (`GOWORK=off`, empty caches, proxy `.info` polled before any `go mod tidy`).
- **description:** PRs: #A = tasks 1+2 (the conformance family needs the adapters, as with #58);
  #B = tasks 3+4; #C = task 5 + public pins. Tags (owner word, one command per step):
  `pockets/authorization/v0.24.0` → `pockets/authorization/stores/pgx/v0.18.0`,
  `pockets/authorization/stores/turso/v0.17.0` → `pockets/authentication/v0.18.0` →
  `pockets/authentication/stores/pgx/v0.8.0`, `pockets/authentication/stores/turso/v0.7.0`.
  No firestore tag (R6). All MINOR (new port methods; one required host migration). `sdk`
  untouched.

### task-7: Segovia handoff

- **depends_on:** [task-6]
- **model:** sonnet
- **files:** this plan's "Segovia handoff" (exact tags/commits), `RELEASING.md` note.
- **verify:** links resolve; versions match the published `.info` records.
- **description:** Finalize the handoff below; no Segovia code.

## Sequencing

task-1 → task-2 and task-3 → task-4 are independent trains; task-5 joins them; task-6/7 close.
## Risks

- **Turso 0004 rebuild + canonical probe:** a byte-level mismatch bricks construction for every
  turso host. Mitigation: the upgrade test (0001+0003→seed→0004) and fresh-schema test both
  compare `sqlite_master` to the fixture; the implementer diffs real output into the fixture.
- **Hard cutover (R5):** old instances cannot restart after 0004; no downgrade once fenced.
  Documented in UPGRADE.md and the handoff; mixed-version replay is safe (D1).
- **Degraded fenced posture** on hosts that keep the `ErrGrantSuperseded` mapping: an
  `accepted` row with no access; Cancel fails toward `ErrAlreadyGranted` (a revoke of nothing).
- **Firestore stubs (R6):** a firestore host that wires a `Fencer` gets `ErrUnsupported` from
  `CancelAcceptance`; the authentication README names this.

## Rulings (taken 2026-10-13)

All four below ruled as recommended; R6 added by the owner.

- **R6 — firestore out of scope.** The authentication firestore store receives compile-only
  `errors.ErrUnsupported` stubs for the two new port methods; no composites, no index tests,
  no tag, no emulator proofs. Reason: owner ruling; no firestore host needs this today.


- **R1 — port shape.** Sibling `OperationLedger` + `Repositories.Operations` (recommended;
  both leads endorse) vs. widening `MutationRepository`. Default: sibling.
- **R2 — `ErrGrantFenced`.** Add the distinct sentinel so a fenced replay terminates as
  `cancelled` (recommended) vs. `ErrGrantSuperseded` only (row ends `accepted`, no access).
  Default: add it; both mappings stay safe; Cancel returns `ErrAlreadyGranted` in the degraded case.
- **R4 — authentication listing index.** No migration, conditional on the EXPLAIN tests
  proving the partial index is chosen (recommended) vs. ship `idx_invitations_resource_active`
  now. Default: none; if EXPLAIN fails, add the index in task 4 as authentication `0020`.
- **R5 — the 0004 one-way door.** Accept (recommended; the alternative is a parallel
  `iam_operation_fences` table with its own probe, two serialization points and a second
  lookup in `Apply`). Default: accept, document the no-rollback rule.

## Consultation notes

**lead-backend-engineer (ship-with-edits), all folded:** Fence refuses ambient transactions
(self-deadlock) → D2/D3/handoff; `Cancel` re-reads on conflict so the pending→accepting race
takes the fence path → D6; PR #B carries authmem + the auth-cms fake or `make check` is red →
task 4; memory Fence bypasses `state.write`'s audit demand → D3; turso canonical body from 0004
→ D4; `Operations` wiring holes (`ErrOperationsWithoutMutations`, typed-nil list,
`memory.Store.Operations()`, factories) → D2/tasks; degraded case returns `ErrAlreadyGranted`
→ D6; `ErrGrantFenced` before `:548`, `recordGrant` reason → D6; `ReasonFor` bullet dropped;
proof-4 `pg_locks` barrier / turso iterate / final state → proofs; literal `IN` → D8;
mixed-version safety → D1.

**data-integration-reviewer (ship-with-edits), all folded:** turso rebuild order, fixture
rule, view/trigger/index and hosted-transaction caveats → D4; pgx single `ALTER … DROP, ADD`,
no `NOT VALID`, catalog constant, hard cutover, "required" only for `Repositories` → D4;
`acceptanceResult(complete)` reuse trap + wrong-status cases → D7; partial-index predicate
literal and EXPLAIN-proven → D8/R4; firestore claim release automatic, index-test touchpoints,
deploy-before-binary → D7/D8/task 4; authentication turso suite name + no disposable guard →
Tasks; `Validate` in core, count/Search parity, `AcceptedAt` zero, no expiry predicate,
`Recorded` on replicas, Fence retry safety → D2/D7/D8; test sizes → proofs.

## Post-build reviews — 2026-10-13

Three read-only reviews on the built branches (PR A `feat/authorization-operation-fence`
713de37a+27189616; PR B `feat/authentication-terminal-settlement` 224e3eb1+c0e701b8). All
three: **ship-with-edits, no blocker**; every edit below was folded on its branch before PR.

- **data-integration-reviewer, PR A.** SQL, lock discipline, fenced-replay ordering and the
  conformance family correct. Edits: UPGRADE.md/turso README wrongly said a host trigger makes
  0004 fail — verified on sqlite 3.43 that triggers `ON iam_operations` and host indexes are
  SILENTLY DROPPED (only a view fails); tests now pin that. Audit-family case: `Fence` needs no
  audit source and writes no audit row. `Recorded` joins an ambient transaction when present
  (decided in-session; `Fence` still refuses one). turso `Recorded` and pgx `Fence` map errors
  like their siblings. pg_locks barrier filtered to the current database.
- **data-integration-reviewer, PR B.** Stores, D6 paths and heal-on-retry all correct; the
  ErrUnsupported skip cannot hide a pgx/turso failure. Edits: pgx EXPLAIN seed spreads active
  rows across history so the partial index must be selective on its prefix; turso EXPLAIN
  asserted before AND after ANALYZE; four heal-path service tests; README firestore wording.
  Deferred to the owner: pending-snapshot re-read finding the same token `accepted` returns
  bare `ErrConflict` (plan: "as today"); reviewer would map it to `ErrAlreadyGranted` when a
  Fencer is configured.
- **platform-sre.** Mixed-version replay verified safe (v0.23 compares fingerprint first).
  Edits: pgx 0004 ACCESS EXCLUSIVE lock has no timeout inside the single migrate transaction
  and a queued ALTER stalls every v0.23 `Apply` → runbook: own migrate run, `lock_timeout` via
  DSN, retry; explicit deploy order (freeze scale-out → migrate → roll binaries → never start
  an old revision); two-phase rollout (phase 1 = v0.24 + 0004 with NO Fencer, reversible by a
  self-guarding reverse ledger entry; phase 2 = wire the Fencer, the one-way door); boot
  self-check `Recorded(ctx,"boot-probe")` when a Fencer is wired; distinct
  `accepted_after_fence` reason + Warn log on the fence-then-accepted branch and a
  "removed-but-has-access" diagnosis runbook. **R6 consequence (H1):** published firestore
  store v0.2.0 does not compile against authentication v0.18.0, so firestore hosts cannot
  upgrade at all — documented as "firestore hosts stay on authentication ≤ v0.17.x" (default;
  owner may instead tag `stores/firestore/v0.3.0` with stubs + pin bump). L1: the two trains
  are independent at the Go module level; store pins must move (authorization stores →
  authorization v0.24.0; authentication stores → authentication v0.18.0) and each store tag
  follows its core's proxy `.info`. L2: label implementer-breaking changes in the record
  (`InvitationRepository` +2 methods; `Outcome.Valid()` accepts `fenced`). Add
  `authorization/stores/goredis v0.4.0` to the cold consumer check.

### Turso verification standard for this train

The hosted playground `libsql://gopernicus-cms-playground-…` is gone (404 "group not found"
at the platform level, confirmed by a direct HTTP probe 2026-10-13). Per the 2026-10-06
precedent (RELEASING.md: "Memory, isolated local PostgreSQL, SQLite and HTTP libSQL behavior
passed before release. Hosted Turso … remain[s] unverified"), verification is: local-file
SQLite suites + the same suites against an isolated local `ghcr.io/tursodatabase/libsql-server`
over HTTP (Hrana; exercises the four-statement 0004 rebuild inside the migration runner's
transaction). Final-commit results over local HTTP libSQL: authorization **41bbbc47** full
`-tags=integration -race` suite 806/806 (0 skip) incl. `TestOperationsFencedUpgrade`,
`TestOperationsFencedUpgradeDropsHostTriggersAndIndexes`, `TestFenceContentionTwoConnections`
(both orderings observed), `TestFenceRestart`, `FenceWritesNoAudit`, `RecordedJoinsTransaction`;
authentication **c4e48f3c** `TestConformance_Turso` 280/280 (0 skip) incl. all nine new
`Invitations/*` cases. pgx suites on both final commits passed against isolated local
PostgreSQL 17 (default schema + a schema leg). Hosted
Turso and the hosted sqld transaction bound remain UNVERIFIED and the release record must say
so; Turso hosts dry-run 0004 on a branched copy first. Rule for this train: the authentication
turso integration suite (no disposable-URL guard) runs ONLY against a local file or local sqld.

## Build record — 2026-10-13

- **PR A** `feat/authorization-operation-fence` @ **41bbbc47** (713de37a task-1, 27189616
  task-2, 41bbbc47 review edits). **PR B** `feat/authentication-terminal-settlement` @
  **52ed3073** (224e3eb1 task-3, c0e701b8 task-4, cfe52707 + cc322dc0 review edits, c4e48f3c
  README wording, 52ed3073 firestore stub message — cherry-picked from the host branch so PR B
  passes `make check` alone). **PR C** `feat/invitation-settlement-reference-host` = main +
  both merges + 4a78ea33 (task-5).
- **Task-5 run-and-look (real HTTP, in-memory auth-cms on :8082, temporary never-committed
  pause hook):** A) grant paused before commit → cancel 200 `cancelled`, released accept 409,
  late accept 409, member route 403, events `invitation_cancelled success {fence: fenced}` then
  `invitation_granted blocked {reason: fenced}`. B) pending cancel → accept 409. C) accepted →
  cancel 409, access kept. D) paused AFTER grant → cancel 409 with event
  `invitation_cancelled blocked {reason: already_granted}`; the released accept then returns
  200 and records `invitation_granted success` — truthful (the grant committed) and the
  member-added notice is deduplicated by `member_added:<id>`; accepted as intended behavior.
- **Deviations recorded:** auth-cms Granter/Fencer stay in `cmd/server/membership.go` (plan's
  file list; architecture-steward item "behavior growth in cmd/" stands as a follow-up); the
  auth-cms Fencer detaches no transaction because the in-memory host has none (doc'd in code);
  `ErrOperationLedgerNotConfigured` keeps wrapping `sdk.ErrInvalidInput` (sdk has no permanent
  5xx sentinel; sibling `ErrMutationsNotConfigured` does the same; boot self-check covers it);
  task-6 verify pattern is `-run 'TestInvitation|TestFence'` (the plan's `TestFencer` misses
  `TestFencedInvitationNotRevivedByResourceRestore`).
- **Test-infra notes:** shared `postgres` DB in the throwaway container accumulates ledger
  rows that fail pgxdb `TestLive_OpenAndMigrate`; use a fresh database per `make check`. The
  pgx authentication default-leg catalog probes are not schema-qualified, so run the default
  leg before a `POSTGRES_TEST_SCHEMA` leg or drop the schema between.

## Recommended reviews

product-manager (scope: three items, nothing else rides along); platform-sre (0004 phasing,
hard cutover, firestore index deploy ordering, three-train tag order); architecture-steward
(sibling port and `Repositories.Operations` placement; behavior growth in auth-cms `cmd/`);
data-integration-reviewer post-build on the SQL/firestore adapters and the EXPLAIN tests.

## Release record — 2026-10-13

The owner authorized merge and release in-session. Code PRs #71 (authorization fence), #72
(authentication settlement) and #73 (auth-cms adoption, stacked) merged in order; pins PR #74
followed. Classifier policy required the owner to run each `gh pr merge`; tags, pushes and
verification ran in this session.

| Module | Published tag | Commit |
| --- | --- | --- |
| Authorization | `pockets/authorization/v0.24.0` | `b7c8a73a28bc581a473fcdab783134b0ef08e8b0` |
| Authentication | `pockets/authentication/v0.18.0` | `b7c8a73a28bc581a473fcdab783134b0ef08e8b0` |
| Authorization PostgreSQL adapter | `pockets/authorization/stores/pgx/v0.18.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |
| Authorization Turso adapter | `pockets/authorization/stores/turso/v0.17.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |
| Authentication PostgreSQL adapter | `pockets/authentication/stores/pgx/v0.8.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |
| Authentication Turso adapter | `pockets/authentication/stores/turso/v0.7.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |

No firestore tag (R6). Both tag commits passed the full local `make check` against a fresh
local PostgreSQL 17 database and main CI
([b7c8a73a](https://github.com/gopernicus/gopernicus/actions/runs/38453166494),
[8c9b56e7](https://github.com/gopernicus/gopernicus/actions/runs/38454562431)). All six proxy
`.info` records resolved on the first poll before any dependent tidy; the pins PR moved the
four adapters and auth-cms (authentication adapters also select sdk v0.10.0, already required
by authentication v0.18.0). Only six tag refs were pushed; no tag was moved or deleted.

Public verification used `GOWORK=off`, initially empty module/build caches, `proxy.golang.org`
and `sum.golang.org`: every archive passed `go mod verify`, standalone build and vet
(integration-tag vet on the Turso adapters); every archive matched its tag source byte for
byte apart from the zip's added repo `LICENSE` and the excluded nested sub-modules. Uncached
tests passed from the archive for both cores and from the exact tag-source checkout
(`GOWORK=off`, public deps) for all four adapters — the adapters' `TestMigrationParity` /
`TestMigrationConstraintParity` read the sibling dialect's migrations by relative path and are
monorepo-only guards (pre-existing since the pockets rename; also true of v0.17.0). A
standalone public consumer built and race-tested against both cores (fence, idempotent
re-fence, `Recorded`, sentinels). `examples/auth-cms` passed standalone build, uncached full
tests and vet against the public cores.

Before release: isolated local PostgreSQL 17 (default + schema legs), local SQLite, and an
isolated local HTTP libSQL server (authorization integration suite 806/806 incl. the 0004
rebuild through the migration runner, two-connection contention with both orderings, restart;
authentication conformance 280/280), plus real-HTTP settlement drives in auth-cms. **Hosted
Turso was not run: the playground database's group no longer exists.** The hosted sqld
transaction bound for 0004 on large ledgers is unverified; Turso hosts dry-run 0004 on a
branched copy. Checksums and provenance:
[release manifest](invitation-terminal-settlement-release-manifest.json).

## Segovia handoff

_Finalized 2026-10-13 (task 7). Published tags (all cold-verified via proxy.golang.org +
sum.golang.org, `GOWORK=off`, empty caches):_

| Module | Tag | Commit |
|---|---|---|
| authorization | `pockets/authorization/v0.24.0` | `b7c8a73a28bc581a473fcdab783134b0ef08e8b0` (PR #71/#73 merge) |
| authentication | `pockets/authentication/v0.18.0` | `b7c8a73a28bc581a473fcdab783134b0ef08e8b0` (PR #72/#73 merge) |
| authorization pgx | `pockets/authorization/stores/pgx/v0.18.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` (PR #74) |
| authorization turso | `pockets/authorization/stores/turso/v0.17.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |
| authentication pgx | `pockets/authentication/stores/pgx/v0.8.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |
| authentication turso | `pockets/authentication/stores/turso/v0.7.0` | `8c9b56e77813ed6fe4d6dbca2506391093beb1f6` |

- **Repin:** authentication `v0.18.0`, authentication stores/pgx `v0.8.0`, authorization
  `v0.24.0`, authorization stores/pgx `v0.18.0` (turso indirect → `v0.17.0` if tidy pulls it;
  `authorization/stores/goredis v0.4.0` is NOT retagged and builds against core v0.24.0).
  Poll each proxy `.info` before `go mod tidy`.
- **Migration:** copy `pockets/authorization/stores/pgx/migrations/0004_iam_operations_fenced.sql`
  verbatim as host IAM ledger entry **0023** (never edit 0022). **Deploy order** (see
  `pockets/authorization/stores/UPGRADE.md`): run 0023 in its **own** migrate run with a lock
  timeout (`options=-c lock_timeout=5s` on the migration DSN; retry on timeout — the ALTER takes
  ACCESS EXCLUSIVE and a queued ALTER stalls every v0.23 `Apply`); freeze scale-out → migrate
  (old instances keep serving) → roll new binaries immediately → never let an old revision start
  afterwards. **Two-phase rollout:** phase 1 = repin + 0023 with NO Fencer wired (still
  reversible via the self-guarding reverse SQL in UPGRADE.md); phase 2 = wire the Fencer (first
  fenced row is the one-way door). Add the boot self-check
  `Mutations.Recorded(ctx, "boot-probe")` expecting `(false, nil)` wherever a Fencer is wired.
- **Granter reorder** (`v2/pockets/auth/inbound/granter.go`): after `DetachTransaction` and
  kind/relation validation, `access.Recorded(ctx, operationID(in.OperationID))`; when recorded,
  **skip `TenantOf`** and go straight to `Standing` + `Apply` (a replay never writes). Map
  `result.Replayed && result.Outcome == mutations.OutcomeFenced → invitations.ErrGrantFenced`
  first; keep `Replayed && Superseded → ErrGrantSuperseded` and `ErrOperationMismatch →
  ErrGrantSuperseded`. Put `"invitation:"+id` in one helper the Fencer shares.
- **Fencer:** `DetachTransaction`, then `access.Fence(ctx, operationID(in.OperationID))`;
  `OutcomeFenced → FenceOutcomeFenced`, else `FenceOutcomeCommitted`; wire
  `InvitationsConfig.Fencer`. Add a fence-then-grant → `ErrGrantFenced` test.
- **Removal flow:** collect with `ListActiveByResource{ResourceType, ResourceID, Relation}`
  to exhaustion (no 5000 cap); per row `PrepareManagement` → `Cancel`. nil → done, the fence
  holds. `ErrAlreadyGranted` → the row is `accepted`; run the ordinary exact revoke for that
  user, continue. Retire the 409s for "accepting row present" and "collection overflow"; keep
  409 only for a `Fence` infrastructure error or `ErrNotPending` on a genuinely foreign state
  (Cancel now re-reads, so a claim racing the cancel is settled, not 409'd).
- **Tests to replace:** `TestLivePersonalRemovalReportsClaimConflictBeforeLaterFirstGrant`
  becomes the desired-contract test (pause before first commit, remove → 200, resume →
  `ErrGrantFenced`, no standing); add deleted-resource settlement and the >5000-row listing proof.
