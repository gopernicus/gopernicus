# authorization operation ledger + safe invitation-acceptance recovery

**Status:** RULINGS TAKEN 2026-10-06 (owner, in-session: "ratified to update the plan with your
recommendations" — R0–R6 all as recommended, recorded under "Rulings"). **Implementation authorized 2026-10-06** by the owner's request to implement this plan.
Tasks 1–4 and the task 6 handoff are implemented; verification and release preparation
are recorded below.
Task 5 publication remains gated on the owner's release word.
Drafted 2026-10-06 from a Segovia upstream request (personal invitations are blocked on it;
`segovia/.claude/plans/v2-personal-timeline-collaboration.md`, "Durable invitation dependency").
Reviewed 2026-10-06 by lead-backend-engineer and data-integration-reviewer (both
ship-with-edits; findings folded).

**This plan partially reverses an implemented owner decision — AUDIT-026 — by ruling R0.**

## Context

Invitation acceptance is a three-step protocol across two pockets that cannot share a commit:

```
ClaimAcceptance (authentication store)  →  Granter.Grant (host → authorization)  →  CompleteAcceptance (authentication store)
```

`pockets/authentication/logic/invitations/service.go` `acceptClaim` deliberately never unclaims
("an error cannot prove that an external side effect did not commit"), so an `accepting` row is
retried forever with the same `GrantInput.OperationID` (the invitation row ID). The `Granter`
doc requires the host to make that idempotent — but nothing upstream lets a host do so durably:

- `MutationRepository.Apply` retains nothing: "No caller operation token or durable result is
  retained; every call validates the current model" (`stores/pgx/mutations.go:68`,
  `stores/turso/mutations.go:28`). That is AUDIT-026 (`AUDIT.md:3664`, implemented 2026-09-11),
  which removed `MutationID`, payload digests, `Receipt`, `Replayed`, `ErrMutationMismatch` and
  `iam_mutations`, ruling: "No durable request deduplication remains; a host needing it
  implements that around the pocket."
- `Apply` refuses an ambient transaction (`ErrMutationInsideTransaction`), so a host cannot put
  its own receipt row, or the invitation finalization, in the grant's transaction.
- A grant is a state-convergent write (Segovia's `Standing` is an atomic *replacement* batch), so
  re-running it re-asserts the invited relation over whatever happened since.

The first two together are the problem: AUDIT-026 sends deduplication "around the pocket", and
the ambient-transaction refusal makes "around the pocket" impossible to do atomically.

The failure, reproduced by Segovia's uncommitted characterization test
(`segovia/v2/pockets/auth/inbound/invitation_recovery_test.go`,
`TestPinnedInvitationAcceptingRetryCurrentlyRestoresRevokedAccess`) against the real `Accept`
service + real memory mutation store:

1. Accept → claim ok → grant commits → `CompleteAcceptance` fails. Row stays `accepting`.
2. A manager revokes (or changes the role of) that person.
3. Accept retry → grant runs again → **revoked access is restored / newer role is overwritten**.

A second test shows two concurrent retries both reach the Granter and both run the swap.
Cancel cannot clear it either: an `accepting` row is not cancellable.

## Goal

1. A mutation command may carry an operation ID that authorization records **in the same
   transaction as the tuple delta**. A repeat of a committed operation writes nothing and says so.
2. Invitation acceptance recovers on top of that, **for a grant that committed**: a retry after
   "grant committed, completion failed" finishes recording acceptance; if the access was since
   revoked or replaced, the retry resolves the invitation terminally and reports it — without
   restoring access. (Grants that never committed are the known gaps under "Out of scope".)
3. Conformance tests prove it for sequential retry, concurrent retry, revoke/replace races, and a
   fresh store instance over the same database (crash/restart).

## Recommendation: the ledger, not the joint transaction

Segovia offered two shapes. This plan builds the first (ruling R1).

| | A — operation ledger in authorization (this plan) | B — let `Apply` join an ambient transaction; claim+grant+complete in one `Transact` |
|---|---|---|
| Works when invitation store and authorization store differ (memory, firestore authentication, split DBs) | yes | no — needs one connector, one database |
| Pocket coupling | none; `Granter` seam unchanged in shape | authentication's `Accept` needs a transactor seam and must hand an ambient ctx across pockets |
| `Apply` internals | unchanged outside the ledger read/insert | global write lock held until host commit; contention retry loop cannot run inside a host tx; tuple-cache delivery rejects ambient contexts (`tuplecache/runtime.go:159`); audit durability changes |
| Fixes non-invitation retries (any host retrying a mutation after an ambiguous commit) | yes | no |
| Prior ruling | new | ruled out of scope with the reasons above in `authorization-stores-ambient-transaction.md` ("The guarded path") |

B is not needed once A exists, and A is the only one that honors the "independence lives in
ports" rule. B stays out of scope.

## Decisions

### D1 — `Command.OperationID` (authorization core)

`mutations.Command` gains `OperationID string`. Empty = exactly today's behavior (no ledger read,
no ledger write). Non-empty: validated with `tuples.ValidateRefField` (valid UTF-8, no control
characters, ≤ `MaxRefFieldLen` = 256 bytes). It is caller-chosen and global to the store, so
the doc tells hosts with more than one producer to namespace it (`invitation:<id>`).
`OpTeardown` (typed method only) refuses an OperationID with `ErrInvalidCommand`. OperationID is
`Service.Apply`-only: the typed helpers (`GrantRelationshipCommand`, role commands,
`UnassignRoleResult`) neither carry nor report it. `ReasonFor` gains a reason code for
`ErrOperationMismatch` so hosts do not log it as a store error.

