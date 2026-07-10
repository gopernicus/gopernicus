# integrations/datastores/pgxdb

The datastore connector for PostgreSQL. It wraps exactly one third-party
library — `github.com/jackc/pgx/v5` (pool via `pgxpool`, same module) — and
gives feature store modules a connector symmetric with
`integrations/datastores/turso`: `Config` / `Open` / `DB` / `MapError` /
`StatusCheck` / `RunMigrations`.

It owns "how to talk to Postgres," never any feature's SQL. No ORM and no
general query builder — the one shared query surface is the list toolkit
below, which owns pagination mechanics (ordering, keyset cursors, offset,
counts) while every store keeps writing its own SQL. App/feature repositories
consume this package's `*DB`.

## Surface

| member | shape |
|---|---|
| `Config` | `DSN` or split `Host`/`Port`/`User`/`Password`/`Database`/`SSLMode`, plus pool settings, `LogQueries`, `Logger`, `Tracer`, and `Retry`; env tags are provided for host parsers, but `Open` never reads environment itself |
| `Open(cfg) (*DB, error)` | opens a `pgxpool` and pings |
| `DB` | `Exec` / `Query` / `QueryRow` / `InTx` / `Begin` / `Close` / `Ping` / `Underlying() *pgxpool.Pool` |
| `Querier` | interface intersection of `*DB` and `*Tx` (`Exec`/`Query`/`QueryRow`) — lets a store accept pool-or-tx |
| `MapError(err) error` | SQLSTATE-based: `23505`→`ErrAlreadyExists`, `23503`→`ErrInvalidReference`, `23514`/`23502`→`ErrInvalidInput`, `pgx.ErrNoRows`→`ErrNotFound`; unknown errors pass through |
| `RedactDSN(dsn) string` | masks a URL-form DSN's userinfo password for safe logging; unparseable input returns the literal `"REDACTED"` |
| `StatusCheck(ctx, db)` | 1s-deadline ping |
| `RunMigrations(ctx, db, fs, dir)` | host-driven migration runner for one database directory; one transaction, filename order, checksum guard, forward-only |
| `List[T]` / `ListQuery[T]` | the shared paginated-SELECT helper implementing the `sdk/foundation/crud` list standards (see below) |
| `QuoteIdentifier(ident) (string, error)` | regex allow-list + per-segment double-quoting for dynamic identifiers (order columns); rejection wraps `ErrInvalidInput` |
| `ApplyCursorPagination` / `AddOrderByClause` / `AddLimitClause` | NamedArgs SQL builders under `List`: tuple-comparison keyset predicate (direction × forPrevious operator table), ORDER BY with PK tiebreaker + optional `LOWER()`, `LIMIT @limit` |
| `LoggingQueryTracer` / `NewLoggingQueryTracer` | `pgx.QueryTracer` over `*slog.Logger`; **logs SQL args verbatim — dev-only** |
| `MultiQueryTracer` / `NewMultiQueryTracer` | fans a query trace out to several `pgx.QueryTracer`s (pgx accepts only one) |
| `PrettyPrintSQL(sql) string` | whitespace-normalizes SQL for log lines |

## The list toolkit — `List[T]` over `ListQuery[T]`

