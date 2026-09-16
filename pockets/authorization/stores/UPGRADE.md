# Authorization schema setup

Authorization ships a fresh canonical SQL schema for PostgreSQL and SQLite/Turso.
Hosts export and apply it before constructing repositories. Framework constructors
validate the applied schema; they do not create tables or run migrations.

## Schema files

| Source | File | Creates |
| --- | --- | --- |
| Base authorization | `migrations/0001_iam_tuples.sql` | `iam_tuples`, `iam_audit`, identity constraints and lookup indexes |
| Optional TupleCache | `tuple_cache_migrations/0002_iam_tuple_cache.sql` | Protocol-2 source identity, delivery receipt, outbox and canonical capture triggers |

Use `MigrationsFS` / `MigrationsDir` or `ExportMigrations(dst)` for the base.
Use `TupleCacheMigrationsFS` / `TupleCacheMigrationsDir` or
`ExportTupleCacheMigrations(dst)` for the optional source named
`authorization-cache-v2`. Apply the base before the cache source, in the same
schema/database. Hosts merging sources into one ledger assign unique ordered
filenames; directory names alone do not establish execution order.

The cache source can be installed when creating the authority or later over
populated canonical facts. Its first publication rebuilds from current facts,
so it does not require historical insert events for rows already present.

The base contains no `iam_roles`, `iam_relationships`, revision counters or
mutation receipt tables. There is no bundled old-schema conversion, preflight,
downgrade or compatibility migration stream. Existing applications choose their
own data adoption procedure; this framework setup targets the canonical schema.

## Construct the services

1. Apply the base schema with the host's migration runner.
2. Optionally apply the TupleCache schema.
3. Construct the selected SQL adapter's `Repositories(ctx, db, ...)`. For a named
   PostgreSQL schema, apply and construct with matching schema options.
4. Pass the repositories to `authorization.New`. `Repositories.Tuples` supplies
   roles, the relationship facade and decisions. Supply a model only when policy
   needs named permissions or graph traversal.
5. If caching is enabled, construct the store with `WithTupleCache()`, supply a
   dedicated Redis namespace and a positive `MaxStaleness`, and supervise the
   returned runtime's poll loop. Constructors start no workers.

`WithAudit()` enables atomic recording; the audit table exists even when recording
is disabled. New records use `tuple/v2`. Hosts own attribution, access and retention.

## Scope and policy

`tuples.Global()` and `tuples.On(type, id)` are explicit distinct scopes; zero scope
is invalid. Full tuple identity permits independent labels for the same subject
and resource. `HasRole` is exact global membership and `HasRoleIn` is exact scoped
membership. Use an explicit `Any` expression or `HasRoleInOrGlobal` when policy
accepts either scope. Exact role reads never expand usersets.

Role and relationship writers address the same facts. Model-permitted graph
checks and guardian rules therefore see eligible facts from either facade.
Trusted writers bypass actor guards; actor-facing mutation commands authorize
inside the serialized write boundary.

## Transactions and cache lifecycle

PostgreSQL compound reads require REPEATABLE READ or SERIALIZABLE when borrowing
an ambient transaction. `pgxdb.TransactSnapshot` provides a read-write REPEATABLE
READ workflow; default READ COMMITTED returns `tuples.ErrSnapshotIsolation`.
SQLite BEGIN IMMEDIATE is suitable. Raw writes join ambient work; guarded
mutations own their transaction and reject an ambient context.

Protocol 2 uses explicit scope and `tuplecache:v2:{namespace}` Redis keys. The
source identity and delivery receipt bind a mirror to one authority. Follow the
adapter's identity/reset procedure when restoring or cloning canonical data.
An unavailable or ineligible mirror causes whole-operation durable fallback.

## Verification

Run the shared conformance suite and application allow/deny fixtures against a
disposable schema. Test fresh base installation, optional cache installation on
empty and populated facts, applied-schema validation, canonical snapshot lifetime,
atomic audit/mutations and SQL-to-Redis recovery.

- [PostgreSQL adapter](pgx/README.md)
- [SQLite/Turso adapter](turso/README.md)
- [Redis adapter](goredis/README.md)
- [Verification and benchmarks](../BENCHMARKS.md)
