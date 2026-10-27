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

## Connection configuration

Zero `MaxLifetime`, `MaxIdleTime`, pool counts and `HealthCheckPeriod` retain
DSN values or pgx defaults. Positive fields override the parsed settings; negative
values, overflowing counts, inconsistent minimum/maximum counts and invalid
health-check periods fail at construction. The pinned pgx defaults retire
connections after one hour and idle connections after 30 minutes. Omitted
lifetimes no longer retire a connection after every use.

`Open(ctx, cfg)` uses the host's startup context for connection checks and retry
waits. `ConnectTimeout` adds an upper bound (10 seconds when omitted); the earlier
deadline wins. Cancellation before opening prevents connection attempts, and a
startup failure closes the owned pool. After a successful return, canceling the
startup context does not close the database; the host calls `DB.Close` when done.
Pocket SQL store constructors take a host context for schema probes; pass your
startup context and deadline to those constructors too.
SQL migration streams are flat directories: nested files belong to separate
host-controlled streams; export copies only direct SQL files.

### Secrets — `DSN` and `Password` are `sdk.Secret`

`Config.DSN` (`DB_URL`) and `Config.Password` (`DB_PASSWORD`) are `sdk.Secret`:
they print `[redacted]` through `fmt` (every verb, `%+v` of a `Config` included),
`slog` and `encoding/json`. `environment.ParseEnvTags` fills them like any string
field, and an untyped literal still assigns; a `string` variable converts with
`sdk.Secret(v)`. Read the value with `Reveal()`; display the target with
`cfg.Redacted()`.

A DSN composed from a printed Secret — `fmt.Sprintf("postgres://u:%s@h/db",
cfg.Password)` — carries `[redacted]` instead of the password. `Open` refuses a
`DSN` or `Password` containing `sdk.Redacted`, in every mode and before any
connection attempt, with an error wrapping `sdk.ErrRedactedSecret` that names the
field and says to read it with `Reveal()`:

```go
dsn := fmt.Sprintf("postgres://u:%s@h/db", cfg.Password.Reveal()) // not cfg.Password
db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: sdk.Secret(dsn)})
if errors.Is(err, sdk.ErrRedactedSecret) { /* a rendered Secret reached the DSN */ }
```

### Statement and lock timeouts — `StatementTimeout` / `LockTimeout`

`Config.StatementTimeout` (`DB_STATEMENT_TIMEOUT`) and `Config.LockTimeout`
(`DB_LOCK_TIMEOUT`) bound every statement and every lock wait on every pooled
connection. `Open` sends them as the `statement_timeout` / `lock_timeout` startup
parameters (`ConnConfig.RuntimeParams`, in whole milliseconds — a sub-millisecond
remainder truncates), so they hold on every connection the pool dials at no extra
round-trip. Zero, the default, sends nothing: the server's, database's or role's
setting stands. A negative value fails at `Open`, and so does a positive value
under 1ms — it would serialize as `0`, which the server reads as "disabled".

```sh
DB_STATEMENT_TIMEOUT=30s
DB_LOCK_TIMEOUT=5s
```

A statement past the bound fails with SQLSTATE `57014` (`query_canceled`), a lock
wait with `55P03` (`lock_not_available`). `MapError` passes both through unchanged,
like every SQLSTATE it does not map: the caller sees an unexpected error (a 500),
never a domain sentinel.

**One source per parameter.** When the connection string already names the same
parameter — a `statement_timeout=` key, or an `options` entry such as
`options=-c lock_timeout=…` in any switch form the server accepts (`-c name=…`,
`-cname=…`, `--name=…`; dashes or underscores; any case), `PGOPTIONS` included —
AND the `Config` field is set, `Open` fails with `sdk.ErrInvalidInput` before any
connection attempt. There is no silent winner (the server would apply the separate
startup parameter over the `options` entry). Set one or the other; a DSN-only
setting reaches the server exactly as written.

**Server process only.** Set the knobs on the serving process. A migrator leaves
both zero: an index build or a backfill runs far past a request-sized statement
timeout, and a lock timeout aborts a migration queued behind live traffic. A
migrator that parses the same environment into `pgxdb.Config` zeroes the two
fields after parsing.

**PgBouncer in transaction mode** accepts only the few startup parameters it
tracks and refuses the rest (`unsupported startup parameter`); listing them in
`ignore_startup_parameters` drops them, so the bound silently never applies.
Behind such a pooler, leave both knobs zero and put the bound on the runtime role,
which every server connection the pooler opens picks up at session start:

```sql
ALTER ROLE app_runtime SET statement_timeout = '30s';
ALTER ROLE app_runtime SET lock_timeout = '5s';
```

## Production posture — `RuntimeMode`

`Config.RuntimeMode` is the host's `environment.Mode`. The composition root reads
it from the process environment and sets it in code — it carries no env tag, and
neither do the two acknowledgements below, so `environment.ParseEnvTags` over
`Config` never fills them. The zero value is mode-blind: `Open` behaves exactly as
before. A non-zero mode must be `development` or `production`
(`environment.ErrModeRequired` / `ErrModeInvalid` otherwise).

```go
var cfg pgxdb.Config
if err := environment.ParseEnvTags("", &cfg); err != nil {
    return err
}
cfg.RuntimeMode = mode // validated by cmd/ from the process environment
db, err := pgxdb.Open(ctx, cfg)
```

The check runs over pgx's own parse of the connection string, before the pool
exists, so `PG*` environment defaults, `~/.postgresql/root.crt` and every
multi-host fallback are judged exactly as pgx will dial them, and a refusal never
attempts a connection. Every target — the primary and each entry pgx keeps in
`ConnConfig.Fallbacks` — is one of:

