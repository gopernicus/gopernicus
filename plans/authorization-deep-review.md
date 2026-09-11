# Authorization: independent second review

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Follow-up implementation authorized by the owner and tracked in
[authorization-followup-implementation.md](authorization-followup-implementation.md).
The review-only restriction below is historical; its findings remain the rationale.


Status: REVIEW COMPLETE — 2026-09-11. The owner requested a fresh, deep review of the
recommendations and the whole authorization pocket after the first implementation.
Priorities: correctness, readable code, an API that is hard to misuse, and honest
flexibility for hosts with different models, stores and consistency requirements.

## Scope and working rules

- Branch `firestore-authentication`, HEAD `6807ed06`, 42 modules. Preserve the
  completed implementation and all earlier dirty/user files. Pre-review hashes:
  `/tmp/gopernicus-authorization-deep-review-baseline.json`.
- This pass reviews and produces findings/design recommendations. Production
  source, migrations, consumer apps and AUDIT are unchanged. Use scratch probes
  or an isolated source snapshot for new diagnostic code, never an application DB.
- Read current source before relying on the prior verdict. Prior implementation
  context: [authorization-audit-implementation.md](authorization-audit-implementation.md).
  Guard ownership: [authorization-guard-dependency-design.md](authorization-guard-dependency-design.md).
- Two fresh independent reviewers follow the named lead-backend-engineer and
  data-integration-reviewer roles. The root reviews public/host/listing workflows,
  verifies concrete leads and reconciles recommendations. Their configured opus
  model is unavailable; inherited models preserve the read-only role behavior.
- All Go/make commands use `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.
  Formatter for any scratch Go: `/Users/jrazmi/go/bin/goimports`.
  Previous seven DB/emulator fixtures were removed. Create new isolated fixtures
  only when a concrete finding needs live evidence; record and remove them.

## Review packets

- [x] Public API and host recipes: construction, roles/relationships, raw facts
  versus permission decisions, baseline/guarded writes, gates, administration
  routes, errors/outcomes, package/type vocabulary and extension seams.
- [x] Evaluator: current model on every edge, deterministic OR/error behavior,
  context and limits, cycles/depth, memo correctness, Check/Explain/batch/filter/
  lookup agreement, and implementation complexity.
- [x] Persistence: all four adapters, reader contracts, transactions, dependency
  closure, concurrent changes, guardian invariants, idempotency/replay, cursor
  semantics, data ownership and meaningful test coverage.
- [x] Host listing: complete IDs, candidate filtering and constrained SQL;
  tenant/search/sort/cursor invariants, error/partial-page behavior, cost bounds,
  caller effort, privacy, changing data and the practical tuning defaults.
- [x] Evidence: reproduce strongest findings, distinguish bugs from declared
  limitations and future design choices, reconcile independently found issues.
- [x] Close: prioritized findings with source locations, smallest coherent plan,
  host examples for important API choices, explicit verification limitations,
  unchanged-source inventory and any fixture cleanup.

## Acceptance for findings

Each correctness finding needs a concrete trigger, affected contract and either
a reproduced result or an explicit source-derived confidence limit. API critique
needs an ordinary host task and a realistic mistake the API invites. Recommend
the smallest complete correction; do not add abstraction solely to generalize.
Preserve opt-in capabilities that solve distinct host needs. State when a change
would need a deliberate host policy decision or a breaking migration.

The output must also say which parts are worth keeping, which complexity is
necessary, and which previous recommendations should be reconsidered. Passing
the first gate is evidence of existing coverage, not proof that the design is
finished or that untested semantics are correct.

## Findings and verification

Review complete. The findings below distinguish reproduced defects, source-only
findings and deliberate API recommendations. No implementation changes were made.

## Verdict

**Keep the architecture; repair the mutation guarantees before adding features.**
The first implementation fixed meaningful model/read/listing problems, but its
passing tests did not cover several important adversarial cases. This review
reproduced eight correctness/robustness defects through public APIs or live
PostgreSQL, plus two host-API problems under documented behavior. A further
Firestore retry finding is source-confirmed, not reproduced against a live
Firestore service. These are findings against the current tree, not claims that
all were introduced in the previous implementation.

The main simplification is to separate what the host asks from how the store
protects it. Hosts should describe principals, permissions, resources and changes.
The pocket should own dependency keys, revision bookkeeping, input ownership,
retry classification and rejection reporting. Different host policies should fit
through small explicit seams without requiring different meanings for the same
check on different entry points.

## Correctness findings, ordered for implementation

### AR2-01 — High: negative userset guards are not protected against phantoms in PostgreSQL

Locations: `stores/pgx/mutations_eval.go:534` (userset traversal), `:554` and `:590`
(reached scopes only), `stores/pgx/mutations.go:248` (dependency lock set).

The forward traversal records the resource and usersets already reachable from
the principal. It does not record a membership that is currently absent into an
unreached group. This matters when a valid host guard authorizes from a negative
answer, such as “this person is not blocked.” The contract in
`domain/mutation/repository.go` and the existing dependency design explicitly
includes absent facts; positive-path tests do not establish that guarantee.

**Reproduced on fresh PostgreSQL 17 with the race detector:** create
`doc:d1#blocked@group:g1#member` and `doc:d2#blocked@group:g2#member`. Alice initially
belongs to neither group. Command A adds Alice to g1 only if she is not blocked
on d2; command B adds Alice to g2 only if she is not blocked on d1. Hold both
guards after their negative read, then release them. Both commands commit.
Either serial execution would reject the second command.

