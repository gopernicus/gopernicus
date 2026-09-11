# Jobs runtime and scheduler ownership fixes

Status: COMPLETE — 2026-09-10.
Parent: [framework-audit.md](framework-audit.md).
Central jobs checklist: [framework-audit-jobs.md](framework-audit-jobs.md).
Findings: [framework-audit-pocket.md](framework-audit-pocket.md), P1 and P3.

## Scope and preconditions

Implement the approved bounded jobs slice: staged handler validation, matching
queue/scheduler kind snapshots, independent runtimes, and optional no-route
registration. Keep generic SDK workers and worker/job middleware contracts.
No scheduler durability redesign, live registry, store/schema change, events or
authorization implementation, CMS cleanup or external consumer edits.

Branch firestore-authentication / 6807ed06; 992 prior dirty entries (git status
--short -uall). /tmp/gopernicus-jobs-runtime-baseline.json records 2019 existing
files; /tmp/gopernicus-jobs-runtime-AUDIT-before.md preserves AUDIT-001..017.
Compare against these task-relative hashes, not HEAD. Existing sdk/pkg and shared
pockets migrations are uncommitted and must remain intact. 42 workspace modules,
Go 1.26.1; formatter /Users/jrazmi/go/bin/goimports; use
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. No root go.mod.

## Implementation plan

1. Add failing regressions for handlers populated after NewService (empty and
   nonempty initial maps), foreign due schedules, invalid late entries, and two
   runtimes made from successive versions of one map. Prove actual queue/schedule
   outcomes and handler identity. Keep EnsureSchedule usable before runtime creation.
2. Retain the host map at NewService for sequential staged startup. NewRuntime
   validates each final entry while copying, derives kinds from the copy, and
   passes those kinds to both queue and scheduler work. Remove service-level
   scheduler kinds; WorkFunc takes and copies its runtime's kinds without mutating
   shared service state. Concurrent map mutation during construction/registration
   is unsupported; later edits do not alter an existing runtime. No new registry.
3. Remove Register's unrelated unfenced-handler requirement. Preserve the optional
   method and logging; it mounts no routes or goroutines. Cover enqueue-only and
   fenced-only construction and remove the unnecessary call from jobs-minimal.
4. Update jobs API comments, README and canonical docs. Append AUDIT-018 for
   startup/behavior changes without altering earlier entries; update release and
   audit handoff. Authorization listing stays queued for its full pocket audit.
5. Use named lead-backend-engineer for a read-only review of the bounded plan and
   final diff. Its configured opus is unavailable; retain the existing agent's
   inherited model and role scope. No extra implementation delegation.

## Verification plan

- Capture failing behavioral regressions before implementation, then rerun them.
- Build/test/vet jobs core, both jobs store modules, cron integration and the
  jobs-minimal/auth-cms consumers. Run jobs race tests and all architecture guards.
- Exercise jobs-minimal's actual HTTP enqueue → handler → completion and orderly
  shutdown using an owned local process; use only memstore, no providers/databases.
- Build documentation after changes. Broaden tests only for changed dependencies,
  failures or concrete unresolved concerns; the preceding path rename already ran
  the full 42-module suite and scaffold/typecheck checks.
- Verify formatter, git diff --check, prior AUDIT bytes, unchanged module/sum and
  generated files, task-relative inventory; record skips and failures explicitly.

## Results

Implemented the jobs P1/P3 slice with no new public abstraction. NewRuntime
validates while copying and derives kinds from that copy; the scheduler Service
no longer holds handler kinds. Each WorkFunc copies its explicit kind slice.
Register is optional logging, including enqueue-only/fenced-only services, and
jobs-minimal omits it. Generic SDK workers, both middleware boundaries, fenced
runtime semantics and scheduling/store protocols are unchanged.

The source review clarified that staging means mutating the same allocated map,
not reassigning Config.Handlers. API/docs/migration notes make that explicit and
retain host ownership of mutable state captured by functions. The named backend
review found the plan and final source/docs ship-ready, with no blocking findings;
the reviewer performed no edits or duplicate suites.

### Regression evidence

Before the fix, the new tests failed with all expected concrete symptoms:
Register rejected enqueue-only/fenced-only services; NewRuntime accepted late
nil/empty-key handlers; the empty initial map advanced a foreign schedule and
created a job; a nonempty initial map failed to schedule the newly added kind.
Log: /tmp/gopernicus-jobs-runtime-before.log (exit 1, expected).

