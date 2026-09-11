# Optional authorization change history

Status: COMPLETE — 2026-09-11. Implements the owner's approved audit-log design and
receipt/revision removal after AUDIT-025 tuple cleanup.

## Preconditions and boundaries

- Branch `firestore-authentication`, HEAD `6807ed062fa93d71e176ae85d0612e2342dc8694`.
  Preserve the large existing dirty tree. New phase baseline (2168 files):
  `/tmp/gopernicus-authorization-auditlog-baseline.json`.
- Read ARCHITECTURE.md and named `.claude/agents/implementer.md`; use current
  architecture over retired names in that role. Inherited model because its
  configured opus model is unavailable. Use the existing named project agents.
- All Go/make commands use `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.
  Formatter `/Users/jrazmi/go/bin/goimports`. 42-module workspace, no root go.mod.
- No current test fixtures. Provision isolated local fixtures later; never use
  unrelated application databases. No external consumers, CMS, publication,
  deployment, production changes or commits. Record breaking changes in AUDIT-026.

## Fixed contract

1. Remove MutationID, request derivation/digests, Receipt, Replayed, Revision,
   ExpectedRevision, dependency revision tracking, iam_mutations and iam_scopes.
   Current model validation and guards run for every command. Natural tuple
   no-ops remain. Keep guardian invariants and atomic write boundaries.
2. `mutation.Result` contains only `Outcome` and `SameRoleGrantRemains`.
   Successful results describe this application, not a persisted receipt.
   Semantic/invariant refusals still return errors and nil results.
   Remove Outcome.Persisted; keep outcome evaluation helpers actually used.
3. Rename the small command address to `mutation.Target` / `TargetKind` /
   `TargetResource` / `TargetSubject`; Command.Target and MutationAttempt.Target.
   This names the resource or global-role subject being changed; it is not a
   revision anchor or a policy mode. Retain real resource-scoped role semantics.
   Replace stale-revision errors with ErrConcurrentMutation (sdk.ErrConflict)
   for actual transaction contention. Rename teardown to
   TeardownResourceAuthorization for symmetry with PurgeResourceAuthorization.
4. Store constructors expose `WithAudit()` (default off), shared by relationships,
   roles and mutation repositories. Domain `audit.Reader.List(ctx, Filter,
   list.Request)` is supplied as `Repositories.Audit`, usable even when new
   recording is off. Memory supplies `Store.Audit()`. No post-commit callback is
   the persistence contract. Remove the old Config.Audit/AuditSink attempt hook.
5. Domain `audit.Source` has ActorType, ActorID, System, Reason strings. Exactly
   one complete actor pair or a nonempty system source is required; reason is
   optional and bounded to 1024 bytes. Reference fields use existing validation.
   `audit.WithSource(ctx, Source) context.Context` uses a private key;
   `audit.SourceFromContext(ctx) (Source,error)` validates attribution. Root
   aliases AuditSource and WithAuditSource make host usage easy. This is audit
   metadata, never authentication/authorization authority. Guarded methods
   overwrite attribution with their validated Actor (preserving a valid optional
   reason); trusted/raw callers supply an explicit source when auditing is on.
   Enabled stores validate attribution before a write, including no-op attempts.
6. `audit.Change` has Action (`added` or `removed`), and exactly one of
   Relationship (*relationship.CreateRelationship) or Role (*role.Assignment).
   Full userset identity is retained. `audit.Record` has ID, EventID, OccurredAt,
   Source, Change. One record per actual fact change avoids Firestore's 1 MiB
   per-document cap. EventID is newly generated per changed operation purely to
   group its records; it is never supplied or reused by callers for replay.
   ID combines that event ID and a fixed-width change ordinal. Replacements
   record removal+addition; no-op and refused/rolled-back operations record none.
7. Shared audit.NewRecords(ctx, changes, now) validates, owns and canonicalizes
   actual deltas, cancels matching add/remove pairs, and creates identifiers.
   Timestamps normalize to UTC microseconds across stores. Records contain full
   source and changed fact, not headers, arbitrary request maps or graph snapshots.
   SQL may store flat fact/source columns; Firestore uses private equality keys.
   Audit Filter has optional ResourceType/ResourceID, SubjectType/SubjectID and
   ActorType/ActorID pairs, each both-or-neither. Empty pairs mean no filter.
   Ordering is occurred_at DESC with record ID tiebreak (ASC also supported),
   standard cursor/offset/count. Host owns access, retention, presentation/export.
8. Audit entries and facts commit in the same transaction/memory publication.
   All supported raw and guarded write paths participate when enabled. SQL
   ambient operations use savepoints so an audit failure cannot leave a fact
   change committable when the host handles the operation's error. Memory stages
   state and owned records before publication. Firestore never silently splits a
   write to accommodate audit size; native transaction overflow rolls back all.
9. PostgreSQL takes schema-qualified relationship+role table locks in fixed order
   (SHARE ROW EXCLUSIVE) before guard/state reads, under READ COMMITTED for owned
   mutations. Raw write helpers take the same locks and capture actual SQL
   RETURNING deltas; joined host transactions retain their isolation and own
   commit. This serializes authorization writes per schema while allowing normal
   reads, protects negative predicates and cooperates with raw DML. Never retry
   a host-owned transaction; propagate deadlock/contention errors. Single-table
   baseline constructors must either lock only their touched table or explicitly
   require both tables at boot, never fail unexpectedly on the first write.
   Turso retains BEGIN IMMEDIATE; Firestore uses native transactional document
   and query reads; memory retains its shared mutex. Guarded calls still refuse
   ambient host transactions. No anchor table or counter replacement.
10. Remove retries after ambiguous transport/commit failures, including Turso
    HTTP503 whole-operation replay. Only definite aborted transaction contention
    can retry internally, preserving terminal guard/validator errors and context
    cancellation. No hidden durable request deduplication is reintroduced.
11. Append SQL migration 0007 (keep 0001–0006 byte-identical): drop legacy receipt/
    revision tables, create iam_audit and listing indexes. No historical audit
    can be reconstructed from old receipts. Hosts archive old tables first if
    desired, stop old writers, migrate, then deploy. Firestore gets explicit
    rerunnable legacy-ledger cleanup and optional audit index configuration;
    no startup data migration. No bundled audit HTTP route or UI.

## Work packets

- [x] A1 Domain mutation/audit contracts, memory implementation and shared tests.
  Owner lead_backend_validation: domain/mutation, domain/audit, memstore,
  storetest (all). Announce stable exported contracts before adapters compile.
- [x] A2 PostgreSQL/Turso implementation, migration, upgrade/concurrency/audit tests
  and adapter docs. Owner authorization_stores_review: both SQL adapter modules.
- [x] A3 Firestore implementation, indexes, maintenance and audit/concurrency tests
  and docs. Owner authorization_engine_review: entire Firestore adapter module.
- [x] A4 Root facade/config/typed commands/results/guard APIs, HTTP and examples,
  root/inbound tests and docs. Owner root. Update AUDIT-026 and central plan.
- [x] A5 Peer review, disposable live SQL/emulator verification, actual host HTTP
  behavior, changed-module build/test/vet/races, complete 42-module snapshot gate,
  real root guards, inventory and fixture cleanup.

## Verification requirements

Retain current-model guard validation, guardian and cancellation/error identity
coverage while replacing receipt/revision assertions with state-based checks.
Prove grant/revoke/replacement/purge/teardown and role deltas, no-op omission,
source attribution/override, disabled behavior, audit-write rollback, ambient
savepoint rollback, pagination, and caller/result ownership. Add cross-writer
negative predicate races after anchor removal. SQL tests cover fresh migration
and populated 0006 upgrade; Firestore covers explicit cleanup, audit manifest
parity and transaction limits. Report real Firestore and non-C locale skips.

## Implementation checkpoint and review

A1–A4 are complete. The final combined source is being checked in
`/tmp/gopernicus-authorization-auditlog-check-psza3itd`; manifest
`/tmp/gopernicus-authorization-auditlog-check-snapshot.json`. The first combined
snapshot passed module checks but failed G24 because native gRPC classification
leaked into the Firestore adapter. That was corrected with the narrow connector
helper `IsTransactionAborted` and companion tests; no guard exception was added.
This expands the changed modules to include the Firestore connector.

Root peer review also fixed empty CreateRelationships batches bypassing
recording-source/cancellation checks and made teardown reason UTF-8/NUL validity
consistent whether recording is enabled or disabled. Retired replay/revision
assertions were replaced by current-state convergence, per-call current-model
rejection, transactional guard/guardian behavior and actual history assertions.
The example's twelve-point proof now reads committed audit history and its HTTP
role lifecycle uses flat outcomes. Missing-source and audit failures remain
write failures, never best-effort warnings.

Current phase inventory:
`/tmp/gopernicus-authorization-auditlog-inventory.json` (170 changed/deleted/added
files at this checkpoint; 132 existing Go files). The twelve historical SQL
migrations through 0006 match baseline hashes. Formatter is goimports. Root
migration entry: AUDIT-026. No external consumers, commits, publication or
production changes. Created test containers `gopernicus-auditlog-pg`,
`gopernicus-auditlog-turso`, and `gopernicus-auditlog-firestore` are all removed;
unrelated local services were not changed.

Verified before the final combined gate:

- Authorization core `go build ./...`, `go test -race ./...`, `go vet ./...`;
  the final empty-batch/reason regressions also passed focused root/engine race.
- Example application build/test/vet; actual HTTP session/role lifecycle and
  twelve-point current-model/guardian/audit proof passed. The first sandboxed
  HTTP attempt could not bind loopback; rerun with local access passed.
- PostgreSQL default full race 90.531s; final audit regressions 5.998s; named
  schema full race 101.041s. Turso full integration/race 29.806s. Fresh/populated
  migrations, RETURNING deltas, raw savepoint rollback and cross-writer negative
  predicates are covered. Reports `/tmp/gopernicus-auditlog-SQL.md` and inventory
  `/tmp/gopernicus-auditlog-SQL-files.json`.
- Firestore emulator full integration/race 127.669s; final audit regressions
  2.003s; post-G24 audit/contention/guard-error race 2.353s. Build/test/vet and
  integration/live compilation passed. Report
  `/tmp/gopernicus-authorization-auditlog-FS.md` and matching FS-files inventory.
  Shared live CI now deploys the optional audit manifest too.
- Root `make guard` passed after G24 correction. Documentation `pnpm typecheck`
  and `pnpm build` passed; only non-blocking update-cache/git-date notices.
- Real Firestore execution and optional PostgreSQL non-C locale proof remain
  unverified; emulator and C-locale results do not claim those gates. Memory and
  Firestore intentionally skip SQL ambient-join tests.

All Go/make commands above used the required GOCACHE. Agent A1's domain/memory
report and peer findings are in
`/tmp/gopernicus-authorization-auditlog-A1-report.md` with its exact file inventory.

## Final verification and handoff

The final 42-module snapshot `make check` passed (exit 0), including generation,
all module build/test/vet checks, tagged compile checks and repository guards.
Log: `/tmp/gopernicus-authorization-auditlog-make-check.log`. G20 requires Git
metadata, so the separate real-root guard run is the authoritative check for that
rule; it passed after G24 correction. No production Go source changed after this
snapshot. Two test-only wording/name cleanups were followed by the corresponding
transport test and twelve-point proof rerun; both passed and regenerated the
checked-in transcript. Final goimports listing is empty and `git diff --check`
passes. Source and previous migration hashes were reviewed against the phase
baseline; final inventory is
`/tmp/gopernicus-authorization-auditlog-inventory.json` (170 paths).

No unresolved implementation or test failure remains. Explicit external gates
still unverified: real Firestore and the optional PostgreSQL non-C locale test.
All three disposable containers are removed. Consumer application rollout is
host-owned and described in AUDIT-026: coordinate core/adapters, stop old writers,
archive old ledgers if wanted, apply migration 0007 or explicit Firestore cleanup,
then enable recording and source metadata on the writers that need history.