| class | pgx shape | connection strings | production |
|---|---|---|---|
| verified | TLS that checks the server: `InsecureSkipVerify` off, or skip-verify plus pgx's own `VerifyPeerCertificate` chain check | `sslmode=verify-full`; `sslrootcert=system`; `sslmode=verify-ca` + `sslrootcert`; `sslmode=require` + `sslrootcert` (libpq treats it as `verify-ca`) | admitted |
| unverified | TLS with no server check | a bare `sslmode=require` | refused unless `TLSUnverifiedAcknowledged` |
| plaintext | no TLS on a TCP host | `sslmode=disable`; the plaintext fallback that `sslmode=prefer` (the libpq default when `sslmode` is absent) and `sslmode=allow` keep | refused unless `TLSDisabledAcknowledged` |
| local | a unix-socket host, which pgx never wraps in TLS | `host=/cloudsql/<instance>`, `host=/var/run/postgresql` | admitted; `Open` logs `tls=local_socket` at INFO |

Production admits a configuration only when every target is verified or local.
`TLSUnverifiedAcknowledged` additionally admits unverified targets;
`TLSDisabledAcknowledged` admits all. A refusal is `ErrTLSRequired` naming the
class, the target, `cfg.Redacted()` and the fix — `sslmode=verify-full` with
`sslrootcert=system` or the provider CA, or `sslmode=verify-ca` with
`sslrootcert` where the certificate does not name the host — and never the DSN
itself.

Production also refuses query logging with `ErrQueryLoggingRefused`: `LogQueries`
(`DB_LOG_QUERIES`), `LogQueryArgs` (`DB_LOG_QUERY_ARGS`, even alone — a stale
variable is a signal), and a `Tracer` that is or wraps (through
`MultiQueryTracer`) a `*LoggingQueryTracer` whatever its `LogArgs`. The text
names each source with its variable — `Config.LogQueries (DB_LOG_QUERIES)` — and
no acknowledgement admits it. When both refusals apply, `Open` returns them
joined.

Development admits everything and logs one WARN per condition production would
refuse. In production each honored acknowledgement logs one WARN carrying
`posture_waiver=TLSUnverifiedAcknowledged` or
`posture_waiver=TLSDisabledAcknowledged`, so the runtime log evidences the
waiver. An acknowledgement that waives nothing is inert — one binary runs in
several environments. Posture lines go to `Config.Logger` (`slog.Default()` when
nil).

### Managed providers

| provider | connection | class |
|---|---|---|
| Neon | `sslmode=verify-full&sslrootcert=system` | verified |
| Supabase, AWS RDS / Aurora, Azure Database for PostgreSQL | `sslmode=verify-full&sslrootcert=<provider CA bundle>` | verified |
| Google Cloud SQL through the Auth Proxy or connector unix socket | `host=/cloudsql/<project>:<region>:<instance>` (the proxy encrypts upstream) | local |
| Google Cloud SQL by direct IP | `sslmode=verify-ca&sslrootcert=server-ca.pem` (the server certificate names the instance, not the IP) | verified |
| Heroku-style self-signed server | `sslmode=require` plus `TLSUnverifiedAcknowledged: true` | unverified, acknowledged |

Loopback TCP earns no exemption: an Auth Proxy listening on `127.0.0.1:5432` with
`sslmode=disable` is plaintext. Run the proxy with `--unix-socket`, or
acknowledge with `TLSDisabledAcknowledged`.

## Append-only tables: the posture probe

The contract, in one sentence: **in production a store refuses to boot when its runtime role
is superuser, owns an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified
triggers, or cannot insert into it — or when a definer function it lists for the table is
executable by PUBLIC, lacks the pinned `search_path`, or resolves outside the table's schema.**

`(*DB).ProbeAppendOnly(ctx, tables ...AppendOnlyTable)` enforces it for the role the `DB`
connects as, under the `RuntimeMode` the `DB` was opened with (`DB.RuntimeMode()`). The pgx
stores call it from their constructors over their own audit tables. `InspectTablePrivileges(ctx,
q, role, tables...)` returns the same evidence for any role — an empty `role` is
`current_user` — so a migrator can preflight the runtime role from its own connection and get
exactly the decision boot will make; `TablePrivilege.ProtectedBy(acknowledged)` names what
protects each table: `privilege`, `trigger`, `waiver` or `none`. Requires PostgreSQL ≥ 14 (the
trigger files use `CREATE OR REPLACE TRIGGER`).

Each `AppendOnlyTable{Name, AllowDelete, Canonical, Functions}` is resolved once with `to_regclass` (a
missing table is `Exists: false`, not an error) and inspected by oid, every name bound as a
parameter:

| evidence | source |
|---|---|
| `Owner`, `OwnerKind` | `pg_has_role(role, …, 'MEMBER')` — NOINHERIT members included — on the owner of the table (`table`), of its schema (`schema`) or of a trigger function of the pair (`function`) |
| `Superuser` | `pg_roles.rolsuper` |
| `Insert`, `Delete`, `Truncate`, `Trigger` | `has_table_privilege` |
| `Update` | `has_any_column_privilege` — a column-level grant counts |
| `SequenceUsage` | `has_sequence_privilege(…, 'USAGE')` on the sequence the `seq` column's default draws from, found through `pg_attribute`/`pg_attrdef`; true when the table has no `seq` column |
| `ImmutableTrigger`, `TriggerState` | the verified trigger pair, below |

The pair is `<table>_immutable` (`BEFORE UPDATE OR DELETE … FOR EACH ROW`) and
`<table>_no_truncate` (`BEFORE TRUNCATE … FOR EACH STATEMENT`). It is verified only when both
triggers exist, are not internal, are `ENABLE ALWAYS` (`tgenabled = 'A'`), carry no `WHEN`
clause and no column list, have the expected level and the `pg_get_triggerdef` form, and execute
a plpgsql function in the table's schema whose name and body (`prosrc`) match one of the store's
`Canonical` `TriggerBody` values, that is not `SECURITY DEFINER`, and whose `proconfig` is exactly
`{search_path=pg_catalog, pg_temp}`. `TriggerState` is `verified` or the first failure:
`absent`, `internal`, `tgenabled=O` (what `pg_restore --disable-triggers` leaves; `D` and `R`
likewise), `level`, `when_clause`, `column_list`, `namespace`, `differs`, `language`,
`body_differs`, `security_definer`, `proconfig`.

