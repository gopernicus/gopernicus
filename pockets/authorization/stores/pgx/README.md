# Authorization on PostgreSQL

This adapter supplies relationship, role, atomic mutation and audit-reader ports
from one store. Hosts own database connections and apply the complete
`authorization` migration source before construction. The framework does not
migrate at boot.

```go
repos, err := pgx.Repositories(ctx, db, pgx.WithSchema(schema), pgx.WithAudit())
if err != nil {
    return err
}
ctx = authorization.WithAuditSource(ctx, authorization.AuditSource{
    System: "document-sync",
    Reason: "project current document access",
})
err = repos.Relationships.CreateRelationships(ctx, tuples)
```

Use `WithAudit()` only when new changes should be recorded. The default is off;
`repos.Audit` still lists previously retained history. `WithGuardianPolicy(policy)`
installs explicit relationship invariants; its default is empty.
`GuardianPolicy()` returns a defensive snapshot and `authorization.New`
validates configured rules against the host relationship model.

`RelationshipRepository(ctx, db, opts...)` supports baseline-only host composition
without constructing a mutation repository. It accepts the same store options.
It probes both fact tables because writes lock both; WithAudit also requires iam_audit.

## Writes and history

Raw relationship and role methods are trusted state-writing ports. They join an
ambient connector transaction or own a transaction. `SetRelationTargets` applies
one desired relation set atomically; a conflicting existing relation returns
`sdk.ErrConflict` and preserves the previous set. Raw relationship creates retain
an existing subject relation on conflict. Atomic commands apply current model
validation, optional guards and configured guardian invariants on every call.

Successful commands return `mutation.Result` containing the current Outcome and
SameRoleGrantRemains annotation. Semantic and invariant refusals return an error
and a nil result. There are no durable operation IDs, receipts, replay tokens,
expected revisions or revision counters. Repeating a command evaluates current
state and policy; natural tuple duplicates remain no-ops.

With recording enabled, every supported raw or command write requires a valid
source before work, including no-op attempts. Supply either an actor type/ID pair
or an explicit system name, with an optional bounded reason. Actor-facing service
methods replace attribution with their validated actor. Source metadata never
confers permission.

Each actual added or removed tuple/role produces one `audit.Record`; EventID
only groups the records of a changed call. Records preserve the complete six-field
relationship identity or five-field role identity. Replacements record removal
and addition, broad deletes record every removed fact, and resource teardown
includes scoped roles. No-ops, refused operations and rollbacks record nothing.
Timestamps use UTC microseconds and describe when the operation records its
changes, rather than a separate commit-order sequence.

Facts and their audit records use the same transaction. Ambient operations use a
savepoint: an audit failure rolls back that operation even when the host handles
its error and commits other work. Failure to restore the savepoint aborts the
host transaction. Multiple calls in one host transaction produce separate event
groups that commit or roll back together. Guarded/command methods refuse an
ambient transaction with `ErrGuardedInsideTransaction`.

`repos.Audit.List(ctx, audit.Filter{...}, list.Request{...})` supports exact
optional resource, subject and actor pairs, standard cursor/offset pagination
and totals. Each filter pair is both-or-neither. Ordering defaults to
`occurred_at DESC` with record ID as the tiebreak; ASC is also supported. Hosts
own access control, retention, presentation and export. No audit HTTP route is
installed. Direct database edits and writes through a separately constructed
store with recording disabled are outside recorded coverage.

## Concurrency

Every mutation-owned transaction explicitly uses READ COMMITTED and takes
`SHARE ROW EXCLUSIVE` locks on the schema-qualified relationship table followed
by the role table, before guard or state reads. Raw writes take the same locks.
These locks also conflict with ordinary SQL INSERT/UPDATE/DELETE, so negative
membership checks, nested usersets and global-role reads stay stable through
commit without revision anchors. Ordinary SELECTs remain concurrent.

This deliberately serializes authorization writers per schema. An ambient host
transaction retains the locks until its own commit; keep such workflows short.
The adapter preserves an ambient transaction's isolation level and captures its
actual INSERT/DELETE RETURNING rows. Host transactions that already acquired
locks in another order can deadlock. Their caller owns rollback/retry. Owned
trusted writes have bounded, cancellation-aware retries for PostgreSQL's definite
40001/40P01 transaction aborts; guarded writes report
`mutation.ErrConcurrentMutation` without rerunning the guard. Unknown commit or
transport failures never replay internally.

## Reads and tuple identity

Relationships use the full tuple as their SQL primary key:
resource type, resource ID, relation, subject type, subject ID, subject relation.
Roles retain their natural five-field key. Neither fact type stores a surrogate
relationship ID or CreatedAt. The independent one-relation-per-exact-subject
constraint remains; distinct userset relations remain distinct subjects.

Relationship lists order by `tuple_key ASC`; role lists by `role_key ASC`.
Keys join canonical fields with U+0001, which validation forbids inside a field.
PostgreSQL ordering expressions explicitly use COLLATE "C".
Model-bound readers filter persisted facts through the current host model.
Recursive userset expansion retains exact userset relation identity, detects
cycles and enforces the same expansion-state budget in ordinary and guarded
reads. Physical scan cost remains planner-dependent.

## Migration and deployment

Export the canonical files with `ExportMigrations(dst)` and apply them using the
host's runner. Fresh databases apply 0001 through 0007. Keep earlier deployed
migration files unchanged.

0006 removes relationship IDs and fact timestamps, preserving full tuple keys.
It rejects legacy U+0001 separators before dropping metadata; repair those
records explicitly and rerun. 0007 drops `iam_scopes` and `iam_mutations`, creates
`iam_audit` and its time/resource/subject/actor listing indexes. Old receipts do
not contain enough information to reconstruct actor-attributed fact history;
the migration does not invent it.

Stop old writers before upgrading. Archive legacy receipt/revision tables first
if needed, apply the complete migration source, then deploy the new binary.
An old binary cannot operate against the new schema. Rollback requires a
compatible backup or a deliberate host migration; do not resume old writers
against partially upgraded tables.

WithSchema(schema) qualifies every runtime table and audit query. Apply migrations with the matching pgxdb.WithSchema(schema); constructor probes catch missing tables before serving requests.

## Verification

Set POSTGRES_TEST_DSN and run `go test -race -count=1 ./...`. Repeat with POSTGRES_TEST_SCHEMA to exercise a named schema. The optional non-C locale proof requires its documented locale fixture.
Live tests include shared fact/mutation/audit conformance, exact userset deltas,
ambient savepoint recovery, retained readers, pagination and populated upgrades.
