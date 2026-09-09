# pockets/authorization/stores/firestore

The authorization pocket's **Google Cloud Firestore** (Native mode) store
adapter — its own module so a host on another datastore never pulls the
Firestore client into its module graph. It owns the collections, the documents,
and the queries; the connector
([`integrations/datastores/firestore`](../../../../integrations/datastores/firestore),
imported here as `firestoredb`) owns how to talk to Firestore; the **host** owns
the database's lifecycle and its index deployment.

It fills **all three** outbound ports, exactly as the `pgx` and `turso` siblings
do — `relationship.Storer` over `iam_relationships`, `role.Storer` over
`iam_roles`, and the atomic `mutation.MutationRepository` over `iam_scopes` +
`iam_mutations` — and passes the pocket's shared `storetest.Run` conformance
suite in full.

## ⚠️ First: this store does NOT join an ambient transaction (known family difference)

**Read this before choosing the family, not after.** It is ruling R1 of the
firestore-stores milestone, and it is a property of Firestore, not a defect or a
gap awaiting a fix.

A Firestore transaction requires **every read to precede every write** and never
observes its own pending writes. The pocket's `storetest.RunTransactional`
family proves the ambient join from *both* sides ("the ambient read sees the
uncommitted change"), which a native Firestore adapter cannot satisfy. So:

- This store returns **no `crud.Transactor`** to the conformance harness, and
  that family **skips loudly** (the harness's existing nil-transactor path,
  which the in-core `memstore` also takes). It is not faked and not wrapped.
- **Every** port method whose context carries a connector transaction
  (`firestoredb.TxFromContext` — that is, a call made inside `db.Transact` or
  `db.ReadSnapshot`) **fails loud** with
  [`ErrAmbientTransactionUnsupported`](store.go), which wraps
  `sdk.ErrInvalidInput`; `Apply`/`ApplyGuarded` additionally wrap the pocket's
  own `mutation.ErrGuardedInsideTransaction`. It never quietly runs on the
  client beside the host's transaction — a silent split of the host's atomic
  unit is the failure mode this refusal exists to prevent.
- The connector still implements `crud.Transactor` for a host's **own**
  multi-document atomicity. That seam is sdk-level and host-facing, independent
  of whether these repositories join it.

**If a host needs cross-repository atomicity** — "write my domain row and this
relationship in one unit, or neither" — it has three options, in the order they
should be considered:

1. **Use the pocket's own atomic write path.** `mutation.MutationRepository`
   (`Apply`/`ApplyGuarded`) already applies a whole authorization command, its
   guardian invariant, its revision bump and its receipt in ONE Firestore
   transaction. Cross-*repository* work is what this store refuses; atomic
   authorization work is exactly what it does.
2. **Make the host's own work idempotent and order it after the authorization
   write**, retrying on failure — the standard posture for any two systems that
   do not share a transaction. The receipt (`MutationID`) makes the
   authorization half safely repeatable.
3. **Stay on `pgx` or `turso`** if the ambient-join guarantee is genuinely
   required. It is a SQL-family property; Redis (`MULTI`/`EXEC` has no reads)
   and DynamoDB (write-only transactions) fail the same spec.

Opening READS to an ambient transaction is a possible later minor change. It is
not the shipped behavior, and nothing here degrades silently into it.

## Surface

| member | shape |
|---|---|
| `Repositories(db *firestoredb.DB, opts ...Option) (authorization.Repositories, error)` | all three ports wired (relationships, roles, atomic mutations); **probes the index manifest** against the live database first |
| `RelationshipRepository(db *firestoredb.DB, opts ...Option) (relationship.Storer, error)` | baseline-only constructor for a host that deliberately wires no mutation repository; probes the same manifest |
| `WithGuardianPolicy(p mutation.GuardianPolicy) Option` | overrides the guardian invariant (default: owner protected on every resource type, minimum one direct anchor) — mirrors the memstore and both SQL siblings |
| `WithoutIndexProbe() Option` | skips the boot probe (below). Two legitimate callers: an emulator run, and a host whose runtime credential cannot be granted `datastore.indexes.list` |
| `ExportIndexes(dst string) error` | MERGES this store's manifest into the host's own `firestore.indexes.json` |
| `IndexesFS` / `IndexesFile` | the embedded manifest, this store's analogue of the SQL siblings' `MigrationsFS`/`MigrationsDir` |
| `ErrAmbientTransactionUnsupported` | ruling R1's sentinel (wraps `sdk.ErrInvalidInput`) |
| `ErrTupleWriteLimit` / `ErrMutationWriteLimit` | the per-transaction document ceilings (wrap `sdk.ErrInvalidInput`) — see "Ceilings" |

**Kind selection is the host's wiring choice**, as with every store:
`Repositories` returns both kinds and the mutation repository; a host wanting a
single kind zeroes the other `authorization.Repositories` field after
construction. A nil kind turns that kind off structurally at
`authorization.NewService`.

## Indexes are this store's migrations

Firestore has no DDL, so there is no migration tree. Its analogue is the **index
manifest** (ruling R5): `firestore.indexes.json`, embedded in the module.

**Scaffold.** `ExportIndexes(dst)` MERGES the fragment into the host's own
manifest, creating it when absent. It merges rather than copies because a
manifest is ONE shared document (a host commonly already has one, for its own
collections and for other pockets), where migrations were append-only files.
The write is byte-stable, so re-exporting an unchanged fragment is an empty
diff and a host's unrelated indexes survive.

```go
// in the host's scaffold/generate step, not at boot
if err := authzfirestore.ExportIndexes("firestore.indexes.json"); err != nil { ... }
```

**Deploy.** The host deploys its merged manifest, exactly as it applies
migrations — pre-boot, never by the framework:

```sh
firebase deploy --only firestore:indexes          # with a firebase.json
# or, per composite, without the Firebase CLI:
gcloud firestore indexes composite create \
  --collection-group=iam_relationships \
  --database="$DATABASE" --project="$PROJECT" \
  --field-config=field-path=resource_key,order=ascending \
  --field-config=field-path=relation,order=ascending \
  --field-config=field-path=subject_key,order=ascending
```

`.github/workflows/live-stores.yml` does the gcloud form in a `jq` loop over the
manifest and then **waits for every index to be READY** — a query against a
still-building index fails the same way a missing one does.

**Probe.** `Repositories`/`RelationshipRepository` check the manifest against the
live database through the Firestore Admin API at construction time and REFUSE to
construct when an index is missing or still building, naming the collection
group, the exact field tuple, and the console page. That is this store's version
of the SQL siblings' table probe: **fail at wiring time, name the missing
thing** — because the alternative is a `FAILED_PRECONDITION` on a production
request. The probe is not retried and not degraded; it needs one IAM permission,
`datastore.indexes.list`. A host that cannot grant even that passes
`WithoutIndexProbe()` and takes ownership of deploying and verifying the
manifest itself.

**The emulator keeps no index registry and enforces no composite index**, so the
probe refuses outright there and every emulator query runs index-free. An
emulator-green suite is therefore *no evidence at all* about index coverage;
that is why the manifest's proof is a live run (see "Testing").

The entry count and the per-query derivation are in
[`SCHEMA.md`](SCHEMA.md) §9 — the manifest is the store's contract with hosts the
way `migrations/0001`–`0005` are for the SQL siblings.

## Document model

Six top-level collections (no subcollections), named after the SQL tables so the
two documentation trees and operator vocabulary stay shared:

| Collection | Holds | SQL analogue |
|---|---|---|
| `iam_relationships` | the ReBAC tuples | table |
| `iam_relationship_subjects` | one-relation-per-subject **claim** | unique index |
| `iam_relationship_ids` | the `relationship_id` **claim** | primary key |
| `iam_roles` | role assignments | table |
| `iam_scopes` | revision anchors | table |
| `iam_mutations` | mutation receipts | table |

Firestore has no unique constraints, so every SQL `UNIQUE` becomes either a
**deterministic document id** (the hash of the natural key, so a `Create`
collides) or a **claim document** read-then-created in the SAME transaction as
the row (ruling R3). Document ids are the connector's `KeyHash` — a fixed-size
hex SHA-256 over a length-prefixed tuple — because a port-legal relationship
tuple reaches 1536 bytes and any component may contain `/`, both illegal in a
document id. The original domain values are kept in fields and in everything
returned; a hash is an identity, never a projection and never a sort key. The
derived query keys (`resource_key`, `subject_key`) and the two **raw** sort keys
(`role_key`, `grant_key`, byte-identical to the SQL adapters') are documented,
field by field, in [`SCHEMA.md`](SCHEMA.md) §3–§5.

## Ceilings and costs a SQL host does not have

Everything here is a Firestore property, stated so it is chosen rather than
discovered. Full detail in [`SCHEMA.md`](SCHEMA.md) §8.

- **166 tuples per write call** (§8.1). Firestore commits at most 500 writes per
  transaction and a tuple owns three documents, so one `CreateRelationships`,
  `SetRelationTargets`, or delete changes at most 166 tuples. Past that the call
  fails with `ErrTupleWriteLimit` **before anything is written**. The operation
  is never split across transactions: a split is a partially applied batch,
  which is the opposite of what these methods promise. Bulk work is several
  batches, each atomic on its own.
- **The same 500-document ceiling bounds one mutation `Command`** (§8.3), in
  documents rather than tuples: 166 created/removed rows, 124 replaced rows, or
  a teardown's rows minus the roles it sweeps, each plus the anchor and the
  receipt. `ErrMutationWriteLimit`, again before the first write.
- **`ListEffectiveByResource` with `WithCount` is O(population)** (§8.2). The
  listing de-duplicates grants across the requested scope and the global scope,
  so its unit is a GROUP; Firestore's count aggregation counts DOCUMENTS and
  cannot group, so a counted request reads and groups both scopes in full under
  the page's own snapshot. Ordinary pages read only as far as their last group.
  The raw listings (`ListBySubject`, `ListByResource`) count documents
  server-side and pay none of this.
- **Contention surfaces as waiting** (§8.4). Commands on one scope genuinely
  serialize. The vendor re-runs a losing transaction callback up to
  `Config.MaxAttempts` (default 5); past that this store re-runs the whole apply
  a bounded number of times with a **jittered** backoff. Exhaustion is
  `sdk.ErrConflict` — an infrastructure conflict the caller may retry — never a
  committed outcome and never a minted receipt.
- **`Transact`'s callback may run more than once.** That is the connector's
  contract and it reaches through here: a guarded mutation's guard, replay check
  and evaluation all re-run on a retried attempt, and every attempt-local view,
  dependency, staged write and result is reset at the top.
- **The group expansion is an engine-side walk, not a database feature**
  (ruling R2) — Firestore has no recursive CTE, so the adapter ports the
  memstore's BFS over batched queries under ONE snapshot, with the port's
  distinct-state budget. There is **no check cache** yet. Measured on the
  emulator: a 35-wide frontier reaching 71 states costs **5 queries**
  (1 + 2 + 2 chunks); filtering 65 distinct candidates costs **6** (three
  expansion chunks + three candidate chunks) where a per-candidate
  implementation would cost 65; a descendant walk over a 31-wide frontier and
  two relations costs **5**. Depth and group width, not row count, are what a
  host should watch.
- **Search** is a client-side postfilter scoped to a parent (ruling R4). No list
  in *this* pocket is searchable, and `ListEffectiveByResource` refuses a
  non-blank `Search` with `sdk.ErrInvalidInput` rather than ignoring it.

## No CONVERSION/UPGRADE runbook — greenfield hosts start from the manifest

The pocket's [`../CONVERSION.md`](../CONVERSION.md) and
[`../UPGRADE.md`](../UPGRADE.md) (the v1 → v3 detection-and-repair and host
upgrade runbooks) are **SQL-only by nature**: they are written in DDL,
`UPDATE … WHERE`, and migration-ledger steps that have no Firestore analogue.
There is no v1 Firestore database to upgrade — this store's first tag is its
first release — so a Firestore host is greenfield by construction: export the
manifest, deploy it, boot. A host migrating data from an existing SQL
authorization database writes that one-time move itself, through the ports (the
domain semantics it must preserve — userset relations, scope revisions, the
guardian invariant — are the ones `CONVERSION.md` describes).

## Testing

```sh
# hermetic — no emulator, no network, no build tag: the derived-key parity,
# chunk arithmetic, claim/collection ownership and manifest-export tests
go test ./...

# emulator: the FULL shared conformance suite plus this store's own cases
docker run --rm -d -p 8080:8080 \
  gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators \
  gcloud emulators firestore start --host-port=0.0.0.0:8080 --project=gopernicus-test
FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 FIRESTORE_PROJECT_ID=gopernicus-test \
  go test -tags=integration -count=1 -timeout 30m ./...

# live: a run-owned disposable database with this store's indexes READY
FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
  FIRESTORE_LIVE_PROJECT_ID='<test-project>' FIRESTORE_LIVE_DATABASE_ID='<run-owned-db>' \
  GOOGLE_APPLICATION_CREDENTIALS='<sa.json>' \
  go test -json -count=1 -tags='integration,live' -timeout 30m -run 'Live$' ./...
```

**Emulator (`integration && !live`).** The suite opens the emulator database
named **`authorization`** — `firestoretest.Reset` clears a WHOLE database, so
every store train owns its own and the three Firestore suites cannot clobber one
another — and constructs with `WithoutIndexProbe()` because the emulator keeps no
index registry. `-timeout 30m` is not padding: ~130 fixtures, several of them
concurrent transaction specs, and the emulator holds a contended lock for up to
thirty seconds. Without `FIRESTORE_EMULATOR_HOST` every case skips loudly.
`TestRunTransactional` SKIPS by design (R1, above).

**What an emulator green does NOT prove**, so no green here may be read as
production evidence: it enforces **no composite index** and keeps no index
registry (the probe refuses there); its transaction behavior is not identical
and its documented locks "may take up to 30 seconds to be released"; it cannot
produce commit-contention exhaustion; it does not enforce all limits.

**Live (`integration && live`).** `firestoretest.OpenLive` +
`ResetLive(t, db, <this store's six collections>)`; a live database is never
emptied wholesale. The live conformance entrypoint constructs **with the probe
enabled** — that the shipped manifest actually serves this store's queries is
precisely what only a live run can show. Unconfigured, every root skips loudly;
`FIRESTORE_LIVE_REQUIRED=1` turns those skips into the release-gate failure.

Live roots today, and what each is for:

| root | why it is live |
|---|---|
| `TestConformanceLive` | the FULL shared suite against real Firestore with the manifest deployed and the boot probe running on every fixture |
| `TestIndexProbeAcceptsTheDeployedManifestLive` | the probe's verdict against a real Admin API index registry |
| `TestQueryMatrixExecutesAgainstTheDeployedIndexesLive` | every query shape in `SCHEMA.md` §7 executed against the deployed indexes — the manifest's actual coverage proof |
| `TestGuardedMutationDependencyRevokeIsDeterminedLive` | which side of a dependency race wins is timing on the emulator and DETERMINED on real Firestore (the aborted commit re-evaluates and denies) |
| `TestSetRelationTargetsConcurrentDisjointSetsConvergeLive` | four-caller strict convergence — the emulator's thirty-second locks would exhaust the attempt budget on timing, not serialization |
| `TestAmbientTransactionRefusedLive` | R1's refusal asserted against production's transaction behavior, not the emulator's |
| `TestRunTransactionalLive` | **the ONE allowed skip** (R1). `.github/workflows/live-stores.yml` allows it BY NAME; any other skipped root fails a required run |

From the repository root, `make test-stores` runs the emulator leg (skipping
loudly without `FIRESTORE_EMULATOR_HOST`) and `make check` vets the `integration`
and `integration,live` files compile-only, so the live leg cannot rot between
dispatches. Both CI legs — `firestore-emulator` and `firestore-live` — live in
`.github/workflows/live-stores.yml`; the live one is dispatch-only, provisions a
disposable database, deploys this module's manifest, waits for READY, and audits
the test/skip counts against the roots derived from the source.
