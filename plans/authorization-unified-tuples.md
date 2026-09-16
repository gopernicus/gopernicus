# Unified authorization tuples

Status: COMPLETE — IMPLEMENTED AND VERIFIED, unreleased, 2026-09-15.
U0–U6 implementation, independent review and integrated local verification passed.
This is the executed record of `.claude/plans/authorization-unified-tuples.md`.
Version pinning, isolated release archives and publication await a separate release
instruction for the five coordinated artifacts.

## Context: the product win

A person should be able to be both `owner` and `member` of an organization. An
invitation adding `member` must not conflict with or silently discard an existing
`owner` relationship. Those are two independent facts, and application policy can
choose which facts grant a permission. The current one-relation-per-subject rule
prevents this; maintaining a separate role table also duplicates persistence,
mutations and cache infrastructure.

The framework target is one canonical tuple authority, exact role APIs for simple
applications, and optional graph evaluation for applications that need it. The
owner explicitly permits breaking changes; 360, Segovia and other applications
can adapt. Their current behavior does not define the framework design. No host
application changes or migrations are performed by this task.

## Goal

Use `iam_tuples` as the single SQL authority, with shared snapshots, mutations,
audit and optional TupleCache delivery, while retaining `HasRole`/`HasRoleIn` and
one coherent permission-expression evaluator. Ship authorization core, PostgreSQL,
Turso and Redis together after validation; remove authorization Firestore support.

## Scope and workflow

- Architecture authority: `ARCHITECTURE.md` and `pockets/README.md`. Domain ports
  remain inward, adapters remain separate modules, and migrations are host-owned
  and applied before boot. No authorization protocol moves into SDK.
- Active plan location is `.claude/plans/authorization-unified-tuples.md`, matching
  `.claude/agents/planner.md` and neighboring authorization plans. The earlier
  `plans/` draft is moved, not left as a second active plan.
- Preserve owner edits in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md`, and
  `plans/segovia-v2-audit-upgrade-handoff.md`.
- Starting worktree baseline was main at
  `1e1308733cd8c23995497134052b2ef16c1f5b30`; check current state before each phase.
- Format Go with the repository's goimports workflow. Do not hand-edit generated
  artifacts. Record durable summaries of changes, verification and open failures.
- Remove only `pockets/authorization/stores/firestore`. Keep
  `integrations/datastores/firestore` and
  `pockets/authentication/stores/firestore`, their tests, guards and dependencies.
- No production datastore actions, host changes, deployment, tags or publication
  are authorized by this implementation task. Test only disposable fixtures.

## Target contract

### T1. One kind-free identity

Canonical comparable `tuples.Tuple{Scope, Relation, Subject}` is the sole fact.
`Subject` is `SubjectRef{Type, ID, Relation}`. Scope exposes `Kind`, `Type`, `ID`:
`Global()` creates explicit global scope; `On(type, id)` creates resource scope.
Zero scope is invalid, and empty resource coordinates do not mean global.

All components participate in equality. Same scope + relation + exact subject is
one fact regardless of whether a role or relationship API wrote it. Exact
re-insertion is idempotent. Different relations coexist for the same subject and
resource. There is no role discriminator, implicit namespace, writer-origin
classification, public synthetic tuple ID or creation timestamp.

Global facts have no resource coordinates and are not wildcards or fabricated
root resources. Canonical validation permits a global userset subject; exact role
checks still use concrete subjects only. A concrete group and `group#member` are
different subjects. Reference validation stays exact, bounded, valid UTF-8 and
control-free, without normalization. P1 introduces no public Key string, hash or
wire protocol: adapter encodings are designed when needed.

`logic/tuples` is a leaf below roles, relationships, decisions, mutations and
cache. Existing Assignment/CreateRelationship conversion and validation already
consume it. Keep one validation definition rather than copied rules.

### T2. Exact roles and explicit global applicability

- `HasRole(ctx, principal, label)` is exact global concrete membership.
- `HasRoleIn(ctx, principal, label, resource)` is exact scoped concrete membership.
- Neither expands usersets, consults a role registry, traverses a graph or falls
  back to another scope. A roles-only application requires no permission model.
- A concrete `editor` tuple written through the relationship facade satisfies
  `HasRoleIn(..., "editor", resource)`. Role listings are filtered tuple views,
  not a record of which API created a fact.
- Assignment requests carry explicit `tuples.Scope`; omitted/invalid scope fails.
  HTTP assignment scope is explicit too, rather than inferred from empty strings.
- Global applicability is deliberate policy. Lazy predicates are distinct from
  immediate read methods, for example:

  ```go
  Evaluate(ctx, principal, Any(Role("admin"), RoleIn("editor", resource)))
  ```

A fixed `HasRoleInOrGlobal` helper may be added if useful as two exact probes in
one tuple snapshot, with no roles→decisions or roles→tuplecache dependency. A
policy expression can express the same choice. Neither route adds a second
evaluator or revives implicit fallback in ordinary HasRoleIn.
Global labels never grant access to future resource types solely by name reuse.

Identity sharing works in both directions: a role-written resource tuple can
participate in model-permitted membership/Through targets, graph lookup and
guardian counts just like the identical relationship-written tuple. The current
model still controls eligible shapes and traversal; writer origin cannot filter
those reads. Retain regressions for this direction as well as HasRoleIn.

### T3. One policy model and evaluator

`logic/decisions` owns the single expression evaluator. Exact membership,
explicit userset membership, Through, named permissions, All and Any are distinct
operations over the same facts. Current relationship `Direct` expands usersets;
it must not silently become exact. Global scope is never an implicit Through
intermediary; resource traversal names resource targets explicitly.