After the fix, the same regressions pass, including initially empty/nonempty
staging, removed handlers, independent runtimes from one service, old/new handler
identity, matching direct/scheduled job outcomes and untouched foreign schedules.
The older runtime runs after the newer one is built and the source map cleared.
A separate scheduler regression mutates the supplied slice and interleaves work
functions to verify independent filters. EnsureSchedule works before handlers
are populated. Log: /tmp/gopernicus-jobs-runtime-after.log (exit 0).

### Verification

- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test ./... -run
  'TestNewRuntime_ValidatesStagedHandlers|TestRuntime_StagedHandlersSnapshot|TestRegister_OptionalAndNoRoutes|TestWorkFunc'
  -count=1` in pockets/jobs: passed after reproducing failures before the fix.
- `python3 /tmp/gopernicus-jobs-runtime-checks.py`: go build ./..., go test ./...
  and go vet ./... passed in each of pockets/jobs, pockets/jobs/stores/pgx,
  pockets/jobs/stores/turso, integrations/scheduling/robfig-cron,
  examples/jobs-minimal and examples/auth-cms. Log:
  /tmp/gopernicus-jobs-runtime-checks.log.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race ./...` in
  pockets/jobs: all packages passed, including memstore/storetest and both
  runtime suites. Log: /tmp/gopernicus-jobs-runtime-race.log.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make guard`: all 23 guards passed.
  Log: /tmp/gopernicus-jobs-runtime-guard.log.
- `make docs-build`: passed. Log: /tmp/gopernicus-jobs-runtime-docs.log.
  Nonfatal Docusaurus warnings concern untracked-file update dates and its local
  update-notifier config permissions; static generation succeeded.

No datastore/provider test variables were configured. Real PostgreSQL/Turso
conformance and provider calls were skipped; their normal module checks passed.
No integration/live tags, browser suite, external consumer execution or published
module resolution was exercised. Full 42-module/scaffold/typecheck checks were
not repeated for this bounded jobs-only change; the prior path rename passed
those, and no SDK/module/template/TypeScript source changed here. No release,
production mutation or external repository write occurred.

AUDIT-018 records the startup/ownership behavior changes; AUDIT-001..017 are
byte-preserved. The audit index points next to events cleanup/middleware/logger
ownership, then the remaining mounting findings and full pocket audits. The full
jobs domain/store audit remains open; authorization listing's three store/join
cases stay queued, and CMS feature work remains deferred.


### Actual host behavior and final checks

`python3 /tmp/gopernicus-jobs-runtime-host-probe.py` passed (exit 0). It builds
jobs-minimal into a temporary directory, starts it with only owned loopback/server
configuration and memstore, checks GET /healthz, and POSTs a known job. Observed
HTTP enqueue → handler → completed log in 0.001 seconds on this run (an observation,
not a performance guarantee). It waits for the specific heartbeat-15s interval
schedule's job to complete, then sends SIGTERM after a five-second slow handler
starts. The handler finished and recorded completion before the runtime drained;
the process exited 0. No Register call occurred. The process stopped and temporary
binary/directory were removed. Logs: /tmp/gopernicus-jobs-runtime-host-probe.log
and /tmp/gopernicus-jobs-runtime-host.log.

The first probe saw the minute-cron job before the interval and mislabeled its
output as interval work; source-specific assertions corrected the probe and the
repeat verified the actual interval job. This was a probe correction, not a
framework failure; no broader passing suites were repeated.

Final goimports check is clean for all six changed Go files; git diff --check
passed. Task-relative hashes confirm no SDK, module requirement/checksum or
checked-in generated artifact changes, and unchanged AUDIT-001..017 bytes.
No unresolved verification failures remain. Live datastore and full jobs
persistence/durability auditing remain explicit future work.

### Exact task-relative changed paths

14 paths relative to the 2019-file baseline; all 992 earlier dirty entries were
preserved. Temporary scripts, logs and hashes are under /tmp. No commit or release.

```text
AUDIT.md
RELEASING.md
examples/jobs-minimal/README.md
examples/jobs-minimal/cmd/server/main.go
plans/framework-audit-pocket.md
plans/framework-audit.md
plans/jobs-runtime-implementation.md
pockets/jobs/README.md
pockets/jobs/internal/logic/schedulesvc/service.go
pockets/jobs/internal/logic/schedulesvc/service_test.go
pockets/jobs/jobs.go
pockets/jobs/jobs_test.go
pockets/jobs/runtime_config_test.go
workshop/documentation/docs/pockets/jobs.md
```
