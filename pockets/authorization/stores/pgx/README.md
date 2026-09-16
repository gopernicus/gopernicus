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
repos, err := pgx.Repositories(ctx, db, pgx.WithAudit())
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

## Writes, guards and audit

`ApplyTuples` applies one validated add/remove delta atomically. `ReconcileTuples`
and `SetRelationTargets` replace only one scope/relation set and preserve every
other label. Resource deletion/teardown sees all facts, regardless of which
facade wrote them. Trusted raw writes join ambient transactions; request-facing
writes should use the guarded mutation service.

Mutation guards, current-model validation, guardian checks, actual changes and
audit share one write boundary. Guardian rules apply to role writes and no-op
attempts too. Explicit batch changes replace the retired ambiguous Replace
operation. Rejected operations return an error and nil result; successful results
contain only the current Outcome. Guarded mutation commands reject ambient host
transactions. There are no replay tokens, revisions or durable operation receipts.

`WithAudit()` requires a valid source even for no-op writes. One actual canonical
addition/removal produces one `audit.Change{Action, Tuple}`, encoded `tuple/v2`.
A fact written through both facades is recorded once. Events group changes within
one call; UTC microsecond timestamps are not commit-order watermarks. Atomic swaps
record both removal and addition. No-ops, refusals and rollbacks record nothing.
Ambient failures roll back an operation savepoint, preserving unrelated host work.

`repos.Audit.List` reads canonical `tuple/v2` records. Recording defaults off;
existing canonical history remains readable. Hosts own audit access, retention
and export. Raw SQL outside recording-enabled adapters is outside audit coverage.

## Concurrency and snapshots

PostgreSQL mutation-owned transactions use READ COMMITTED and lock `iam_tuples`
with SHARE ROW EXCLUSIVE before reading guards or current facts. Raw writes take
the same lock. It conflicts with ordinary SQL writers, including absent-row
predicates, but permits ordinary readers. This deliberately serializes writers
per schema. Ambient writes keep their locks until the host commits.

Trusted owned operations retry only definite PostgreSQL serialization/deadlock
aborts (40001/40P01). Guard callbacks are never replayed. Unknown commit/transport
outcomes are never automatically retried. Hosts own retries for ambient work.

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

The suites cover canonical identity, cross-facade audit/guardians, snapshots,
ambient savepoint recovery, transport batches, keyset pagination, fresh schema
installation, applied-schema validation and cache protocol binding.