### D2 — the ledger row and its atomicity

One row per committed operation: `operation_id` (PK), `fingerprint`, `outcome`, `committed_at`.

- **Fingerprint** = SHA-256 (lowercase hex) over a canonical encoding of the command's bound
  meaning. Computed in `logic/mutations` (pure, one implementation for every store). This is the
  "bind the operation to the exact resource, relation and subject" requirement.
  - It is **versioned and frozen**, because rows never expire and the fingerprint is recomputed by
    whatever release is running at retry time. Each row stores `encoding = 'operation/v1'`. v1 is
    a framed field encoding: each field is its UTF-8 bytes followed by one NUL byte (ref fields
    reject control characters; empty fields still emit a NUL). The exact field sequence is:
    - `operation/v1`, target kind, target type, target ID, operation;
    - `add`, the add count, then each deduplicated, `tuples.Compare`-sorted `Requested().Add`
      tuple as scope kind/type/ID, relation, subject type/ID/relation;
    - `remove`, the remove count, then each deduplicated, `tuples.Compare`-sorted
      `Requested().Remove` tuple in the same seven-field form;
    - `reconcile`, relation, subject count, then each deduplicated subject as type/ID/relation,
      sorted lexicographically by those three fields. For other operations relation is empty
      and subject count is zero.

    Counts are canonical unsigned base-10 strings without leading zeroes (except `0`). Tags
    and counts distinguish collection boundaries: `Add=[X], Remove=[Y]` cannot encode like
    `Add=[X,Y], Remove=[]`. Only normalized requested facts are hashed, never the raw
    `Relationships`/`Roles`/`Tuples` input shape. `MaxAffectedRows`
    (rewritten by `Service.Apply`, `mutation_service.go:80`) and the OperationID are excluded.
    Golden vectors in `logic/mutations` lock it; a future v2 must still verify v1 rows.
  - **Known edge:** the binding covers the whole requested delta. A host whose command shape
    changes between attempts (Segovia's `Standing` removes every *other* relation of the kind, so
    adding a relation to a kind changes the remove set) gets `ErrOperationMismatch` on in-flight
    retries. D4 makes that terminal, never a re-grant and never a stuck claim.
- **Write path**, inside the store's existing write-serialized transaction. The lookup sits
  **after the lock and before the SemanticValidator** — pgx between `lockAuthorization` and
  `validate` (`mutations.go:91`/`:94`), turso at the top of `attempt`, memory at the top of the
  `write` closure — so a replay never depends on the current model:
  - absent → validate and evaluate as today; if a definite `Result` is produced (`applied`,
    `no_change`, `not_found`), insert the ledger row in the same transaction as delta + audit.
    Refusals and errors record nothing, as today — a later retry re-evaluates.
  - present, same fingerprint → **replay** (D3). No validator call, no tuple write, no audit row,
    no cache wake (capture is triggers on `iam_tuples` only). The recorded outcome is parsed and
    checked with `Outcome.Valid()`; an unknown value is an error.
  - present, different fingerprint → `ErrOperationMismatch` (new sentinel wrapping
    `sdk.ErrConflict`), nothing written, no validator call.
  - The lookup's no-rows is "absent", never an error. The insert is
    `INSERT … ON CONFLICT (operation_id) DO NOTHING`; `RowsAffected != 1` returns
    `ErrConcurrentMutation` and rolls back. A collision cannot happen under the lock (pgx
    `LOCK TABLE iam_tuples IN SHARE ROW EXCLUSIVE MODE`, taken by every writer including ambient
    baseline writes; turso `BEGIN IMMEDIATE`), but it is checked, not trusted, and never by
    driver-string matching.