The observed dependencies were the checked doc at revision 1 and
`resource:user:alice` at revision 0; neither contained the relevant group. All
writes used the mutation repository. This defect is independent of accepted
baseline/guarded mixing limitations.

**Recommendation:** protect the predicate the guard actually read, including
absence. Evaluate a target-rooted userset walk that records the referenced group
before checking its membership, against PostgreSQL serializable transactions
with whole-operation retries. Choose using the smallest complete implementation
and measured contention; do not bolt a second positive-path recheck onto the
existing forward traversal. A generic negative guard cannot be advertised as
atomic until this case is covered. Do not silently forbid negative policies:
they are a legitimate host need.

Acceptance: the two-command write-skew schedule cannot commit both; direct,
nested-userset, empty membership and Through dependencies remain correct; budgets
and cancellation still bound the work. Verify all adapters, with a concurrency
harness that also accommodates memory/libSQL serialization rather than requiring
both guards to enter concurrently.

### AR2-02 — High: global-role guard reads can protect the wrong principal

Location: `stores/pgx/mutations_eval.go:621` (`HasRole`), specifically the caller
scope recorded at `:625` and the separately supplied subject queried at `:632`.
The same dependency-reporting shape exists in the other adapters; PostgreSQL is
where the live revision race was demonstrated.

A natural reusable guard is:

```go
view.HasRole(ctx, attempt.Scope, "admin", attempt.Actor.Type, attempt.Actor.ID)
```

It works for a resource operation. For Alice assigning a global role to Bob,
`attempt.Scope` is Bob's subject scope. The method reads Alice's global admin
role but records Bob's revision. The API accepts these arguments without rejecting
or correcting the mismatch.

**Reproduced:** while Bob's guarded assignment paused after reading Alice's
admin role, a command revoked Alice's admin role and committed. Bob's command then
committed without detecting a changed dependency. Its dependency list contained
only Bob. The finding is the missing promised revision validation; overlapping
operations alone would not establish a serializability violation.

**Recommendation:** derive global dependencies from the queried principal now.
At the public boundary, expose an explicit global-role check and a resource-role
check; callers should not supply a subject revision key separately from the
subject being checked. Reject invalid scope kinds before any read. Test true and
false global answers, scoped fallback, mismatched subjects and concurrent revoke.

### AR2-03 — Medium: a guard can rewrite a command after structural validation

Location: `mutation_service.go:132` (`composeGuard`).

`ProposedChange` receives the original command's role and relationship slices.
A synchronous callback edit changes the command that the store subsequently
applies. Passing `MutationAttempt` by value does not copy its backing arrays.

