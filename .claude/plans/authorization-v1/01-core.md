# Phase Z1 — `features/authorization` core (engine, DSL, socket, memstore, storetest)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: opus
Depends on: — (first phase)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2 (all of
it — the ruling cashed, the anatomy, the port split, storage semantics),
§9 (crud re-typing), §13 Z1, §14 (checklist trace). Module 31 after
task-1.

Salvage source (reference-only; design ported, code re-typed fresh — the
sdk-parity bar; never copy import paths):
`gopernicus-original/core/auth/authorization/{authorizer,model,builder,schema_validator,membership,explain,cache_store,errors}.go`
+ `authorization_test.go` (the ~2,650-line behavioral reference).
The original's `Storer` is at `model.go:246` — **14 methods** (the
design's §2.5 list is abbreviated; salvage the full surface — overview
staleness finding 1). The original's Config is `{MaxTraversalDepth int}`
ONLY (`authorizer.go:16`); platform-admin is a DATA TUPLE, not a Config
field (review-gate fold, major 1).

## DoD

- Module `github.com/gopernicus/gopernicus/features/authorization`
  compiles standalone, `go.mod` requires exactly `sdk` (FS1), registered
  in `go.work` + Makefile `MODULES` **and in the FS1 guard's hardcoded
  list with a recorded prove-can-fail** (review-gate fold, steward minor
  4); `make check` green at **31 modules**.