- `no_change` is recorded on purpose: "the relation was already there when this operation
  committed" is a decision too; a later revoke must not be undone by a retry of it.
- **Memory store:** `state.write` runs a final `ctx.Err()` check *after* the closure
  (`memory/audit.go:51`). The ledger entry is staged on `next` and published in the same step as
  `s.facts = next.facts`; the lookup reads the committed map under `s.mu`. Writing the map inside
  the closure would keep a ledger entry for a delta that was dropped.
- **Schema:** `migrations/0003_iam_operations.sql` in pgx and turso. Not `0002`: hosts merge the
  base with `tuple_cache_migrations/0002_iam_tuple_cache.sql` into one ordered stream
  (`stores/UPGRADE.md`). Plain `CREATE TABLE`, no secondary index, no STRICT/WITHOUT ROWID.

  ```sql
  -- pgx (unqualified; text COLLATE "C")
  CREATE TABLE iam_operations (
      operation_id TEXT COLLATE "C" NOT NULL PRIMARY KEY,
      encoding TEXT COLLATE "C" NOT NULL,
      fingerprint TEXT COLLATE "C" NOT NULL,
      outcome TEXT COLLATE "C" NOT NULL,
      committed_at TIMESTAMPTZ NOT NULL,
      CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
      CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
      CONSTRAINT ck_iam_operations_fingerprint CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
      CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found'))
  );
  ```

  turso is the same table as `main.iam_operations` with `COLLATE BINARY`, `committed_at TEXT`
  written by `tursodb.FormatTime`, and the fingerprint CHECK as
  `length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'`.
- **Probe:** `Repositories` requires the table (an upgraded store over an un-migrated database
  fails at construction, loudly — the same posture as `iam_audit` there).
  `RelationshipRepository` does not probe or require it.
- **Retention:** rows are never deleted in v1, including by purge/teardown — and teardown makes
  that necessary: if rows went with the scope, an `accepting` retry would re-grant onto a
  torn-down resource, whereas a kept row replays as `Superseded`. An acceptance claim "is not a
  lease and never expires", so no age-based purge is safe; a row is ~150 bytes per operation. The
  fingerprint is a hash over subject IDs (pseudonymous but linkable): the README tells hosts
  their user-erasure procedure should know the table exists and must not delete rows a producer
  may still retry. A purge API is a follow-up if a host shows volume.

### D3 — what a replay returns

```go
type Result struct {
	Outcome    Outcome // as recorded when the operation first committed
	Replayed   bool    // this call wrote nothing: OperationID had already committed
	Superseded bool    // only with Replayed: the command's facts are no longer in effect
}
```

`Superseded` means **the requested facts are not in effect**: some requested Add is missing, some
requested Remove is present, a reconcile's set differs, or a purge target is non-empty. It is
computed under the same lock by a pure helper in `logic/mutations` over the facts each store's
`evaluate` already reads (`evaluate` is split into read / plan+apply so replay runs the read
only). The helper deliberately skips integrity policy, the affected-rows limit and the model
validator — `Plan` checks those before it computes a delta (`plan.go:47-73`), so re-planning
would report a still-present grant as superseded the day a host tightens its policy. A read or
I/O error is an error, not a verdict. It is a snapshot — a revoke can always land a moment later —
but it lets a host answer "the grant I am repeating is gone" without a second, unserialized read.
The safety property (never re-apply) does not depend on it.

