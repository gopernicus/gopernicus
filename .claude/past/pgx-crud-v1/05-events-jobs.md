# Phase P5 — events (idiom pass) + jobs (pagination + idiom pass)

Status: **RATIFIED 2026-07-08 (jrazmi — Q1/Q2/Q3 at recommendations; see 00-overview.md)**
Executor model: opus
Depends on: P3 (the pattern; independent of P4 — may swap with it)

## Context

Two small feature legs, one file. **events** has no crud-paginated List
at all — `ListUnpublished(ctx, limit)` is a plain-limit outbox drain and
stays that way (ratified out-of-scope); its pgx work is the idiom pass
plus one real Exec loop: `insertRecords`
(`features/events/stores/pgx/outbox.go:120-131`, shared by `Append` and
`AppendTx`). **jobs** has two crud-paginated ports (queue List with
Kind/Status filter, schedules List), no HTTP inbound layer, and the two
concurrency idioms this milestone must not regress: the
FOR UPDATE SKIP LOCKED claim (`queue.go:110-122`) and the ClaimDue CAS
(`schedules.go:130-132`). Jobs' `memstore/` is public in-core, implements
cursor paging, and — a found gap — is NOT wired into storetest today;
this phase closes that, which also makes the jobs storetest extension
hermetically testable in `make check`.

## Goal

events' pgx store is idiomatic pgx v5 with a single UNNEST append path;
jobs' paginated ports support order/prev/offset/count across pgx, turso,
and its memstore — with the memstore newly under the shared conformance
suite.

## Definition of Done

- events pgx: NamedArgs + CollectRows/CollectOneRow throughout;
  `insertRecords` is one UNNEST statement; `Append`/`AppendTx` Tx
  semantics and the storetest oldest-first ordering guarantee unchanged.
  events turso: untouched (no crud changes → nothing needed).
- jobs: order allow-lists in `features/jobs/domain/{job,schedule}`;
  storetest six-case family on queue List + schedules List;
  `features/jobs/memstore` passes storetest hermetically via a new
  conformance test; pgx on `pgxdb.List` with Claim/ClaimDue preserved
  verbatim; turso minimally migrated.
- Zero `ListPage` callers remain in either feature's stores.

## Out of scope

- Cursor-paginating `ListUnpublished` or any events port; events HTTP
  (SSE) untouched; `outboxmem` untouched beyond compiling.
- A jobs HTTP layer (none exists; none invented).
- turso idiom parity.

## Schema / datastore impact

None. UNNEST append must preserve the outbox's insertion-order guarantee
(the storetest oldest-first case is the acceptance — use ORDINALITY or
equivalent if plain UNNEST ordering proves insufficient under test).

## Risks

1. **Claim/ClaimDue regressions are correctness failures** — the SKIP
   LOCKED claim and the CAS update are copied byte-for-byte into the
   NamedArgs form only if the storetest contention cases stay green live;
   when in doubt, leave those two statements positional and log it.
2. Wiring jobs memstore into storetest may surface latent memstore bugs
   (it has never run the suite) — fixing those is in scope for task-2;
   loosening the suite is not.

## Tasks

### task-1: events pgx idiom pass + UNNEST append