- `logic/relationship` public rim: tuple entity, `CreateRelationship`,
  filters, listing row types, and the full 14-method `Storer` port —
  listing methods **crud-re-typed** (`sdk/crud.ListRequest`/`Page[T]`,
  design §9; the original's `fop` vocabulary does not survive).
- Model DSL + schema validator: `NewSchema`/`ResourceSchema`/
  `PermissionRule` (`AnyOf` unions, `Through` traversals); unknown
  relations, bad through-targets, and schema cycles rejected loudly at
  `NewService` time.
- The engine (`internal/logic/authorizersvc`): Check (incl. the
  `checkSelf` self-grant rule), through-traversal, cycle guards, the
  `MaxTraversalDepth` bound, CheckBatch, FilterAuthorized,
  LookupResources
  (`LookupResult{Unrestricted, IDs}` — non-nil IDs when restricted),
  relationship CRUD, RemoveMember with last-owner protection,
  ValidateRelation/ValidateRelationships/GetSchema/
  GetPermissionsForRelation, platform-admin bypass via the
  `platform:main#admin` DATA TUPLE (user AND service_account subjects —
  no Config field).
- FS2 socket (cut refinements 1/6): `Repositories`/`Config`/
  `NewService(repos, cfg) (*Service, error)` (loud validation) /
  `(*Service) Register(mount) error` (logger-only, no routes;
  `/authorization/*` claimed-unregistered); engine vocabulary aliased at
  root (`Subject`, `CheckRequest`, `CheckResult`, `LookupResult`,
  `Schema`, …).
- `memstore/` public in-core (R3 allowance) — Go graph-walk group
  expansion, mutex-backed, honest (unique-tuple enforcement, direct-only
  counts).
- `storetest/` two-layer suite (cut refinement 4) with the **five named
  adversarial sub-runners** green against memstore hermetically inside
  `make check`.
- Rule-6 greps empty both directions; `make guard` green (G7 auto-covers
  the new feature).

## Preconditions

- `make check` green on the current tree (30 modules, 7 guards).
- Read the design §2 in full, then the salvage files above — especially
  `model.go` (Storer + DSL + LookupResult doc contracts) and
  `membership.go` (last-owner semantics) — before typing anything.
- Read `features/jobs/jobs.go` (FS2 socket + routeless Register +
  public `memstore/` precedents) and
  `features/authentication/authentication.go` (alias-at-root precedent).

## Tasks

### task-1: module skeleton + `logic/relationship` rim + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/go.mod,
  features/authorization/logic/relationship/relationship.go,
  features/authorization/logic/relationship/relationship_test.go,
  go.work, Makefile]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make check` (31 modules; go.work ↔ MODULES agreement) and `make guard`; FS1 prove-can-fail (review-gate fold, steward minor 4): temporarily add a fake extra require to `features/authorization/go.mod`, observe `guard-feature-core-sdk-only` fail naming it, revert, `make guard` green again
- **description:** Create the module (go version + sibling `replace`
  per `features/jobs/go.mod`; requires sdk only). `logic/relationship`:
  the tuple entity (resource_type, resource_id, relation, subject_type,
  subject_id, subject_relation — the optional userset relation),
  `CreateRelationship`, `RelationTarget`, the listing row types
  (`SubjectRelationship`, `ResourceRelationship`) + filters, and the
  **full 14-method `Storer`** salvaged from the original's `model.go:246`
  — permission checks (`CheckRelationWithGroupExpansion`,
  `GetRelationTargets`, `CheckRelationExists`, `CheckBatchDirect`), CRUD
  (`CreateRelationships`, `DeleteRelationship`,
  `DeleteResourceRelationships`, `DeleteByResourceAndSubject`),
  `CountByResourceAndRelation` (doc comment carries the §2.5 pin
  verbatim in intent: **direct tuples only, never expanded membership —
  a count divergence is a security divergence**), the two crud-re-typed
  listing methods (`sdk/crud.ListRequest` in, `crud.Page[T]` out —
  design §9), and the three LookupResources primitives
  (`LookupResourceIDs`, `LookupResourceIDsByRelationTarget`,
  `LookupDescendantResourceIDs` — doc: recursive transitive walk,
  cycle-safe). Port doc comments are the spec storetest executes
  (duplicate-tuple semantics pinned against the original's SQL — log
  what the original does: idempotent insert vs conflict — and state it
  on `CreateRelationships`). Rim test: compile-check stub pinning the
  signatures. Register in `go.work` + Makefile `MODULES` (alphabetical:
  after `features/authentication/stores/turso`, before `features/cms`);
  bump the Makefile header count 30 → 31; **add `features/authorization`
  to the FS1 guard's hardcoded list** (`guard-feature-core-sdk-only`,
  Makefile:116) with the prove-can-fail in this task's verify —
  review-gate fold, steward minor 4 (supersedes the events-precedent
  defer-to-docs-phase staging: the store phases must not be a
  machine-unchecked window for the core).

### task-2: model DSL + schema validator

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/internal/logic/authorizersvc/model.go,
  features/authorization/internal/logic/authorizersvc/builder.go,
  features/authorization/internal/logic/authorizersvc/schema_validator.go,
  features/authorization/internal/logic/authorizersvc/model_test.go,
  features/authorization/internal/logic/authorizersvc/schema_validator_test.go]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Salvage the registered-data model DSL (design §2.3):
  `Schema`, `NewSchema(...)`, `ResourceSchema` (resource types, relations,
  permission rules), `PermissionRule` with `AnyOf` unions of direct
  relations and `Through` traversals, `Subject{Type, ID, Relation}`.
  The schema validator rejects unknown relations, bad through-targets,
  and cycles — loud, enumerated errors (salvage the original's error
  vocabulary from `errors.go`, re-typed). Adding a resource type is a
  code change with zero migration — say so in the package doc (the
  EAV-spine philosophy applied to permissions). Tests re-typed from the
  original's model/validator coverage: valid schema round-trip, each
  rejection class, the builder helpers. Keep or drop the original's
  schema-merge `Remove()` affordance faithfully — log the call either
  way.

### task-3: the engine

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/authorization/internal/logic/authorizersvc/service.go,
  features/authorization/internal/logic/authorizersvc/membership.go,
  features/authorization/internal/logic/authorizersvc/lookup.go,
  features/authorization/internal/logic/authorizersvc/service_test.go,
  features/authorization/internal/logic/authorizersvc/membership_test.go,
  features/authorization/internal/logic/authorizersvc/lookup_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make guard`
- **description:** Salvage the evaluation engine against the rim's
  `Storer`: `Check` (direct relation → group expansion → `Through`
  traversal, with cycle guards on traversal and the
  **`MaxTraversalDepth` bound** — Config's only field, default 10,
  `<= 0` ⇒ 10; the SHARED bound memstore and both SQL CTEs must honor
  identically, review-gate fold lead refinement 8), **`checkSelf`
  explicitly in scope** (authorizer.go:~250, lead refinement 9:
  self-grant with reason "self" when subject == resource for `user`/
  `service_account` resource types and permission ∈ {read, update,
  delete} — nothing else), `CheckBatch`,
  `FilterAuthorized`, `LookupResources` returning
  `LookupResult{Unrestricted, IDs}` with the original's contract
  verbatim in intent (**explicit bool, fail-closed zero value; IDs
  always non-nil when restricted; Unrestricted ⇒ caller skips ID
  filtering entirely**), relationship CRUD delegation, `RemoveMember`
  with last-owner protection over `CountByResourceAndRelation` (direct
  counts — the §2.5 pin), the Validate*/GetSchema/
  GetPermissionsForRelation surface, and the **platform-admin bypass —
  a DATA TUPLE, not a Config field** (review-gate fold, major 1; a
  config-level bypass would amend ratified §2.5 and is not this plan's
  to decide): `checkPlatformAdmin(ctx, subj)` =
  `store.CheckRelationExists(ctx, "platform", "main", "admin",
  subj.Type, subj.ID)` (authorizer.go:244) — short-circuits Check/
  CheckBatch/FilterAuthorized and yields `Unrestricted` from
  LookupResources; both user and service_account subjects, per the
  original's tests; a host provisions it by declaring a `platform`
  resource type in its schema and creating the tuple.
  `explain.go`/`cache_store.go` are
  salvage-if-free — build them only if they fall out cleanly; log
  build-or-skip (never acceptance criteria). Tests: re-type the
  behavioral core of the original's 2,650-line suite for every method
  above against an in-package fake store (the memstore arrives in
  task-5; the adversarial cases are storetest's in task-6 — do not
  duplicate them here beyond what unit-level coverage needs), race-run.

