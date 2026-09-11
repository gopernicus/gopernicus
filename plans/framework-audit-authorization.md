# Authorization pocket: audit and implementation plan

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Implementation completed and verified 2026-09-11 in
[authorization-audit-implementation.md](authorization-audit-implementation.md).
The independent [second review](authorization-deep-review.md) led to the authorized
[follow-up implementation](authorization-followup-implementation.md), with migration
in AUDIT-024.
The original audit/proposal below remains historical evidence; its pre-fix
behavior and earlier authorization status are superseded by that plan.

Status: AUDIT/PLAN COMPLETE — 2026-09-10; **no implementation authorized**.
Parent: [authentication-authorization-audit.md](authentication-authorization-audit.md).
Listing: [three-scenario design](authorization-listing-design.md) and
[measured findings](authorization-listing-findings.md).

Date: 2026-09-10. Review role: repository `.claude/agents/lead-backend-engineer.md`, read-only; inherited model because configured opus unavailable.
Active plans: `plans/authentication-authorization-audit.md`, `plans/authorization-listing-audit.md`.

## Verdict

**Plan fixes before further expansion.** The current architecture has useful, demonstrated contracts: immutable host models, explicit principal/userset distinction, independent ReBAC/role ownership, optional transport, baseline desired-state writes, and an optional atomic mutation boundary. Keep those. The main correctness work is evaluator work bounds/model-change semantics, then small constructor/memory-store/parity fixes. Do not replace authorization with a generic SDK interface during this audit.

No authorization source, schema, or shipped package documentation was changed in this review. Final comparison to `/tmp/gopernicus-auth-audit-readonly-baseline.json` found zero changed authorization files. This report is now preserved in plans/ for later contexts; its probes remain
temporary diagnostics. The linked listing design consolidates the separate review.

## Evidence and verification

Temporary source: `/tmp/gopernicus-authorization-core-probe_test.go`.
Probe log: `/tmp/gopernicus-authorization-core-probe.log`.
Run from `pockets/authorization`:

```sh
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test /tmp/gopernicus-authorization-core-probe_test.go -v -count=1
```

Ten probes passed **by asserting the current faulty behavior**, not by asserting a fix. They use public authorization APIs and memstore, with a counting/failing reader wrapper for the graph/batch cases. No production data or repository source changed.

Passed against current source:

- `pockets/authorization`: `go build ./...`, `go test -race ./...`, `go vet ./...`.
- `pockets/authorization/stores/pgx`: build/vet and `go test -tags=integration -race ./... -count=1`, on the parent's fresh audit-owned PostgreSQL fixture. Full suite passed in 82.290s. Default schema, not a second named-schema run. Log `/tmp/gopernicus-authorization-pgx-live.log`.
- `pockets/authorization/stores/turso`: build/vet and the same integration/race command, on fresh audit-owned libSQL. Passed in 24.811s. Log `/tmp/gopernicus-authorization-turso-live.log`.
- `pockets/authorization/stores/firestore`: build/vet and **hermetic** `go test -race ./...` passed, 1.810s. Log `/tmp/gopernicus-authorization-firestore-hermetic.log`.
- Core race log `/tmp/gopernicus-authorization-core-tests.log`.

SQL connection attempts initially failed because the sandbox blocked localhost; reruns with escalation succeeded. No remaining verification failure. Tests requiring `POSTGRES_NON_C_TEST_DSN` were not enabled. No Firestore emulator or real Firestore fixture was supplied/run, so Firestore transaction contention, real indexes, IAM and live query parity are **unverified in this audit**. Existing live-test source is evidence of intended coverage, not a claim it ran here. Existing SQL suites passing does not cover the new probes unless noted.

## Confirmed findings

### C1 — P1: distinct-state budget does not bound repeated permission traversal

