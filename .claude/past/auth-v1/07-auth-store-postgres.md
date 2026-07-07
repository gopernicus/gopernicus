# Phase 7 — `features/auth/stores/postgres`

Status: DRAFT — added 2026-07-02 by ratified amendment R1
(`.claude/plans/roadmap/datastore-portability.md` §8; `00-intersections.md` §6)
Executor model: opus
Depends on: phase 1 AND datastore-portability P1 (`integrations/datastores/
postgres` — built THERE, never here; if P1 hasn't landed, this phase queues,
it does not build the connector). Independent of phases 2–5; phase 6 (docs)
executes after this one.

## Goal

The auth feature's second dialect store, satisfying the ratified DP1 charter
rule (every feature ships {turso, postgres} + the storetest reference, parity
gating milestone close): SQL implementations of the five v1 ports over the
postgres connector + canonical migrations under source name `"auth"` with a
version (filename) set IDENTICAL to `stores/turso`'s, passing the same
`storetest` suite the turso store passes.

## Work items

1. Module scaffold (`gopernicus/features/auth/stores/postgres`; requires
   `gopernicus/{sdk, features/auth, integrations/datastores/postgres}`;
   go.work + MODULES).
2. Migrations (`migrations/*.sql`): the SAME filenames/version numbers as
   `stores/turso`'s tree (portability §6's version-set invariant — same
   filename = same logical schema step), content in postgres idiom:
   `TIMESTAMPTZ` timestamps (microsecond precision — the keyset tie-break on
   `(created_at, id)` is load-bearing; portability §5's precision trap),
   native `BOOLEAN`, same UNIQUE/FK structure as turso (representation may
   change; structure may not).
3. Store implementations per port, using the postgres connector's
   `DB`/`MapError` exactly as the turso store uses turso's; identical port
   semantics — same sentinel for the same violation (`23505` →
   `errs.ErrAlreadyExists` via `MapError`, absent → `errs.ErrNotFound`,
   expired-at-read per the port doc comments). Cursors must encode from
   STORED values, not in-memory ones (the precision trap again).
4. `Repositories(db) auth.Repositories` + `ExportMigrations(dst)` +
   `Register` — the same exported trio as `stores/turso`, so a host switches
   dialect by switching one import and one `Open` call.
5. Tests: unit tests for mapping where the turso store has them; the live leg
   runs `storetest.Run` env-gated on `POSTGRES_TEST_DSN` (loud
   `t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")`
   when absent; each `newRepos` truncates the auth tables via `t.Cleanup`).
   README documents the docker one-liner
   (`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=… postgres:17`).

## Acceptance

The store passes `storetest.Run` against a live local Postgres, and the run
is recorded as a dated NOTES.md artifact (dialect, DSN class, result —
portability §4.3's anti-false-green rule; a hermetic skip does NOT satisfy
this phase).

```sh
cd features/auth/stores/postgres && go build ./... && go vet ./... && go test ./...
diff <(ls features/auth/stores/turso/migrations) \
     <(ls features/auth/stores/postgres/migrations)   # empty — identical version set
make check         # green, module included; hermetic (skips are loud)
POSTGRES_TEST_DSN=... go test ./...                    # the real gate
```

## Real-interaction check

Standing check (a), plus the live-Postgres `storetest` run above as this
phase's own gate. (A postgres-backed example host is a non-goal —
portability §7.)

## Execution log

### 2026-07-02 — phase 7 executed (loop leg 11; implementer on opus) — LIVE-VERIFIED

Shipped `features/auth/stores/postgres` (13th module): migrations 0001–0005
with filenames IDENTICAL to the turso tree (diff empty, verified twice);
postgres idiom (TIMESTAMPTZ, native BOOLEAN for email_verified) with
turso-parity structure (plain session tokens, no enforced FKs — matching
phase 5's logged decisions so the suite passes identically); five stores
over the P1 connector (23505→ErrAlreadyExists via MapError,
ErrNotFound/ErrExpired per port docs, passwords ON CONFLICT upsert);
exported trio + MigrationsFS/Dir. go.work + MODULES + STORE_MODULES +
test-stores updated.

Divergence (logged, per phase instruction): env-gated with no build tag
(cms postgres precedent), unlike the turso store's -tags=integration leg —
test-stores reflects both patterns.

Acceptance (firsthand): hermetic build/vet/test PASS with the loud skip;
migration diff EMPTY; `make check` → "all checks passed" (13 modules, 4
guards). **LIVE conformance GREEN TWICE** (dockerized postgres:17,
:55432): implementer run 17/17 leaf subtests verbose; independent loop-leg
re-run `-count=1` ok in 0.55s; containers removed. Real-interaction (a):
minimal :8081 → 200/200; port free.

Unverified: nothing. Both auth dialect stores now pass one suite —
charter checklist items 10–11 hold for features/auth; DP1 parity gates
are satisfied ahead of the milestone-close docs phase (6).
