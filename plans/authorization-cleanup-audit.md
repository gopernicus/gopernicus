# Authorization cleanup audit

Status: COMPLETED — audit only; cleanup implementation remains a follow-up.

## Context

Unified tuples and composable guards are implemented but remain uncommitted and
unreleased. The owner asks whether `internal/tuplekey` is still needed and what
cleanup remains. Evaluate actual use, public extension contracts and remnants
of superseded designs before proposing deletion or moving APIs.

## Goal

Produce a ranked, evidence-backed cleanup list and a concrete recommendation for
tuple key ownership, preserving useful internal implementation details.

## Scope

- Authorization core, supported store adapters, tests and current documentation.
- Preserve all existing worktree changes and owner plans.
- No production code changes, migration edits, datastore mutations or publishing.
- Historical migrations and explicit compatibility fences are not dead code.

## Tasks

### A1: Trace canonical key and cursor ownership

- Inspect tuplekey consumers, public tuple ports, list cursors and SQL encodings.
- Verify whether outside adapter authors and callers can implement pagination
  using the public contracts alone; distinguish identity, order and wire format.

### A2: Review transition leftovers and boundaries

- Use named backend and architecture reviewers for independent bounded audits.
- Inspect aliases, unused helpers, legacy middleware, dependencies and stale
  current documentation. Confirm actual references before proposing removals.

### A3: Validate and report

- Reproduce concrete failures using temporary local fixtures where helpful.
- Run focused existing tests with a task-specific Go cache as appropriate.
- Record findings with file/line evidence, priority, recommendation and required
  follow-up verification. Make no broad cleanup changes during the audit.

## Verification and limits

An audit is not an implementation sign-off. Tests only substantiate the inspected
behavior. No live service tests are needed for static placement or dead-code
findings; use owned memory/local fixtures for reproductions.

## Conclusion

The tuple-key behavior is live and needed. Keep a private codec for serialized
list cursors and old-format rejection, but stop requiring that private encoding
in the public raw tuple query. Prefer a typed `Query.After *Tuple`, with the
seven-component byte ordering and a comparator owned by `logic/tuples`.

This separates canonical fact identity, raw query position, and opaque user-facing
cursor encoding. `Tuple` is already comparable, so audit deduplication can key by
the tuple value and sort with the same canonical comparator. Memory/cache sorting
can use that comparator without encoding strings for every comparison. Preserve
the existing version-2 listing wire format during this cleanup.

## Findings

### A1 — Medium: public tuple pagination depends on an inaccessible private codec

`logic/tuples/store.go:43–54` requires `Query.After` to contain an opaque tuple key,
but `Reader.Lookup` returns only `[]Tuple`. There is no public continuation builder.
`stores/storetest/tuples.go:102` resumes by importing `internal/tuplekey.Encode`;
`logic/decisions/expression_lookup.go:449` does the same.

An external module can import the public tuple package, but importing the encoder
fails with `use of internal package .../internal/tuplekey not allowed`. Custom
adapters must also decode incoming raw continuations using an undocumented private
format or duplicate it. This is an incomplete public extension contract.

**Recommendation:** use typed `After *Tuple` for raw lookup, validate it in
`Query.Validate`, and document/provide canonical tuple comparison. Keep encoded
cursor handling behind list adapters. Moving Encode/Decode into `tuples` is a
smaller patch, but unnecessarily exposes a wire format to repair the raw-query API.

### A2 — Medium: memory and SQL disagree on invalid tuple cursors

`stores/memory/pagination.go:90–107` accepts a decoded cursor without checking that
its order value equals its primary key. A token with the old `role_key` field is
treated as no cursor and restarts the list. Both behaviors were reproduced using
public memory-store/list APIs.

PostgreSQL `stores/pgx/tuples.go:489–504` and the corresponding Turso validator
explicitly reject mismatched keys and a nonempty cursor decoded to nil. SQL
rejection was verified by code inspection, not a live SQL run in this audit.

**Recommendation:** align memory's strict tuple-cursor validation and add shared
conformance cases for wrong fields, mismatched keys, obsolete versions and valid
forward/backward continuations. This is a pagination contract mismatch; the
reproduction does not demonstrate an authorization bypass.

### A3 — Medium: legacy relation-to-permission introspection is incorrect

`logic/decisions/compiled_model.go:82–86` promises all permissions a relation grants.
Its reverse index in `compiler.go:267` uses only the flat graph projection
`perm.checks`, which intentionally excludes mixed and nested expressions.

Reproduced with `Any(Direct("viewer"), Role("admin"))`: a concrete viewer passes
`Check`, while `GetPermissionsForRelation("doc", "viewer")` returns no permission.
The backend reviewer also reproduced omission of a nested Any permission.

**Recommendation:** retire this production-unused introspection method and its
reverse index, or define a narrower accurate inspection API and test general
expressions. Merely collecting relation references would not justify claiming
that the relation alone grants an `All` expression. Keep live flat graph
projections used by batch/lookup optimizations.

### A4 — Medium: root wiring can split the advertised canonical authority

`config.go:22–28` accepts independent `Tuples` and `Relationships` repositories;
`constructor.go:89–98` constructs their writers against those separate inputs.
Two independent memory stores are accepted without an error. A relationship
write then succeeds while the decision service cannot see the fact, as reproduced.

This is a miswiring opportunity, not evidence that first-party constructor helpers
currently use different stores. Those helpers correctly share state.