`Writable()` is `Owner || Superuser || ((Update || (Delete && !AllowDelete) || Truncate ||
Trigger) && !ImmutableTrigger)`: an owner or superuser is refused even with the triggers, which
exempt the owning role by design; TRIGGER counts because its holder can change what happens to
inserted rows. `CannotAppend()` is `!Insert || !SequenceUsage`.

`ProbeAppendOnly` runs in its own read-only transaction with `SET LOCAL search_path TO
pg_catalog` once the names are resolved:

- **zero mode** — nil, no query, no log line;
- **development** — never refuses; one WARN line `append_only=unenforced tables=[…]` with
  every table production would refuse and why;
- **production** — `errors.Join` of one error per finding: `ErrAppendOnlyTableWritable`
  (`owner=table|schema|function`, `superuser`, or `privileges=update,delete,truncate,trigger`
  with `trigger=<state>` or `tgenabled=<value>`) or `ErrAppendOnlyTableUnwritable`
  (`insert=false`, `sequence_usage=false sequence=<name>`), each naming the table, the reason
  and the fix — plus `ErrDefinerFunctionUnsafe` per unsafe definer function (below).

The fix every refusal names, with the inspected role and a `<migrator>` placeholder:

```text
activate the trigger files; create the runtime role with SQL, not the console;
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON <t> FROM <role>    -- DELETE is kept where AllowDelete (iam_operations)
GRANT USAGE ON SEQUENCE <t>_seq TO <role>
ALTER TABLE <t> OWNER TO <migrator>
ALTER SCHEMA public OWNER TO <migrator>
ALTER TABLE <t> ENABLE ALWAYS TRIGGER <t>_immutable           -- and <t>_no_truncate
```

`Config.AppendOnlyWritableAcknowledged` (programmatic, no env tag) admits
`ErrAppendOnlyTableWritable` only — the single-role host. Each admitted table logs one WARN
carrying `posture_waiver=AppendOnlyWritableAcknowledged`: for an owner or superuser, "runtime
role owns <t>: immutability triggers do not apply" with `trigger_exempt=true`; for a role
holding grants without the verified pair, "runtime role can modify <t>: immutability triggers
are not verified" with `trigger_exempt=false`. `ErrAppendOnlyTableUnwritable` is never waivable:
the rail could not record anything.

Managed providers — the two-role recipe is
[database-posture.md](../../../workshop/documentation/docs/guides/database-posture.md):

- **Neon** — roles created in the console or the API carry `pg_write_all_data` through
  `neon_superuser`, which no per-table `REVOKE` removes; create the runtime role with SQL.
- **AWS RDS / Aurora** — `rds_superuser` can set `session_replication_role`, which silences
  triggers that are not `ENABLE ALWAYS`; hence `tgenabled = 'A'`.
- **Google Cloud SQL** (and Neon or Supabase default roles) — on PostgreSQL 15+ `public` belongs
  to `pg_database_owner`, which `cloudsqlsuperuser` and those defaults reach: the probe reports
  `owner=schema` until `ALTER SCHEMA public OWNER TO <migrator>`.

### Definer functions on an append-only table

A store lists `Functions []DefinerFunction{Signature}` on an `AppendOnlyTable` for the
migrator-owned `SECURITY DEFINER` functions its runtime role calls to do what its own grants
refuse (the authentication store's `security_events_purge_before`). `Signature` is bound to
`to_regprocedure`, bare or schema-qualified, with every argument type `pg_catalog`-qualified —
`security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)` — and the store calls
the function with the same explicit casts, so a planted overload cannot make the call
ambiguous. A store that lists none is probed, and logs and refuses, byte for byte as before.

Each signature is resolved in the same phase as the tables — on the caller's `search_path`,
**before** `ProbeAppendOnly` pins `pg_catalog` (resolved after the pin, every bare-schema host
would read "function missing") — and inspected by oid into `TablePrivilege.Functions[Signature]`,
a `FunctionPrivilege`:

| evidence | source |
|---|---|
| `Exists` | `to_regprocedure` resolved it; a missing function is `Exists: false`, not an error and not a probe finding — the store that lists it refuses, naming its migration |
| `SameNamespace` | `pronamespace = relnamespace`: the function lives in the table's schema, not a same-named function earlier on the `search_path` |
| `SearchPathPinned` | `proconfig` is exactly ONE element, `search_path=pg_catalog, <schema>, pg_temp`, the expected text built in SQL with `quote_ident(nspname)` — PostgreSQL flattens a schema needing quotes as `"S3-Mixed"` |
| `SecurityDefiner` | `prosecdef` |
| `OwnerIsTableOwner` | `pg_has_role(proowner, relowner, 'MEMBER')` |
| `OwnerSuperuser` | `rolsuper` of the function's owner |
| `PublicExecute` | `has_function_privilege('public', …, 'EXECUTE')` |
| `PathSchemaCreate` | `has_schema_privilege(role, <the table's schema>, 'CREATE')` — PUBLIC's grant included |
| `Execute` | `has_function_privilege(role, …, 'EXECUTE')` — evidence only; the store decides what a missing grant means for the capability it wires |

The probe never compares a definer function's body. The findings split in two:

- **Unsafe — refused.** `TablePrivilege.UnsafeFunctions()`: PUBLIC holds EXECUTE, the
  `search_path` is not pinned or not the canonical value, or the signature resolves outside the
  table's schema. Production returns `ErrDefinerFunctionUnsafe` — one per function in the
  `errors.Join` — naming the table, `function=<signature>`, the reasons (`public_execute=true`,
  `search_path_pinned=false`, `same_namespace=false namespace=<schema>`) and the fix:
  `REVOKE EXECUTE ON FUNCTION <fn> FROM PUBLIC; GRANT EXECUTE ON FUNCTION <fn> TO <role>`, or
  `ALTER FUNCTION <fn> RESET ALL; ALTER FUNCTION <fn> SET search_path = pg_catalog, <schema>, pg_temp`.
  **Not waivable**: `AppendOnlyWritableAcknowledged` never admits it (a host `DROP` + `CREATE`
  silently re-grants PUBLIC EXECUTE). Development names each in the one `append_only=unenforced`
  line.
- **Inoperative — WARN.** `TablePrivilege.InoperativeFunctions()`: not `SECURITY DEFINER`, an
  owner that is not a member of the table's owner, a superuser owner, or CREATE on the table's
  schema for the inspected role. Production boots and logs one `definer_function=inoperative`
  WARN per function with the fix (`ALTER FUNCTION <fn> SECURITY DEFINER`; `ALTER FUNCTION <fn>
  OWNER TO <table owner>`; a `NOSUPERUSER` migrator owning the table and the function;
  `REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC, <role>`); the function then fails loudly when
  called. Development stays silent on it.

No function state moves `Writable()`, `CannotAppend()` or `ProtectedBy()`: DELETE on the rail
stays the owner's, so the runtime role's posture is unchanged. A function passes when the
migrator creates it with a static body, then pins it from the table's own namespace (never
`current_schema()`, never `SET search_path FROM CURRENT`, which captures the runner's path
without `pg_temp`), then revokes and grants:

```sql
DO $$ BEGIN
  EXECUTE pg_catalog.format('ALTER FUNCTION <fn>(pg_catalog.timestamptz, pg_catalog.int4) SET search_path = pg_catalog, %I, pg_temp',
    (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = '<table>'::pg_catalog.regclass));
END $$;
REVOKE EXECUTE ON FUNCTION <fn>(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION <fn>(pg_catalog.timestamptz, pg_catalog.int4) TO <runtime role>;
```

The same `DO` block re-pins a function restored into a renamed schema. A `-posture-role` runner
reads the same evidence from the migrator's connection through `InspectTablePrivileges` and can
print it per table — `functions=[<name>:execute=true|false unsafe=… inoperative=…]` — deciding
as the role's boot probe does.

**`TablePrivilege` is no longer comparable (v0.12.0):** the `Functions` map field means `==` on
two `TablePrivilege` values no longer compiles; compare with `reflect.DeepEqual` or field by
field.

**Behavior change (v0.11.0):** `MapError` maps SQLSTATE `42501` (`insufficient_privilege`) to
`sdk.ErrForbidden` — a 403 through the web mappers — for every consumer, keeping the
`*pgconn.PgError` in the chain so `errors.As` still reads the SQLSTATE. A missing grant and an
append-only trigger's refusal (`<t> is append-only`) both arrive that way; a host that matched
the raw driver error sees the sentinel now.

## Surface

