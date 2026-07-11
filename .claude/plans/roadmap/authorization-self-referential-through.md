# Authorization: relax circular-through validation for self-referential hierarchies

Status: **EXECUTED 2026-07-11** (ratified same day, jrazmi) — D1 = (b) permit + document/pin, (c) named follow-up; D2 = reject unsatisfiable self-only rules (predicate refined during task-1: a check is unsatisfiable-self-only only when its Permission equals the rule's own permission — the literal spec would have falsely rejected satisfiable cross-permission rules like `Through("parent","admin")`). Tasks 1–4 all green: validator relaxed, e2e hierarchy + D1(b) boundary pin, docs, `make check` + `make guard` pass.
Date: 2026-07-11
Base: HEAD `a44584b`
Module touched: `features/authorization` only (single module; no store, schema, or go.mod changes).

## Context

`detectCircularThrough`/`findCycle` in
`features/authorization/internal/logic/authorizersvc/schema_validator.go`
keys its visited set by `targetType.permission` only — schema-level, with no
notion of instance or relation progress. The canonical hierarchy rule
`space.view = AnyOf(Direct("viewer"), Through("parent","view"))` (relation
`parent` → subject type `space`) is therefore rejected at `NewService` as
`circular through-relation detected: space.view -> space.view`, even though
both runtime evaluators already terminate on exactly this shape:

- **Check** (`service.go` `checkPermission`) has a depth bound
  (`Config.MaxTraversalDepth`, default 10) plus an instance-level visited key
  (`type:id#perm`) — real parent chains walk fine, data cycles deny with
  "cycle detected".
- **Lookup** (`lookup.go` `lookupThrough`) has an explicit
  `ref.Type == resourceType` branch: `lookupDirectOnly` finds roots via direct
  grants, then `store.LookupDescendantResourceIDs` expands descendants
  (recursive CTE in pgx/turso, BFS fixpoint in memstore — all cycle-safe,
  parity pinned by the ONE `storetest` suite's `Lookups` sub-runner).

The validator is the only thing blocking schema-expressed hierarchies. This
plan relaxes it narrowly, keeps every genuinely non-terminating shape
rejected, and pins the one Check/Lookup divergence the relaxation exposes.

## Goal

`NewService` accepts the self-referential hierarchy schema shape while still
rejecting mutual cross-type and cross-permission through-cycles, with the
LookupResources completeness boundary documented and pinned by tests.

## Definition of Done

- `space.view = AnyOf(Direct("viewer"), Through("parent","view"))` passes
  `ValidateSchema`; `NewService` builds.
- Mutual cross-type cycles (`a.x -> b.x -> a.x`) and cross-permission
  self-type chains (`space.view -> space.admin -> space.view`) are still
  rejected, proven by table-driven tests.
- An end-to-end test through the public `features/authorization` API
  (memstore) proves Check AND LookupResources both resolve a 3-level parent
  chain, and a second case pins the documented Lookup boundary (D1).
- Doc comments state the LookupResources completeness boundary and the
  `MaxTraversalDepth` sizing consequence.
- `make check` and `make guard` green at the end.

## Decisions

| # | Decision | Options | Recommendation | Status |
|---|----------|---------|----------------|--------|
| D1 | Check/Lookup divergence posture. `lookupDirectOnly` seeds the self-referential descendant walk from DIRECT grants of the same permission only; roots granted via a non-self Through (e.g. `Through("org","view")`) contribute zero descendants, so Check can allow what LookupResources misses. | **(a)** validator permits self-referential Through only when the same AnyOf contains a Direct sibling. **(b)** permit unconditionally; document + test-pin the Lookup boundary. **(c)** engine change: seed the descendant walk from non-self-derived roots too (full consistency). | **(b)** for this plan. Consultation produced a counterexample killing (a): a syntactically-present but empty Direct sibling still ships the inconsistency (org-granted root, no direct viewer grants → Check allows the grandchild, Lookup misses it), and (a) wrongly rejects legitimate through-only schemas — the gate is neither necessary nor sufficient. (c) is the only true fix and is engine work, named as a follow-up (it matters if Segovia's D8 list prefiltering must see org-granted subtrees). | **NEEDS RATIFICATION (jrazmi)** — note this amends the pre-consultation recommendation of (a) |
| D2 | Unsatisfiable rules. The pure relaxation would admit `space.view = AnyOf(Through("parent","view"))` alone — terminating but can NEVER evaluate true (no grant bottoms out anywhere). | reject with a distinct "unsatisfiable" error / allow silently | **Reject**: a permission whose every AnyOf check is a Through targeting only the self type is a schema bug; error message names it "unsatisfiable", distinct from "circular". | **RATIFY-WITH-DEFAULT** |
| D3 | Depth semantics. `checkThrough` silently denies past `MaxTraversalDepth` (default 10) with reason "max depth exceeded"; hosts collapsing hand-walked hierarchies into schema must size it deliberately, and each hop costs one `GetRelationTargets` round-trip. | — | Documentation only, no code: Config doc comments + README. The comment must NOT imply the bound applies to the store descendant walk — that is unbounded-but-cycle-safe per the 2026-07-08 ruling recorded at `service.go:14-16`. | SETTLED (doc task) |

## Out of scope

- **(c) from D1** — seeding `lookupThrough`'s descendant walk from
  Through-derived roots. If ratified later, it is its own engine task with
  its own tests; do not smuggle it in here.
- **Segovia work** (separate repo). Flag #8 in
  `segovia/.planning/gopernicus-v2/04-gopernicus-flags.md` — collapsing
  `checkSpace` + the checker's `spaces.Store` dependency into schema and
  unblocking D8 list prefiltering — is explicitly gated on this relaxation
  shipping. Name it in the closeout; plan nothing for it here.
- Store changes of any kind. `LookupDescendantResourceIDs` parity
  (memstore/turso/pgx) is already exercised by `storetest`'s `Lookups`
  sub-runner; nothing in this plan touches SQL or store Go.
- Threading `MaxTraversalDepth` into stores (re-litigated and closed
  2026-07-08; engine-only).

## Schema / datastore impact

None. No SQL, no migrations, no store adapters. The EAV-style
registered-data permission model means accepting new schema shapes is a
code-level validation change with zero migration.

## Module / API impact

Single module, `features/authorization`. No exported-symbol changes; no
go.mod/go.work changes. Behavior change in `ValidateSchema`: accepts more
schemas (the relaxation) and — under D2 — rejects one new shape
(unsatisfiable self-only rules). The new rejection is technically breaking
for a host carrying such a rule (none exist in this repo; `make check`
builds all example hosts and would surface one). Next
`features/authorization` tag is a **minor** bump per RELEASING.md.

## Generated-artifact impact

None. No `.templ` sources touched.

## Risks

1. **Mis-scoped sanction predicate.** `findCycle` does not currently carry
   the SOURCE permission — only `check.Permission` (target) and `path`. If
   the implementer approximates the sanction with locally-available fields,
   cross-permission self-type chains slip through. Mitigation: task-1
   specifies the `sourcePerm` parameter plumbing exactly, and the
   adversarial table cases pin all three traced shapes.
2. **False-green end-to-end test.** A 3-level chain whose root is granted
   directly passes even if the org-seeded-root divergence regresses —
   Lookup's self-branch only ever expands `lookupDirectOnly` roots.
   Mitigation: task-2 includes the org-seeded case as a deliberate
   assertion of the documented boundary, not an accidental gap.
3. **D2 rejection surprising a host.** Low: repo-internal hosts are built
   by `make check`; external hosts get a loud, named error at `NewService`,
   which is the feature's stated failure posture (fail loudly at wiring).

## Tasks

### task-1: relax findCycle and reject unsatisfiable self-only rules

- **depends_on:** []
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/internal/logic/authorizersvc/schema_validator.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/internal/logic/authorizersvc/schema_validator_test.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Add a `sourcePerm string` parameter to `findCycle`;
  `detectCircularThrough` passes `permName` at the top call and each
  recursion passes the outer frame's `check.Permission`. Inside the
  per-`targetType` loop, sanction the DIRECT self-loop —
  `targetType == resourceType && check.Permission == sourcePerm` — with a
  `continue` that neither writes `visited[key]` nor recurses (the validator
  itself must terminate); every other revisit of a `type.permission` key
  stays rejected with the existing "circular" error. Per D2, add a
  validation pass rejecting any permission rule whose every AnyOf check is a
  Through whose target types are ALL the self type, with a distinct
  "unsatisfiable" error. Extend the table-driven tests to cover, at
  minimum: (1) `space.view = AnyOf(Direct("viewer"), Through("parent","view"))`
  accepted; (2) mutual cross-type `a.x -> b.x -> a.x` still rejected;
  (3) cross-permission self-type `space.view -> space.admin -> space.view`
  rejected; (4) a real cycle hiding behind a sanctioned self-edge still
  caught; (5) self-only Through with no other check rejected as
  unsatisfiable (D2); (6) a relation whose `AllowedSubjects` mixes self-type
  and another type (e.g. `parent` allowing `space` and `org`) — self edge
  sanctioned, `org` edge validated normally; (7) two self-referential
  relations on one type (`parent` and `origin`) each individually
  sanctioned; (8) all four existing rejection tests unchanged.

### task-2: end-to-end hierarchy test through the public API (memstore)

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/hierarchy_test.go` (new, package `authorization_test` or root `authorization` test package matching `authorization_test.go`; imports `features/authorization/memstore` — same module, and guard-clean since G2 only blocks `stores|views`)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Through the public `features/authorization` API on
  memstore: (1) `NewService` accepts the hierarchy schema
  (`space.view = AnyOf(Direct("viewer"), Through("parent","view"))`);
  (2) build a 3-level chain (`root <- mid <- leaf` via `parent` tuples),
  grant the user `viewer` on `root` directly, assert `Check` allows on
  `leaf` AND `LookupResources` returns all three IDs; (3) the D1 boundary
  pin — grant access to the root only via a non-self Through (e.g.
  `Through("org","view")` with the user admin on the org, NO direct viewer
  grants), assert `Check` allows on the grandchild while `LookupResources`
  returns only the org-reachable root (descendants missing) — with a
  comment naming this the documented D1(b) boundary so a future (c) engine
  task flips the assertion deliberately.

### task-3: document the Lookup boundary and depth semantics

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/internal/logic/authorizersvc/lookup.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/internal/logic/authorizersvc/service.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/authorization.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization/README.md`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Doc comments only, no behavior. On
  `LookupResources`/`lookupThrough` (both the engine and the public wrapper
  at `authorization.go:220`): state the D1(b) boundary verbatim —
  "self-referential Through enumerates descendants of DIRECTLY-granted
  roots only; roots granted via a non-self Through are not expanded, though
  Check honors them." On `Config.MaxTraversalDepth` (engine `service.go`
  Config, public `authorization.go:130` Config, and the README config
  table row): hosts collapsing a hand-walked hierarchy into schema must
  size the depth deliberately — the engine silently DENIES past the bound
  with reason "max depth exceeded", and each Check hop costs one
  `GetRelationTargets` round-trip. Do NOT imply the bound applies to the
  store descendant walk (unbounded-but-cycle-safe, 2026-07-08 ruling).
  Add a short README note under the relationships kind describing the now-
  supported hierarchy pattern and the D1(b) Lookup boundary.

### task-4: repo-wide verification

- **depends_on:** [task-1, task-2, task-3]
- **model:** sonnet
- **files:** []
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make check && make guard`
- **description:** Run the full cross-module check (all modules build/test/
  vet, templ drift, all thirteen guards). Confirms no example host carried
  a schema newly rejected by D2 and that the single-module change stays
  boundary-clean.

## Sequencing

task-1 → {task-2, task-3 in either order} → task-4. task-2 and task-3 are
independent of each other but both depend on the final validator semantics
from task-1.

## Consultation notes

`lead-backend-engineer` consulted 2026-07-11 (single hop). Verdict:
ship-with-edits. Material findings, all incorporated:

- Verified independently that all three runtime termination guards hold, so
  the relaxation removes no protection the engine relies on; blast radius is
  `ValidateSchema`'s single production caller (`NewService`).
- **Killed the Direct-sibling gate (pre-consultation D1 recommendation (a))**
  with a concrete counterexample:
  `space.view = AnyOf(Direct("viewer"), Through("org","view"), Through("parent","view"))`,
  user is org-admin with no direct viewer grants → the Direct sibling exists
  syntactically but is empty; Check allows the grandchild, LookupResources
  returns only the org-reachable root. The gate is neither necessary nor
  sufficient; the real hole is `lookupDirectOnly`'s seed, fixable only in
  the engine (option (c)). Hence D1 recommends (b) with (c) as a named
  follow-up.
- Flagged the `sourcePerm` plumbing as the implementation landmine (the
  sanction predicate cannot be expressed with `findCycle`'s current
  parameters) and traced the three adversarial shapes with it — all resolve
  correctly. Task-1 specifies the exact signature change.
- Flagged the always-false self-only rule the pure relaxation would admit
  (became D2), the false-green risk in a directly-granted-only e2e test
  (became task-2 case 3 / risk 2), and the mixed-AllowedSubjects +
  multiple-self-relations cases (became task-1 cases 6–7).

## Open questions

- **D1 ratification (jrazmi):** (b) now with (c) as follow-up — or pull (c)
  into scope immediately if Segovia's D8 prefiltering needs org-granted
  subtrees enumerable? Choosing (a) is NOT recommended (see counterexample).
- **D2 default confirm:** reject unsatisfiable self-only rules with a named
  error.

## Recommended reviews

- `product-manager` — scope discipline; the D1(b)-now/(c)-later split is a
  deliberate shippable-value call.
- `lead-backend-engineer` — post-hoc pass on the final task list (the
  consult reviewed a sketch).

## Notes

Follow-ups named, not planned here: (1) D1 option (c) — engine task seeding
the self-referential descendant walk from Through-derived roots; (2) Segovia
flag #8 (`segovia/.planning/gopernicus-v2/04-gopernicus-flags.md`) unblocks
once this ships — do NOT move Segovia's flow-down into schema before the
validator relaxes.
