# Optional authorization read caching: implementation plan

Status: IMPLEMENTED AND TAGGED WITH OWNER-ACCEPTED VERIFICATION GAPS — 2026-09-14.
The owner requested implementation, local merge, and then push/release in the
Codex session. Local correctness verification passed. Real-cloud verification
and performance acceptance remain open. Host adoption, deployment and live cloud
mutations remain separate authorization scopes.
Parent: [cacher investigation](cacher-design.md#authentication-and-authorization-caching-investigation--2026-09-14).
This is the current prospective implementation specification for the owner's
cache-first clarification. Earlier alternatives in the parent remain research
history. The September 10 generic [cacher implementation](cacher-implementation.md)
is a different, already implemented feature.

## Context

Gopernicus should accept an optional ordinary `cacher.Storer` to accelerate repeated
authorization reads. The existing bounded in-memory LRU is the first adapter;
Redis is interchangeable byte storage. Support is required across **all current
authorization stores: Turso/libSQL, PostgreSQL/pgx, Firestore and memory**. The
framework owns the security-specific read protocol, each store owns atomic
invalidation and snapshots, and hosts own
configuration, migration application and worker lifecycle.

The owner selected cache-first reads, durable fallback and transactional
invalidation. Asynchronous invalidation necessarily permits an interval of old
permission results. Therefore this design requires the adopting caller to supply
an explicit positive `MaxStaleness`; **there is no default revocation delay and no
claim that this preserves immediate observation of completed revocations**.
Implementation can proceed against the specified configurable contract; a host
must choose and accept its value before enabling it. Hosts needing authoritative
reads keep an uncached decision service. Guards always keep their current
transaction-bound readers.

## Goal

Provide optional cache-first `Check`, `CheckBatch` and `CheckExplain`, using cached
tuple-query results from one consistent store generation, with one whole-operation
durable snapshot fallback on a miss and explicit bounded observation age.

## Definition of Done

- Nil/absent cacher retains today's APIs, direct read behavior and startup cost;
  it requires no cache schema and starts no worker.
- Memory LRU and Redis pass the same read-cache conformance suite over Turso,
  PostgreSQL, Firestore and memory. Durable-store verification includes two
  independent application processes; memory authorities remain process-local.
- Every supported fact writer participates atomically once invalidation is
  activated under that store's writer-upgrade contract, including processes
  without a cacher. SQL and Firestore activation differ as specified below.
- Cache hits, mixed hits/misses, model changes, expiry, cancellation and failures
  obey the contract below; no cached grants escape the explicit freshness bound.
- Existing authentication, guards, guardian invariants, mutation results, audit,
  enumeration and ambient transaction contracts pass regression verification.
- Representative performance and recovery measurements justify adoption; the
  implementation is not called an optimization solely because it passes tests.

## Decisions and scope

| Decision | v1 specification |
|---|---|
| Durable authority | One authorization store: relationships, roles and invalidation head share a database/schema or Firestore database and transaction boundary. Memory shares one locked state. |
| Optional interface | A supplied non-nil `cacher.Storer` plus explicit policy enables read caching. No Redis-specific pocket API. |
| Invalidation record | One singleton head advanced atomically with facts: SQL trigger row, Firestore document, or memory state. The durable forms are a coalesced invalidation outbox. |
| Delivery | Each caching process polls the authoritative head independently. No global acknowledgement, deletion, consumer table, message broker or Redis subscriber. |
| Logical invalidation | New generation means new immutable keys. Do not require physical deletion or `DeletePrefix` for correctness. TTL/eviction reclaim old entries. |
| Read unit | Entire public decision operation, including a heterogeneous `CheckBatch`, chooses one generation. |
| Miss | Abandon the cache attempt and rerun the whole operation once inside one durable snapshot; populate only after it closes successfully. |
| Initial granularity | Whole authorization-store generation. No selective tenant/resource dependency invalidation in v1. |
| First adoption | LRU over a durable SQL store. Redis follows the same protocol and needs its own measured benefit. |

Out of scope:

- Authentication credentials, sessions, `Live`, grants, reset/verification proofs,
  refresh rotation or delivery providers; final permission decisions and mutation
  results are also not cached.
- Resource lookup, full-set/list caches, `FilterAuthorized`, `FilterPage`, raw tuple
  inspection/counts and audit reads. Pagination and overflow behavior stay direct.
- A Redis relationship graph, maintained read projection, write-behind store,
  distributed transaction, public authorization revision or retired mutation receipt.
- Changes to Segovia/GPS 360 business lifecycles, SDK cache
  port expansion, distributed locks, Redis scripts, pub/sub and consumer cursors.
- Automatic migrations, goroutines in constructors, Redis client ownership or
  automatic replay of writes with unknown commit outcomes.

## Preconditions and source authority

Work from final published source, not the older active checkout's authentication
code. The investigation checked tags/remotes and the September 13 publication
commit `127284f65fa0669b2b101721bc45355aa5e13cca`, including
`plans/gps360-upstream-release-manifest.json` at that commit.

| Module | Latest verified publication for this investigation | Source |
|---|---|---|
| Authentication core / Turso / pgx / pgxdb | v0.11.1 / v0.5.1 / v0.6.1 / v0.7.1 | `5fb8d51cb2355027c6a32a9b1bb617acb42b13db` |
| Authorization core / Turso / pgx | v0.13.0 / v0.7.0 / v0.8.0 | `c3f8b4ad453021ba1e40c91572d8e4618c2d8382` |
| SDK / goredis / Turso connector | v0.9.0 / v0.2.0 / v0.4.0 | `c3f8b4ad453021ba1e40c91572d8e4618c2d8382` |
| Firestore connector / authorization store | v0.1.0 / v0.1.0 | `10e5f97b32c043f8fdd71cbd733fbb09592c42f7` |
| Authentication Firestore store (regression baseline) | v0.1.1 | `5fb8d51cb2355027c6a32a9b1bb617acb42b13db` |

Before implementation, repeat the release check and select a base containing any
newer final releases without downgrading. Read [AUDIT.md](../AUDIT.md), especially
the later AUDIT-026 retirement, [RELEASING.md](../RELEASING.md), the audit release
manifest and the final host contracts. Historical migration comments describing
receipts/revisions are superseded by migration 0007 and later source.

Planning checkout: `firestore-release-20260911` / `aa75aa07`; clean publication
worktree: `/private/tmp/gopernicus-gps360-followups-20260913` / `127284f6`.
Preserve the dirty `plans/gps-360-go-audit-upgrade-handoff.md` and untracked
`plans/segovia-v2-audit-upgrade-handoff.md`. Do not prune historical worktrees or
modify `gopernicus-original`. The parent contains original-source traces and the
direct-store/projection comparisons; those are not repeated here.

This companion follows the repository's established design/implementation plan
pair in `plans/`, using the task fields in `.claude/agents/planner.md`. Current
charters and explicit user scope supersede that role's historical module map and
`.claude/plans` location. Consulted named backend and architecture reviewers;
their old `opus`/`fable` model labels are unavailable here, so review used their
existing inherited agent configuration.

## Schema / datastore impact

### Store coverage is a release requirement

The owner's all-stores clarification supersedes the earlier Firestore deferral.
The common `WithCacher`/`CacheSource` contract, four cached query families,
freshness policy and optional runtime apply to every current authorization store.
This does not broaden cached data to authentication credentials/sessions/proofs
or add caching to unrelated pockets.

| Store | Atomic invalidation | Snapshot/freshness implementation | Adoption requirement |
|---|---|---|---|
| Turso/libSQL | Optional SQL head and fact triggers | New connector `BeginRead`; proven authoritative routing | Apply optional migration; triggers cover ordinary older/nil-cacher SQL writers. |
| PostgreSQL/pgx | Optional SQL head and fact/TRUNCATE triggers | New repeatable-read `BeginRead`; primary observations | Apply optional migration in correct schema and grant invoker permissions. |
| Firestore | Optional head document, updated by participating writers in the same native transaction | Reuse existing connector `ReadSnapshot`; strong point read for Observe | Upgrade/configure every writer before activation; external/older writers cannot be made safe by a server event trigger. |
| Memory reference | Generation and facts published under the shared state mutex | Clone one complete state/version; observer reads that state | Use shared `memory.New(WithCacheReads())` bundle; separate processes are separate authorities. |
| Custom/future adapters | Implement equivalent atomic invalidation and writer activation | Implement the optional `CacheSource` and binding contract | Pass shared conformance; do not advertise caching support based on byte storage alone. |

LRU and Redis are tested with each store; choosing Redis never selects a different
authorization repository. A future store may work uncached before it implements
this optional capability, but this feature's current release must not leave
Firestore as an unspecified follow-up or label a bypass-only adapter supported.

### Optional migration source

Add `cache_migrations/0001_iam_cache_invalidation.sql` to each authorization SQL
store module. Export it as a separate source, **`authorization-cache`**, with
`CacheMigrationsFS`, `CacheMigrationsDir` and `ExportCacheMigrations(dst)`.
Leave the existing `MigrationsFS`, `MigrationsDir`, `ExportMigrations` and base
0001–0007 inventory unchanged. Source/version ledgers do not express cross-source
ordering: the host must finish `authorization` through 0007 before applying this
optional source to the same database/schema. Document this new optional-source
convention in both store READMEs and migration inventory tests.

The SQL migration creates one row:

| Column | Representation and constraint |
|---|---|
| `slot` | Integer primary key, must equal 1. Exactly one row must exist. |
| `protocol` | Integer, must equal 1 for this protocol. |
| `epoch` | 32 lowercase hex characters, randomly generated database incarnation. |
| `generation` | Signed 64-bit integer, nonnegative, initially 0. Never wrap or reset within an epoch. |

Table name: `iam_cache_invalidation`. No timestamps, payload, consumer ownership,
`published_at`, acknowledgement or retention deletion. A poll from generation 5
straight to 20 is sufficient: all previous keys become ineligible. Intermediate
events have no individual delivery obligation. This is less machinery than a
general event log because invalidation is store-wide.

Seed the epoch within the host-owned migration: SQLite/libSQL
`lower(hex(randomblob(16)))`; PostgreSQL
`replace(gen_random_uuid()::text, '-', '')`, supported by the PostgreSQL 17 fixture
baseline; reconfirm the supported deployment versions in task 1.
[PostgreSQL UUID generation](https://www.postgresql.org/docs/17/functions-uuid.html).
The epoch is operational cache metadata, not an actor,
tenant, public mutation revision or authentication revision.

### Trigger contract

Install non-temporary `AFTER INSERT`, `AFTER DELETE`, and `AFTER UPDATE` row
triggers on **both** `iam_relationships` and `iam_roles`. UPDATE fires only when
OLD and NEW differ, comparing all current columns: the six relationship fields
and five role fields after migration 0007. Do not rely on `UPDATE OF`. PostgreSQL
also gets an `AFTER TRUNCATE FOR EACH STATEMENT` trigger on each table. Whole-row
comparison in PostgreSQL and explicit per-column `IS NOT` comparisons in SQLite
avoid null-comparison mistakes. Later fact schema migrations must update this
coverage before cache reads resume. SQLite only offers row triggers; PostgreSQL
documents the `UPDATE OF` limitation when other triggers change rows.
[SQLite triggers](https://www.sqlite.org/lang_createtrigger.html),
[PostgreSQL 17 triggers](https://www.postgresql.org/docs/17/sql-createtrigger.html).

Each firing must increment the singleton in the **same** transaction. Multiple
increments per statement/command are allowed; observers see only committed values.
`ON CONFLICT DO NOTHING` inserting no row produces no increment. A conservative
increment on additional changes is safe, but do not promise exact change counts.
Audit-on/off does not affect invalidation. No Go-side post-commit notification is
required, and no cache I/O runs inside a writer or mutation guard.

SQLite trigger body specification (repeat for each triggering operation):

```sql
UPDATE iam_cache_invalidation
SET generation = generation + 1
WHERE slot = 1 AND protocol = 1
  AND typeof(generation) = 'integer'
  AND generation >= 0 AND generation < 9223372036854775807
  AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
SELECT CASE WHEN changes() <> 1
  THEN RAISE(ABORT, 'authorization cache head invalid') END;
```

Add matching table constraints, including `typeof(generation) = 'integer'`.
SQLite integer overflow can promote arithmetic to floating point, so a positive
number check alone is insufficient. Use `RAISE(ABORT)`, which rolls back the
statement; existing store savepoints/transactions must propagate the failure and
roll back their full write unit. `RAISE(FAIL)` is unsuitable for multi-row writes.
[SQLite arithmetic](https://www.sqlite.org/lang_expr.html),
[SQLite conflict handling](https://www.sqlite.org/lang_conflict.html).

PostgreSQL uses one shared, schema-local trigger function with equivalent
protocol/epoch/nonnegative/upper-bound predicates and an explicit affected-row
check. Use `SECURITY INVOKER`. Address the head via safely quoted
`TG_TABLE_SCHEMA`, never an unqualified table selected by the caller's
`search_path`. Missing/incompatible head and overflow raise an exception so the
fact statement cannot commit. Existing writer roles need `SELECT` on the head
columns read by the function and `UPDATE(generation)`; lack of required permission
fails the fact write. Runtime identities must not have trigger-disabling/DDL
privileges. Test actual grants and schema `USAGE` under the invoker identity.
[PostgreSQL function security](https://www.postgresql.org/docs/17/sql-createfunction.html).

Covered ordinary SQL DML remains safe even from an older or nil-cacher writer.
DDL, imports that disable triggers, replication modes bypassing triggers, and
restore are administrative operations: fence caching processes, perform work,
rotate the epoch, validate schema/triggers, then start fresh readers. A startup
probe cannot protect against an administrator removing a trigger afterward.

### SQL reader activation and common binding

Add `WithCacheReads()` to each SQL store's existing `Option` family. It **exposes
and probes the optional read capability**; it does not enable invalidation writes.
Installing the migration enables those database-wide. Ordinary `Repositories`
keeps its existing base probes. With cache reads requested, construction must
also verify singleton shape, supported protocol and expected enabled
trigger/function definitions, and fail clearly for partial installation.
Store-owned definition signatures/catalog checks need dialect fixtures; checking
only object names or an `enabled=true` field is insufficient.

PostgreSQL must bind all three fact/head tables to the same resolved schema.
For default unqualified construction, resolve their actual schema, reject mixed
resolution and freeze that schema into the enabled repository/read source;
subsequent `search_path` changes must not move its facts. `WithSchema` remains the
explicit route. Keep unconfigured construction's SQL behavior unchanged.

`Repositories(..., WithCacheReads())` returns an optional `CacheSource` as well as
normal ports. Each enabled relationship/role reader and source exposes the same
nonempty opaque `CacheBinding() string`, derived from the validated incarnation
and store scope, without connection secrets. Services forward this identity via
an optional structural capability. Cache-enabled construction rejects absent or
mismatched bindings. This prevents accidentally using store A's head with store
B's relationships or roles. Custom cache sources are trusted implementations of
this contract, covered by conformance tests, not arbitrary independent head
readers.

The single-kind `RelationshipRepository` constructor remains a direct API;
reject `WithCacheReads()` there with guidance to use the bundle and select its
relationship field plus source. This avoids exposing a partially configured
cache that cannot supply its snapshot capability.

### Firestore invalidation and snapshot implementation

Firestore is required in this release. Use a new fixed document
`iam_cache_invalidation/head`, holding `protocol`, `epoch` and signed-int64
`generation` with the same validation as SQL. No composite index is needed for
the fixed-document Get; existing fact-query indexes remain required. Reserve
the collection in `SCHEMA.md` and the collection-ownership tests. This metadata
is new internal cache coordination, not the retired mutation scope anchors.

Add these proposed APIs to `pockets/authorization/stores/firestore`:

```go
func InitializeCacheInvalidation(ctx context.Context, db *firestoredb.DB) error
func WithCacheInvalidation() Option
func WithCacheReads() Option
```

The initializer is an explicit maintenance call, never a constructor side effect.
It creates the head once with a random epoch and generation 0; on an existing
head it validates without resetting/changing it. The host runs it while readers
and writers are fenced. Restore/clone requires a separate explicit epoch rotation
under the same fence; initialization must not masquerade as rotation.

`WithCacheInvalidation()` opts this bundle's relationship, role and mutation
writers into required atomic head maintenance, even when no cacher is configured.
`WithCacheReads()` implies that writer participation and also probes/exposes the
common CacheSource. Both options require initialized compatible metadata at boot.
The default, with neither option, keeps current direct behavior and adds no
protocol reads/writes. The single-kind constructor accepts writer-only
invalidation; as with SQL, read-cache users use the bundle plus source.

**Activation is different from SQL:** there is no synchronous database trigger
that can force an older Firestore Go writer to maintain this head. Stop, upgrade
and configure **every** writer before allowing cached readers; prevent obsolete
binaries/maintenance clients from retaining write access. An upgraded process
with no cacher must still use `WithCacheInvalidation()` while any cache depends
on that database. Document edits through an Admin client, imports and existing
maintenance helpers are outside this protocol unless explicitly adapted; fence
readers and rotate epoch around them. Server clients bypass Firestore Security
Rules, so those rules cannot enforce this invariant for the current Go adapter.
[Firestore server authorization](https://docs.cloud.google.com/firestore/native/docs/security/rules-structure).

Automatic marker discovery on every write is deliberately not v1: it would add
billed metadata reads even for unconfigured hosts and still would not protect
against older writers. The explicit writer option preserves optionality, at the
cost of a mandatory deployment-wide writer inventory and activation procedure.
Do not claim that installing a document alone activates all writers as SQL
trigger installation does.

Current integration points:

- `mutations_eval.go:factWrites.flush` centralizes tuple/subject-claim/role and
  optional audit writes. Add separate invalidation configuration and a head read
  while still in the transaction's read phase. Validate protocol, bound epoch,
  integer type/range and overflow; queue an explicit `generation = h + 1` with
  actual fact writes and audit in that same native transaction. Missing/invalid
  head must abort the change, never silently disable invalidation. No-op paths
  need not advance the head. Do not issue a metadata read after queuing a write.
- Thread configuration through `relationships.go`, `roles.go`, `writes.go` and
  `mutations.go:apply/applyTx`. Re-read head and reset all attempt-local state on
  each retry. Preserve `contention.go:retryTransact/retryContention`: definite
  aborts may retry; policy refusals remain terminal; uncertain commit/transport
  failures do not authorize replay. Never split facts/head/audit to fit limits,
  allocate versions outside the transaction, or use an unchecked increment.
- `integrations/datastores/firestore/transact.go:DB.ReadSnapshot` already supplies
  a read-only, exactly-once callback. Reuse it; no new Firestore `BeginRead` is
  required. CacheSource first refuses **caller** ambient context because that
  connector otherwise reuses an ambient transaction. Then read head first and
  give the callback a private check reader bound to the supplied Reader.
- Reuse `reads.go:expand`, `anyTupleWithSubject`, `relationTargets`, set-read
  helpers and `grants.go:roleExists`. Public store methods refuse ambient
  transactions and cannot be naively called inside the snapshot. All batch
  chunks, graph hops and roles must use one supplied Reader; `ForChecks` preserves
  model, version and lifetime. Add explicit closed-state/cancellation checks and
  never fall back to client reads from an expired view.
- `Observe` refuses ambient context, reads the head directly from the server
  without historical read time, validates and returns its version. Bind source
  and readers with the validated epoch, connector `DB.Target()` and fixed
  collection/protocol identity. Publish cache fills only after ReadSnapshot
  returns successfully. Normal public ambient refusal remains unchanged when
  the coordinator bypasses caching.

Firestore read-only transactions without an explicit historical read time use
strong consistency; the existing connector supplies the snapshot boundary.
[Firestore transaction options](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/TransactionOptions).
Cloud Functions/Eventarc document events are asynchronous, can repeat and have no
ordering guarantee. They cannot substitute for the head update in the fact
transaction; polling a head maintained later by such events could repeatedly
renew a stale observation.
[Firestore events](https://firebase.google.com/docs/functions/firestore-events).

The singleton document adds one metadata read and, for a changed transaction,
one write, plus index/billing work and contention. Measure retry amplification
and the resulting store-wide serialization pressure on real Firestore. Sharding
the head would require a new consistency proof and is not an automatic tuning
option. If costs outweigh saved graph reads, the host can remain uncached while
the framework still provides the tested optional capability.

`upgrade.go:UpgradeTupleStorage` and other bulk maintenance bypass the normal
flush path: explicitly fence caches and rotate epoch for upgrades/import/restore.
Rollback disables/drains all cache readers before removing writer participation
or deleting head metadata. Head deletion during enabled fact writes must fail
those changes rather than create an untracked interval.

## Module / API impact

All signatures below are **proposed**, not available in today's release.
Keep coordination in `logic/decisions`, which already consumes relationship and
role reads. Direct relationship and role services do not gain independent cache
runtimes in v1. No new `logic/cache` package or SDK security policy is necessary.

### Core ports and configuration

In `logic/relationships/read_model.go`, extract this subset from existing Reader:

```go
type CheckReader interface {
    PermissionReader // existing expansion check and complete target reads
    CheckBatchDirect(ctx context.Context, resourceType string,
        resourceIDs []string, relation, subjectType, subjectID string,
        maxExpansionStates int) (map[string]bool, error)
}

type CheckReadSource interface {
    ForChecks(ReadModel) CheckReader
}
```

Existing `Reader` embeds `CheckReader` plus its unchanged lookup methods; existing
implementations satisfy the same method set. `ForChecks` is a separate new name
because existing `ForModel` interfaces have different return types and Go has no
covariant interface method returns.

In `logic/decisions/read_cache.go`:

```go
type CacheVersion struct {
    Epoch      string
    Generation int64
}

type CheckReads interface {
    relationships.CheckReadSource
    HasExactRole(ctx context.Context,
        subjectType, subjectID, role, resourceType, resourceID string,
    ) (bool, error)
}

type CacheSource interface {
    CacheBinding() string
    CacheableContext(context.Context) bool
    Observe(context.Context) (CacheVersion, error)
    ReadSnapshot(context.Context,
        func(context.Context, CacheVersion, CheckReads) error,
    ) error
}

type CachePolicy struct {
    Namespace      string
    MaxStaleness   time.Duration
    PollInterval   time.Duration
    PollTimeout    time.Duration
    CacheTimeout   time.Duration
    EntryTTL       time.Duration
    MaxEntryBytes  int
    MaxFillBytes   int
    MaxFillEntries int
}

func WithCacher(cacher.Storer, CachePolicy) Option
func (s *Service) ReadCache() *CacheRuntime
func (r *CacheRuntime) Poll(context.Context) error
func (r *CacheRuntime) PollInterval() time.Duration
func (r *CacheRuntime) Close() error
func (r *CacheRuntime) Stats() CacheStats
```

Add optional `CacheSource decisions.CacheSource` fields to root `Repositories`
and `decisions.Readers`. Root `authorization.WithCacher(cacher.Storer,
decisions.CachePolicy)` forwards at assembly; root `Components.ReadCache` points
to the same runtime returned by `Decisions.ReadCache()`. No runtime is allocated
when disabled. Direct users of `decisions.NewService` get the same option and
validation; assembly must not mutate supplied model-bearing services.

Untyped nil cacher means disabled, including explicitly passing nil. Typed nil
is invalid input. A non-nil cacher without compatible source/readers, namespace,
positive `MaxStaleness`, or a decision-capable model is a construction error.
Do not silently ignore requested caching or silently drop a configured kind.
Opaque roles-only wiring without a role model cannot use this feature.

Policy resolution:

| Setting | Rule |
|---|---|
| `Namespace` | Required nonempty application/store name, e.g. `segovia/authorization`. This is a namespace, not a tenant selector. |
| `MaxStaleness` | Required positive duration; no default. It is maximum age of an authoritative head observation, not cache-entry TTL. |
| `PollInterval` | Zero resolves to `min(250ms, MaxStaleness/4)`; otherwise positive. |
| `PollTimeout` | Zero resolves to `min(1s, MaxStaleness/4)`; otherwise positive. |
| Poll timing validation | Both resolved values positive; their sum must be less than `MaxStaleness`, using overflow-safe validation. Extremely small durations that round to zero are invalid. |
| `CacheTimeout` | Zero resolves to 25ms; positive bounds the entire cache-only attempt, and separately the entire post-snapshot publication attempt. |
| `EntryTTL` | Zero resolves to 5 minutes; positive required after resolution. Memory reclamation only. |
| Entry/staging bounds | Defaults: 64 KiB per encoded entry, 1 MiB total staged keys/envelopes, 256 staged entries per operation. Negative values invalid; total must accommodate one allowed entry. |

These numeric defaults are starting operational budgets, not measured optimal
values. Tests use short explicit durations and a controllable private clock.
Expose aggregate, concurrency-safe runtime statistics through an additive
`Stats()` snapshot: hit-complete/miss-fallback/bypass reason counts, cache errors,
oversized/skipped fills, poll failures, observation age and last observed
generation. No raw keys, principals, tokens or tuple values in metrics/logs.
Use existing logger conventions for rate-limited state transitions; do not add a
new observability dependency or per-call audit event.

### Reuse the existing evaluators

Add `CheckWith`, `CheckBatchWith`, and `CheckExplainWith` to relationship Service,
accepting `(ctx, CheckReadSource, request)` with the existing corresponding
request/result types. These select `ForChecks(s.readModel)` and call the same
private evaluator, with fresh per-operation budgets/memoization. Refactor batch
helpers to accept `CheckReader`. Do not modify a shared service's reader/model,
copy mutex-bearing services or compile models on each request. Existing guarded
`EvaluateWith` and its callback-scoped transaction reader remain unchanged.

Factor decisions' public methods into cache orchestration and private operation
workers accepting operation-specific readers. Roles reuse `roleEngine.check`
and existing exact/global fallback. Keep all dispatch, validation, explanation,
batch ordering/duplicate handling and limit semantics in those existing workers.

**Filtering trap:** current `decisions.FilterAuthorized` calls relationship
filtering directly but invokes public `CheckBatch` for roles. Route its roles
branch through the private uncached worker. `FilterPage` transitively stays
uncached for both kinds. Do not accidentally enable cache only for role lists.

### Store snapshot capability

Add `BeginRead(ctx) (*Tx, error)` to the Turso and pgxdb connectors. Preserve
existing `Begin` and `Transact` behavior exactly. PostgreSQL uses pgx
`BeginTx` with `RepeatableRead` and `ReadOnly`; Turso uses pinned-connection
`BEGIN DEFERRED` with existing cleanup/discard rules. SQLite's deferred begin
alone does not forbid writes: the pocket exposes only check-read capability to
the callback, and connector documentation must not claim engine-enforced
read-only mode. Never change Turso's security writer `BEGIN IMMEDIATE` to deferred.
PostgreSQL Read Committed is insufficient for a multi-query fill; successive
queries need the same snapshot.
[PostgreSQL isolation](https://www.postgresql.org/docs/17/transaction-iso.html).

Each SQL `ReadSnapshot` implementation:

1. Rejects an ambient transaction for this callback API; `CacheableContext`
   already sends ordinary ambient operations down the existing direct path.
2. Begins its read transaction and reads the head as the **first** snapshot query.
3. Supplies relationship/role readers bound to that exact transaction and version.
   Refactor existing SQL reads to select a private read querier override, rather
   than duplicate CTEs or expose the driver through the core port.
4. Preserves model scoping, budgets, ordering and every nested userset edge.
   A relationship-only check does not read role rows merely because the snapshot
   supports both kinds.
5. Closes on success/error/panic/cancellation using connector cleanup rules.
   Exposes no cache I/O while the transaction is open. A failed close is an error;
   do not publish staged values or synthesize successful decisions after it.
6. Invalidates all callback views at closure; retained `ForChecks` readers fail
   with a documented snapshot-closed error and never switch to pool reads. Views
   are sequential callback capabilities, not safe to escape to goroutines.

For SQL, `Observe` is a fresh, standalone authoritative SELECT, never `QuerierFrom` an
ambient transaction and never a replica whose security visibility is uncertain.
Supported Turso client/server/version and routing must be demonstrated in task 3.
If the deployed connector cannot guarantee primary observations and snapshot
fills, do not enable this capability on that deployment. A local libSQL test
cannot certify Turso Cloud replica freshness.

Current `QuerierFrom` trusts the host to pass a context from the same DB instance;
the handle is not an instance-ownership check. Cache binding does not repair that
existing host obligation. Cache bypass detects any ambient transaction of the
selected connector; no cross-database or cross-dialect participation is inferred.
Keep guarded/system mutation ambient rejection and raw writer savepoint behavior.

For the memory reference store, add `WithCacheReads()` to `memory.New` and a
`CacheSource()` accessor. Generate a new epoch once for the shared Store, advance
generation inside `state.write` only upon committed fact changes, independent of
audit, and clone facts+version together under its mutex for snapshots. Evaluate
against the private clone after releasing the original lock. Standalone
`NewRelationships` and `NewRoles` do not certify a combined source. The in-memory
authorization repository here is a test/reference adapter; hosts may use an LRU
**cacher over Turso**, without using an in-memory authorization repository.

## Read and invalidation algorithms

### Cache material and keys

| Read family | Cached value | Required key dimensions beyond namespace/version |
|---|---|---|
| `CheckRelationWithGroupExpansion` | Successful bool, including false | All resource/relation/subject args, exact expansion-state budget and model allowlist digest. |
| `GetRelationTargets` | Complete successful target slice, including empty | Resource type/ID, relation and model allowlist digest; each target preserves its exact userset relation. |
| `CheckBatchDirect` | Complete successful map for the entire input batch | All args, full ordered resource-ID list including duplicates, expansion budget and model digest. Do not split one batch result into independent cached checks. |
| `HasExactRole` | Successful bool, including false | Full subject, role and exact resource scope; global empty scope is a separate key. |

These include tuple-derived membership booleans, not just physical tuple rows.
The permission model still executes every request; no cached `CheckResult` can
skip evaluation, negative membership, exact userset semantics or budgets.

Use `cacher.Cache`'s existing length-framed namespace, plus an internal format
prefix `authorization-check-reads/v1`, epoch, generation, query family and a
SHA-256 digest of a versioned canonical argument encoding. Encode ordered fields
unambiguously with JSON structs/arrays; do not concatenate with delimiters.
Hash canonical `ReadModel.JSON()` for relationship queries. Keep the actual batch
input order/multiplicity in its canonical encoding; do not assume it is safe to
deduplicate before applying existing budgets. Independent model changes cannot
reuse incompatible tuple material. Role permission expressions are evaluated
from the current immutable model; only exact role facts are reused.

Envelope fields: format version, epoch, generation, query family, argument digest
and typed value. Validate all fields and the complete map/target shape before use.
The batch map must contain exactly the distinct requested IDs, including explicit
false values, and no extra IDs. Validate target fields and model eligibility;
preserve evaluator canonical ordering and never treat a truncated list as complete.
False/empty is a hit; missing/invalid JSON is a miss. Copy mutable slices/maps.
Never cache errors, incomplete target sets, overflow, cancellation or partially
completed batches. Cached large values must still pay the existing evaluation
budgets; a cached success from a larger limit cannot bypass a smaller limit.

Namespace plus random incarnation prevents Segovia/GPS 360/store collisions even
if a host shares a Redis service. Tuples have no universal built-in tenant field;
hosts must keep identifiers and models consistent with their tenant boundaries.
The initial store-wide head intentionally invalidates across all tenants because
nested/global grants may cross a local resource boundary. Do not infer an
invalidation partition from `resource_id` alone.

Cache is inside the security trust boundary: access controls must prevent
untrusted writers fabricating valid envelopes. Encoding checks detect corruption,
not malicious forgery. Redis credentials and payloads remain host-owned.

### Poll lifecycle and freshness

`CacheRuntime` starts cold and owns no goroutine. `Poll` serializes with other
polls; waiting respects caller cancellation and no older completion overwrites a
newer observation. It uses `PollTimeout` and records monotonic start time `s`
**before** the authoritative read. On success, the observation expires at
`s + MaxStaleness`, not at response time. A response arriving after that deadline
cannot make the cache ready. Request SQL fills, cache hits and Redis availability
never extend this deadline.

A poll failure immediately disables shared-cache reads until a successful poll;
keep the last version only for regression detection. A lower generation in the
same epoch, changed epoch, malformed head or protocol mismatch latches this
runtime disabled and reports an error requiring store/source reconstruction.
Ordinary reads can still use the direct store path. A same-generation successful
authoritative poll renews observation age. There is no persisted ready marker.
`Close` is idempotent, prevents future polls, cancels/invalidates an in-flight poll
and clears readiness; it never closes the borrowed DB/cacher/Redis client.

At the final cache-hit validation point, check the original captured observation
age and that runtime readiness/version still match. If either changed, discard
the attempt and take the single durable fallback. Use a monotonic elapsed-time
source and test clock jumps; deployments where suspend/resume stops that clock
must restart/clear readiness on resume before serving. This is a check-operation
freshness contract, not a hard real-time guarantee about later HTTP delivery.

If revocation commits at time `r` after an observation began at `s`, a result
using its old version can pass the final freshness check only while
`now < s + MaxStaleness`, hence before `r + MaxStaleness`. This reasoning requires
authoritative observations and complete transactional generation coverage.
Polling usually shortens the delay but does not remove it. A stalled poller
loses acceleration when its observation expires. A request started earlier also
rechecks age before returning a cache-derived result.

This mode gives neither immediate read-after-write nor immediate revocation.
To make a strict check, wire a second `decisions.NewService` from the same normal
services without `WithCacher` and use it for that operation. No new context mode
or authorization result field is required. Guards continue to read in their
mutation transaction. A separate permission check followed by a business write
still has its existing check/use race.

### Per-operation algorithm

1. Check context, validate input/model dispatch and existing batch limits before
   touching cache. With no cacher, call the existing direct worker unchanged.
2. With caching configured but ambient/ineligible context, cold/expired/closed
   observer or unavailable source, bypass to the existing direct worker. This
   bypass does not warm cache and does not require the optional head to be healthy.
3. Capture one healthy observed version `g` and its original expiration. Evaluate
   the entire operation over **cache-only** typed readers, under one
   `CacheTimeout` budget. Preserve the normal per-call memo reader.
4. A missing/invalid/oversized entry or cache transport timeout/error yields a
   private sentinel, propagated through every evaluator branch. Do not turn the
   sentinel into a denial or ignore it while trying another granting branch.
   Other genuine model/evaluation errors retain their existing meaning.
5. If every required read hits, validate caller cancellation, captured age and
   current readiness/version again. A valid result returns with zero request SQL.
6. On sentinel or failed final freshness validation, discard all partial results,
   explanations and memoized facts. Run the entire operation **once** via
   `CacheSource.ReadSnapshot`, ignoring all shared-cache entries. Use its actual
   version `h`; it need not equal `g`, but its epoch must match the bound source
   and its generation must be at least the captured `g`. A lower generation is
   evidence of a routing/restore violation: disable this runtime, fail the
   operation and publish nothing. Do not compare against a later concurrent poll
   that legitimately advanced after this snapshot began. No repeated miss/restart
   loop.
7. While reading that snapshot, stage owned successful primitive results in
   bounded request memory. Skip oversized entries and stop staging at either
   count/byte ceiling; the durable operation itself can still succeed. Do not
   truncate query results to make them cacheable.
8. After successful operation and snapshot closure, encode/validate and publish
   staged entries under immutable `h` keys with `EntryTTL`, within one separate
   `CacheTimeout` budget and caller context. Best-effort publication failures do
   not retry or replace the durable result. Check caller cancellation before the
   final return. Do not create background fill goroutines.

The cache-only timeout uses a derived context; its expiration permits fallback
under the still-live original request context. Check this at the coordinator,
even when the evaluator's own `ctx.Err()` returned `DeadlineExceeded` between
cache reads instead of a wrapper returning the private sentinel. Original
cancellation stops work and takes precedence over timeout fallback.
Staging byte accounting must include owned keys and envelope bytes, with bounded
encoding and post-encoding size checks. A partial cache publication is safe:
subsequent operations either find all their entries or perform one full snapshot.

Why independently filled hits compose: within an epoch, **any** relevant committed
fact change advances generation. Therefore snapshots with equal generation have
equal cache-observable facts, provided all writers obeyed the selected store's
invalidation contract. A miss must
not combine old `g` hits with new `h` SQL facts: that combination might never have
existed, especially across removals and negative checks. Whole-operation fallback
avoids maintaining a versioned graph or historical database snapshots.

### Writes and failures

No existing writer returns a generation or cache receipt. Current mutation
`Outcome` and `SameRoleGrantRemains` remain unchanged; no new audit record is
minted for head changes. Raw writer ambient savepoints and owned/guarded/system
transaction rules remain exactly as published. Trigger failures propagate as
durable errors, never as a best-effort invalidation warning.

| Interleaving | Required outcome |
|---|---|
| Revoke commits while another instance hits old cache | Old result is permitted only within the explicitly chosen observation bound; observing a newer head or losing readiness forces fallback. Guards never use it. |
| Old snapshot finishes filling after revocation/invalidation | It writes old-version keys. New-version readers cannot select them; old observers remain limited by their original deadlines. |
| Commit succeeds; Redis cleanup fails | No required cleanup exists. Fact change and head committed together; every poller eventually sees the head or stops accepting cache. |
| Redis down/slow/corrupt/evicting | Bound cache attempt; rerun one authoritative snapshot when eligible, or normal direct path if observer unhealthy. Durable failures remain errors. |
| Durable database down | A still-ready observer may temporarily serve all-hit reads within the accepted bound; a failed poll disables them immediately. Misses/expired observations require durable reads and fail if those fail. No stale-on-error extension. |
| Poll messages lost, duplicated or reordered | There are no delivery messages in v1. Every process reads current head; duplicate versions are harmless; serialized polling and regression checks reject backwards movement. |
| Multiple LRU instances / shared Redis | Every process observes independently. There is no single competing consumer that can acknowledge invalidation for everyone else. |
| Redis restart or old persisted Redis state | Cold runtime must first observe DB. Matching immutable-version entries remain valid; mismatches are unreachable. TTL is not the revocation proof. |
| DB restore/clone or generation regression | Fence cache readers and rotate epoch before reopening; a running source detecting regression/epoch change disables itself. Fresh runtimes alone cannot detect a restored historical epoch: the operational fence/rotation is mandatory. |
| Concurrent application writes | Database fact/head transactions serialize as required by existing write paths; the head adds a store-wide contention point. Measure it. |
| Request canceled / read close unknown | Stop, clean up transaction independently, publish nothing from an unsuccessful snapshot, return error. Never convert cache error into permission success. |
| Write commit result unknown | Preserve current uncertain result. The DB contains either both facts/head or neither. Polling observes committed state later; do not replay a write automatically or invent a receipt. |
| Stale Turso replica supplies head/fill | Unsupported for this contract. A lagging head can renew old authority forever; use a proven authoritative route or leave caching disabled. |

## Host wiring, migration and rollout

Illustrative future composition-root code; `acceptedMaxStaleness` comes from an
explicit host configuration decision, not a framework default:

```go
// Apply authorization, then optional authorization-cache migrations pre-boot.
repos, err := authorizationturso.Repositories(ctx, securityDB,
    authorizationturso.WithCacheReads(),
)
// Handle err using the host's normal startup error path.
parts, err := authorization.New(repos,
    authorization.WithRelationshipModel(relationshipModel),
    authorization.WithCacher(cacher.NewMemory(cacher.WithMaxEntries(10000)),
        decisions.CachePolicy{
            Namespace:    "segovia/authorization",
            MaxStaleness: acceptedMaxStaleness,
        }),
)
// Handle err before constructing or mounting dependent components.
if runtime := parts.ReadCache; runtime != nil {
    pool := workers.NewPool(runtime.Poll,
        workers.WithName("authorization-cache"),
        workers.WithWorkerCount(1),
        workers.WithPollInterval(runtime.PollInterval()),
        workers.WithIdleInterval(runtime.PollInterval()),
    )
    // Register pool.Run in the host's existing cancellation group.
    // Close runtime during shutdown; close borrowed resources afterward.
}
```

`Poll` returns nil on an unchanged healthy head, not `workers.ErrNoWork`; renewal
is necessary work. The explicit idle interval also prevents accidental worker
backoff from exceeding the freshness budget. A missing worker safely leaves the
cache cold. Redis substitution is `goredis.NewCacher(hostOwnedRedisClient)`;
client opening/closing belongs to the host. No events/jobs pocket dependency is
needed: SDK workers already accepts `func(context.Context) error`.

Optionality has two independent lifecycle settings: supplying a cacher enables
this process's cache reads; applying the optional SQL migration enables durable
invalidation for the whole store. Removing a cacher stops that process's cache
work. Once the migration exists, nil-cacher writers still pay trigger/head update
cost while another reader might rely on it. To remove database overhead, first
disable and drain **all** caching processes, then remove triggers/head in a
host-owned migration. Never remove invalidation first.

Use [host contracts H0–H10](../examples/README.md#3-the-rules--h0-through-h10): H7/H10
keep concrete construction in `cmd` and SQL in stores/workshop; H1/H3 keep
transactions/cache storage out of host domain compositions; H6 is the boundary
for host pocket adapters. Runtime polling is reusable framework behavior; choosing
which business operations require strict authorization is host policy.

Segovia and GPS 360 use separate security databases, namespace/incarnation and
worker sets. Their users, credentials, sessions and permission graphs remain
independent. PostgreSQL business rows may keep application-qualified opaque actor
references; they do not become authentication or authorization storage.

Cache adoption can happen while Segovia still keeps authorization in PostgreSQL.
Moving authorization to Turso is a separate host migration. Segovia's current
resource creation/move operations that commit business rows and authorization
topology together lose that atomicity across databases. The parent investigation
traces these concrete functions and distinguishes tenant seeding and unimplemented
top-level deletion. Before split-store adoption, specify durable pending/active/
deleting states, idempotent provisioning/reconciliation, visibility gates, move
failure/compensation and tombstone behavior. Permission changes and deletion must
not expose business rows against stale topology. An authorization invalidation
head cannot make a PostgreSQL business transaction atomic with Turso.

Rollout order: direct baseline → optional schema/triggers with all readers direct →
source validation/poll-only exercise → isolated LRU canary with accepted freshness →
measured expansion → Redis only if useful. Rollback disables cacher/read runtime
first and keeps schema/triggers until no caching readers remain. Restore/clone
runbooks rotate epoch with reader fencing. No host migration is performed by this
framework plan.

For Firestore, replace the SQL schema step with the writer fence/upgrade,
explicit head initialization and `WithCacheInvalidation()` on every writer.
Test an instance with writer invalidation but no cacher alongside LRU/Redis
instances. Do not carry SQL's older-writer coexistence promise over to Firestore.
For memory, keep all readers/writers on one shared reference Store; sharing Redis
cannot turn unrelated in-memory authorities into one store.

## Verification specification

### Deterministic and durable correctness suites

Create a reusable `stores/storetest/read_cache.go` conformance entry point and private
test clock/barrier fixtures. Test schedules use channels/proxy barriers with
bounded deadlines, not arbitrary sleeps. SQL and cache statement counters must
distinguish actual network exchanges from API calls. At minimum:

1. **Opt out/configuration:** no new schema probes, no cache calls, no runtime;
   nil/typed nil, bad policy, missing migrations/triggers, mismatched bindings,
   mixed-schema resolution and unsupported store fail at the specified boundary.
   All current stores support the enabled contract. Custom stores without the
   optional capability still compile/work uncached and reject requested caching.
2. **Read parity:** allow/deny, exact userset relations, nested/cyclic groups,
   negative checks, role scope/global fallback, mixed `CheckBatch`, duplicate IDs,
   model narrowing, all evaluation budgets and `CheckExplain` parity. Warm hits
   execute zero request SQL; runtime polling is accounted separately.
3. **Mixed-generation trap:** stage two individually plausible facts whose
   combination would grant although neither committed snapshot grants. Force a
   hit at `g` and miss after `h`; prove complete reevaluation at `h`, with no partial
   batch/explanation/memo reuse and at most one fallback.
4. **Fill/revoke race:** pause snapshot fill before publication, commit revoke,
   poll newer head, then release old fill. Both independent LRU readers and Redis
   readers must reject old keys. Repeat cache-hit/revoke and expiry-during-check.
5. **Writer coverage:** raw grant/revoke/replace/purge/teardown and role writes;
   guarded/system writes; SQL outside Go store wrappers; DML UPDATE and PG
   TRUNCATE; audit on/off; duplicates/no-op; multi-row operations; missing/invalid/
   overflowing head. Fact/head rollback together on statement, savepoint and
   outer rollback. Assert existing affected-row and mutation result semantics.
6. **Guard races:** revoke actor membership or last-guardian protection while
   another instance attempts a guarded mutation; negative membership and nested
   userset changes must serialize through current transaction readers. Warm stale
   grants must not enter guards. New snapshot readers must fail after callback
   lifetime and `ForChecks` must preserve it. Existing guard views are only valid
   within their callback; test supported in-callback behavior and preserve their
   transaction/model binding. Do not claim existing memory guard views already
   enforce a closed-state check, or add that separate hardening implicitly.
7. **Outage/recovery:** cache timeout/error/corruption, unavailable durable store,
   failed/stalled/late polls, observer close, process pause/resume, old Redis
   persistence, eviction, epoch change/regression and restart. Poll failure cannot
   renew freshness; cache fills cannot repair authority readiness. Lost polls,
   duplicate observations and concurrently invoked polls cannot regress version.
8. **Transactions/cancellation:** ambient direct bypass, correct same-DB host
   ownership, panic cleanup, canceled Begin/SELECT/commit, transport loss after
   write COMMIT and after read close. Verify pinned connections are safe for
   reuse/discard and no automatic mutation retry was introduced.
9. **Enumeration unchanged:** `LookupResources`/ID-page paths, complete-set
   overflow, ordering/cursors, retained readers, role and relationship
   `FilterAuthorized`, low-selectivity `FilterPage`. Cache counters remain zero
   for these operations, including the roles filtering branch.
10. **Mounted authentication regression:** real mounted HTTP login/session,
    cookie/bearer `Live`, logout, refresh/grace, password change/reset, disable,
    credential ownership and replacement eligibility, session/grant/reset-proof
    revocation, one-time proof consumption from two instances. Invalid identifier
    replacement must leave proof/state intact; valid concurrent consumption has
    one winner. Preserve the v0.11.1 distinction between atomic credential change
    and a separately consumed provider grant; do not invent refund semantics.
    Mount the authorization checks with each cacher while proving authentication
    has no shared-cache reads. Use local owned fixtures and fake delivery only.

11. **Firestore parity:** atomic fact/claim/role/audit/head commit and rollback;
    canceled/definitely aborted/unknown transactions; invalid/overflowing head;
    read-before-write ordering; reset on every retry; complete multi-hop/chunk
    snapshots; caller-ambient refusal versus internally owned snapshots; retained
    view closure and cancellation. Prove all raw/guarded/system writers update
    head, including a nil-cacher participating writer. Demonstrate that an old or
    unconfigured writer bypasses invalidation, then verify the activation runbook
    excludes it before cache traffic. Exercise upgrade/import/restore fences and
    epoch rotation. Never add external effects inside retry callbacks.

12. **Memory parity:** shared-bundle generation, snapshots, rollback/audit,
    concurrent writers and cache-fill races with both cacher implementations.
    Reject mixing independent authority bundles. Two-process coherence tests
    use a durable shared store; separate memory stores are separate authorities.

Real SQL tests must use the pinned Go connectors, actual transactions, and two
independent app processes with distinct pools/caches. A fake repository or Python
SQLite test does not close these gates. PostgreSQL runs both default and explicit
non-public schema and least-privilege variants. Turso runs local supported libSQL
first, then a separately authorized disposable target deployment with independent
clients and controlled replica lag/routing. Failure to establish that deployment
contract blocks its cache support, not the uncached adapter.

Firestore runs an owned emulator/database first, then an explicitly authorized
disposable GCP database with required indexes ready, independent clients/processes
and actual head contention. Its current publication manifest records emulator-only
verification and open production snapshot, contention, index and limit evidence.
That historical exception does not waive this feature's real-GCP gates. No cloud
tests or index deployment are authorized by this planning update. Missing GCP
evidence remains a release gate, not a reason to defer Firestore implementation.

Keep shared core conformance parameterized by a `cacher.Storer` factory; core and
SQL store tests can use SDK LRU/fakes. Real Redis and combined mounted
authentication+authorization fixtures belong in an **owned temporary host module**
that imports the required adapters. Do not add goredis/Redis dependencies to the
authorization core or SQL-store modules. Do not put foreign-pocket imports even
in external store `_test.go` files: `guard-store-no-foreign-pocket` scans those
too. Existing own-pocket authentication HTTP/store regressions stay in their
current modules. Record temporary fixture source with verification artifacts so
the combined proof is reproducible.

Fixtures are explicitly owned: unique temporary DB files, unique PostgreSQL
database/schema, isolated Redis process/port and synthetic credentials. Never
inherit a developer `*_DATABASE_URL`/provider configuration into destructive tests.
Preflight fixture identity and bind only loopback; cleanup only recorded owned
resources. Existing Redis, development databases and emulator data are untouched.

### Benchmark matrix and acceptance

Compare direct store, enabled-but-bypassed, LRU, and Redis with the same pinned
source, data, model, network placement and offered load. Run cold/warm, all-hit,
partial-hit and high-mutation workloads. Use two processes; report per-process
LRU locality separately from shared Redis reuse. Benchmark:

| Path | Required variation |
|---|---|
| `Check` / `CheckExplain` | Direct relation/role; nested usersets/through; allow/deny; graph depth/fanout; repeated and unique queries. |
| `CheckBatch` | 1/10/100/500 requests within configured limits; homogeneous/mixed kinds; repeated ancestors vs unrelated resources; partial misses. |
| Lookup/filter | Normal/near-overflow/overflow; multiple cursor pages and sparse `FilterPage`; verify no cache overhead/regression. |
| Login/session | Mounted password login with real configured hashing, `Live`, refresh/logout/reset/replacement; caching remains out of scope, measure regression baseline. |
| Mutations | Raw/guarded changes, bulk moves, role grant/revoke, last-guardian refusal, audit on/off; trigger overhead, head-row contention and reader interference. |

Run the matrix on all three durable backends and the memory reference. Report
Firestore document reads/writes, RPCs, query chunks, transaction attempts, head
contention and index amplification separately from SQL/Redis round trips. Compare
both strict no-protocol baseline and writer-invalidation-only overhead.

Report sample count, warmup, duration, repetitions, throughput and p50/p95/p99;
errors/timeouts, CPU/allocations/bytes, pool wait, lock hold/wait, retries,
generation churn, entry and whole-operation hit rates, staging drops, and recovery
time. Count SQL statements **and physical round trips**, Redis commands **and
physical round trips**, including snapshot control and background polls. Include
open-loop offered-load measurements so stalls do not disappear from percentiles.

Source-derived expectations, **not measured performance**:

| Path | Request SQL | Cache I/O / background cost |
|---|---|---|
| Direct / disabled | Existing `Q` | None. |
| Enabled, all reads hit | Zero | `K` byte-cache reads for `K` distinct tuple queries; LRU is local, Redis may take `K` RTTs. Existing call-local memo still reduces repeats. |
| Enabled, partial hit/miss | Begin + head + full snapshot queries + close | Wasted partial cache reads plus bounded fills; more expensive than an ordinary direct miss. |
| Cold/unhealthy/ambient bypass | Existing `Q` | No fill; no request cache I/O. |
| Poll | One authoritative head SELECT per process per poll | Approximately `N / interval` SELECTs per second for `N` processes, plus connection/routing costs. |
| Mutation after migration | Existing write/commit path with trigger work | Head-row lock/update in SQL; no Redis call. SQLite may update head once per changed row. |

For Firestore, a poll is one server document Get. A changed participating
transaction adds a head read and write to its existing transaction; actual RPC
counts depend on the existing batched paths. Memory incurs lock/clone work with
no database round trips. Measure these separately from the SQL estimates.

Do not assume `GetMany` removes dependency-shaped graph reads: later keys may not
be known yet. v1 can use existing batching/memoization; Redis pipelines, richer
cache ports and selective generations require new evidence. Store-wide churn
can erase reuse and serialize writers. Fix query/index/placement issues first if
direct store meets the host's target more simply.

Record host SLOs and a minimum useful latency/cost improvement **before** running
comparisons. Keep safety gates absolute: zero inconsistent-generation grants,
zero cache-derived guard decisions and zero post-bound cache grants in controlled
schedules. Choose LRU/Redis/disabled per measured workload; no predetermined
percentage speedup or invented measured result is asserted by this plan.

## Dependency-ordered tasks

Task metadata uses `model: inherited` because the historical planner labels do
not exist in this runtime. Execute sequentially unless independent fixture work
is explicitly delegated. New test filenames and symbols below are planned.

Verification shorthand, expanded here to make commands reproducible:

- **module-check(PATH):** `(cd PATH && go build ./... && go test ./... && go vet ./...)`.
- **core-race:** `(cd pockets/authorization && go test -race -count=1 ./...)`.
- **cross-check:** `make guard` then `make check` with live-service variables unset;
  inspect/report skipped live cases. Do not run Go commands at the module-less root.
- **durable-cache:** execute the new owned-fixture harness in task 4a, which runs
  pgx tests with `POSTGRES_TEST_DSN` (default and `POSTGRES_TEST_SCHEMA`), Turso
  `go test -tags=integration -count=1 ./...` with fixture URLs, and Redis tests
  against its owned process. It must fail if a required leg skips. Planned command:
  `python3 .github/scripts/authorization_cache_verify.py --mode all --report /tmp/authorization-cache-all.json`; during SQL
  work use `--mode sql`, then `--mode mounted`/`--mode redis` for the added fixture
  legs. `--mode benchmark --report /tmp/authorization-cache-benchmark.json` runs the matrix after correctness passes.
- **firestore-cache:** owned runner `--mode firestore` uses an isolated emulator
  with store/connector `go test -race -tags=integration -count=1 ./...` suites.
  `--mode firestore-live` uses existing disposable live-test configuration with
  `FIRESTORE_LIVE_REQUIRED=1` and `-tags='integration,live'`; actual execution is
  required, not compile-only success. Preserve the named allowed ambient-suite
  skip and reject other required-test skips. Live execution requires separate
  authorization; this plan does not open a real GCP database.
- Format touched Go files with the repository's established `gofmt` workflow;
  use `git diff --check` on every task. No generated files need hand-editing.

### task-1: Lock the release base and acceptance contract

- **depends_on:** []
- **model:** inherited
- **files:** `plans/authorization-cacher-implementation.md`, `plans/cacher-design.md`
- **verify:** `git status --short`; `git worktree list`; `git log -8 --oneline`;
  `git ls-remote --tags origin`; inspect final manifests/tagged source; `git diff --check`.
- **description:** Select an isolated implementation checkout containing latest
  published security fixes, preserving owner work. Record supported database/
  driver versions, trigger/primary-route prerequisites, proposed protocol examples
  and benchmark thresholds; reconfirm this is an explicit stale-read mode.

### task-2: Extract operation-specific read seams without caching

- **depends_on:** [task-1]
- **model:** inherited
- **files:** `pockets/authorization/logic/relationships/read_model.go`,
  `pockets/authorization/logic/relationships/service.go`,
  `pockets/authorization/logic/relationships/explain.go`,
  `pockets/authorization/logic/decisions/composite.go`,
  `pockets/authorization/logic/decisions/roles.go`,
  `pockets/authorization/logic/decisions/read_cache_test.go`
- **verify:** module-check(`pockets/authorization`), core-race, `make guard`.
- **description:** Add `CheckReader`/`CheckReadSource` and reader-parameterized
  evaluation helpers. Factor private uncached operation workers, pin the role
  filtering bypass and prove parity/limits/retained-reader behavior before adding
  a runtime. No behavior change for existing constructors.

### task-3: Add connector read-snapshot primitives

- **depends_on:** [task-1]
- **model:** inherited
- **files:** `integrations/datastores/turso/tx.go`,
  `integrations/datastores/turso/tx_integration_test.go`,
  `integrations/datastores/turso/README.md`,
  `integrations/datastores/pgxdb/tx.go`,
  `integrations/datastores/pgxdb/tx_test.go`,
  `integrations/datastores/pgxdb/README.md`
- **verify:** module-check for both connectors; owned live transaction tests with
  the actual driver; `make guard`; cross-check after cross-module integration.
- **description:** Implement additive `BeginRead`, preserving writer mode and
  cleanup. Prove simultaneous reader/writer behavior, cancellation/discard, and
  repeatable multi-query reads. Validate actual supported Turso route/server
  behavior; document unsupported replica configurations instead of guessing.

### task-4: Add optional protocol, construction and memory reference

- **depends_on:** [task-2]
- **model:** inherited
- **files:** `pockets/authorization/logic/decisions/read_cache.go`,
  `pockets/authorization/logic/decisions/constructor.go`,
  `pockets/authorization/logic/decisions/options.go`,
  `pockets/authorization/logic/relationships/service.go`,
  `pockets/authorization/logic/roles/service.go`,
  `pockets/authorization/config.go`, `pockets/authorization/options.go`,
  `pockets/authorization/constructor.go`,
  `pockets/authorization/stores/memory/memory.go`,
  `pockets/authorization/stores/memory/mutations.go`,
  `pockets/authorization/stores/memory/audit.go`,
  `pockets/authorization/stores/memory/read_snapshot.go`,
  `pockets/authorization/stores/storetest/read_cache.go`
- **verify:** module-check(`pockets/authorization`), core-race, `make guard`.
- **description:** Define source/policy/runtime contracts and construction errors,
  optional binding forwarding, memory generation and callback-scoped snapshot.
  Add a conformance harness with fake cache and deterministic barriers. Constructors
  stay inert; do not merge a publicly enabled partial protocol before tasks 5–7 pass.

### task-4a: Build the owned-process verification runner

- **depends_on:** [task-3, task-4]
- **model:** inherited
- **files:** `.github/scripts/authorization_cache_verify.py`,
  `.github/scripts/test_authorization_cache_verify.py`,
  `.github/scripts/authorization_cache_fixture/go.mod.tmpl`,
  `.github/scripts/authorization_cache_fixture/main.go.tmpl`,
  `.github/scripts/authorization_cache_fixture/fixture_test.go.tmpl`
- **verify:** `python3 -m unittest discover -s .github/scripts -p 'test_authorization_cache_verify.py'`;
  generate/compile the temporary baseline fixture; prove ownership preflight and
  cleanup against isolated local processes; capture the direct-store baseline.
- **description:** Follow the existing `.github/scripts` Python verification
  convention. The new adjacent template directory holds reviewable source for a
  temporary host module, not a production module or permanent example. The runner
  allocates a private directory, copies templates, pins the implementation source,
  opens owned SQL/Redis processes and starts two fixture app processes. Use a
  bounded control channel for race barriers and JSON reports for commands/results.
  Never source `.env`; reject unowned endpoints and clean up only recorded owned
  PIDs/containers/files. Start with direct fixtures; add feature cases in task 7.

### task-5: Install optional SQL invalidation and snapshot sources

- **depends_on:** [task-3, task-4, task-4a]
- **model:** inherited
- **files:** `pockets/authorization/stores/turso/turso.go`,
  `pockets/authorization/stores/pgx/postgres.go`, and in **each** of
  `pockets/authorization/stores/turso/` and `pockets/authorization/stores/pgx/`:
  `cache_migrations/0001_iam_cache_invalidation.sql`, `read_snapshot.go`,
  `cache_invalidation.go`, `relationships.go`, `roles.go`, `read_model.go`,
  `migrations_test.go`, `cache_invalidation_test.go`, `read_snapshot_test.go`,
  `README.md`
- **verify:** module-check for both store modules; durable-cache SQL legs;
  default/explicit PG schemas and writer permissions; `make guard`; cross-check.
- **description:** Export the separate migration source, implement triggers and
  capability probing/binding, then bind existing SQL readers to snapshots. Test
  all writer paths without adding Go write-through calls. Exercise savepoints,
  mixed old/nil-cacher writers, missing head, overflow and migration rollback.

### task-5f: Implement Firestore transaction and snapshot parity

- **depends_on:** [task-4, task-4a]
- **model:** inherited
- **files:** `pockets/authorization/stores/firestore/store.go`,
  `pockets/authorization/stores/firestore/cache_invalidation.go`,
  `pockets/authorization/stores/firestore/read_snapshot.go`,
  `pockets/authorization/stores/firestore/relationships.go`,
  `pockets/authorization/stores/firestore/roles.go`,
  `pockets/authorization/stores/firestore/writes.go`,
  `pockets/authorization/stores/firestore/mutations.go`,
  `pockets/authorization/stores/firestore/mutations_eval.go`,
  `pockets/authorization/stores/firestore/read_model.go`,
  `pockets/authorization/stores/firestore/cache_invalidation_integration_test.go`,
  `pockets/authorization/stores/firestore/read_snapshot_integration_test.go`,
  `pockets/authorization/stores/firestore/SCHEMA.md`,
  `pockets/authorization/stores/firestore/UPGRADE.md`,
  `pockets/authorization/stores/firestore/README.md`,
  `.github/scripts/authorization_cache_verify.py`,
  `.github/scripts/authorization_cache_fixture/fixture_test.go.tmpl`
- **verify:** module-check(`pockets/authorization/stores/firestore`),
  firestore-cache emulator leg, existing connector snapshot tests, `make guard`.
- **description:** Add explicit head initialization, writer/read options, binding
  and transactional generation at the common flush seam. Reuse connector
  ReadSnapshot and private fact helpers, enforcing closed views and preserving
  caller-ambient refusal. Test retries, missing head, overflow, no partial writes
  and nil-cacher writer participation. Add owned emulator fixtures and document
  activation/rollback/restore; real-GCP verification is required in task 7.

### task-6: Implement optional cache coordination and typed entries

- **depends_on:** [task-4, task-5, task-5f]
- **model:** inherited
- **files:** `pockets/authorization/logic/decisions/read_cache.go`,
  `pockets/authorization/logic/decisions/read_cache_readers.go`,
  `pockets/authorization/logic/decisions/read_cache_keys.go`,
  `pockets/authorization/logic/decisions/read_cache_test.go`,
  `pockets/authorization/logic/decisions/read_cache_keys_test.go`,
  `pockets/authorization/logic/decisions/composite.go`,
  `pockets/authorization/logic/relationships/service.go`,
  `pockets/authorization/logic/roles/service.go`
- **verify:** module-check(`pockets/authorization`), core-race; deterministic
  all-hit/miss/timeout/expiry/close/race cases; `make guard`.
- **description:** Implement serialized Poll/Close, age checks, key/envelope
  codecs, four read families and bounded staging. Cache-only evaluation falls
  back once for the entire operation; successful durable results survive fill
  errors. Keep direct services and mutation readers free of shared cache.

### task-7: Prove durable, HTTP and two-instance behavior

- **depends_on:** [task-5, task-5f, task-6]
- **model:** inherited
- **files:** `pockets/authorization/stores/storetest/read_cache.go`,
  `pockets/authorization/stores/turso/read_cache_integration_test.go`,
  `pockets/authorization/stores/pgx/read_cache_test.go`,
  `pockets/authorization/stores/firestore/read_cache_integration_test.go`,
  `pockets/authorization/stores/firestore/read_cache_live_test.go`,
  `pockets/authorization/logic/decisions/read_cache_test.go`,
  `pockets/authentication/inbound/http/identifiers_test.go`,
  `pockets/authentication/inbound/http/session_security_test.go`,
  `pockets/authentication/stores/storetest/challenge_grant_regressions.go`,
  `.github/scripts/authorization_cache_fixture/main.go.tmpl`,
  `.github/scripts/authorization_cache_fixture/fixture_test.go.tmpl`;
  change own-pocket regressions only for gaps in existing coverage
- **verify:** durable-cache, firestore-cache, core-race, mounted authentication HTTP
  store-conformance cases, module-check for affected cores/stores, cross-check.
- **description:** Complete all twelve correctness groups above. Run the mounted
  cross-pocket and Redis cases in the isolated task-4a host module; no foreign
  pocket imports in store tests and no Redis dependencies added to core/SQL
  modules. Reuse existing own-pocket regressions instead of duplicating them.

### task-8: Measure representative performance and recovery

- **depends_on:** [task-7]
- **model:** inherited
- **files:** `pockets/authorization/stores/turso/read_cache_benchmark_test.go`,
  `pockets/authorization/stores/pgx/read_cache_benchmark_test.go`,
  `pockets/authorization/stores/firestore/read_cache_benchmark_test.go`,
  `pockets/authorization/logic/decisions/read_cache_benchmark_test.go`,
  `.github/scripts/authorization_cache_verify.py`,
  `.github/scripts/authorization_cache_fixture/fixture_test.go.tmpl`,
  `plans/authorization-cacher-implementation.md`; raw reports under a recorded
  task-specific temporary directory with a durable evidence manifest in the plan
- **verify:** compile fixture binaries; preflight recorded process/DB ownership;
  run durable-cache and benchmark matrix; confirm both independent instances;
  record command lines with secrets redacted, source versions and raw results.
- **description:** Use the task-4a runner and existing store integration seams to
  run the complete benchmark matrix. SQL-module benchmarks use SDK LRU; Redis and
  combined login/session benchmarks run in the isolated host module. LRU first,
  Redis second; compare all background/read/write costs with direct baseline.

### task-9: Document adoption and prepare compatibility release

- **depends_on:** [task-7, task-8]
- **model:** inherited
- **files:** `pockets/authorization/README.md`,
  `pockets/authorization/stores/turso/README.md`,
  `pockets/authorization/stores/pgx/README.md`,
  `pockets/authorization/stores/firestore/README.md`,
  `pockets/authorization/stores/firestore/SCHEMA.md`,
  `pockets/authorization/stores/firestore/UPGRADE.md`,
  `integrations/datastores/turso/README.md`,
  `integrations/datastores/pgxdb/README.md`,
  touched modules' `go.mod`/`go.sum` as required,
  `AUDIT.md`, `RELEASING.md`, `plans/authorization-cacher-implementation.md`
- **verify:** cross-check; final supported-store/live gates; `GOWORK=off` isolated
  module builds against release candidates; compile public wiring snippets;
  migration export/parity tests and final diff review.
- **description:** Document precise optional/freshness contract, callback lifetime,
  nil behavior, worker ownership, privilege/restore runbooks and measured results.
  Only implemented changes enter audit/release notes. Plan dependency-ordered
  changed connector → core → changed store tags under RELEASING.md; publish only in a separately
  authorized release task, using versions newer than then-current releases.

### task-10: Plan each host's adoption independently

- **depends_on:** [task-9]
- **model:** inherited
- **files:** host-owned plans under each host's established plan location;
  framework plan cross-references only, without overwriting existing dirty handoffs
- **verify:** host H0–H10/`gopernicus guard`, migration rehearsal, two-instance
  canary/fallback and business lifecycle tests before any deployment.
- **description:** Choose namespace and accepted MaxStaleness, strict check call
  sites, DB route, worker settings and numerical SLO. Rehearse rollout/rollback.
  Treat Segovia's PostgreSQL-to-Turso security split as separate work requiring
  resource lifecycle design; cache availability is not that migration's approval.

## Sequencing, compatibility and reviews

Order: task 1 → tasks 2/3 → task 4 → task 4a → tasks 5/5f → task 6 →
task 7 → task 8 → task 9. Task 10 is a separate host adoption effort.
No user-visible partially configured cache should be released between these
steps. Implement in small reviewable commits on an isolated branch.

API additions are opt-in, but adding exported struct fields can affect unkeyed
external literals; include a compatibility note and consumer compile check.
Existing repository interfaces keep their method sets. No SDK port change or
Redis adapter change is expected; keep dependencies inward and SDK stdlib-only.
The new SQL source is additive and optional, but **installing it changes writer
cost and privileges database-wide**. Preserve historical migration files and
avoid upgrading the whole base source for a host that does not want caching.

Authentication modules need regression tests, not a new cache API or schema.
Firestore support is required in this phase, with native transactional invalidation
and snapshot reads. No-cache/no-invalidation configuration remains unchanged.
Cache-enabled construction against unsupported custom sources fails clearly.
Retired public receipts/revisions stay retired. Internal cache
versions are not authorization proof, replay protection or a host transaction
token.

Generated-artifact impact: none expected. No templ/OpenAPI/codegen output is to
be edited by hand; if public docs generation exists at implementation time, edit
its source and run the established generator.

Recommended reviews: architecture steward (ports/optional migration/H-rules),
lead backend engineer (snapshot/read algorithms and transaction cleanup), product
manager (explicit freshness contract), platform SRE (authoritative routing,
privileges, polling load, restore fencing and operational budget). These are
focused review gates, not authorization to publish.

Planning review completed with the named architecture steward and backend lead.
Their final corrections are incorporated: current `stores/storetest` paths,
cross-pocket/Redis fixtures outside core/store modules, invoker SELECT/UPDATE
privileges, timeout classification at the coordinator, and no unsupported claim
that existing memory guard views enforce closure. Their reviews found no
remaining fundamental flaw in the proposed generation/snapshot composition;
the implementation and deployment verification gates remain required.

The subsequent all-stores extension was source-reviewed with the backend lead.
Firestore connector/authorization files were compared against final v0.1.0 source
at `10e5f97b`; there was no source diff. The existing ReadSnapshot/flush/retry seams
support the proposed adapter work, with explicit writer activation replacing
SQL's trigger guarantee. No new live/emulator test or benchmark was run for this
planning extension, and the published emulator-only exception is not new cache
verification.

## Evidence, risks and remaining prerequisites

Planning validation: Markdown local links and code fences, task dependency order,
`git diff --check`, and hashes of both pre-existing owner handoffs passed. The only
repository changes for planning are this plan and its research continuation in
`plans/cacher-design.md`. Go build/test/vet, live SQL/Redis, mounted HTTP
and performance suites were not run: no Go implementation changed in this step.

The parent records source-backed original/current findings and the earlier SDK
LRU race probe. No new Go framework implementation or representative network
benchmark was run for this plan. Published release verification is prior recorded
evidence, not a rerun of all published test commands today.

A disposable Python/SQLite **3.45.3** prototype on an explicitly owned WAL database
applied the current seven authorization migrations and the proposed trigger
shape. Seven assertions passed: fact/head precommit invisibility and outer
rollback; atomic committed visibility and top-level affected-row counts;
savepoint rollback; duplicate/no-op behavior; two-connection snapshot consistency
across revocation; missing head and overflow aborting a whole multi-row statement;
and role updates sharing the same head from an unwrapped SQL connection.
Fixture/script:
`/var/folders/qm/bjn1lmt54tlf9fc8vblm1hc80000gp/T/gopernicus-authorization-cache-plan-rzk4449i/trigger_probe.py`.
Reproduce in a new owned directory/database; do not rerun against existing data.
This resolves the narrow SQLite trigger/rollback mechanism only. It does not
verify libSQL transport, PostgreSQL, Turso replicas, mounted HTTP or performance.

Highest risks, in order: accepting a revocation delay unintentionally; renewing
freshness from a lagging replica; missing a fact writer/restore fence; combining
incompatible generations; and store-wide head contention or low whole-operation
hit rates erasing any performance gain. The specified explicit policy, atomic
triggers, authoritative observation, whole-snapshot fallback and benchmarks address
these separately; TTL or post-commit deletion would not.

Remaining adoption prerequisites are concrete: the host's accepted positive
MaxStaleness and strict call sites; proof of supported Turso authoritative routing
and real Firestore snapshot/transaction behavior;
least-privilege/restore runbooks; the durable/HTTP/two-instance correctness gates;
and measured improvement against declared SLO/cost targets. These do not prevent
implementing the optional framework protocol, but they prevent claiming an
untested host is ready to enable it.

**Assessment:** This is useful and safely implementable as an optional Gopernicus
authorization read cache with an explicit bounded-staleness contract. Cache the
four model-aware tuple/role query families, begin with the existing LRU, and keep
Redis as an interchangeable cacher across Turso, PostgreSQL, Firestore and memory.
Build atomic store-wide invalidation, snapshot
ports, optional decision coordination, lifecycle handling and the specified
verification before adoption. Keep authentication and guarded decisions
authoritative; keep direct reads when strict freshness or measured economics
make them the better choice.

## Implementation execution — 2026-09-14

- Active plan: `plans/authorization-cacher-implementation.md` in isolated worktree
  `/private/tmp/gopernicus-authorization-cache-20260914`, branch
  `authorization-cache-20260914`, base `127284f6`. Original checkout and all four
  owner plan/handoff changes are preserved. No historical worktrees were pruned.
- Task 1 release check: remote tags were read successfully on September 14. Latest
  final authorization/core/store and connector versions match the source-authority
  table above. Base includes `5fb8d51c` authentication fixes and final manifests.
  Raw tag evidence: `/tmp/authorization-cache-remote-tags.txt`.
- Pinned drivers: libsql-client-go `v0.0.0-20260528064733-9d5d30a29a60`,
  modernc SQLite `v1.52.0`, pgx `v5.8.0`, Firestore `v1.25.0`. PostgreSQL 17
  is the owned fixture baseline. Cloud replica routes remain uncertified; local
  SQLite proof must not be reported as Turso Cloud authority proof.
- Explicit positive MaxStaleness remains mandatory, with no default revocation
  delay. SQL migration activation and Firestore all-writer activation retain the
  distinct contracts specified above. No host has enabled this mode.
- Task 2 is in progress under the named implementer charter, inherited runtime
  configuration because its historical model label is unavailable. No cache is
  publicly enabled by that refactor.
- Numerical performance acceptance targets requested from owner before measuring.
  Provisional proposal: at least 20% lower warm p95 latency and at most 10%
  mutation p95 regression; absolute correctness gates remain mandatory. These
  are evaluation thresholds, not measured results or a host adoption decision.
- Baseline verification: Turso connector `go test ./...` passed. PostgreSQL
  connector `go test ./...` passed against owned PostgreSQL 17.4, UTF-8 database
  `authorization_cache` on loopback port 64912. Initial cluster-default SQL_ASCII
  database failed the existing simple-protocol UTF-8 test; corrected the fixture
  by creating a UTF-8 database, with no production code change. Owned fixture
  metadata/logs: `/private/tmp/gopernicus-authorization-cache-evidence-hf3e09hk`.
- Live Turso/GCP verification needs
  separately authorized disposable targets; no existing service/data is owned
  merely because a CLI or environment setting exists.

### Completed foundation tasks

- Task 2 complete: six changed files in relationship/decision logic (read_model,
  service, explain, composite, roles, read_cache_test). Existing evaluators are
  reused and role filtering calls the private direct worker. Core build/test/vet,
  `go test -race -count=1 ./...`, `make guard` and diff checks passed. Independent
  named backend review found no blocking defects. Guard log:
  `/private/tmp/gopernicus-authorization-cache-task2-guard.log`.
- Task 3 connector primitives implemented: each connector's tx.go, README and
  read_snapshot_test.go, plus Turso tx_driver_test.go. Both modules passed
  `go build ./...`, `go test ./...`, `go vet ./...`, and race tests. PostgreSQL
  race tests ran against the owned fixture, including real repeatable reads,
  read-only enforcement and cancellation. Turso local file/WAL tests use the
  actual pinned driver and prove simultaneous writer commit/snapshot stability;
  transport failure/discard is covered by the existing driver fixture extended
  to deferred begin. Cloud routing remains unverified.
- Task 3 first guard attempt hit sandbox denial writing the default Go build
  cache; rerun uses `GOCACHE=/private/tmp/gopernicus-authorization-cache-gocache`.
- Task 4 in progress. Internal decisions tests currently import memory; adding
  memory's optional decisions.CacheSource requires adapting those tests to avoid
  a test import cycle, without moving production port ownership outward.

- Task 3 guard rerun passed with the temporary build cache. Backend review found
  no blocking issue. Strengthened the live PG cancellation test to assert pooled
  acquired-connection count returns to baseline; focused race rerun passed.
- Explicitly delegated independent task-4a fixture implementation alongside task 4
  (the sequencing exception for independent fixture work). Baseline fixture work
  does not depend on a publicly enabled partial cache.

- Task 5 preparatory backend review: freeze actual PG fact/head schema from
  catalog resolution, not current_schema(); verify full trigger event/condition/
  function properties and canonical bodies. Enabled SQLite reads must qualify
  main facts to avoid TEMP shadowing. Both adapters must preserve binding and
  private snapshot queriers through ForModel. Same-named no-op trigger/function
  replacements are required negative fixtures.

- Owned preliminary PostgreSQL trigger rehearsal passed: role insert/update,
  no-op update, rollback and TRUNCATE generation behavior. Synthetic invoker role
  without head SELECT privilege failed atomically; granting SELECT allowed the
  fact change with search_path excluding the fact schema, updating its proper
  head. Scripts and raw logs are `pg-trigger-probe.*` and
  `pg-trigger-privileges.*` in the owned evidence directory. These rehearse the
  proposed SQL only; they do not replace adapter conformance tests.

### SQL and owned-runner verification

- Task 4 completed: core build/test/vet/race and guards passed; independent backend
  review found no blocking issue. Root wiring regression covers relationship
  model + ordinary model-less roles service + cacher, matching the plan example.
- Task 4a complete: nine Python runner tests now pass. The generated temporary
  host module builds/tests/vets and two independent processes verify direct
  PostgreSQL and local Turso deny/grant/revoke. Runner owns all endpoints/processes,
  hashes copied module source, embeds fixture source in JSON and rejects skips.
- Task 5 implemented: separate optional migration sources, exact activation
  probes, frozen schema/binding and callback-scoped snapshots for both SQL stores.
  Both module build/test/vet passed. The copied-source owned runner recorded
  196 PostgreSQL and 187 Turso passing test events with zero skips, including
  enabled standard/audit conformance, all-column updates, rollback, no-cache
  writers, tamper rejection, privileges, TEMP shadowing and PG schema tests.
  Evidence: `/private/tmp/authorization-cache-task5-fixture.json`.
- Review identified and implementation rejects PostgreSQL RLS and inheritance
  for v1 enabled sources, because their visible facts can escape this head's
  writer coverage. Default direct stores retain their previous capabilities.
- Added the already-pinned modernc SQLite driver as a Turso store test dependency
  for owned hermetic file/WAL fixtures; no Redis or foreign-pocket dependencies
  were added to production/store modules. Isolated tidy required network/cache
  access; final go.mod/go.sum record the pinned dependency.
- Actual libSQL primary transport verified: owned container image
  `sha256:07d5da358f37f7f327cc33c1cb5278e7892e04225f011a65a76049372a308982`,
  loopback `127.0.0.1:49446`. Connector full integration/race suite and store
  `TestCacheLiveSnapshots` integration/race passed. This establishes local
  primary HTTP behavior, not cloud replica routing.
- Task 5f underway. Owned Firestore emulator uses exact CI image
  `sha256:45a15cc163d2df13137478856b4b9bb90426100fa8aec542269460c10110b727`,
  loopback `127.0.0.1:49448`; ownership records in the evidence directory.
- Independently delegated task 7 fixture preparation while Firestore is built;
  cache behavior tests cannot pass until task 6 enables coordination.

- Task 5 guard initially caught a test calling a forbidden raw connector handle.
  Replaced it with connector pool configuration at fixture creation; targeted
  TEMP-shadow race test and guard rerun passed. No guard was weakened. Migration
  export tests now compare exported bytes to the optional embedded source.
- Authentication core build/test/vet passed. The first test run was blocked only
  by the sandbox's httptest loopback listener restriction; the permitted local
  listener rerun passed. No authentication production behavior changed.
- libSQL fixture reports `sqld 0.24.33 (6f451a1f 2026-07-01)`. Full SQL store
  regression/race runs are underway against owned PostgreSQL and libSQL.

- Full authorization store regressions passed with race detection on owned
  PostgreSQL (22.039s) and libSQL HTTP (58.813s), including existing guards,
  mutation, audit and ambient transaction tests. Logs:
  `/private/tmp/gopernicus-authorization-cache-pgx-full.log` and
  `/private/tmp/gopernicus-authorization-cache-turso-full.log`.
- Shared snapshot harness corrected to use its outer context for independent
  writes and the supplied snapshot context only for snapshot reads. Firestore's
  existing caller-ambient refusal correctly exposed this fixture bug; no store
  ambient behavior was relaxed.

### Cache coordination and public behavior

- Full PostgreSQL explicit-schema regression/race passed; log
  `/private/tmp/gopernicus-authorization-cache-pgx-schema-full.log`.
- Task 5f implemented. Full emulator `^TestCache` conformance passed under race
  (130.531s), including existing store/audit contracts with participation enabled.
  Build/test/vet, integration/live-tag vet, guards and optionality test passed.
  Independent SQL + Firestore source review found no remaining blockers.
- Task 6 coordinator is implemented and initial decisions tests pass; deterministic
  lifecycle, expiration, regression and typed-payload coverage is being completed.
- Shared `storetest.RunReadCache` now passes on memory, PostgreSQL, SQLite and
  Firestore emulator (durable legs with race). It proves cold no-cache work,
  one-snapshot fill/fallback, warm hits, outage fallback, revoked deny after poll,
  uncached filtering, cancellation and a heterogeneous batch that must discard a
  cached grant when a later miss observes a different generation.
- Mounted authentication fixture preparation uses actual HTTP handlers, password
  hashing/session stores and a local fake delivery sink. No messages are sent.
- Memory benchmark matrix scaffolding compiles and runs all 40 cases at 1x;
  this smoke run is not performance acceptance. A 500-request warm case exceeded
  default staging entries, so the explicit all-hit benchmark policy raises that
  bound to 2048; production defaults remain unchanged. Bounded default staging
  behavior belongs in correctness/partial-hit measurements.
- Cloud target availability was requested separately. Local verification does
  not authorize cloud mutations and cannot satisfy real-GCP release gates.

- Task 6 complete: full core build/test/vet/race and final focused race passed,
  including rate-limited redacted state-transition logging. Architecture guard
  initially classified `json.NewEncoder` in canonical-key hashing as HTTP output;
  per-field `json.Marshal` plus framing preserves the key semantics and passes
  the unchanged guard. Independent coordinator security review found no blockers.
- `make check` passed repository-wide on 2026-09-14: all workspace module
  build/test/vet, generated-artifact checks, integration/live-tag compilation and
  architecture guards. Live tests are intentionally skipped in this hermetic
  command and covered by the separate owned fixtures where available. Log:
  `/private/tmp/gopernicus-authorization-cache-final-check.log`.
- Public README wiring snippet was extracted to
  `/private/tmp/authorization-cache-wiring.go` and compiled successfully.
- Mounted SQL × LRU/Redis HTTP regression passed all four cases; evidence
  `/private/tmp/authorization-cache-mounted.json`. Includes real password hashing,
  cookie/bearer Live, refresh/grace, foreign-account replacement refusal, logout,
  password replacement, concurrent single-use reset (one winner), replay denial
  and disable. Authentication routes made zero authorization-cache calls, also
  during cache outages. Delivery remains entirely in-process and synthetic.
- Owned Firestore runner passed emulator `TestCache` integration cases with zero
  skips plus two separate application processes for LRU and Redis, shared Redis
  reuse, participating nil-cacher revoke/poll, expiration and failure/recovery.
  Evidence `/private/tmp/authorization-cache-firestore.json`; ownership cleanup
  passed. The runner creates its own digest-pinned, token-labeled emulator.
- Reviewable isolated commits: `5453a90a` connectors; `7e7bf787` core/memory;
  `99b14e3b` SQL; `83fe32ef` Firestore. Existing owner changes remain untouched in
  the original checkout. The copied parent cacher-design notes are not part of
  these implementation commits.


### Final review and measurement status

- Final logging review found that invoking a host slog handler under the runtime
  mutex could deadlock a handler that calls Stats. Emission now occurs after both
  the mutex and polling gate are released; protected transition/rate-limit state
  is unchanged. Reentrant handler tests were added; source review cleared the
  fix. Final race execution follows the uncontended benchmark window.
- Prepared five cache-specific `integration,live` Firestore roots for standard
  conformance, audit, snapshots, public cache behavior and an independent
  participating writer. These use existing OpenLive/ResetLive safeguards and
  required index probes. They are not recorded as executed against GCP.
- Memory reference microbenchmarks completed 40 cases × 3 repetitions at 50ms
  minimum timed duration. Raw results and parsed samples:
  `/private/tmp/authorization-cache-memory-bench.txt` and
  `/private/tmp/authorization-cache-memory-summary.json`. At 500 seeded grants,
  median mean operation cost for Check allow was 4.873ms direct versus 5.494µs
  warm; role Check was 221ns direct versus 2.574µs warm. Filter cost remained
  about 0.49–0.52s and performs no cache I/O. These are local CPU/allocation
  benchmarks, not p95 or host adoption evidence. Runtime-close log lines split
  some raw benchmark lines; the parser preserves all 40 × 3 samples. Subsequent
  benchmark sources suppress lifecycle output only during benchmark execution.
- Durable microbenchmarks separate strict no-metadata baseline, writer-only,
  cold bypass, warm LRU and cache miss; include 1/10/100/500 batch sizes, graph,
  role and explanation paths plus audit on/off write pairs. All 78 cases passed
  smoke on each durable backend. Measured 3-repeat SQL runs passed; Firestore
  measurement is underway. Source hashes and exact commands are recorded in
  `/private/tmp/gopernicus-cache-bench-durable-provenance.json`.
- Early SQL sequential measurements show significant warm-read gains but
  roughly 12–50% metadata write-pair overhead and 2.2–2.4× cold-role overhead.
  These do not satisfy a universal “caching always helps” claim. Scheduled-load
  p95 and provisional threshold comparisons remain a distinct next measurement.

- Durable measurements completed: 78 cases × 3 repetitions per backend at 100ms
  minimum timed duration. All passed; logs and parsed summaries retain every
  sample. These medians summarize per-repetition **mean ns/op**, not p95:

| Local backend | Check allow, direct → warm | Batch 500, direct → warm | Cold role / direct | Writer-only / baseline, audit off/on |
|---|---:|---:|---:|---:|
| SQLite/Turso adapter | 4.563ms → 6.248µs | 9.823ms → 0.424ms | 2.24× | 1.12–1.50× |
| PostgreSQL | 0.370ms → 5.888µs | 9.986ms → 0.384ms | 2.44× | 1.04–1.50× |
| Firestore emulator | 938ms → 8.447µs | 1042ms → 0.544ms | 2.44× | 1.30–2.05× |

The emulator's large graph-walk costs at 500 grants are not real-GCP latency
claims. Cache misses and centralized invalidation have measurable costs; host
adoption must use the intended query mix, mutation rate and actual deployment.

- Memory write benchmarks also passed all 8 cases × 3 repetitions (50ms minimum);
  raw log `/private/tmp/authorization-cache-memory-write-bench.txt`.
- Final core `go build ./...`, `go vet ./...` and full `go test -race -count=1
  ./...` passed after the logging fix. New live test entrypoints pass
  `go vet -tags=integration,live ./...`; they remain unexecuted on GCP.
  Final race log `/private/tmp/authorization-cache-final-core-race.log`.
- Final logging fix commit `5645d6f4`; prepared live conformance `63637984`;
  benchmark harnesses `f8f45c8d`. The scheduled-load runner is now measuring
  source-identical two-process latency before its final all-mode correctness run.

- Scheduled-load initial report is retained at
  `/private/tmp/authorization-cache-benchmark.json`, including its failed status:
  PostgreSQL completed, then SQLite concurrent writes returned `SQLITE_BUSY`.
  Cleanup passed. PostgreSQL provisional performance acceptance was false:
  one writer process exceeded the 10% regression budget and several Redis read
  cases missed the improvement target. Default-capacity batch-500 cases had zero
  complete cache hits; these must not be described as all-hit warm measurements.
  A continuation retains the same offered load and repetitions while recording
  per-operation errors so the remaining SQLite and emulator matrix can finish.
  Such errors fail acceptance even when measurement proceeds.
- Implementation remains on branch `authorization-cache-20260914` in
  `/private/tmp/gopernicus-authorization-cache-20260914`; it has not been applied
  to the user's original `firestore-release-20260911` checkout. The user flagged
  this visibility gap; the worktree and primary implementation paths were
  explicitly provided. Original checkout changes remain preserved.

- User authorized local merge on 2026-09-14. Original branch
  `firestore-release-20260911` fast-forwarded from `aa75aa07` to `f8f45c8d`,
  including the two prerequisite security/release commits and eight cache
  implementation commits. All four pre-existing owner plan files were verified
  byte-for-byte unchanged; implementation paths match committed HEAD. Preflight
  hashes and the 127-file incoming manifest are preserved in
  `/private/tmp/authorization-cache-merge-preflight.json`. Nothing was pushed.
  Uncommitted verification runner and this evidence plan remain in the isolated
  worktree pending final checks; performance/cloud release gates remain open.


### Final local qualification and release preparation

- Final owned `--mode all` passed after configuring a bounded, per-connection
  SQLite fixture busy timeout (5000ms), preserving four connections and the
  concurrent single-use reset assertion. Earlier zero-timeout runs returned
  `SQLITE_BUSY` from beginning the losing transaction, including direct/no-cache
  mode; no production code or error assertion was weakened. Independent backend
  review accepted the fixture correction and final reporting.
- Final report `/private/tmp/authorization-cache-final-all-busy-timeout.json`:
  PostgreSQL 199 and Turso 190 adapter cases, no skips; two-process LRU/Redis
  behavior on PostgreSQL, Turso and Firestore emulator; mounted direct/LRU/Redis
  authentication on both SQL stores; owned-resource cleanup passed. Separate
  final Firestore correctness report also passed:
  `/private/tmp/authorization-cache-final-firestore.json`.
- Runner ownership/reporting regressions: all 14 passed. Command timeouts kill
  and reap the owned process group, and mounted benchmark errors count against
  acceptance. Diagnostics contain error type/message without reset-token input.
- Scheduled-load evidence is preserved, including failures. PostgreSQL and Turso
  provisional performance acceptance failed. Turso continuation records two
  operation errors; default-capacity large batches saturate the unchanged load.
  Firestore's direct matrix hit its 60-second operation deadline during warmup,
  before cache legs; full Firestore scheduled-load performance remains unmeasured.
  Reports: `/private/tmp/authorization-cache-benchmark.json`,
  `/private/tmp/authorization-cache-benchmark-remaining.json`, and
  `/private/tmp/authorization-cache-benchmark-firestore.json`. The later fixture
  timeout correction does not retroactively change these measurements.
- All framework implementation is present in the original checkout at `f8f45c8d`.
  Release preparation is tracked in [authorization-cache-release.md](authorization-cache-release.md).
  Real GCP, Turso Cloud authoritative routing and representative performance
  acceptance remain explicit release gates. Local passing tests do not waive them.


### Publication follow-up

The owner explicitly accepted the stated cloud/performance qualification gaps
and authorized the exact GitHub destination and six-module release. All six tags,
including authorization core `v0.14.0`, are published from `9e67a165`; remote
identities and preservation of prior refs passed. Public consumer verification
is tracked in the release plan. No host adoption or deployment was performed.
