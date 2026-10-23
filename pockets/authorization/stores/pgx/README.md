# Authorization on PostgreSQL

One canonical `iam_tuples` table supplies `Repositories.Tuples`, the relationship
facade, atomic mutations and optional audit recording. The roles service consumes
the same tuple port. There is no independent role repository or role table.
Hosts own connections and apply migrations before constructing the repositories;
constructors validate the applied canonical columns, full identity primary key,
shape/encoding constraints and byte collations before returning ports. Partial
schemas, retained legacy authorities and extra uniqueness rules are rejected.
Constructors start no workers.

```go
repos, err := pgx.Repositories(ctx, db, pgx.WithAudit(),
    pgx.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
if err != nil { return err }
ctx = audit.WithSource(ctx, audit.Source{System: "access-sync"})
err = repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{
    {Scope: tuples.On("document", "d1"), Relation: "owner",
      Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
}})
```

## Identity and policy

Full identity is `(scope_kind, resource_type, resource_id, relation,
subject_type, subject_id, subject_relation)`. Global scope is explicitly
`tuples.Global()` (SQL kind 1, empty resource coordinates); a resource scope is
`tuples.On(type, id)` (kind 2, both coordinates required). Missing coordinates
never imply global scope. Both `iam_tuples` and `iam_audit` store `scope_kind` as
`SMALLINT`. Reference values are exact UTF-8 strings, at most 256 bytes per
field and free of control characters. No normalization is performed.

Multiple labels can coexist for one subject/resource. Exact duplicate adds and
absent removes are idempotent. A concrete subject and its `#member`/`#admin`
usersets remain distinct. Global usersets are structurally valid facts; global
scope never invents a resource node or implicit graph traversal.

`HasRole` checks exact global concrete membership. `HasRoleIn` checks exact
resource membership. Global fallback must be explicitly composed, for example
`HasRoleInOrGlobal` or a decisions expression. A graph model is optional. Models
filter graph reads and compose exact role predicates through the one decisions
service. Neither roles nor named permissions are separately cached decisions.

## Writes, integrity and audit

Inbound authorizes the exact command. The principal-free mutation service applies
shape validation, configured integrity, changes and audit under one serialized
write boundary. It does not invoke a principal guard or decision service.
`WithIntegrityPolicy(mutations.IntegrityPolicy{Rules: ...})` installs data rules;
the default is empty. `DefaultIntegrityPolicy()` explicitly requires one concrete
`owner` per resource. Zero `MinSubjects` means one; an empty `ResourceType` matches
all resource types. Usersets cannot satisfy a concrete-subject minimum.

Every ordinary writer honors this policy: atomic commands, role/relationship
facades, raw `ApplyTuples`, reconciliation, scope deletion and natural no-ops.
`ApplyTuples` applies an add/remove delta atomically. `ReconcileTuples` and
`SetRelationTargets` replace only one scope/relation set. Seed all required
subjects together when establishing a protected scope. Only the explicit
`TeardownResourceAuthorization` command may remove the final protected facts;
ordinary purge and `DeleteScope` return a conflict instead.

Raw writes join supported ambient transactions. Atomic mutation commands own
their boundary and reject ambient contexts. Rejected operations return an error
and nil result; successful results contain only the current `Outcome`. There are
no principal-guard callbacks, replay tokens, revisions or durable operation receipts.
A permission revoked after inbound admission does not retract an admitted command.

`WithAudit()` requires a valid source even for no-op writes. One actual canonical
addition/removal produces one `audit.Change{Action, Tuple}`, encoded `tuple/v2`.
A fact written through both facades is recorded once. Events group changes within
one call; UTC microsecond timestamps are not commit-order watermarks. Atomic swaps
record both removal and addition. No-ops, refusals and rollbacks record nothing.
Ambient failures roll back an operation savepoint, preserving unrelated host work.

`repos.Audit.List` reads canonical `tuple/v2` records. Recording defaults off;
existing canonical history remains readable. Hosts own audit access, retention
and export. Raw SQL outside these adapters is outside integrity and audit coverage.
Every adapter instance writing this authority must use the same integrity policy.

