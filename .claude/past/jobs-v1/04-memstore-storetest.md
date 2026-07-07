# Phase 4 — in-core `memstore` + the `storetest` conformance suite

Status: RATIFIED (cut from design §6.4–§6.5; R3 placement)
Executor model: opus
Depends on: phase 2.
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §6.4 (memstore),
§6.5 (suite contract + the honesty note), §6.2–§6.3 (the claim/lease
semantics the suite asserts). Precedent: `features/auth/storetest` and
`features/cms/storetest` (runner shape, newRepos contract doc).

## Work items

1. `features/jobs/memstore` — a PUBLIC package inside the feature core
   (ratified R3: stdlib-only, G2-clean, importable by both the proof host
   and storetest; the named exception to DP2's test-scoped default because
   a lease-respecting concurrent queue is too substantial to duplicate).
   Implements both ports with a mutex, honestly: Enqueue ID-uniqueness →
   `errs.ErrAlreadyExists`; Claim honors ordering (priority DESC,
   created_at), `scheduled_for` gating, lease reclaim, `workers.ErrNoWork`;
   Fail → reschedule or dead-letter at max attempts; ClaimDue value-CAS.
2. `features/jobs/storetest` — exported `RunQueue(t, newRepo func(t)
   job.QueueRepository)` and `RunSchedules(t, newRepo func(t)
   schedule.Repository)` (port-set sub-runners per R4; clean-isolated
   repo per call, documented). Cases per §6.5: CRUD/List pagination where
   ports page (include the timestamp-precision case if Job/Schedule List
   pages by (created_at,id) — derive from the actual port contracts,
   phase 2 authority); enqueue idempotency; claim ordering; scheduled_for
   gating; retry → dead_letter at max_attempts; lease-expiry reclaim;
   ClaimDue CAS (stale prevNextRunAt never wins); **concurrent-claim
   safety** — G goroutines claiming N jobs get N distinct jobs, no
   spurious errors (SQLITE_BUSY must surface as adapter-internal
   retry/wait, never a failed claim). Honesty note in the package comment:
   the concurrency assertions are trivially green against the mutex
   memstore — load-bearing only against real dialects (phases 5/7).
3. Suite runs against memstore in the feature's own `go test ./...`.

## Acceptance

```sh
cd features/jobs && go build ./... && go vet ./... && go test ./...   # suite green vs memstore
grep -c require features/jobs/go.mod    # STILL exactly 1
make check                              # G2 green (no drivers anywhere in the core)
```

## Real-interaction check

Standing check (a).

## Execution log

### 2026-07-02 — phase 4 executed (loop leg 18; implementer on opus)

Shipped `features/jobs/memstore` (public in-core package, R3: mutex-backed
Queue with WithLease default 15m — Enqueue ID-uniqueness, Claim with
priority-DESC/created_at/id ordering + scheduled_for gating + lease
reclaim + ErrNoWork, Fail requeue-or-dead-letter, keyset-paginated List;
Schedules with Ensure upsert-by-Name, ClaimDue value-CAS) and
`features/jobs/storetest` (RunQueue 9 cases + RunSchedules 7 cases —
idempotency, ordering, gating, retry→dead_letter, LEASE-EXPIRY RECLAIM,
CAS stale-never-wins, 8-goroutine/60-job concurrent-claim distinctness;
§6.5 honesty note in the package comment). Lease contract for live
stores: exported `storetest.Lease = 250ms`, each newRepo constructs its
store with it — real sleep, no clock injection, so SQL stores in phases
5/7 run the identical case.

Deliberate non-assertions (logged): no precision-collision pagination
case — the shipped List contracts promise cursor pagination but no order
column (phase-2 doc comments are the spec); pagination cases assert
set-identity/no-dupes/limit. Complete/Fail-on-missing-id unasserted
(contract silent). created_at tie-break case uses a 3ms real gap to
survive µs-truncating dialects.

Acceptance (re-run FIRSTHAND): `go test -race -count=1 ./...` PASS (5
packages ok, storetest 1.8s); go.mod requires exactly 1; `make check`
(from the repo root — first attempt ran in the wrong cwd, re-run) → "all
checks passed"; minimal :8081 → 200/200; port free. Stale-editor
diagnostics (itoa/strconv) disproven by the firsthand build.

Unverified: concurrency cases against real dialects — exactly phases
5/7's job.
