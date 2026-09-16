# Authorization internal structure audit

Status: COMPLETED — audit only, 2026-09-16. Implementation is a follow-up.

## Context and goal

After unified IAM tuples and the separation of inbound admission from tuple
integrity, assess whether authorization retains dead code or internal boundaries
that no longer fit. Specifically distinguish useful role/relationship facades
from obsolete independent authorities or evaluators.

Audited branch: main, HEAD 6bad23cc8d9432a42f7125bf98ed695a817d2d6d;
latest authorization implementation commit f989b895. Preserve existing changes to
plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
plans/segovia-v2-audit-upgrade-handoff.md and .github/scripts/__pycache__/.
No applicable on-disk AGENTS.md or named project agent definitions were found.

## Scope

- Authorization root, logic packages, inbound HTTP, memory and supported adapters.
- Recent changes, repository consumers, architecture guards and current docs.
- Audit and evidence only; no runtime changes, schema changes, service mutations,
  commits or publication. Historical migrations and compatibility rejection are
  not dead merely because the current happy path does not execute them.

## Tasks

- [x] A1 Map the current public and internal dependency graph and recent removals.
- [x] A2 Trace role/relationship reads and writes to canonical tuple ownership.
- [x] A3 Confirm dead helpers, redundant state, misleading names and stale docs
      against repository consumers and public extension contracts.
- [x] A4 Run affected-module build/test/vet and focused architecture checks;
      use local temporary reproductions for concrete behavioral findings.
- [x] A5 Record ranked findings, recommended keep/remove/move decisions, exact
      verification results and limits.

## Verification constraints

This repository has no root go.mod. Verify core and nested authorization adapter
modules separately. Do not consume host datastore environment variables for
destructive tests. Audit-only changes require no Go formatting or generation.
Tests support specific findings; they do not establish complete security coverage.

## Execution record

- Confirmed clean authorization source at start; unrelated owner changes listed
  above remain untouched. Recent relevant commits: 66d70c51 (unified tuples),
  68866b39 (host denial/logging), f989b895 (inbound admission/tuple integrity).
- Read existing cleanup audit and current integrity policy record to distinguish
  previously resolved findings from current source.

## Conclusion

There is worthwhile cleanup, but no reason to rename all Go packages to
`iam_tuples` or collapse the authorization logic into one package. `iam_tuples`
names the SQL authority; `logic/tuples` already owns its canonical vocabulary.
Roles and relationships are useful ways to interpret the same facts.

The strongest restructuring opportunity is to reduce the older relationship
store facade alongside the newer tuple-backed service. This duplication already
has observable behavioral differences. A separate obsolete validation surface,
four private dead helpers, an orphan exported error, duplicate ordering code and
stale contract comments are smaller cleanup targets. No authorization bypass was
established by this audit; this is not an exhaustive security assessment.

## Current ownership: what should stay

| Package/surface | Current responsibility | Recommendation |
| --- | --- | --- |
| `logic/tuples` | Comparable scope/subject/fact values, exact queries, raw read/write/snapshot ports and ordering | Keep as canonical leaf; do not put graph evaluation here |
| `logic/roles` | Exact concrete-subject membership, explicit global/resource scope, assignment projections and writes | Keep the thin convenience facade |
| `logic/relationships` | Raw resource-scoped views/writes, plus shared model-filtered graph-read contracts | Keep facade semantics; untangle the large store contract and duplicated implementation |
| `logic/decisions` | Compiled expressions, graph traversal, checks, batch/filter/lookup and explanations | Keep the single evaluator |
| `logic/mutations` | Principal-free atomic commands, canonical delta planning and integrity | Keep; transaction ownership differs from raw writers |
| `logic/tuplecache` | Raw tuple mirror, snapshot/fallback lifecycle and optimized graph checks | Keep; not another role authority or decision cache |
| `logic/model` | Shared request/result/principal/resource/limit vocabulary | No demonstrated need to move it in this cleanup |

`constructor.go:68–97` builds decisions, roles and relationships from the same
`Repositories.Tuples`. Both ordinary writers call canonical tuple operations.
`mutations.Command.Requested` converts role/relationship command inputs into
canonical changes, and all bundled mutation adapters call `mutations.Plan`.
Memory owns one `map[tuples.Tuple]struct{}`; SQL reads/writes `iam_tuples`.

For example, assigning `viewer` to user Alice on document D and creating the
equivalent concrete relationship produce the same fact. `HasRoleIn` checks that
exact fact. A graph permission can additionally follow a stored `group#member`
userset. A global role remains a different scope. These distinctions justify
facades even with one storage model.

## Findings, ordered by recommended action

### A1 — Medium: two relationship facade implementations have drifted

The root's `relationships.Service` uses canonical `ListTuples`, `Contains` and
`Lookup` (`logic/relationships/service.go:63–148`). Adapters also retain a large
`relationships.Storer` implemented independently in memory, PostgreSQL and Turso.
The older store surface is publicly reachable through `Store.Relationships`,
`NewRelationships` and SQL `RelationshipRepository`; it is not dead code.

