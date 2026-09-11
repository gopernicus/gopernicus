# Jobs and events audit execution

Status: JOBS/EVENTS COMPLETE — 2026-09-10. Authentication/authorization follow-up
is complete (audit/planning only) in [authentication-authorization-audit.md](authentication-authorization-audit.md). Owner authorized completing the remaining jobs and
events review and justified fixes while away. Parent: [framework-audit.md](framework-audit.md).
Jobs index: [framework-audit-jobs.md](framework-audit-jobs.md). Existing
[framework-audit-events.md](framework-audit-events.md) covers SDK events; this
plan also covers the events pocket and its stores.

## Preconditions and boundaries

- Branch firestore-authentication / 6807ed06. Preserve the large earlier audit
  diff. Task baseline /tmp/gopernicus-jobs-events-baseline.json records 2025 files
  and 1000 earlier dirty entries; AUDIT prefix saved separately under /tmp.
- Go 1.26.1; 42 workspace modules, no root go.mod. Use goimports and
  GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
- Keep SDK workers generic, host-owned middleware/policy/lifecycle, stdlib-only
  SDK and datastore-free pocket cores. Prefer clear concrete operations over
  generic transaction/lifecycle managers. Preserve previous audit fixes.
- Review code and consumers before deciding changes. Document consequential
  choices and implemented compatibility changes in AUDIT.md. No publishing,
  external consumer edits, CMS work or authorization-listing implementation.
- Live store checks use newly created disposable loopback services only; existing
  databases and containers are out of scope. Clean owned services after checks.
- Use matching named repository review/implementation roles for independent
  tasks. Their configured opus model is unavailable; use the inherited model
  while preserving role scope. Read-only reviewers do not edit source.

## Sequence and completion criteria

1. [x] Scheduler: finish validation, concurrent Ensure, metadata, and recoverable
   dispatch design; implement with meaningful failure/concurrency regressions
   across memory/PostgreSQL/Turso. Describe delivery and missed-window semantics.
2. [x] Jobs queue/fenced/runtime: trace all J1–J6 areas, distinguish confirmed bugs
   from optional features, fix justified issues, compare store behavior and
   host-visible inspection/setup. Preserve generic workers and staged handlers.
3. [x] Events pocket: audit construction/Close/logging/middleware, identity and
   event visibility, SSE lifetime/drops, outbox append/delivery/ack and store
   transactions. Implement verified corrections and migration instructions.
4. [x] Verification: affected-module build/test/vet, meaningful race regressions,
   live store conformance/rollback/contention, actual host flows where practical,
   architecture guards, docs build/typecheck and repository check at completion.
5. [x] Handoff: reconcile master and pocket indexes, decisions, exact changed
   files, commands/results and remaining limits. Audit completion means every
   listed area has an explicit disposition; green tests alone are insufficient.
6. [x] Owner follow-up (separate audit plan): after jobs/events, deeply audit and plan authentication
   and authorization, including the three authorization-listing store/join
   scenarios. REVIEW/PLANNING ONLY for these two pockets; no implementation.

## Decisions and execution evidence

### Scheduler design

- Persist one pending occurrence per schedule in a new job_schedule_occurrences
  table (memory map in memstore). ClaimDue atomically guards ID/slot/kind/spec,
  reads the current payload/tenant, advances NextRunAt and stores an immutable
  occurrence. Payload/tenant edits take effect at claim; changes after claim do
  not rewrite admitted work. Compare spec as well as kind to reject stale next
  calculations. A pending occurrence prevents a second claim for that schedule.
- The engine prepares due schedules, then lists and delivers pending occurrences
  of its handler kinds. An occurrence survives failures, restart, schedule edits,
  disable and deletion. After enqueue succeeds (or the stable ID already exists),
  acknowledge by occurrence JobID. Ack atomically removes pending state and
  records matching LastRunAt/LastJobID; repeated/late acknowledgements are no-ops.
  UpdatedAt must not move backwards. Stable IDs include fractional slot precision.
- No generic outbox abstraction or same-database-only transaction requirement:
  this protocol works with separate schedule/queue stores and all three shipped
  adapters. Delivery is at least once; queue IDs must stay retained until pending
  dispatch is acknowledged. Host effects must be idempotent. No claim of exactly
  once side effects. No backfill can recover occurrences previously lost.
- Keep whole-second Every intervals, minimum one second, matching existing SQL
  persistence; reject fractional/negative values instead of silently truncating.
  Cron parsers must return a nonzero time strictly after now. Validate schedule
  name/kind and JSON payload before persistence. Missed windows coalesce to one
  occurrence with the next time computed from now.
- PostgreSQL Ensure becomes one atomic upsert, preserving NextRunAt unless the
  spec changes; this handles concurrent creation and eliminates stale rewinds.
  Pending-table migration is additive, host-owned and applied before new writers.
  Old/new scheduler writers must not overlap during rollout. Rollback requires
  draining pending occurrences before reverting the binary/schema.

