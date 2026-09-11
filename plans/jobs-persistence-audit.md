# Jobs persistence audit: generation ordering and scheduler recovery

Status: COMPLETE — 2026-09-10 for SQL ordering implementation and initial scheduler
persistence review. Scheduler findings remain open for implementation.
Central checklist: [framework-audit-jobs.md](framework-audit-jobs.md).
Parent: [framework-audit.md](framework-audit.md).

## Scope and preconditions

The owner continued jobs work after establishing its checklist. Start J3/J4 with
SQL generation ordering and scheduler persistence boundaries. Complete a bounded
SQL ordering fix if existing locks/schema suffice; record scheduler findings and
concrete recovery options without bundling a new scheduling protocol into it.
Generic SDK workers, prior runtime snapshots and host middleware remain unchanged.

Branch firestore-authentication / 6807ed06. Baseline
/tmp/gopernicus-jobs-persistence-baseline.json covers 2022 files and 996 prior
dirty entries (--short -uall); /tmp/gopernicus-jobs-persistence-AUDIT-before.md
preserves AUDIT-001..018. Use task-relative hashes, preserving all previous work.
Go 1.26.1; goimports /Users/jrazmi/go/bin/goimports;
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. No root module; 42 workspace modules.

## Bounded implementation plan

1. Reproduce SQL GetLatestByKey choosing an older generation after a simulated
   backward clock, using a future stored timestamp and descending explicit IDs.
   Test Replace and EnqueueOnce after a terminal generation, independent keys,
   returned/read-back times and concurrent replacements on disposable stores.
2. Within the existing PostgreSQL advisory-key transaction / Turso BEGIN IMMEDIATE
   transaction, read the maximum stored created_at for the logical key before
   inserting. Advance the new timestamp if necessary, at one microsecond for
   PostgreSQL and one nanosecond for fixed-width Turso TEXT. Use it for created_at
   and updated_at. Keep scheduled_for, keyless admission, existing rows and public
   APIs unchanged; add no clock registry or schema generation column.
3. Check the new reads use the existing index and PostgreSQL schema qualification.
   Document that adjusted timestamps preserve generation order and are not exact
   wall-clock measurements. Existing malformed/misordered history cannot be
   reconstructed; the change applies to new admissions and does not backfill.
4. Use named lead-backend-engineer (existing inherited model; configured opus
   unavailable) for independent read-only scheduler failure/precision/concurrency
   review while SQL implementation proceeds. Keep scheduler fixes as explicit
   follow-up plans if they require contract/schema choices.
5. Update canonical jobs/store documentation, append AUDIT-019 for the implemented
   ordering behavior, and update the central/global handoff. No releases or
   external consumer writes. Live tests use new owned PostgreSQL/libSQL containers,
   cached images, loopback ephemeral ports and no host data volumes; remove them
   afterward. Existing local services remain untouched.

## Verification plan

Capture failing regressions before the fix, then passing regressions and live
fenced conformance (including contention) after it. PostgreSQL must pass bare and
named schema legs; local libSQL exercises the actual Turso adapter but does not
prove hosted-service behavior. Build/test/vet affected modules, focused race
checks and architecture guards. Preserve module/sum/schema/SDK files; record
exact changed paths, commands/results, cleanup and any skipped checks.

## SQL generation ordering: implemented

Both adapters now read MAX(created_at) across all surviving rows of the same key
inside the existing admission transaction. PostgreSQL truncates the current time
to microseconds before comparing, preventing a sub-microsecond difference from
collapsing on storage; Turso's canonical fixed-width TEXT preserves nanoseconds.
If needed, the new timestamp advances one stored tick. Existing key/index/locking
semantics are retained; no public clock, sequence column or migration was added.
The existing logical_key/created_at indexes cover this lookup by construction;
no query-plan benchmark or throughput measurement was performed.

The regression fixtures put the predecessor an hour ahead and use descending
explicit IDs, avoiding sleeps or global clock hooks. Before the fix, sequential
Replace, EnqueueOnce after cancellation, and concurrent Replace all inserted a
backdated generation in both databases. The same tests now pass. Strengthened
checks prove future CreatedAt does not delay claiming, replaced/canceled leases
stay invalid, independent keys stay on wall time, and returned timestamps match
Get. Earlier memstore fixes remain unchanged.