| member | shape |
|---|---|
| `Config` | `DSN` or split `Host`/`Port`/`User`/`Password`/`Database`/`SSLMode` (`DSN` and `Password` are `sdk.Secret`; see "Secrets"), plus pool settings, `StatementTimeout`/`LockTimeout` (see "Statement and lock timeouts"), `LogQueries`, `LogQueryArgs`, `Logger`, `Tracer`, and `Retry`; env tags are provided for host parsers; pgx also honors standard `PG*` environment defaults during DSN parsing. `RuntimeMode`, `TLSUnverifiedAcknowledged`, `TLSDisabledAcknowledged` and `AppendOnlyWritableAcknowledged` are programmatic (no env tag; see "Production posture" and "Append-only tables") |
| `ErrTLSRequired` / `ErrQueryLoggingRefused` | production posture refusals from `Open`, matched with `errors.Is` |
| `ErrAppendOnlyTableWritable` / `ErrAppendOnlyTableUnwritable` / `ErrDefinerFunctionUnsafe` | production append-only refusals from `ProbeAppendOnly`, matched with `errors.Is` |
| `Open(ctx, cfg) (*DB, error)` | refuses a `DSN`/`Password` containing `sdk.Redacted` (`sdk.ErrRedactedSecret`), then opens a `pgxpool` and pings; every connection scans `timestamptz` in UTC and, unless the host named a zone, runs with session `timezone=UTC` (see below) |
| `DB` | `Exec` / `Query` / `QueryRow` / `InTx` / `Begin` / `Close` / `Ping` / `Underlying() *pgxpool.Pool` / `RuntimeMode()` / `ProbeAppendOnly(ctx, tables…)` |
| `Setting` / `(*DB).TransactWithSettings(ctx, settings, fn)` / `(*Tx).Setting(name)` | `Transact` whose first statement applies transaction-local settings (`set_config(name, value, true)`, all bound); validation before `Begin` refuses with `ErrInvalidInput`; `Tx.Setting` reports, from memory, what was applied to that transaction (see "Transaction-local settings for row-level security") |
| `Querier` | interface intersection of `*DB` and `*Tx` (`Exec`/`Query`/`QueryRow`) — lets a store accept pool-or-tx |
| `MapError(err) error` | SQLSTATE-based: `23505`→`ErrAlreadyExists`, `23503`→`ErrInvalidReference`, `23514`/`23502`→`ErrInvalidInput`, `22P02` (malformed uuid/integer literal)→`ErrInvalidInput` keeping the server message as the sentence before the sentinel, `42501`→`ErrForbidden` keeping the `*pgconn.PgError` in the chain, `pgx.ErrNoRows`→`ErrNotFound`; unknown errors pass through |
| `RedactDSN(dsn) string` | masks URL userinfo, `password` and `sslpassword`; keyword DSNs and malformed inputs return the literal `"REDACTED"` |
| `StatusCheck(ctx, db)` | 1s-deadline ping |
| `ProbeTable(ctx, q, table) error` | boot-time existence probe for a store's table (`to_regclass`, name bound as a parameter, bare or schema-qualified): absent → wraps `ErrNotFound` naming the relation; a query failure maps through `MapError` and is never misreported as missing. Run it in a store constructor so a host aimed at the wrong database fails before serving |
| `ProbeTables(ctx, q, tables…) error` | `ProbeTable` over every relation a store owns; probing stops at the first failure and returns it unchanged — the absent-relation error already names the table, and a query failure is about none of them. No tables is nil |
| `InspectTablePrivileges(ctx, q, role, tables…) (Report, error)` | the append-only evidence for `role` (empty = `current_user`) over `AppendOnlyTable{Name, AllowDelete, Canonical []TriggerBody{Function, Body}, Functions []DefinerFunction{Signature}}`; `Report{Role, Tables []TablePrivilege}`; `TablePrivilege.Writable()` / `CannotAppend()` / `ProtectedBy(acknowledged)`; `TablePrivilege.Functions map[string]FunctionPrivilege` / `UnsafeFunctions()` / `InoperativeFunctions()` (see "Append-only tables" and "Definer functions") |
| `RunMigrations(ctx, db, fs, dir, opts…)` | host-driven migration runner for one directory; one transaction, filename order, checksum guard, forward-only. One merged stream per schema: call it once per schema, with filenames unique within the stream that are never renumbered — `dir` is an `fs.FS` subpath, never a ledger namespace (all rows in a stream share the `"default"` source) |
| `MigrateOption` / `WithSchema(s)` | the only `RunMigrations` option: run this stream inside `s` — create the schema if absent, `SET LOCAL search_path` for the transaction, and keep the stream's `schema_migrations` ledger in `s`. No option = today's unqualified stream, byte-for-byte |
| `Schema` / `NewSchema(name) (Schema, error)` | validated Postgres schema name; the zero value means "no schema" and renders bare names. Rejection wraps `ErrInvalidInput` (empty, >63 bytes, non-identifier, reserved `pg_` prefix, `information_schema`); `public` is valid |
| `(Schema).Table(t)` / `IsZero()` / `String()` | `"<schema>".t` for a set schema, bare `t` for the zero value; the one qualifier used by the runner's ledger statements and by every pgx store's `WithSchema` |
| `NewLimiter(db, opts…) *Limiter` / `WithLimiterKeyPrefix` / `WithLimiterSchema` | a durable `sdk/capabilities/ratelimiter.Limiter` over the caller-owned `*DB` — atomic admission against the host-owned `ratelimit_windows` table, optionally qualified with a validated `Schema` (see below) |
| `(*Limiter).StatusCheck(ctx) error` | boot probe for the host-owned table and required window_ms column; call it before serving |
| `Collect[T](ctx, q, sql, args…) ([]T, error)` | parent-bounded, unpaginated read: every row scanned into a db-tagged `T` via strict `RowToStructByName`, both the query and the collect error through `MapError`, and no rows as an empty NON-NIL `[]T` so the caller marshals `[]` and never `null`. Not a paging primitive — an unbounded result set belongs on `List` |
| `List[T]` / `ListQuery[T]` | the shared paginated-SELECT helper implementing the `sdk/pkg/list` list standards (see below) |
| `QuoteIdentifier(ident) (string, error)` | regex allow-list + per-segment double-quoting for dynamic identifiers (order columns); rejection wraps `ErrInvalidInput` |
| `ApplyCursorPagination` / `AddOrderByClause` / `AddLimitClause` | NamedArgs SQL builders under `List`: tuple-comparison keyset predicate (direction × forPrevious operator table), ORDER BY with PK tiebreaker + optional `LOWER()`, `LIMIT @limit` |
| `LoggingQueryTracer` / `NewLoggingQueryTracer(logger)` / `NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{Logger, LogArgs})` | `pgx.QueryTracer` over `*slog.Logger`, context-aware (`request_id`): `sql`, `arg_count`, `arg_types`; a failed query's `sqlstate`/`constraint` or `error_type`. `LogArgs` adds `args` and the error text verbatim. **Dev-only** — see "Query logging" |
| `MultiQueryTracer` / `NewMultiQueryTracer` | fans a query trace out to several `pgx.QueryTracer`s (pgx accepts only one) |
| `PrettyPrintSQL(sql) string` | whitespace-normalizes SQL for log lines |

## The list toolkit — `List[T]` over `ListQuery[T]`

