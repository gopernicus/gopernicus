# Authorization schema setup and API adoption

Authorization ships a fresh canonical SQL schema for PostgreSQL and SQLite/Turso.
Hosts export and apply it before constructing repositories. Framework constructors
validate the applied schema; they do not create tables or run migrations.

## Schema files

| Source | File | Creates |
| --- | --- | --- |
| Base authorization | `migrations/0001_iam_tuples.sql` | `iam_tuples`, `iam_audit`, identity constraints and lookup indexes |
| Optional TupleCache | `tuple_cache_migrations/0002_iam_tuple_cache.sql` | Protocol-2 source identity, delivery receipt, outbox and canonical capture triggers |

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
mutation receipt tables. There is no bundled old-schema conversion, preflight,
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
5. If caching is enabled, construct the store with `WithTupleCache()`, supply a
   dedicated Redis namespace and a positive `MaxStaleness`, and supervise the
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