AUDIT-019 records the observable timestamp change and all-writer rollout caveat.
No history is rewritten: pre-existing out-of-order generations cannot be reliably
reconstructed, and old binaries can still write backdated generations. Primary
jobs docs and five scheduler source comments also correct overstatements of scheduler crash guarantees discovered
below; scheduler behavior and port signatures have not been changed in this slice.

## Scheduler findings: review complete, fixes pending

Independent named backend review used temporary Go overlays and actual memory
repositories. These findings are separate from the implemented SQL fix.

### J4-P1: advancing before enqueue can lose an occurrence

Current fire order is ListDue → ClaimDue (advances next_run_at/last_run_at) →
EnqueueJob → SetLastJob. Make the enqueuer return a transient error immediately
after a successful claim: the tick returns nil, no job exists, and next_run_at is
already advanced. Restore the enqueuer and retry at the same clock: ErrNoWork.
A process crash between those operations leaves equivalent persistent state,
although no process-kill/durable-store recovery test has been performed here.

Confirmed with a due one-hour memory schedule: zero jobs, next advanced one hour,
LastRunAt populated. This is occurrence loss, not merely missing error reporting.
Returning the error would improve observability but does not make the occurrence
retryable. Deterministic enqueue IDs do not repair missing pending state.

### J4-P1: interval precision is inconsistent

The core accepts arbitrary positive Every durations. Two 100ms memory-store slots
within one second produce one job: IDs use NextRunAt.Unix(), and the duplicate
error from the second enqueue is treated as success. Both SQL serializers store
int64(Every/time.Second): adapter shape probes confirm 100ms becomes zero and
1.5s becomes one second. The test used actual serializer code through an overlay;
no persisted fractional-interval integration case was run in this slice.

Decision needed in the next bounded plan: require positive whole-second Every
values consistently, or support finer precision with appropriate SQL migration
and collision-free occurrence IDs. Also validate CronNext returns a strictly
advancing next time; current parser ports allow zero/nonadvancing results.

### J4-P2: older occurrences overwrite current last-job metadata

Pause occurrence A after ClaimDue, run the next occurrence B to completion, then
resume A. Both jobs exist, but unconditional SetLastJob lets A replace B's
LastJobID and move UpdatedAt backward while LastRunAt still describes B.
Confirmed deterministically with one-second memory intervals. All three store
implementations use unconditional latest-job writes; live SQL interleaving was
not reproduced. Conditional bookkeeping needs an explicit occurrence/claim
identity so stale acknowledgements cannot overwrite newer state.

### J4-P2: PostgreSQL Ensure can write back stale NextRunAt

Source finding, not yet live-reproduced: pgx Ensure selects the schedule without
FOR UPDATE, calculates next_run_at, then writes that earlier value on update.
ClaimDue can advance it between those statements under the connector's ordinary
transaction isolation. Review locking/atomic upsert, including concurrent creation
by Name. Turso's BEGIN IMMEDIATE serializes its corresponding read/write sequence.
Existing kind/enabled CAS guards remain valuable and must be preserved.

### J4 decision: which configuration does a claimed occurrence use?

An actual listed schedule remains claimable after a same-kind/same-spec Ensure
changes its payload: the enqueued job uses the old listed payload. Kind changes
are guarded, but payload/tenant edits have no version check. This is an observed
race semantic to specify, not automatically a defect. Decide whether the snapshot
linearizes at list, claim or durable occurrence creation; use that decision when
designing dispatch and conditional acknowledgements.

## Recommended next scheduler work

1. Write a bounded plan for interval/recurrence validation, PostgreSQL Ensure/CAS
   serialization and conditional last-job bookkeeping. Review the compatibility
   cost of whole-second validation versus preserving fractional schedules.
2. Plan durable dispatch explicitly. For queue and schedules in the same database,
   a narrow jobs-specific store operation can atomically claim/advance, insert the
   job and update metadata. Merely wrapping today's calls in Transact is insufficient:
   the ordinary queue/schedule adapters use their DB directly instead of joining
   ambient transactions.
3. For separate queue and schedule stores, persist a pending occurrence with a
   stable ID and immutable job template before enqueue. Retry after failure or
   restart, then acknowledge conditionally. Queue idempotency handles uncertain
   enqueue outcomes; this requires a persisted recovery scan/contract.

Compare these options against actual hosts before adding an abstraction. Moving
enqueue ahead of ClaimDue or attempting compensating rollback does not reliably
close the crash window and can defeat re-kind/disable protection. The bounded
validation/bookkeeping fixes alone must not be described as solving occurrence
loss. SDK generic workers remain outside this design.

