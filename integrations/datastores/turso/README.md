# integrations/datastores/turso

The datastore connector for Turso/libSQL (SQLite dialect). It wraps the libsql
driver behind the same connector shape as
`integrations/datastores/pgxdb`: `Config` / `Open` / `DB` / `MapError` /
`StatusCheck` / `RunMigrations` / `RedactDSN`, plus the shared store-facing
surface (`Querier`, `Scanner`, `ExecAffecting`, the timestamp helpers, and
`List`).

It owns "how to talk to libSQL," never any pocket's SQL. App/pocket
repositories consume this package's `*DB`.

`Open(ctx, cfg)` uses the host's startup context for the driver ping and optional
eager connectivity checks and retry waits. `ConnectTimeout` adds an upper bound
(10 seconds when omitted); the earlier deadline wins. Cancellation before opening
prevents I/O, and startup failures close the owned database. After a successful
return, canceling the startup context does not close the database; the host calls
`DB.Close` when done. The pinned driver's WebSocket connection handshake does not
accept cancellation; use HTTP/libSQL URLs when startup cancellation is required.

`Config.AuthToken` overrides credentials supplied in the URL (`authToken`,
`auth_token` or `jwt`) and is URL-encoded before reaching the driver. URL-only
credentials still work. Malformed query strings and URL fragments are rejected;
`RedactDSN` and `Config.Redacted` mask every supported credential alias and fully
redact malformed input.

Migration streams are flat: `RunMigrations` reads direct `.sql` files in lexical
filename order, skipping directories and names beginning with `_`.
`ExportMigrations` copies direct `.sql` files, including underscore-prefixed SQL
for the host to manage, and skips documentation and nested directories.

## Single-row statement completion

`QueryOne` closes its result before returning a successful row. SQLite may yield
an `UPDATE ... RETURNING` row before the implicit transaction commits; a busy
failure while closing now returns an error and a zero result. Store-owned busy
retry can then retry the statement without exposing an uncommitted job claim.
An explicit transaction still commits through its transaction owner.

## The list helper — `List[T]`, the pgxdb semantic twin