### Events design

Scheduler review addition: pending delivery records an attempt before enqueue and
orders pending work by attempt count, then slot/ID. This prevents a failing oldest
batch from starving healthy later work even under a frozen clock. Count/rotation
belongs to occurrence persistence; no generic retry manager or host dead-letter
policy is added. Ensure and SetEnabled also preserve monotonic UpdatedAt.

- Add Config.Logger, snapshot middleware, public Close for the bus subscription,
  nil-router validation and explicit nil-poller repository errors. Close does not
  own the host bus or HTTP shutdown; document the lifetime order.
- Add a host-owned per-event visibility callback receiving request context,
  principal and event. Require explicit policy when mounting streams; nil fails
  closed. Apply it to both general/resource streams, in addition to resource
  authorization/type filters. Hosts may explicitly allow all for a shared feed.
  No SDK tenant policy: the missing policy seam is the issue, not tenant naming.
- Outbox Append validates record identity/type before insertion and preserves
  opaque payload bytes including empty input. PostgreSQL needs an additive
  migration from JSON payload storage to bytea with documented historical JSON
  normalization limits. Preserve documented Append own-transaction behavior and
  explicit AppendTx transaction participation; test rollback through AppendTx.
  Poll acknowledgement follows delivery; replay uses immutable EventID. Review
  malformed historical rows and operator recovery in documentation.

### Jobs queue/fenced follow-up design

- Confirmed by public-behavior probes: make failed duplicate-ID memory Replace
  atomic; clone mutable payloads and timestamp pointers at memory store input and
  output boundaries, including fenced checkpoints. Preserve lease checks.
- Honor persisted per-job MaxAttempts in ordinary processing, with configured
  default only for jobs lacking one. Reject blank kinds in public fenced
  admission, preserving documented optional logical keys.
- Prevent ordinary Complete/Fail from mutating terminal jobs. The ordinary queue
  still has no lease token and does not promise fencing of stale running workers;
  use the fenced queue when that guarantee is required.
- Fix jobs-minimal sibling HTTP/runtime error cancellation so startup/fatal
  errors shut down the other component and return. Verify actual startup failure.
- Shared conformance regressions should pin these behaviors across SQL and memory.

## Verification and changed files

All implemented work is unreleased. AUDIT-001..019 remain byte-for-byte intact;
AUDIT-020/021 contain consumer migration. No dependency/module/generated-source
changes or authentication/authorization pocket implementation.

Passed (GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache, goimports formatter):

- Module-local go build ./..., go test ./..., go vet ./... for jobs/events,
  selected PG/Turso adapters, SDK workers/web and affected host examples.
- go test -race ./... in jobs/events and auth-cms; SDK workers and SSE race checks.
- Live jobs: python3 /tmp/gopernicus-jobs-events-live.py all ran full
  go test -race -count=1 ./... on PG bare/named and Turso with -tags=integration.
  Logs /tmp/gopernicus-jobs-events-all-{pgx-bare,pgx-jobs_events_audit,turso-bare}.log.
- Scheduler migration upgrades: same script with upgrade, same three store legs.
  Live previous0003→0004 retained schedules and admitted a pending occurrence.
- Actual separate-process recovery: /tmp/gopernicus-schedule-restart executable,
  admit then recover on each SQL store. The first process exited before enqueue;
  the next runtime executed exactly once in that fixture, acknowledged and drained.
  Log /tmp/gopernicus-jobs-events-process-restart.log. This fixture does not imply
  exactly-once side effects for arbitrary failures.
- Events live PG bare/named/decoy, legacy0001→0002, raw bytes, batch validation and
  AppendTx rollback; live libSQL full integration race including matching0002.
  Logs /tmp/gopernicus-events-{pgx-default,pgx-named,turso}.log. No skips in these
  store legs. Real SSE HTTP fixtures cover visibility, isolation and cancellation.
- make check passed (42 modules, isolated scaffold/generated checks, guards,
  build/test/vet, integration/live-tag compilation):
  /tmp/gopernicus-jobs-events-make-check.log. The final Turso no-op migration was
  subsequently validated by its scoped live race/export checks; no repeated broad
  test run was needed. pnpm typecheck and pnpm build passed in workshop/documentation.
- Independent named backend reviews accepted scheduler and final events code.
  /tmp/gopernicus-scheduler-final-review.md and /tmp/gopernicus-events-final-review.md.
  Queue implementation/evidence: /tmp/gopernicus-jobs-queue-implementation-report.md;
  events implementation: /tmp/gopernicus-events-implementation-report.md.

