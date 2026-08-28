# integrations/datastores/pgxdb

The datastore connector for PostgreSQL. It wraps exactly one third-party
library — `github.com/jackc/pgx/v5` (pool via `pgxpool`, same module) — and
gives pocket store modules a connector symmetric with
`integrations/datastores/turso`: `Config` / `Open` / `DB` / `MapError` /
`StatusCheck` / `RunMigrations`.

It owns "how to talk to Postgres," never any pocket's SQL. No ORM and no
general query builder — the one shared query surface is the list toolkit
below, which owns pagination mechanics (ordering, keyset cursors, offset,
counts) while every store keeps writing its own SQL. App/pocket repositories
consume this package's `*DB`.

## Surface

| member | shape |
|---|---|
| `Config` | `DSN` or split `Host`/`Port`/`User`/`Password`/`Database`/`SSLMode`, plus pool settings, `LogQueries`, `Logger`, `Tracer`, and `Retry`; env tags are provided for host parsers, but `Open` never reads environment itself |
| `Open(cfg) (*DB, error)` | opens a `pgxpool` and pings; every connection scans `timestamptz` in UTC and, unless the host named a zone, runs with session `timezone=UTC` (see below) |
| `DB` | `Exec` / `Query` / `QueryRow` / `InTx` / `Begin` / `Close` / `Ping` / `Underlying() *pgxpool.Pool` |
| `Querier` | interface intersection of `*DB` and `*Tx` (`Exec`/`Query`/`QueryRow`) — lets a store accept pool-or-tx |
| `MapError(err) error` | SQLSTATE-based: `23505`→`ErrAlreadyExists`, `23503`→`ErrInvalidReference`, `23514`/`23502`→`ErrInvalidInput`, `22P02` (malformed uuid/integer literal)→`ErrInvalidInput` keeping the server message as the sentence before the sentinel, `pgx.ErrNoRows`→`ErrNotFound`; unknown errors pass through |
| `RedactDSN(dsn) string` | masks a URL-form DSN's userinfo password for safe logging; unparseable input returns the literal `"REDACTED"` |
| `StatusCheck(ctx, db)` | 1s-deadline ping |
| `ProbeTable(ctx, q, table) error` | boot-time existence probe for a store's table (`to_regclass`, name bound as a parameter, bare or schema-qualified): absent → wraps `ErrNotFound` naming the relation; a query failure maps through `MapError` and is never misreported as missing. Run it in a store constructor so a host aimed at the wrong database fails before serving |
| `ProbeTables(ctx, q, tables…) error` | `ProbeTable` over every relation a store owns; probing stops at the first failure and returns it unchanged — the absent-relation error already names the table, and a query failure is about none of them. No tables is nil |
| `RunMigrations(ctx, db, fs, dir, opts…)` | host-driven migration runner for one directory; one transaction, filename order, checksum guard, forward-only. One merged stream per schema: call it once per schema, with filenames unique within the stream that are never renumbered — `dir` is an `fs.FS` subpath, never a ledger namespace (all rows in a stream share the `"default"` source) |
| `MigrateOption` / `WithSchema(s)` | the only `RunMigrations` option: run this stream inside `s` — create the schema if absent, `SET LOCAL search_path` for the transaction, and keep the stream's `schema_migrations` ledger in `s`. No option = today's unqualified stream, byte-for-byte |
| `Schema` / `NewSchema(name) (Schema, error)` | validated Postgres schema name; the zero value means "no schema" and renders bare names. Rejection wraps `ErrInvalidInput` (empty, >63 bytes, non-identifier, reserved `pg_` prefix, `information_schema`); `public` is valid |
| `(Schema).Table(t)` / `IsZero()` / `String()` | `"<schema>".t` for a set schema, bare `t` for the zero value; the one qualifier used by the runner's ledger statements and by every pgx store's `WithSchema` |
| `NewLimiter(db, opts…) *Limiter` / `WithLimiterKeyPrefix` | a durable `sdk/capabilities/ratelimiter.Limiter` over the caller-owned `*DB` — one atomic statement per `Allow` against the host-owned `ratelimit_windows` table (see below) |
| `(*Limiter).StatusCheck(ctx) error` | boot probe for that host-owned table; call it before serving, because a missing table makes the sdk middleware fail open and silent |
| `Collect[T](ctx, q, sql, args…) ([]T, error)` | parent-bounded, unpaginated read: every row scanned into a db-tagged `T` via strict `RowToStructByName`, both the query and the collect error through `MapError`, and no rows as an empty NON-NIL `[]T` so the caller marshals `[]` and never `null`. Not a paging primitive — an unbounded result set belongs on `List` |
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
`pockets/authentication/stores/pgx`, the pattern-setter):

