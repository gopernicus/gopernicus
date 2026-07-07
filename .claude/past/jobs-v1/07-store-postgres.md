# Phase 7 — `features/jobs/stores/postgres`

Status: RATIFIED (cut from design §6.1–§6.3; pgx connector consumed from
portability P1 per ratified R2 — design phase 6 is STRUCK)
Executor model: opus
Depends on: phases 2 + 4 (P1's connector already exists).
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §6.1 (the
postgres-flavored schema shown there is near-verbatim for this module),
§6.2 (the proven claim shape WITH `FOR UPDATE SKIP LOCKED` — contention-
free concurrent claiming), §6.3 (lease config, default 15m). Template:
`features/auth/stores/postgres` (module shape, env-gated no-build-tag
conformance harness with Registrar-applied migrations + TRUNCATE cleanup,
README docker one-liner). Migration filenames IDENTICAL to phase 5's turso
tree (portability §6 invariant — verify with diff).

## Work items

1. Module `gopernicus/features/jobs/stores/postgres` (sdk + features/jobs
   + `integrations/datastores/postgres`); go.work + Makefile MODULES +
   STORE_MODULES + test-stores leg.
2. Migrations mirroring the turso filenames exactly; postgres idiom
   (TIMESTAMPTZ, JSONB, BOOLEAN, partial indexes per §6.1).
3. Both stores; Claim uses the §6.2 `FOR UPDATE SKIP LOCKED` statement
   with the lease-reclaim arm; ClaimDue value-CAS (byte-identical
   semantics to turso's); MapError sentinels.
4. Env-gated conformance test on `POSTGRES_TEST_DSN` (loud skip verbatim;
   the suite's CONCURRENT-CLAIM case is load-bearing here — N workers, N
   distinct jobs). Unit tests per the sibling precedent. README.

## The live gate (mandatory — never closes hermetically)

Docker available (55432→5432 precedent):
`docker run --rm -d --name jobs7 -p 55432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`,
wait for pg_isready, run the suite `-count=1` until GREEN, remove the
container. If docker fails, stop and report.

## Acceptance

```sh
cd features/jobs/stores/postgres && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
diff <(ls ../turso/migrations) <(ls migrations)     # empty
make check
```
Plus the live run (dated NOTES.md artifact at milestone close).

## Real-interaction check

Standing check (a), plus the live run above.

## Execution log

### 2026-07-02 — phase 7 executed (loop leg 20; implementer on opus) — LIVE-VERIFIED

Shipped `features/jobs/stores/postgres` (17th module): migrations
filename-identical to the turso tree (diff empty, verified twice);
postgres idiom (TIMESTAMPTZ, native BOOLEAN, partial indexes, status
CHECK); Queue with the §6.2 `FOR UPDATE SKIP LOCKED` Claim + lease-reclaim
arm (no busy-retry needed — the SKIP LOCKED advantage over turso's
discipline, exactly as designed), single-UPDATE CASE Fail, keyset List
with cursors from STORED µs-truncated values; Schedules with value-CAS
ClaimDue. go.work + MODULES + STORE_MODULES + test-stores updated.

Divergence (logged, sound): payload column is `JSON`, not §6.1's shown
`JSONB` — JSONB re-canonicalizes bytes and the suite asserts byte-exact
round-trips; the store treats payloads as opaque, so JSON is strictly
correct. Documented in the migrations + README.

Acceptance (firsthand): hermetic build/vet/test PASS (loud skip);
migration diff EMPTY; **LIVE conformance GREEN TWICE** (implementer +
independent -count=1 re-run, dockerized postgres:17 on :55432): all 16
cases, ConcurrentClaim clean in 0.33s (SKIP LOCKED), LeaseExpiryReclaim
3.14s (the designed sleep), total ~4.2s; containers removed.
`make check` (repo root) → "all checks passed" (17 modules, 4 guards);
minimal :8081 → 200/200; port free.

Unverified: nothing. Both jobs dialects now pass one suite — DP1 parity
holds for the third feature built under it.