- **depends_on:** []
- **model:** opus
- **files:** [features/events/stores/pgx/outbox.go, features/events/stores/pgx/postgres.go]
- **verify:** `cd features/events/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic skip); live leg: `docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...` — suite green incl. the append-order case; container removed
- **description:** Convert the outbox store to NamedArgs +
  CollectRows/CollectOneRow over a db-tagged row struct + toDomain.
  Replace the `insertRecords` per-row loop with one UNNEST array-param
  INSERT preserving array order (the oldest-first storetest case is the
  acceptance); `Append` keeps its own-Tx wrapper and `AppendTx` the
  caller's-Tx contract exactly. `ListUnpublished` keeps its plain-limit
  port shape (idiom conversion only).

### task-2: jobs order vocabulary + storetest + memstore under conformance

- **depends_on:** []
- **model:** opus
- **files:** [features/jobs/domain/job/order.go, features/jobs/domain/schedule/order.go, features/jobs/storetest/storetest.go, features/jobs/memstore/queue.go, features/jobs/memstore/schedules.go, features/jobs/memstore/conformance_test.go]
- **verify:** `cd features/jobs && go build ./... && go test ./... && go vet ./...` — the NEW memstore conformance test runs hermetically and passes; then `make check`
- **description:** Declare queue + schedules order allow-lists
  (minimum `created_at` DESC; `priority` for the queue only if indexed —
  log the decision). Apply the P3 six-case family to
  `testQueueListFilterAndPaginate` (under its Kind/Status filters — count
  must respect the filter) and `testSchedulesListPaginate`. Extend the
  memstore's shared `page[T]` helper (queue.go:247) for
  order/prev/offset/count, and add
  `features/jobs/memstore/conformance_test.go` running
  `storetest.RunQueue`/`RunSchedules` hermetically — closing the found
  coverage gap and giving `make check` a hermetic leg for this feature's
  new semantics. Dialect stores fail the new cases live until tasks 3–4.

### task-3: jobs pgx — List onto pgxdb.List + idiom sweep, claims preserved

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/jobs/stores/pgx/queue.go, features/jobs/stores/pgx/schedules.go, features/jobs/stores/pgx/helpers.go, features/jobs/stores/pgx/postgres.go]
- **verify:** hermetic module verify, then the live pgx leg (as task-1) — full extended suite green incl. the claim-contention cases; then `make check`
- **description:** Move queue List (Kind/Status filter builder) and
  schedules List onto `pgxdb.List[T]` with row structs + MapPage;
  convert remaining queries to NamedArgs + Collect*. **Preserve
  verbatim:** the `queue.go:110-122` FOR UPDATE SKIP LOCKED claim
  statement and the `schedules.go:130-132` ClaimDue CAS (affected-rows
  bool contract) — idiom-convert their surroundings only, and leave the
  statements positional if NamedArgs conversion changes anything
  observable (log the choice).

### task-4: jobs turso minimal migration

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/jobs/stores/turso/queue.go, features/jobs/stores/turso/schedules.go, features/jobs/stores/turso/helpers.go]
- **verify:** `cd features/jobs/stores/turso && go build ./... && go test ./... && go vet ./... && go vet -tags=integration ./...` then `make check`; live leg (playground discipline — URL must be `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`): `go test -tags=integration ./...` — extended suite green, recorded
- **description:** Migrate the two `turso.ListPage` call sites to
  `turso.List` with the order allow-lists and full ListRequest; nothing
  else changes (turso-minimal scope).

## Acceptance

```sh
cd features/events/stores/pgx && go build ./... && go vet ./... && go test ./...
cd features/jobs && go test ./...          # memstore conformance hermetic-green
make check && make guard
grep -rn "ListPage" features/jobs/stores/ features/events/stores/   # → empty
grep -c "INSERT INTO event_outbox" features/events/stores/pgx/outbox.go  # → 1 (the UNNEST form)
```

Live: pgx legs (tasks 1, 3) + turso leg (task 4) recorded (dated) for NOTES.

## Real-interaction check

Standing check: `make check` green; boot `examples/minimal` (:8081) →
200s, kill. Plus: run `examples/jobs-minimal` (`cd examples/jobs-minimal
&& go run ./cmd/server` or its documented run form), confirm it boots on
the extended memstore, enqueues and runs its demo jobs, and shuts down
clean — that host is the memstore's only production consumer.

## Execution log

### 2026-07-08 — phase 5 executed (implementer on opus); PHASE COMPLETE

All four tasks landed. events pgx: `outboxRow` + toDomain, NamedArgs +
CollectRows throughout, `insertRecords` → ONE UNNEST INSERT (**plain
UNNEST, no ORDINALITY — deliberate:** the batch shares one created_at
and event_id tiebreaks, so array position never affects oldest-first;
the live AppendAndListOrder case is the proof), Append/AppendTx Tx
contracts unchanged, ListUnpublished keeps its plain-limit shape. jobs:
order vocabularies (created_at only — **priority EXCLUDED, logged:**
the only priority index is the partial claim-hot-path
`idx_job_queue_claim` leading with scheduled_for; it cannot serve a
List sort), six-case family on queue List (Kind-filtered, count
respects the filter) + schedules List, memstore `page[T]` extended to
the full matrix and **newly wired under conformance**
(memstore/conformance_test.go — the found coverage gap closed), pgx
Lists onto `pgxdb.List` + jobFilter builder + NamedArgs sweep with
**Claim (SKIP LOCKED) and ClaimDue (CAS) preserved verbatim/positional**
(contention cases ConcurrentClaim/ClaimDueCAS/LeaseExpiry green live),
turso minimal migration of both call sites.

**Flagged, accepted:** jobs family seeds separate rows by real 2ms
sleeps (Enqueue/Ensure take no created_at), so the jobs family lacks a
same-created_at tiebreak PAIR — id tiebreak still asserted structurally;
documented in the storetest. **Open cleanup noted, owner's call:**
`features/jobs/storetest/reference_test.go` (pre-existing, G2-guard
rationale) and the new memstore conformance test now both run the
memstore suite — consider retiring reference_test.go.

**Live legs (2026-07-08, for NOTES):** events pgx — postgres:17, suite
green incl. AppendAndListOrder + AppendTx. jobs pgx — postgres:17,
Queue 4.35s incl. ConcurrentClaim + LeaseExpiry + 7 family cases,
Schedules incl. ClaimDueCAS + 7 family cases; container removed, port
free. jobs turso — playground URL byte-verified (hard gate), 41.5s
green (queue family 16.5s, schedules 24.8s), no tokens emitted.

Acceptance re-verified by the main session fresh: jobs + events-pgx
build/vet/test green, `make check` all 30 modules, zero ListPage in
both features' stores, exactly one `INSERT INTO event_outbox` (the
UNNEST). Real-interaction (both executor and main session):
examples/minimal :8081 → 200; examples/jobs-minimal :8083 booted on the
extended memstore — main session enqueued `demo.print` via POST
/enqueue → **200, executed and completed in 44µs**, minute-cron
scheduler fired concurrently, SIGTERM → "jobs runtime drained", exit
clean, port free. (Executor's earlier run additionally showed
demo.flaky retry→success and demo.doomed exhaustion.) Next: P6
(`06-cleanup-docs.md`) — the milestone's last phase.