**Reproduced in memory through `Service.AssignRole`:** request Alice's global
admin grant; inside the guard set `attempt.Change.Roles[0].SubjectID = "bob"` and
return nil. Bob receives the grant, while Alice receives the scope revision and
receipt. Structural validation would reject that row/scope mismatch if it ran
on the modified command. This is an accidental-host-mutation hazard, not a claim
that an untrusted client can supply the guard implementation.

**Recommendation:** copy proposed rows inside each callback invocation so the
original command stays unchanged and retries each receive the original proposal.
Test both row types and retry isolation. Do not treat a second validation as a
substitute for input ownership.

### AR2-04 — Medium: PostgreSQL SetRelationTargets can succeed with the wrong state

Locations: `stores/pgx/relationships.go:577` (resource/relation lock), `:599`
(conflict preflight), `:619` (delete old targets), `:622` (generic creation),
`:522` (bare `ON CONFLICT DO NOTHING`).

The preflight and insertion do not have the same conflict boundary. A concurrent
writer on another relation can claim the desired subject after the preflight.
The generic creation path silently ignores that unique-subject conflict, after
reconciliation has already deleted surplus targets.

**Reproduced on PostgreSQL with the race detector:** seed reader=old; hold an
uncommitted editor=new tuple; request reader=new; observe reconciliation's insert
waiting on the competing transaction in `pg_stat_activity`; commit the competing
transaction. Reconciliation returns nil. Reader has no targets: new is missing
and old was deleted. The first scratch attempt omitted a required synthetic
`created_at`; that fixture error was corrected before the successful proof.

**Recommendation:** use reconciliation-specific insert/conflict semantics and
rollback the entire reconciliation on a conflicting subject. Generic additive
creation's intentional no-op behavior is not an implementation of “make this
exact set true.” Preserve exact duplicates and retained row identity/timestamps.
Cover concurrent raw creation and different-relation reconciliation, including
ambient host transaction rollback.

### AR2-05 — Medium: cycle validation explores exponentially many paths

Locations: `internal/logic/authorizersvc/schema_validator.go:132` and `:198`.
A new DFS starts for every Through edge and forgets completed nodes, so valid
convergent graphs repeatedly explore the same suffix. Runtime evaluation limits
do not protect construction.

**Reproduced through NewService:** four distinct parent relations per level,
all targeting the next level's `view`; the leaf grants via `owner`. There are no
stored tuples and the graph is valid. One local diagnostic run measured:

| Types | Through checks | Construction |
| --- | --- | --- |
| 6 | 20 | 1.5 ms |
| 9 | 32 | 37.6 ms |
| 11 | 40 | 432 ms |
| 13 | 48 | 7.19 s |

These are diagnostic timings, not a production benchmark. The code establishes
the repeated-path cause; the small model demonstrates its practical consequence.

**Recommendation:** one graph cycle pass with unvisited/active/completed node
states, keyed by `(resource type, permission)`. Keep the sanctioned
same-permission self edge and deterministic error reporting. Canonicalize edges
once. Test linear work structurally, not with a fragile millisecond threshold.

### AR2-06 — Medium: role decisions ignore MaxEvaluationSteps

Location: `internal/logic/decisionsvc/role_permission.go:12`.
The common role evaluator never receives or charges the configured step limit.
This affects ordinary and guarded checks despite the shared-budget contract in
`Config.Limits` and README. Role model size bounds work in a different sense;
it does not enforce the host's configured limit.

**Reproduced:** three unheld grantor roles, `MaxEvaluationSteps: 1`. CheckExplain
returns ordinary denial with nil error, makes six exact/global store probes,
and reports three role steps.

**Recommendation:** define the small shared accounting rule (root plus visited
permission rules) and enforce it in the common role evaluator, including guards,
explain, batch and verification. Through depth and relationship fan-out naturally
remain relationship-specific. Do not introduce a generic budget framework or
silently redefine existing limits as advisory. Include early-allow cases.

### AR2-07 — Low: maximum lookup limit overflows the lookahead cap