Source: `internal/logic/authorizersvc/service.go:114` builds ordinary Check's budget over the raw store; `:250`–`:294` uses a path-local recursion stack, removing frames on return; `:339`–`:380` traverses every target again. `budget.go:67` charges a previously seen state zero additional budget. `service.go:395` shares a store-read memo for sequential batches but still repeats recursive evaluation. Explain uses the same walk, so trace allocation can amplify too.

Probe `TestAZCoreProbe_ConvergentGraphRevisitsExponentialPaths`: a depth-13 layered DAG, two nodes per layer with convergent paths, configured MaxThroughDepth=13, MaxGraphStates=27 and fanout=2 produced **16,383 GetRelationTargets calls over just 27 distinct states**, with no limit error. Default depth is 10; the probe intentionally uses a valid custom limit to make the failure clear. This is a graph-shaped work amplification issue, not an assertion that every ordinary check is expensive. SQL/remote stores multiply it into round trips; the reproduction counted memory-backed port calls.

**Smallest sound direction:** first pin one decision's traversal semantics and make repeated completed work reusable or explicitly charge traversal work. A read memo alone only removes network amplification; it does not bound repeated CPU/trace work. Do not blindly memoize a path-local cycle-deny as a globally final deny: depth remaining and active-cycle context affect valid reuse. Reuse a worklist/fixpoint evaluator only after preserving root-relative depth, error/short-circuit, reason and Explain contracts; current `evaluate_set.go` is not automatically a drop-in substitute.

**Impact:** likely internal evaluator change plus possible clarified/additional limit behavior; no schema change. A new explicit work limit or changed exhaustion behavior is host-visible and belongs in AUDIT. Preserve `ErrEvaluationLimit` as indeterminate/unavailable, never truncate to deny/allow.

### C2 — P1 for migration policy; confirmed Check/lookup mismatch after model narrowing

Source: `service.go:339` checks only `target.IsUserset()` before navigating concrete targets, not whether their types remain permitted by the current compiled relation. `evaluate_set.go`'s `hop` has the same omission. `compiler.go:235` provides `relationResourceTargets`, and the lookup traversal uses that compiled allowlist. Current write validation uses compiled subjects.

Probe `TestAZCoreProbe_NarrowedThroughTypesStillGrantCheck`: initially `doc.parent` allows folder or org and a doc points to an org that grants user access. Reconstruct over the same store with doc.parent allowing folder only (org still exists elsewhere in the model). New ValidateRelationships rejects the org target; **Check still allows via org, while LookupResources returns zero document IDs**.

Probe `TestAZCoreProbe_NarrowedUsersetStillGrants`: remove `group#member` from doc.viewer's AllowedSubjects, retaining only user. Existing doc->group#member grant continues to allow Check. The current reader port only gets resource/relation/principal coordinates and a budget; it does not get the model allowlist for each userset expansion edge. Therefore merely filtering Through target types does not define all read behavior after a model change.

**Required design choice:** state whether AllowedSubjects is a write-only shape constraint requiring a host data migration, or a policy constraint enforced on every read. The former still must make Check/lookup consistent and clearly document safe model rollout; the latter needs model-aware direct/userset traversal as well as Through filtering. Do not present this as an arbitrary external attack against a valid unchanged schema: it requires old/off-model stored tuples. It is still material because the code supports schema upgrades and old receipts explicitly, and users can reasonably expect a narrower authorization model to remove authority.

**Recommended first slice:** specify/read-model migration policy and add a shared stale-tuple matrix before coding. Filter Through types consistently as part of that policy; do not claim this alone solves stale usersets. Add/remove relation, subject type, userset relation and target permission cases across Check, CheckBatch, FilterAuthorized, LookupResources and guarded CheckPermission.

**Impact:** behavior can revoke previously effective access. Record breaking behavior and consumer cleanup/rollout requirements. A model-aware reader design may change adapter ports. Existing tuple storage has enough identity fields; no schema change is intrinsically required for Through filtering. New model-version columns should not be assumed necessary.

