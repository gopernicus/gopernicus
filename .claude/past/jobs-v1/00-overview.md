# jobs-v1 — milestone overview

Status: **RATIFIED** — phases cut 2026-07-02 from the ratified design at
`.claude/plans/roadmap/jobs-feature-design.md` (the DESIGN DOC — every
phase references it by section; do not re-decide anything it decides).
Milestone: `jobs-v1` — `sdk/workers` + `features/jobs` (durable queue +
cron/interval schedules) + `integrations/scheduling/robfig-cron` + memstore
+ storetest + both dialect stores + proof host.

## Inherited law

The constitution (`restructure/00-overview.md`, rules 1–8), the roadmap
rulings (R1–R10, `roadmap/00-intersections.md`), the trio layout
(`roadmap/feature-trio-relayout.md` — anatomy paths are `logic/job`,
`logic/schedule`, `internal/logic/{queuesvc,schedulesvc,runtime}`; no
`internal/inbound` until the v2 admin surface), store posture C, and the
charter (`features/README.md`, checklist items 1–12) all apply unchanged.
Design decisions J1–J9 are RATIFIED (J1 as amended by R2: the pgx
connector comes from portability P1, never built here; J9 superseded by
R3: in-core `features/jobs/memstore` package). Notable ratified calls the
executors must not relitigate: NO naive cron parser (J2 — `Spec.Every` is
the stdlib path); `Mount.Jobs` NOT added (J3 — shape designed in §5.1,
deferred); `Runner[T]` lives in sdk (J4); no admin HTTP in v1 (J5 —
`/jobs/*` claimed in docs only); stale-claim lease recovery folded into
`Claim`, REQUIRED (J6); no tracer hooks (J7); the §6.1 vocabulary renames
(J8).

- **Executor model policy (jrazmi): implementation phases on
  `model: opus`; design/doc-judgment phases on `model: fable`. Never
  sonnet.**

## Phases (execute in order except where noted; design §10 numbering kept — 6 is STRUCK per R2)

| Phase | File | What | Executor model |
|---|---|---|---|
| 1 | `01-sdk-workers.md` | `sdk/workers`: pool + runner (design §2) | opus |
| 2 | `02-jobs-core.md` | `features/jobs` core: entities, ports, services, runtime, jobs.go (design §3) | opus |
| 3 | `03-robfig-cron.md` | `integrations/scheduling/robfig-cron` (design §4) | opus |
| 4 | `04-memstore-storetest.md` | in-core `memstore` package + `storetest` suite (design §6.4–§6.5) | opus |
| 5 | `05-store-turso.md` | `stores/turso` (design §6.1–§6.3) | opus |
| 7 | `07-store-postgres.md` | `stores/postgres` (consumes portability P1's connector) | opus |
| 8 | `08-proof-host.md` | `examples/jobs-minimal` + the §8 real-interaction protocol | opus |
| 9 | `09-docs-sync.md` | READMEs, charter/ARCHITECTURE/RELEASING/module-map sync, records, fresh-eyes | fable |

Dependencies: 2 needs 1. 3 needs 2 (port shape). 4 needs 2. 5 needs 2+4.
7 needs 2+4 (P1 connector already exists). 8 needs 2+3+4. 9 needs all.
(Phase-cut note: design §10's phase 8 bundled proof host + docs; split
here into 8+9 mirroring the auth-v1 precedent — docs judgment runs on
fable. Logged as a cut-time refinement of the explicitly-rough §10, not a
design change.)

## Loop protocol

Same as auth-v1's: one phase per leg; read this overview + the phase file
+ THE DESIGN DOC fully; preconditions → work items in order → acceptance →
real-interaction check → dated execution-log entry → stop. Surgical diffs;
goimports; premise-false → closest correct thing + log divergence;
constitution/ratified-decision conflict → STOP and flag.

**Standing real-interaction check** (every phase): `make check` green (all
modules + guards), boot `examples/minimal` (:8081), `GET /` and
`GET /products/widget-3000` → 200s, kill, port free.

**Jobs proof-host check (phases 8–9; design §8 verbatim — green tests
never close it):**
1. `go run ./cmd/server` boots, logs pool + scheduler start.
2. `curl -fsS -X POST localhost:PORT/enqueue -d '{"kind":"demo.print","payload":{"msg":"hi"}}'`
   → 200 + job ID, handler log line appears **promptly** (sub-second —
   observably proves the enqueue→wake wiring, §3.4).
3. `demo.flaky` job → two failure logs then completion (retry path); one
   forced-exhaustion variant reaches `dead_letter`.
4. Wait ~90s: the `Every` schedule fires repeatedly + the cron schedule at
   the minute boundary, each with a deterministic `sched_…` job ID;
   restart mid-window → **no double-fire** (CAS + idempotent enqueue).
5. Ctrl-C (SIGTERM) with a slow job in flight → handler finishes, `Run`
   returns cleanly.
Record exact commands, ports, and observed log lines.

**Live-store gates:** turso leg `-tags=integration` + `TURSO_*` — the ONLY
authorized database is
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
(verify the env URL matches before any run); postgres leg env-gated on
`POSTGRES_TEST_DSN` (docker, 55432→5432). Loud skips mid-milestone are
fine; milestone close requires one recorded live conformance run per
dialect as dated NOTES.md artifacts — never a hermetic green.

## Acceptance for the milestone as a whole

- All 8 phases' execution logs green; `make check` covers all modules in
  the Makefile list (this milestone adds four: `sdk/workers` is inside
  sdk, `features/jobs`, its two dialect stores, plus
  `integrations/scheduling/robfig-cron` and `examples/jobs-minimal`).
- `features/jobs/go.mod` requires exactly `gopernicus/sdk` (charter item
  2; cron parsing and drivers live behind ports in their own modules).
- Recorded live conformance runs per dialect (NOTES.md artifacts) incl.
  the storetest concurrency assertions against real databases (§6.5's
  honesty note: they are only load-bearing there).
- The §8 proof-host protocol passes end to end, incl. the no-double-fire
  restart and the graceful drain.
- Rule 6: `grep -rn "features/\(auth\|cms\)" features/jobs/` empty (and
  the reverse).

## Execution log

(planning-leg and cross-phase entries here; per-phase logs in each file)

### 2026-07-02 — planning leg (loop leg 14): phase files cut

Cut `00-overview.md` + phases 1–5, 7, 8, 9 from the ratified design's
§10 (design phase 6 STRUCK per R2 — pgx connector consumed from
portability P1; memstore per R3; trio paths per the re-layout; §8's
proof-host protocol carried verbatim as the milestone gate). Cut-time
refinement, logged: design §10's phase 8 split into 8 (proof host, opus)
+ 9 (docs sync, fable), mirroring the auth-v1 precedent — a refinement of
the explicitly-rough breakdown, not a design change. No code touched.
Real-interaction: `make check` → "all checks passed" (13 modules, 4
guards); minimal :8081 → 200/200; port free. Next leg: phase 1
(`01-sdk-workers.md`, opus).
