# Jobs pocket audit: central checklist

Status: COMPLETE — 2026-09-10. J1–J6 reviewed; justified correctness fixes are
implemented and verified, with explicit future-feature dispositions below.
Parent: [framework-audit.md](framework-audit.md). Latest implementation:
[jobs-events-audit-implementation.md](jobs-events-audit-implementation.md), AUDIT-020.

This is the starting point for all jobs audit work across context windows. Keep
completed work, unresolved findings and review questions here; link detailed
evidence and implementation plans rather than duplicating their execution logs.
Unchecked review areas are questions to investigate, not approved redesigns or
claims that the current behavior is broken. Add findings as the audit progresses.

## Design decisions to preserve

- SDK workers remain generic in sdk/pkg/workers. Hosts may supply their own work
  or queues without importing pockets/jobs. sdk/capabilities/work remains the
  narrow keyed-work protocol used by other pockets.
- Jobs owns durable queue/schedule policy and its adapters. Host applications
  choose handlers, middleware, repositories, timing and lifecycle.
- Preserve the two middleware boundaries: worker iterations before claim and
  job processing after claim. Gates can be deliberate architectural extension
  points even when few current callers use them.
- Preserve staged startup through the same allocated handler map, snapshots at
  NewRuntime, optional registration and queue-only/fenced-only operation.
- Prefer readable control flow and justified packages over additional managers,
  registries or generic abstractions. Names and package boundaries remain reviewable.
- Record implemented breaking changes in root [AUDIT.md](../AUDIT.md). Keep
  proposed changes here until implemented. No implicit consumer edits or releases.

## Completed work to build on

