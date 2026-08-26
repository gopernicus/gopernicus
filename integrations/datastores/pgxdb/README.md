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
| `ProbeTable(ctx, q, table) error` | boot-time existence probe for a store's table (`to_regclass`, name bound as a parameter, bare or schema-qualified): absent → wraps `ErrNotFound` naming the relation; a query failure maps through `MapError` and is never misreported as missing. Run it in a store constructor so a host aimed at the wrong database fails before serving |
| `RunMigrations(ctx, db, fs, dir)` | host-driven migration runner for one database directory; one transaction, filename order, checksum guard, forward-only. One merged stream per database: call it once, with globally unique filenames that are never renumbered — `dir` is an `fs.FS` subpath, never a ledger namespace (all rows share the `"default"` source) |
| `NewLimiter(db, opts…) *Limiter` / `WithLimiterKeyPrefix` | a durable `sdk/capabilities/ratelimiter.Limiter` over the caller-owned `*DB` — one atomic statement per `Allow` against the host-owned `ratelimit_windows` table (see below) |
| `(*Limiter).StatusCheck(ctx) error` | boot probe for that host-owned table; call it before serving, because a missing table makes the sdk middleware fail open and silent |
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

## Durable rate limiting — `Limiter`

`NewLimiter(db)` implements `sdk/capabilities/ratelimiter.Limiter` over the
connection the host already owns, so a multi-instance deployment gets a
cross-instance limiter without standing up Redis. One connector implementing a
second sdk port is the `kvstores/goredis` precedent: the integration unit is the
**library**, not the port.

Semantics match the goredis limiter deliberately, so the two are swappable:
`Limit.Burst` adds to `Limit.Requests` to form the effective ceiling; each key is
an independent budget inside a configurable namespace (`WithLimiterKeyPrefix`,
default `ratelimit:`); a new window carries the decaying tail of the previous one
(the sliding approximation); a denial reports `Remaining: 0` with a positive
`RetryAfter` and consumes no quota; `Reset` clears one key. `Close` is an
idempotent no-op — it never closes the caller's pool. One deliberate strictness:
a non-positive `Limit.Window` **or** a non-positive ceiling
(`Requests + Burst`) returns `sdk.ErrInvalidInput` instead of denying everything,
because a zero ceiling on a login path is a misconfiguration, not a policy.
Both `Allow` and `Reset` map database failures through `MapError`, so callers
match the same stable sdk kinds on either method.

**Every decision is one statement.** `Allow` is a single
`INSERT … ON CONFLICT (key) DO UPDATE … RETURNING` whose admission test is
computed in SQL: Postgres locks the conflicting row and re-evaluates the `SET`
expressions against its latest committed version, so under N instances racing a
ceiling of K, exactly K calls are admitted. A read-then-check-then-write limiter
over-admits here (measured: 40/40 admitted at a ceiling of 8), which is why the
transition is indivisible and why the proof is a live exact-K test rather than a
unit test.

**Server time only.** `clock_timestamp()` is evaluated once per statement, in the
proposed row, and read back on the conflict branch — window selection, `ResetAt`,
and `RetryAfter` are all server arithmetic. A skewed application clock cannot
change a decision or a returned duration. That instant is captured when the
statement *starts*, before it waits on the row lock, so a statement that loses a
race can hold a `now` older than the window the winner installed; every sliding
weight is clamped to `[0, 1]` and `RetryAfter` to the window, so a stale `now`
can never over-count the previous window's tail or return a `RetryAfter` longer
than `Limit.Window` (measured unclamped: `1m0.000024s` on a one-minute window).

### Failure posture is the host's call

`Allow` returns an error when Postgres is unreachable or slow. The limiter sets
**no internal deadline** — it inherits the caller's context, so a stalled
database stalls the rate-limited request for as long as that context allows.
Give the request context a deadline you are willing to serve.

What the host does with that error is the host's decision, and today's callers
genuinely differ: `sdk/capabilities/ratelimiter.Middleware` fails **open** (a
limiter error is swallowed and the request proceeds unthrottled), while the
authentication feature's login and passwordless call sites fail **closed** (the
error propagates and the attempt is rejected — its refresh path fails open).
Neither is wrong; they are different tradeoffs between "let traffic through
during a database outage" and "never admit unmetered credential attempts."

This matters most on the swap. A Memory limiter cannot fail: replacing it with
this one puts every rate-limited path on a network round-trip to Postgres, so a
database incident becomes an availability event (fail-closed paths) or a
brute-force window (fail-open paths). Decide which you want per path, and
monitor limiter error rate and latency as first-class signals, not as database
noise.

### Reference DDL — host-owned

**The connector creates and migrates nothing.** Copy this into your own migration
ledger (the same scaffold-and-own rule every feature store follows); the table
name is fixed, and keys are always bound parameters, never concatenated SQL.

**If this table is absent, the limiter fails OPEN and SILENT.** Every `Allow`
returns an error (`42P01`), and `sdk/capabilities/ratelimiter.Middleware`
swallows limiter errors and lets the request through — so a host that forgot the
migration, or pointed at the wrong database or `search_path`, serves completely
unthrottled traffic with a green health check and no log line. Nothing in the
request path will tell you. **Verify the table at boot, before serving:**