Confirmed using one memory store, one `doc:d#viewer@user:alice` fact and both
facades over that same store:

| Call | Older `store.Relationships()` | Root `components.Relationships` / canonical tuples |
| --- | --- | --- |
| List with `list.Request{Search: "absent"}` | Returns the fact, nil error | Rejects with `sdk.ErrInvalidInput` |
| List with `ResourceRelationshipFilter{Relation: &emptyString}` | Empty result, nil error | Rejects with `sdk.ErrInvalidInput` |
| Exact existence with an already-canceled context | Returns true, nil error | Returns false, `context.Canceled` |

Evidence: `stores/memory/memory.go:205`, `:497`, `:527` versus
`stores/memory/tuples.go:23`, `:123` and the core facade. These differences were
executed, not inferred from tests passing. They do not establish a permission
bypass in the normal decision service, which uses its own snapshot/check paths.

SQL has a related structural difference: old listing methods call the connector
list helper on `db.QuerierFrom(ctx)` (`stores/pgx/relationships.go:504–564`, Turso
equivalent), whereas canonical `ListTuples` obtains a whole-operation snapshot
(`stores/pgx/tuples.go:315–365`, Turso equivalent). The connector executes page,
previous-page probe and optional count separately. Outside a suitable ambient
transaction, the old SQL listing path therefore lacks the canonical path's
coherent view. This concurrency difference is code-inspected, not reproduced
against PostgreSQL in this audit.

**Recommendation:** route equivalent raw listing/count/exact-read operations
through one tuple-backed implementation. Keep optimized model-filtered graph
queries. Narrow or retire the oversized exported `relationships.Storer` in an
explicit API change after checking downstream callers. Preserve atomic broad
deletes/reconciliation and ambient contracts; replacing them with an unprotected
read-then-write sequence would be a regression. Add shared facade parity cases
for invalid filters, unsupported search, cancellation and snapshot behavior.

### A2 — Medium: old decision-service write validation contradicts current shapes

`logic/decisions/compiled_model.go:11–63` retains
`Service.ValidateRelation`, `ValidateRelationName` and `ValidateRelationships`.
They implement the old closed relation catalog: undeclared resource types or
relations are rejected. The actual writers use
`CompiledModel.ValidateTuple` (`logic/decisions/expression.go:185`), which accepts
opaque labels and enforces only explicitly declared subject-shape constraints.

Reproduction with `authorization.New` and no model:

- `components.Decisions.ValidateRelationships([doc:d#viewer@user:alice])`
  returns `unknown resource type "doc"`.
- `components.Decisions.CompiledModel().ValidateTuple(theSameTuple)` returns nil.
- `components.Relationships.ValidateRelationships(theSameRows)` returns nil and
  the actual relationship writer successfully stores the fact.

Repository-wide call search found no consumers of the three old **decision
service** methods outside their own internal call. Similarly named relationship
facade methods are live. External consumers cannot be ruled out because these
are exported methods. `GetSchema` and `SchemaDigest` in the same file are live
and should stay.

**Recommendation:** retire the old validation trio and its now-unnecessary
`decisions.CreateRelationship` alias in a documented API cleanup. If strict
catalog inspection is still wanted, give it an explicit inspection contract
instead of presenting it as the validation used by writers. Shape validation
should have one implementation on `CompiledModel`.

### A3 — Low: confirmed private dead code and one orphan public sentinel

The following private declarations have no code references, including tests,
in repository-wide searches:

- `logic/decisions/schema_validator.go:299`: `isSelfLoop`.
- `stores/memory/memory.go:183`: `checkRelationExpandedLocked`.
- `stores/memory/memory.go:571`: `keepRows`.
- `stores/memory/audit.go:84`: `relRow.toRelationship`.

`logic/relationships/errors.go:5` declares `ErrRelationshipsNotConfigured` but
no production path returns it; its only external reference is a negative
error-identity test in `codes_test.go`. This differs from
`ErrRolesNotConfigured` and `ErrNoDecisionKind`, which still serve nil-receiver
paths, and `ErrNoKindConfigured`, which is returned for missing tuples despite
its dated name.

**Recommendation:** delete the private helpers and orphaned imports (notably
the relationships import in memory/audit.go). Remove the public error only as
an explicit API cleanup; unused locally does not prove unused by downstream
hosts. Do not delete live graph helpers or live nil-receiver errors by analogy.

### A4 — Low: mutation planning still duplicates canonical tuple ordering

`logic/mutations/plan.go:75–91` contains a complete seven-component comparator
equivalent to `tuples.Compare`, added as the canonical ordering API during the
previous cleanup. Memory, cache and audit already use the canonical comparator.

**Recommendation:** use `slices.SortFunc(delta.Add, tuples.Compare)` and the same
for removals; drop the inline comparator and its `strings` import. No new
abstraction is needed. Current code is consistent, so this is maintenance risk,
not a reproduced ordering defect.

### A5 — Low: source contracts still describe retired guards and kinds

Examples in active Go documentation:

- `logic/tuples/store.go:165–166` says actor-facing writes use guarded mutations
  and raw writers apply structural validation only. Current raw writers also
  enforce configured integrity, and principal policy belongs at inbound.
- `logic/relationships/relationship.go` still describes count/existence helpers
  as the last-owner mechanism. Current integrity counts canonical concrete
  subjects through `IntegrityPolicy.ValidateState`; the raw count includes
  userset facts and is not an integrity count.
- SQL `relationships.go` comments describe a transaction-bound mutation
  `DecisionView` that was removed.
- `logic/relationships/order.go:5–7` describes the old six-component key and
  omits the current version/scope encoding.
- `logic/model/check.go` still talks about mutation principals, independent
  decision kinds and the retired `LookupResourcesIn` surface.

**Recommendation:** update these contracts alongside the focused cleanup. Keep
live sentinel identities unless deliberately changing the public API; a dated
name alone is not proof that the symbol is dead. Current README/architecture
already explain the newer model more accurately than these local comments.

## Restructuring that is optional, not required

The graph-read interfaces and `ReadModel` in `relationships/read_model.go` and
`set_reader.go` are live: SQL, memory and tuple cache implement them, and
`decisions.graphReader` consumes their optimized views before its portable tuple
fallback. `decisions/ports.go` aliases them; those aliases do not establish a
second evaluator. Do not remove these contracts or SQL graph optimizations.

For now, retain their package location and split the large `relationship.go`
into focused type/port files when touching it. If a later API revision extracts
graph contracts, they need a shared dependency-light home: moving them directly
into `decisions` would create a cycle because decisions imports tuplecache and
tuplecache implements those contracts. The existing `logic/model` vocabulary is
one candidate, but that move is not necessary to resolve this audit's findings.

`mutations.Command` also accepts Relationships, Roles and canonical Tuples.
These are live convenience encodings normalized by Requested, not separate
authorities. A later revision could normalize typed methods directly to tuple
changes and simplify the command union. That is broader public API work and
must preserve `not_found` versus `no_change`, bounds, reconciliation, teardown,
audit and transaction ownership. It is not the first cleanup to undertake.

## Suggested implementation order

1. Remove confirmed private dead code, reuse `tuples.Compare`, and fix stale
   comments. This can be a small implementation-only change.
2. Resolve the old public validation methods/sentinel with explicit downstream
   compatibility review. Preserve live snapshot/digest inspection APIs.
3. Consolidate duplicate relationship facade reads/listing and extend shared
   conformance coverage; retain graph fast paths and atomic store operations.
4. Reassess package extraction only if the reduced code still feels mixed.

No wholesale `roles`/`relationships` rename, new IAM package or schema migration
is justified by the current evidence.

## Verification and limitations

All commands used `GOCACHE=/tmp/gopernicus-go-build`. Database connection and
schema variables were removed for the module checks: POSTGRES_TEST_DSN,
POSTGRES_NON_C_TEST_DSN, POSTGRES_TEST_SCHEMA, TURSO_DATABASE_URL,
TURSO_AUTH_TOKEN and AUTHORIZATION_TURSO_DISPOSABLE_URL.

For each module below, `go build ./...`, `go test -json -count=1 ./...` and
`go vet ./...` passed:

| Module | Passed named tests/subtests | Skipped named tests/subtests |
| --- | ---: | ---: |
| `pockets/authorization` | 1256 | 1: memory TestTransactional, no ambient transaction seam |
| `pockets/authorization/stores/pgx` | 41 | 173: database-dependent tests, no fixture configured |
| `pockets/authorization/stores/turso` | 483 | 0 in the untagged suite; includes disposable local SQLite |
| `pockets/authorization/stores/goredis` | 43 | 0; local redis-server fixture exercised |

Also passed: core `go test -race -count=1 ./...`, the four memory/API reproduction
cases above, `git diff --check`, and these Make targets:

```text
guard-authorization-no-delivery-repo
guard-authorization-rolesvc-no-engine
guard-authorization-tuples-leaf
guard-authorization-decisions-no-mutations
guard-inbound-authorization
guard-tuple-write-integrity
```

Initial sandboxed checks hit a blocked local HTTP listener and unavailable
dependency downloads. The approved unrestricted rerun passed; neither was a
code failure. Installed staticcheck could not run: it was built with Go 1.25
while this repository requires Go 1.26.1, and its default cache was restricted.
Dead-code findings were therefore confirmed by declaration/reference inspection,
not a successful staticcheck run.

Not run: live PostgreSQL, remote Turso, tagged SQL-to-Redis end-to-end suites,
full 42-module make check, browser/manual host flows, load benchmarks or an
external-consumer compatibility sweep. Existing real HTTP tests ran as part of
the core suite. No runtime source, generated files, dependencies or migrations
were edited. No implementation changes are claimed complete.

Changed files: this audit and
`plans/authorization-internal-structure-audit-evidence.json`. Temporary logs and
the small Go reproduction live under
`/tmp/gopernicus-authorization-structure-audit`; the findings and results above
are durable independently of those logs. Owner changes remain untouched.
