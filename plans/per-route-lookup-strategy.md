# per-route lookup strategy — forward/reverse reads and traversal direction

**Status:** PHASE 1 RATIFIED 2026-10-14 (owner, in-session, approving Phase 1, the Gate and
the Verify bar: "this is just 'does this person have a direct grant before we traverse, right?'
that seems reasonable", "i think this makes sense", "recs are good", "sure"). R1 through R7
take their recommended values: the short circuit is on by default for LookupAuto; there is no
raw forward/reverse switch; Phase 2 is route-level only and named LookupStrategy /
WithLookupStrategy; Phase 3 covers single checks only; automatic direction is out of scope;
and Phases 2 and 3 are gated on the post-Phase-1 measurement. Phases 2 and 3 remain sketches
and are NOT ratified. Building Phase 1 is authorized. **2026-10-14: Phase 1 RELEASED: PR #81 merged @ 826c5197, tagged pockets/authorization/v0.26.0 (proxy-verified); test-only store repin PR #82 open (no store tags). Next: the Gate.**

Rev 2 folds in four reviews from 2026-10-14: lead-backend-engineer, data-integration-reviewer,
product-manager and platform-sre. The review notes are at the end. The main changes are:

- Phase 1 ships alone and is measured.
- Phases 2 and 3 are gated behind that measurement. Phase 2 shrinks to one route-level option.
  Phase 3 waits on a demand check.
- The claim that strategies "never change answers" is restated precisely.

## Terms

A **tuple** is one fact with three parts: resource, relation, subject. An example is "space S,
viewer, user Alice". Every store indexes each tuple twice. Postgres and Turso use indexes, the
memory store uses maps, and the Redis cache uses hash fields.

- **Forward set:** keyed by resource and relation. An example is "everyone who is a viewer of
  space S". It answers "who can". Its size is the number of grantees on one resource.
- **Reverse set:** keyed by subject. An example is "everything Alice holds, anywhere". It
  answers "what can Alice". Its size is the principal's total grants.
- **Userset subject:** a subject that is itself a set, such as `group G#member` or
  `space P#viewer`. Following one is called **expansion**.
- **Subject-first expansion:** start at the principal and compute every userset they reach by
  reading reverse sets outward. Then test whether the target is among them.
- **Resource-first expansion:** start at the target resource's forward set and follow its
  userset subjects outward, reading forward sets, until the principal is found or the frontier
  is empty.
- **Navigational hop (`Through`):** "view on S if view on S's parent". It reads S's forward set
  for the `parent` relation, then evaluates the permission on each parent. It is already
  resource-first (`logic/decisions/evaluate.go:287`).

The question "can Alice view S" can be answered either way. The cost is the size of the sets
read. When a principal has thousands of grants and each resource has a handful, forward reads
are cheap and reverse reads are expensive. When a resource has ten thousand grantees and a
principal has three grants, the opposite holds.

## Context — what a route gets today

Routes already declare their own policy through
`authorizationhttp.Require(predicate, opts...)`
(`pockets/authorization/inbound/http/composable_middleware.go:27`). Each predicate constructor
silently fixes a read strategy:

| Predicate | Lowers to | Reads | Cost driver |
|---|---|---|---|
| `HasRole(label, target)` | `decisions.RoleIn` | `facts.Contains` on the forward set (`expression.go:149`) | grantees on the resource |
| `Can(perm, target)` with a `Through` rule | `checkThrough` (`evaluate.go:287`) | forward `GetRelationTargets`, then a recursion per parent | parent chain length |
| `HasRelationship(label, target)`, or `Can` reaching a `Direct` | `CheckRelationWithGroupExpansion` (`expression.go:174`) | **the subject-first closure, every time** | principal's total grants |
| Batch / `FilterAuthorized` | `checkBatch` → traversal, sequential or optimized (`evaluate.go:180-193`); optimized uses `CheckBatchDirect` (`evaluate.go:405`) | the subject-first closure once, then a match against all IDs | principal's total grants |

Subject-first works the same way in every store:

- Postgres: a recursive CTE seeded from the subject (`stores/pgx/relationships.go:33`).
- Memory: `expandReachable`.
- Turso: its own CTE.
- The cache reader (`logic/tuplecache/reader.go` `reachable`) and the decisions tuple graph
  reader (`logic/decisions/tuple_graph_reader.go`) read reverse sets.

So a `Direct` check on a principal with thousands of grants reads all of them, even when the
principal is directly in the resource's three-person viewer set. That is the gps-360-go and
Segovia shape.

## Problem

Remove the subject-first cost where the forward set already decides. Then, if measurement shows
a need, let a route choose its expansion direction. Wherever both strategies decide, they must
agree. They may differ only in whether a check exhausts its budget.

## Design

### Phase 1 — direct-hit short circuit (no new API) — **for ratification**

In `evaluateExpression`, add these steps before calling `CheckRelationWithGroupExpansion` for a
`Direct` relation:

1. If `readModel.Allows(rt, rel, principal.Type, "")`, call `s.facts.Contains(ctx,
   Tuple{Scope: On(rt, id), Relation: rel, Subject: {principal.Type, principal.ID, ""}})`. A
   hit grants.
2. If the compiled model admits **no** userset subject for `(rt, rel)`, a miss denies. Record
   this per relation at compile time as `relationHasUsersets`.
3. Otherwise, fall through to the existing expansion unchanged.

**Why it is exact** (verified by lead-backend-engineer):

- The concrete seed `(st, sid, "")` is the only reached state with an empty relation.
- The final-edge filter is the same `Allows(rt, rel, st, "")` in memory (`memory.go:174`), the
  fallback reader (`tuple_graph_reader.go:76`) and the cache reader (`reader.go:248`).
- `compiler.go:277` rejects relations that have no allowed subjects.
- `s.facts` and `b.reader` come from one snapshot reader in `bound()`
  (`constructor.go:109-113`). That holds for both `ReadTupleSnapshot` and `tupleCache.Run`
  (`check_operation.go:26,38-44`). In pgx, `ForModel` shares `readQuerier`
  (`read_snapshot.go:28-31`).
- `rememberScopedDenial` and the diagnostic hooks run only on `RoleName` paths, so the short
  circuit skips nothing they rely on.

**Budget:** charge **no** extra step. A step is already charged when the node is entered
(`expression.go:104`, `budget.go:134`). An extra charge would tip decisions that sit just under
`MaxEvaluationSteps` into a 503.

**Batch:** `checkBatchTraversal` and `checkBatchSequential` route through
`evaluateExpression`, so they get Phase 1 automatically. Only `checkBatchOptimized`
(`evaluate.go:405`) needs an edit: one `ContainsMany` across all IDs, then expand the misses,
and only when the relation admits usersets. Test all three paths.

**Explain:** keep `ExplainKindDirect` and the `direct:<rel>` reason. Mark the short circuit in
the step's outcome. Recording `exact` would make the trace look like a role check.

**Answers can change at the budget edge, and the change is an improvement.** Today a principal
who reaches more than `MaxGraphStates` states gets `ErrExpansionBudgetExceeded`, a 503, even
when they hold a direct grant. Phase 1 turns that into a grant on a hit. For a relation without
usersets, it also turns it into a deny on a miss. Release notes must say so.

**Costs that Phase 1 adds:**

- **A miss on a relation that admits usersets pays one extra read**: `Contains`, then the full
  expansion. In `checkBatchTraversal` it also adds a scheduling round per check
  (`batch_evaluate.go:79,113`).
- **With the tuple cache on, `Contains` decodes the resource's whole forward set**
  (`tuplecache/reader.go:90-109`). Postgres uses an indexed `EXISTS` and is unaffected. A
  resource with 10,000 grantees gets slower under the cache. This sits beside #76 D7's fit
  guidance.
- **Open question for implementation:** `Tuple.Validate` inside `Contains` could reject an ID
  that the expansion path would quietly deny today. That would turn a 403 into a 500. Check it,
  and match the expansion path's behaviour.

### Gate — measure before Phases 2 and 3

After Phase 1 is released:

1. Run the Phase 1 benchmark (see Verify) on pgx and on the memory cache.
2. Inventory both host models (gps-360-go, Segovia). For each, list every relation that admits
   usersets. Record whether nesting uses `Through` (already resource-first) or userset subjects
   such as `space P#viewer`.
3. List any routes that are still slow, and why.

Phases 2 and 3 proceed only on the owner's word after the gate. The owner's foresight counts as
demand, but the gate writes down the expected shape first.

### Phase 2 — one route-level option (sketch, gated)

```go
// logic/decisions
type LookupStrategy string
const (
	LookupAuto          LookupStrategy = ""              // Phase 1 short circuit, then subject-first
	LookupSubjectFirst  LookupStrategy = "subject-first" // today's closure, no short circuit
	LookupResourceFirst LookupStrategy = "resource-first" // Phase 3
)
```

- **Route-level only:** `Require(p, authorizationhttp.WithLookupStrategy(x))`. Per-predicate
  `.Expand()` and a model-level `RelationDef` setting are cut. Direction applies to a whole
  traversal, not to single edges, so a per-relation setting would need a bidirectional search
  that nobody has designed. Revisit only when a route needs mixed directions inside one
  predicate.
- **Carrier:** the strategy travels as **evaluation state**, on the budget or the request, not
  as an `Expression` field. `Can` lowers to `NamedPermission`, and `checkPermission` evaluates
  the compiled model's expression (`evaluate.go:270`), not the route's tree, so a route
  default cannot be stamped onto those nodes.
- **Non-route APIs:** `Check`, `CheckBatch`, `FilterAuthorized` and `EvaluateResolved` gain a
  struct-input or option form that carries the same field. The default is `LookupAuto`.
  Optional parameters are never positional.
- **Example:** one route in `examples/auth-cms` uses `WithLookupStrategy`, so the option is
  exercised for real.

### Phase 3 — resource-first expansion (sketch, gated on the demand check)

Implement it once in `logic/decisions` over `tuples.Reader.ReadSets`, which every snapshot
reader provides, including the cache reader. It needs no new store port and no SQL.

```
frontier = { (rt, id, rel) }; seen = frontier
loop:
  sets = ReadSets(forward keys for frontier)
  for each tuple, skipping off-model tuples (same Allows filter as subject-first):
    subject == principal (concrete)                 -> grant
    subject is userset (T, tid, trel), not in seen  -> next frontier
  frontier = next; empty -> deny
```

- **Budget:** bound by total states plus tuples scanned under `MaxGraphStates`. Do not reuse
  `MaxRelationTargets`, which is documented as navigational fan-out (`limits.go:23-24`);
  reusing it would make any group over 1,000 members a 503. Map `ReadSets` `ErrReadLimit` to
  `ErrEvaluationLimit`. Overflow is indeterminate, never a deny.
- **Read limit semantics:** `ReadSets` bounds the total across all keys in one call
  (`tuples/store.go:128-130`). Choose between one `ReadSets` per set and a budget defined as a
  per-level total, and test whichever is chosen.
- **Batch:** single checks only. Batch and `FilterAuthorized` stay subject-first unless a
  measured need appears.

### Equivalence proof (acceptance gate for every phase)

Add a property test to `pockets/authorization/stores/storetest`, next to `oracle.go`. It uses
random models with and without usersets, and random tuples that include cycles and off-model
rows. It evaluates each check under every available strategy and asserts:

- **Where every strategy finishes, `Allowed` is identical.** Overflow outcomes are not compared
  across strategies, because their cost drivers differ.
- **Each strategy's overflow is an error, never a deny.**
- **`LookupAuto` never fails a budget that `LookupSubjectFirst` passes.**

The suite runs against memory, pgx and turso. It adds one run with the memory tuple cache in
front. That run asserts `Stats().Fallbacks == 0` and `Hits > 0`, and polls after every
mutation, so a silent durable fallback cannot pass it. An optional goredis run executes when
`redis-server` is present.

## Rulings

- **R1 — Phase 1 default:** on for `LookupAuto`, the zero value, so every existing route
  benefits (recommended). Before ratifying, look at the benchmark row for "cache on, resource
  with 10,000 grantees". If that regression matters to you, the alternative is on by default
  for durable reads only.
- **R2 — knob shape:** expansion direction only, with no raw forward/reverse switch
  (recommended). The read side follows from the direction.
- **R3 — Phase 2 surface:** route-level only (recommended). Node and model levels are cut.
- **R4 — naming:** `LookupStrategy` / `WithLookupStrategy`, the owner's own phrase
  (recommended).
- **R5 — Phase 3 batch:** single checks only (recommended).
- **R6 — automatic direction from set sizes:** out of scope. Record it as an issue.
- **R7 — gating:** Phases 2 and 3 wait for the post-Phase-1 measurement and the host inventory
  (recommended).

## Out of scope

- The native-set Redis backend. Record it as an issue with the #76 benchmark table.
- Enumeration (`LookupResources`, `FilterPage`). It answers "what can Alice", which is
  inherently a reverse-set question.
- Host changes. The Redis cache is being turned off in both hosts.

## Docs

`pockets/authorization/README.md` gains a section titled "Choosing a lookup strategy". It
covers:

- the forward and reverse terms;
- the rule of thumb, principal-heavy versus resource-heavy;
- how to read the explain trace for a short-circuit hit;
- how budgets behave for each strategy;
- the tuple-cache caveat from Phase 1's cost list.

The Phase 1 release note states the budget-edge answer change and the explain-outcome change.

## Release

| Phase | Module | Bump |
|---|---|---|
| 1 | `pockets/authorization` | MINOR (answer change at the budget edge, explain outcome) |
| 2 | `pockets/authorization` | MINOR (new option and API fields) |
| 3 | `pockets/authorization` | MINOR |

Phase 1 can ship in the #76 train (v0.25.0) or on its own. Store modules need no tag unless the
storetest changes force a repin.

## Verify

```sh
cd pockets/authorization && go build ./... && go vet ./... && go test ./...
cd pockets/authorization/stores/pgx && go test ./...     # storetest equivalence on Postgres
cd pockets/authorization/stores/turso && go test ./...   # storetest equivalence on Turso
make guards
```

Before Phase 1 is reported done, add a benchmark to `stores/pgx`, plus a memory-cache variant,
and report p50 before and after for each case:

| Case | Expected |
|---|---|
| Hit: principal with 5,000 direct grants, resource with 3 viewers | large improvement |
| Miss on a relation that admits usersets | no meaningful regression |
| Grant reachable only through a userset | no meaningful regression |
| Cache on, resource with 10,000 grantees, principal with 3 grants | regression measured and reported (R1 input) |

Confirm in the explain trace that a hit records the short-circuit outcome with no expansion.

## Review notes (2026-10-14)

- **lead-backend-engineer:** ship Phase 1 with edits; rework Phases 2 and 3. Phase 1 logic is
  exact and snapshot-coherent. Corrections folded in: budget-edge answers, no extra step
  charge, tuple-cache `Contains` cost, the three batch paths, the evaluation-state carrier, the
  model-level setting cut, the `MaxRelationTargets` misuse, the explain kind, and the
  `Validate` question.
- **data-integration-reviewer:** an equivalence claim must not compare overflow; the cache run
  must assert no fallbacks; `ReadSets` limits are per call; note the cache `Contains` cost.
- **product-manager:** ship Phase 1 alone and measure; gate Phases 2 and 3; route-level only;
  add a demand check for Phase 3; benchmark the miss and userset cases; add adopter docs and
  one example route.

## Build notes (Phase 1, 2026-10-14)

- **Explain-outcome shape (owner ruling, in-session, option 1):** a direct-hit short circuit
  records `ExplainStep{Kind: ExplainKindDirect, Outcome: ReasonDirectHit}`, with the new
  constant `ReasonDirectHit = "direct_hit"`. The new value appears only in
  `ExplainStep.Outcome`. A miss on a relation without usersets records plain `denied`. A miss on
  a relation that admits usersets falls through to expansion and records its `granted`/`denied`
  as before. `CheckResult.ReasonCode` and `Explanation.Decision` keep `granted`/`denied`, and
  `Reason` keeps `direct:<rel>`.
- **Release:** v0.25.0 is tagged (#76), so Phase 1 ships alone as `pockets/authorization`
  v0.26.0.
- **Batch exact-read dedup (owner-approved add to #81):** `resolveBatchReads` reads each distinct
  exact fact once per traversal round (c08e9460). At n=600, FilterAuthorized on SQLite drops from
  21 to 16 statements (subject-first is 9).
- **pgx 7-column index: DROPPED (owner ruling).** Measured on 1M tuples over 10k resources, the
  index grows from 12 MB to 120 MB (B-tree deduplication is lost), about +24% table storage, and
  it only helps freshly bulk-loaded, un-vacuumed tables. The owner does not expect bulk loads
  except a maintenance-window migration. The docs say to `VACUUM ANALYZE iam_tuples` after a
  bulk load. The parity guard (`TestMigrationParity`) would also have blocked a pgx-only 0005.
