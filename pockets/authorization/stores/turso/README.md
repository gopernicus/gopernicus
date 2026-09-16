# Authorization on Turso / libSQL

One canonical `iam_tuples` table supplies `Repositories.Tuples`, the relationship
facade, atomic mutations and optional audit recording. The roles service consumes
the same tuple port. There is no independent role repository or role table.
Hosts own connections and apply migrations before constructing the repositories;
constructors validate the applied canonical columns, full identity primary key,
shape/encoding constraints and byte collations before returning ports. Partial
schemas, retained legacy authorities and extra uniqueness rules are rejected.
Constructors start no workers.

```go
repos, err := turso.Repositories(ctx, db, turso.WithAudit(),
    turso.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
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
never imply global scope. Values are exact UTF-8 strings, at most 256 bytes per
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

Writes acquire SQLite's BEGIN IMMEDIATE before reading current facts.
Ambient writes retain their write intent until the host commits. Only definite
busy/locked errors acquiring the transaction are retried; a started callback or
uncertain commit is never replayed. A 503 is not proof of an aborted transaction.

The connector's ordinary `db.Transact(ctx, fn)` provides the required ambient
snapshot and preserves its pending writes. Outside a host transaction, canonical
reads open a deferred read transaction. Production reads explicitly qualify
`main`; temporary objects cannot shadow facts, audit or cache metadata.

`ReadTupleSnapshot` pins its view before entering the synchronous callback,
including when the callback's first action lets another writer commit. Bulk exact
membership, set reads and tuple pagination share that view across transport
batches. Completion errors discard provisional answers. Escaped readers fail with
`tuples.ErrSnapshotClosed`, including optimized graph readers. Callbacks must not
escape into goroutines.

Tuple lists use a versioned complete identity key in byte order. Old tuple/role
cursor encodings fail explicitly. ReadSets preserves input order and repeated
keys, and refuses an exceeded total result bound without returning partial sets.

All canonical tables live in `main`. Cache construction validates the complete
owned table and trigger definitions there. Raw `INSERT OR REPLACE` requires
`PRAGMA recursive_triggers = ON` on every writer connection to capture implicit
deletions; ordinary upserts and framework reconciliation do not require it.

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
No-op updates generate no work.

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

Hermetic tests use TempDir-owned SQLite WAL files. External integration tests
refuse to connect unless the database URL has an exact, separately supplied
disposable-fixture identity:

```sh
export TURSO_DATABASE_URL='file:/absolute/disposable/authorization-test.sqlite'
export AUTHORIZATION_TURSO_DISPOSABLE_URL="$TURSO_DATABASE_URL"
go test -tags=integration -race -count=1 ./...
```

Remote libSQL fixtures additionally require `TURSO_AUTH_TOKEN`. The opt-in must
match the complete URL; malformed URLs and insecure non-loopback endpoints are
rejected before Open/migration/deletion. Never use an application database.
Remote primary/replica routing remains a deployment-specific verification.

The suites cover canonical identity, cross-facade audit/integrity, snapshots,
ambient savepoint recovery, transport batches, keyset pagination, fresh schema
installation, applied-schema validation and cache protocol binding.