The new model is compile-visible: replace `authmodel.RoleModel`,
`CompiledRoleModel`, `CompileRoleModel`, `WithRoleModel`, `WithRelationshipModel`
and split permission-pair dispatch with one `decisions.Model`,
`decisions.Compile` and `authorization.WithModel` surface. The exact model fields
are finalized with P2 compile fixtures before implementation, but retaining two
models behind a facade is not an acceptable outcome. Named permissions map to
explicit expressions; a roles-only exact/predicate host can omit the named model.
Duplicate named permissions are construction errors, not implicit cross-model OR.

Write-time catalog disposition is explicit: delete the implicit RoleModel role
catalog gate (`roleModelValidatorFor` and its scope-dependent global rule).
Structurally valid opaque assignments remain legal without a model. Optional
host assignment restrictions belong to a supplied guarded write policy, not to
whether a label happens to appear in the permission model. Existing graph shape
rules become explicitly declared canonical relation/subject-shape constraints
where the host chooses them, enforced identically through every bound facade.
Opaque role-only hosts with no model remain allowed. Update
`mutations/mutation_service.go` semantic validation and `mutation_view.go` compiled
evaluation together. Named permissions do not silently restore global fallback.

Validate the entire expression before I/O, including branches later skipped.
Reject empty/nil/malformed All and Any; bound expression depth/nodes and leaf
work. Evaluate sequentially and deterministically: All stops on false, Any on
true, and encountered read errors abort without allowing. Unread branches do not
produce datastore errors. Errors/cancellation/failed snapshot completion discard
provisional results. Middleware only resolves inputs and enforces the result.

Dependency direction is `mutations -> decisions -> tuples` as needed; decisions
must not import mutations. Roles depend on tuple reads and must not import the
engine or tuplecache. Guards reuse the same decisions evaluator against their
serialized view, not a detached service/cached lookup. Add executable import
guards for these directions alongside the tuple-leaf guard.

### T4. One coherent operation view, including ambient transactions

All reads contributing to one result use one coherent view. A transaction that
revokes A and grants B cannot make All(A, B) true if they never coexisted. This
covers exact roles, graph/mixed decisions, explain, batch/filter and lookup.
Snapshot contracts expose raw tuple reads; optimized graph reads stay with their
consumer and retain the same bound transaction and model. Do not introduce a
neutral-looking `HasExactRole` port or a second roles expression engine.

Ordinary SQL operations open a suitable read snapshot without cache metadata.
Memory owns a copied/immutable view under its shared lock. Borrowed views are
callback-scoped, close on every exit, and cannot be used after completion.

The old ambient contract accepts the connector transaction's isolation, including
PostgreSQL's default READ COMMITTED. That is insufficient for this promise and
must change explicitly. Compound/multi-read operations require proven coherent
bound reads or repeatable-read/serializable isolation; otherwise reject before
any evaluator leaf runs. Never open a detached transaction, silently upgrade an
already-used transaction, or merely document weaker results as coherent.

Add `pgxdb.DB.TransactSnapshot(ctx, fn)` with read-write REPEATABLE READ, preserving
the ordinary Transact default for unrelated consumers. Add connector-owned
`Tx.SnapshotIsolation(ctx) (bool, error)` that queries actual transaction_isolation
on that bound transaction, accepting REPEATABLE READ/SERIALIZABLE. Do not infer
isolation from pool defaults or change it after host work. The canonical adapter
borrows a suitable ambient transaction or returns a named invalid-input error
before evaluation; no detached snapshot is opened. SQLite's BEGIN IMMEDIATE
ambient transaction remains suitable.

Retire unconditional READ COMMITTED `RunCheckAmbient` expectations; replace the
coherent contract with `RunSnapshotCheckAmbient`, explicit PG default-READ-COMMITTED
rejection and retained pending-write visibility/commit/rollback/error/panic tests.
Record the connector prerequisite/version in release notes. The PG connector is
a fifth release artifact, dependency-first from the same reviewed commit as the
four authorization modules. No silent default-isolation upgrade across pockets.

Guarded mutations retain their serialized write boundary and bypass cache.
Custom adapters must implement the required coherent contract; there is no
undocumented fallback to individually sampled reads.

### T5. Exact mutations, unified invariants and audit

Grant/revoke exact tuples. An atomic batch of exact revokes and grants represents
a swap. Remove ambiguous Replace/ReplaceRelationship, which removes an unknown
existing relation. Reconciliation names one scope+relation and converges its
subjects atomically. Independent owner/member labels remain intact. Duplicates
within one add/remove set are idempotent; the same fact in both sets is invalid
input. Bound command size and affected rows, and never split an atomic request
across commits to evade a backend limit.

No built-in exclusive-group DSL is added. Hosts enforce exclusivity, when wanted,
with guards over the serialized view; trusted/raw writes bypass application
policy deliberately. Removing the physical unique-subject constraint is an
intentional change, not equivalent enforcement through a new model setting.

Retained minimum-anchor/guardian invariants operate on canonical facts and apply
through every guarded facade, including role unassignment of an owner tuple.
P2 must not expose common-fact aliases before canonical mutations enforce these
rules. Validate guards/current policy on natural no-ops. Resource teardown
explicitly removes resource-scoped facts and retains global facts.

Derive one canonical before/after delta for audit and outbox. An atomic swap
produces a removal and addition in one event/commit. No-op, denied, failed or
rolled-back writes emit no facts or audit. Audit failure rolls back facts.
Use one public `Change{Action, Tuple}`; remove its Role/Relationship pointer
union and the kind-prefixed factKey. Historical provenance belongs to the
record's encoding metadata (`role/v1`, `relationship/v1`, `tuple/v2`), not to new
fact identity. In 0008, convert old row payloads to canonical coordinates while
preserving ID, event ID, source, time, action and original encoding. Keep both old
records when their payloads become equal; never deduplicate audit history or
invent migration events. New writes use tuple/v2 and one canonical delta.