## Verification record

- `/tmp/gopernicus-jobs-persistence-checks.py before`: new live regression cases
  failed on both adapters as expected; log /tmp/gopernicus-jobs-persistence-before.log.
- The same script with `after`: full store test suites with -race -count=1 passed
  on PostgreSQL bare tables, local libSQL (-tags=integration), and PostgreSQL named
  schema jobs_persistence_audit. Log /tmp/gopernicus-jobs-persistence-after.log.
- After strengthening claim/lease assertions, the script's focused `before` mode
  (the mode name selects tests, not expected failure) passed on both fixed
  adapters. Log /tmp/gopernicus-jobs-persistence-focused-final.log. Only regression
  tests changed after the full live race run; production logic did not. Five later scheduler source changes are comments only,
  verified by reversing those edits against the task baseline hashes.
- Scheduler probes: `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -overlay
  /tmp/gopernicus-j4-audit-overlay.json ./pockets/jobs/internal/logic/schedulesvc
  -run TestJ4Probe -v -count=1` passed, demonstrating the current defects above.
  Log /tmp/gopernicus-j4-audit-probe.log; source /tmp/gopernicus-j4-audit-probe_test.go.
  SQL serializer probes use /tmp/gopernicus-j4-sql-shape-overlay.json. These overlay
  probes inspect existing behavior and were not committed as desired-behavior tests.

Live stores were newly created from cached images postgres:17 (5c855ad7b85e) and
ghcr.io/tursodatabase/libsql-server (07d5da358f37), with ephemeral loopback ports,
no host data volumes and no access to existing local service data. Their IDs and
connection settings are in /tmp/gopernicus-jobs-persistence-stores.json. Initial
Docker discovery was denied by the sandbox; scoped elevated local access succeeded.
No automatic approval rejection occurred.

- `python3 /tmp/gopernicus-jobs-persistence-build-vet.py`: build/vet passed in
  jobs core and both SQL modules; core tests passed; Turso integration-tag vet
  passed. The separate live suites above provide both adapters' test runs.
  Log /tmp/gopernicus-jobs-persistence-build-vet.log.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make guard`: all 23 guards passed.
  Log /tmp/gopernicus-jobs-persistence-guard.log.
- `make docs-build`: passed; log /tmp/gopernicus-jobs-persistence-docs.log.
  Nonfatal warnings concern untracked update dates and update-notifier permissions.
- Named backend review of final ordering code/tests/migration notes found no
  blockers; no duplicate test runs or reviewer source edits.
- Cleanup confirmed both recorded audit containers removed, including automatic
  anonymous-volume cleanup. An initial immediate check raced Docker's asynchronous
  auto-removal; waiting for removal completed cleanup successfully. Existing
  local services were untouched; state JSON records cleaned_up=true.
- Final goimports is clean for all nine changed Go files; git diff --check passed.
  SHA-256 comparison confirms unchanged SDK, dependencies/checksums, schema SQL,
  generated artifacts and previous AUDIT entries. No unresolved check failures.

Hosted Turso, external consumer execution, real provider calls, end-to-end
process-crash recovery and the full jobs audit remain unverified. The full
42-module suite and browser/host UI checks were not repeated for this adapter
change; live adapter behavior was exercised directly. No publication or consumer
edits. Next: the bounded scheduler plan described above, preserving recorded
SQL verification and explicit unfixed dispatch-loss semantics.


## Exact task-relative changed paths

17 paths relative to the 2022-file baseline; 996 earlier dirty entries preserved.
Two production functions changed for ordering; five other Go files have only
scheduler-comment corrections. Temporary probes, service metadata and logs are
under /tmp. No commit or release.

```text
AUDIT.md
RELEASING.md
plans/framework-audit-jobs.md
plans/framework-audit.md
plans/jobs-persistence-audit.md
pockets/jobs/README.md
pockets/jobs/domain/schedule/schedule.go
pockets/jobs/internal/logic/schedulesvc/service.go
pockets/jobs/memstore/schedules.go
pockets/jobs/stores/pgx/README.md
pockets/jobs/stores/pgx/fenced.go
pockets/jobs/stores/pgx/fenced_timestamps_test.go
pockets/jobs/stores/pgx/schedules.go
pockets/jobs/stores/turso/fenced.go
pockets/jobs/stores/turso/fenced_timestamps_integration_test.go
pockets/jobs/stores/turso/schedules.go
workshop/documentation/docs/pockets/jobs.md
```