## Concurrency and snapshots

PostgreSQL mutation-owned transactions use READ COMMITTED and lock `iam_tuples`
with SHARE ROW EXCLUSIVE before reading current facts. Raw writes take
the same lock. It conflicts with ordinary SQL writers, including absent-row
predicates, but permits ordinary readers. This deliberately serializes writers
per schema. Ambient writes keep their locks until the host commits. A stale
REPEATABLE READ/SERIALIZABLE view cannot satisfy integrity using outdated facts;
the write returns `ErrConcurrentMutation` and leaves the host to retry.

Owned operations retry only definite PostgreSQL serialization/deadlock
aborts (40001/40P01). Model validation is pure and may run again after an abort.
Unknown commit/transport outcomes are never automatically retried. Hosts own retries for ambient work.

Use `db.TransactSnapshot(ctx, fn)` for host workflows making compound authorization
reads: it is read-write REPEATABLE READ and sees pending writes. Ordinary
`db.Transact` retains its normal isolation. Canonical compound reads inspect an
ambient transaction's actual isolation and reject READ COMMITTED/READ UNCOMMITTED
with `tuples.ErrSnapshotIsolation` before invoking the callback. REPEATABLE READ
and SERIALIZABLE are accepted.

`ReadTupleSnapshot` pins its view before entering the synchronous callback,
including when the callback's first action lets another writer commit. Bulk exact
membership, set reads and tuple pagination share that view across transport
batches. Completion errors discard provisional answers. Escaped readers fail with
`tuples.ErrSnapshotClosed`, including optimized graph readers. Callbacks must not
escape into goroutines.

Tuple lists use a versioned complete identity key in byte order. Old tuple/role
cursor encodings fail explicitly. ReadSets preserves input order and repeated
keys, and refuses an exceeded total result bound without returning partial sets.

`WithSchema(schema)` qualifies every runtime table. Apply migrations with the
matching `pgxdb.WithSchema(schema)`. Cache construction resolves and freezes the
actual schema even for a search-path constructor. It rejects mixed schemas,
RLS, inheritance, temporary/unlogged facts, altered or disabled capture triggers.
Canonical reference columns and recursive CTE anchors use COLLATE "C", keeping
identity and order byte-exact under non-C database locales.

## Schema setup

The fresh base is `migrations/0001_iam_tuples.sql`. It creates `iam_tuples` and
`iam_audit` directly, with their constraints and indexes. Export it using
`ExportMigrations(dst)` or apply `MigrationsFS` / `MigrationsDir` through the
host's pre-boot migration runner.

`Repositories` also requires the operation ledger from the same source:
`0003_iam_operations.sql` creates `iam_operations` and
`0004_iam_operations_fenced.sql` widens its outcome check for fences. 0004 is a
one-way cutover: older adapter versions refuse the widened schema, and there is
no downgrade once a fenced record exists. Apply 0004 in its own migrate run with
a lock timeout (`options=-c lock_timeout=5s`): its ALTER takes ACCESS EXCLUSIVE
and can stall every authorization write while it waits; see the [deploy order
and lock guidance](../UPGRADE.md). `Repositories` populates both `Mutations` and
`Operations`; `RelationshipRepository` needs neither table.

**v0.20.0: 0005 is required; 0006 makes the audit tables append-only.**
`0005_iam_audit_integrity.sql` adds `request_id`, `trace_id`, `seq` and
`recorded_at` to `iam_audit` and `recorded_at` to `iam_operations`. The store
writes the two ids; the database stamps `seq` and `recorded_at`, which stay NULL
on earlier rows. The file sets its own `lock_timeout` and ends with `GRANT USAGE
ON SEQUENCE iam_audit_seq TO PUBLIC`, so a runtime role holding table privileges
only keeps inserting. `Repositories` and any store built `WithAudit()` refuse a
database without 0005, and v0.19.0 refuses one with it: run v0.19.1 everywhere
first, migrate 0005, then repin v0.20.0. After 0005, roll back only to v0.19.1.