- **Row structs, not domain tags.** `T` is a store-local db-tagged row struct
  with a `toDomain` converter; pages bridge through `crud.MapPage`. Domain
  entities never carry persistence tags.
- **NamedArgs filter builders.** Per-store WHERE fragments are plain funcs
  appending to `pgx.NamedArgs` — shared by the list call and (via the count
  wrap) the total, so the two can never disagree.
- **UNNEST for multi-row writes.** Bulk inserts are single
  `INSERT … SELECT … FROM UNNEST(@col::type[], …)` statements (the cms
  `entry_fields`/`entry_terms` and events outbox writes), never Exec loops.
- **A list whose order is the product's, not the caller's — `FixedOrder`.**
  Some lists are not user-sortable: the store defines a composite order the
  allow-list cannot express (`closing_date DESC NULLS LAST, name ASC, id ASC`;
  a `NULLS LAST`, a second sort column, a computed key). Such a store sets
  `ListQuery.FixedOrder` — trusted store text like `BaseSQL`, written
  verbatim as the `ORDER BY`, with the store's own pk tiebreak included —
  and leaves `OrderFields`/`DefaultOrder` empty (both set is
  `sdk.ErrInvalidInput`, the same posture as an order field outside the
  allow-list). The list is then **offset-only**: a request carrying an
  `Order` or the cursor strategy is `sdk.ErrInvalidInput`, because no keyset
  predicate is derivable from an arbitrary expression. Everything else about
  the offset flow — `LIMIT n+1` for `HasMore`, `HasPrev` from the offset,
  `WithCount` → `Total`, the folded search clause, `MapError` — is unchanged,
  which is the point: the store stops hand-rolling `LIMIT/OFFSET` plus its own
  `COUNT(*)` wrap to get a fixed order. The zero value keeps the allow-list
  path byte-for-byte.

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

## Schema-scoped migrations — `WithSchema`

`RunMigrations(ctx, db, fs, dir, pgxdb.WithSchema(s))` applies a stream inside a
host-chosen schema. Build the schema once at the host so a malformed name fails
before any migration or store is constructed:

```go
s, err := pgxdb.NewSchema(os.Getenv("DB_SCHEMA")) // "auth"
if err != nil { return err }
```

**Quoting preserves case.** `NewSchema("Auth")` and `NewSchema("auth")` are two
different schemas — every rendering is `"Auth".users` / `"auth".users`.

### One stream per schema

A stream is a directory plus the `schema_migrations` ledger that records it.
Without `WithSchema` that ledger is the default schema's; with `WithSchema(s)`
it is `"<s>".schema_migrations`. The ledgers are disjoint, so filename
uniqueness is per (schema, source) and **cross-schema ordering is not expressed
by the ledgers**. A host that wants the pocket tables in `auth` and its own
tables in the default schema exports two directories and makes two calls:

```
migrations/
  auth/            # every pocket's exported stream
    0001_….sql
  0001_….sql       # the host's own tables
```

```go
if err := pgxdb.RunMigrations(ctx, db, os.DirFS("."), "migrations/auth", pgxdb.WithSchema(s)); err != nil {
    return err
}
if err := pgxdb.RunMigrations(ctx, db, os.DirFS("."), "migrations"); err != nil {
    return err
}
```

**One call is one transaction; two streams are two transactions.** There is no
cross-schema atomicity: if the first call commits and the second fails, the
database is partially upgraded and nothing rolls the committed stream back. So:

- fix the call order and keep it deterministic — it is the host's, not the
  ledgers';
- on failure, correct the failing stream and rerun. Every committed stream is
  idempotent by its own ledger, so rerunning replays nothing;
- schema-qualify cross-schema dependencies (foreign keys, views, functions)
  explicitly and apply them after the stream they depend on. Boot probes and a
  store's `StatusCheck` detect an incomplete boot; they cannot roll one back.

Per-repository *different* schemas inside one pocket have no migration story
here: one stream, one schema.

Inside the transaction the runner sets `search_path` to the schema alone —
**no `public` fallback**, because with `public` on the path an
`ALTER TABLE users` whose `"auth".users` is missing would silently alter the
host's `public.users`. `pg_catalog` stays implicitly searched, so
`gen_random_uuid()`, `hashtext`/`hashtextextended`, and `pg_advisory_xact_lock`
resolve; **migrations calling extension functions installed in `public` must
qualify them** (`public.uuid_generate_v4()`).

The four ledger statements are explicitly qualified through `Schema.Table`,
never left to `search_path`.

### If you run the exported stream with your own runner

`ExportMigrations` is unchanged and the exported SQL is unqualified. Apply it
either inside a transaction that has run `SET LOCAL search_path TO "<schema>"`,
or qualify every DDL statement yourself. A pool-wide `search_path` pin in the
DSN is the thing this option replaces: it is global, hidden, and it relocates
the host's own unqualified statements too.

**Do NOT include the limiter DDL in a schema-scoped stream.** The
`ratelimit_windows` reference DDL below is host-schema SQL and the limiter's own
statements are unqualified; merging it into a schema-scoped stream relocates the
table away from the limiter, which then fails at `(*Limiter).StatusCheck`.

### Required grants

| situation | grant |
|---|---|
| the schema does not exist yet and the runner should create it | `CREATE ON DATABASE` for the migrating role |
| the schema is pre-created by a DBA | `USAGE, CREATE ON SCHEMA "<schema>"` for the migrating role |

The runner probes `pg_namespace` first and skips `CREATE SCHEMA` when the
schema already exists, so DBA pre-creation is a real workaround for a role
without `CREATE ON DATABASE`. Each failure names the missing grant and wraps
`ErrForbidden`: schema absent and uncreatable (`insufficient_privilege`, 42501),
missing `USAGE`, missing schema `CREATE`.

### One-time preflight: relocating an existing ledger

If the database was previously migrated under a DSN-level
`search_path=auth,public` pin, the tables are in `auth` but the ledger rows are
probably in `public.schema_migrations` (the probe searched the whole path; the
unqualified `CREATE TABLE`/`INSERT` resolved to the relation it found). Adopting
`WithSchema` then finds an empty `"auth".schema_migrations` and re-runs the whole
stream. That re-run is safe — the shipped pocket files are `IF NOT EXISTS`-safe
— but it is **not free**: authentication `0014_user_status.sql` performs an
`ALTER COLUMN … TYPE TEXT COLLATE "C"` (full table rewrite under an exclusive
lock) and `0015_challenge_subject_keys.sql` repeats an `UPDATE` backfill.

The recommended path is a one-time ledger copy, run in one explicit transaction
**before** the first schema-scoped `RunMigrations` call. Verify the target schema
and the already-migrated tables exist, then:

```sql
BEGIN;
CREATE TABLE IF NOT EXISTS "auth".schema_migrations (
    source TEXT NOT NULL,
    version TEXT NOT NULL,
    checksum TEXT NOT NULL,
    raw_sql TEXT,
    applied_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source, version)
);
INSERT INTO "auth".schema_migrations
    (source, version, checksum, raw_sql, applied_at)
SELECT source, version, checksum, raw_sql, applied_at
FROM public.schema_migrations
WHERE source = 'default' AND version IN (<pocket files>)
ON CONFLICT (source, version) DO NOTHING;

-- Assert every manifest file landed exactly once with a matching checksum.
DO $$
DECLARE
    manifest CONSTANT TEXT[] := ARRAY[<pocket files>];
    copied INT;
    drift INT;
BEGIN
    SELECT count(*) INTO copied
    FROM "auth".schema_migrations
    WHERE source = 'default' AND version = ANY (manifest);
    IF copied <> array_length(manifest, 1) THEN
        RAISE EXCEPTION 'ledger relocation: copied % of % manifest rows',
            copied, array_length(manifest, 1);
    END IF;

    SELECT count(*) INTO drift FROM (
        SELECT source, version, checksum FROM public.schema_migrations
          WHERE source = 'default' AND version = ANY (manifest)
        EXCEPT
        SELECT source, version, checksum FROM "auth".schema_migrations
          WHERE source = 'default' AND version = ANY (manifest)
    ) AS d;
    IF drift <> 0 THEN
        RAISE EXCEPTION 'ledger relocation: % manifest rows missing or checksum-mismatched', drift;
    END IF;
END $$;
COMMIT;
```

Notes that matter:

- the column list is explicit — never `SELECT *`;
- `ON CONFLICT (source, version) DO NOTHING` makes the copy resumable;
- `<pocket files>` is the exact manifest of the files already applied for that
  schema (`'0001_….sql', '0002_….sql', …`), not every row in `public`;
- the assertion aborts the transaction on a missing, duplicated, or
  checksum-mismatched row, so a partial copy never commits;
- do **not** invoke the runner just to create the target ledger — that
  invocation immediately starts applying files;
- only after this transaction commits, call the schema-scoped `RunMigrations`.
  It must report every copied file already applied.

### Statement cache sizing

pgx's default statement cache is 512 entries per connection, and rendered SQL
differs per schema. A host running three or more store sets (schema-per-tenant)
on one pool should size `statement_cache_capacity` in the DSN or use one pool
per schema.

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
authentication pocket's login and passwordless call sites fail **closed** (the
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
ledger (the same scaffold-and-own rule every pocket store follows); the table
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

**Schedule** the pruning statement (cron, `pockets/jobs`, or pg_cron — the
connector never runs it):

```sql
DELETE FROM ratelimit_windows WHERE expires_at < now();
```

Rows whose `expires_at` has passed carry no live budget: deleting one is
equivalent to a `Reset` for a key nobody is currently limiting. The index above
is what keeps that sweep cheap.

**Pruning is a data-retention control, not just capacity hygiene.** The `key`
column persists whatever the host puts in its keys, verbatim. With the
authentication pocket wired, that includes **client IP addresses** and **user
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

## Time zone — UTC-located scans on every connection

`Open` owns `pgxpool.Config.AfterConnect` and registers UTC-located
`timestamptz` codecs on each new connection's type map. `Config` exposes no
`AfterConnect` field; should one be added, it will **chain after** the
connector's registration, never replace it.

**The contract.** A scanned `timestamptz` — scalar or `timestamptz[]` — is a
`time.Time` located in `time.UTC`, under both the extended (binary) and simple
(text) protocols, for row scans and `RowToStructByName` struct scans alike. The
instant is unchanged: `Equal`/`Before`/`Sub` behave exactly as before. What
changes is presentation — `String()`/`Format` and `encoding/json` now render `Z`
instead of the process's local offset. There is no `Config` knob: a
`time.Time`'s location is presentation, not data, so a host that wants a local
rendering calls `.In(loc)` at its own edge.

**Why it was needed.** pgx's default `timestamptz` decoder for the extended
protocol reads microseconds since 2000-01-01 with no zone and builds the value
with `time.Unix`, which yields a `time.Local`-located `time.Time`
(`pgtype/timestamptz.go:272-278`). The session `TimeZone` feeds only the *text*
decoder's input (`:305-331`), so `SET TIME ZONE 'UTC'` alone would not have
changed a scanned value. `pgtype.TimestamptzCodec.ScanLocation` is the seam both
decoders honour (`:276-278`, `:326-328`), and that is what the connector sets.

