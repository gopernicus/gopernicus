# Authorization TupleCache

Status: COMPLETE for upstream implementation and local verification — 2026-09-15.
Unreleased; host adoption and deployment-specific checks remain below. The owner selected raw tuple caching
before any DecisionCache, and retirement of the previous global-generation
cache. The owner clarified that Redis holds a maintained raw tuple mirror,
reconstructed from the configured store when necessary; processed outbox events
are disposable delivery work, not the reconstruction source. The owner authorized building it
and clarified that the configured STORE is authoritative: support both Turso and
PostgreSQL behind one database-independent source contract.

## Context

The previous cache changes every cache key's namespace after any authorization
mutation. It also caches derived group-expansion answers, despite being described
as a fact cache. Under sustained writes the global invalidation undermines reuse.
The owner wants the configured store to remain the durable authority, a
transactional outbox to retain actual tuple mutations, and Redis to serve raw
relationship reads.

This plan supersedes the active design direction in
[authorization-cacher-implementation.md](authorization-cacher-implementation.md).
That document and published migrations remain historical records. Preserve the
completed [Through batching](authorization-through-batching.md) work.

## Goal

Prove a TupleCache that serves batched raw relationship reads and applies targeted
committed changes reliably, without storing permission answers or invalidating
unrelated relationships.

## Agreed boundaries

- The public name is **TupleCache**. No DecisionCache implementation, expanded
  membership cache, permission boolean cache, or cached authorized-resource list
  belongs in this work. Permission evaluation continues on every request.
- The configured Turso or PostgreSQL store is the durable source of truth; Redis
  is reconstructible. Redis failure after SQL commit does not undo a write.
- Redis holds the configured tuple store's complete raw mirror and indexes.
  Requests do not populate individual sets on demand. Initial population and
  recovery read authoritative tuples from the configured store; the outbox
  delivers subsequent committed changes. Keep the public name TupleCache.
- Successfully processed outbox records may be deleted. They are pending work,
  not permanent event history and not the source of truth for rebuilding Redis.
- Tuple mutations and full outbox payloads commit in the same SQL transaction.
  Include operation, complete old/new identities as applicable, and ordering
  information. Subject relation is part of identity.
- A post-commit channel notification wakes the delivery path; polling recovers
  missed notifications and interrupted delivery. Acknowledge only successful
  Redis application. Use an explicitly ordered relay; do not assume the generic
  events poller's timestamp ordering or lack of row claiming solves this.
- Ordinary writes change only the raw indexes containing the changed tuple.
  No global or tenant generation participates in cache keys or invalidation.
  An outbox sequence may order delivery; it is not a cache-wide invalidation
  version or a public consistency token.
- Keep model filtering, tuple identity, host-wide limits, canonical ordering,
  per-request budgets, mutation guards and the existing batch evaluator.
- SQL enumeration remains available. Making all resource enumeration run in
  Redis is a separate indexing task, not a prerequisite for proving TupleCache.
- Keep exact role reads durable initially. Do not disguise role or expanded
  permission answers as relationship tuples.

## Raw read shapes

The present evaluator requires two indexes over the same raw facts:

| Lookup | Raw result |
|---|---|
| Resource type, ID and relation | Exact subject references |
| Exact subject type, ID and relation | Resource type, ID and relation references |

Both support batches. The reverse index includes ordinary direct grants as well
as userset memberships. Current Direct evaluation computes the principal's full
reachable closure and reports overflow before returning a match. Replacing it
with a resource-first walk would change the existing search-limit contract.

Redis may hold model-independent tuples. Every edge consumed by permission
evaluation must still satisfy `ReadModel.Allows`; model narrowing must immediately
stop obsolete tuples from granting authority. Do not rely on ambiguous printable
tuple strings as a serialization format: references are opaque exact strings.

## Population, persistence and recovery

- Redis persistence can retain the mirror across restarts. RDB snapshots and AOF
  are host configuration, not substitutes for store authority. With AOF's
  every-second fsync policy, a crash can lose recent acknowledged writes.
  [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).