`0006_iam_audit_immutable.sql` requires PostgreSQL ≥ 14. It installs
`<table>_immutable` (BEFORE UPDATE OR DELETE, per row) and `<table>_no_truncate`
(BEFORE TRUNCATE, per statement), both `ENABLE ALWAYS`, on `iam_audit` and
`iam_operations`. `iam_audit` refuses every change; `iam_operations` refuses
UPDATE, TRUNCATE and the DELETE of a fenced row. Members of the role that owns
the table, including a SECURITY DEFINER function it owns, are exempt. A refused
write is SQLSTATE 42501, `sdk.ErrForbidden`. In production `Repositories` then
runs the append-only posture probe over `AuditTables(opts...)` and refuses a
runtime role that can modify or cannot append to those tables; a host whose
migrations and server share one role must split them. See the
[upgrade guide](../UPGRADE.md) and
[pgxdb](../../../../integrations/datastores/pgxdb/README.md#append-only-tables-the-posture-probe).

**v0.21.0: 0007 is admitted, not required.** `0007_iam_operations_expiry.sql`
builds `idx_iam_operations_committed` (`committed_at, operation_id`) with
`CREATE INDEX IF NOT EXISTS` under its own `lock_timeout`. `Repositories` sets
`OperationExpiry`, whose `Expire` deletes the oldest expired non-fenced records
in one statement on the pool, without the authorization lock or a row lock, so
the runtime role needs DELETE but not UPDATE. Without the index (or with an
invalid one) `Repositories` logs one WARN on `slog.Default`,
`index_missing=idx_iam_operations_committed file=0007_iam_operations_expiry.sql`;
pre-build it `CONCURRENTLY` on a large ledger as the [upgrade guide](../UPGRADE.md)
describes.

TupleCache adds one optional file,
`tuple_cache_migrations/0002_iam_tuple_cache.sql`, exposed by
`TupleCacheMigrationsFS` / `TupleCacheMigrationsDir` and
`ExportTupleCacheMigrations(dst)`. Its source name is `authorization-cache-v2`.
Apply the base first, then this source in the same schema/database. It can be
installed over populated canonical facts; the first relay publication loads
current facts into the mirror.

There is no legacy migration prefix or bundled conversion/downgrade procedure.
For ordered host setup and construction, see the [schema setup guide](../UPGRADE.md).

## Optional TupleCache

`Repositories(ctx, db, WithTupleCache())` exposes `TupleSource` and matching
`TupleCacheBinding` on the canonical reader. Global, scoped, concrete and userset changes all enter the
same outbox, including ordinary SQL writes and writers without cache/audit options.
No-op updates generate no work. PostgreSQL TRUNCATE requests a full rebuild.

Delivery snapshots include one consistent receipt, exact pending event IDs and
current canonical facts for a rebuild. Changed/missing receipts force a rebuild;
processed outbox rows are disposable work. After successful atomic Redis
publication, acknowledgement compares the old receipt and deletes only captured
IDs. Sequence maxima are never commit watermarks. Later commits survive.

Ambient contexts bypass Redis and use canonical durable snapshots with pending
writes. Delivery `Snapshot` and `Acknowledge` reject ambient transactions. Relay
lifecycle, primary routing, freshness policy and client lifecycle remain host-owned.
A new protocol requires a fresh Redis namespace; protocol1 bytes cannot be used.

Before trigger-bypassing imports, restore or cloning, stop/drain readers and
relays. Rotate the store's 32-character lowercase-hex identity, clear its receipt,
reconstruct repository bindings, and rebuild Redis before resuming readers.

## Verification

Use an explicitly disposable local database:

```sh
POSTGRES_TEST_DSN='postgres://fixture@localhost:5432/authorization_test?sslmode=disable' go test -race -count=1 ./...
```

Repeat with `POSTGRES_TEST_SCHEMA=authorization_test` for schema qualification and
`POSTGRES_NON_C_TEST_DSN` pointing at a disposable non-C UTF-8 database for the
collation proof. Tests include a million-row query-plan measurement.

The suites cover canonical identity, cross-facade audit/integrity, snapshots,
ambient savepoint recovery, transport batches, keyset pagination, fresh schema
installation, applied-schema validation and cache protocol binding.
