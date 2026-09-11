# Authorization tuple storage and scope removal

Status: T1–T3 COMPLETE; durable receipt removal approved; audit design pending — 2026-09-11.

The owner authorized removing iam_scopes, relationship_id, and created_at from
relationship and role storage, with composite relationship identity. Tuple
metadata work is complete. The owner has now explicitly chosen to remove the
durable idempotency receipt machinery: a host that needs request deduplication
can implement it around this infrastructure. Whether to ship an optional audit
log is the current design question. Receipt/scope removal is approved but has
not yet been implemented; do not mistake this decision record for a code change.

Latest decision supersedes the older pending-replay discussion below. Keep
natural tuple no-op behavior, authorization checks, atomic writes and retained
guardian invariants. Do not reintroduce request keys, replay receipts or public
revision requirements to support auditing. Audit records may have their own
ordinary identity and timestamp without making them operation replay tokens.

## Preconditions and boundaries

- Branch firestore-authentication, HEAD 6807ed06. Preserve the large existing
  dirty tree and all previous audit work. Baseline (2155 source/document files):
  /tmp/gopernicus-authorization-tuples-baseline.json.
- Current implemented contracts are AUDIT-023/024. Prior fixtures were removed.
- Read ARCHITECTURE.md, pockets/README.md and the named implementer role. Use
  current architecture where role text contains retired paths. Inherited model
  is used because the configured opus model is unavailable.
- All Go/make commands: GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
  Formatter /Users/jrazmi/go/bin/goimports. There is no root Go module; 42 modules.
- Scope: authorization core, all four stores, affected examples/docs, append-only
  SQL upgrades, Firestore upgrade guidance, AUDIT-025. No consumer edits, release,
  publishing, production mutations, or CMS changes.

## Design fixed for independent work

- Remove the surrogate relationship ID from public input/projections and store
  records. Relationship identity is the full six-field tuple, including userset
  relation. SQL gets a composite primary key; Firestore's tuple-derived document
  key remains an internal implementation key.
- Remove relationship and role CreatedAt storage/projections and authorization
  Config.IDs, which exists solely to mint relationship IDs. Keep timestamps only
  for whatever history contract the owner selects.
- Relationship listings use canonical tuple field order, exposed as tuple_key;
  role listings use existing role_key. Default ascending byte order. Natural
  keys join validated components with U+0001 (already forbidden in components).
  SQL expressions explicitly use byte-order collation. Preserve exact userset
  identity in projections; do not keep an ID-shaped alias for the natural key.
- This does not itself change the independently enforced one-relation-per-exact-
  subject rule. Preserve it until the owner explicitly chooses otherwise.
- Remove scope revisions and ExpectedRevision/preconditions, with replacement
  concurrency proof for guards. Resource-scoped role assignments and host models
  remain: those are existing RBAC/ReBAC semantics, not the unimplemented policy
  authorization mode the owner originally meant by scopes.
- Metadata SQL upgrade is 0006; reserve the next migration for finalized scope/
  history changes. Do not rewrite earlier migration sources. Tests must cover
  both upgrade preservation and fresh installs.

## Work packets

- [x] T1 Core tuple metadata, engine ID wiring, memory storage/listing, raw
  relationship/role conformance and natural-key pagination regressions.
- [x] T2 PostgreSQL/Turso tuple storage, composite key upgrade 0006, raw list
  order, mutation row metadata adaptation, relevant fixtures and upgrade tests.
- [x] T3 Firestore tuple metadata and surrogate-ID-claim removal, list/index
  contracts, mutation row metadata adaptation, upgrade guidance and tests.
- [ ] T4 Root scope-revision removal, transaction contract, selected history/
  audit API, guarded/trusted/core/HTTP/example migration, shared mutation tests.
  Final history semantics pending owner response; progress independent T1–T3.
- [x] T5 Tuple phase: live disposable-store conformance/races, host HTTP behavior, appropriate
  module build/test/vet, full 42-module make check/root guards, migration docs,
  final source inventory and fixture cleanup.

## Current audit recommendation (not yet approved)

The owner rejected framework-owned durable request deduplication. The existing
iam_mutations receipt table is to be removed, not renamed and presented as a
complete audit log. Current state still contains the old receipt implementation
until the next implementation packet runs.

Recommend opt-in authorization change history within this pocket. Useful records
identify the actor (or an explicit system source), time, and actual relationship
or role additions/removals, including exact userset identity. Replacements and
bulk cleanup must record the actual delta. No-op requests do not invent changes.
Permission checks and denied attempts are separate logging/telemetry concerns.

