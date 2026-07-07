# P1 — `integrations/datastores/postgres`

Status: RATIFIED (cut from design §3 / DP3)
Executor model: opus
Depends on: nothing (first phase; P2 may run before or parallel).
Design doc: `.claude/plans/roadmap/datastore-portability.md` §3 (the
member-for-member spec), §4.3 (gating). Read the turso connector
(`integrations/datastores/turso`) thoroughly first — the surface mirrors it
by convention (DP3: convention, not an sdk interface; the design's stated
non-guarantee applies — no guard proves symmetry, so match it by hand).

## Goal

One new module wrapping exactly one third-party library
(`github.com/jackc/pgx/v5`, pool via `pgxpool` — same module), giving
feature store modules a postgres connector symmetric with turso's:
Config/Open/DB/MapError/StatusCheck/Registrar.

## Work items

1. Module scaffold: `integrations/datastores/postgres` (module
   `gopernicus/integrations/datastores/postgres`), go.work entry, Makefile
   `MODULES` entry. Requires `gopernicus/sdk` + `github.com/jackc/pgx/v5`
   only (constitution rule 2).
2. `Config`: DSN, pool sizes (MaxConns, MinConns, MaxLifetime, MaxIdleTime),
   ConnectTimeout — salvage the original `pgxdb.Options` field set (read
   `gopernicus-original/infrastructure/database/postgres/pgxdb/`), DROP its
   env tags (hosts use `sdk/config`), DROP the functional-options layer and
   query tracers (D9: observability returns via `sdk/tracing`, not
   per-connector fields).
3. `Open(cfg) (*DB, error)`: opens pool + pings. `DB` with
   `Exec/Query/QueryRow/InTx/Close/Ping/Underlying` over `pgxpool.Pool`
   (`Underlying() *pgxpool.Pool`) — mirror turso's `DB` method set
   member-for-member.
4. `MapError`, code-based via `pgconn.PgError` (vs turso's substring
   matching): `23505`→`errs.ErrAlreadyExists`; `23503`→
   `errs.ErrInvalidReference` (both insert- and delete-direction,
   undifferentiated, matching turso); `23514`/`23502`→`errs.ErrInvalidInput`;
   `pgx.ErrNoRows`→`errs.ErrNotFound`.
5. `StatusCheck(ctx, db)`: 1s-deadline ping (turso parity).
6. `Registrar`/`NewRegistrar`/`Register`/`Apply` implementing
   `feature.MigrationRegistrar`: same ledger table shape
   `(source, version, checksum, raw_sql, applied_at)` and apply semantics as
   turso's (one tx, lexical source order, checksum guard, forward-only).
   Table introspection via `to_regclass`/`information_schema` (never
   `sqlite_master`/PRAGMA). **OMIT** the legacy-adoption path (`_legacy`
   re-sourcing) and the `RunMigrations` single-source wrapper — no legacy
   postgres databases exist (design §3's table says omit; do not port them
   for symmetry's sake).
7. Tests — hermetic: `MapError` unit-tested via constructed
   `pgconn.PgError` values for all four codes + `pgx.ErrNoRows`; config
   validation; registrar duplicate-source error. Live (env-gated per §4.3):
   ONE test covering Open/ping + a migrate-apply round-trip, skipping
   loudly without `POSTGRES_TEST_DSN`.
8. Module README: what it is, the docker one-liner
   (`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=… postgres:17`),
   the env-gating rule, and the stated non-guarantee (connector symmetry is
   convention — design §3).

Not in scope: query builders, dialect helpers, ORM-ish anything, any
feature's SQL.

## Acceptance (design §10 P1 row, verbatim intent)

```sh
cd integrations/datastores/postgres && go build ./... && go vet ./... && go test ./...
make check          # green with the module included; hermetic (live test skips loudly)
```

- `MapError` unit-tested for all four codes + ErrNoRows.
- Live ping/migrate test runs when `POSTGRES_TEST_DSN` is set; skips with
  the loud message when not. State plainly in the log which happened.

## Real-interaction check

Standing check (a) from `00-overview.md` (make check; boot examples/minimal
:8081; `GET /` and `GET /products/widget-3000` → 200s; kill; port free).
If `POSTGRES_TEST_DSN` is available (check env + docker — do not ask), also
run the live leg and record it; if absent, state the live path is
unverified (it becomes mandatory at P3/milestone close, not here).

## Execution log

### 2026-07-02 — P1 executed (loop leg 2; implementer on opus)

Shipped `integrations/datastores/postgres`: `go.mod` (pgx/v5 v5.8.0 only,
workspace replace for sdk), `postgres.go` (Config/Open/MapError, SQLSTATE
code-based), `db.go` (DB over pgxpool: Exec/Query/QueryRow/Close/Ping/
Underlying + StatusCheck 1s ping), `tx.go` (Tx/Begin/Commit/Rollback/InTx),
`migrate.go` (Registrar per feature.MigrationRegistrar — ledger
`(source, version, checksum, raw_sql, applied_at)`, one tx, lexical source
order, checksum guard, forward-only, `to_regclass` introspection; legacy
paths omitted per spec), hermetic tests (MapError all four codes +
ErrNoRows + nil/passthrough; config validation; registrar dup-source/empty
name), env-gated live test with the loud skip message, README (docker
one-liner, gating, symmetry non-guarantee). go.work + Makefile MODULES
updated (7 modules).

Divergences (logged, all sound): `Config` omits the original's
`HealthCheck` field (design §3's enumeration is authoritative; pgxpool's
default applies); `Tx.Commit/Rollback` capture the Begin context to keep
turso's context-free signatures; `DB.Close() error` returns nil (pgxpool
Close is infallible) to preserve surface symmetry.

Acceptance: module build/vet/test PASS; `make check` → "all checks passed"
(7 modules, 4 guards). **Live test RAN and PASSED** (dockerized postgres:17
on :55432): Open/ping OK, migration `0001_init.sql` applied, re-apply was a
checksum-guarded no-op; container removed after.

Real-interaction check (re-run firsthand by the loop leg after the
implementer's own run): `make check` all green;
`GET http://localhost:8081/` → 200,
`GET http://localhost:8081/products/widget-3000` → 200; server killed
(pkill child + wrapper), port 8081 free.

Unverified: nothing outstanding for P1. The connector-symmetry
non-guarantee stands by design (§3); P2/P3's suite is the behavioral net.