### C3 — P2: optimized CheckBatch loses successful short-circuit semantics

Source: `service.go:434`–`:450` evaluates every direct branch against the entire resource-ID set even after an earlier branch grants every candidate. `Check` short-circuits, and the set evaluator removes granted candidates.

Probe `TestAZCoreProbe_OptimizedBatchVisitsAlreadyGrantedBranch`: model `view = Direct(a) OR Direct(z)`; `a` grants, reader for `z` returns an infrastructure error. Standalone Check allows; **a one-element CheckBatch fails on z**. This proves API inconsistency without ambiguous per-batch budget arithmetic.

**Fix:** track unresolved IDs in optimized batches, preserve first granting relation/reason and original output order/multiplicity, stop when none remain. Pass only unresolved IDs to subsequent branches. Add a regression with some granted and some unresolved IDs, duplicates, unknown permission, and a later failing branch. Preserve whole-batch errors for an unresolved candidate's necessary read.

**Impact:** successful results replace spurious errors; no signature/schema change.

### C4 — P2: guardian configuration retains mutable caller policy in all adapters

Source: `memstore/mutations.go:37`/`:53`; `stores/pgx/postgres.go:85`, `stores/turso/turso.go:75`, `stores/firestore/store.go:65` store GuardianPolicy by value but its Rules slice by reference; each mutation-store constructor retains it. GuardianPolicy is explicitly construction policy (`domain/mutation/guardian.go:30`).

Probe `TestAZCoreProbe_GuardianPolicyRetainsCallerSlice`: construct owner-minimum policy, establish an owner, confirm last-owner revoke is blocked, mutate the original rule's ResourceType, then repeat the revoke: it **applies**. Only memory was behavior-probed; same retained slice in SQL/Firestore is source-confirmed. This is host configuration aliasing, not an untrusted payload bypass. Concurrent mutation also races with evaluation.

**Fix:** snapshot Rules at option/store construction consistently (each resulting store must own its policy). Avoid adding a general policy registry or live setter. Consider rejecting malformed/negative guardian rules at construction separately; currently MinAnchors<1 normalizes to 1 by documented method behavior, so changing that default needs an explicit decision.

**Impact:** mutation of source config will stop changing live behavior, matching construction-policy intent. No API/schema change. Test each store or a shared option/conformance fixture with retained host slices and reused options.

### C5 — P2: memory receipts persist a field explicitly defined as nonpersistent

Source: `memstore/mutations.go:180` sets SameRoleGrantRemains, `:182` stores the entire receipt, and `:137` replays it without clearing the field. `domain/mutation/receipt.go:93`–`:110` explicitly requires false on replay. SQL inserts omit the annotation; Firestore receipt document conversion omits it.

Probe `TestAZCoreProbe_MemoryReplayPersistsNonPersistentAnnotation`: scoped+global editor grants; scoped unassign returns true as intended. Remove global grant and replay the scoped command: memory returns **Replayed=true, SameRoleGrantRemains=true**, contrary to the API contract. Existing `storetest/mutations.go:501` tests the first true annotation but not that receipt's replay.

**Fix:** store the durable receipt projection before annotating the returned copy, or explicitly clear nonpersistent annotations in stored copies. Test replay both while a global grant remains and after its removal, and verify no re-derivation is attempted. Keep the op-specific annotation concept; it tells a host why an exact scoped unassign did not remove the role via global fallback.

**Impact:** memory behavior correction; no schema/API change.

### C6 — P2: memory mutations can commit after cancellation during the guard

Source: `memstore/mutations.go:80` and `:96` check context before acquiring the mutex; `applyLocked:113` never rechecks it before replay/evaluation/receipt persistence. Shared cancellation test `storetest/mutations.go:1464` covers only cancellation before Apply.