When enabled as durable history, write the audit entry in the same transaction
as the authorization change. Host applications own enabling it, retention,
access, presentation and exports. Existing best-effort AuditSink events lack
actual tuple deltas and do not cover ordinary RelationshipWriter operations;
they cannot truthfully be presented as complete committed-change history.
Coverage must include supported trusted and guarded write paths when enabled,
with explicit system attribution where there is no human actor. Direct database
edits remain outside framework-generated history. Avoid a general SDK audit
framework or a new mandatory history dependency for every host.

## Progress and verification

Implementation beginning with independent tuple metadata work. Append decisions,
changed files, concrete verification and unresolved failures here. No stale test
assertion or unsupported fixture is evidence of the intended new contract.

### Disposable fixtures (current phase)

Created 2026-09-11; unrelated containers left alone. Root owns cleanup.

- PostgreSQL: `gopernicus-tuples-pg-20260911`, ID `f6202b06b279`,
  `POSTGRES_TEST_DSN=postgres://audit:audit@127.0.0.1:55483/audit?sslmode=disable`.
- libSQL: `gopernicus-tuples-libsql-20260911`, ID `f90cbd19d9c1`,
  `TURSO_DATABASE_URL=http://127.0.0.1:58083`; the test helper requires a nonempty
  token, so tests use `TURSO_AUTH_TOKEN=owned-tuples-no-auth` against this
  unauthenticated local fixture.
- Firestore emulator: `gopernicus-tuples-firestore-20260911`, ID `21496d22ef58`,
  `FIRESTORE_EMULATOR_HOST=127.0.0.1:58084`,
  `FIRESTORE_PROJECT_ID=gopernicus-tuples-audit`.

All three reached readiness. T2 owns SQL fixture use; T3 owns the emulator.
These are local disposable fixtures, not proof of real Firestore index or
contention behavior.

### Root verification so far

- Core `go build ./...`, `go test ./...`, `go vet ./...`, and `go test -race ./...`
  passed with the new tuple projections. T1's subsequent validation tests are
  still in progress and will be included in the final gate.
- Root `make guard` passed; log `/tmp/gopernicus-authorization-tuples-guard.log`.
- Example host HTTP lifecycle first attempt failed because the sandbox forbids
  binding a local httptest port. Retrying with loopback access; this was an
  environment failure, not an assertion failure.
- All commands use the fixed GOCACHE above. No history/scope-revision API or
  receipt persistence has been changed while the owner decision is pending.

### T1 handoff and current boundary

Core tuple/role metadata removal, memory storage and natural-key pagination are
stable. Shared tests cover scrambled insertion, concrete/userset distinctions,
byte ASC/DESC order, cursor limits 1/2 with previous navigation, offset/count
parity, delete/reinsert stability, and retired-order rejection. Raw writers
reject malformed key components before publishing any prefix of a batch.
Focused build/vet/race checks passed, including full memory conformance.

The example host HTTP role lifecycle passed after allowing its local httptest
listener. The root handler order/response tests passed after adding explicit
400 cases for retired timestamp ordering. The earlier AUDIT.md content is
byte-for-byte preserved; AUDIT-025 appends only implemented tuple changes.

T4 remains deliberately pending the history contract. Scope revisions, mutation
IDs, receipts and expected-revision behavior have not been removed. The scope
and history APIs should be changed together once their intended replacement is
settled, preserving transaction-bound guards and refusing unsafe concurrency.

### Verification artifacts

- T1 packet: `/tmp/gopernicus-authorization-T1-report.md`; owned-file hashes:
  `/tmp/gopernicus-authorization-T1-files.json`.
- Phase baseline: `/tmp/gopernicus-authorization-tuples-baseline.json`.
- Current changed-file inventory (regenerated at close):
  `/tmp/gopernicus-authorization-tuples-changed-files.json`.
- Full-gate snapshot scripts prepared at
  `/tmp/gopernicus-authorization-tuples-snapshot.py` and
  `/tmp/gopernicus-authorization-tuples-run-check.py`. They copy tracked and
  nonignored untracked working-tree files without `.git`, preserving the dirty
  source tree while checking generated-file before/after drift. The real root
  `make guard` separately checks guards that need git.

### Next design discussion: audit versus retry history

The receipt ledger has a valid narrow purpose even without an audit trail:
preventing the same operation from being re-executed after a lost response,
including a delayed retry after an intervening revoke. It does not answer who
changed which tuples. A full audit log also does not automatically prevent
re-execution; that requires an operation key and atomic duplicate detection.

Recommendation remains actual change history as the useful host-facing feature,
with durable request deduplication optional if a host needs it. If both are
selected, one audit record can carry the idempotency key/digest and result; two
permanent parallel history tables are not inherently required. The owner has not
selected the final contract. Do not rename receipts to iam_audit and imply they
contain actors or tuple deltas, or silently remove replay protection.