`Service.Apply` must also let recorded operations reach the ledger after a host lowers
`MaxBatchSize`. Move its configurable raw-input-count check (`mutation_service.go:77`) into
the fresh-operation validation callback, before current-model validation; stores invoke that
callback only after the ledger lookup finds no row. Keep structural `Command.Validate()` and
the hard `MaxCommandTuples` bound before lookup, and keep the existing `MaxAffectedRows`
clamping for fresh planning. A committed replay bypasses configurable batch admission as well
as the affected-row limit; a fresh command over the configured batch limit still returns
`ErrEvaluationLimit` and records nothing. Retain the existing early batch-count check for an
empty OperationID to preserve today's validation order. Update the callback contract doc to
cover pure fresh-operation service admission as well as current-model tuple validation.

A replay still requires an audit source in context when audit is enabled (all three stores check
it before the transaction); it just writes no audit row.

### D4 — `Granter` contract (authentication)

The seam's shape is unchanged. Its doc distinguishes the safe recovery contract below from
the legacy state-convergent posture:

- One OperationID commits **at most once**. A repeat must not write.
- New sentinel `invitations.ErrGrantSuperseded` (wraps `sdk.ErrConflict`): the Granter returns it
  when this operation committed earlier and the granted relation is no longer in effect.
- nil keeps meaning "the exact relation is in place" (applied now, or replayed and still
  current). A Granter may no longer return nil for a repeat while the relation is absent.
- Every definite "already committed, will not write" answer must map to nil or
  `ErrGrantSuperseded` — never to a retryable error, or the claim is stuck `accepting` forever.
  That includes `ErrOperationMismatch` (the host changed its command shape between attempts):
  the safe mapping is `ErrGrantSuperseded`.
- State-convergent adapters (a baseline `RelationshipWriter`, a role-column write) remain
  supported for compatibility, but do **not** satisfy the at-most-once recovery contract above.
  Their doc states the cost plainly: an accepting retry can restore revoked access. Hosts
  requiring safe recovery must use the ledger or an equivalent atomic deduplication mechanism;
  the reference example and Segovia handoff demonstrate the safe contract.
- By design and stated in the doc: a first attempt that never committed, followed by a role
  change, followed by a retry, applies the invited relation for the first time. That is a late
  first grant, not a restore; the ledger has nothing to replay.

### D5 — `Accept` recovery (authentication)

One change, in `acceptClaim` (`ResolveInvitations` calls it, `service.go:716`, and keeps its
warn-and-continue handling of a superseded result). After `ClaimAcceptance`:

| Granter returns | Service does | Caller sees |
|---|---|---|
| nil | `CompleteAcceptance`, grant-success event, member-added notice (already keyed by invitation ID) | success |
| `ErrGrantSuperseded` | `CompleteAcceptance` (the grant did commit under this operation — `accepted` is the true terminal state and it releases the tuple reservation so the person can be re-invited); a `superseded` security event; **no** member-added notice | `ErrGrantSuperseded` (409-class) |
| any other error | unchanged: stay `accepting`, grant-failure event | the error |

No new invitation status, so no authentication store migration. The superseded event is
`TypeInvitationGranted` with a blocked/superseded status (no CHECK constrains event type or
status in any store).

Stated limits of the reporting:
- `ErrGrantSuperseded` reaches the caller whose retry resolved the claim. Any later use of the
  now-`accepted` token takes the existing short-circuit (`service.go:533`) and returns success
  without reaching the Granter — the same as today's accepted-then-revoked behavior.
- Two concurrent retries racing a revoke can yield one success (with notice) and one
  superseded result; `CompleteAcceptance` returns an already-accepted row unchanged. Tests assert
  the invariant (one committed grant, nothing restored), not a single outcome.

### D6 — conformance (the third Segovia ask)

- `stores/storetest`, new `OperationLedger` family registered in `Run`, executed by memory, pgx
  and turso: sequential replay is `Replayed` with the recorded outcome and unchanged facts;
  replay after revoke, after role replacement, and after `OpTeardown` → `Superseded`, nothing
  restored; fingerprint mismatch → `ErrOperationMismatch` (`errors.Is(sdk.ErrConflict)`), no
  write; refuse → fix state → retry the same OperationID ⇒ `Applied`, `Replayed=false` (no port
  reads the ledger, so "a refusal leaves no row" is proven by behavior); replay with a validator
  that now rejects ⇒ still `Replayed`; N goroutines on one OperationID ⇒ exactly one
  `Replayed=false`; empty OperationID is today's behavior.
- `RunAudit` (the audit-enabled factory): first apply writes one audit set, a replay none,
  N concurrent applies one.
