# `features/jobs` — design (durable queue + workers + cron schedules)

Status: **IMPLEMENTED 2026-07-02** — jobs-v1 milestone executed (all 8
phases; logs in `.claude/plans/jobs-v1/*`). Executed with four logged
amendments beyond the ratified R2/R3: (1) `storetest.Lease` raised
250ms→3s after remote-turso latency evidence (the reclaim arm was
double-claiming in-flight jobs whose Claim→Complete cycle exceeded the
lease); (2) Job backing fields are `JobID/JobStatus/Retries` with
`ID()/Status()/RetryCount()` methods (the §3.1 field/method name collision
is unsatisfiable in Go as written); (3) `CronSchedule` is a TYPE ALIAS so
third-party parsers wire with zero adapter; (4) postgres payload column is
`JSON` not `JSONB` (byte-exact round-trips for an opaque column).
(original status: RATIFIED 2026-07-02, as amended by R2/R3)
Date: 2026-07-02
Depends on: `features/README.md` (the charter this design is held to),
`.claude/plans/restructure/capability-map.md` (Jobs & events rows + ratified
YOUR CALL #6), `.claude/plans/restructure/00-overview.md` (the constitution),
`.claude/plans/restructure/auth-feature-design.md` (the fidelity bar and the
precedents this design cites), and the **concurrent**
`.claude/plans/roadmap/datastore-portability.md` (owns the cross-cutting
multi-datastore policy — see §6's scope note).

This is a design document only. Nothing here is built. A future milestone
phases from it. It is written *before* auth v1 executes, deliberately, to
surface feature intersections early (§7) and to hold every future feature to
the multi-datastore bar (§6).

Prior art salvaged (design, not code, per the capability map):
`gopernicus-original/sdk/workers/{pool,runner,model,middleware}.go`,
`core/jobs/scheduler/scheduler.go`, `core/repositories/jobs/{jobqueue,
jobschedules}/`, `workshop/migrations/primary/000{5,6}_*.sql`.

## 1. Scope v1

**v1 ships:**

- **`sdk/workers`** — a new stdlib-only sdk facility: the worker pool
  (adaptive polling, wake channel, middleware, panic recovery, graceful
  drain) and the generic job runner (`Runner[T Job]`: claim → hooks →
  process-with-retry → complete/fail). §2.
- **`features/jobs`** — a datastore-free feature core: a durable job queue
  (enqueue with idempotency, atomic claim, retry, dead-letter, stale-claim
  recovery) and cron/interval recurring schedules (`ListDue`/`ClaimDue`
  compare-and-set, deterministic refire, fire-once catch-up). §3.
- **`integrations/scheduling/robfig-cron`** — the one third-party module,
  satisfying the feature's `CronParser` port (ratified YOUR CALL #6). §4.
- **Three stores out of the box**: `stores/turso`, `stores/postgres`, and a
  memory store, all run against one exported conformance suite. §6.
- **A zero-infra proof host.** §8.

**v1 explicitly excludes:**

| excluded | why v1 skips it | where it lands |
|---|---|---|
| Admin HTTP surface / UI (job list, retry button, schedule editor) | Real view scope (templ, theme, forms) with no current operator demanding it; the original shipped only generated CRUD bridges here (workshop-v2 territory) | jobs v2; namespace `/jobs/*` claimed now (§3) so it has a home |
| `feature.Mount` jobs-registrar field | No producer feature exists yet; adding it now creates a port with zero consumers — the same plurality failure the auth design's W3 pass axed `sdk/identity` for. Shape is fully designed in §5 so adding it later is cheap | `sdk/feature`, the day cms scheduled-publishing or an auth maintenance job actually registers work from inside its own `Register` |
| Event bus / transactional outbox | Ratified capability-map call #4: build `sdk/events` + the outbox only when `features/events` gives it a real second consumer. Jobs must not smuggle it in early | `features/events` (builds ON `sdk/workers` — §7.1) |
| `tenant_id` / `aggregate_type` / `aggregate_id` queue columns | Tenancy is deferred to auth v2+ (ratified call #3); aggregate columns served the original's event-sourcing ambitions, which the outbox row owns | returns with tenancy / `features/events` if ever needed |
| Distributed leader election, priority queues per topic, delayed-retry backoff curves in SQL, rate-limited kinds | The original had none of these; adding them out-engineers it | only on demonstrated need |
| Tracer hooks in `sdk/workers` | The original's `tracer.go` is the same vestigial-tracing shape D9 removed from `sdk/cacher`; the OTEL story returns deliberately via the ratified `sdk/tracing` port (capability-map Telemetry row), which does not exist yet | re-threaded when `sdk/tracing` lands (a `Middleware` can carry it without port changes) |

**Why this cut is principled.** The original's jobs capability was exactly
three things: `sdk/workers` (pool + runner), the scheduler engine
(`Scheduler[S]` with `ListDue`/`ClaimDue` CAS), and the two entity
repositories + tables (0005/0006). v1 is that surface — minus tenancy
columns it can't use and a tracer stub the constitution already ruled on,
plus one deviation *upgraded from the original*: stale-claim recovery (§6.3),
because without it "durable at-least-once" is a false claim, not restraint
(a job claimed by a crashed worker stays stuck forever). That is a latent
bug in the original, not a feature of its scope.

## 2. `sdk/workers` — the execution kernel

**Why sdk, not feature-internal.** The capability map's ratified row places
pool + runner in sdk. It passes the admission test cleanly: stdlib-only
(`sync`, `sync/atomic`, `time`, `log/slog`); multiple honest consumers
(jobs' runtime is the first; `features/events`' outbox poller is the named
next, per the ratified build order; any host's own background loop after
that); observable behavior sdk can define and conformance-test (idle
backoff, drain-on-cancel, panic containment — the original's
`pool_test.go`/`runner_test.go` port over as the suite); zero domain
knowledge. For `Runner[T Job]` specifically — whose only *orchestrator*
consumer today is `features/jobs` — the defense is that it is a generic
**mechanism** parameterized by a port, like `sdk/slug` or the cursor codec:
the plurality test applies to the `JobStore[T]` port it consumes (turso,
postgres, and memory stores all honestly implement it, §6), not to the
single generic orchestrator over it.

**Public surface sketch** (salvaging the original's shape; renames noted):

```go
package workers // sdk/workers — stdlib only

var (
    ErrNoWork         = errors.New(...) // idle: pool backs off to IdleInterval
    ErrWorkerShutdown = errors.New(...) // stop this worker
    ErrPoolShutdown   = errors.New(...) // stop the pool; surfaced on Errors()
)

type WorkFunc func(ctx context.Context) error
type Middleware func(WorkFunc) WorkFunc

func WithWorkerID(ctx context.Context, id string) context.Context
func WorkerIDFromContext(ctx context.Context) string // was GetWorkerID; sdk/logging naming

type Pool struct{ ... }
func NewPool(work WorkFunc, opts ...PoolOption) *Pool
// PoolOptions: WithName, WithWorkerCount, WithPollInterval, WithIdleInterval,
// WithMiddleware, WithLogger, WithWakeChannel
func (p *Pool) Run(ctx context.Context) error // was Start; blocks, drains on cancel
func (p *Pool) Stats() Stats
func (p *Pool) Errors() <-chan error

// The queue mechanism. Method names lose the original's Get* prefixes (Go idiom)
// and Checkout becomes Claim — one verb across sdk/workers and features/jobs (§6.2).
type Job interface {
    ID() string
    Status() string
    RetryCount() int
}
type JobStore[T Job] interface {
    Claim(ctx context.Context, workerID string, now time.Time) (T, error) // ErrNoWork when empty
    Complete(ctx context.Context, jobID string, now time.Time) error
    Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
}
type ProcessFunc[T Job] func(ctx context.Context, job T) (T, error)
type PreProcessHook[T Job]  func(ctx context.Context, job T) error
type PostProcessHook[T Job] func(ctx context.Context, job T, err error) error

type Runner[T Job] struct{ ... }
func NewRunner[T Job](store JobStore[T], process ProcessFunc[T], log *slog.Logger,
    opts ...RunnerOption) *Runner[T]
// RunnerOptions: WithMaxAttempts (default 3), WithClock (testing)
func (r *Runner[T]) AddPreProcessHooks(...)  / AddPostProcessHooks(...)
func (r *Runner[T]) WorkFunc() WorkFunc
```

**Deviations from the original, called out so they read as intentional:**

1. **`tracer.go` does not carry over** even though the capability-map row
   lists it in the file inventory. D9's rationale governs: no vestigial
   tracing fields; when `sdk/tracing` lands (ratified target), a
   `Middleware`/hook carries spans without any port change here.
2. **The env-tag `Options` struct is dropped.** Sizing comes in as
   `PoolOption`s; a host that wants env-driven sizing loads its own struct
   via `sdk/config` and passes the values through. sdk packages should not
   each own an env-parsing convention.
3. **`WithWakeChannel` stays, but its v1 justification is in-feature, not
   speculative.** The first consumer is `features/jobs`' own enqueue path:
   `Service.Enqueue` signals the runtime's pool so a fresh job runs promptly
   instead of waiting out a poll interval (§3.4 makes this wiring explicit —
   it is a correctness/latency coupling, not a nicety). `features/events`'
   outbox poller is the named *future* second consumer (§7.1); it is not the
   load-bearing argument.

## 3. Feature anatomy — `features/jobs`

Mirrors `features/cms`/`features/auth` anatomy (`features/README.md` §2).
`go.mod`: **stdlib + sdk only** (leaner than cms — no view deps; same bar as
auth).

```
features/jobs/          (trio layout per the ratified 2026-07-02 re-layout)
  jobs.go              Repositories, Config, HandlerFunc, CronParser/CronSchedule ports,
                       Service, NewService, Runtime, NewRuntime, Register — the entire
                       host-facing surface, in one file (auth precedent, W3-hardened)
  logic/job/           job.Job entity + job.Enqueue input + job.QueueRepository port
  logic/schedule/      schedule.Schedule entity + schedule.Spec + schedule.Repository port
  internal/logic/queuesvc/    enqueue validation, idempotency, wake signaling
  internal/logic/schedulesvc/ the fire engine: ListDue → ClaimDue CAS → deterministic
                       JobID → enqueue (ErrAlreadyExists swallowed) → SetLastJob
  internal/logic/runtime/     pool assembly: Runner over QueueRepository + the scheduler WorkFunc
  storetest/           EXPORTED conformance suite all stores run (§6.5)
  memstore/            in-core reference in-memory stores (ratified R3) — public,
                       stdlib-only, G2-clean; backs storetest AND the proof host (§6.4)
  stores/turso/        separate module: SQL + canonical migrations, source Name = "jobs"
  stores/postgres/     separate module: same contract, pgx dialect (§6.1 scope note)
```
(jobs v1 has no HTTP surface, so no `internal/inbound/` until the v2 admin
UI; entity import paths throughout this doc read as `logic/job` etc.)

### 3.1 Entities and ports

```go
// job/job.go
type Status string
const (
    StatusPending    Status = "pending"
    StatusRunning    Status = "running"    // the original's STAGED, renamed
    StatusCompleted  Status = "completed"
    StatusFailed     Status = "failed"     // retryable; rescheduled to pending
    StatusDeadLetter Status = "dead_letter"
)

type Job struct {
    ID            string
    Kind          string          // the original's event_type; renamed to avoid
                                  // colliding with the future events feature's vocabulary
    Payload       json.RawMessage
    Status        Status
    Priority      int
    RetryCount    int
    MaxAttempts   int
    WorkerName    string
    FailureReason string
    ScheduledFor  time.Time
    ClaimedAt     *time.Time      // the original's staged_at — LOAD-BEARING for §6.3
    CompletedAt   *time.Time
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
// Job satisfies sdk/workers.Job via ID()/Status()/RetryCount() methods
// (compile-time asserted in the core: var _ workers.Job = Job{}).

type Enqueue struct {
    ID           string          // optional; caller-supplied = idempotency key
                                 // (the scheduler's deterministic refire relies on this)
    Kind         string
    Payload      json.RawMessage
    ScheduledFor time.Time       // zero = now
    Priority     int
    MaxAttempts  int             // zero = Config default
}

type QueueRepository interface {
    // Enqueue inserts one job; errs.ErrAlreadyExists when ID is already present.
    Enqueue(ctx context.Context, in Enqueue) (Job, error)
    // Claim atomically transitions exactly one due job to running for workerID,
    // or returns workers.ErrNoWork. "Due" includes stale reclaims (§6.3).
    Claim(ctx context.Context, workerID string, now time.Time) (Job, error)
    Complete(ctx context.Context, jobID string, now time.Time) error
    // Fail increments retry_count and either reschedules (pending) or
    // dead-letters when attempts are exhausted.
    Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
    Get(ctx context.Context, id string) (Job, error)
    List(ctx context.Context, f ListFilter, req repository.ListRequest) (repository.Page[Job], error)
}
```

**Port reconciliation — one claim path, one owner.** `job.QueueRepository`
is deliberately a strict superset of `sdk/workers.JobStore[job.Job]` and
satisfies it **structurally** (same `Claim`/`Complete`/`Fail` names and
signatures; compile-time asserted in the core:
`var _ workers.JobStore[job.Job] = (QueueRepository)(nil)` via an interface
conversion check). There is no adapter layer, no second interface for store
authors to puzzle over, and exactly one empty-claim contract: **`Claim`
returns `workers.ErrNoWork`** (not `errs.ErrNotFound`) so the runner's idle
backoff engages with zero translation. The feature core importing
`sdk/workers` for the sentinel is inward-pointing and legal (rule 8). This
mirrors the original, where `jobqueue.Repository` carried a compile-time
`workers.JobStore` assertion.

```go
// schedule/schedule.go
type Spec struct {
    Cron  string        // 5-field cron or @descriptor; requires Config.Cron (§4)
    Every time.Duration // stdlib-only fixed interval; no parser needed
}                       // exactly one of Cron/Every set; validated at Ensure

type Schedule struct {
    ID        string
    Name      string          // unique; the Ensure upsert key
    Kind      string          // job kind fired into the queue
    Spec      Spec
    Payload   json.RawMessage
    Enabled   bool
    NextRunAt time.Time
    LastRunAt *time.Time
    LastJobID string
    CreatedAt time.Time
    UpdatedAt time.Time
}

type Ensure struct {
    Name    string
    Kind    string
    Spec    Spec
    Payload json.RawMessage
}

type Repository interface {
    // Ensure upserts by Name (create or update spec/kind/payload), setting
    // NextRunAt = next for creates and spec changes.
    Ensure(ctx context.Context, in Ensure, next time.Time) (Schedule, error)
    ListDue(ctx context.Context, now time.Time, limit int) ([]Schedule, error)
    // ClaimDue is a pure value compare-and-set on next_run_at: true = this
    // caller won the (schedule, slot) pair. §6.2.
    ClaimDue(ctx context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error)
    SetLastJob(ctx context.Context, id, jobID string, now time.Time) error
    Get(ctx context.Context, id string) (Schedule, error)
    List(ctx context.Context, req repository.ListRequest) (repository.Page[Schedule], error)
    SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
    Delete(ctx context.Context, id string) error
}
```

### 3.2 Host-facing surface (`jobs.go`)

```go
type Repositories struct {
    Queue     job.QueueRepository
    Schedules schedule.Repository // nil = queue-only host; Runtime skips the scheduler
}

// HandlerFunc executes one job of a registered kind. Handlers are host-supplied
// data (constitution rule 5): closures over whatever services the host built —
// including other features' services, with zero ports (§7.2).
type HandlerFunc func(ctx context.Context, j job.Job) error

type Config struct {
    Handlers      map[string]HandlerFunc // kind → handler; required non-empty to build a Runtime
    Cron          CronParser             // nil OK until a Spec.Cron schedule appears; then error (§4)
    Workers       int                    // queue pool size; 0 → default (e.g. 4)
    PollInterval  time.Duration          // 0 → default
    IdleInterval  time.Duration          // 0 → default
    MaxAttempts   int                    // default job attempts; 0 → 3
    ScheduleBatch int                    // due-schedules per tick; 0 → 20
}

func NewService(repos Repositories, cfg Config) (*Service, error)

// Enqueue is the primitive-typed entry point. Its signature is a HARD
// constraint: stdlib types only (string, json.RawMessage), so a consuming
// feature's own narrow enqueuer port matches it structurally with zero
// import of features/jobs (§5.2 / constitution rule 6).
func (s *Service) Enqueue(ctx context.Context, kind string, payload json.RawMessage) (jobID string, err error)

// EnqueueJob is the full-fidelity variant for hosts and internal use
// (idempotency ID, ScheduledFor, Priority).
func (s *Service) EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error)

// EnsureSchedule validates in.Spec (cron via cfg.Cron; Every > 0) and upserts.
func (s *Service) EnsureSchedule(ctx context.Context, in schedule.Ensure) (schedule.Schedule, error)

// NewRuntime takes the BUILT Service — never (repos, cfg) a second time — so
// the wake channel and dependencies are shared by construction (§3.4).
func NewRuntime(svc *Service) (*Runtime, error)

// Run blocks: the queue pool (workers.Runner over repos.Queue, dispatching by
// Kind to cfg.Handlers) plus, when repos.Schedules != nil, a single-worker
// scheduler pool. Cancel ctx to drain gracefully: in-flight jobs finish and
// persist Complete/Fail, then Run returns.
func (r *Runtime) Run(ctx context.Context) error

func Register(m feature.Mount, repos Repositories, cfg Config) error
```

### 3.3 `Register` in v1 — the literal body, stated so it isn't ceremony

Jobs is unusual: its primary artifact is a **runtime the host must
explicitly run**, not a route table. v1's `Register` therefore does exactly
two things: (1) validates the `(repos, cfg)` pair — non-nil `Queue`,
handlers well-formed, `Cron` present if any configured schedule spec needs
it — returning the same errors `NewService` would, so a misconfigured host
fails at registration, not first-enqueue; (2) logs the feature's
registration on `m.Logger`. It registers **no routes** (the claimed
namespace `/jobs/*` is a documentation-level reservation, per
`features/README.md` §4 item 1, until the v2 admin surface exists) and
starts **no goroutines** — the framework never runs background work behind
the host's back, the exact philosophy D4 applies to migrations. Migrations
are registered by the store adapters, as with cms and auth. `NewService`/
`NewRuntime` alongside `Register` follows the auth precedent (`NewService`
alongside `Register`, all in `<name>.go`), and stretches the charter's
anatomy-table wording no further than auth already does — the anatomy-table
generalization is already flagged in auth-v1 phase 6.

### 3.4 Service/Runtime wiring — the wake channel is load-bearing

Auth's design flagged a benign "Service built twice" wrinkle. Jobs has a
**sharper** version that must be resolved by construction, not convention:
`Service.Enqueue` must signal the `Runtime`'s pool (a non-blocking send on a
buffered cap-1 wake channel, per `WithWakeChannel`'s documented protocol) or
low-latency processing silently degrades to poll-interval latency — a
failure nobody notices until production. Therefore:

- `Service` owns the wake channel; `Enqueue`/`EnqueueJob` perform the
  non-blocking send.
- `NewRuntime(svc *Service)` — taking the built `Service`, **not**
  `(repos, cfg)` — hands that same channel to the queue pool. One object
  graph, shared by construction; the "built twice" shape is impossible.
- When the runtime runs in a **separate process** (dedicated worker binary,
  §7.4), the wake send is a no-op across processes; the poll interval is the
  cross-process backstop, exactly as the original's wake design intended
  ("lost wake signals are tolerated").

## 4. The `CronParser` port + `integrations/scheduling/robfig-cron`

Ratified YOUR CALL #6: port + integration split, keeping `features/jobs`'
go.mod stdlib+sdk only.

```go
// jobs.go — feature-declared, consumer-owned (constitution rule 3)
type CronSchedule interface {
    Next(after time.Time) time.Time // zero Time = never fires again
}
type CronParser interface {
    // Parse validates a 5-field cron expression (plus @hourly-style
    // descriptors). Evaluation is UTC — v1 has no timezone support; the
    // port contract states it so an adapter can't silently localize.
    Parse(expr string) (CronSchedule, error)
}
```

`integrations/scheduling/robfig-cron` is its own module wrapping exactly
`github.com/robfig/cron/v3` (constitution rule 2), exposing a `Parser` type
satisfying `jobs.CronParser` structurally — configured with the original's
exact parser flags (`Minute|Hour|Dom|Month|Dow|Descriptor`, UTC), so what
`EnsureSchedule` accepts the fire engine can always evaluate.

**No stdlib naive cron parser ships as a default.** A hand-rolled cron
parser is a known bug farm (DOM/DOW OR-semantics, descriptor aliases) and
would fail the "sdk defines observable behavior" bar the moment it disagreed
with robfig on an edge case. Instead, the stdlib-only escape hatch is
**`Spec.Every time.Duration`** — fixed-interval schedules computed with bare
time arithmetic (`next = now.Add(Every)`), needing no parser at all. The
asymmetry mirrors auth's required-`Hasher` call: `Config.Cron` is required
*only when actually needed* — `EnsureSchedule` with a `Spec.Cron` and a nil
parser errors loudly; queue-only and interval-only hosts (including the
zero-infra proof, §8) carry zero integrations. Importing robfig-cron in a
"zero-infra" host is legitimate when wanted — it is a CPU-only library, the
same precedent auth set for bcrypt.

## 5. Mount evolution — the jobs registrar, designed but not added

### 5.1 The shape (so the day it's needed, it's a decision, not a design)

`features/README.md` §6 names a jobs registrar as a Mount candidate, "added
the day a real feature needs them." The shape, mirroring
`MigrationRegistrar`'s collect-now/host-drives-execution pattern:

```go
// sdk/feature (candidate — NOT built in jobs v1)
type JobHandlerFunc func(ctx context.Context, kind string, payload []byte) error

type JobRegistrar interface {
    // RegisterHandler declares a kind this feature can execute; errors on
    // duplicate kind (namespaced by convention: "<feature>.<verb>").
    RegisterHandler(kind string, fn JobHandlerFunc) error
    // RegisterSchedule declares recurring work; the host's jobs runtime
    // drives execution. Collect-now: registration never starts anything.
    RegisterSchedule(name, kind, cronExpr string, every time.Duration, payload []byte) error
}

// Mount gains: Jobs JobRegistrar — nil when the host mounts no jobs runtime,
// exactly as examples/minimal leaves Migrations nil today.
```

The host-side implementation would be a small collector in `features/jobs`
(or the host's own), folded into `Config.Handlers` + `EnsureSchedule` calls
before `Runtime.Run`. Payload/handler signatures use stdlib types only, so
`sdk/feature` never learns jobs' entity vocabulary.

### 5.2 Why v1 does NOT add it — and how features enqueue meanwhile

**Recommendation: defer.** Today the field would have zero consumers: auth
v1 needs no background work (§7.2's recommendation), cms scheduled
publishing is future scope, events doesn't exist. A Mount field with no
consuming feature is exactly the plurality failure the auth design's W3
adversarial pass axed `sdk/identity` for — and C3 already encodes the cheap
path: pre-v1, adding a named field later is a compatible change. This design
names the trigger precisely: **the first feature that needs to register
background work from inside its own `Register` call** adds `Mount.Jobs`
with the §5.1 shape.

Meanwhile the constitution already covers every near-term intersection
without any new contract:

- **Host-authored handlers need no ports at all.** `Config.Handlers` is
  host-supplied data; the host writes
  `"auth.purge_sessions": func(ctx, j) error { return authSvc.PurgeExpired(ctx) }`
  — a closure over another feature's service, wired in `main`, importing
  both modules only at the composition root. This covers "run feature X's
  maintenance on a schedule" with zero feature changes.
- **A feature enqueueing at runtime from inside its own service** (e.g. cms
  wanting to enqueue a publish job when an editor schedules a post) declares
  its own narrow port in its own public package, constitution rule 6:

  ```go
  // features/cms (illustrative, future)
  type TaskEnqueuer interface {
      Enqueue(ctx context.Context, kind string, payload json.RawMessage) (string, error)
  }
  ```

  `jobs.Service.Enqueue`'s primitive-typed signature (§3.2) matches this
  structurally — that stdlib-types-only constraint is the whole point, and
  is stated in `jobs.go` as a compatibility contract, not an accident.
  Neither feature imports the other; the host passes `jobsSvc` into
  `cms.Config`.

## 6. Storage — multi-datastore out of the box

**Policy note.** The cross-cutting rule — *every feature ships turso +
postgres + memory stores with a conformance suite* — is owned by the
concurrent `.claude/plans/roadmap/datastore-portability.md`. This section
conforms to that policy and designs jobs' instance of it; it does not
re-decide it. **Sequencing dependency, flagged honestly:** jobs' postgres
store requires `integrations/datastores/postgres` (pgx), which does not
exist yet — capability map row: backlog. **RESOLVED by ratified R2
(2026-07-02): the datastore-portability milestone's P1 owns building the pgx
integration and setting the ecosystem's pgx conventions; jobs consumes it
and never builds it.** The former phase 6 is struck from §10; the postgres
store phase depends on portability P1 instead.

### 6.1 Schema (canonical migrations, source Name `"jobs"`)

Salvaged from the original's 0005/0006, trimmed and corrected. Two tables,
per dialect in each store module (`TIMESTAMPTZ`/`JSONB` on postgres; `TEXT`
ISO-8601 timestamps / `TEXT` JSON on turso, matching the cms turso store's
conventions):

```sql
-- 0001_job_queue.sql (postgres flavor shown)
CREATE TABLE IF NOT EXISTS job_queue (
    job_id         TEXT        NOT NULL,
    kind           TEXT        NOT NULL,            -- was event_type
    payload        JSONB       NOT NULL DEFAULT '{}',
    status         TEXT        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed','dead_letter')),
    priority       INT         NOT NULL DEFAULT 0,
    retry_count    INT         NOT NULL DEFAULT 0,
    max_attempts   INT         NOT NULL DEFAULT 3,
    worker_name    TEXT,                            -- KEPT: reclaim + forensics
    failure_reason TEXT,
    scheduled_for  TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at     TIMESTAMPTZ,                     -- was staged_at; KEPT: drives §6.3
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT job_queue_pk PRIMARY KEY (job_id)
);
CREATE INDEX IF NOT EXISTS idx_job_queue_claim
    ON job_queue (scheduled_for, priority DESC, created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_job_queue_kind ON job_queue (kind, status);

-- 0002_job_schedules.sql
CREATE TABLE IF NOT EXISTS job_schedules (
    schedule_id  TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    kind         TEXT        NOT NULL,
    cron_expr    TEXT,                              -- exactly one of cron_expr /
    every_secs   BIGINT,                            -- every_secs set (Spec, §3.1)
    payload      JSONB       NOT NULL DEFAULT '{}',
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at  TIMESTAMPTZ NOT NULL,
    last_run_at  TIMESTAMPTZ,
    last_job_id  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT job_schedules_pk PRIMARY KEY (schedule_id),
    CONSTRAINT job_schedules_name_unique UNIQUE (name)
);
CREATE INDEX IF NOT EXISTS idx_job_schedules_due
    ON job_schedules (next_run_at) WHERE enabled = TRUE;
```

Dropped vs the original: `tenant_id`, `aggregate_type`, `aggregate_id`,
`correlation_id` (§1 exclusions). Renamed: `event_type`→`kind`,
`staged_at`→`claimed_at`, `STAGED`→`running`, status values lowercased.
`worker_name` and `claimed_at` are explicitly **kept** — they are the
columns stale-claim recovery stands on.

### 6.2 Claim semantics per dialect — defined at the port, implemented per store

The **port contract** is dialect-neutral: *`Claim` atomically transitions
exactly one due job to `running` for this worker and returns it, or returns
`workers.ErrNoWork`; two concurrent claimers never receive the same job;
"due" means `pending` with `scheduled_for <= now`, **or** `running` with an
expired lease (§6.3); selection order is priority DESC, then created_at.*

- **Postgres** — the original's proven shape:

  ```sql
  UPDATE job_queue
  SET status='running', worker_name=$1, claimed_at=$2, updated_at=$2
  WHERE job_id = (
      SELECT job_id FROM job_queue
      WHERE (status='pending' AND scheduled_for <= $2)
         OR (status='running' AND claimed_at < $3)      -- $3 = now - lease
      ORDER BY priority DESC, created_at
      LIMIT 1
      FOR UPDATE SKIP LOCKED)
  RETURNING job_id, kind, payload, ...;
  ```

  `FOR UPDATE SKIP LOCKED` gives contention-free concurrent claiming — N
  workers each lock a different row.

- **Turso/SQLite** — same statement minus `FOR UPDATE SKIP LOCKED` (SQLite
  has no row locks), with the status predicate repeated in the outer
  `WHERE`. SQLite's single-writer model serializes the whole
  `UPDATE … WHERE job_id=(SELECT … LIMIT 1) … RETURNING` statement, so the
  subquery evaluates against committed state and **double-claim is
  impossible**; zero rows returned maps to `workers.ErrNoWork`. The real
  hazard is not correctness but **`SQLITE_BUSY` under concurrent writers**
  (and, in Turso remote mode, per-statement network round-trips serializing
  claims — an inherent throughput ceiling for that mode, documented, not
  fixable here). The turso store must set `busy_timeout` / retry-on-busy so
  contention surfaces as waiting, not errors — and the conformance suite
  asserts it (§6.5). `RETURNING` requires SQLite ≥ 3.35; libsql satisfies
  this (the cms turso store's baseline).

- **Schedules `ClaimDue`** is a pure **value** compare-and-set — no locking
  construct at all, byte-identical semantics on both dialects:

  ```sql
  UPDATE job_schedules
  SET next_run_at = $2, last_run_at = $3, updated_at = $3
  WHERE schedule_id = $1 AND next_run_at = $4 AND enabled = TRUE;
  -- rows affected 1 → this caller won the (schedule, slot) pair
  ```

  Combined with the deterministic job ID (`sched_<scheduleID>_<slotUnix>`)
  and `Enqueue`'s `errs.ErrAlreadyExists` (which the fire engine swallows),
  N runtime instances need no leader election: a lost CAS is silence, a
  crash-window refire collapses into the idempotency key. Missed windows
  fire once — `next_run_at` advances from *now* (a 3-hour outage on an
  hourly schedule produces one job, not three). All salvaged verbatim from
  the original's scheduler design.

### 6.3 Stale-claim recovery — required, folded into `Claim`

A worker that crashes after claiming leaves a `running` row that, in the
original's design, is stuck forever — silently breaking the at-least-once
promise. v1 fixes this **inside the claim statement** (the
`status='running' AND claimed_at < now - lease` arm above) rather than as a
separate sweeper: one atomic statement, no second loop to schedule, no
second thing to forget to run. The **lease duration is store-adapter
configuration** (e.g. `turso.NewQueueStore(db, turso.WithLease(15*time.Minute))`,
default 15m), not a `Claim` parameter — keeping the port signature identical
to `workers.JobStore[T]` (§3.1). A reclaimed job re-runs; handlers are
therefore documented as at-least-once/idempotent-preferred, which is the
standard contract of every claim-based queue. (Consultation upgraded this
from a YOUR CALL to required — see Consultation notes.)

### 6.4 The memory store

`features/jobs/memstore` — a **public package inside the feature core
module** (ratified R3, reconciling this design's original `stores/memory`
module proposal with portability DP2): stdlib-only, G2-clean (it is not a
`stores/*` module and carries no driver), importable by both the proof host
(§8) and `storetest`. It earns the in-core-package placement because a
lease-respecting concurrent queue is too substantial to duplicate
example-locally — simple features keep DP2's test-scoped reference +
example-local memstores; jobs is the named exception class. It implements
both ports with a mutex, honestly: `Enqueue` enforces ID uniqueness with
`errs.ErrAlreadyExists` (the memstore-honesty lesson from phase 2 W7 —
enforce what the doc comment promises, and test it), `Claim` honors
ordering, lease reclaim, and `workers.ErrNoWork`.

### 6.5 Conformance — `features/jobs/storetest`

An exported suite (`storetest.RunQueue(t, func() job.QueueRepository)`,
`storetest.RunSchedules(t, ...)`) that all three stores run. Asserts, beyond
CRUD/pagination: enqueue idempotency (`ErrAlreadyExists` on duplicate ID);
claim ordering (priority, then age); `scheduled_for` gating; retry →
dead-letter transitions at `max_attempts`; lease-expiry reclaim; `ClaimDue`
CAS (a stale `prevNextRunAt` never wins); and **concurrent-claim safety** —
G goroutines claiming N jobs receive N distinct jobs with no spurious
errors (`SQLITE_BUSY` must surface as retry/wait inside the adapter, never
as a failed claim). Honesty note, flagged by consultation: against the
mutex-backed memory store the concurrency assertions are trivially green —
they are only load-bearing against the real dialects. So the suite runs
in-process for memory, and **env-gated** for turso and postgres (following
the existing precedent of `features/cms/stores/turso/entries_integration_test.go`:
a real database URL via env var, skip otherwise — no testcontainers harness
pulled forward from workshop v2). CI/`make check` coverage of the env-gated
legs is a portability-plan concern.

## 7. Intersections (the reason this design exists now)

### 7.1 jobs ↔ events

`features/events`' transactional-outbox poller (capability map, ratified
build order #5) is a `workers.WorkFunc` on a `workers.Pool` — **not** a
`Runner` (the outbox is its own dispatch loop, not a claim/complete queue).
What `sdk/workers` must therefore expose for events, all in v1's surface
(§2): `Pool` + `NewPool` + `Run(ctx)`; `WorkFunc` + the `ErrNoWork` idle
contract; `WithWakeChannel` (the original's `events/wake` pattern — emit →
non-blocking wake → poller runs now instead of at next poll); `Middleware`.
Nothing events needs is missing, and nothing is added *only* for events —
each item has a v1 consumer inside jobs itself. Events' durable delivery
may also *enqueue* follow-up work; that goes through §5.2's consumer-port
pattern like any other feature. Sequencing jobs before events (ratified
order) means `sdk/workers` is built once, here.

### 7.2 jobs ↔ auth

**Recommendation: auth v1 stays exactly as designed — no background work,
no jobs dependency.** Session expiry is enforced on read (validation checks
`expires_at`); expired-row and used-verification-token purge is hygiene,
not correctness — the tables just grow slowly. Coupling the in-flight
auth-v1 milestone to an unbuilt jobs feature would be pure sequencing risk
for zero v1 value. When jobs exists, the cleanup lands as **host wiring
with no auth code change**: the host registers a
`"auth.purge_expired"` handler closing over an auth-service purge method
(auth v1.5 adds a small exported `PurgeExpired(ctx)` — one method, not a
port) and ensures a daily interval schedule. §5.2's no-ports-needed pattern,
exercised for real.

### 7.3 jobs ↔ cms

Scheduled publishing (publish entry X at time T) is the named future
consumer that likely triggers `Mount.Jobs` (§5.2) — cms's *service* would
enqueue at edit time via a cms-declared `TaskEnqueuer` port, and the host
registers a `"cms.publish_entry"` handler over the cms service. Notably the
Registry/EAV spine needs **no schema change** for this: the job payload
carries the entry ID; publishing is a status transition the entry spine
already owns. Nothing in jobs v1 blocks or presupposes it; it is recorded
so cms v-next designs against a named seam.

### 7.4 jobs ↔ host

The **host owns the run loop**, full stop — the execution analog of
"migrations are host-applied pre-boot." `Register` never starts goroutines;
the host explicitly calls `runtime.Run(ctx)`, in one of two topologies, both
first-class: (a) **in-process** — `go runtime.Run(ctx)` next to the HTTP
server, sharing one process (the examples' shape; enqueue→wake gives prompt
execution); (b) **dedicated worker binary** — a second composition root
(`cmd/worker`) wiring the same stores + `NewService`/`NewRuntime` without
the HTTP surface; cross-process wake doesn't exist, the poll interval is the
backstop (§3.4). Graceful shutdown is ctx-cancellation: the pool stops
claiming, in-flight handlers finish (bounded by the host's shutdown
timeout), `Complete`/`Fail` persist, `Run` returns; anything harder killed
mid-flight is recovered by the lease (§6.3). The host's signal handling,
not the feature's, decides when.

## 8. Zero-infra proof host

`examples/jobs-minimal` (or folded into an existing example — implementer's
call, mirroring auth A1's separate-host default): memory stores from
`features/jobs/memstore` (R3); two handlers — `"demo.print"` (logs its
payload) and `"demo.flaky"` (fails until `RetryCount >= 2`, proving
retry/dead-letter visibly); one `Spec.Every` 15-second schedule (stdlib
path) and one `Spec.Cron` `* * * * *` schedule via
`integrations/scheduling/robfig-cron` (CPU-only lib — the bcrypt precedent
for zero-infra); a **host-owned** `POST /enqueue` route (the host's own
handler calling `svc.Enqueue` — deliberately not a feature route, since v1
claims none). Boots with `go run ./cmd/server`, zero external
infrastructure.

**Real-interaction check for the future milestone** (green tests never
close this):

1. `go run ./cmd/server` — boots, logs pool + scheduler start.
2. `curl -fsS -X POST localhost:PORT/enqueue -d '{"kind":"demo.print","payload":{"msg":"hi"}}'`
   → 200 with a job ID, and the handler log line appears **promptly**
   (sub-second — this observably proves the enqueue→wake wiring, §3.4, not
   just eventual poll pickup).
3. Enqueue a `demo.flaky` job → observe two failure logs then a completion
   (retry path), and one forced-exhaustion variant reaching `dead_letter`.
4. Wait ~90s → the `Every` schedule fires several times and the cron
   schedule at the minute boundary; each firing logs a deterministic
   `sched_…` job ID; restart the server mid-window and confirm **no
   double-fire** (CAS + idempotent enqueue).
5. Ctrl-C while a slow job is in flight → the handler finishes, `Run`
   returns cleanly, no goroutine leak panic.
6. Record exact commands, ports, and observed log lines in the execution
   log.

## 9. Open decisions

| # | decision | proposed default | status |
|---|---|---|---|
| J1 | Postgres store in v1 | **Yes** — per jrazmi's multi-datastore directive; the pgx integration comes from portability P1 (R2), never from this milestone. Backend-lead dissent (defer postgres) recorded and overridden by the directive | **RATIFIED 2026-07-02** (as amended by R2) |
| J2 | Stdlib cron default | **No naive parser**; `Spec.Every` is the stdlib-only mode, cron expressions require `integrations/scheduling/robfig-cron`; `Config.Cron` errors loudly only when actually needed | YOUR CALL |
| J3 | `Mount.Jobs` registrar in v1 | **Not added**; shape fully designed (§5.1), trigger named (first feature registering work inside its own `Register`) | YOUR CALL |
| J4 | `Runner[T Job]` lives in sdk despite a single orchestrator-consumer | **Yes** — ratified capability-map row; defended as a generic mechanism whose plurality test applies to the `JobStore[T]` port (three honest store implementations), not the orchestrator | proposed default |
| J5 | Admin HTTP surface | **None in v1**; `/jobs/*` namespace claimed in docs; admin UI is v2 (and may inform the workshop-v2 generated-bridge question) | proposed default |
| J6 | Stale-claim recovery | **Required, folded into `Claim`** (lease as store config, default 15m) — upgraded from YOUR CALL by consultation: without it "durable" is false | proposed default |
| J7 | `sdk/workers` drops the original's tracer hooks | **Yes** (D9 precedent); deliberate deviation from the capability-map row's file inventory | proposed default |
| J8 | Vocabulary: `event_type`→`kind`, `STAGED`→`running`, lowercase statuses, `staged_at`→`claimed_at`, drop tenant/aggregate/correlation columns | **Yes** — avoids colliding with the future events feature's vocabulary and carries no tenancy it can't use | proposed default |
| J9 | Memory store placement | **Superseded by ratified R3**: in-core public package `features/jobs/memstore` (§6.4) — not a `stores/memory` module, not example-local | **RATIFIED 2026-07-02** (via R3) |

## 10. Rough phase breakdown (for the future milestone; sizes S/M/L)

| phase | what | size | depends on |
|---|---|---|---|
| 1 | `sdk/workers`: pool + runner + tests (ported/adapted from the original's suites), guard/Makefile module coverage | M | — |
| 2 | `features/jobs` core: entities, ports, `internal/{queuesvc,schedulesvc,runtime}`, `jobs.go` surface, unit tests with in-package fakes | L | 1 |
| 3 | `integrations/scheduling/robfig-cron` | S | 2 (port shape) |
| 4 | `memstore` in-core package (R3) + `storetest` conformance suite (suite lands here so it's proven against the reference first) | M | 2 |
| 5 | `stores/turso` (SQL, migrations source `"jobs"`, busy-timeout discipline, env-gated conformance leg) | M | 2, 4 |
| ~~6~~ | ~~`integrations/datastores/postgres`~~ — **STRUCK (ratified R2)**: built by datastore-portability P1, consumed here | — | — |
| 7 | `stores/postgres` (env-gated conformance leg) | M | 2, 4, datastore-portability P1 |
| 8 | `examples/jobs-minimal` proof host + real-interaction check + READMEs + guard generalization (G2 covers `features/jobs`, per auth-v1 A4's all-features pattern) + records | M | 2, 3, 4 |

Guard/infra notes for the milestone planner: extend the Makefile module list
(new modules in `check`), G2's feature-isolation grep must cover
`features/jobs` (auth-v1 A4 generalizes it — verify it landed), and the
existing G1/G3 greps already catch `sdk/workers` regressions. `RELEASING.md`
applies to four new taggable modules (`features/jobs`, its two dialect
stores, `integrations/scheduling/robfig-cron`); the pgx integration is
portability P1's to list.

## 11. Checklist trace (`features/README.md` §8)

1. **Standalone compile** — `cd features/jobs && go build ./...`; own go.mod.
2. **No datastore driver in go.mod** — stdlib + sdk only; cron parsing and
   drivers live behind ports in their own modules. Leaner than cms (no view
   deps), same bar as auth.
3. **Never imports integrations/examples/own stores** — by construction;
   G2 extension flagged (§10).
4. **`Register(mount, repos, cfg) error` conforms** — §3.3; reaches only
   `mount.Logger` in v1 (`Router` unused until v2's admin surface;
   `Migrations` is the store adapters' concern). `NewService`/`NewRuntime`
   are additional surface alongside it, in `jobs.go` — the auth precedent.
5. **Unique migration source name** — `"jobs"`, distinct from `"cms"`/`"auth"`.
6. **Minimal-host proof** — §8, memory stores, zero external infra.
7. **README documents route surface (`/jobs/*` claimed, none registered in
   v1), Config fields + defaults, Repositories ports** — this design doc is
   the source content.
8. **No init()/service locator** — `Service`/`Runtime` are explicit values;
   handlers are explicit Config data; `Register` starts nothing.
9. **No feature→feature imports** — cross-feature enqueue is the consumer's
   own port matched structurally by `Service.Enqueue`'s stdlib-typed
   signature (§5.2); background work on behalf of other features is
   host-authored closures (§7.2).

## Consultation notes

`lead-backend-engineer` reviewed the sketch (single hop). Verdict:
ship-with-edits. Material changes adopted into this design:

- **Service/Runtime wake wiring made structural** (§3.4): `NewRuntime(svc)`
  takes the built Service so enqueue→wake cannot be mis-wired; flagged as a
  correctness/latency coupling, not auth's benign "built twice" wrinkle.
- **Wake-channel justification reframed** onto jobs' own enqueue path as the
  v1 consumer; events demoted to named-future-second (§2 deviation 3, §7.1).
- **Port reconciliation added** (§3.1): `QueueRepository` structurally
  satisfies `workers.JobStore[job.Job]`; empty claim = `workers.ErrNoWork`,
  defined once at the port.
- **Stale-claim recovery upgraded to required and folded into `Claim`**;
  `claimed_at`/`worker_name` explicitly kept in the schema trim (§6.3, J6).
- **`Register`'s v1 body stated literally** so it reads as contract, not
  ceremony (§3.3).
- **SQLite hazard corrected**: the risk is `SQLITE_BUSY`/turso-remote
  serialization, not double-claim; busy-timeout discipline + conformance
  assertion added; `ORDER BY` added to the claim subquery (§6.2, §6.5).
- **`Enqueue` primitive-typed signature made a hard constraint** for
  rule-6 structural matching (§3.2, §5.2).
- **Runner-in-sdk defense** adopted (mechanism-parameterized-by-port framing, J4).
- **Recorded dissent**: the lead recommended cutting postgres from v1
  (matching auth's precedent; capability map lists the pgx integration as
  backlog with auth/cms as first consumers) unless the portability plan is
  ratified first. This design keeps postgres per jrazmi's explicit
  multi-datastore directive and the concurrent portability plan, with the
  pgx integration priced as its own phase — J1 carries the flag for
  ratification.

## Open questions

- ~~Does the portability plan want `integrations/datastores/postgres` in its
  own milestone rather than jobs'?~~ **RESOLVED (ratified R2): portability
  P1 owns it**; the env-gated conformance-leg CI story (`make test-stores`,
  loud skips, NOTES.md artifacts) is the portability plan's §4.3 too.
- Timezone-aware cron (non-UTC) — v1 is UTC-only by port contract (§4);
  confirm no near-term host needs otherwise.

## Recommended reviews

- **product-manager** — scope discipline: is J5 (no admin surface) the right
  v1 for operator value; is the proof host's demo shape convincing.
- **lead-backend-engineer** — post-hoc re-review of §6 SQL sketches once
  drafted into a phase plan (already consulted at sketch level).
- **data-integration-reviewer** — dialect parity, busy-timeout discipline,
  conformance-suite coverage, pgx integration conventions (J1/phase 6).
- **platform-sre** — lease-duration defaults, graceful-shutdown/drain
  semantics, worker-binary topology, guard/CI coverage of env-gated
  conformance legs, four new taggable modules per `RELEASING.md`.
- **architecture-steward** — J3/J4 placements (Mount deferral, Runner in sdk)
  and the `stores/memory`-as-module precedent (J9).