`List[T]` runs a paginated SELECT to the `sdk/pkg/list` standards; the list
package doc's mode/count matrix is normative, and this helper is its pgx
implementation. A store describes its list with a `ListQuery[T]`:
`BaseSQL` (a `SELECT … FROM … [WHERE …]` with **no** ORDER BY/LIMIT/OFFSET),
`Args` (`pgx.NamedArgs` for the base WHERE), the aggregate's `OrderFields`
allow-list + `DefaultOrder`, the `PK` tiebreaker column, an optional
`Limits` (`list.Limits` — the resource's page-size default/max, passed to
`req.NormalizedLimit`; the zero value keeps `list`'s `DefaultLimit`/`MaxLimit`),
and `OrderValueOf`/`PKOf` accessors for cursor encoding. The helper validates the
request, resolves the order **by column** against the allow-list (every
identifier passes `QuoteIdentifier`), then switches on the request's
**resolved strategy** into one of two linear flows — `listCursor` (keyset
predicate + reverse-probe prev pages) or `listOffset` (`LIMIT/OFFSET`, HasMore
from its own over-fetch, no cursors emitted). The strategy is explicit
(`list.StrategyCursor` / `list.StrategyOffset`), never inferred from the offset
value, so `Offset 0` under the offset strategy is a real first offset page. Both
flows scan via `pgx.CollectRows` + `RowToStructByName[T]`, and on `WithCount`
wrap `BaseSQL` in a `COUNT(*)` subquery so the filter WHERE is reused by
construction.

Store conventions that ride the toolkit (set by the authentication store,
`pockets/authentication/stores/pgx`, the pattern-setter):

- **Row structs, not domain tags.** `T` is a store-local db-tagged row struct
  with a `toDomain` converter; pages bridge through `list.MapPage`. Domain
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

The limiter DDL can share a schema-scoped host stream when the limiter is
constructed with `WithLimiterSchema(s)` for that same schema. Without that option,
its statements stay unqualified. See the [qualified limiter DDL](#schema-selection-on-a-shared-pool)
below; changing a migration stream alone does not configure the limiter.

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

Semantics match Memory and Redis: anchored two-window counters, integer
milliseconds, and Requests+Burst as the ceiling. Windows round up to milliseconds;
invalid keys or numerical ranges match `sdk.ErrInvalidInput`. A live key cannot
change Window; ceiling changes preserve consumed quota. Expired rows are absent
for admission even before physical pruning. Reset clears one compatible key.
Both methods map database errors through MapError. The host owns pool lifecycle;
the limiter has no Close method.

Every configured prefix receives the internal `v2:` suffix. New code normally
starts fresh budgets beside old keys. A collision with legacy state lacking a
valid window_ms returns `sdk.ErrConflict` and preserves the row, even on Reset.
Old and new writers must have disjoint physical keys; use a fresh host prefix if
old logical v2:* keys could overlap. The format guard cannot detect an old writer
overwriting new state.
Coordinate the required column migration and rolling-upgrade quota policy using
[AUDIT-011](../../../AUDIT.md#audit-011-rate-limiter-contract-and-adapter-corrections).

**Every quota decision is one atomic statement.** `INSERT … ON CONFLICT … DO UPDATE … RETURNING`
serializes admission on the row. A missing key first creates an expired, zero-count
placeholder, then repeats the statement to decide after locking that record. Thus
a new key needs two statements; established keys need one. Initialization consumes
no quota, and backend errors are never retried. A second initialization interrupted
by concurrent Reset/pruning returns `sdk.ErrConflict` without admission. The conflict branch samples clock_timestamp()
after obtaining that lock, once, and clamps to the previous decision on clock
rollback. Window selection and returned retry durations use this database time.
RetryAfter is a relative checkpoint at the current bucket end, not a reservation
or the earliest possible admission. Live tests cover exact-K concurrency and a
request blocked across a window boundary.

### Failure posture is the host's call

Allow inherits the caller's deadline; it sets no internal timeout. Cancellation
returns the caller error but cannot roll back a completed remote admission.

SDK middleware defaults to closed (503 for dependency/capacity errors). Hosts
can select FailOpen and use OnError to observe failures. Configuration errors
remain 500 and quota denial remains 429 with Retry-After. Authentication explicitly
opens its public-IP and refresh-session outage paths and logs those failures;
its other service policies remain unchanged. Memory can also fail at capacity.
Choose policy per route and monitor errors and latency.

### Reference DDL — host-owned

**The connector creates and migrates nothing.** Copy this into your own migration
ledger (the same scaffold-and-own rule every pocket store follows); the table
name is fixed, and keys are always bound parameters, never concatenated SQL.

Apply the reference schema before serving traffic. A missing table or column
makes admission fail; the host's middleware policy determines the HTTP result.
Verify the required schema at boot:

```go
limiter := pgxdb.NewLimiter(db)
if err := limiter.StatusCheck(ctx); err != nil {
    return fmt.Errorf("rate limiter is not usable: %w", err) // refuse to start
}
```

`StatusCheck` probes `SELECT window_ms FROM ratelimit_windows LIMIT 0` and
reports a missing table or required column as `sdk.ErrNotFound`; other failures
map through `MapError`. An undeadlined context gets one second. This is a schema
availability probe; it does not validate every stored row or test write privileges.

```sql
CREATE TABLE ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    window_ms     BIGINT      NOT NULL DEFAULT 0
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
`expires_at` is the current bucket start plus two windows, matching Redis.
`window_ms` records the normalized policy and distinguishes compatible state.
An absent key first inserts an expired, zero-count placeholder, then makes its
admission in the same locked conflict path as existing keys. A second concurrent
removal during initialization returns `sdk.ErrConflict` without consuming quota.

For an existing table, apply this host-owned migration before deploying the new
adapter:

```sql
ALTER TABLE ratelimit_windows
    ADD COLUMN window_ms BIGINT NOT NULL DEFAULT 0;
```

The zero default lets old writers retain their original insert statements. New
adapter prefixes always append `v2:`, including custom and empty prefixes; normal
old keys therefore start fresh budgets on rollout. Ensure old and new deployments
use disjoint physical key namespaces: an arbitrary old logical key beginning
`v2:` can collide with a new key. Allow and Reset reject nonpositive or out-of-range
`window_ms` as `sdk.ErrConflict` without modifying the row, even when expired.
That guard cannot identify an old writer that updates a new row while preserving
its positive `window_ms`. The connector never migrates or deletes legacy state
automatically; any intentional quota reset or legacy cleanup is host-owned.

### Schema selection on a shared pool

`WithLimiterSchema(s)` qualifies admission, reset and startup probes with the
validated schema, without changing `search_path` or opening another pool. Two
limiters on the same `*DB` can use different schemas and the same key prefix;
their windows remain independent. A missing selected schema, table or
`window_ms` column fails the startup probe with `sdk.ErrNotFound`, even when a
compatible table exists in `public` or another schema. The zero `Schema` keeps
the default unqualified behavior. Repeated options use the last schema.

For example, a host choosing `auth` applies this DDL in its own migration ledger
before application startup:

```sql
CREATE SCHEMA IF NOT EXISTS "auth";
CREATE TABLE "auth".ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    window_ms     BIGINT      NOT NULL DEFAULT 0
);
CREATE INDEX ratelimit_windows_expires_at_idx
    ON "auth".ratelimit_windows (expires_at);
```

Postgres puts this index in the table's schema. The application's database role
needs `USAGE` on the schema and `SELECT`, `INSERT`, `UPDATE`, `DELETE` on the
table. The migration role creates the schema/table/index; runtime constructors
do not apply DDL. After the migration, compose the limiter on the existing pool:

```go
schema, err := pgxdb.NewSchema("auth")
if err != nil {
    return err
}
limiter := pgxdb.NewLimiter(db, pgxdb.WithLimiterSchema(schema))
if err := limiter.StatusCheck(ctx); err != nil {
    return fmt.Errorf("rate limiter is not usable: %w", err)
}
// Supply limiter to the host's middleware and authentication configuration.
```

If the selected table already exists without `window_ms`, apply
`ALTER TABLE "auth".ratelimit_windows ADD COLUMN window_ms BIGINT NOT NULL DEFAULT 0;`
instead of recreating it. The `v2:` namespace and legacy-state rules above apply
equally to qualified tables. An already compatible table needs no DDL change.
Qualify the host's pruning statement too:
`DELETE FROM "auth".ratelimit_windows WHERE expires_at < now();`.

Selecting a new schema does not move or copy existing unqualified windows. A new
table starts empty budgets; coordinate that reset across application instances.
If preserving live quota, migrate the existing table/state under a host-owned
cutover while writers are stopped, and switch every instance to the same schema.
Keep the previous table/state until the host's rollback and retention policy
permits removal. Rolling back the option selects the old unqualified table again;
it does not transfer budgets accumulated in the selected schema.

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
host decides whether to retry them. The connector never retries a database
error; its sole extra statement initializes a missing key without charging quota.

### Pruning, write load, and retention are the host's job

Every checked request executes an upsert; a missing key also needs initialization.
Expiry stays at the current bucket
start plus two windows, so requests inside the same bucket do not keep extending
retention. Updates still create heap versions and WAL; index work and HOT
eligibility depend on which stored values change and table/page conditions.
Monitor write volume, latency and dead tuples, and tune the optional storage
parameters against host traffic.

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

For the same reason: **do not enable `DB_LOG_QUERY_ARGS` (`Config.LogQueryArgs`) on
a connection with the limiter wired in a shared environment.** With it the logging
tracer logs SQL args verbatim, so every `Allow` writes the full key — IPs included —
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

## Query logging — `LogQueries` / `LogQueryArgs`

`DB_LOG_QUERIES=true` (`Config.LogQueries`) installs a `LoggingQueryTracer`. Each
query logs a start line with `sql`, `arg_count` and `arg_types` (each argument's
Go type, `%T`) — never a value — and an end line with `command_tag`. A failed
query's end line, at ERROR, carries `sqlstate` and `constraint` for a
`*pgconn.PgError` and `error_type` (`%T`) otherwise, never the error text: pgx
encode errors embed the argument and a `22P02` message repeats the input. The
tracer logs with the query's context, so a `logging.New` logger adds `request_id`.

`DB_LOG_QUERY_ARGS=true` (`Config.LogQueryArgs`) widens the same lines with `args`
and the error text, verbatim — secrets and PII included. Without
`DB_LOG_QUERIES` it is inert (no tracer). A development `RuntimeMode` logs one
posture WARN naming every source; production refuses all of them (see
"Production posture"). A host composing its own tracer uses
`NewLoggingQueryTracerWithConfig(pgxdb.LoggingQueryTracerConfig{Logger: l,
LogArgs: false})` — `NewLoggingQueryTracer(l)` is the same safe default.

## Symmetry is convention, not a guarantee

This connector mirrors the turso connector member-for-member **by convention**.
No `make guard` row proves the two surfaces or their sentinel coverage stay
aligned; a pocket's `storetest` conformance suite is the only parity net, and
it sees only port-reachable behavior. Do not over-trust the symmetry.

Query logging is symmetric across both connectors: each carries an opt-in
`Config.LogQueries` / `Config.LogQueryArgs` / `Config.Logger` with the same
dev-only posture — argument count and types by default, values only with
`LogQueryArgs`, refused in production. pgx installs it as a native
`ConnConfig.Tracer`, turso threads it through its `DB`/`Tx` wrapper because
database/sql exposes no tracer hook (and so logs no end line).
`Config.Tracer` (and `MultiQueryTracer` above) is the one exception that remains
pgx-only: it composes an external `pgx.QueryTracer` (e.g. OpenTelemetry) into
that native seam, which SQLite's driver does not expose. This is interim
plumbing, expected to fold into a shared `sdk/capabilities/tracing` package later — until
then, hosts opt in by setting `Config.LogQueries` or `Config.Tracer`.

## Testing

Unit tests are hermetic (`MapError` over constructed `pgconn.PgError` values,
`Collect` and `ProbeTables` over in-memory `Querier` stubs, config validation,
the `RuntimeMode` posture matrix over pgx's DSN parse, the append-only and definer-function
policy and mode matrices over hand-built reports (the no-function output pinned byte for byte),
migration checksum/error paths, `poolConfig`'s
`AfterConnect`/session-zone defaulting, the timeout knobs (milliseconds applied,
zero unset, negative and sub-millisecond refused, the DSN-and-`Config` double
setting refused at `Open` in every `options` form, a DSN-only setting untouched),
and the codec registration driven through
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
its own session zone while scans stay UTC), `TestLive_Timeouts` (`SHOW` = the knob
from the startup packet, `pg_sleep` past `StatementTimeout` → `57014`, a held
`ACCESS EXCLUSIVE` lock past `LockTimeout` → `55P03`, both unmapped by `MapError`,
zero knobs sending nothing, a DSN-only setting kept, the double setting and a
sub-millisecond value refused at `Open`), the limiter legs (the shared
`ratelimitertest.Run` conformance suite, the exact-K concurrency proof — which
also bounds the returned `RetryAfter`/`Remaining`, the only place a stale
server clock is reachable — server time, burst, prefix isolation, context
cancellation, pool-survives-`Close`, `Allow`/`Reset` error-kind parity,
`StatusCheck` against a present and a missing table, shared-pool schema isolation
for admission/reset/probes with no fallback for missing schemas/tables/columns,
and the pruning statement —
the limiter legs create the reference table themselves, playing the host that
migrated it), and `TestLive_AppendOnlyProbe` (its own tables, trigger pairs and
run-unique fixture roles, each case decided both by `InspectTablePrivileges` from the
owner's connection and by `ProbeAppendOnly` as the role), and `TestLive_DefinerFunctionProbe`
(definer functions the fixture migrator creates through the recipe above in a schema whose
name needs quotes: clean, each unsafe and inoperative state, a decoy earlier on the path, a
bare-schema host resolved before the pin, a missing function, EXECUTE granted and revoked), and the
`TestLive_TransactWithSettings_*` legs (no leak on a one-connection pool after commit, rollback
and a recovered panic; bound values under the default exec mode and simple protocol; and, on a
superuser DSN only, a FORCE RLS table read and written as a run-unique `NOBYPASSRLS` role
entered with `SET LOCAL ROLE`, dropped with its schema in cleanup).
Unset, they skip loudly
(`POSTGRES_TEST_DSN not set — postgres conformance NOT verified`, plus a
banner on stderr and the controlling terminal for the limiter legs, because
`go test ./...` hides a passing package's output) — a silent green that tested
nothing is the false-green failure mode we guard against.

**`POSTGRES_TEST_DSN` must point at a throwaway/dedicated database.** The limiter
legs `CREATE TABLE ratelimit_windows`, create and drop a scratch schema, and run
an unqualified `DELETE FROM ratelimit_windows` to prove the pruning statement.
The schema legs create and `DROP SCHEMA … CASCADE` their own disposable schemas
and `CREATE ROLE`/`DROP ROLE` disposable login roles, and delete only the
`public.schema_migrations` rows they wrote. The append-only legs `CREATE ROLE` login roles
(one `SUPERUSER`) and a NOLOGIN owner, and drop every table, function and schema they created
before `DROP OWNED BY`/`DROP ROLE`. The row-level-security leg creates a run-unique schema and a NOLOGIN role, and
drops them (`DROP SCHEMA … CASCADE`, `DROP OWNED BY`, `DROP ROLE`) in cleanup.

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

## Listing projection and transaction cleanup

All List paths order the projected output of the authored SELECT. Search and
cursor helpers wrap that SELECT before adding their outer predicate. Run them before final ordering or pagination. Project every
search/order/PK field using an unqualified output name: select
`t.created_at AS created_at` and use `Column: "created_at"`. Update strict row
scanners for added projections. `OrderValueOf` must return the projected value
and type. Fixed-order expressions must use projected fields. Authored filters,
including OR and nested SELECTs, remain intact.

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

Ambient transactions are per instance. The identity of an instance is the
`*DB` one `Open` call returned; wrappers hold or embed that pointer and never
copy a `DB` by value. `d.TxFrom(ctx)` and `d.QuerierFrom(ctx)` answer only for
`d`: another instance's transaction in the context is invisible, so a store
over `d` runs on `d`'s pool beside it. `Transact` and `TransactSnapshot` refuse
nesting on the same instance with `ErrNestedTransact` (match it with
`errors.Is`, not the message); a different instance's transaction nests
independently and commits or rolls back on its own. Two databases never share a
transaction, and the connector provides no cross-database atomicity. Stores
built with `WithSchema` over one `*DB` share its identity, so one `Transact`
spans their schemas. Two `Open` calls against the same database are two
instances: nesting them can wait on a lock the outer holds, and the server
cannot detect that client-side wait, so set `LockTimeout` (`DB_LOCK_TIMEOUT`)
and `StatementTimeout` on such a pair and do not nest it. A nested request holds
one connection from each pool; nest instances in one fixed order and give
requests a deadline.

Limiter options configure private construction settings in order; the last prefix
and schema win, and the internal `v2:` suffix is appended afterward, including to
an empty prefix. They cannot change a live limiter. `NewLimiter` panics on a nil option.
`RunMigrations` rejects a nil `MigrateOption` with `sdk.ErrInvalidInput` before
accessing the database; repeated `WithSchema` uses the last schema.

### Read snapshots

`BeginRead(ctx)` starts an engine-enforced read-only transaction at repeatable-read
isolation. The first query establishes the snapshot shared by subsequent queries.
Call `Commit` on success and `Rollback` on error or panic; cleanup retains the
existing independent cancellation context. `Begin` and `Transact` retain their
existing writer semantics. Security freshness observations require a primary
connection, not a lagging read replica.

`TransactSnapshot(ctx, fn)` provides the same ambient transaction context as
`Transact`, with explicit read-write REPEATABLE READ isolation. Use it when a
workflow needs multiple reads from one snapshot together with its own pending
writes. It commits on success and retains the same error, panic and cancellation
cleanup rules as `Transact`. Both methods reject nesting on the same instance,
including nesting one inside the other. Neither retries a callback;
serialization failures return to the caller.

`Tx.SnapshotIsolation(ctx)` queries `transaction_isolation` on that transaction.
It returns true for REPEATABLE READ or SERIALIZABLE, false for weaker isolation,
and an error when isolation cannot be read. It does not infer isolation from
pool configuration or alter a transaction after work has begun. A repository
that requires a coherent multi-read result can use this check before reading
from an ambient transaction. Ordinary `Transact` continues using the server's
default isolation; callers opt into `TransactSnapshot` explicitly.

## Transaction-local settings for row-level security

`TransactWithSettings(ctx, settings, fn)` is `Transact` whose first statement is
`SELECT set_config($1, $2, true), …` with every name and value bound (pgx-escaped under simple
protocol). The settings end with the transaction, on commit, rollback or panic, and never reach
another pool borrower. It is implemented by wrapping `Transact`, so nesting (`ErrNestedTransact`
on the same instance), cleanup and the per-instance ambient context are `Transact`'s. A failure of
the first statement is wrapped as `pgxdb: applying transaction settings: …`, keeping the mapped
error. There is no snapshot variant; `TransactSnapshot` applies no settings.

Validation runs before `Begin`, so a refusal acquires no connection. Each refusal wraps
`sdk.ErrInvalidInput`: no settings; a name that is not a lowercase, dotted custom parameter
(`^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$`); a duplicate name; an empty value, a NUL byte or invalid
UTF-8. Core parameters (`role`, `search_path`, …) are undotted, so they are unreachable; extension
parameters (`plpgsql.*`) are dotted and are not blocked, so use a prefix you own. The name rule is a
guardrail, not a security boundary.

`d.TxFrom(ctx)` inside `fn` returns a `*Tx` whose `Setting(name)` reports what was applied, from
memory. It is `("", false)` on a `Transact` or `TransactSnapshot` transaction. A store over a
FORCE row-level-security table checks both, `d.TxFrom` and `tx.Setting`, and refuses otherwise,
instead of `QuerierFrom`'s pool fallback that RLS answers with a silent empty result. The docs
site's *Row-level security with pgxdb* guide covers policies, schema, roles and the host shape.
