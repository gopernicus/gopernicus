# Firestore authorization store

This module implements the authorization pocket's relationship, role and atomic
mutation repositories on Google Cloud Firestore Native mode. It also supplies
an optional transactional change history and its reader. The adapter owns the
storage schema and queries; the [Firestore connector](../../../../integrations/datastores/firestore)
owns vendor access. Hosts own database lifecycle, credentials and index deployment.

## Wiring

```go
repos, err := authorizationfirestore.Repositories(ctx, db,
    authorizationfirestore.WithAudit(), // optional; off by default
    authorizationfirestore.WithGuardianPolicy(policy), // optional; empty by default
)
```

`Repositories` returns `Relationships`, `Roles`, `Mutations`, and `Audit`.
`RelationshipRepository` constructs only the relationship port with the same
options. A host selects authorization kinds when wiring its service. Guardian
policy is copied at construction and enforced by atomic mutation commands.

Both constructors take the host startup context first. Each index probe honors
cancellation and the earlier of the host deadline or `firestoredb.ProbeTimeout`
(30 seconds). A canceled context also rejects construction with
`WithoutIndexProbe()`. The context is used only during construction; the host
retains ownership of the database.

| API | Purpose |
|---|---|
| `WithAudit()` | record actual tuple and role changes atomically with writes |
| `WithGuardianPolicy(policy)` | explicitly configure guardian invariants |
| `WithoutIndexProbe()` | host assumes index verification; required on the emulator |
| `ExportIndexes(path)` | merge baseline index definitions into a host manifest |
| `ExportAuditIndexes(path)` | merge optional history index definitions |
| `IndexesFS`, `IndexesFile` | embedded baseline manifest |
| `AuditIndexesFS`, `AuditIndexesFile` | embedded history manifest |
| `UpgradeTupleStorage(ctx, db)` | explicit natural-tuple metadata upgrade |
| `RemoveLegacyMutationStorage(ctx, db)` | explicit retired receipt/revision cleanup |

Constructors never deploy indexes or migrate stored data. Follow
[UPGRADE.md](UPGRADE.md) when replacing an older adapter.

## Transaction behavior

Each authorization write is atomic. Guards, current-model validation, guardian
checks, affected fact reads and optional audit writes share a native Firestore
transaction. All reads precede writes. Native transaction isolation protects
both existing facts and negative predicates; there is no scope counter or
anchor collection. Every command is evaluated against current state, including
natural no-ops. Successful `mutation.Result` values describe that application.

There are no caller-supplied mutation IDs, durable receipts or replay promises.
Only definite aborted transactions retry internally. A guard or semantic
validator refusal preserves its original error identity and stays terminal.
Exhausted contention matches `mutation.ErrConcurrentMutation`; unavailable,
deadline and unknown commit failures return without replay. A caller repeating a
command after a lost reply must account for intervening state changes.

The store rejects ambient connector transactions, including read snapshots,
with `ErrAmbientTransactionUnsupported` (`sdk.ErrInvalidInput`). Atomic mutation
calls also match `mutation.ErrGuardedInsideTransaction`. Firestore cannot observe
its pending writes or issue reads after writes, so these repositories cannot
provide the SQL adapters' ambient join contract. The connector still supports
transactions over a host's own documents. Hosts requiring one transaction across
authorization and application repositories should select a compatible SQL store.

## Optional change history

`WithAudit()` applies to every raw and guarded write port from the constructor.
Recording is off by default. When enabled, raw and trusted callers must attach
an explicit actor pair or system source:

```go
ctx = authorization.WithAuditSource(ctx, authorization.AuditSource{
    System: "migration",
    Reason: "import access assignments",
})
```

Guarded service methods use their validated actor and may retain a valid reason.
Source metadata grants no authority. Enabled stores validate it even for no-op
attempts. For complete history, hosts must route writers through repositories
with recording enabled; direct database changes bypass this feature.

The store writes one audit record per actual fact addition or removal. A
replacement records both; duplicates, absent removals, refusals and failed
attempts produce none. Each operation's records share a freshly generated event
ID. Full tuple identity, including userset relation, is preserved. Audit and
facts commit together; an audit failure rolls back the facts. Event IDs group
records and have no durable idempotency meaning.

`repos.Audit.List` remains usable when new recording is disabled. It supports
optional complete resource, subject and actor pairs in any combination, ordered
by `occurred_at` and record ID (DESC by default, ASC supported), with standard
cursor, previous-page, offset and count behavior. Timestamps are event creation
time within the committed attempt, normalized to UTC microseconds. Hosts own
history access control, retention, export and presentation; no audit HTTP route
or TTL is installed.

## Index deployment

The baseline `firestore.indexes.json` has 28 composite indexes and the required
single-field index declarations. `firestore.audit.indexes.json` adds 16 optional
history indexes for all filter combinations and both directions. Its field
overrides avoid indexing payload and reason fields unnecessarily.

Merge the required fragments into the host's manifest, deploy them, and wait
until they are ready before boot. `Repositories` and `RelationshipRepository`
probe baseline indexes; `WithAudit()` adds the history probe. A host reading old
history with recording disabled still needs the history indexes deployed.
`WithoutIndexProbe()` is appropriate for the emulator or a runtime credential
without index-list permission when the host verifies deployment separately.
The export helpers preserve unrelated host entries and do not deploy anything.

The emulator has no composite index enforcement or usable index registry.
Hermetic query-matrix checks validate the manifest's declared shapes; a real
Firestore run is needed to prove deployed coverage. See [SCHEMA.md](SCHEMA.md)
for exact fields, index shapes and ordering contracts.

## Capacity and costs

The adapter never splits a transaction to fit backend limits. Firestore's native
request-size limit applies to all fact, subject-claim and audit writes together;
an oversized commit fails atomically. Each tuple create/delete affects two fact
and claim documents; a replacement affects three. Audit adds one document per
actual fact change. Per-fact audit documents avoid one large event document's
1 MiB size ceiling, but the whole transaction remains bounded by the backend.

Tuple and role listings use natural order without stored IDs or fact timestamps.
The tuple's private two-part sort key preserves complete natural ordering while
keeping indexed values below 1500 bytes. Effective-role counts group all matching
scoped/global rows and cost O(population) reads. Graph reads use one snapshot,
with bounded expansion returning an error rather than a partial decision.

## Verification

From this module, using the repository's configured Go cache:

```sh
go build ./...
go test ./...
go vet ./...
FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 go test -race -tags=integration -timeout=15m ./...
go test -tags='integration,live' -run '^$' ./...
```

Integration fixtures use the isolated `authorization` named database. The suite
includes shared authorization and audit conformance, raw and guarded actual
deltas, no-ops, source validation, audit and native-size rollback, pagination,
legacy cleanup, callback identity, and guard races against raw writers. The
ambient join family explicitly skips; separate tests verify the refusal.

Real Firestore tests require the connector's disposable live-test configuration
and deployed indexes. They are skipped when that configuration is absent. The
emulator does not substitute for production index or concurrency-mode coverage.