- Rebuild from a consistent authoritative tuple snapshot into a replacement mirror,
  coordinating an outbox cutoff so concurrent changes are neither lost nor
  applied in reverse order. Protect the required catch-up work from deletion
  while rebuilding; this is temporary coordination, not permanent event history.
- Do not serve an incomplete replacement. Use durable reads until recovery has
  established a usable mirror and its catch-up boundary.
- A restored Redis dataset may be older even if it is nonempty. Detect/reconcile
  that state or rebuild it from the configured store before trusting it. Pending events alone
  cannot restore already-acknowledged changes lost by Redis persistence.
- Normal TTL expiry or arbitrary key eviction must not silently turn part of a
  full mirror into apparent absence. Define capacity/error behavior and readiness
  together; a missing healthy-mirror relation means empty only when completeness
  is established.

## Remaining scope and read-contract decisions

**Adapter scope:** Turso and PostgreSQL sources, Redis backend and an in-memory
backend for conformance. Firestore and the standalone memory authority remain
usable through durable reads; this first replacement does not advertise a
maintained TupleCache source for them. Remove the old active cache API/runtime
across adapters; preserve historical migration files and useful read snapshots.

### Implementation protocol

- Each source has a stable store identity and an acknowledged delivery receipt.
  The mirror binding also includes MaxStaleness; sharing a namespace with a
  different freshness policy fails explicitly.
  These bind recovery to the correct store; they never appear in per-set keys.
- One consistent source snapshot reads its acknowledged receipt and ALL visible
  pending mutations. On absent/mismatching Redis receipt it also reads all current
  tuples. SQL event IDs identify deletions/ordering, not a commit-order watermark:
  PostgreSQL sequences can commit out of order. Delete exactly acknowledged IDs.
- Publish a whole captured batch atomically to Redis's raw forward/reverse
  indexes, conditional on its previous receipt. Then atomically acknowledge the
  new receipt and delete captured outbox IDs in the source. A failed source ack
  leaves recoverable pending work; a receipt mismatch rebuilds from source facts.
  Concurrent relays cannot overwrite each other's newer publications.
- Redis keeps the mirror and its metadata in one hash, so eviction/loss cannot
  leave some tuple keys present and others silently missing. Ordinary batches
  change only affected index fields. Full rebuilds prepare a temporary hash and
  conditionally swap it into place; requests never see a partial build.
- An operation captures a mirror receipt and requires it on every raw read and
  at completion. If publication intervenes, retry the operation in a durable
  source snapshot. This receipt check does not delete data or require refills.
  Publish ALL mutations from the source snapshot together to avoid exposing half
  of a SQL transaction. Large batches are a measured resource cost, not silently
  split into separately visible transactions.
- Host-supplied positive MaxStaleness bounds eligibility. Successful source
  observation renews Redis eligibility for only the remaining bound measured
  from the start of that observation. Redis expiry checks use Redis's clock.
  A stalled relay or unavailable mirror uses durable reads; no request fills.
- Relationship-only decisions use TupleCache. Role and mixed-kind operations
  use the durable source snapshot, preserving coherence without caching role
  booleans. Resource enumeration remains durable for this proof.

Eventual delivery is accepted as a design direction, not a claim of an atomic
SQL/Redis transaction. Define the host-visible lag/failure behavior before
enabling cached permission reads. Atomic application of one tuple to its indexes
does not by itself preserve a multi-tuple SQL transaction or make a multi-read
permission evaluation one snapshot. Preserve coherent evaluation through a
specified read protocol/durable fallback, or explicitly agree to a changed
contract; do not silently conflate that choice with accepting delayed delivery.

## Module / API impact

- Own tuple-specific types and read/delivery ports in authorization core, under
  `pockets/authorization/logic/tuplecache` or the existing relationship concern
  where inspection shows that is simpler. Public wiring uses TupleCache naming.
- A tuple-specific Redis adapter belongs in an authorization sibling store
  module. It must not make the generic Redis integration import authorization,
  or make authorization core import a Redis driver.