- `RunTransactional`: an OperationID inside `Transact` still gets `ErrMutationInsideTransaction`.
- `mutation_cancellation.go`: OperationID variant — a cancelled attempt then a retry is
  `Applied`, not `Replayed` (pins the memory staging rule).
- **Restart** is not portable (the conformance factories truncate per call): it lives in
  `pgx/mutations_live_test.go` and `turso/mutations_live_test.go` — a second connection handle,
  no truncation, a fresh `Repositories` replays.
- `logic/mutations`: fingerprint golden vectors, order/duplicate independence,
  `MaxAffectedRows` exclusion; explicit collection-boundary vectors for valid `OpBatch`
  commands `Add=[X], Remove=[Y]` versus `Add=[X,Y], Remove=[]` (where `X < Y`), and reconcile
  subject order/duplicate independence.
- `Service.Apply` regression tests: commit a command, reconstruct the service over the same
  repository with a lower `MaxBatchSize`, then replay the exact command ⇒ `Replayed`, unchanged
  facts. The same command with a fresh OperationID or an empty OperationID ⇒
  `ErrEvaluationLimit`, no write; retry the fresh ID under the original limit ⇒ not replayed.
- storetest additions from review: replay after an integrity-policy tightening with the grant
  still present ⇒ `Replayed`, **not** `Superseded`.
- `logic/invitations` service tests with a ledger-backed fake Granter: Segovia's exact scenario
  (grant ok → completion fails → revoke → retry ⇒ `ErrGrantSuperseded`, invitation `accepted`,
  access absent); completion-failure-then-retry without revoke ⇒ success, one grant; two
  concurrent accepting retries ⇒ one committed grant; a Granter that maps a mismatch to
  `ErrGrantSuperseded` resolves terminally; resolve-on-registration parity.
- `examples/auth-cms`: its Granter moves to the ledger path (R4); the fault-injected real-app
  check is task 4.

## Out of scope

- `Apply` joining an ambient transaction (option B).
- Restoring anything else AUDIT-026 removed: revisions, compare-and-set, receipts on the wire,
  client-supplied mutation IDs. Calls without an OperationID keep AUDIT-026's state-based
  semantics exactly.
- An authorization firestore store: it exists only on unmerged branches
  (`firestore-authorization`, `firestore-connector-to-main`). If it lands it is a first-party
  `MutationRepository` and owes the ledger + the `OperationLedger` conformance family.
- Any change to the baseline `RelationshipWriter` — it stays state-convergent and ledger-free.
- Ledger purge/retention API; operation IDs on teardown.
- **Known gaps, follow-up (R5).** Two `accepting`-forever paths survive this plan. Neither
  restores access, and both go in the README and the Segovia handoff:
  - (a) a host precondition that runs before `Apply` turns permanently false after the commit
    (Segovia's Granter checks the resource exists first, so a committed operation on a
    since-deleted resource errors forever and never reaches the ledger);
  - (b) a first attempt that was refused (`ErrInvariantBlocked`) stays claimed, cannot be
    cancelled, and grants whenever the refusal clears.
  The candidate fix for (a), a read-only `OperationRecorded(ctx, id)` on the mutation port, is
  not built here.
- Recording the canonical requested facts on the ledger row (R6).
- HTTP exposure of client-supplied operation IDs on the bundled role routes.
- The Segovia adoption itself (repin, migration import, Granter change, replacing the pinned
  characterization tests, enabling personal invitations) — that is the Segovia session's leg; a
  handoff note is this plan's last task.

## Rulings

Taken 2026-10-06 by the owner, each as recommended.

- **R0 — AUDIT-026 is reversed, narrowly.** AUDIT-026 (`AUDIT.md:3664`) removed the caller
  operation ID, payload digest, replay flag and ledger table, ruling "no durable request
  deduplication remains; a host needing it implements that around the pocket." That escape hatch
  cannot be built atomically while `Apply` refuses ambient transactions, so those four return as
  an opt-in on `Service.Apply` only. One exception is carved out of AUDIT-026's rule "if the
  current model no longer permits the relation, even a previously successful grant is rejected":
  a **replay** is answered from the ledger before the model validator runs (it writes nothing,
  so there is nothing for the model to reject). Everything else AUDIT-026 removed stays removed,
  and calls without an OperationID keep its state-based semantics exactly. AUDIT.md gets a new
  entry recording this (task 1).