Probe `TestAZCoreProbe_GuardCancellationStillCommitsMemory`: guard cancels the provided context and returns nil; memory then **writes the role and receipt**, verified by subsequent replay. A real deadline can expire while a guard is returning or while a caller waits for the store mutex. SQL subsequently performs context-aware queries/commit; the memory behavior is not equivalent.

**Fix:** check after acquiring the lock, after the host guard/semantic validator, and immediately before applying state changes. Avoid checking only after mutation and returning an error with committed rows. For long in-memory walks, make cancellation observable at meaningful work boundaries as part of C1. Do not promise an impossible atomic ordering between a last nanosecond cancellation and a completed mutation.

**Impact:** canceled operations stop changing state when cancellation is observed before mutation; no schema change. Add shared guarded-cancellation regression, memory lock-wait regression, and fresh-ID retry proving no receipt was consumed.

### C7 — P2: compiler symbol validation and cycle keys do not match public reference vocabulary

Source: `compiler.go:118`, `compileRelations:281`, `compilePermissions:332` check emptiness but do not apply relationship.ValidateRefField to names. Role compiler does apply it. Runtime request validation rejects control characters, invalid UTF-8 and overlong reference fields. `schema_validator.go:178` uses `fmt.Sprintf("%s.%s", targetType, check.Permission)` as a visited identity.

Probe `TestAZCoreProbe_MalformedModelNamesPassConstruction`: resource name containing newline passes NewService; every matching Check returns invalid input.

Probe `TestAZCoreProbe_ValidDottedNamesCollideInCycleDetector`: an acyclic chain `(root,view) -> (a.b,c) -> (a,b.c)` is rejected as a circular through relation because the distinct latter pairs both encode as `a.b.c`. Dot is permitted in validated reference fields. This is an identifier collision in the validator, not an actual graph cycle.

**Fix:** validate all model symbols with the shared reference validator, retain sorted aggregated errors, use a struct pair as the cycle-map key and keep formatted strings for diagnostic paths only. Reject/define ignored combinations such as a Direct PermissionCheck carrying an otherwise unused Permission field when tightening structural validation; that latter issue was source-observed, not separately behavior-probed.

**Impact:** rejects previously accepted unusable models and admits valid models previously misclassified; no schema change. Do not ban punctuation just to preserve an unsafe delimiter key. Add reference-field boundary matrix and dotted-pair cycle tests alongside existing real-cycle/self-hierarchy/immutable-digest tests.

### C8 — P2 boot quality: typed-nil repositories pass construction and panic on first use

Source: `authorization.go:475` uses interface `!= nil` to detect configured repositories. Inner services then retain those interfaces. Configuration promises construction-time checking, but does not distinguish typed nil.

Probe `TestAZCoreProbe_TypedNilRelationshipAcceptedAtBoot`: `var r *memstore.Relationships`; NewService with Relationships:r and a valid model succeeds, then first Check panics on dereference.

**Fix:** reject supplied typed-nil repositories/dependencies at composition boot, or explicitly declare that input unsupported consistently across pockets. For this framework's ease-of-use goal, early descriptive invalid-config errors are preferable. Cover Relationships, Roles, Mutations and optional Guard/Audit interfaces where applicable; ordinary nil must keep existing opt-in behavior. Do not recover arbitrary runtime panics or introduce a public generic null abstraction.

**Impact:** earlier errors for invalid host wiring; no schema change.

## Source-confirmed usability issue

### C9 — P2/P3: logger wiring depends on Register and never reaches SystemMutator

`authorization.go:597` assigns only Service.log from Mount.Logger. `mutation_service.go:211` declares SystemMutator.log, but construction does not initialize it and Register cannot reach it. Teardown logs in `mutation_service.go:363` therefore always use slog.Default. Headless guarded service audit-failure logs also use Default unless Register has run (`:639`). No live probe was needed: the field's only initialization is its zero value.