### Source freeze and workspace gate

T1/T2/T3 runtime sources are stable. A read-only peer review of SQL and Firestore
metadata, key ordering, exclusivity and upgrade behavior found no additional
blocker after the Turso NUL-aware separator-preflight correction.

Snapshot: `/tmp/gopernicus-authorization-tuples-check-_3x4chi0`, initially 2168
working-tree files; manifest `/tmp/gopernicus-authorization-tuples-check-snapshot.json`.
One live-test destructuring fix in Firestore `writes_live_test.go` was copied
into the snapshot and its manifest before the gate reached live-tag compilation.
The fix adapts claimRefs from three results to two after ID-claim removal. It
changes no runtime source. Full-gate log:
`/tmp/gopernicus-authorization-tuples-make-check.log`.

### Final completed gates

All use `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

- Full 42-module `make check`: PASS (exit 0) on the source snapshot, including
  build/test/vet, integration/live-tag compile checks, and generation verification.
  Snapshot-generated files have zero drift. Final runtime/test files match the
  checked snapshot; only plan notes have changed since it was taken.
- Real working-tree `make guard`: PASS again after source freeze. Log:
  `/tmp/gopernicus-authorization-tuples-guard-final.log`. This covers the git-based
  guard that cannot run in the deliberately git-free snapshot.
- Changed Go files: all 68 pass `goimports -l`; changed-path `git diff --check`
  passes. No formatter writes were needed at this final check.
- PostgreSQL full `go test -race -count=1 -v ./...`: PASS on the default schema
  (114.553s) and `POSTGRES_TEST_SCHEMA=authorization_tuples` (104.813s).
- Turso full `go test -tags=integration -race -count=1 -v ./...`: PASS (36.857s),
  including all 22 separator/NUL preflight rollback cases.
- SQL report and exact commands: `/tmp/gopernicus-tuples-SQL.md`.
  Final logs: `/tmp/gopernicus-tuples-PG-full-default-final.log`,
  `/tmp/gopernicus-tuples-PG-full-schema-final.log`, and
  `/tmp/gopernicus-tuples-Turso-full-final.log`.
- The optional PostgreSQL non-C locale proof was not configured and skipped;
  catalog/SQL collation checks passed. Real Firestore remains unconfigured; an
  emulator run cannot establish production index deployment or contention.

SQL fixtures were released by T2 and removed by root after checking their exact
container IDs. Firestore emulator cleanup follows its final adapter checks.

### Tuple phase close

- Firestore full `go test -tags=integration -race -count=1` suite: PASS (241.611s),
  including the shared conformance suite (225.67s). Upgrade tests prove a
  103-record maintenance pass, rerun safety, metadata/ID-claim removal, preserved
  tuple/role facts, and rejected invalid legacy keys. Full-length natural-order
  cases cover values whose combined key exceeds 1500 bytes.
- Firestore build/test/vet, emulator/live tag compilation, index manifest parity
  and export tests passed. Failed upgrade errors identify the document to repair.
  Report: `/tmp/gopernicus-authorization-tuples-FS.md`; full log:
  `/tmp/gopernicus-tuples-firestore-full.log`.
- Known verification limits: real Firestore index deployment/contention and
  optional PostgreSQL non-C locale checks remain unverified. Memory and Firestore
  intentionally skip the connector ambient-transaction family; Firestore's
  explicit refusal checks pass. No unresolved test or build failure remains.
- All three run-owned containers were stopped/automatically removed; final
  `docker ps -a --filter name=gopernicus-tuples-` returned no rows. Unrelated
  containers were not changed.
- AUDIT-025 records this completed tuple phase; all preceding AUDIT.md bytes were
  checked against the phase baseline and preserved. No tags, commits, deployment,
  publication, external consumer changes or production data changes occurred.

T4 is the next step after the owner chooses the history contract. It must remove
scope revisions with a transaction/concurrency proof and implement the selected
history semantics. This tuple phase does not claim that broader redesign is done.

### Changed-file inventory for this phase

81 files (68 Go files), all within authorization and its audit/release docs.
Machine-readable before/after hashes:
`/tmp/gopernicus-authorization-tuples-inventory.json`; file list:
`/tmp/gopernicus-authorization-tuples-changed-files.json`.

- `AUDIT.md`
- `RELEASING.md`
- `plans/authorization-tuples-implementation.md`
- `plans/framework-audit.md`
- `pockets/authorization/README.md`
- `pockets/authorization/authorization.go`
- `pockets/authorization/domain/relationship/order.go`
- `pockets/authorization/domain/relationship/relationship.go`
- `pockets/authorization/domain/role/assignment_test.go`
- `pockets/authorization/domain/role/order.go`
- `pockets/authorization/domain/role/role.go`
- `pockets/authorization/internal/inbound/authorization/roles.go`
- `pockets/authorization/internal/inbound/authorization/roles_test.go`
- `pockets/authorization/internal/logic/authorizersvc/batch_reader_test.go`
- `pockets/authorization/internal/logic/authorizersvc/batch_reason_test.go`
- `pockets/authorization/internal/logic/authorizersvc/explain_test.go`
- `pockets/authorization/internal/logic/authorizersvc/limits_test.go`
- `pockets/authorization/internal/logic/authorizersvc/lookup_test.go`
- `pockets/authorization/internal/logic/authorizersvc/principalref_test.go`
- `pockets/authorization/internal/logic/authorizersvc/service.go`
- `pockets/authorization/internal/logic/authorizersvc/service_test.go`
- `pockets/authorization/internal/logic/rolesvc/assignment_validation_test.go`
- `pockets/authorization/internal/logic/rolesvc/service.go`
- `pockets/authorization/memstore/memstore.go`
- `pockets/authorization/memstore/memstore_test.go`
- `pockets/authorization/memstore/mutations.go`
- `pockets/authorization/memstore/roles.go`
- `pockets/authorization/relationship_writer_test.go`
- `pockets/authorization/stores/firestore/README.md`
- `pockets/authorization/stores/firestore/SCHEMA.md`
- `pockets/authorization/stores/firestore/UPGRADE.md`
- `pockets/authorization/stores/firestore/claims_test.go`
- `pockets/authorization/stores/firestore/conformance_live_test.go`
- `pockets/authorization/stores/firestore/doc.go`
- `pockets/authorization/stores/firestore/documents.go`
- `pockets/authorization/stores/firestore/firestore.indexes.json`
- `pockets/authorization/stores/firestore/fixtures_test.go`
- `pockets/authorization/stores/firestore/grants.go`
- `pockets/authorization/stores/firestore/indexes_test.go`
- `pockets/authorization/stores/firestore/integrity_test.go`
- `pockets/authorization/stores/firestore/keys.go`
- `pockets/authorization/stores/firestore/keys_test.go`
- `pockets/authorization/stores/firestore/lists.go`
- `pockets/authorization/stores/firestore/lookups_test.go`
- `pockets/authorization/stores/firestore/mutations_eval.go`
- `pockets/authorization/stores/firestore/mutations_live_test.go`
- `pockets/authorization/stores/firestore/mutations_test.go`
- `pockets/authorization/stores/firestore/relationships.go`
- `pockets/authorization/stores/firestore/roles.go`
- `pockets/authorization/stores/firestore/roles_test.go`
- `pockets/authorization/stores/firestore/tuple_order_integration_test.go`
- `pockets/authorization/stores/firestore/tuples.go`
- `pockets/authorization/stores/firestore/upgrade.go`
- `pockets/authorization/stores/firestore/upgrade_integration_test.go`
- `pockets/authorization/stores/firestore/writes.go`
- `pockets/authorization/stores/firestore/writes_live_test.go`
- `pockets/authorization/stores/firestore/writes_test.go`
- `pockets/authorization/stores/pgx/README.md`
- `pockets/authorization/stores/pgx/collation_test.go`
- `pockets/authorization/stores/pgx/expansion_work_test.go`
- `pockets/authorization/stores/pgx/explain_live_test.go`
- `pockets/authorization/stores/pgx/migrations/0006_iam_tuple_identity.sql`
- `pockets/authorization/stores/pgx/migrations_test.go`
- `pockets/authorization/stores/pgx/mutations_eval.go`
- `pockets/authorization/stores/pgx/relationships.go`
- `pockets/authorization/stores/pgx/roles.go`
- `pockets/authorization/stores/pgx/schema_probe_test.go`
- `pockets/authorization/stores/pgx/tuple_upgrade_test.go`
- `pockets/authorization/stores/pgx/upgrade_runbook_test.go`
- `pockets/authorization/stores/turso/README.md`
- `pockets/authorization/stores/turso/migrations/0006_iam_tuple_identity.sql`
- `pockets/authorization/stores/turso/migrations_test.go`
- `pockets/authorization/stores/turso/mutations_eval.go`
- `pockets/authorization/stores/turso/relationships.go`
- `pockets/authorization/stores/turso/roles.go`
- `pockets/authorization/stores/turso/schema_probe_test.go`
- `pockets/authorization/stores/turso/tuple_upgrade_test.go`
- `pockets/authorization/storetest/natural_order.go`
- `pockets/authorization/storetest/reference_validation.go`
- `pockets/authorization/storetest/roles.go`
- `pockets/authorization/storetest/storetest.go`