### T6. Explicit disposition of retiring public behavior

| Existing contract | New disposition |
|---|---|
| Multi-argument HasRole / ResolveScope | Replace with exact HasRole and HasRoleIn; fallback only explicit composition |
| Assignment empty resource pair | Assignment owns explicit Scope; invalid/omitted scope rejected |
| HasRoleWhere / direct-global-both provenance | Remove scope-fallback provenance APIs; explain identifies the evaluated exact leaf and scope |
| EffectiveGrant / ListEffectiveRoleGrantsByResource | Remove implicit global-union listing; use raw exact tuple views or evaluator-backed effective access query |
| SameRoleGrantRemains / `same_role_grant_remains` JSON | Remove Go result field and JSON annotation with handler/consumer tests; an unrelated scope is not an effective remaining grant |
| RoleModel / WithRoleModel and pair ownership | Replace with one compile-visible decisions model and WithModel; update every repo consumer |
| Implicit role catalog on writes | Remove; retain structural validation and explicit host guarded policy |
| OpReplace / ReplaceRelationship | Remove; explicit exact revoke+grant atomic batch |
| semantic_conflict outcome/reason/error for another held relation | Remove this built-in outcome; independent relations are legal. Host policy refusals use explicit policy/invariant errors |
| Role/relationship audit.Change union | Introduce canonical tuple change identity; retain explicit decoding of historical encodings without fabricating history |
| Unrestricted resource lookup | Only an explicit global branch applicable to that resource type can yield Unrestricted |
| Check/Explain/Batch/Filter/Lookup | Same evaluator/view/budgets; no special hidden role fallback |

Update HTTP JSON contracts, error translation, snapshots/golden tests and docs
atomically with these changes. Do not preserve misleading zero-valued JSON fields
for compatibility. Roles administration remains an ordinary guarded facade.

## Schema and migration design

### Transactional base migration 0008

Append `0008_iam_tuples.sql` in PostgreSQL and Turso with matching version sets;
never edit shipped 0001–0007. Within one migration transaction:

1. Validate all legacy fields/scopes and quantify role/relationship canonical
   overlap. Run the explicit global-grant preflight described below.
2. Create iam_tuples with explicit scope kind, non-null coordinates/identity,
   scope shape CHECK and full-key primary key. Use COLLATE C in PG and BINARY in
   SQLite. There is no unique-subject index. This intentionally updates both the
   table and recursive SQL: every textual CTE anchor/recursive arm/UNION input,
   including parameter casts and descendant seeds, must have the same C
   collation. Merely collating the new table recreates SQLSTATE 42P21. The old
   0001 uncollated-column workaround is historical; it is not sufficient proof
   for new SQL. Require an applied non-C-database regression for userset and
   Through recursion and keyset order before this checkpoint is green.
3. Copy relationships as resource scope and roles as explicit global/resource
   scope. Deduplicate exact canonical overlap deliberately and verify expected
   distinct full-key counts/sets. No first-row-wins or ambiguous rename behavior.
4. Migrate iam_audit schema/constraints for canonical Change identity and retained
   historical encoding; PG ALTER constraints and SQLite table rebuild must both
   be transactional. Preserve historical records and event grouping exactly.
5. Drop old iam_relationships/iam_roles tables, indexes and dependent old triggers
   after verification, in that same transaction. A successful migration therefore
   has no old writable authority or staged-copy rollback window.
6. Commit only when every preflight/copy/dedup/audit/drop check passes. Failure
   rolls back the whole migration, including metadata and source tables.

Actual applied-schema tests must construct from historical migrations, apply
0008 and query constraints/indexes/triggers/rows. SQL-text substring tests are
insufficient. Cover empty/full databases, malformed rows, duplicates, global
facts, usersets, audit history and forced late failures. Include named PG schema,
non-C ordering and SQLite main/TEMP shadowing. Canonical cursors order by the
full validated seven-component key in byte order in both adapters, with a
versioned cursor containing the explicit scope tag and all coordinates. Choose
one private encoding shared by memory/SQL and pin empty global coordinates,
separators, Unicode and maximum-length ordering tests; obsolete cursor versions
fail explicitly. There are no Firestore key splitting requirements.

### Global-grant semantic preflight and transition evidence

The schema copy preserves global facts, but new exact-scoped checks no longer
inherit them. Before migration, the host-run read-only preflight must output SQL
counts and all global subjects/labels/rows potentially affected. RoleModel lives
in Go, not SQL: those queries cannot identify exact scoped permissions/resources
that lose access. Combine the row report with explicitly supplied old/new model
declarations and named permission/middleware/guard usage; classify missing model
or application evidence as unknown. Provide concrete PG/SQLite queries and
old-allow/new-deny fixture replay in the guide, not only a prose warning.

Add an AUDIT.md entry for the semantic break, the exact API/model rewrite and
host verification steps. An opt-in, disabled-by-default transition event
`global_grant_not_applied` can count final scoped denies with a matching exact
global fact that the policy did not apply, using that operation's same snapshot.
It indicates potential reliance, not a counterfactual old allow. Extra probes
are bounded and cancellation-aware; labels are low-cardinality and never contain
subject IDs. It changes no decision and retains no legacy evaluator or fallback.
Document its transition lifetime/removal. Fixtures cover bounded same-snapshot
diagnostics, no observation when disabled and seeded old-allow/new-deny replay
with explicit model inputs; unknown old-policy behavior remains unknown.

### Optional cache migration 0003 and protocol

Base 0008 and optional cache migrations have separate ledgers. Immutable cache
0001/0002 reference the old fact tables, so applying the full old cache stream
after base 0008 has dropped those tables is invalid. Supply two explicit routes:

- **Fresh or previously durable-only:** apply base migrations through 0008, then
  use a fresh canonical-cache baseline/export under a new migration source
  (`authorization-cache-v2`). It creates protocol-v2 metadata/outbox/triggers
  directly against iam_tuples and never executes legacy cache 0001/0002. This
  also supports a canonical durable-only host enabling caching later.
- **Existing cache source:** while traffic is stopped and legacy tables still
  exist, bring base through 0007 and its old cache prefix through 0002 (including
  a cached v0.13 host that currently has only cache 0001). Then apply base 0008
  and the old source's new cache 0003 upgrade. Cache 0003 requires that completed
  0002 prefix and must reject missing/partial metadata. Constructors recognize
  the final canonical protocol from either supported route, without treating
  migration ledger numbers as a substitute for applied schema validation.

The baseline and upgrade produce the same final schema/protocol. Do not apply
both or silently migrate one ledger to another. No consumer must first run
v0.17 binaries; direct upgrade applies the necessary prefixes offline. Test fresh
cache, fresh durable-only then later cache, legacy cached 0001/0002, direct older
host upgrade and unsupported/partial combinations in both dialects. Document the
export APIs, source names and ordering explicitly; an undifferentiated
ExportCacheMigrations call must not emit a broken install path.

Append `cache_migrations/0003_iam_tuples.sql`: rotate protocol and source
binding/identity, replace old outbox/trigger encoding and capture all canonical
facts. Encode scope kind and coordinates explicitly in snapshots, deltas and
Redis forward/reverse keys, including global tuples and exact userset subjects.
The wire fact has all seven explicit components: scope kind, resource type/ID,
relation, subject type/ID and subject relation. No resource-shaped key may encode
global scope by accidentally validating two empty resource fields.
Include the protocol version in the Redis key prefix as well as validated state;
reusing a human namespace cannot accidentally read old fields.

Binding rules must distinguish source identity, protocol and freshness policy.
Two matching new clients share a mirror; changed source/protocol or incompatible
policy is rejected, not automatically rebound to old data. Test constructor,
State/Read/Publish, source Snapshot and Acknowledge behavior across old/new
bindings. Old workers cannot acknowledge new work or renew an incomplete mirror.
Keep exact-ID CAS acknowledgement, atomic full/delta publication, expiry,
physical limits, deadlines and full rebuild. Roles-only cache needs no graph
model and uses the same mirror, never a separate roles cache.

Caching remains an explicit freshness decision for the whole operation/policy.
A mixed operation cannot combine cached relations with current SQL roles. Cold,
expired, oversized or incompatible cache work retries the entire operation on
one durable view. Guards/ambient transactions bypass cache. Retain the current
one-independent-mirror and shared-receipt tradeoffs; no regional/Cluster redesign.

### Exact rollback boundary

Before the first committed incompatible schema/protocol fence, transaction
rollback leaves the prior compatible authority intact. The offline cache-prefix
upgrade can itself make older generation-cache processes incompatible. Base
0008 COMMIT drops the old tables and definitively fences old durable binaries;
cache 0003 additionally flips protocol/binding and fences old tuple-cache workers.
The earliest committed fence in the selected upgrade route is the restart
boundary, not universally cache 0003. Keep all old processes stopped throughout.

Ship a tested offline downgrade script driven by a consistent pre-upgrade
backup/export, including both original fact populations, audit, schema version
and cache configuration. Canonical data alone cannot recover the original split
when identical old facts merged; do not invent a generic inverse or a permanent
origin column to support it. Verify the backup and stop all readers/writers/relays
before work. The ordinary downgrade path must reject before writes if the
canonical authority or audit has changed since the upgrade baseline, or the
backup is missing/mismatched. Test exact restoration after each committed fence,
including cache-prefix upgrades, base 0008 and cache 0003, with a fresh cache
identity and rebuild before old cached readers resume.

Test refusal for new independent relations, global usersets, added canonical
audit and any other post-upgrade writes. Returning to the old schema after new
writes requires an explicitly chosen backup restoration that may discard those
writes; the framework does not silently resolve that loss. This is the precise
no-automatic-rollback boundary. A failed migration before its commit still rolls
back normally; a committed incompatible fence is reversible only through the
validated downgrade/restore path, never by restarting an old binary.

## Integrated implementation checkpoints

P1 is complete. Tasks below are decomposition units, not separately releasable
features. The first subsequent integrated checkpoint includes core, memory,
mutations, PostgreSQL, Turso, cache interfaces and all conformance consumers;
it must leave the workspace green. In particular, shared-fact reads cannot ship
before cross-facade mutation invariants, and changed cache interfaces cannot
leave Redis uncompilable. No tag before the full validation/release gate.

### U0 — Remove only authorization Firestore and harden test fixtures

- **depends_on:** [P1]
- **files:** `pockets/authorization/stores/firestore/`; `go.work`, `go.work.sum`,
  `Makefile`, `.github/workflows/` references if present, architecture/authorization
  docs and release inventory; `pockets/authorization/stores/turso/conformance_test.go`
  and all destructive live-fixture setup helpers.
- **description:** Remove the authorization adapter/module from active support and
  all workspace/CI matrices. Preserve Firestore connector/authentication adapter.
  Before any destructive Turso suite opens/migrates/deletes data, require an
  explicitly supplied disposable/truncate-safe test URL allowlist/identity check.
  Apply the check centrally and reject mismatches before connecting or truncating.
- **verify:** workspace inventory/guard checks; unit tests reject arbitrary,
  malformed and mismatched Turso URLs and accept the explicit disposable fixture;
  `make check` with external service settings unset.
- **done:** no active authorization Firestore dependency; shared Firestore modules
  still pass; destructive tests cannot target an ambient application URL.

### U1 — Freeze unified APIs, model and coherent transaction support