```go
limiter := pgxdb.NewLimiter(db)
if err := limiter.StatusCheck(ctx); err != nil {
    return fmt.Errorf("rate limiter is not usable: %w", err) // refuse to start
}
```

`StatusCheck` probes for the table (`SELECT 1 … LIMIT 0` — no rows, no heap
access) and reports a missing one as `sdk.ErrNotFound`; other failures map
through `MapError`. An undeadlined context gets one second, like the
package-level `StatusCheck`.

```sql
CREATE TABLE ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL
)
-- Suggested, not required — tune against your own traffic (see the write-load
-- note below). Leave more free space per page for the update-in-place attempt,
-- and let this hot, small table vacuum far more eagerly than the cluster
-- default:
-- WITH (
--     fillfactor = 70,
--     autovacuum_vacuum_scale_factor = 0.02,
--     autovacuum_vacuum_cost_delay = 0,
--     autovacuum_analyze_scale_factor = 0.05
-- )
;

CREATE INDEX ratelimit_windows_expires_at_idx ON ratelimit_windows (expires_at);
```

`last_allowed` is the outcome of the most recent decision, written by the same
transition that made it — it is how one atomic statement returns its verdict.
`expires_at` is 2.5 windows past that decision, mirroring the goredis key TTL.

**`UNLOGGED` is a real option.** `CREATE UNLOGGED TABLE ratelimit_windows`
roughly halves the WAL this table generates and keeps its contents out of base
backups. The cost: an unlogged table is **truncated** on crash recovery, and it
is not replicated — a failover or a crash restart empties every window. For a
rate limiter that is usually acceptable (worst case, in-flight budgets reset to
full at the moment of a failover); for a deployment where a reset budget is a
security event, keep it logged. The connector behaves identically either way.

**Isolation.** The limiter assumes the stock `READ COMMITTED` default, where a
conflicting `ON CONFLICT DO UPDATE` blocks and then re-evaluates against the
winner's committed row. If your database or role sets a
`REPEATABLE READ`/`SERIALIZABLE` default, that same statement raises
serialization failures (`40001`) under contention instead of blocking, and the
host must retry them — the connector never auto-retries statements.

### Pruning, write load, and retention are the host's job

**Every checked request is one write, and none of them are HOT.** `expires_at`
is indexed and is rewritten on every `Allow`, so each call costs a new heap
tuple **plus** a new index tuple plus the WAL for both — a heap-only-tuple
update is off the table by construction (that is the price of the cheap expiry
sweep). Budget for it: this is one of the highest-churn small tables in the
database, its bloat is autovacuum-bound rather than volume-bound, and the
storage parameters commented into the DDL above exist for exactly that reason.
Monitor write volume and dead-tuple counts against your login/attempt traffic.

**Schedule** the pruning statement (cron, `features/jobs`, or pg_cron — the
connector never runs it):

```sql
DELETE FROM ratelimit_windows WHERE expires_at < now();
```

Rows whose `expires_at` has passed carry no live budget: deleting one is
equivalent to a `Reset` for a key nobody is currently limiting. The index above
is what keeps that sweep cheap.

**Pruning is a data-retention control, not just capacity hygiene.** The `key`
column persists whatever the host puts in its keys, verbatim. With the
authentication feature wired, that includes **client IP addresses** and **user
identifiers** (user IDs raw; email/phone values as digests — a host writing its
own keys may not digest anything). That means this table lands in your base
backups, your WAL archive, and any replica, and it inherits the retention of all
three. Treat the pruning schedule as the retention policy for that data, keep
the table inside whatever DSR/erasure process covers request logs, and prefer
opaque or digested key material for anything you would not want to keep.

For the same reason: **do not enable `DB_LOG_QUERIES` (`Config.LogQueries`) on a
connection with the limiter wired in a shared environment.** The logging tracer
logs SQL args verbatim, so every `Allow` writes the full key — IPs included —
into your application logs, which are typically retained far longer and read far
more widely than this table.

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

The live tests are gated on `POSTGRES_TEST_DSN`: `Open`/ping + a migrate-apply
round-trip, the `List` behavior suite, and the limiter legs (the shared
`ratelimitertest.Run` conformance suite, the exact-K concurrency proof — which
also bounds the returned `RetryAfter`/`Remaining`, the only place a stale
server clock is reachable — server time, burst, prefix isolation, context
cancellation, pool-survives-`Close`, `Allow`/`Reset` error-kind parity,
`StatusCheck` against a present and a missing table, and the pruning statement —
the limiter legs create the reference table themselves, playing the host that
migrated it). Unset, they skip loudly
(`POSTGRES_TEST_DSN not set — postgres conformance NOT verified`, plus a
banner on stderr and the controlling terminal for the limiter legs, because
`go test ./...` hides a passing package's output) — a silent green that tested
nothing is the false-green failure mode we guard against.

**`POSTGRES_TEST_DSN` must point at a throwaway/dedicated database.** The limiter
legs `CREATE TABLE ratelimit_windows`, create and drop a scratch schema, and run
an unqualified `DELETE FROM ratelimit_windows` to prove the pruning statement.

Spin a local database and run the live leg with `-race` — Go-level race hygiene
for the test's own 40 goroutines; the atomicity proof is the exact-K assertion
itself, not the race detector:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test -race ./...
```

`make check` stays hermetic (the live test skips); the live path is the store
modules' conformance gate, recorded as a dated NOTES.md artifact at milestone
close.
