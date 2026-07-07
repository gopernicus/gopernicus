# integrations/datastores/pgxdb

The datastore connector for PostgreSQL. It wraps exactly one third-party
library — `github.com/jackc/pgx/v5` (pool via `pgxpool`, same module) — and
gives feature store modules a connector symmetric with
`integrations/datastores/turso`: `Config` / `Open` / `DB` / `MapError` /
`StatusCheck` / `RunMigrations`.

It owns "how to talk to Postgres," never any feature's SQL. No query builders,
no dialect helpers, no ORM. App/feature repositories consume this package's
`*DB`.

## Surface

| member | shape |
|---|---|
| `Config` | `DSN` or split `Host`/`Port`/`User`/`Password`/`Database`/`SSLMode`, plus pool settings, `LogQueries`, `Logger`, and `Tracer`; env tags are provided for host parsers, but `Open` never reads environment itself |
| `Open(cfg) (*DB, error)` | opens a `pgxpool` and pings |
| `DB` | `Exec` / `Query` / `QueryRow` / `InTx` / `Begin` / `Close` / `Ping` / `Underlying() *pgxpool.Pool` |
| `Querier` | interface intersection of `*DB` and `*Tx` (`Exec`/`Query`/`QueryRow`) — lets a store accept pool-or-tx |
| `MapError(err) error` | SQLSTATE-based: `23505`→`ErrAlreadyExists`, `23503`→`ErrInvalidReference`, `23514`/`23502`→`ErrInvalidInput`, `pgx.ErrNoRows`→`ErrNotFound`; unknown errors pass through |
| `RedactDSN(dsn) string` | masks a URL-form DSN's userinfo password for safe logging; unparseable input returns the literal `"REDACTED"` |
| `StatusCheck(ctx, db)` | 1s-deadline ping |
| `RunMigrations(ctx, db, fs, dir)` | host-driven migration runner for one database directory; one transaction, filename order, checksum guard, forward-only |
| `LoggingQueryTracer` / `NewLoggingQueryTracer` | `pgx.QueryTracer` over `*slog.Logger`; **logs SQL args verbatim — dev-only** |
| `MultiQueryTracer` / `NewMultiQueryTracer` | fans a query trace out to several `pgx.QueryTracer`s (pgx accepts only one) |
| `PrettyPrintSQL(sql) string` | whitespace-normalizes SQL for log lines |

## Symmetry is convention, not a guarantee

This connector mirrors the turso connector member-for-member **by convention**.
No `make guard` row proves the two surfaces or their sentinel coverage stay
aligned; a feature's `storetest` conformance suite is the only parity net, and
it sees only port-reachable behavior. Do not over-trust the symmetry.

`Config.LogQueries`, `Config.Logger`, and `Config.Tracer` (and the tracers
above) are deliberate exceptions to that symmetry: pgx exposes a native
`ConnConfig.Tracer` observability seam that SQLite's driver has no equivalent
for, so turso carries no matching fields. This is interim plumbing, expected
to fold into a shared `sdk/tracing` package later — until then, hosts opt in
by setting `Config.LogQueries` or `Config.Tracer`.

## Testing

Unit tests are hermetic (`MapError` over constructed `pgconn.PgError` values,
config validation, migration checksum/error paths) and run with a plain
`go test ./...` — no database required.

One live test (`Open`/ping + a migrate-apply round-trip) is gated on
`POSTGRES_TEST_DSN`. Unset, it skips loudly
(`POSTGRES_TEST_DSN not set — postgres conformance NOT verified`) — a silent
green that tested nothing is the false-green failure mode we guard against.

Spin a local database and run the live leg:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test ./...
```

`make check` stays hermetic (the live test skips); the live path is the store
modules' conformance gate, recorded as a dated NOTES.md artifact at milestone
close.