| Work | Status and evidence |
| --- | --- |
| Final handler validation; matching queue/scheduler kinds; independent runtimes; optional Register | Implemented and verified, unreleased: [jobs-runtime-implementation.md](jobs-runtime-implementation.md), [AUDIT-018](../AUDIT.md#audit-018-jobs-runtime-handler-snapshots). |
| SQL latest-generation ordering under repeated/backward clocks | Implemented and live-verified on PostgreSQL/libSQL: [jobs-persistence-audit.md](jobs-persistence-audit.md), [AUDIT-019](../AUDIT.md#audit-019-jobs-sql-generation-ordering). No schema change or historical backfill; all writers must upgrade. |
| Generic workers, middleware/gating, persistence outcomes and fatal sibling-pool shutdown | SDK-led implementation and jobs bridge verification: [workers-cleanup.md](workers-cleanup.md). This did not audit all jobs domain/store behavior. |
| Memory store latest-generation ordering under repeated/backward clocks | Corrected during [identity-cryptography-cleanup.md](identity-cryptography-cleanup.md); SQL follow-up remains below. |
| Kind-filtered claims and expected-kind schedule CAS | Historical released implementation: [jobs-kind-filtered-claim.md](jobs-kind-filtered-claim.md). Preserve its concurrency regressions. |
| Ordinary runtime drain and heartbeat passthrough | Historical implementations: [jobs-graceful-drain.md](jobs-graceful-drain.md), [jobs-heartbeat-passthrough.md](jobs-heartbeat-passthrough.md). Current SDK paths are sdk/pkg; old plans retain historical paths. |

The completed audit passed SDK/jobs race tests, full PostgreSQL bare/named-schema
and libSQL race/conformance, migration upgrade checks, separate-process persisted
schedule recovery on both SQL stores, actual host startup failure/drain tests,
42-module make check, 23 guards and documentation checks. The execution plan owns
exact commands, files and limits; hosted Turso and external consumers were not run.

## Known carry-forward findings

- [x] **SQL latest-generation ordering.** Fixed in
  [jobs-persistence-audit.md](jobs-persistence-audit.md); before/after regressions
  and live race/conformance passed on PostgreSQL (bare/named schema) and local
  libSQL. The original finding: PostgreSQL
  and Turso select the latest logical-key job by created_at DESC, job_id DESC,
  while inserts use wall-clock timestamps. Equal timestamps with a lower new ID
  or a backward clock can select a superseded generation. The memory correction
  did not fix SQL. The adapters now advance new same-key creation timestamps at
  stored precision under existing admission locks, including terminal history.
  Evidence: [framework-audit-work.md](framework-audit-work.md), durable-ordering
  follow-up; current stores/{pgx,turso}/fenced.go. No schema migration or backfill.
- [x] **Scheduler occurrence loss, precision and metadata.** Fixed by atomic
  pending-occurrence admission, stable-ID retries, kind/spec guards and exact
  acknowledgement. Attempts rotate failures; PG Ensure is an atomic upsert.
  Whole-second intervals and advancing cron results are validated. Payload/tenant
  snapshot at claim, deletion survival and stale acknowledgement are specified.
  See the implementation plan and AUDIT-020 for custom ports and migration 0004.
- [x] **Stale-pending/orphaned-kind visibility reviewed.** Ordinary Queue.List,
  schedule ListPending, timestamps and worker-kind logs support host inspection.
  Fenced global backlog/tenant filters and admin UI remain future features; known
  IDs/keys or host SQL cover existing fenced inspection. Framework code cannot
  know which host kinds are intentionally unserved. Historical issue #39 was not
  queried; its external state is not needed for this source audit disposition.
- [ ] **Consumer cancellation follow-up.** Earlier GPS360 source review found a
  comps workflow path that can summarize cancellation and return job success.
  Recheck the consumer's current source during adoption, preserving intentional
  partial-success/no-change outcomes. This is a consumer workflow finding, not
  an established jobs runtime defect. See [workers-cleanup.md](workers-cleanup.md).

## Audit coverage

| Slice | What to trace and decide | Starting source |
| --- | --- | --- |
| J1 — Public API and host setup | Enqueue-only, ordinary, fenced and combined setup; configuration/defaults/errors; facade names and size; lifecycle ownership; staged map and middleware ergonomics. Preserve completed fixes while reviewing the rest. | jobs.go, fenced.go, README.md, examples/jobs-minimal, examples/auth-cms |
| J2 — Ordinary queue and execution | Admission validation, payload ownership, idempotency, priority/due order, kind filtering including stale claims, attempts/retries/dead letters, deferral, error propagation, claim/processing/persistence timeouts and cancellation. | domain/job/job.go, internal/logic/queuesvc, internal/logic/runtime, memstore/queue.go, storetest |
| J3 — Fenced/keyed work | EnqueueOnce/Replace concurrency, logical-key scope, latest generation, lease ownership/expiry, checkpoint integrity, replacement during processing, terminal callbacks, retry/deferral and purge. Include the SQL ordering finding above. | fenced.go, domain/job/fenced.go, memstore/fenced.go, storetest/fenced.go, stores/{pgx,turso}/fenced.go |
| J4 — Scheduling durability | Ensure/upsert semantics, enable/disable and re-kinding races, recurrence validation, missed windows, multiple schedulers, restart/crash recovery and cancellation. Trace ListDue → ClaimDue → EnqueueJob → SetLastJob across each failure point; verify exactly-once/at-least-once documentation against what persists. Check subsecond intervals against slot-ID precision, UTC/cron behavior, batching and fairness. | domain/schedule, internal/logic/schedulesvc, memstore/schedules.go, stores/{pgx,turso}/schedules.go, integrations/scheduling/robfig-cron |
| J5 — Store conformance and migrations | Compare memory/PostgreSQL/Turso behavior for each preceding slice; transaction participation, locks/CAS, isolation, time precision, indexes, pagination and errors. Exercise real contention and rollback on disposable stores; inspect migration compatibility before proposing changes. | storetest, stores/{pgx,turso}, their migrations and datastore integrations |
| J6 — Operations, documentation and simplification | Host-visible states/logging/heartbeat, backlog and orphan kinds, safe inspection/retry examples, honest delivery guarantees, minimum ordinary/fenced host examples, and any justified removal/renaming/consolidation. Assess future features separately from defects. | README.md, workshop/documentation/docs/pockets/jobs.md, examples, representative consumers |

All six slices have an explicit disposition:

- J1 keeps optional registration, staged handler snapshots and host-owned runtime
  setup. No additional registry/manager/module consolidation is justified.
- J2 corrects per-job retry ceilings, ordinary JSON parity, memory ownership and
  terminal resurrection. Ordinary retries count failures and remain immediate;
  stale running workers are intentionally unfenced. Processing/storage deadlines,
  future backoff/retention/redrive and host policy remain explicit responsibilities.
- J3 fixes failed duplicate Replace atomicity and checkpoint/result aliasing,
  rejects empty kinds, and preserves generation/lease semantics. Keys are globally
  scoped unless the host namespaces them. Terminal hooks are best effort; lease
  renewal, global inspection and durable cleanup are future features.
- J4 implements recoverable pending dispatch across separate stores, immutable
  snapshots, precision validation, concurrency guards and metadata consistency.
  Invalid legacy recurrences/missing cron parser can fill a pre-claim due batch;
  operators repair/disable those rows. No silent drop or automatic catch-up storm.
- J5 shared conformance and live contention cover memory/PG/Turso. Migration 0004
  is additive, host-owned, and needs old-writer shutdown and pending drain before
  rollback. Earlier migrations are unchanged; no historical lost-work backfill.
- J6 docs explain supported guarantees and limits, inspection and smallest host
  flows. The actual HTTP bind failure now cancels the jobs runtime; runtime failure
  cancels HTTP. No admin UI or generic policy manager is introduced.

## How to continue

1. Check this index, the latest jobs implementation plan and the global handoff.
   Confirm branch/dirty state and capture a new task-relative baseline; never
   overwrite previous audit changes or treat old consumer pins as current.
2. Jobs audit work is complete. Implement future features only from a concrete
   host requirement; adopt AUDIT-018..020 when updating consumers. Authentication
   and authorization are the next REVIEW/PLANNING ONLY phase, including the three
   listing strategies. Events completed in the same execution plan.
3. For each slice, record confirmed bugs, design tradeoffs, incomplete features
   and simplification options separately, with real caller impact and evidence.
   Write a bounded implementation plan before significant code changes.
4. Verify with repository goimports, build/test/vet in affected modules, meaningful
   regressions/race tests and an actual host flow where practical. Use disposable
   databases for store claims; report skipped/live checks explicitly. Reuse prior
   passing evidence when unchanged rather than repeating every suite by default.
5. Update this checklist and the global handoff after each slice. Link the plan
   containing exact changed files, verification commands/results and unresolved
   failures; append AUDIT migration entries only for implemented changes.

The owner authorized finishing jobs/events while away, then auditing/planning
both authentication and authorization without implementation. Continue from
[authentication-authorization-audit.md](authentication-authorization-audit.md).
CMS remains deferred; external consumer cancellation follow-up remains an adoption
task, not a framework bug or permission to edit consumer repositories.

## Index creation and verification

Created 2026-09-10 from existing plans and source locations; no new correctness
audit or implementation in this documentation task. Branch firestore-authentication
/ 6807ed06; baseline /tmp/gopernicus-jobs-audit-index-baseline.json covers 2021 files
and 995 earlier dirty entries. Changed paths: plans/framework-audit-jobs.md,
plans/framework-audit.md and plans/jobs-runtime-implementation.md. All 11 relative
link targets, trailing whitespace and git diff --check passed; task-relative
hashes confirm only those three documents changed. Go tests were not rerun for
these plan-only changes.
No source, AUDIT migration entries, dependencies or consumer repositories changed.