`List[T]` runs a paginated SELECT to the `sdk/foundation/crud` standards; the crud
package doc's mode/count matrix is normative, and this helper is its pgx
implementation. A store describes its list with a `ListQuery[T]`:
`BaseSQL` (a `SELECT … FROM … [WHERE …]` with **no** ORDER BY/LIMIT/OFFSET),
`Args` (`pgx.NamedArgs` for the base WHERE), the aggregate's `OrderFields`
allow-list + `DefaultOrder`, the `PK` tiebreaker column, an optional
`Limits` (`crud.Limits` — the resource's page-size default/max, passed to
`req.NormalizedLimit`; the zero value keeps `crud`'s `DefaultLimit`/`MaxLimit`),
and `OrderValueOf`/`PKOf` accessors for cursor encoding. The helper validates the
request, resolves the order **by column** against the allow-list (every
identifier passes `QuoteIdentifier`), then switches on the request's
**resolved strategy** into one of two linear flows — `listCursor` (keyset
predicate + reverse-probe prev pages) or `listOffset` (`LIMIT/OFFSET`, HasMore
from its own over-fetch, no cursors emitted). The strategy is explicit
(`crud.StrategyCursor` / `crud.StrategyOffset`), never inferred from the offset
value, so `Offset 0` under the offset strategy is a real first offset page. Both
flows scan via `pgx.CollectRows` + `RowToStructByName[T]`, and on `WithCount`
wrap `BaseSQL` in a `COUNT(*)` subquery so the filter WHERE is reused by
construction.

Store conventions that ride the toolkit (set by the authentication store,
`features/authentication/stores/pgx`, the pattern-setter):

- **Row structs, not domain tags.** `T` is a store-local db-tagged row struct
  with a `toDomain` converter; pages bridge through `crud.MapPage`. Domain
  entities never carry persistence tags.
- **NamedArgs filter builders.** Per-store WHERE fragments are plain funcs
  appending to `pgx.NamedArgs` — shared by the list call and (via the count
  wrap) the total, so the two can never disagree.
- **UNNEST for multi-row writes.** Bulk inserts are single
  `INSERT … SELECT … FROM UNNEST(@col::type[], …)` statements (the cms
  `entry_fields`/`entry_terms` and events outbox writes), never Exec loops.

## The `Querier` surface stays Exec/Query/QueryRow — no `SendBatch`

`Querier` is deliberately the three-method intersection of `*DB` and `*Tx`
(`Exec` / `Query` / `QueryRow`) and nothing more. The list toolkit
(`List` / `ListQuery` / `ApplyCursorPagination` / `AddOrderByClause` /
`AddLimitClause`) needs only those three: the cursor flow issues one main `Query`
and an optional reverse-probe `Query`, the offset flow one `Query`, and a count
is one `QueryRow` over a `COUNT(*)` wrap of the base SQL. Adding `SendBatch` (or
`Begin`) to `Querier` would widen the port every store must accept and pull
`pgx.Batch` into the shared surface for a batching optimization no current
caller needs — so it stays out. A store that genuinely needs pipelining can
reach for the concrete `*DB`/`*Tx` directly; the shared list path does not.

## Boot-connectivity retry is opt-in — and statements are never auto-retried

`Config.Retry` (`RetryPolicy{Attempts, MinBackoff, MaxBackoff}`) governs one
thing: the connectivity check `Open` runs at boot. The zero value is no retries
— `Open` pings exactly once, today's behavior. Setting `Attempts > 1` makes
`Open` verify boot connectivity with a real round-trip (`StatusCheck` — `Ping` +
`SELECT 1`) retried under a full-jitter exponential backoff (each sleep uniform
in `[MinBackoff, cap]`, the cap doubling from `MinBackoff` up to `MaxBackoff`),
aborting on context cancellation. This targets the orchestration race — the pool
cannot yet acquire a connection at startup. This is symmetric with the turso
connector's `Config.Retry`.

**Statement-level retry is store-owned, explicit, and per-call — the connector
never auto-retries statements.** A method verb does not encode idempotency
(`Query`/`QueryRow` carry `RETURNING` writes), so no automatic retry is applied
to any `Exec`/`Query`/`QueryRow`. `Config.Retry` is boot connectivity only.
(database/sql-style bad-conn retry inside the pool is pgx's own, bounded and
independent of this policy.)

## Symmetry is convention, not a guarantee

This connector mirrors the turso connector member-for-member **by convention**.
No `make guard` row proves the two surfaces or their sentinel coverage stay
aligned; a feature's `storetest` conformance suite is the only parity net, and
it sees only port-reachable behavior. Do not over-trust the symmetry.

Query logging is symmetric across both connectors: each carries an opt-in
`Config.LogQueries` / `Config.Logger` with the same dev-only, args-verbatim
posture — pgx installs it as a native `ConnConfig.Tracer`, turso threads it
through its `DB`/`Tx` wrapper because database/sql exposes no tracer hook.
`Config.Tracer` (and `MultiQueryTracer` above) is the one exception that remains
pgx-only: it composes an external `pgx.QueryTracer` (e.g. OpenTelemetry) into
that native seam, which SQLite's driver does not expose. This is interim
plumbing, expected to fold into a shared `sdk/capabilities/tracing` package later — until
then, hosts opt in by setting `Config.LogQueries` or `Config.Tracer`.

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