Only initial command-location/sandbox setup failures occurred; corrected module
commands and authorized local-listener checks passed. No unresolved verification
failure. Hosted Turso, production providers, external consumers and unrelated
live Firestore/Redis tests were not run. Their tag compile gates passed where
included in make check. Nonfatal docs warnings concerned untracked file dates
and local updater configuration.

Disposable services were isolated loopback-only containers without host volumes.
Final state files /tmp/gopernicus-jobs-events-stores.json and
/tmp/gopernicus-events-owned-stores.json both record cleaned_up=true; owned IDs
were stopped and disappearance verified. Existing containers/databases untouched.

Task-relative paths below compare against the 2025-file initial baseline, not
HEAD. The separate authentication/authorization plan is subsequent work; its
source trees remain unchanged.


### Exact changed files (79)

- AUDIT.md
- examples/auth-cms/cmd/server/main.go
- examples/auth-cms/internal/outboxmem/outboxmem.go
- examples/jobs-minimal/README.md
- examples/jobs-minimal/cmd/server/lifecycle_test.go
- examples/jobs-minimal/cmd/server/main.go
- plans/framework-audit-events-pocket.md
- plans/framework-audit-jobs.md
- plans/framework-audit-pocket.md
- plans/framework-audit.md
- plans/jobs-events-audit-implementation.md
- pockets/events/README.md
- pockets/events/domain/outbox/outbox.go
- pockets/events/events.go
- pockets/events/events_test.go
- pockets/events/internal/inbound/events/routes.go
- pockets/events/internal/inbound/events/routes_test.go
- pockets/events/internal/logic/hub/hub.go
- pockets/events/internal/logic/hub/hub_test.go
- pockets/events/poller.go
- pockets/events/poller_test.go
- pockets/events/stores/pgx/README.md
- pockets/events/stores/pgx/migrations/0002_event_outbox_payload_bytes.sql
- pockets/events/stores/pgx/outbox.go
- pockets/events/stores/pgx/payload_migration_test.go
- pockets/events/stores/pgx/postgres.go
- pockets/events/stores/turso/README.md
- pockets/events/stores/turso/appender_test.go
- pockets/events/stores/turso/migrations/0002_event_outbox_payload_bytes.sql
- pockets/events/stores/turso/outbox.go
- pockets/events/stores/turso/turso.go
- pockets/events/stores/turso/validation_test.go
- pockets/events/storetest/payload_test_contract.go
- pockets/events/storetest/reference_test.go
- pockets/events/storetest/storetest.go
- pockets/events/visibility_test.go
- pockets/jobs/README.md
- pockets/jobs/admission_test.go
- pockets/jobs/domain/job/fenced.go
- pockets/jobs/domain/job/job.go
- pockets/jobs/domain/schedule/schedule.go
- pockets/jobs/fenced.go
- pockets/jobs/fenced_admission_test.go
- pockets/jobs/internal/logic/queuesvc/service.go
- pockets/jobs/internal/logic/schedulesvc/recovery_test.go
- pockets/jobs/internal/logic/schedulesvc/service.go
- pockets/jobs/internal/logic/schedulesvc/service_test.go
- pockets/jobs/jobs.go
- pockets/jobs/jobs_test.go
- pockets/jobs/memstore/fenced.go
- pockets/jobs/memstore/job.go
- pockets/jobs/memstore/queue.go
- pockets/jobs/memstore/schedules.go
- pockets/jobs/retry_limit_test.go
- pockets/jobs/stores/pgx/conformance_test.go
- pockets/jobs/stores/pgx/migrations/0004_job_schedule_occurrences.sql
- pockets/jobs/stores/pgx/occurrences.go
- pockets/jobs/stores/pgx/occurrences_upgrade_test.go
- pockets/jobs/stores/pgx/postgres.go
- pockets/jobs/stores/pgx/queue.go
- pockets/jobs/stores/pgx/schedules.go
- pockets/jobs/stores/turso/conformance_integration_test.go
- pockets/jobs/stores/turso/migrations/0004_job_schedule_occurrences.sql
- pockets/jobs/stores/turso/occurrences.go
- pockets/jobs/stores/turso/occurrences_upgrade_integration_test.go
- pockets/jobs/stores/turso/queue.go
- pockets/jobs/stores/turso/schedules.go
- pockets/jobs/stores/turso/turso.go
- pockets/jobs/storetest/fenced.go
- pockets/jobs/storetest/occurrences.go
- pockets/jobs/storetest/ownership.go
- pockets/jobs/storetest/storetest.go
- sdk/pkg/web/sse.go
- sdk/pkg/web/sse_deadline_test.go
- sdk/pkg/workers/retry_limit_test.go
- sdk/pkg/workers/runner.go
- workshop/documentation/docs/pockets/events.md
- workshop/documentation/docs/pockets/jobs.md
- workshop/documentation/docs/sdk/pkg.md