**Recommendation:** derive the raw role/relationship facades from the canonical
tuple authority where practical. Keep optimized graph reads as explicit optional
capabilities. At minimum, make the shared-authority requirement explicit and
validate adapter identities when available; arbitrary injected repositories
cannot universally be proven identical by reflection or pointer comparisons.

### A5 — Low: remove confirmed dead helpers and misleading sentinels

The backend review and repository-wide reference searches found:

- `logic/decisions/compiler.go:486`: unused `dedupeSortChecks` (which no longer
  sorts or deduplicates despite its name/comment).
- `logic/decisions/compiler.go:501`: unused `checkString`.
- `logic/decisions/evaluate.go:250`: unused `checkDirectRelation`.
- `logic/decisions/evaluate.go:203`: ignored `checks` parameter on
  `checkPermission`; a Through path still computes the discarded argument.
- `logic/audit/audit.go:216`: `cloneChange` now returns its value unchanged.
- `logic/roles/service.go:14`: exported `ErrInvalidRoleAssignment` is never
  returned; validation uses canonical tuple errors.

**Recommendation:** remove the private no-ops/dead code. Retire the orphan error
as an explicit public API cleanup before the pending release, rather than imply
that callers can classify errors using it. Preserve expression order; do not
restore the obsolete sorting/deduplication behavior described by old comments.

### A6 — Low: the old graph snapshot capability has no production consumer

`relationships.LookupSnapshotter` at `logic/relationships/read_model.go:105–114`
and `ReadLookupSnapshot` implementations in the three supported authority adapters
are superseded by `tuples.Snapshotter`. The decision operation now asserts only
that canonical capability. Remaining old calls are test bridges and lifecycle
conformance helpers.

**Recommendation:** retire the old port/methods and move those tests onto canonical
snapshots. Preserve cancellation, panic cleanup, escaped-reader, ambient-write
and fractured-read coverage. **Do not delete entire `lookup_snapshot.go` files:**
they also contain live `Lookup*` reader methods used by canonical snapshots.

### A7 — Medium: current public comments still require obsolete semantics

The most consequential stale contract is
`logic/relationships/relationship.go:214–234`: it says a second relation on the
same resource/subject is silently ignored or rejected by exclusivity. Independent
labels now coexist. An adapter author following the comment would implement the
wrong behavior.

Other stale guidance remains in `logic/model/check.go:133–143`, the composite
engine comments in HTTP middleware, the effective-grant references in
`stores/memory/pagination.go:35–40`, and old root middleware names in
`ARCHITECTURE.md:231,248`.

**Recommendation:** update current contract comments alongside cleanup. Keep
historical migrations, old audit encoding support, upgrade/downgrade fences and
historical execution records: old terminology in those places is intentional.

### A8 — Design follow-up: graph contracts and HTTP compatibility remain split

`logic/decisions/ports.go:5–18` reexports graph contracts still owned by
`relationships/read_model.go`; the raw relationship `Storer` also requires graph
expansion and enumeration capabilities unused by its current raw service/writer.
This makes custom adapters implement more than the raw facade consumes.

Port relocation is not a mechanical alias inversion. Decisions currently imports
TupleCache, and TupleCache implements model-aware optimized membership reads.
Moving contracts into decisions directly would create a cycle. Those optimized
readers are live, analogous to SQL graph query adapters; this audit has not shown
that deleting them preserves performance. A follow-up design should compare
neutral snapshot injection/cache assembly at the root against consolidation of
portable traversal, preserving batching and proving graph parity/benchmarks.

The older HTTP permission gates and `Checker` capability were deliberately retained
by the composable-guard plan. Their independent snapshots/budgets and the optional
`ExpressionEvaluator` assertion are documented compatibility debt. Unifying or
retiring them is a behavior/API decision, not deletion of dead code.

## Recommended cleanup sequence

1. Remove confirmed private leftovers, fix current documentation and add cursor
   parity/introspection regressions. Decide the orphan public APIs explicitly.
2. Complete the typed raw pagination contract; narrow tuplekey to serialized
   cursor handling and use tuple values/comparison for identity/order.
3. Retire the obsolete snapshot capability while preserving its lifecycle tests.
4. Address facade authority wiring and graph-port ownership as a bounded design
   change, with cache/SQL parity and targeted benchmark checks. Decide legacy
   middleware convergence separately.

The first three are appropriate before publishing the pending breaking release.
No production edits were made during this audit.

## Verification record

- Branch `main`, HEAD `1e1308733cd8c23995497134052b2ef16c1f5b30`.
- External public tuple import compiled; private tuplekey import failed with Go's
  expected internal-package restriction. Downloads and persistent Go settings
  were disabled, and only temporary source files were used.
- Standalone memory-only reproductions confirmed A2, A3 and the A4 miswiring
  opportunity. Durable source and outputs are in
  `plans/authorization-cleanup-audit-evidence.json`.
- `go test ./internal/tuplekey ./internal/decisioncursor ./logic/tuples` passed.
- Architecture reviewer ran the tuple-leaf, roles-no-engine and
  decisions-no-mutations guards; all passed.
- Staticcheck was unavailable to the backend reviewer because its installed
  build predates this Go toolchain. Reference searches substantiate the specific
  dead-code findings; this is not a claim of exhaustive whole-repo dead-code
  analysis.
- No live SQL/Redis, full workspace, race or benchmark suite was rerun for this
  read-only audit. Their preceding implementation results do not prove a future
  cleanup. Cleanup requires fresh applicable checks.

Only this report and its evidence were added. Existing application source and
owner worktree changes were preserved. No commit, tag or external publication.
