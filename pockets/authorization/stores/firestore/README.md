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
migrations — pre-boot, never by the framework. The manifest has **two halves and
both must be deployed**: the `indexes` (composites) and the `fieldOverrides` (the
single-field indexes the composite-free shapes read through). Deploying only the
composites leaves the boot probe failing on a field the manifest declares — which
reads like a store bug and is an undeployed manifest.

```sh
firebase deploy --only firestore:indexes          # with a firebase.json, both halves

# or, without the Firebase CLI — one call per composite:
gcloud firestore indexes composite create \
  --collection-group=iam_relationships \
  --database="$DATABASE" --project="$PROJECT" \
  --field-config=field-path=resource_key,order=ascending \
  --field-config=field-path=relation,order=ascending \
  --field-config=field-path=subject_key,order=ascending

# and one call per field override. `--index` OVERWRITES that field's index set
# and deletes anything it omits, so pass every index the manifest declares for it:
gcloud firestore indexes fields update resource_key \
  --collection-group=iam_relationships \
  --database="$DATABASE" --project="$PROJECT" \
  --index=order=ascending
```

`.github/workflows/live-stores.yml` does the gcloud form in `jq` loops over the
manifest — composites **and** field overrides — and then waits for **both**:
every composite READY (a query against a still-building index fails the same way
a missing one does) and every declared field configuration applied and not
`CREATING`. The two waits are separate because `indexes composite list` never
returns single-field indexes; that is what makes the live leg evidence for the
manifest as a WHOLE. On a **pinned** (re-used) live database nothing prunes what
a manifest no longer declares — sweep stale entries by hand with `gcloud
firestore indexes composite list --database=<pin>` / `indexes composite delete`,
and `gcloud firestore indexes fields list --database=<pin>` / `indexes fields
update <field> --collection-group=<cg> --clear-exemption`, or they keep consuming
the per-database caps (200 composite indexes and 200 single-field configurations
without billing enabled, 1000 each with; the second is shared with TTL policies).

**Probe.** `Repositories`/`RelationshipRepository` check the manifest against the
live database through the Firestore Admin API at construction time and REFUSE to
construct when an index is missing or still building, naming the collection
group, the exact field tuple, and the console page. That is this store's version
of the SQL siblings' table probe: **fail at wiring time, name the missing
thing** — because the alternative is a `FAILED_PRECONDITION` on a production
request. The probe is not retried and not degraded; it needs one IAM permission,
`datastore.indexes.list`.

**Who passes `WithoutIndexProbe()`** — the same three callers `RELEASING.md`
names, and no fourth:

1. **the emulator**, which keeps no index registry, so the probe refuses there
   outright (this store's emulator suite passes it);
2. **a credential that cannot be granted `datastore.indexes.list`** — a runtime
   service account a host will not widen;
3. **a deployment that must boot while the Admin API is degraded**, because the
   probe is a HARD BOOT DEPENDENCY on an API the request path never touches.

All three take ownership of deploying and verifying the manifest themselves.

**What the probe costs at boot.** One Admin `ListIndexes` for the composites plus
one `GetField` per declared field override with a non-empty index list (an
override declaring an empty list issues no RPC). Both pockets mounted in one host
is therefore roughly **2 + N Admin RPCs before the first request**, under **two**
independent `firestoredb.ProbeTimeout` budgets of 30 s — the constructors take no
context, so each store bounds its own probe. A host that would rather pay that at
DEPLOY time than at boot can:

```go
// deploy step / preflight job — not the request path
if err := firestoredb.ProbeIndexesFS(ctx, db, authzfirestore.IndexesFS, authzfirestore.IndexesFile); err != nil { ... }

// boot
repos, err := authzfirestore.Repositories(db, authzfirestore.WithoutIndexProbe())
```

That keeps the manifest checked against the real database and takes the Admin API
out of the boot path; what it gives up is the guarantee that the database a
process actually connects to is the one that was checked.

**The emulator keeps no index registry and enforces no composite index**, so the
probe refuses outright there and every emulator query runs index-free. An
emulator-green suite is therefore *no evidence at all* about index coverage;
that is why the manifest's proof is a live run (see "Testing").

The manifest declares two things, and the probe checks both: the **composite
indexes** the multi-filter and ordered queries need, and the **field overrides**
that pin the single-field indexes the composite-free shapes read through (the
expansion hop, the whole-resource reads, the role sweep). Declaring the latter is
what makes them checkable — the probe validates only what the manifest states, so
an undeclared dependency on Firestore's automatic single-field indexing is an
unchecked one, and an emulator run cannot notice. Note that declaring a field
override REPLACES that field's default index set; `SCHEMA.md` §9.5 says why that
is safe here.

The entry counts and the per-query derivation are in
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

### Those six collection ids are RESERVED by this store

This store owns the six ids above outright — it creates, queries, resets and
sweeps them, and its claim collections encode uniqueness only its own writes
maintain, so a host document under one of those ids is not "extra data", it is a
row this store believes it owns.

It reaches further than the top level, because a **field override is
database-wide for a collection-group id, at any depth**. A `fieldOverrides` entry
naming `iam_roles` configures the single-field indexing of `iam_roles`
*everywhere in the database* — a host's own `orgs/{id}/iam_roles` subcollection
would inherit it. The manifest cannot scope it more narrowly; that is how
Firestore field configuration works, not a choice this store made.

**So: this store expects a database that does not share those six names**, as a
top-level collection or as a subcollection id anywhere. Give it its own database
(the cheapest answer, and what CI does per run), or rename the host's colliding
collections. There is no prefix option; the ids deliberately mirror the SQL table
names so the two documentation trees and the operator vocabulary stay shared.

## Ceilings and costs a SQL host does not have

Everything here is a Firestore property, stated so it is chosen rather than
discovered. Full detail in [`SCHEMA.md`](SCHEMA.md) §8.

- **A batch is bounded by the request, not by a write count** (§8.1, §8.3).
  Firestore publishes **no** per-transaction write COUNT limit: the quotas page
  bounds a commit by the 10 MiB maximum API request size and by 500 field
  transformations *per document*. This store therefore enforces no ceiling of its
  own, and it never splits a call — a split is a partially applied batch, which
  is the opposite of what these methods promise. An oversized request is refused
  by the SERVER, **atomically**: nothing is written, and the caller sends less in
  one call. A relationship tuple costs three documents (row + two claims) and a
  replaced row four, which is what a large command's request size is made of.
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
- **Reads per expanded check, measured** (`TestCheckReadBudgetScalesWithTheGrantsOfThePrincipal`).
  For the shape that dominates — a principal holding N direct grants and
  belonging to no group — one `CheckRelationWithGroupExpansion` reads
  **N + 1 documents**: the principal's own N grants (the first expansion hop
  reads every row where it is the subject) plus the one matching row. The query
  count is `1 + ceil(N/30)` for the hops plus the chunks the final match scans,
  each `Limit(1)`. Measured on the emulator:

  | direct grants of the principal | queries | document reads |
  |---|---|---|
  | 1 | 3 | 2 |
  | 100 | 7 | 101 |
  | 1000 | 52 | 1001 |

  The cost is linear in the PRINCIPAL's grants, not in the resource's, and it is
  paid on every check because there is no cache yet. A principal with thousands
  of direct grants is the shape to watch.
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
emptied wholesale. The live leg makes **one probe-enabled construction per
package** (`probeLiveOnce`) and every per-fixture construction then passes
`WithoutIndexProbe()` explicitly — that the shipped manifest actually serves this
store's queries is precisely what only a live run can show, and one construction
proves it exactly as well as a hundred do, while a hundred would spend a hundred
`ListIndexes` plus a hundred `GetField` per declared override against a shared
project's Admin quota. `TestIndexProbeAcceptsTheDeployedManifestLive` asserts the
probe's verdict on its own besides. Unconfigured, every root skips loudly;
`FIRESTORE_LIVE_REQUIRED=1` turns those skips into the release-gate failure.

Live roots today, and what each is for:

| root | why it is live |
|---|---|
| `TestConformanceLive` | the FULL shared suite against real Firestore with the manifest deployed and the package's one probe-enabled construction behind it |
| `TestIndexProbeAcceptsTheDeployedManifestLive` | the probe's verdict against a real Admin API index registry |
| `TestQueryMatrixExecutesAgainstTheDeployedIndexesLive` | every query shape in `SCHEMA.md` §7 executed against the deployed indexes — the manifest's actual coverage proof |
| `TestGuardedMutationDependencyRevokeIsDeterminedLive` | which side of a dependency race wins is timing on the emulator and DETERMINED on real Firestore (the aborted commit re-evaluates and denies) |
| `TestSetRelationTargetsConcurrentDisjointSetsConvergeLive` | four-caller strict convergence — the emulator's thirty-second locks would exhaust the attempt budget on timing, not serialization |
| `TestLargeBatchCommitsInOneTransactionLive` | A7's proof that Firestore publishes no per-transaction write COUNT limit: a batch the old "500 writes" ceiling would have refused commits in ONE real transaction |
| `TestAmbientTransactionRefusedLive` | R1's refusal asserted against production's transaction behavior, not the emulator's |
| `TestRunTransactionalLive` | **the ONE allowed skip** (R1). `.github/workflows/live-stores.yml` allows it BY NAME; any other skipped root fails a required run |

From the repository root, `make test-stores` runs the emulator leg (skipping
loudly without `FIRESTORE_EMULATOR_HOST`) and `make check` vets the `integration`
and `integration,live` files compile-only, so the live leg cannot rot between
dispatches. Both CI legs — `firestore-emulator` and `firestore-live` — live in
`.github/workflows/live-stores.yml`; the live one is dispatch-only, provisions a
disposable database, deploys BOTH halves of this module's manifest, waits for the
composites to be READY and the field configuration to be applied, and audits the
test/skip counts against the **eight** roots derived from the source.