Locations: `internal/logic/authorizersvc/limits.go:131`, `budget.go:94`, and
pagination's `limit + 1` in both engines.

**Reproduced with a public custom store:** construction accepts
`MaxLookupResults = maxInt`; LookupAllResourceIDs passes
`-9223372036854775808` to the reader on this 64-bit machine. Non-positive limits
mean unbounded in the memory adapter/port, and SQL can fail instead. No huge
allocation or dataset was needed for the proof.

**Recommendation:** reject values that cannot reserve a lookahead element during
limit resolution, and use checked arithmetic wherever request limits are added.
This is configuration robustness, not an anonymous request exploit.

### AR2-08 — Low: empty lookup results can ignore cancellation and start another read

Locations: `internal/logic/authorizersvc/lookup.go:175` and `:404`.

**Reproduced with a custom scoped reader:** a successful first empty read cancels
the context. The engine starts a second direct lookup with that canceled context,
and eventually returns successful emptiness with nil error. This violates the
method's explicit promise to check cancellation before every read. A similar
empty-source completion boundary exists in `FilterPage`; that second site is
source-reviewed only.

**Recommendation:** check context at reader boundaries and before final successful
completion, including the empty-result case. Do not promise to eliminate the
unavoidable race after the final context check.

### AR2-09 — Medium, source-confirmed: Firestore suppresses retries for storage errors returned through a guard

Locations: `stores/firestore/mutations.go:164`, `:129`, `:135`, and
`stores/firestore/contention.go:64`.

Every error returned by the guard is marked terminal. A normal callback that
returns a retryable contention error from `view.CheckRelation` therefore prevents
retry, even though an equivalent read error outside the callback retries. Vendor
retry identity is detached before the outer loop handles the terminal flag.
Existing tests enshrine the broad “guard returned it” classification; they do not
exercise view-originated failures through a normal callback.

**Recommendation:** distinguish a policy refusal from a known retryable
transactional-read failure before detaching vendor identity. Preserve stable
policy refusals as terminal. Require a deterministic view-read error injection
and real contention test. This is an availability/retry defect, not an observed
false allow. No new Firestore emulator/production run was performed in this pass.

## Host API and simplicity recommendations

### D1 — Let the host declare guardian rules; make impossible wiring fail at startup

`domain/mutation/guardian.go:55` defaults to one direct `owner` on every resource
type. The store receives this policy independently of the model and cannot tell
that the model has no owner relation. This is documented, previously accepted
behavior; the second review recommends changing it deliberately.

**Reproduced:** a model with `doc.reader` and `view = Direct(reader)` constructs
successfully with the ordinary memory bundle. Its first trusted reader grant
returns `invariant_blocked`. The host cannot repair it with an owner grant through
the service because the model forbids owner. Groups with only a member relation
have the same configuration trap.

Prefer explicit host-owned invariant configuration, validated against the
current model when the guarded capability is constructed. Keep useful last-owner
protection as an opt-in policy. The storage transaction must still enforce it;
keep one policy source rather than duplicating mutable configuration between
service and adapter. Validate malformed rules and impossible minimums where the
model makes them provable. This requires a considered constructor/adapter change
and a breaking migration; it is not silently applied in this review.

Also keep the existing one-relation-per-exact-subject rule explicit. It models an
exclusive access level, not arbitrary independent labels. Multiple role grants
remain available. Do not add a configurable uniqueness mode without a concrete
host use case and corresponding invariant design.

### D2 — A refused write should be an ordinary Go error

`domain/mutation/receipt.go` deliberately separates command errors from outcomes.
As a result, the valid host model above produces `err == nil` even though the
write is refused. The conventional host code `_, err := GrantRelationship(...);
return err` reports success. Bundled role transport also has an all-outcomes-200
contract, although today's role operations do not normally hit relationship
conflicts/guardian rules.