- The current SDK `cacher.Storer` does not promise conditional updates or atomic
  index mutation. Do not implement mutable sets with unprotected Get/Set calls.
  Prefer a narrow consumer-owned contract over expanding SDK for this one use.
- Reuse host-owned worker lifecycle and appropriate existing outbox mechanisms
  through ports. Core cannot import the events pocket. Do not create a general
  authorization-specific jobs subsystem.
- Remove generation-based runtime wiring and derived cache families once the
  replacement path is proved. Retain useful durable read-snapshot boundaries,
  ambient-transaction bypass and guard isolation independently of generations.
- New module registration, if needed, includes `go.work`, Makefile module lists,
  architecture inventory and module-graph checks. No generated UI artifacts.

## Schema / datastore impact

- Keep published `cache_migrations/0001_iam_cache_invalidation.sql` immutable.
  Supply a host-applied upgrade that installs full mutation capture and removes
  the previous counter triggers/table. Framework startup never migrates data.
- Cover create, delete and update (old and new keys), bulk/replace operations,
  no-op writes, rollbacks and supported raw SQL writers. Audit enablement must
  not determine whether changes reach TupleCache.
- Document cutover: old cache-enabled binaries cannot keep running against the
  replacement schema. New cache keys use a separate namespace; old keys may
  expire without a broad Redis deletion.
- Delete processed outbox work under the delivery/rebuild coordination contract.
  Redis loss/restore recovery uses authoritative tuples, including tuples whose
  original outbox events have long since been deleted.

## Definition of Done

- Only raw tuples/sets and cache/delivery bookkeeping exist in Redis. Warm graph
  checks expand and evaluate current raw data; no derived answers are retained.
- Mutating a group updates its raw indexes and changes subsequent dependent
  checks without changing unrelated resource cache entries.
- Cold, warm, empty and partially populated states have unambiguous behavior;
  unsupported, corrupt, unavailable or insufficiently fresh cache data uses the
  documented fallback and cannot manufacture authority.
- Replay after Redis success/SQL acknowledgement failure is safe. Delete/recreate,
  delayed delivery, lost wakeups, relay restart and Redis loss recover correctly.
- Rebuild succeeds after processed events have been deleted, including under
  concurrent tuple writes. A Redis restore that loses acknowledged changes is
  detected/reconciled rather than accepted merely because keys still exist.
- Model narrowing, mixed resource types, exact usersets, cycles and evaluation
  budgets match the durable evaluator. Existing Through batching stays effective.
- Actual SQLite and Redis exercise mutation delivery and recovery end to end;
  benchmark read reuse while unrelated tuples are continuously mutated.

## Tasks

### task-1: Finalize the raw cache protocol

- **depends_on:** []
- **files:** this plan; relevant existing reader and snapshot contracts
- **verify:** named backend/architecture review of storage, ordering and read guarantees
- **description:** Resolve adapter scope and specify the minimum atomic backend
  operations for the selected full mirror. State precisely what a read can
  promise while delivery is pending or SQL transactions change multiple tuples.

### task-2: Implement raw reads and cache backend

- **depends_on:** [task-1]
- **files:** authorization TupleCache core and tests; memory reference;
  Redis sibling adapter and tests; module inventories if added
- **verify:** focused package tests and race tests; real Redis conformance
- **description:** Implement batched forward/reverse reads, exact serialization,
  model filtering at consumption, completeness/readiness, and store-based full
  population/recovery. Store no expanded memberships or decisions.

### task-3: Capture and deliver committed mutations

- **depends_on:** [task-2]
- **files:** selected SQL adapter migration/source; delivery ports/runtime;
  transaction and recovery tests
- **verify:** real SQLite rollback/raw-DML tests and SQLite-to-Redis delivery tests
- **description:** Capture full changes transactionally, apply them in the
  specified order, and acknowledge only after successful cache application.
  Prove retry, duplicate and restart behavior, including all old/new indexes.

### task-4: Connect evaluation and retire the generation cache

- **depends_on:** [task-3]
- **files:** authorization root wiring; relationships/decisions read seams;
  selected adapters; replaced cache tests and documentation