### task-4: the FS2 socket + root aliases

- **depends_on:** [task-3]
- **model:** opus
- **files:** [features/authorization/authorization.go,
  features/authorization/authorization_test.go]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment: `! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/`
- **description:** The host-facing surface (cut refinements 1/6/7, as
  corrected at the review-gate fold):
  `Repositories{Relationships relationship.Storer}` (required —
  exported `ErrRelationshipsRequired`), `Config{Model Schema,
  MaxTraversalDepth int}` (`ErrModelRequired`; schema validated at
  construction — invalid model = the validator's loud error;
  `MaxTraversalDepth <= 0` ⇒ default 10, never an error; **no
  `PlatformAdmin` field** — platform-admin is the data tuple, task-3),
  `NewService(repos, cfg) (*Service, error)`, `(*Service)
  Register(m feature.Mount) error` — **registers no routes** (jobs
  precedent), logs one line via `m.Logger` when non-nil; the
  `/authorization/*` namespace is claimed for a future admin surface
  (package doc says so). `Service` promotes the full §2.3 method set by
  thin delegation: Check, CheckBatch, FilterAuthorized, LookupResources,
  CreateRelationships, DeleteRelationship, DeleteResourceRelationships,
  DeleteByResourceAndSubject, RemoveMember, ValidateRelation,
  ValidateRelationships, GetSchema, GetPermissionsForRelation,
  ListRelationshipsBySubject, ListRelationshipsByResource. Root aliases
  (the `auth.Granter` precedent): `Subject`, **`Resource`** (review-gate
  fold, lead refinement 7 — Z4 constructs `authorization.Resource{…}`;
  it won't compile otherwise), `CheckRequest`,
  `CheckResult`, `LookupResult`, `Schema`, `NewSchema`,
  `ResourceSchema`, `PermissionRule` + builders — hosts write
  `authorization.CheckRequest{Subject: authorization.Subject{…}}`
  exactly as design §2.2's snippet shows; **verify that CheckBatch/
  FilterAuthorized argument types need no further root aliases** (lead
  refinement 7) and add any that do. Package doc opens with the
  three-posture posture note (one paragraph; the full table is the
  README's, Z5) and the AV2 split: consumer seams are Check-only;
  everything on `Service` beyond Check is flagship-specific API, never a
  seam. Tests: construction validation (nil repos / nil model / invalid
  model), promoted-method delegation smoke, Register-with-logger,
  zero-value `feature.Mount` tolerance.

### task-5: `memstore/` — the public in-core reference

- **depends_on:** [task-4]
- **model:** opus
- **files:** [features/authorization/memstore/memstore.go,
  features/authorization/memstore/memstore_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make guard`
- **description:** Public in-core `relationship.Storer` implementation
  (the R3 allowance: substantial — group expansion re-implemented as a
  Go graph walk — and host-needed: Z4's zero-infra proof runs on it;
  `features/jobs/memstore` is the placement precedent; never a
  `stores/memory` module). Mutex-backed; unique-tuple enforcement honest
  (duplicate semantics exactly as task-1 pinned); graph-walk group
  expansion with a visited-set cycle guard (the memstore must survive
  A∈B, B∈A data — the suite will prove it) **honoring the same traversal
  bound the engine's `MaxTraversalDepth` implies and the SQL CTEs will
  carry** (review-gate fold, lead refinement 8 — a bound skew is a
  per-backend security divergence; the executor pins how the original
  threads the bound between engine and store and mirrors it, logging the
  mechanism);
  `CountByResourceAndRelation` counts direct tuples only;
  `LookupDescendantResourceIDs` as a transitive walk; keyset-shaped
  listing honoring `crud.ListRequest` with a stable tiebreak matching
  what the SQL stores will do (pin the cursor/order fields now — Z2
  implements the same contract). memstore_test runs the task-6 suite
  hermetically once it exists (wire the call in task-6; this task's
  tests cover memstore-specific mechanics).

### task-6: `storetest/` — the two-layer conformance suite with the NAMED adversarial sub-runners

- **depends_on:** [task-5]
- **model:** opus
- **files:** [features/authorization/storetest/storetest.go,
  features/authorization/storetest/adversarial.go,
  features/authorization/memstore/conformance_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make check` (the suite runs hermetically via memstore on every future `make check`) and `make guard`
- **description:** `storetest.Run(t, newStore func(t *testing.T)
  relationship.Storer)` — two layers (cut refinement 4). **Layer (a),
  port contract against the Storer directly:** tuple CRUD round-trip +
  duplicate semantics; the three delete variants; `CheckRelationExists`;
  `GetRelationTargets`; `CheckBatchDirect` map semantics;
  `CountByResourceAndRelation` direct-only; the three Lookup*
  primitives; listing pagination (keyset cursor round-trip + stable
  tiebreak + empty-page shape — pin the empty-page case here, closing
  the D5-era gap for this feature from day one). **Layer (b),
  engine-over-store:** construct `authorization.NewService` with a
  fixture schema over the store under test and assert authorization
  OUTCOMES — this is what proves the memstore and the recursive-CTE
  stores authorize identically (design §2.3). The **named adversarial
  sub-runners** (design §13 Z1, verbatim — these names appear literally
  in `t.Run` and in the per-dialect live artifacts):
  - `Adversarial/MembershipCycle` — A∈B, B∈A: expansion terminates and
    answers correctly (both allowed-through-cycle and
    denied-outside-cycle assertions).
  - `Adversarial/DeepNesting` — ≥3-level group nesting resolves
    (user→G3→G2→G1→resource), **plus the depth-boundary pair
    (review-gate fold, lead refinement 8): a membership chain exactly at
    the traversal bound resolves; a chain at bound+1 does not** — the
    ≥3-level case alone cannot detect a bound skew, and a bound skew is
    a per-backend security divergence.
  - `Adversarial/DiamondDedup` — diamond/multi-path membership
    deduplicates, **with an explicit `CountByResourceAndRelation`
    assertion**: multiple expansion paths never inflate the direct count
    (§2.5 — a count divergence is a security divergence; last-owner
    protection depends on it).
  - `Adversarial/NestedUserset` — `group#member@group#member`-style
    subjects (`Subject.Relation` set) resolve through the userset.
  - `Adversarial/Unrestricted` — `LookupResult.Unrestricted` wildcard
    semantics: the fixture **declares a `platform` resource type in the
    schema and seeds the `platform:main#admin@<type>:<id>` tuple**
    (review-gate fold, major 1 — which also exercises
    `CheckRelationExists` through the engine); admin subject ⇒
    `Unrestricted=true` and the caller-skips-filtering contract;
    non-admin ⇒ `Unrestricted=false` with non-nil (possibly empty) IDs.
    Cover a service_account admin subject too (the original's bypass
    tests).
  **Fixture discipline (lead refinement 9):** fixtures must account for
  `checkSelf` — never model a case where subject == resource on a
  `user`/`service_account` type with a read/update/delete permission
  unless checkSelf is the thing under test, so a self-grant can never
  silently pass a relation-expansion case.
  `memstore/conformance_test.go` runs the whole suite hermetically —
  green inside `make check` from this task forward. The suite is
  stdlib + sdk + this feature only (G2/FS1 keep drivers out).

## Acceptance

```sh
cd features/authorization && go build ./... && go vet ./... && go test -race ./...
make check     # 31 modules, all seven guards
make guard
```

Rule-6 greps (import-anchored), both directions:

```sh
grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/   # → empty
grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/features/authorization' features/authentication/ features/cms/ features/events/ features/jobs/   # → empty
```

`features/authorization/go.mod` requires exactly `sdk` — machine-checked
from task-1 on (the FS1 guard-list add + prove-can-fail land there;
review-gate fold, steward minor 4).

## Real-interaction check

Standing check (a): `make check` green (31 modules); boot
`examples/minimal` (:8081), `GET /` and `GET /products/widget-3000` →
200s (the new module is unwired in every host — behavior unchanged);
kill, port free. No user-facing surface exists in this phase; the
run-and-look is the no-regression proof.

## Execution log

(append dated entries here)