- **depends_on:** [U0]
- **files:** `pockets/authorization/logic/tuples/` read/write/snapshot contracts;
  `logic/decisions/`, `logic/model/role_model.go`, `logic/roles/`, authorization
  constructors/options; `integrations/datastores/pgxdb/{tx.go,transact.go}` and
  connector tests/docs; Turso transaction contract/tests as needed; `Makefile`.
- **description:** Implement the T2/T3 API replacements as one compile-visible
  design and fixtures, select connector ambient isolation support, and add
  tuples-leaf, mutations→decisions/no reverse and roles→tuplecache import guards.
  Consumer-owned optimized graph ports stay outside tuples. Document errors for
  unsupported weak ambient reads and pin full-expression validation semantics.
  Remove the transitional `logic/model/reference.go` const/error/function
  forwarding shim and migrate every caller to the owning tuple definitions.
  The true SubjectRef type alias may remain where it expresses the same value.
- **verify:** API Example/compile tests; connector isolation/lifecycle tests;
  negative import-guard fixtures; core build/test/vet/race and make guard.
- **done:** public contracts are concrete and consumed; no second model/evaluator
  or temporary role-specific neutral read layer is introduced.

### U2 — One memory authority, evaluator and canonical mutations

- **depends_on:** [U1]
- **files:** `pockets/authorization/stores/memory/{memory.go,roles.go,mutations.go,
  audit.go,read_snapshot.go,lookup_snapshot.go,read_model.go,tuple_cache.go}`;
  `logic/decisions/`, `logic/mutations/`, `logic/audit/audit.go`, roles/listing
  facades; `pockets/authorization/stores/storetest/`; HTTP results/errors/tests.
- **description:** Replace separate owned memory fact slices with canonical facts;
  implement exact roles plus the one decisions expression engine; migrate atomic
  writes, guardian enforcement, audit and listings together. Remove all retiring
  fields/JSON/outcomes in T6 with their tests. No origin-based role classification.
- **verify:** core build/test/vet/race; owner+member invitation regression, exact
  scope/global cases, coherent A-revoke/B-grant, cross-facade guardian protection,
  explicit swaps/audit rollback, usersets/Through/limits and cursor order.
- **done:** real constructor paths and runnable Example demonstrate model-free
  roles and coherent All/Any; all canonical facades share invariants and identity.

### U3 — Transactional SQL authority, upgrade and downgrade

- **depends_on:** [U2]
- **files:** `pockets/authorization/stores/{pgx,turso}/migrations/0008_iam_tuples.sql`;
  constructor/schema probes, relationship/role/fact queries, read snapshots,
  mutation/audit implementations, applied migration and downgrade tests; adapter
  READMEs; `AUDIT.md`.
- **description:** Implement one SQL authority and bulk exact reads in both dialects;
  apply transactional preflight/copy/dedup/audit/drop; revise ambient READ COMMITTED
  behavior explicitly; add global-grant preflight/report and transition metric.
  Supply direct-upgrade and tested backup-based downgrade runbooks.
- **verify:** both modules build/test/vet/race; real PG default/named schema and
  SQLite applied migrations, late-failure rollback, direct upgrade chains,
  backup downgrade acceptance/rejection and canonical storetest parity.
- **done:** applied schema and real writes prove full-key uniqueness, old tables
  gone, history intact, exact semantics and no weak compound ambient result.

### U4 — Canonical cache interfaces, protocol 0003 and Redis parity

- **depends_on:** [U3]
- **files:** `pockets/authorization/logic/tuplecache/`, decisions cache integration;
  both SQL `cache_migrations/0003_iam_tuples.sql`, a fresh canonical-cache
  baseline tree/export/source, sources/probes/tests;
  `pockets/authorization/stores/goredis/{tuple_cache.go,scripts.go,end_to_end_test.go}`
  and protocol/key/binding/limits tests.
- **description:** Use explicit scope-aware canonical wire/key identity and new
  Redis protocol prefix; capture every fact; enforce binding/freshness choices,
  atomic mixed publication, exact-ID acknowledgement and existing recovery limits.
  Update every cache-interface consumer before the integrated checkpoint closes.
- **verify:** actual SQLite/PG→Redis cold/warm/revoke/race suites, old worker and
  old prefix rejection, source/policy mismatch, global/userset keys, failed ack,
  capacity/deadline fallback, full rebuild and ambient/guard bypass.
- **done:** all four authorization modules and workspace pass; no partial
  authority or old protocol can read/ack canonical work. This closes the first
  integrated implementation checkpoint, not the release gate.

### U5 — Performance suite, examples and final public documentation

- **depends_on:** [U4]
- **files:** SQL/core/Redis benchmark suites; `pockets/authorization/BENCHMARKS.md`,
  core/adapter READMEs; `examples/auth-cms/cmd/server/` and tests;
  `ARCHITECTURE.md`, `AUDIT.md`, `RELEASING.md` preparation notes.
- **description:** Update all repository consumers to final APIs, including HTTP
  JSON removals. Document model-free exact roles, explicit global composition,
  optional graph, mutation swaps, migrations and cache freshness. Benchmark bulk
  SQL before recommending cache; separate rejected work from completed fallback.
- **verify:** full public-surface inventory; repeatable benchmark matrix below;
  actual auth-cms unauthenticated/deny/allow/revoke behavior if wiring changes;
  build/test/vet/race and final make check.
- **done:** no old fallback/model/exclusivity guidance survives as current advice,
  examples run and performance claims match measured end-to-end behavior.

### U6 — Coordinated release readiness, no premature tag

- **depends_on:** [U5]
- **files:** this record, final contract/runbook fixes and later release manifest.
- **description:** Independently review the final diff and rehearse direct upgrade,
  malformed/preflight refusal, downgrade refusal/restore and protocol flip.
  Prepare authorization core, stores/pgx, stores/turso and stores/goredis as one
  coordinated release train from the same reviewed commit. No authorization
  Firestore tag. A changed PG connector adds a fifth release artifact; pin and
  verify it dependency-first, rather than implying only four artifacts changed.