- **verify:** durable-versus-cache parity, batch query counts and mutation-guard regressions
- **description:** Route evaluation through raw reads, retaining durable fallback
  and existing limits. Remove the previous active global-generation and derived
  cache paths. Document the API/schema migration for current adopters.

### task-5: Prove behavior under writes and failures

- **depends_on:** [task-4]
- **files:** end-to-end tests, benchmarks, this plan and adoption documentation
- **verify:** `go build ./...`, `go test ./...`, `go vet ./...` in changed modules;
  relevant `go test -race ./...`; complete `make check`; real local Redis/SQLite runs
- **description:** Measure cold/warm reads, steady unrelated writes, membership
  changes, delivery lag and recovery. Report physical SQL/Redis work and tested
  failure behavior, not just green unit tests. DecisionCache stays out of scope.

## Implementation and verification record

### Workspace and changed files

- Branch `main`, original HEAD `4fb07615`, workspace
  `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`. No commits or releases made.
- Preserved existing owner edits in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md` and the untracked Segovia handoff.
  Preserved the completed Through batching work and its query-count evidence.
- Core: `config.go`, `constructor.go`, `options.go`; `logic/tuplecache/{tuplecache,
  runtime,reader}.go` and runtime tests; decisions constructor/options/composite
  and new `tuple_cache.go`/tests; relationship/role binding forwarding. Removed
  retired `logic/decisions/read_cache*.go` implementation and old-only tests.
- Stores: new memory TupleCache backend; generation-free memory snapshot helper;
  shared `stores/storetest/read_snapshot.go`; retired old cache suites. Turso/PG
  constructors, bindings, snapshot readers, new tuple source implementations,
  optional `cache_migrations/0002_iam_tuple_cache.sql`, capture/recovery tests and
  adapter READMEs. Historical optional 0001 unchanged.
- New sibling module `stores/goredis`: raw backend, Lua publication/read scripts,
  actual Redis tests, go.mod/go.sum and README. Registered in go.work, Makefile
  and ARCHITECTURE module inventory (43 modules).
- Firestore: removed retired generation options/source and bump logic; ordinary
  writes/audit/Through behavior retained. Updated README, SCHEMA and UPGRADE.
- Documentation: authorization README, AUDIT-037, this plan and historical-plan
  superseding pointer. No host repository changes, deployment or production data.

### Protocol review and fixes

Final named backend review reported no remaining blocking findings after the
policy-binding fix. Review confirmed exact-ID acknowledgement, receipt CAS, atomic
index publication, full-source reconstruction, model filtering and budget parity.
It identified a mixed-policy freshness bug: another relay could renew a shared
mirror with a larger bound. Fixed by including the chosen MaxStaleness in the
stable mirror binding; mismatched processes cannot publish or use that mirror.
A deterministic regression covers both policies and stale revocation. This is
configuration binding, not cache-wide invalidation.

Core tests also cover initial durable fallback, successful bootstrap, idle
renewal without changed receipt, delivered revocation, failed SQL acknowledgement,
old Redis restoration after outbox disposal, publication during a check, expiry,
slow source observations, Redis failure, scoped-reader cancellation/panic,
provisional-result disposal after failed durable retry, role fallback, model
narrowing, cycles, exact usersets, closure budgets and duplicate/empty batch IDs.
Turso outbox decoding rejects invalid UTF-8 before JSON can replace it with a
different identity; valid replacement-character IDs remain exact.

### Actual SQL + Redis behavior

A disposable harness used local SQLite through the Turso adapter, PostgreSQL 17
and actual Redis over a Unix socket. Dataset: 366 documents inheriting through a
space and nested groups (369 initial tuples). Both authoritative sources passed:

| Measurement | SQLite/Turso | PostgreSQL |
|---|---:|---:|
| Cold batch SQL statements | 3 | 5 |
| Warm batch SQL statements | 0 | 0 |
| Unrelated create → deliver → check cycles | 100 | 100 |
| Decisions across those cycles | 36,600 | 36,600 |
| SQL statements during those checks | 0 | 0 |
| Redis commands during those checks | 700 | 700 |
| Cache hits / rebuilds during those cycles | 100 / 0 | 100 / 0 |

PostgreSQL's cold count includes transaction statements. Single local warm samples
were about 3 ms; these are synthetic Unix-socket measurements, not Segovia,
network or concurrent-load benchmarks. Group revocation denied from Redis with
zero SQL; unrelated raw sets stayed identical; processed outbox count was zero.
Restoring an old Redis dump and deleting the mirror both recovered from current
SQL tuples without retained historical events. The harness was rerun after the
policy-binding change. Dedicated PostgreSQL and Redis fixtures were stopped.

A 10,000-tuple reverse set with graph-state limit 100 denied with
`ErrEvaluationLimit`, zero SQL and three Redis commands. Both backends received
490,176 Redis bytes and allocated approximately 6.74 MB / 120,165 allocations per
sample (9.44 ms SQLite, 9.67 ms PostgreSQL). This demonstrates a physical limitation:
whole raw sets are decoded before the semantic state limit is charged. It is not
a byte/memory limit. Larger sets, backlog batches and full rebuild duration must
be measured for the adopting host.

Local harness/logs:

- `/tmp/gopernicus-tuple-cache-e2e/main.go`
- `/tmp/gopernicus-tuple-cache-e2e/both-final.log`
- `/tmp/gopernicus-tuple-cache-e2e/large-final.log`
- `/tmp/gopernicus-tuple-cache-pg/final-race.log`

### Verification commands

Passed focused/module checks:

- Repository formatter: `/Users/jrazmi/go/bin/goimports -w <changed Go files>`.
- Authorization core: `go test -race ./...`; final focused decisions/TupleCache
  race run after the freshness-binding and result-disposal tests.
- Turso: `go build ./...`, `go test ./...`, `go vet ./...`, full
  `go test -race ./...`, integration-tag compile-only, final targeted race tests.
- PostgreSQL: build/vet and full live race tests, including a named schema:
  `POSTGRES_TEST_DSN=<disposable local PG> POSTGRES_TEST_SCHEMA=tuple_cache_suite
  GOCACHE=/tmp/gopernicus-tuple-cache-go-cache go test -race -count=1 ./...`.
- Redis: build/vet and `GOCACHE=/tmp/gopernicus-tuple-cache/cache
  go test -race -count=1 ./...` with real temporary Redis, AOF restart, concurrent
  publication, delayed reads, lost build key and ACL-induced mid-script failure.
- Firestore: build/test/vet/race and integration/live-tag compile/vet only.
- `git diff --check`.
- Final Turso follow-up: full `go test ./...` and focused race tests for raw
  mutation capture and malformed UTF-8 payloads passed after preserving raw
  escaped-text round trips. The initial overstrict decoder validation failed the
  raw newline fixture and was narrowed to prevent lossy decoding only.
  Logs: `/tmp/gopernicus-tuple-cache-turso-final-all.log` and
  `/tmp/gopernicus-tuple-cache-turso-final.log`.

Full repository gate **passed** (43 modules: build, test, vet, generated drift,
integration/live compile checks and architecture guards):
`env -u POSTGRES_TEST_DSN -u POSTGRES_TEST_SCHEMA -u TURSO_DATABASE_URL
-u TURSO_AUTH_TOKEN -u FIRESTORE_EMULATOR_HOST -u FIRESTORE_PROJECT_ID
GOCACHE=/tmp/gopernicus-tuple-cache/cache make check`.
Log: `/tmp/gopernicus-tuple-cache-check.log`. Initial sandbox attempts failed on
Go's default build-cache permissions and local socket binding; the task-local
cache and approved test-socket execution resolve these environment constraints.

### Remaining deployment verification

Remote Turso and authoritative/replica routing, a non-C PostgreSQL locale,
Firestore emulator/live cleanup, application-scale concurrent load and capacity,
and actual Segovia/GPS-360 adoption are unverified. No release/deployment is part
of this task. Host rollout requires the documented optional migration, shared
freshness policy, supervised worker and post-commit notification wiring.