`List[T]`/`ListQuery[T]` implement the `sdk/pkg/list` list standards — ordering
against a per-aggregate allow-list, bidirectional keyset cursors (reverse-probe
prev pages), and opt-in `COUNT(*)` totals — with observable semantics identical
to `pgxdb.List`. Like the pgxdb twin it switches on the request's **resolved
strategy** (`list.StrategyCursor` / `list.StrategyOffset`, explicit — never
inferred from the offset value) into a `listCursor` or `listOffset` flow; the
offset flow appends `LIMIT/OFFSET`, derives HasMore from its own over-fetch, and
emits no cursors. Like the pgxdb twin, `ListQuery[T]` carries an optional
`Limits` (`list.Limits` — the resource's page-size default/max, passed to
`req.NormalizedLimit`; the zero value keeps `list`'s `DefaultLimit`/`MaxLimit`).
The per-pocket `storetest` conformance suites are the parity proof.

The twin is *semantic*, deliberately not idiomatic: it binds `?` placeholders
from an `Args []any` slice (no named-args emulation), scans through a
hand-written `Scan func(Scanner) (T, error)` callback (no struct scanning), and
formats time order values through `FormatTime` to match the fixed-width TEXT
storage. Order-identifier safety is allow-list membership — column strings are
store-authored constants, and raw request input never reaches SQL.

**Follow-up milestone (declared, not scheduled): `turso-crud-parity`** — named
parameters, struct scanning, and builder ergonomics to match the pgx toolkit's
authoring experience. Until it lands, this connector carries exactly the
semantics the conformance suites demand, nothing more.

## Query logging is opt-in and dev-only

Set `Config.LogQueries` (with an optional `Config.Logger`; nil falls back to
`slog.Default()`) to log every query this connector runs — on both the `DB`
connection and its transactions. This is symmetric with the pgxdb connector's
`Config.LogQueries` / `Config.Logger`, and carries the identical posture:
**it logs SQL arguments verbatim**, and those can carry secrets or PII, so it is
dev-only tooling — leave it false in production. database/sql exposes no
driver-level tracer hook (pgx's `ConnConfig.Tracer`), so the `DB`/`Tx` wrapper
threads the logging through its own `Exec`/`Query`/`QueryRow`; the transaction
path logs too, so opting in never silently drops tx statements. At defaults
(`LogQueries` unset) the connector emits nothing. This is interim plumbing,
expected to fold into a shared `sdk/capabilities/tracing` package later.

`RedactDSN` (and `Config.Redacted()`) masks the userinfo password and the
`authToken` query parameter in a libSQL URL, so a host can log the connection
target without leaking the auth token; unparseable input returns the literal
`"REDACTED"`.

## Boot-connectivity retry is opt-in — and statements are never auto-retried

`Config.Retry` (`RetryPolicy{Attempts, MinBackoff, MaxBackoff}`) governs one
thing: the connectivity check `Open` runs at boot. The zero value is no retries
— `Open` keeps its single lazy ping exactly, today's behavior. Setting
`Attempts > 1` opts into **eager** boot validation: `Open` runs a real
round-trip (`StatusCheck` — `Ping` + `SELECT 1`) retried under a full-jitter
exponential backoff (each sleep uniform in `[MinBackoff, cap]`, the cap doubling
from `MinBackoff` up to `MaxBackoff`), aborting on context cancellation. Eager is
deliberate: the remote libSQL driver's `Ping` is lazy, so retrying a ping that
cannot fail would be vacuous. This targets the orchestration race — the database
not yet reachable at startup.

**Statement-level retry is store-owned, explicit, and per-call — the connector
never auto-retries statements.** A method verb does not encode idempotency
(`Query`/`QueryRow` carry `RETURNING` writes), so no automatic retry is applied
to any `Exec`/`Query`/`QueryRow`. `Config.Retry` is boot connectivity only.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — the `List`
behavior tests run against in-memory SQLite through the real driver.

Live conformance runs are per-pocket (the `stores/turso` modules), gated on
`-tags=integration` + `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`, and only ever
against the authorized playground database. Unset, they skip loudly.

## Listing projection and transaction cleanup

All List paths order the projected output of the authored SELECT. Search and
cursor helpers wrap that SELECT before adding their outer predicate. Run them before final ordering or pagination. Project every
search/order/PK field using an unqualified output name: select
`t.created_at AS created_at` and use `Column: "created_at"`. Update strict row
scanners for added projections. `OrderValueOf` must return the projected value
and type. Authored filters, including OR and nested SELECTs, remain intact.

SQL keyset ordering requires non-null order and PK values throughout the matched
population. Use a non-null projected key or offset mode for nullable ordering.
A SQL cursor with a null order value, or a non-string case-folded value, is
invalid input. Other type compatibility belongs to `OrderValueOf` and the
projected column; the generic helper does not inspect the database schema.

Reverse probes include the incoming cursor and fetch `limit+1` records before
restoring normal order for `list.MarkPrevPage`. This fixes previous links at a
page size of one. Case-folded ordering retains the raw primary-key tiebreaker.

`DB.Transact` implements `capabilities/transaction.Transactor` and shares cleanup
with `InTx`. Pass its callback context to participating repositories backed by
the same DB instance. Commit uses the Begin context. Rollback receives an
independent five-second deadline; actual completion depends on the driver.
Callback error causes and panic values survive cleanup. SQL helpers do not
retry callbacks; portable consumers must allow for connectors that do.
Cancellation racing a commit cannot undo a commit already accepted by a server.

A failed manual COMMIT is rolled back before its connection is released. Failed
rollback or uncertain BEGIN disposes of the physical connection. Empty offset
pages now serialize `items: []`, matching cursor pages.