**`tstzrange` / `tstzmultirange` are out of scope.** pgx's default range codecs
captured a pointer to the default `timestamptz` element type at init
(`pgtype/pgtype_default.go:119`, `:127`, the same pattern as `_timestamptz` at
`:169`), so they would each need explicit re-registration. No store in this repo
scans a timestamp range; a host that does can register its own on top.

### The session zone defaults to UTC

Unless the host named a time zone, `Open` sets the startup parameter
`timezone=UTC` (`ConnConfig.RuntimeParams`, not a DSN rewrite — pgconn has
already parsed the DSN, and a startup parameter costs no extra round-trip and
survives PgBouncer's transaction-mode connection reuse, which a `SET` does not).

**The host wins whenever it named a zone**, in any of the forms pgconn folds
into `RuntimeParams` (`pgconn/config.go:351-356`, `:466-469`):

- a `timezone=` key in the DSN (`…?sslmode=disable&timezone=Europe/Oslo`, or
  `timezone=Europe/Oslo` in `key=value` form),
- the `PGTZ` environment variable (pgconn maps it onto `timezone`),
- a `timezone=` entry inside an `options` value — DSN `options=-c timezone=…` or
  `PGOPTIONS='-c TimeZone=…'` (matched case-insensitively).

`Open` still never reads the process environment itself; `PGTZ`/`PGOPTIONS` are
pgconn's own handling, honoured here as "the host named a zone".

**What the default changes, for a host that named none.** Nothing about scans —
those are covered above and are unconditional. It changes *server-side*,
zone-dependent SQL, which previously evaluated in whatever zone the server
defaulted to:

- `now()::text` and any `timestamptz → text` rendering,
- `to_char(tstz, …)`,
- `date_trunc('day', tstz)` and other bucketing,
- `tstz::date`,
- `EXTRACT(hour FROM tstz)`,
- `timestamptz → timestamp` casts,
- a bare literal such as `'2026-01-01 00:00'` bound to a `timestamptz`, which is
  interpreted in the session zone (the sharp one).

Bound `time.Time` arguments are unaffected — pgx binds an instant. The escape
hatches are `AT TIME ZONE '…'` per expression, or pinning a session zone with
`timezone=` in the DSN / `PGTZ` / `options=-c timezone=…` as above. Grep a host
for `date_trunc|::date|to_char|AT TIME ZONE` before adopting if it relied on a
server-local zone.

## Symmetry is convention, not a guarantee

This connector mirrors the turso connector member-for-member **by convention**.
No `make guard` row proves the two surfaces or their sentinel coverage stay
aligned; a pocket's `storetest` conformance suite is the only parity net, and
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
`Collect` and `ProbeTables` over in-memory `Querier` stubs, config validation,
migration checksum/error paths, `poolConfig`'s
`AfterConnect`/session-zone defaulting, and the codec registration driven through
a bare `pgtype.Map` in both wire formats) and run with a plain `go test ./...` —
no database required. The time-zone default cases clear `PGTZ`, `PGOPTIONS`,
`PGSERVICE`, and `PGSERVICEFILE` first, so ambient libpq configuration cannot
decide the result.

The live tests are gated on `POSTGRES_TEST_DSN`: `Open`/ping + a migrate-apply
round-trip, the schema-scoped migration legs (tables and ledger inside the
schema with `public` untouched, schema-then-default ledger isolation on one
pool, the non-atomic per-schema transaction boundary, the ledger-relocation
preflight and the no-copy fallback, and the four-case privilege matrix), the
`List` behavior suite, `TestLive_Collect` (parent-bounded rows, an empty non-nil
result, a collect inside a transaction, and `22P02`/unknown-relation error
mapping), `TestLive_ScanUTC` (UTC-located scalar, array, struct-scan
and simple-protocol reads, `SHOW TimeZone` = `UTC`, and the DSN override keeping
its own session zone while scans stay UTC), and the limiter legs (the shared
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
The schema legs create and `DROP SCHEMA … CASCADE` their own disposable schemas
and `CREATE ROLE`/`DROP ROLE` disposable login roles, and delete only the
`public.schema_migrations` rows they wrote.

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