- **R1 — the ledger (A) only.** `Apply` joining an ambient transaction (B) stays out of scope.
- **R2 — superseded resolution.** Finalize the invitation as `accepted` and return
  `ErrGrantSuperseded` to the retry that resolved it (D5). No new invitation status.
- **R3 — `Result` shape.** `Replayed` and `Superseded` booleans beside the recorded `Outcome` (D3).
- **R4 — example host.** `examples/auth-cms`'s Granter moves from the baseline
  `RelationshipWriter` to `Mutations.Apply` with the OperationID and `ErrGrantSuperseded`, so
  the example is the reference for the safe posture.
- **R5 — grants that never committed.** Named as known gaps (see "Out of scope"), taken as a
  follow-up; no read port in this plan.
- **R6 — requested facts on the row.** Not in v1. The fingerprint's known edge (D2) stands, and
  the mismatch → `ErrGrantSuperseded` mapping (D4) keeps it safe.

## Tasks

Each ends with `go build ./... && go test ./... && go vet ./...` in the touched module, plus the
repo guards (`make guard`).

1. **authorization core** — `Command.OperationID` + validation; framed `Fingerprint`;
   move configurable service batch admission into fresh-operation validation + regression tests;
   `Result.Replayed/Superseded`; `ErrOperationMismatch`; `MutationRepository` contract doc;
   memory store ledger; storetest `OperationLedger` family; README + `stores/UPGRADE.md`; a new
   AUDIT.md entry recording the R0 reversal of AUDIT-026.
   verify: `cd pockets/authorization && go test ./...` (memory conformance green).