Recommend stable errors for `semantic_conflict` and `invariant_blocked`, retaining
successful idempotent `no_change` and `not_found` results. Keep revisions,
idempotency and real committed receipts. Avoid calling an unpersisted refusal a
Receipt. An optional `RequireApplied` helper is weaker because every caller must
remember it; merely renaming Outcome does not prevent the mistake. Align HTTP
mapping and audit event classification with the final contract. Preserve replay
behavior and audit visibility of refused attempts. This is a deliberate breaking
API recommendation, not a claim that the existing implementation violates its
current outcome contract.

### D3 — Use the same business request inside and outside a guard

Prefer a guard method shaped like `view.Check(ctx, CheckRequest)` over
`CheckPermission(ctx, ScopeKey, permission, principalType, principalID)`.
A conceptual host callback becomes:

```go
result, err := view.Check(ctx, authorization.CheckRequest{
    Principal: attempt.Actor.PrincipalRef,
    Permission: "manage",
    Resource: authorization.Resource{Type: "document", ID: documentID},
})
if err != nil { return err }
if !result.Allowed { return sdk.ErrForbidden }
return nil
```

This is a proposal, not currently compilable API. The important part is that the
host names the resource it intends to authorize. It does not fabricate revision
keys. Offer clearly named global/scoped raw-role and relationship-fact methods
for hosts that need them. Keep `ForModel` and `Dependencies` on the adapter-facing
contract rather than the ordinary host-facing guard interface. A host needing
raw facts must choose them explicitly; do not silently reinterpret HasRole as a
model permission. Keep one model-driven evaluator behind all decision surfaces.

### D4 — Shrink required store interfaces before adding more generality

`relationship.Reader` and `Storer` both require `FilterRelation` and
`RelationTargetsFor`. A repository-wide production-call search found no callers
outside adapter implementations; the current engine uses memoized
`GetRelationTargets` and `CheckBatchDirect`. Conformance tests alone do not make a
method a necessary core dependency.

Remove these two methods from required interfaces at the next breaking boundary.
Retain a concrete/optional capability only if an actual host uses it; inspect
consumer call sites before deleting implementations. Keep the small
`PermissionReader` distinction and immutable `ReadModel`: they enforce current
host policy on every traversed edge. Do not fall back to unscoped reads when a
custom adapter lacks the scoped reader.

Do not move this pocket into SDK or build a universal policy/query planner.
Continue to use narrow host/pocket check seams. The existing model dispatcher is
reasonable: one owner per `(resource type, permission)` avoids implicit unions
and coupled failure behavior. Hosts can compose higher-level policy explicitly.
If the same custom policy needs reuse across list filtering and gates, evaluate a
small checker/filter capability; do not add an open-ended third engine on
speculation. Today `FilterPage` is tied to `*Service`, which limits that composition.

### D5 — Revise my earlier candidate-pull default

The previous change made zero BatchSize shrink to remaining page capacity. That
saves a few candidates but produces many round trips near the end of sparse
pages. A fixed pull of the requested page size is simpler, preserves the dense
case, and is a better default. Keep explicit BatchSize for host tuning and clamp
all pulls to the remaining scan budget.

**Measured through public FilterPage**, page size 10, 1,000 candidates, same
ordered results:

| Allowed density | Current shrinking default | Fixed 10 | Explicit 20 |
| --- | --- | --- | --- |
| 100% | 1 pull / 10 candidates | 1 / 10 | 1 / 20 |
| 50% | 5 / 19 | 2 / 20 | 1 / 20 |
| 1% | 282 / 901 | 91 / 910 | 46 / 920 |

These are operation counts from a deterministic fixture, not latency claims.
No adaptive-density estimator or shared cross-page cache is needed.

Keep all three listing recipes:

- Different stores: host-ordered candidates with per-row cursors and bounded
  filtering. `HasMore` means more candidates, not a guaranteed authorized row.
- Same database without joining: a complete bounded permission set before
  tenant/search/sort/pagination, when that set is affordable; otherwise the same
  candidate stream. A page of lexical IDs is not a complete filter.
- Same SQL database with joining: retain the constrained host adapter and its
  validated model shape. Do not claim its direct-user EXISTS example implements
  arbitrary usersets, roles, hierarchy or host bypass policy.