Prefer constructor Config.Logger defaulting once, propagated to both returned capabilities; keep Register optional transport composition. Do not add background lifecycle. Add logging capture tests for headless actor audit failure and trusted teardown, plus precedence when a mount logger differs. This is additive configuration and behavior clarification, no schema change. A startup warning for intentionally absent bundled routes (`authorization.go:611`) is also noisy for legitimate custom-route/headless hosts; downgrade/remove it unless missing routes are explicitly required.

## Intentional contracts and architecture to preserve

### Baseline and guarded writes are distinct, with an important dependency-closure limit

README:544–570 explicitly offers baseline state convergence and optional guarded invariants/receipts; baseline operations intentionally bypass scope revisions. Do not call that omission an accidental missing revision increment without revisiting the architecture. The mutation repository's comment claiming its two methods are the ONLY sanctioned write path (`domain/mutation/repository.go:117`) is stale relative to this ratified public baseline writer and needs reconciliation.

The current warning says not to mix paths for the same security-sensitive relation. It should say **every relation and role that a guard depends on must have compatible concurrency ownership**, including topology on another resource/type. Merely assigning different relation names to different write paths does not make dependency validation cover baseline writes.

Concrete host evidence: Segovia `pockets/auth/logic/guard.go:171` uses CheckPermission and `:209` reads a space's tenant relation inside the guard; `internal/outbound/domains/tenancy/authorization.go:72` and `:80` write tenant/parent through baseline SetRelationTargets. Its actor-write policy rejects topology mutations, but the guard still depends on topology. These are legitimate host needs and a reason to keep both capabilities, while clarifying which races that composition accepts. We did not run a host mutation race or alter host source. A later improvement could make baseline writers participate in revision fencing without receipts, but that changes cross-path concurrency and adapter transactions and deserves a separate design task, not a drive-by fix.

### Role semantics are deliberate, not accidental superuser behavior

`internal/logic/decisionsvc/role_model.go` immutably compiles declared per-type roles and permission grantors; duplicate ownership of a (resourceType,permission) across kinds is rejected. Global assignments have no resource type and the exact same role name can grant matching permissions across declared resource types. Scoped HasRole checks exact assignment then global fallback. An unknown pair denies. There is no hidden admin or role hierarchy bypass. Unassign and reads remain able to address assignments removed from the current model.

Keep RoleModel optional for opaque-role fact lookup; permission APIs require a model-bearing kind. Keep explicit ownership and reject conflicts instead of accidentally unioning kinds. Consider deduplicating/rejecting repeated names inside a permission's grantor list (`role_model.go:156`–`:171`); currently duplicates remain in the canonical list/digest and cause repeated probes. This is low-priority compiler cleanup, not a privilege escalation.

**Completion candidate:** transaction-bound DecisionView.CheckPermission refuses role-owned permissions and asks the guard to use HasRole (`mutation_view.go:32`–`:59`). That means a host adopting RoleModel still reconstructs its role-to-permission policy in guards or limits guards to exact roles. The present refusal is explicit and safe; do not remove it casually. A later bounded task could compose the role model over transaction-bound HasRole using the same model owner dispatch as normal Check, with global-scope dependency tests. No new schema is inherently required. This is more useful than an SDK authorization promotion.

### Gates, routes and write authorization

The gate family shares one body (`authorizersvc/middleware.go`), takes sdk.Principal from context, returns 401 without it, 403 for deny, 503 on evaluation-limit error and 500 on other errors. Coordinate and OR builders validate models at registration; RequireAnyPermission snapshots alternatives and intentionally fails the whole gate if an earlier necessary alternative errors. Host policy bypasses stay host closures. Keep these precedents.

A small consistency fix remains: legacy RequirePermission does not reject a nil resolver at mount (`middleware.go:138`–`:149`), whereas RequireAnyPermission does; this is source-confirmed potential runtime panic, not a separate HTTP probe here. Fixed-resource builder checks only nonempty ID; reuse reference validation when touching boot validation.