2. **authorization stores pgx + turso** — `0003_iam_operations.sql`; ledger read/insert in
   `Apply`; `evaluate` split into read+plan / apply; live + restart tests. Probe work: pgx
   `probeCanonicalSchema` gains an operations switch (true from `Repositories` only), exact
   catalog-form CHECK constants, `storeTables`; turso's probe reads a second canonical file and
   `Repositories` adds `probeTable("iam_operations")`. Fixtures: add `iam_operations` to
   `authorizationTables` (truncation + `qualifySQL`) in both `conformance_test.go` files, the
   exact-table assertions in both `fresh_schema_test.go` files, `canonicalMigrations` /
   `expectedTables` / `expectedConstraints` in both `migrations_test.go` files, plus a
   merged-stream test (base 0001+0003 with cache 0002) and probe-rejection cases in
   `canonical_schema_test.go`. Update the stale "no operation token is retained" comments and
   `UPGRADE.md` ("no mutation receipt tables"; "copy only `0003_iam_operations.sql` into an
   existing merged stream — do not re-export over renamed files"). verify: pgx against local
   Postgres; turso `-run 'TestConformance$'` on the playground URL only (verify the URL first),
   `-timeout 45m`.
3. **authentication** — `ErrGrantSuperseded`, `Granter`/`GrantInput` docs, `acceptClaim` +
   `ResolveInvitations` recovery, security event, service tests, README. No store change.
   verify: `cd pockets/authentication && go test ./...`.
4. **examples/auth-cms** (R4) — Granter on the ledger path. The real-app check must inject
   the fault, or it passes before and after the change alike (a normal accept completes, and a
   later click takes the accepted short-circuit): run the example with a host-side
   fault-injecting invitation repository wrapper that fails `CompleteAcceptance` once after the
   grant commits; accept, confirm access, revoke, retry the accept link, confirm the 409-class
   result, that the invitation lists as accepted, and that no access returned.
5. **Release** — PRs: #A core → #B stores (stacked on A) ; #C authentication (independent).
   Tags (owner word, one step per command): `pockets/authorization/v0.23.0`,
   `stores/pgx/v0.17.0`, `stores/turso/v0.16.0`, `pockets/authentication/v0.17.0`; poll each
   `.info` URL before any `go mod tidy`; cold-verify; record in RELEASING.md; copy this plan to
   `plans/`. Migration 0003 is a **required host migration**.
6. **Segovia handoff note** — pins, the one migration file to copy into `iam`, the Granter diff
   (`cmd.OperationID = "invitation:" + in.OperationID`; `Replayed && Superseded` →
   `ErrGrantSuperseded`), and the desired-contract tests that replace the two `TestPinned…`
   characterizations.

## Compatibility

- Host-visible: one required migration for every pgx/turso host using `Repositories` (boot
  fails with the probe error otherwise). Behavior is unchanged when OperationID is empty.
- Breaking for third-party `MutationRepository` implementers (must honor OperationID) and for
  any Granter that returned nil on a repeat with the relation absent — pre-1.0 MINOR bumps.


## Implementation record — 2026-10-06

Active plan: `.claude/plans/invitation-grant-operation-ledger.md`; tracked copy:
`plans/invitation-grant-operation-ledger.md`. Implementation used the repository's
implementer, backend reviewer and verifier roles. Existing changes to
`plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md` and `.github/scripts/__pycache__/`
were preserved. No generated templates or assets changed.

Tasks 1–4 implemented: core and memory ledger, both SQL adapters and migration/probe
updates, authentication terminal recovery, and the reference host. Backend review
confirmed the two pre-build findings were fixed and requested explicit revoke/replace
races and SQL policy-tightening tests; those are now implemented and passing.

Verification commands and results (use
`GOCACHE=/private/tmp/gopernicus-invitation-go-cache` where a writable cache is needed):

- `go build ./...`, `go test ./...`, `go vet ./...` in authorization core,
  authentication core, pgx and Turso adapters: passed.
- `go test -race ./logic/invitations` in authentication: passed, including
  deterministic concurrent success/superseded recovery across revoke and replacement.
- `go test -race ./stores/memory -run 'TestConformance/OperationLedger' -count=10`
  in authorization: passed, including concurrent retries racing manager changes.
- pgx full tests against isolated local PostgreSQL 17: passed; schema-qualified
  conformance/transactional/audit/restart and targeted replay races: passed.
- Turso integration conformance/transactional/audit/restart against local SQLite
  and an isolated local HTTP libSQL server: passed, including replay races and
  restart with tightened integrity policy. Hosted Turso remains unverified.
- `go test ./cmd/server -run 'TestInvitation' -count=1` in auth-cms: passed over
  real local TCP. The fault-injecting repository fails completion after grant;
  manager revoke then retry returns 409, lists the row as accepted and preserves
  absent access. A later accepted-token retry succeeds without restoring access.
- `make guard`: passed. `go build ./...`, `go test ./...`, `go vet ./...`:
  passed in auth-cms, authorization goredis and authentication pgx/Turso/Firestore
  stores and Goth views. `go vet -tags=integration ./...`: passed in authentication
  Turso/Firestore and authorization goredis; `go vet -tags=integration,live ./...`:
  passed in authentication Firestore. Those downstream live suites were not run.
- `git diff --check`: passed. Full `make check`, generated-template regeneration,
  visual browser automation and hosted Firestore/Turso execution were not run.
  The changed behavior is covered through real HTTP; generated files have no diff.

Default sandbox test attempts could not bind localhost or write the default Go
cache; reruns used the writable cache and approved local-socket execution.
No unresolved code/test failure is currently known. No hosted service, production
migration, remote PR or release tag has been published. Module pins remain at
published versions until the planned release/proxy sequence runs; workspace
verification resolves the candidate sources.

The two isolated test containers created for verification were stopped and removed;
existing containers were untouched.

Task 5 remains: split the reviewable changes into core → stores and independent
authentication PRs (with the example adoption after both), merge, obtain the owner's
release word, publish the four candidate tags, poll their proxy `.info`, update
store/example module pins, tidy and cold-verify the published archives. Proposed
tags remain authorization `v0.23.0`, pgx `v0.17.0`, Turso `v0.16.0`, authentication
`v0.17.0`. Do not describe these versions as published yet.

### Segovia handoff — task 6

After publication, pin the four versions above. Copy only the selected dialect's
`0003_iam_operations.sql` into Segovia's existing `iam` migration stream and apply
it before boot. Keep the historical renamed files untouched; keep operation rows
across resource teardown, erasure and backup/restore whenever retries remain possible.

Adapt the existing stable Standing command in the Granter:

```go
cmd.OperationID = "invitation:" + in.OperationID
result, err := mutationsService.Apply(ctx, cmd)
if errors.Is(err, mutations.ErrOperationMismatch) {
    return invitations.ErrGrantSuperseded
}
if err != nil {
    return err
}
if result.Replayed && result.Superseded {
    return invitations.ErrGrantSuperseded
}
return nil
```

Keep system audit attribution and existing resource preflight. This ledger path
cannot resolve a retry that the resource preflight refuses before `Apply`; that
known gap remains. A never-committed/refused operation can first grant later when
its refusal clears. If a model deployment changes Standing's requested remove set,
its old ID mismatches and resolves terminally as superseded without a new write.

Replace `TestPinnedInvitationAcceptingRetryCurrentlyRestoresRevokedAccess` and the
concurrent-swap characterization with desired-contract tests: inject grant-committed
completion failure, revoke or replace, retry and assert accepted + superseded conflict
with no restored access; run concurrent accepting retries and assert exactly one
committed grant. Also pin successful recovery without revoke, model/command mismatch,
revoke/replace races and a fresh SQL handle after restart. Enable personal invitations
only after the downstream adoption and required migration have been verified.

### Changed files

- `AUDIT.md`
- `RELEASING.md`
- `examples/auth-cms/README.md`
- `examples/auth-cms/cmd/server/authorization_test.go`
- `examples/auth-cms/cmd/server/invitation_recovery_test.go`
- `examples/auth-cms/cmd/server/main.go`
- `examples/auth-cms/cmd/server/membership.go`
- `plans/invitation-grant-operation-ledger.md`
- `pockets/authentication/README.md`
- `pockets/authentication/logic/authentication/securityevent/securityevent.go`
- `pockets/authentication/logic/invitations/recovery_test.go`
- `pockets/authentication/logic/invitations/service.go`
- `pockets/authorization/README.md`
- `pockets/authorization/logic/model/reasons.go`
- `pockets/authorization/logic/mutations/mutation.go`
- `pockets/authorization/logic/mutations/mutation_service.go`
- `pockets/authorization/logic/mutations/operation.go`
- `pockets/authorization/logic/mutations/operation_test.go`
- `pockets/authorization/logic/mutations/reasons.go`
- `pockets/authorization/logic/mutations/repository.go`
- `pockets/authorization/logic/mutations/result.go`
- `pockets/authorization/mutation_service_test.go`
- `pockets/authorization/stores/UPGRADE.md`
- `pockets/authorization/stores/memory/audit.go`
- `pockets/authorization/stores/memory/memory.go`
- `pockets/authorization/stores/memory/mutations.go`
- `pockets/authorization/stores/memory/mutations_test.go`
- `pockets/authorization/stores/pgx/canonical_schema_test.go`
- `pockets/authorization/stores/pgx/collation_test.go`
- `pockets/authorization/stores/pgx/conformance_test.go`
- `pockets/authorization/stores/pgx/fresh_schema_test.go`
- `pockets/authorization/stores/pgx/migrations/0003_iam_operations.sql`
- `pockets/authorization/stores/pgx/migrations_test.go`
- `pockets/authorization/stores/pgx/mutations.go`
- `pockets/authorization/stores/pgx/mutations_eval.go`
- `pockets/authorization/stores/pgx/mutations_live_test.go`
- `pockets/authorization/stores/pgx/operations.go`
- `pockets/authorization/stores/pgx/postgres.go`
- `pockets/authorization/stores/pgx/schema.go`
- `pockets/authorization/stores/storetest/audit.go`
- `pockets/authorization/stores/storetest/mutation_cancellation.go`
- `pockets/authorization/stores/storetest/operation_ledger.go`
- `pockets/authorization/stores/storetest/storetest.go`
- `pockets/authorization/stores/storetest/transactional.go`
- `pockets/authorization/stores/turso/canonical_schema_test.go`
- `pockets/authorization/stores/turso/conformance_test.go`
- `pockets/authorization/stores/turso/fresh_schema_test.go`
- `pockets/authorization/stores/turso/migrations/0003_iam_operations.sql`
- `pockets/authorization/stores/turso/migrations_test.go`
- `pockets/authorization/stores/turso/mutations.go`
- `pockets/authorization/stores/turso/mutations_eval.go`
- `pockets/authorization/stores/turso/mutations_live_test.go`
- `pockets/authorization/stores/turso/operations.go`
- `pockets/authorization/stores/turso/schema.go`
- `pockets/authorization/stores/turso/turso.go`
- `.claude/plans/invitation-grant-operation-ledger.md` (ignored active plan; mirrored above).