The host still owns tenant/search predicates, order and cursor binding. Neither
ID enumeration nor several candidate pulls promise a cross-request snapshot.
No strategy removes the need to authorize a later write at its own boundary.

### D6 — Keep ordinary state writes, but decide their consistency deliberately

The existing [dependency ownership design](authorization-guard-dependency-design.md)
is still accurate: ordinary writes do not advance guarded revisions, including
mutable parent/tenant topology. This is an accepted limitation, not AR2-01.
Separate capability possession remains valuable; don't force every bootstrap,
projection or synchronization into a permanent receipt ledger.

For a host such as Segovia with mutable topology that guarded policies read, a
receipt-free writer participating in the same revision protocol deserves its own
design packet. It must cover ambient SQL transactions, multi-scope discovery and
lock ordering, no-ops, all writers, and every adapter. Do not claim that sharing
a SQL transaction between business rows and tuples already solves this.
Keep the current contract while deciding that feature explicitly; do not silently
weaken atomicity or start reusing SystemMutator inside unsupported ambient
transactions.

### D7 — Clarify legality hooks and shorten historical commentary

`Config.AssignmentPolicy` runs only on the bundled assign route, before service
validation and outside mutation audit/transaction handling. A host that interprets
it as a rule for all assignments gets different behavior from direct service or
SystemMutator calls. Its documentation is honest, but the generic name invites
the wrong expectation.

If retained as route policy, name it accordingly. If hosts need a domain legality
rule beyond RoleModel, put a pure validator on the service's first-application
semantic-validation path, shared by actor/trusted writes; preserve old receipt
replay semantics. Do not add state reads there. Authorization remains in the
transaction-bound guard. Do not add both hooks without distinct demonstrated uses.

Public comments repeatedly carry old phase IDs (`AZ3`, `Q5`, “ratified default”),
all-caps arguments and descriptions of superseded designs. Move rationale/history
into these plans. Keep concise contracts, errors, concurrency requirements and a
small correct example beside exported code. For instance, the input and replay
contract on MutationGuard is necessary; paragraphs about which earlier phase
introduced its closure are not needed to implement a host guard correctly.
This is targeted readability work, not a file-count reduction exercise.

## What should remain

- Stdlib/domain-only pocket core and independently imported third-party adapters.
- Immutable compiled host models, model-scoped readers and typed concrete
  principals versus usersets. Current policy governs permission reads; raw stale
  facts can still be inspected and removed.
- One shared forward evaluator, bounded work, deterministic ordering, path-local
  cycle tracking, and call-local fact memoization. Removing these distinctions
  reintroduces real model/depth/budget bugs.
- Check/Explain/batch/filter/enumeration parity, with complete sets distinct from
  pages. Preserve privacy and honest continuation semantics.
- Optional bundled HTTP routes with host-supplied authentication/authorization
  middleware; construction rejects half-enabled security features. Host gates
  still own CSRF protection for cookie-authenticated writes.
- Separate trusted mutation capability; actor replay reauthorizes before exposing
  the stored receipt. Teardown stays explicit and requires a reason.
- Explicit SQL ambient-transaction support for baseline state, and explicit
  refusal for guarded commands until a compatible design exists.

## Proposed implementation packets

1. **Correctness first:** AR2-01 through AR2-04 and AR2-09. Write failing,
   behavior-based regressions before repairs. Use the existing mutation contract;
   do not combine this with naming or outcome migrations. Settle the negative-read
   dependency strategy with real concurrency evidence before changing all stores.
2. **Evaluator robustness:** AR2-05 through AR2-08. Linear compilation, shared
   role accounting, checked limits and cancellation boundaries. Compare all
   ordinary/guarded decision surfaces and custom scoped readers.
3. **Host contract:** D1/D2/D3/D7. Choose explicit guardian construction, normal
   error handling for refusals, a business-shaped DecisionView and one clear
   legality hook. Show roles-only, ReBAC-only and combined host examples before
   changing public signatures. Inventory consumers and record implemented breaks
   in AUDIT when this packet is authorized and complete.