Bundled role routes mount only with RoleRoutesGate, roles and Guard; they derive actor from the request context, bound JSON bodies to 1 MiB, reject malformed transport input and route writes through the atomic guard. Reads rely entirely on the explicit host gate. AssignmentPolicy is a route-only legality precheck, not atomic permission policy; keep the distinction. Hosts own authentication/CSRF and resource-specific listing authorization. No new implicit middleware should be inserted.

### Mutations and adapter distinctions

Keep stable command errors separate from domain outcomes; receipt replay must still reauthorize an actor, while first-application model validation skips valid historical replays. Preserve one-scope commands and trusted teardown as an explicitly held capability. Best-effort AuditSink is not a transactional audit outbox, and teardown plus foreign-resource deletion is not a cross-store atomic operation; both limits are documented and need no invented feature now.

PostgreSQL uses ordered scope-anchor locks and records guard dependencies before reading their rows; live dependency/phantom/race/ambient-transaction suites passed. libSQL uses BEGIN IMMEDIATE plus retry handling; its global write serialization is a backend trade-off, not an abstract-port guarantee. Ordinary SQL repository calls join the matching connector's ambient transaction; guarded mutations deliberately refuse ambient transactions. Firestore deliberately refuses ambient transactions for every port method because read-before-write/no-read-your-writes semantics do not match the SQL contract; never silently run beside a host transaction. Firestore stages mutation reads/evaluation/writes, with attempt-local state and retry classification. Preserve those implementation details where they express real datastore constraints rather than forcing artificial parity.

Firestore's index manifest and boot probe isolate a real deployment concern. Hermetic index tests passing do not prove real index deployment. Do not simplify away deterministic tuple/claim keys, staged atomic writes, query chunking, or contention handling merely because they are verbose. They require isolated emulator and real-Firestore evidence before substantive changes.

Memory is a reference implementation with one global mutex and whole-slice scans. That is reasonable for tests/small hosts; it is not a high-volume database. Its raw methods mostly ignore context and expansion scans every stored row per reached state (`memstore/memstore.go:210`). The cancellation/graph work tasks should cover observable failures, without adding production indexing machinery solely to optimize the reference store.

### Limits and caching need precise claims

Memoization is call-local store-result caching, not a persistent decision cache (`batch_reader.go:57`). It copies cached values and does not persist errors; keep it. It does not provide an atomic database snapshot for an entire ordinary batch and must not be advertised as one. No cross-request cache should be added without invalidation and consistency requirements from actual hosts.

SQL boundedReachableCTE (`stores/pgx/relationships.go:41`–`:90`, corresponding Turso code) recurses over `(type,id,relation,depth)`, then DISTINCTs states and caps downstream results. The comments claim work depends on the configured state budget. Cycles revisit distinct depth rows, and downstream cap is not by itself evidence that database work or rows fetched are bounded. Firestore expandScoped (`reads.go:108`–`:150`) materializes query results before testing the new-state budget. These are **source-backed measurement gaps**, not quantified SQL/Firestore denial-of-service claims in this report. Parent/listing review owns work-cost measurement; add actual plan/port-count/row-count tests before making stronger complexity guarantees.

The root reviewer subsequently confirmed the root-relative depth issue in C10
below. The listing review also measured the PostgreSQL budget issue: budget 10
expanded 5,001 recursive states before returning overflow. See the linked
findings for actual plans and the proposed-only state-cap simplification.

## Additional root verification and recommended decisions

### C10 — P1/P2: batch contents change effective Through depth

Root follow-up, confirmed with public APIs and memory store. Source:
`internal/logic/authorizersvc/evaluate_set.go:95` seeds every candidate at depth
zero, `:199` records edges using the shared frontier depth, and `:231` propagates
grants without their distance from each candidate root.

