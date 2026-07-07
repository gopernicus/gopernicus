# features/jobs/stores/pgx

The jobs feature's **PostgreSQL** store adapter — the dialect sibling of
`features/jobs/stores/turso`. Its own module so a host that brings a different
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
| `NewScheduleStore(db) *Schedules` | the schedule store |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

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

Each `newRepo` opens a connection, applies the migrations via the connector's
`RunMigrations`, and `TRUNCATE ... CASCADE`s the jobs tables (up front and via
`t.Cleanup`) so every leaf subtest starts from a clean, isolated store. The
lease-expiry and concurrent-claim cases sleep ~3.1s each by design (they exercise
the real stale-claim window with a wall-clock sleep past `storetest.Lease`).

`make check` stays hermetic (the suite skips); `make test-stores` runs this live
path expecting `POSTGRES_TEST_DSN`.
