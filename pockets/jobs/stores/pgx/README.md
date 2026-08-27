# pockets/jobs/stores/pgx

The jobs pocket's **PostgreSQL** store adapter — the dialect sibling of
`pockets/jobs/stores/turso`. Its own module so a host that brings a different
datastore never pulls `pgx` into its module graph. It owns the SQL and the
canonical migration files; the host owns its database lifecycle.

It ports the two jobs ports (`job.QueueRepository`, `schedule.Repository`) to
postgres idiom — `TIMESTAMPTZ`, native `BOOLEAN`, `BIGINT`, `$n` placeholders,
SQLSTATE-based error mapping — with **the same structure** as the turso tree.
Representation changes; structure does not.

Two SQL decisions worth flagging:

- **`Claim` uses `FOR UPDATE SKIP LOCKED`** (design §6.2): the selected row is
  locked so N concurrent claimers each take a *different* job with no contention
  — no busy-retry loop is needed (unlike the turso store's `SQLITE_BUSY`
  discipline). The lease-expiry reclaim arm (`status='running' AND claimed_at <
  now-lease`) is folded into the same statement.
- **`payload` is `JSON`, not `JSONB`.** The payload is opaque to this store (no
  jsonb operators or indexes), and `JSON` preserves the caller's exact bytes
  while `JSONB` re-canonicalizes whitespace/key order. The conformance suite
  asserts a byte-exact payload round-trip, which only `JSON` satisfies.

## Surface

Mirrors the turso store's exported surface (a host switches dialect by one import
+ one `Open` call):

| member | shape |
|---|---|
| `Repositories(db *postgres.DB, opts ...QueueOption) jobs.Repositories` | the two stores, no migration side effects |
| `NewQueueStore(db, opts...) *Queue` / `WithLease(d)` | the queue store + its lease option (default 15m) |
| `NewScheduleStore(db, opts...) *Schedules` | the schedule store (`WithLease` accepted and ignored) |
| `NewFencedQueueStore(db, opts...) *FencedQueue` | the fenced store (`WithLease` accepted and ignored — the lease is per-claim) |
| `Option` | the option type every constructor takes |
| `QueueOption` | alias of `Option`, kept so `WithLease` call sites that named it keep compiling |
| `WithSchema(s pgxdb.Schema) Option` | places every table in `s`; **Postgres-only** — the turso sibling has no equivalent because SQLite has no schemas |
| `StatusCheck(ctx, db, opts...) error` | boot gate: every jobs table exists under the configured schema |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

`QueueOption` is now an alias, so values returned by `WithLease(d)` are
unchanged — but a caller that constructs, converts, invokes, returns, or wraps
its **own** `QueueOption` no longer compiles: the underlying function type moved
from `func(*Queue)` to `func(*config)`.

## Schema

By default every statement names its table bare, exactly as it always has. A
host that wants the jobs tables in a dedicated schema builds it once and passes
it to both the migration runner and the stores:

```go
s, err := pgxdb.NewSchema(os.Getenv("JOBS_SCHEMA")) // validated here, at the host
if err != nil {
    return err
}
if err := pgxdb.RunMigrations(ctx, db, jobspgx.MigrationsFS, jobspgx.MigrationsDir, pgxdb.WithSchema(s)); err != nil {
    return err
}
if err := jobspgx.StatusCheck(ctx, db, jobspgx.WithSchema(s)); err != nil {
    return err
}
repos := jobspgx.Repositories(db, jobspgx.WithSchema(s))
```

`WithSchema` never panics — a malformed name fails at `pgxdb.NewSchema`, before
any store exists. Call `StatusCheck` at boot whenever a schema is configured:
`Repositories` is deliberately probe-less and error-less, so a store built for a
schema the migrations never reached would otherwise read and write the host's own
unqualified `job_queue`/`job_schedules`/`fenced_job_queue` instead of failing.

Two limits worth knowing:

- **Per-repository different schemas are out of scope.** The constructors
  mechanically allow it, but the one-stream-per-schema migration model gives it
  no story. Use one schema per store set.
- **The fenced store's advisory lock is database-scoped, not schema-scoped.**
  `pg_advisory_xact_lock(hashtext(key))` keys on the logical key text alone, so
  two hosts sharing one database and the same key text contend across schemas.
  This is not a regression (it is equally true under a `search_path` pin), and no
  code change fixes it — pick distinct logical-key prefixes per host.

## Migrations

`migrations/*.sql` carry the **identical filename set** as the turso tree —
`0001_job_queue.sql`, `0002_job_schedules.sql`. Same filename = same logical
schema step; content is per-dialect. After export, the host owns the final
migration stream in `workshop/migrations/{db}`.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs, and the live
conformance suite (`storetest.RunQueue` / `storetest.RunSchedules`) **skips
loudly** without a DSN (`POSTGRES_TEST_DSN not set — postgres conformance NOT
verified`). A silent green that tested nothing is the false-green failure mode
this gating exists to prevent.

The live conformance run is this store's dialect-parity gate. Spin a local
database and run it:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test -count=1 ./...
```

Set `POSTGRES_TEST_SCHEMA` and the same suite reruns entirely inside that schema
— dropped, migrated with `pgxdb.WithSchema`, constructed with `WithSchema`, and
truncated by qualified name. That leg is the behavioral proof that every executed
statement is qualified; the hermetic `TestNoBareTableReferences` guards the
statement forms it does not execute.

Each `newRepo` opens a connection, applies the migrations via the connector's
`RunMigrations`, and `TRUNCATE ... CASCADE`s the jobs tables (up front and via
`t.Cleanup`) so every leaf subtest starts from a clean, isolated store. The
lease-expiry and concurrent-claim cases sleep ~3.1s each by design (they exercise
the real stale-claim window with a wall-clock sleep past `storetest.Lease`).

`make check` stays hermetic (the suite skips); `make test-stores` runs this live
path expecting `POSTGRES_TEST_DSN`.