- **verify:** make check; required real-service races and migration rehearsals;
  independent GOWORK=off candidate build/test/vet/checksums and public API consumer
  checks per RELEASING.md when a release is authorized.
- **done:** all validation and independent review pass. Tags/publication require
  the owner's separate release instruction; do not tag the foundation alone.

## Verification and fixture safety

No root go.mod exists. Use repository goimports, then:

- Core `pockets/authorization`: `go build ./...`, `go test ./...`,
  `go vet ./...`, `go test -race ./...`.
- PG adapter: the same commands; run real races with an explicitly disposable
  `POSTGRES_TEST_DSN`, then named-schema and non-C-locale fixtures.
- Turso adapter: the same commands plus `go vet -tags=integration ./...`.
  Before `go test -race -tags=integration -count=1 ./...`, the new truncate-safe
  URL check must pass before any Open/migration/DELETE call. Absent safe fixture
  means explicitly skipped remote execution, not fallback to an application URL.
- Redis adapter: the same commands and actual SQL→Redis
  `go test -race -tags=integration -count=1 ./...` with disposable PG/SQLite and
  redis-server. Do not silently count a skipped PG leg as verified.
- Connector changes: build/test/vet and appropriate real transaction tests in
  each changed connector module, preserving unrelated default transaction use.
- Root: `make guard`, `git diff --check`, `make check`; inspect generated diffs.
  The module count becomes 42 after authorization Firestore removal. Shared
  Firestore connector/authentication tagged compilation remains in the matrix.

Read-only preflight can run on host-supplied exports, but this task never performs
application DB migrations. Destructive suite safety is a code prerequisite, not
an instruction that depends only on the operator remembering the right URL.

## Regression and benchmark matrix

Correctness must cover owner+member invitation coexistence; exact full-key
idempotence; same-label facade equality; explicit scope/invalid scope; global
usersets without implicit graph roots; concrete versus userset membership;
model-free roles; unified named model; All/Any skipped-branch validation/error
semantics; snapshots and READ COMMITTED rejection; guardians through role writes;
exact swaps/reconciliation/teardown; single canonical audit deltas; HTTP removed
fields/errors; global transition report; applied migrations/downgrades and cache
protocol/binding/key/freshness behavior. Check/explain/filter/full and paged lookup
must agree, including explicit Unrestricted branches and byte-order cursors.

| Dimension | Cases |
|---|---|
| Checks | Exact global/scoped, deny, explicit global/scoped Any, All, userset, Through |
| Batch | 1, 20, 128 and near configured limit; distinct scopes and repeated subjects |
| Storage | Recorded old baseline, canonical bulk SQL, cold and warm cache |
| Shape | Many subjects per relation, many scopes per subject, multiple labels, global/local overlap |
| Concurrency | Guarded writers, unrelated publications, revoke during mixed operations |
| Delivery | Full rebuild, small delta, hot set, obsolete backlog and capacity recovery |
| Cost | Latency, allocations, SQL/Redis round trips, hits/fallbacks, publication bytes/time |

Extend existing families `BenchmarkRoleBatchReads`, `BenchmarkDecisionsPostgres`,
`BenchmarkThroughBatchSQLite`, guarded-writer benchmarks, `BenchmarkTupleCache`,
`BenchmarkTupleSourceBacklog`, `BenchmarkSQLRedisDecisions` and publication-overlap
benchmarks. Run `-benchmem -count=5` with documented benchtime/cardinality/versions.
Report actual fallback cost including SQL retry; do not present fast capacity
rejection as completed authorization throughput. Local benchmarks are not a
production load guarantee.

## Final acceptance and exclusions

- Memory, PG and SQLite/Turso contain one canonical authority; Redis mirrors it.
  Authorization Firestore is removed while unrelated Firestore modules remain.
- Independent owner/member facts coexist. HasRole/HasRoleIn are exact and require
  no graph model; one decisions evaluator composes policies coherently.
- Canonical mutations, guards, audit and cache capture share one boundary/delta;
  no role facade bypass, implicit fallback or ambiguous replacement remains.
- Applied 0008 and optional 0003 migrations, direct upgrades, global preflight,
  protocol fences and backup-based downgrade are tested and documented.
- All four authorization modules are green together; complete validation precedes
  one-commit release preparation, and publication remains separately authorized.
- No app changes, online dual-write migration, compatibility namespaces, exclusive
  group DSL, universal admin bypass, NOT/attribute policy, regional mirrors or
  unrelated SDK changes are included. No mandatory v0.17 intermediate deployment.

## Consultation and settled decisions

Architecture/backend/product reviews agree on kind-free facts, explicit scopes,
exact role APIs, one evaluator, canonical mutation enforcement and a leaf tuples
package. The owner removed authorization Firestore and accepts breaking APIs.
The ambient connector ruling is TransactSnapshot plus actual bound-transaction
SnapshotIsolation inspection as recorded in T4. U1 settled the single
`decisions.Model` declaration and explicit expression constructors; there are no
remaining design decisions for this implementation.

Recommended final reviewers: lead-backend-engineer, data-integration-reviewer,
product-manager, architecture-steward for imports/API and platform-sre for
protocol/cutover/rollback. Review concrete code/evidence, not only this plan.

## Durable execution record

- P1 implemented `logic/tuples/{reference,scope,subject,tuple}.go` with
  comparable values, invalid-zero scope and shared reference validation.
  Tests are `reference_test.go`, `scope_test.go` and `tuple_test.go`; SubjectRef
  tests live in tuple_test.go, not a separate subject_test.go.
  `logic/model/reference.go`, `logic/roles/role.go` and
  `logic/relationships/relationship.go` consume the values/conversions.
  `tuple_values_test.go` covers cross-facade equality and scope/userset identity.