Minimal fixture: node a -> parent b -> parent c, c.viewer = user u;
`view = Direct(viewer) OR Through(parent, view)`, MaxThroughDepth=1.
`Check(a)` and `FilterAuthorized([a])` return ErrEvaluationLimit.
`FilterAuthorized([a,b])` returns both a and b, without a limit error. Adding the
ancestor as a candidate lets a two-hop grant travel through work seeded at depth
zero. This violates the documented same-limits equivalence and means candidate
batch size/content changes limit behavior. It is a budget/parity defect; the
unbounded model itself would grant both nodes, so the probe does not establish
access outside that model's grants.

Evidence: `/tmp/gopernicus-authorization-depth-probe_test.go` and `.log`, run from
the authorization module with `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache
go test /tmp/gopernicus-authorization-depth-probe_test.go -v -count=1`, passed by
asserting current faulty behavior. No repository test/source changed.

Include this in AZ-C0/C1. Define per-root depth and shared-state budget separately;
do not repair it by disabling candidate/ancestor batches or silently dropping
granted candidates. Test permutations, singleton/joint batches, longer chains,
cycles with an alternate grant, exact depth boundaries and FilterPage pulls.

### Recommended decisions for the implementation review

- Prefer one clearly documented **read-enforced** permission model: narrowing
  allowed subjects should stop those tuples contributing authority. This is a
  proposal, not an approved behavior change. It requires model-aware userset
  evaluation, consistent lookup/check behavior and a safe data/model rollout;
  a Through-only patch is incomplete. If the owner instead chooses write-only
  constraints, make that equally explicit and require tuple cleanup before
  relying on narrower policy. AZ-C0 resolves this before ports change.
- Retain root-relative depth limits. Share completed work only when its depth
  and cycle context make reuse valid; compare all public decision surfaces with
  one fixture matrix. Do not copy the current set evaluator into Check until
  C10 and short-circuit/error semantics are settled.
- Keep baseline and guarded mutations as different capabilities. Expand the
  concurrency warning to every dependency of a guard, then assess revision-aware
  baseline writes separately. Existing dependency risks are not solved by merely
  moving the warning or changing logger wiring.
- Schedule compile/boot, guardian ownership and memory parity fixes as small
  independent changes after approval. Follow core semantics with listing API
  clarity/examples, measured work improvements, then the optional SQL proof.

## Keep / simplify / complete / defer disposition

| Area | Disposition | Reason |
|---|---|---|
| Host-owned immutable models, root vocabulary and split principal/userset refs | Keep | Clear domain ownership and actual host use; no external dependency in core. |
| Separate ReBAC and role models with one owner per permission pair | Keep | Avoids implicit privilege union and supports real distinct styles. |
| Baseline writer and optional guarded mutation capability | Keep, clarify dependency ownership | Segovia uses both; forcing receipts onto topology is not a simplification for the host. |
| Repeated evaluators/read paths | Simplify carefully | Actual drift in work bounds, short-circuit and model filtering; unify semantics/tests before implementation. |
| Mutation receipt/guardian/decision-view machinery | Keep, fix concrete parity defects | It buys real replay/invariant/concurrency behavior, with live tests. |
| Model validators | Simplify/complete | One reference validation vocabulary, safe pair identities, deterministic errors; remove stale partial-validator surface internally when tests migrate. |
| Constructor/logger/transport lifecycle | Simplify/complete | Headless usage should not need Register to receive its logger; reject invalid wiring at boot. |
| Role-owned CheckPermission in guards | Complete later if approved | Existing refusal safe, but forces host policy duplication. |
| SDK authorization/list API promotion | Defer | Requires host ergonomics evidence; check and list policy is still authorization-domain-specific. |
| Persistent caching, wildcard/admin bypass, new policy DSL/hierarchy | Defer | No concrete missing behavior justifies invalidation/semantic complexity now. |
| Multi-scope mutations, cross-store transaction promise, durable audit outbox | Defer | Honest documented limits, materially larger design than identified fixes. |
| Storage/module collapse or Firestore SQL imitation | Reject | Would erase dependency boundaries and actual datastore semantics. |

