# Phase 5 — `features/jobs/stores/turso`

Status: RATIFIED (cut from design §6.1–§6.3)
Executor model: opus
Depends on: phases 2 + 4.
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §6.1 (schema —
translate the postgres-flavored SQL shown there to the turso conventions),
§6.2 (claim semantics: same statement MINUS `FOR UPDATE SKIP LOCKED`,
status predicate repeated in the outer WHERE; SQLite single-writer makes
double-claim impossible; the REAL hazard is SQLITE_BUSY — busy_timeout /
retry-on-busy discipline REQUIRED, asserted by the suite's concurrency
case; RETURNING needs SQLite ≥3.35, libsql satisfies), §6.3 (lease as
store config: `WithLease(15*time.Minute)` default, folded into Claim's
"due" predicate — NEVER a Claim parameter). Template:
`features/auth/stores/turso` (module shape, trio-era conventions,
fixed-width TEXT timestamps, `-tags=integration` gating).

## Work items

1. Module `gopernicus/features/jobs/stores/turso` (sdk + features/jobs +
   turso connector); go.work + Makefile MODULES + STORE_MODULES +
   test-stores leg.
2. Migrations from 0001, source Name `"jobs"`: `job_queue` +
   `job_schedules` per §6.1 (TEXT ISO-8601 fixed-width timestamps, TEXT
   JSON payloads; the partial indexes; J8's vocabulary — kind/claimed_at/
   lowercase statuses; NO tenant/aggregate/correlation columns).
3. Both stores implementing the ports over the connector's DB/MapError;
   Claim's single-statement reclaim arm; ClaimDue value-CAS; busy_timeout
   discipline.
4. Tests: hermetic unit tests where the auth/cms turso stores have them;
   live leg = `storetest.RunQueue` + `RunSchedules` under
   `-tags=integration` + `TURSO_*` loud-skip gating, cleaning jobs tables
   per newRepo call.

## The live gate

The ONLY authorized turso database is
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` —
verify the env URL matches before running; then
`go test -tags=integration -count=1 ./...` until GREEN (expect ~30–90s,
remote round-trips; the concurrency case is the load-bearing one here —
SQLITE_BUSY/remote serialization must surface as waiting, not errors).

## Acceptance

```sh
cd features/jobs/stores/turso && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
make check
```
Plus the live run (recorded as a dated NOTES.md artifact at milestone
close).

## Real-interaction check

Standing check (a), plus the live run above.

## Execution log

### 2026-07-02 — phase 5 executed (loop leg 19; implementer on opus) — LIVE-VERIFIED after a suite fix

Shipped `features/jobs/stores/turso` (16th module): migrations 0001–0002
(source "jobs", §6.1 in turso idiom — fixed-width TEXT timestamps, status
CHECK, partial pending/due indexes, J8 vocabulary); Queue with the
single-statement Claim (subquery + repeated predicate + RETURNING, lease
folded in as the stale arm, priority DESC/created_at/job_id ordering),
single-UPDATE CASE-based Fail, keyset List; Schedules with value-CAS
ClaimDue (fixed-width TEXT equality); busy discipline = best-effort
PRAGMA busy_timeout + bounded ctx-aware retryBusy around every write
(200 tries, 2→200ms backoff). Exported trio + MigrationsFS/Dir. go.work +
MODULES + STORE_MODULES + test-stores updated.

**Genuine suite-vs-dialect conflict found, escalated correctly, resolved
by the loop leg:** storetest.Lease was 250ms but the measured remote
Claim≈222ms / Claim→Complete≈338ms, so the reclaim arm legitimately
double-claimed in-flight jobs (ConcurrentClaim 29/60 doubles; zero
spurious errors; store proven correct with a 30s lease → 60/60 distinct).
The implementer stopped per protocol (storetest is cross-store, off
limits to a store phase). Loop-leg ruling: **storetest.Lease raised to
3s** (~9x margin over the slowest dialect's cycle; doc comment now
records the evidence and the trade-off — reclaim cases sleep ~3.1s).

Acceptance (firsthand): hermetic build/vet/test PASS (loud skip);
memstore reference re-run with the new lease ok 3.3s; **LIVE turso leg
FULLY GREEN** `-count=1` ok in 60.1s against the authorized playground
(URL verified) — all 9 RunQueue + 7 RunSchedules cases incl.
ConcurrentClaim (0 doubles, 0 spurious) and LeaseExpiryReclaim;
`make check` (repo root) → "all checks passed" (16 modules, 4 guards);
minimal :8081 → 200/200; port free.

Unverified: nothing for this phase.
