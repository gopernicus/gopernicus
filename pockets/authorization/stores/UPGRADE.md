# Authorization schema setup and API adoption

Authorization ships a fresh canonical SQL schema for PostgreSQL and SQLite/Turso.
Hosts export and apply it before constructing repositories. Framework constructors
validate the applied schema; they do not create tables or run migrations.

## Adopting the direct-hit short circuit (v0.26.0)

Upgrade authorization core. No host code change, no migration and no new
option is required; the short circuit is on for every `Direct` relation. See
[Direct-grant short circuit and read costs](../README.md#direct-grant-short-circuit-and-read-costs).

**Budget-edge answers change.** A principal whose subject-first expansion
exceeds `MaxGraphStates` used to receive `ErrEvaluationLimit` (503) even when
directly granted. Now a direct hit grants, and on a relation that admits no
userset subject a miss denies. Every other overflow, including a miss on a
relation that admits usersets, still returns `ErrEvaluationLimit` and is never
a deny.

**Explain outcomes gain `direct_hit`.** `model.ReasonDirectHit`
(`"direct_hit"`) can now appear in `ExplainStep.Outcome`, on a
`Kind: "direct"` step answered by the principal's own tuple. Code that compares
step outcomes to `ReasonGranted` must also accept `ReasonDirectHit`.
`CheckResult.ReasonCode`, `Explanation.Decision` and `Reason` are unchanged.

**Store modules.** Store runtime code is unchanged and existing store tags work
with authorization v0.26.0. The PostgreSQL, Turso and Redis store test suites
call the new `storetest` equivalence suite, so those modules repin
authorization to v0.26.0 for their own tests.

**Misses on userset relations cost more.** A single check that misses on a
relation admitting usersets pays one extra round trip before expanding (about
30 µs on loopback PostgreSQL). On SQL stores, `CheckBatch` and
`FilterAuthorized` add statements for each Direct level they probe: in the
benchmark graph, `ceil(n/100) + 1` more for n requests (7, 8, 10 and 16
statements instead of 5, 5, 5 and 9 at n = 50, 128, 366 and 600). Requests that
probe the same fact in one round share one read. This matters
most on remote Turso, where each statement is a network round trip.

**Wide resources.** With TupleCache on, the probe decodes the resource's whole
forward set, so a check on a resource with 10,001 direct grantees goes from
about 4 µs to about 1 ms and allocates about 3.1 MB. On a vacuumed and analyzed
PostgreSQL table the same check gets faster (about 148 µs to 112 µs). On a
freshly loaded or un-vacuumed table, PostgreSQL's cached generic plan for the
exact probe can prefer `idx_iam_tuples_relation_resource` and filter every
grantee (about 0.83 ms) until autovacuum runs; resource-scoped `RoleIn` checks
and batch exact reads already share that exposure. After a bulk load, run
`VACUUM ANALYZE iam_tuples` rather than waiting for autovacuum. Principals with many grants
checked on small resources get much faster on both. See
[BENCHMARKS.md](../BENCHMARKS.md#direct-hit-short-circuit-v0260--published-2026-10-14).

## Adopting operation fences (v0.24.0) — a one-way migration

Upgrade authorization core with PostgreSQL store `v0.18.0` or Turso store
`v0.17.0`. SQL hosts using `Repositories` **must apply**
`migrations/0004_iam_operations_fenced.sql`; it widens the `iam_operations`
outcome check to accept `fenced`. `RelationshipRepository` alone does not
require it. Copy only `0004_iam_operations_fenced.sql` as the next entry of the
host's authorization migration stream, after `0003_iam_operations.sql`; never
edit an applied file.

- PostgreSQL: one `ALTER TABLE … DROP CONSTRAINT …, ADD CONSTRAINT …` on the
  unqualified table, resolved through the host's `search_path`/schema.
- SQLite/Turso: a table rebuild (create `iam_operations_next`, copy, drop,
  rename) inside the migration transaction.

**PostgreSQL lock guidance.** The 0004 `ALTER TABLE` takes an ACCESS EXCLUSIVE
lock on `iam_operations`, and `pgxdb.RunMigrations` sets no lock timeout and
runs the whole stream in one transaction. If any session holds a lock on
`iam_operations`, the ALTER queues; every running v0.23 `Apply` with an
OperationID then blocks behind it while holding `iam_tuples` in SHARE ROW
EXCLUSIVE mode, and all authorization writes stall. Apply 0004 in its own
migrate run, and set a lock timeout through the DSN
(`options=-c lock_timeout=5s`) or the migration role. On timeout the
transaction rolls back cleanly and the `(source, version)` ledger stays correct;
retry the run. If a later migration in the same run touches `iam_tuples`, the
lock order is inverted against running `Apply` calls and PostgreSQL aborts one
side as a deadlock — another reason to run 0004 alone.

**SQLite/Turso preflight.** The rebuild fails loudly only on a host view that
names `iam_operations`; drop the view first and recreate it afterwards. Host
triggers (for example `AFTER INSERT ON iam_operations`) and host indexes on
`iam_operations` are **silently dropped**: 0004 succeeds and they are gone.
Before 0004, save the output of

```sql
SELECT type, name, sql FROM sqlite_master
WHERE tbl_name = 'iam_operations' AND name NOT LIKE 'sqlite_autoindex%' AND type <> 'table';
```

and recreate those objects after it. Take a Turso point-in-time snapshot or
branch before running 0004. The copy is one server-side statement, so only very
large ledgers approach hosted transaction limits; verify on a branched copy
before running 0004 on a large ledger.

**Hard cutover and deploy order.** Both schema probes are exact-match: after
0004 an older binary (authorization `v0.23.x`, PostgreSQL store `v0.17.x`,
Turso store `v0.16.x`) refuses construction, and the new binary refuses a schema
without 0004. The probe runs only at construction, so deploy in this order:

1. Freeze scale-out and pin minimum instances.
2. Run the migration. Old instances keep serving; while they do, they treat a
   fenced record as `ErrOperationMismatch`, so no access is granted.
3. Roll the new binaries immediately.
4. Never let an old revision start after the migration: autoscale-out, a crash
   restart or a node drain of an old revision fails construction.

**Two-phase rollout.** Phase 1 ships authorization v0.24 and 0004 with **no**
Fencer wired (authentication's `InvitationsConfig.Fencer` unset and no host
`Fence` calls). No fenced row can exist yet, so phase 1 is still reversible.
Phase 2 wires the Fencer; the first fenced row is the real one-way door. Once a
fenced row exists the check cannot be narrowed, and deleting fence rows
re-opens operations they closed. Keep fence rows with the rest of the ledger
across purge, teardown, backup and restore.

**Emergency reverse (phase 1 only).** Reverse with a **new forward** ledger
entry that the host authors — never delete or edit 0004 (or the host's copy of
it) — and roll back to the v0.23 binaries in the same deploy, because the v0.24
probe refuses the narrowed check. Both statements guard themselves: they fail
if any fenced row exists. These are reference SQL, not shipped migrations.

PostgreSQL (`ADD CONSTRAINT` validates existing rows and fails on a fenced one):

```sql
ALTER TABLE iam_operations DROP CONSTRAINT ck_iam_operations_outcome, ADD CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found'));
```

SQLite/Turso (the copy fails the narrowed CHECK on a fenced row; the body is
byte-identical to 0003 so the v0.16 probe accepts it; the same view, trigger and
index preflight applies). The guard only holds when execution stops at the
first error inside one transaction, as the migration runner does. A shell that
continues past the failed copy would drop the ledger and rename an empty table,
so from the `sqlite3` CLI run it with `-bail`:

```sql
CREATE TABLE main.iam_operations_prev (
    operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE BINARY NOT NULL,
    fingerprint TEXT COLLATE BINARY NOT NULL,
    outcome TEXT COLLATE BINARY NOT NULL,
    committed_at TEXT NOT NULL,
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found'))
);
INSERT INTO main.iam_operations_prev (operation_id, encoding, fingerprint, outcome, committed_at)
SELECT operation_id, encoding, fingerprint, outcome, committed_at FROM main.iam_operations;
DROP TABLE main.iam_operations;
ALTER TABLE main.iam_operations_prev RENAME TO iam_operations;
```

Wire `Repositories.Operations` to enable `Mutations.Fence` and
`Mutations.Recorded`; the bundled SQL constructors populate it, and
`memory.Store.Operations()` returns it for hand-built factories.
`MutationRepository` is unchanged. Third-party mutation stores that honor
`OperationID` must implement `mutations.OperationLedger` (fence under the same
write lock as `Apply`, refuse an ambient transaction, replay a fenced ID as
`OutcomeFenced` for any command before comparing fingerprints) and wire it as
`Operations`: the `OperationLedger` conformance family fails when `Mutations` is
wired without it.

## Adopting the operation ledger (v0.23.0)

Upgrade authorization core with PostgreSQL store `v0.17.0` or Turso store
`v0.16.0`. SQL hosts using `Repositories` **must apply** the additive
`migrations/0003_iam_operations.sql` before constructing the upgraded adapter,
even if they do not supply operation IDs. `RelationshipRepository` alone does
not require the new table. Historical migrations and cache protocol are unchanged.

For an existing merged migration stream, copy only `0003_iam_operations.sql`
into the host's authorization source/schema. Do not re-export over renamed
historical files. Fresh merged streams order base 0001, optional cache 0002,
then base 0003; separate sources apply both base migrations before the cache.
`iam_operations` retains committed outcomes across purge and teardown. Do not
delete rows whose producer can still retry. Backup and restore it together with
the canonical tuples; restoring facts without their operation records can allow
an old request to run again. A rollback keeps the additive table installed and
must stop ledger-dependent acceptance retries before using a ledger-free writer.

Third-party mutation stores must implement atomic operation binding and run the
new `OperationLedger` conformance family. Calls with an empty OperationID keep
their prior behavior. These versions are published; the release record is
[the implementation plan](../../../plans/invitation-grant-operation-ledger.md).

## Adopting the outcome cleanup (v0.22.0)

`mutations.OutcomeInvariantBlocked` and `Outcome.Rejection` are removed. No
production path ever produced that outcome: integrity refusals are returned as
the `ErrInvariantBlocked` error (wrapping `sdk.ErrConflict`) with no result, and
`Outcome.Valid` now accepts only `applied`, `no_change` and `not_found`. Upgrade
authorization core to `v0.22.0` with PostgreSQL store `v0.16.0` or Turso store
`v0.15.0`. No SQL migration, cursor/cache format, configuration or dependency
change is required.

## Adopting the internal cleanup (v0.21.0)

The authorization core removes `decisions.Service.ValidateRelation`,
`ValidateRelationName`, `ValidateRelationships`, the `decisions.CreateRelationship`
alias and `relationships.ErrRelationshipsNotConfigured`. These were leftovers
from the separate relationship engine. Use `CompiledModel.ValidateTuple` for
current tuple-shape validation and `relationships.CreateRelationship` for the
relationship facade's input. `GetSchema`, `SchemaDigest` and model snapshots
remain available for schema inspection. No production path returned the removed
error; remove checks for that sentinel. An undeclared label is legal for tuple
writes unless an explicit shape constraint rejects its subject; validation is
not a closed role/relation catalog.

Raw relationship stores now share exact reads, counts and list projections with
the canonical tuple facade. Empty/malformed selectors, nonblank unsupported search
and canceled contexts return errors consistently. Do not rely on ignored search
text or empty selector pointers; omit optional filters to leave them unconstrained.
Raw standalone listings/counts use a coherent snapshot. Raw SQL operations still
join the host transaction at its existing isolation, including PostgreSQL READ
COMMITTED; root services and decision operations still require suitable snapshot
isolation. Graph model filtering, atomic writes, integrity and audit are unchanged.

Upgrade authorization core to `v0.21.0` with PostgreSQL store `v0.15.0` or
Turso store `v0.14.0`. No SQL migration, cursor/cache format, configuration or
new third-party dependency is required. Recheck host error handling and listings through any directly held raw
relationship repository. No package rename or role API migration is required.

## Schema files

| Source | File | Creates |
| --- | --- | --- |
| Base authorization | `migrations/0001_iam_tuples.sql` | `iam_tuples`, `iam_audit`, identity constraints and lookup indexes |
| Optional TupleCache | `tuple_cache_migrations/0002_iam_tuple_cache.sql` | Protocol-2 source identity, delivery receipt, outbox and canonical capture triggers |
| Base authorization | `migrations/0003_iam_operations.sql` | Atomic operation identity, fingerprint and committed outcome |

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
legacy mutation receipt tables; `iam_operations` is the opt-in request ledger.
There is no bundled old-schema conversion, preflight,
downgrade or compatibility migration stream. Existing applications choose their
own data adoption procedure; this framework setup targets the canonical schema.

## Construct the services

1. Apply the base schema with the host's migration runner.
2. Optionally apply the TupleCache schema.
3. Construct the selected SQL adapter's `Repositories(ctx, db, ...)`. For a named
   PostgreSQL schema, apply and construct with matching schema options. Set
   `WithIntegrityPolicy` on every writing adapter for the authority; its default
   is empty. `DefaultIntegrityPolicy()` opts into one concrete owner per resource.
4. Pass the repositories to `authorization.New`. `Repositories.Tuples` supplies
   roles, the relationship facade and decisions. Supply a model only when policy
   needs named permissions or graph traversal.
5. If caching is enabled, construct the store with `WithTupleCache()`, pass the
   backend (a Redis namespace is optional) and a `MaxStaleness` (zero means 5s;
   every replica must agree), and supervise the
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
checks and integrity rules therefore see eligible facts from either facade.
Inbound adapters make every principal permission decision. The single
`mutations.Service` takes commands without actor/guard arguments and enforces
model shape and configured data integrity in its serialized transaction. All
ordinary raw and facade writers enforce the same minima, including scope deletion
and no-ops. Only explicit resource teardown bypasses minima; globals survive.
A fresh protected resource needs all required subjects in its first write.

Bundled role administration requires both `RoleRoutes.Gate` and
`RoleRoutes.WritePolicy`. The latter admits the validated exact assign/unassign
command before execution. Audit source identifies the actor or system; it does
not authorize the write. Permission revocation after admission does not retract
the command; current-state integrity still runs at write time.

## Adopting the inbound and integrity API

These are source changes; this refactor requires no SQL migration or cache
protocol change. Upgrade core and adapters together. The names below identify
removed APIs, not compatibility aliases.

| Previous API | Current API or placement |
| --- | --- |
| `MutationGuard`, `WithGuard`, transactional `DecisionView` | Admit at inbound using `HTTP.Require`, the decision engine, or a policy over the parsed command. No principal callback runs inside the write. |
| `Mutations.Apply(ctx, actor, cmd)` and actor-taking typed methods | Authorize first at inbound, then call `Mutations.Apply(ctx, cmd)` or the typed method without an actor argument. Supply audit attribution through `audit.WithSource`. |
| `Components.SystemMutator` | Use the single `Components.Mutations` writer. Explicit `TeardownResourceAuthorization` still requires a reason and a retired/deleted resource. |
| `mutations.NewService(repo, engine, ...)` returning a component pair | `mutations.NewService(repo, mutations.WithModel(compiled), ...)` returns one service. The compiled model validates shape, not permission. Nil repositories fail construction. |
| `GuardianPolicy`, `GuardianRule`, `MinAnchors`, `WithGuardianPolicy` | `IntegrityPolicy`, `IntegrityRule`, `MinSubjects`, `WithIntegrityPolicy`. Rules now apply to every ordinary writer, including raw facades. |
| `MutationRepository.ApplyGuarded` | Implement `Apply(ctx, cmd, validate)` and `IntegrityPolicy()`. Keep shape validation, post-state integrity, delta and audit atomic. |
| `ErrGuardedInsideTransaction` | `ErrMutationInsideTransaction`; atomic commands still reject ambient transactions. |
| `RoleRoutes.AssignmentPolicy` | Required `RoleRoutes.WritePolicy`, receiving `RoleWriteRequest` for **both** assign and unassign. Preserve any assignment-specific restrictions while authorizing both operations. |

If `Repositories.Mutations` is omitted, `Components.Mutations` is nil; raw
writers remain available and still honor the store's policy. Configure the same
policy on every writing adapter instance. Seed all required subjects in one
initial write; raw `DeleteScope` no longer bypasses minima.

For related authentication callers:

- Import `InviteCheck`, `UserAdminCheck` and their request/action types from
  `authentication/inbound/http`. Root configuration still wires the HTTP callbacks;
  logic constructors no longer accept them.
- Replace invitation `CreateAuthorized` with inbound admission over
  `PrepareCreate` followed by `CreatePrepared`. Replace `ListByResourceAuthorized`
  and `AuthorizeUserAdmin` with an inbound policy call before the service operation.
- Replace caller-ID arguments to invitation `Cancel`/`Resend` with
  `PrepareManagement`, an inbound issuer check, then execution of that exact
  prepared value. Keep recipient and credential proof in authentication logic.

Exercise host allow/deny cases at every entry point, missing write-policy boot
errors, admitted-write/revocation ordering, last-owner conflicts and explicit
teardown. The new boundary deliberately permits an admitted command to finish
after permission revocation; preserving integrity is a separate serialized check.

## Transactions and cache lifecycle

PostgreSQL compound reads require REPEATABLE READ or SERIALIZABLE when borrowing
an ambient transaction. `pgxdb.TransactSnapshot` provides a read-write REPEATABLE
READ workflow; default READ COMMITTED returns `tuples.ErrSnapshotIsolation`.
SQLite BEGIN IMMEDIATE is suitable. Raw writes join ambient work while enforcing
integrity; atomic mutation commands own their transaction and reject an ambient
context. An ambient snapshot does not make an earlier permission check atomic
with a later business write.

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