- The tuple-leaf import guard is G26 (G25 already names another guard); its
  label/comment/error and architecture reference have been corrected. Core README
  documents the current conversions; architecture carries the lasting package
  rule. Neither links readers to an ignored, in-progress plan.
  P1 changes no stored authority, role check semantics, model or cache protocol.
  Assignment.Validate accepts the same value set and preserves errors.Is
  classification, but now checks scope before subject/label and changes the
  partial-scope error text. Its complete observable behavior is not unchanged.
- P1 verification passed core build/test/vet/race, tuple-package fuzzing and the
  full then-43-module make check, including generated-artifact and import guards.
  Initial cache/socket sandbox restrictions were resolved before those passes.
  Live SQL migrations, remote Turso and new SQL/Redis benchmarks were not run
  because P1 does not implement them. No later-phase behavior is claimed verified.
- Authorization Firestore source and active references are removed; the workspace
  has 42 modules. P1 review fixes are applied. The full 42-module `make check`,
  authorization core `go test -race ./...`, all 36 Python script tests and
  `git diff --check` pass. Remote datastore suites were not run. The completed
  removal record is `plans/authorization-firestore-retirement.md`.
  This was the pre-U1 checkpoint; no commits, tags or production mutations were performed.
- Remaining: U0's disposable Turso URL enforcement, then U1–U6. Record exact later commands/results and unresolved failures
  here as they complete; do not rely on temporary log paths as durable evidence.

- U0/U1 prerequisite: disposable Turso URL enforcement is implemented; tests require
  an exact explicit AUTHORIZATION_TURSO_DISPOSABLE_URL match before destructive
  fixture work. pgxdb.TransactSnapshot and bound Tx.SnapshotIsolation passed
  build, vet and race tests against an isolated local PostgreSQL 17.4 fixture.
- U1–U4 API freeze: tuples.Reader/ Snapshotter/ Storer are raw canonical ports;
  role Assignment carries Scope. HasRole is exact global, HasRoleIn exact scoped,
  and HasRoleInOrGlobal explicitly probes both in one view. decisions.Model plus
  WithModel replaces both old model declarations. Tuple-key v2 includes scope
  kind and every identity component. All guarded adapters call mutations.Plan
  for one canonical delta; audit Change is Action + Tuple with encoding metadata.
- Core production tuple/role/relationship/mutation/audit/memory packages build.
  Memory now owns one map of canonical facts. The decisions package tests pass,
  including migrated graph regressions and exact expressions. Core tuplecache
  and Redis build/test/race/vet passed; Redis exercised an isolated local fixture.
  One-iteration cache benchmarks are smoke checks, not performance conclusions.
- Integrated gate remains OPEN: SQL migration/adapter work, legacy test migration,
  example/docs/transition diagnostics, applied upgrade/downgrade checks and full
  benchmark matrix are still in progress. Current full workspace is not yet green.
  Ownership: implementer SQL/connector, product_manager reassigned decisions and
  root/HTTP tests, planner reassigned cache/shared conformance/memory tests; root
  owns canonical domain, memory production, composition, example and final gate.
  Role overrides were explicit because the session could not spawn more threads.
- U2/API migration now passes core build/test/vet and targeted race for decisions,
  HTTP and root composition. Raw roles/mutations/audit tests have been migrated;
  canonical tuple selectors/cursor validation and pure mutation invariant tests
  were added. Root now binds explicit shape validation to RoleWriter as well as
  RelationshipWriter; model-free opaque labels stay valid. Guardian construction
  refuses an explicitly userset-only protected relation, without imposing a
  permission-model catalog on opaque labels. Ordinary zero-bound system commands
  now default affected rows to4096; teardown remains the explicit exception.
- Shared conformance/memory race tests pass with one authority. PG full build,
  race/count1 and vet pass in both default and named schemas on a private
  PostgreSQL17.4 en_US.UTF-8 database, including recursive collation and applied
  migration tests. SQLite build, tagged integration race/count1 and tagged vet
  pass against an explicitly authorized disposable local file. Remote Turso has
  not been used. Actual PG+SQLite→Redis tagged race integration passes without
  PG skips. Temporary logs are supplemental; these results and limits are the
  durable execution summary.
- auth-cms full suite passed with local HTTP listeners (16.509s server package,
  1.176s document listing). It covers authenticated allow/deny/revoke and both
  raw/guarded invitation owner+member coexistence. Global auditor alone now gets
  scoped-policy403. The SQL business-list EXISTS predicate uses iam_tuples.
- DiagnosticGlobalGrantNotApplied is opt-in, default no reads, maximum8 exact
  probes per owned operation in the same snapshot, at most1 low-cardinality event
  after successful completion. No IDs/labels are included. Explicit guard views
  do not emit. ModelSnapshot now deep-copies nested expressions/resources and
  exposes a complete expression accessor; duplicate NewSchema permission names
  fail compilation while explicit MergeResourceType overrides remain deliberate.
- Core/current workshop/example docs and AUDIT-042 now describe unified behavior,
  exact scopes, migration routes and fences. Old UPGRADE/CONVERSION records remain
  clearly labeled historical. G27 guards decisions against mutation imports;
  G19 blocks role→engine/cache imports and G26 keeps tuple vocabulary a leaf.
  The existing owner handoff/cacher files still match their saved hashes.
- Still OPEN: stricter base-schema constructor probes (root review found table
  existence alone accepts partial canonical schemas), backup/downgrade scripts
  and rehearsals, repeated benchmark summary, replacement of obsolete byte-cache
  verification harness with current suites, final workspace/race/fuzz checks and
  independent review. No tags or commits have been made.