## Dependent implementation task packets (proposed, NOT authorized or implemented)

1. **AZ-C0: pin evaluator and schema-change contract.** Files: core tests under `internal/logic/authorizersvc`, shared `storetest`, README; include C1/C2/C3 and root's FilterAuthorized/depth findings. Produce test matrix before selecting evaluator changes. Must decide write-only vs read-enforced AllowedSubjects and root-relative budget/short-circuit semantics. No migration yet.
2. **AZ-C1: evaluator work and parity repair, depends on C0.** Files: `service.go`, `budget.go`, `evaluate_set.go`, `batch_reader.go`, `explain.go`, relevant decision-view adapters and conformance helpers. Fix graph work reuse/bounding and C3 unresolved-ID batching. Verify Check/Explain/CheckBatch/FilterAuthorized/guarded CheckPermission agreement, cycles/diamonds, per-root depths, canceled reads, error branches, duplicates and large convergent graphs. Add measured port/step bounds, not only elapsed-time tests. Keep adapter-port changes conditional on C0.
3. **AZ-C2: compile/boot hardening, independent after behavioral tests.** Files: `compiler.go`, `schema_validator.go`, `role_model.go`, `authorization.go`, gate builders and tests. Shared symbol validation, struct cycle key, duplicate grantor decision, typed nil rejection, nil resolver validation. Build/test/vet core and construct current Segovia/auth-cms models via copied temporary probes. Reject no previously valid host punctuation. No schema change; AUDIT config acceptance changes.
4. **AZ-C3: memory mutation parity and immutable guardian policy, independent of evaluator redesign.** Files: `memstore/mutations.go`, all four adapter option/constructor files, `storetest/mutations.go` plus focused memory tests. Fix C4/C5/C6. Tests: retained policy mutation; replay false annotation; guard cancellation and blocked-lock cancellation consume no ID; shared live PG/Turso tests. Firestore constructor snapshot can be hermetic, but don't claim live mutation proof without fixture. No schema change.
5. **AZ-C4: constructor logger and concise capability docs.** Files: `authorization.go`, `mutation_service.go`, constructor/log tests, README and site docs. Config.Logger propagation to Service/SystemMutator; optional Register behavior; reconcile outdated “only write path,” “roles opaque with no model” and hidden admin comments. Document baseline dependency closure and show real Segovia example. No lifecycle goroutines, schema change or host-policy default.
6. **AZ-C5: baseline-write/guard interoperability design, depends on ownership decision with host review.** Inventory every host guard dependency that baseline writes can change, not only the mutation target relation. Options: explicitly accepted races/immutable topology contract; host transaction/design change; or revision-aware baseline writes without receipts. If latter chosen, all memory/PG/Turso/Firestore writes and ambient transaction tests change together. This is a separate authorization concurrency feature, not folded silently into C3.
7. **AZ-C6: role-permission decision view completion, optional after C0/C1.** Files: `mutation_view.go`, `decisionsvc/roles.go` or narrowly extracted role checker, ownership tests and store dependency tests. Share compiled RoleModel evaluation over HasRole, retain exact/global dependency tracking and explain error semantics. Existing explicit refusal remains until implementation is separately approved.

For each approved implementation slice, use repository formatter, core `go build ./...`, `go test ./...`, `go vet ./...`, focused race tests; run affected adapter modules and fresh-fixture integration suites when touching contracts/stores. The recorded `/tmp/gopernicus-authorization-audit-stores.json` endpoints are now
cleaned up; recreate isolated fixtures for future runs. Never point destructive
conformance helpers at an application database. Firestore requires its separately owned emulator/live fixtures and documented build tags. Parent should record agreed behavior/port/schema breaks in AUDIT at implementation time; this read-only audit does not itself authorize them.