4. **Small simplifications and listing:** D4/D5 plus targeted comment cleanup.
   Remove stale mandatory methods after consumer inventory; use a fixed page-size
   default; preserve the three concrete listing workflows. Keep D6's future
   revision-aware writer as a separately scoped design decision.

No packet is implemented by this review. Do not append proposed API changes to
AUDIT as if consumers should already migrate.

## Evidence, coverage and limitations

Root diagnostic source:
`/tmp/gopernicus-authorization-deep-review/review_test.go`.
Logs in that directory: `memory-probes.log`, `postgres-probes.log`,
`lookup-probes.log`. The test names and recipes above preserve the cases even
if temporary files disappear. These probes deliberately assert the existing
faults; their passing results are evidence of defects, not regression tests that
would certify a fix.

All Go commands use `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

Passed in this review:

- `go build ./pockets/authorization/...`,
  `go test -race ./pockets/authorization/...`, and
  `go vet ./pockets/authorization/...` from the workspace. This covers the core
  module, including public API, models, gates, bundled routes and memory tests;
  nested store modules are separate.
- Independent reviewer: `go test ./internal/logic/authorizersvc
  ./internal/logic/decisionsvc` and `go test ./domain/mutation ./memstore` from core.
- Independent store reviewer: `go test ./...` inside each of
  `pockets/authorization/stores/{pgx,turso,firestore}`. Database-dependent tests
  skipped without their DSNs; Firestore tagged live tests were excluded.
- Scratch memory/role/constructor/FilterPage probes:
  `go test -v -count=1 -timeout=45s -run
  'TestGuard|TestRole|TestConvergent|TestCandidate|TestDefault'
  /tmp/gopernicus-authorization-deep-review/review_test.go`.
- Lookup probes: same file with `-run TestLookup`.
- Fresh PostgreSQL scratch probes: same file with
  `-race -v -count=1 -run TestPG`, and `AUTHORIZATION_REVIEW_TEST_DSN` loaded only
  from the new fixture metadata. Final run passed in 2.109s.

One initial scratch compilation used the wrong role.Assign argument shape; it
was corrected to role.Assignment. The first PG reconciliation fixture omitted a
required timestamp; corrected and rerun successfully. Neither required source
changes. No unresolved test/tool failure remains from these probes.

Not claimed: full 42-module verification rerun, a new full live adapter matrix,
production Firestore behavior/index/IAM checks, real consumer migrations, or a
formal proof of authorization correctness. Previous implementation verification
is recorded separately; this pass does not borrow it as newly executed evidence.

The PostgreSQL timestamp-precision candidate was **not reproduced**: 20 first /
replay receipts matched on this machine's clock. Source still constructs an
untruncated Go time before PostgreSQL storage, so submicrosecond clocks deserve
a portability regression; do not elevate it to a reproduced defect.

Public API/host/listing review included construction and capability presence,
model composition, raw versus effective roles, ownership dispatch, guard/trusted
writes, replay/outcomes, gates and role-route decoding/policy/response boundaries,
complete IDs, candidate cursors and constrained SQL examples. The independent
reviewers inspected evaluators and all adapter families. Green existing tests
coexist with the new adversarial failures above; no new false allow was confirmed
in the ordinary model evaluator.

The single new PostgreSQL fixture was removed after verifying its recorded ID
and name. Metadata `/tmp/gopernicus-authorization-deep-review-postgres.json` now
records `cleaned_up: true`. No existing application container was changed.

Final comparison against the 2,138-file pre-review baseline found **zero source,
migration or AUDIT changes**, no deletions, and exactly these review/index changes:

- Added `plans/authorization-deep-review.md`.
- Updated `plans/framework-audit.md` with the current phase and handoff.
- Updated `plans/framework-audit-authorization.md` with the second-review link.

Inventory: `/tmp/gopernicus-authorization-deep-review/inventory.json`.
`git diff --check` passed for these paths. Earlier dirty/user work is preserved.
The next step is owner discussion of the proposed packets, then implementation
under a subsequent instruction. CMS remains excluded.