- Final verification/review checkpoint: all42 module build/test/vet and tagged
  compilation passed; make check then caught a new test using Underlying. It now
  uses the connector's pool option and the full guard matrix passes. Core full
  race/count1 passes; docs pnpm typecheck/build pass. Bounded fuzz runs pass:
  tuple opaque identity875,616 executions; canonical cursor1,415,907 executions.
- Repeated performance matrix complete:106 cases, five1s samples each,530 valid
  samples. BENCHMARKS.md contains commands, machine/fixture versions, medians,
  ranges, allocations and limits. Warm Redis benefits some PG graph/role work;
  local SQLite exact predicates can be slower with Redis. Large hot sets have
  linear publication/decoding costs; capacity rejection is not complete fallback.
- SQL applied-schema probes now reject partial/altered canonical columns,
  primary keys/constraints, retained legacy authorities and obsolete unique
  indexes. SQLite TEMP cannot supply authority or mask missing durable tables.
  PG event replay now orders qualified numeric IDs;9→10/99→100 and actual
  alternating SQL→Redis revoke regression pass. Both full PG schema variants
  and full safe-file SQLite integration race/build/vet pass after these fixes.
- Independent review found portable graph fanout/expansion-state parity and
  expression lookup budget gaps. Root fixed portable reads to match model-filtered
  subject-first closure, seed-inclusive state accounting and canonical reverse
  enumeration; new portable/optimized parity races pass. Root fixed bound-check
  cancellation and MaxEvaluationSteps overflow sentinel validation. Implementer
  is fixing shared expression discovery/candidate budgets with regressions.
- Offline downgrade rehearsal passes supported fence routes, overlap restoration,
  canonical/audit divergence refusal and late-error rollback. Independent SRE
  review additionally found unsupported custom IAM tables/columns could be
  discarded. Strict schema/object/dependency checks are being finalized before
  the source-pinned owned harness runs. No permission to discard custom data is
  inferred from the upgrade request.
- Verification runner replaces obsolete byte-cache generated hosts with source-
  pinned core/connector, both PG schema variants, safe SQLite, real Redis and CMS
  HTTP suites. Named proofs and any SQL/Redis skips fail the run. Benchmark mode
  requires the entire106-case inventory with exactly5 samples per case; parser
  validated against actual corrected measurement logs. Python safety/coverage
  regression suite37 tests passes. Real all-mode run remains pending final edits.

- Final independent backend review: no remaining blockers after portable reader,
  expansion-state, expression lookup-budget, cancellation and overflow fixes.
  Full core build/race/count1/vet passes. Trace comments now match retained
  partial evaluator traces and discarded completion/cancellation results.
- Final independent SRE review: downgrade/tooling ship-ready within the explicit
  supported schema boundary. Unknown IAM tables/columns/functions/triggers and
  external foreign keys are refused; ledger additions require exact source,
  filename, checksum and SQL. PG default ownership/privileges are required.
  Backup baseline is base0007 (including publishedv0.13), optionally cache0001/0002;
  old source prefix, base0008 and cache0003/fresh baseline restore routes pass.
  PG dump/self-lock deadlock fixed by blocking writes before validation and
  acquiring exclusive locks only afterward; timeout cleanup regression passes.
  Independent SQLite downgrade9 methods and runner safety12 tests pass.
- Final make check PASS across42 modules, all architecture guards and tagged
  compilation. No templ/generated UI drift. Final docs typecheck/build PASS.
  The source-pinned owned all-mode rehearsal is now running with explicit
  fail-on-skip backend proofs; it is the last pending acceptance gate.

## Final verification and handoff

The final source-pinned owned rehearsal passed and cleaned all owned processes and
storage. No PostgreSQL, SQLite or Redis behavioral proof was skipped. It ran
connector/core races, both PostgreSQL schema variants on a non-C locale, tagged
SQLite and SQL-to-Redis races, applied migration/downgrade scripts, and the CMS
HTTP/SQL-listing suite. The memory ambient-transaction test explicitly skips
because memory has no host transaction seam; the SQL variants passed. Packages
with no tests are not counted as behavioral skips.

| Suite | Passed named tests including subtests | Behavioral skips |
| --- | ---: | --- |
| `integrations/datastores/pgxdb` | 301 | None |
| `pockets/authorization` | 1177 | TestTransactional |
| `pockets/authorization/stores/pgx (public)` | 550 | None |
| `pockets/authorization/stores/pgx (authorization_named)` | 550 | None |
| `pockets/authorization/stores/turso` | 550 | None |
| `pockets/authorization/stores/goredis` | 52 | None |
| `examples/auth-cms` | 522 | None |

The full 42-module `make check`, documentation typecheck/build, formatting and
diff checks pass. The benchmark matrix contains106 cases ×5 samples; both10s fuzz
runs pass. Independent backend and platform/SRE reviews have no remaining
blockers. Earlier OPEN checkpoint entries above are chronological and are closed
by this final result.

Durable evidence: [verification and changed-file inventory](authorization-unified-tuples-verification.json),
[benchmarks](../pockets/authorization/BENCHMARKS.md),
[upgrade guide](../pockets/authorization/stores/UPGRADE.md),
[PostgreSQL restore](../pockets/authorization/stores/pgx/scripts/README.md), and
[SQLite restore](../pockets/authorization/stores/turso/scripts/README.md). Temporary
raw logs are supplemental; this record does not depend on their retention.

Remote Turso, production-scale load and a new Linux CI run remain unverified.
The downgrade boundary deliberately refuses unsupported schema extensions and
PG ownership/privilege customizations. No host databases, 360/Segovia applications,
commits, tags or deployments were changed. The preserved owner plan files still
match their starting hashes.

Next release work is the dependency-first five-artifact train in RELEASING.md:
pgxdb connector, authorization core, PG store, Turso store and Redis store. Assign
versions/pins and perform isolated GOWORK=off archive/consumer verification only
as part of that separately authorized release. No authorization Firestore tag.
