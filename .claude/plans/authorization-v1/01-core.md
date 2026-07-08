# Phase Z1 — `features/authorization` core: BOTH kinds (rims, engine, roles service, socket, memstore, storetest)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: opus
Depends on: — (first phase)
Size: **XL** (grown from L at the 2026-07-08 multi-kind owner direction —
resized honestly). **Pre-declared split boundary (multi-kind re-review
fold, note 12):** if the relationship engine consumes the budget, Z1
lands relationship-only — tasks 1/3/4 + the relationship socket methods
+ the memstore/adversarial slices — and **Z1b** is the roles slice —
tasks 2/5 + the roles socket methods + the roles memstore/storetest
slices; the socket (task-6) is the join. Flag and split rather than
rush.
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2 (all of
it — the ruling cashed, the anatomy, the port split, storage semantics),
§9 (crud re-typing), §13 Z1, §14 (checklist trace) — **as amended by the
2026-07-08 multi-kind owner direction (00-overview: iam_* tables, the
roles kind, the deferred policy seam)**. Module 31 after task-1.

The feature this phase builds is the **IAM/authorization domain with two
independently-wireable kinds**: the relationship kind (the ReBAC engine
salvage — table `iam_relationships`) and the roles kind (NEW, minimal —
table `iam_roles`). ReBAC is one kind, not the feature's identity.

Salvage source for the RELATIONSHIP kind only (reference-only; design
ported, code re-typed fresh — the sdk-parity bar; never copy import
paths):
`gopernicus-original/core/auth/authorization/{authorizer,model,builder,schema_validator,membership,explain,cache_store,errors}.go`
+ `authorization_test.go` (the ~2,650-line behavioral reference).
The original's `Storer` is at `model.go:246` — **14 methods** (the
design's §2.5 list is abbreviated; salvage the full surface — overview
staleness finding 1). The original's Config is `{MaxTraversalDepth int}`
ONLY (`authorizer.go:16`); platform-admin is a DATA TUPLE, not a Config
field (review-gate fold, major 1). The ROLES kind has **no salvage
source** — it is new, deliberately minimal (overview cut refinement 12).

## DoD

- Module `github.com/gopernicus/gopernicus/features/authorization`
  compiles standalone, `go.mod` requires exactly `sdk` (FS1), registered
  in `go.work` + Makefile `MODULES` **and in the FS1 guard's hardcoded
  list with a recorded prove-can-fail** (review-gate fold, steward minor
  4); `make check` green at **31 modules**.
- `domain/relationship` public rim: tuple entity, `CreateRelationship`,
  filters, listing row types, and the full 14-method `Storer` port —
  listing methods **crud-re-typed** (`sdk/crud.ListRequest`/`Page[T]`,
  design §9; the original's `fop` vocabulary does not survive).
- `domain/role` public rim (NEW, refinement 12 as amended at the
  multi-kind re-review fold): `Assignment` entity +
  the 5-method `role.Storer` (`Assign`, `Unassign`, **`HasExactRole`**,
  the two listings) — plain lookups, NO graph walk; the
  `ListByResource` direct-scope-only pin and the store-stamped
  `CreatedAt` provenance in the port docs.
- Model DSL + schema validator: `NewSchema`/`ResourceSchema`/
  `PermissionRule` (`AnyOf` unions, `Through` traversals); unknown
  relations, bad through-targets, and schema cycles rejected loudly at
  `NewService` time.
- The relationship engine (`internal/logic/authorizersvc`): Check (incl.
  the `checkSelf` self-grant rule), through-traversal, cycle guards, the
  `MaxTraversalDepth` bound, CheckBatch, FilterAuthorized,
  LookupResources
  (`LookupResult{Unrestricted, IDs}` — non-nil IDs when restricted),
  relationship CRUD, RemoveMember with last-owner protection,
  ValidateRelation/ValidateRelationships/GetSchema/
  GetPermissionsForRelation, platform-admin bypass via the
  `platform:main#admin` DATA TUPLE (user AND service_account subjects —
  no Config field).
- The roles service (`internal/logic/rolesvc`, NEW): assign/unassign
  delegation + the service-level scope rule (global fallback per Q5's
  ratified answer); plain `(subjectType, subjectID)` pair signatures,
  never importing the relationship engine (re-review steward minor 6).
- Multi-kind FS2 socket (cut refinements 1/6/12/13): per-kind nil-safe
  `Repositories` fields, per-kind loud validation, per-kind Service
  method families, NO composed Check facade; `Register` logger-only,
  no routes; `/authorization/*` claimed-unregistered; engine vocabulary
  aliased at root (`Subject`, `Resource`, `CheckRequest`, `CheckResult`,
  `LookupResult`, `Schema`, …).
- `memstore/` public in-core (R3 allowance) — BOTH kinds: Go graph-walk
  group expansion + plain role maps, mutex-backed, honest (unique
  enforcement, direct-only counts).
- `storetest/` two-layer suite (cut refinement 4) with the **five named
  adversarial sub-runners** AND the `Roles/*` family, green against
  memstore hermetically inside `make check`.
- Rule-6 greps empty both directions; `make guard` green (G7 auto-covers
  the new feature).

## Preconditions

- `make check` green on the current tree (30 modules, 7 guards).
- Read the design §2 in full, the 00-overview owner-direction section,
  then the salvage files above — especially `model.go` (Storer + DSL +
  LookupResult doc contracts) and `membership.go` (last-owner semantics)
  — before typing anything.
- Read `features/jobs/jobs.go` (FS2 socket + routeless Register +
  public `memstore/` precedents) and
  `features/authentication/authentication.go` (alias-at-root precedent +
  the deny-by-absence subsystem validation shape this socket's per-kind
  wiring mirrors).
- Q5's answer known (role scope semantics — this phase implements it in
  task-5 and pins it in task-8's `Roles/GlobalFallback` case).

## Tasks

### task-1: module skeleton + `domain/relationship` rim + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/go.mod,
  features/authorization/domain/relationship/relationship.go,
  features/authorization/domain/relationship/relationship_test.go,
  go.work, Makefile]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make check` (31 modules; go.work ↔ MODULES agreement) and `make guard`; FS1 prove-can-fail (review-gate fold, steward minor 4): temporarily add a fake extra require to `features/authorization/go.mod`, observe `guard-feature-core-sdk-only` fail naming it, revert, `make guard` green again
- **description:** Create the module (go version + sibling `replace`
  per `features/jobs/go.mod`; requires sdk only). `domain/relationship`:
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
  cycle-safe). Package doc names the backing table `iam_relationships`
  (owner direction — the `rebac_` name does not survive). Port doc
  comments are the spec storetest executes
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

### task-2: `domain/role` rim — the roles kind's port (NEW)

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/domain/role/role.go,
  features/authorization/domain/role/role_test.go]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** The roles kind's public rim, exactly as pinned in
  overview refinement 12 (as amended at the multi-kind re-review fold) —
  minimal by direction, no salvage:
  `Assignment{SubjectType, SubjectID, Role, ResourceType, ResourceID,
  CreatedAt}` where the empty `("", "")` resource pair means a GLOBAL
  assignment (**empty strings, never NULL** — the DDLs pin the scope
  columns `NOT NULL DEFAULT ''` so the pair participates
  in the unique index under both dialects; doc comment says so —
  re-review lead major 1). **`CreatedAt` provenance (lead minor 9,
  in the port doc):** the STORE stamps it via the connector timestamp
  helpers; a duplicate `Assign` retains the ORIGINAL timestamp (ON
  CONFLICT DO NOTHING semantics).
  `role.Storer` — **5 methods, plain lookups, NO graph walk**:
  `Assign(ctx, Assignment) error` (idempotent — duplicate assignment is
  a no-op nil), `Unassign(ctx, subjectType, subjectID, role,
  resourceType, resourceID) error` (idempotent — zero rows deleted is
  nil, the `DeleteByUser` bulk precedent), **`HasExactRole`**`(ctx,
  subjectType,
  subjectID, role, resourceType, resourceID) (bool, error)` (**exact
  scope match at the store** — renamed from `HasRole` at the re-review
  fold, lead minor 8, so store and Service never share one name across
  two contracts; the doc comment states the exact-vs-fallback split and
  points at `Service.HasRole` for the Q5 rule, which lives in the
  service, task-5), `ListBySubject(ctx, subjectType, subjectID,
  crud.ListRequest) (crud.Page[Assignment], error)` and
  `ListByResource(ctx, resourceType, resourceID, crud.ListRequest)
  (crud.Page[Assignment], error)` (keyset, same cursor/tiebreak
  conventions as the relationship listing). **`ListByResource` doc pin
  (re-review lead major 3, mirroring the ratified
  CountByResourceAndRelation pin):** it returns direct-scope assignments
  ONLY and never surfaces globally-granted subjects that
  `Service.HasRole` would allow — an accepted-and-documented v1
  limitation; "effective grants for a resource" enumeration is a named
  deferred item. The port takes plain same-typed strings —
  **deliberate** (lead note 16, decided keep-strings): it mirrors the
  relationship `Storer`'s strings-only rim discipline and avoids a
  second scope vocabulary; the argument-swap risk is covered by the
  task-8 isolation cases. **Roles are opaque strings**
  the host interprets (the invitation `Relation` opacity precedent — no
  role registry/vocabulary in v1; a role model is policy-seam-adjacent;
  package doc says so). Package doc names the backing table `iam_roles`.
  Port doc comments are the spec the `Roles/*` storetest family
  executes. Rim test: compile-check stub pinning the signatures.

### task-3: model DSL + schema validator (relationship kind)

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
  EAV-spine philosophy applied to permissions). The model governs the
  RELATIONSHIP kind only — the roles kind has no model (opaque strings,
  task-2); say so. Tests re-typed from the
  original's model/validator coverage: valid schema round-trip, each
  rejection class, the builder helpers. Keep or drop the original's
  schema-merge `Remove()` affordance faithfully — log the call either
  way.

### task-4: the relationship engine

- **depends_on:** [task-3]
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
  **`MaxTraversalDepth` bound** — relationship-kind-scoped Config,
  default 10,
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
  task-7; the adversarial cases are storetest's in task-8 — do not
  duplicate them here beyond what unit-level coverage needs), race-run.

### task-5: the roles service (`internal/logic/rolesvc`, NEW)

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/authorization/internal/logic/rolesvc/service.go,
  features/authorization/internal/logic/rolesvc/service_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make guard`
- **description:** The roles kind's sealed service over `role.Storer` —
  deliberately thin, with **plain `(subjectType, subjectID string)` pair
  signatures throughout; it NEVER imports the relationship engine**
  (re-review steward minor 6 — the root socket alone adapts `Subject` →
  pair, task-6): `AssignRole`/`UnassignRole` delegation (input
  validation: empty subject/role → loud error; a scoped assignment
  requires BOTH resource fields or NEITHER — a half-scoped assignment is
  a loud error), the two listing delegations, and the one piece of real
  logic: `HasRole(ctx, subjectType, subjectID, role, resourceType,
  resourceID)`
  implementing **Q5's ratified scope rule** (recommended: exact-scoped
  `HasExactRole` lookup first, then the global `("", "")` fallback — one
  documented
  rule, two store lookups worst case, no graph walk; if Q5 ratifies
  no-fallback, this is a single exact lookup and the doc says callers
  compose). Fail-closed: any store error returns `(false, err)`. Tests
  against an in-package fake: idempotence pass-through, half-scoped
  rejection, the scope rule both ways (scoped hit, global-fallback hit,
  miss), error propagation, race-run.

### task-6: the multi-kind FS2 socket + root aliases

- **depends_on:** [task-4, task-5]
- **model:** opus
- **files:** [features/authorization/authorization.go,
  features/authorization/authorization_test.go]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment: `! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/`
- **description:** The host-facing surface (cut refinements 1/6/7/12/13,
  as corrected at the review-gate fold and amended by the owner
  direction). **Multi-kind wiring:**
  `Repositories{Relationships relationship.Storer, Roles role.Storer}` —
  each kind nil-safe; nil = that kind OFF structurally (the auth
  Providers/Granter deny-by-absence precedent). `Config{Model Schema,
  MaxTraversalDepth int}` — both relationship-kind-scoped;
  `MaxTraversalDepth <= 0` ⇒ default 10, never an error; **no
  `PlatformAdmin` field** — platform-admin is the data tuple, task-4.
  Validation at `NewService(repos, cfg) (*Service, error)`: zero kinds
  wired → loud exported `ErrNoKindConfigured`; `Relationships` wired ⇔
  `Model` set — either without the other is a loud partial-wiring error
  (exported, the `ErrOAuthReposRequired` precedent); invalid model =
  the validator's loud error. Calling an unwired kind's methods returns
  a loud exported per-kind sentinel — **`ErrRelationshipsNotConfigured`
  / `ErrRolesNotConfigured`** (re-review lead minor 10: errs discipline,
  no string matching) — fail closed, never a silent
  false/allow; document it on every method family. **The roles-kind
  socket methods adapt `Subject` → the plain pair for `rolesvc` and
  REJECT a `Subject` with non-empty `Relation` loudly** (re-review
  steward minor 6, decided fail-closed: userset subjects are a
  relationship-kind concept — silently dropping the field would treat
  `group#member` as the group itself, a wrong-grant hazard). `(*Service)
  Register(m feature.Mount) error` — **registers no routes** (jobs
  precedent), logs one line via `m.Logger` when non-nil; the
  `/authorization/*` namespace is claimed for a future admin surface
  (package doc says so). **Per-kind method families, NO composed Check
  facade (refinement 13** — a host composes kinds in its own closure;
  say so in the package doc): relationship kind — Check, CheckBatch,
  FilterAuthorized, LookupResources,
  CreateRelationships, DeleteRelationship, DeleteResourceRelationships,
  DeleteByResourceAndSubject, RemoveMember, ValidateRelation,
  ValidateRelationships, GetSchema, GetPermissionsForRelation,
  ListRelationshipsBySubject, ListRelationshipsByResource (promoted from
  `authorizersvc`); roles kind — AssignRole, UnassignRole, HasRole,
  ListRoleAssignmentsBySubject, ListRoleAssignmentsByResource (promoted
  from `rolesvc`). Root aliases
  (the `auth.Granter` precedent): `Subject`, **`Resource`** (review-gate
  fold, lead refinement 7 — Z4 constructs `authorization.Resource{…}`;
  it won't compile otherwise), `CheckRequest`,
  `CheckResult`, `LookupResult`, `Schema`, `NewSchema`,
  `ResourceSchema`, `PermissionRule` + builders, and the roles kind's
  `Assignment` (`= role.Assignment` — hosts construct it) — hosts write
  `authorization.CheckRequest{Subject: authorization.Subject{…}}`
  exactly as design §2.2's snippet shows; **verify that CheckBatch/
  FilterAuthorized/HasRole argument types need no further root aliases**
  (lead refinement 7) and add any that do. Tests additionally cover the
  non-empty-`Relation` rejection on every roles-kind method and both
  named sentinels. Package doc opens with the
  three-posture posture note plus the KINDS framing (one paragraph each;
  the full tables are the README's, Z5) and the AV2 split: consumer
  seams are Check-only; everything on `Service` beyond the boolean
  checks is flagship-specific API, never a seam. Tests: construction
  validation (zero kinds; each partial-wiring pair; invalid model;
  roles-only wiring succeeds with no model), unwired-kind sentinel on
  both families, promoted-method delegation smoke on both kinds,
  Register-with-logger, zero-value `feature.Mount` tolerance.

### task-7: `memstore/` — the public in-core reference, BOTH kinds

- **depends_on:** [task-6]
- **model:** opus
- **files:** [features/authorization/memstore/memstore.go,
  features/authorization/memstore/roles.go,
  features/authorization/memstore/memstore_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make guard`
- **description:** Public in-core implementation of BOTH kind ports
  (the R3 allowance: substantial — group expansion re-implemented as a
  Go graph walk — and host-needed: Z4's zero-infra proof runs on it;
  `features/jobs/memstore` is the placement precedent; never a
  `stores/memory` module). Relationship kind: mutex-backed; unique-tuple
  enforcement honest
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
  implements the same contract). Roles kind (`roles.go`): plain
  mutex-backed maps implementing the 5-method `role.Storer` — exact-
  scope `HasExactRole`, idempotent Assign/Unassign (duplicate retains
  the original CreatedAt — the honest mirror of the stores' ON CONFLICT
  semantics), keyset listing with the
  same tiebreak conventions. memstore_test runs the task-8 suite
  hermetically once it exists (wire the call in task-8; this task's
  tests cover memstore-specific mechanics for both kinds).

### task-8: `storetest/` — the two-layer conformance suite: NAMED adversarial sub-runners + the `Roles/*` family

- **depends_on:** [task-7]
- **model:** opus
- **files:** [features/authorization/storetest/storetest.go,
  features/authorization/storetest/adversarial.go,
  features/authorization/storetest/roles.go,
  features/authorization/memstore/conformance_test.go]
- **verify:** `cd features/authorization && go build ./... && go test -race ./... && go vet ./...` then `make check` (the suite runs hermetically via memstore on every future `make check`) and `make guard`
- **description:** `storetest.Run(t, newRepos func(t *testing.T)
  authorization.Repositories)` — the shipped implementations wire BOTH
  kinds (cut refinement 4, amended multi-kind). **Nil-kind behavior
  (re-review steward minor 5):** a nil `Repositories` field skips that
  kind's families with a loud named `t.Skip` — deny-by-absence extended
  to conformance, so a single-kind host store can prove conformance.
  **Layer (a), port
  contracts against the Storers directly.** Relationship kind: tuple
  CRUD round-trip + duplicate semantics; the three delete variants;
  `CheckRelationExists`; `GetRelationTargets`; `CheckBatchDirect` map
  semantics; `CountByResourceAndRelation` direct-only; the three Lookup*
  primitives; listing pagination (keyset cursor round-trip + stable
  tiebreak + empty-page shape — pin the empty-page case here, closing
  the D5-era gap for this feature from day one). Roles kind (the
  **`Roles/*` named family**, `roles.go`):
  - `Roles/AssignIdempotent` — duplicate assign is a no-op nil; the row
    count stays 1 **including for the GLOBAL `("", "")` pair — asserted
    via the listing so the dedup is proven at the CONSTRAINT level, not
    application logic** (re-review lead major 1: a nullable scope column
    would make two global rows distinct under both dialects'
    unique-index NULL semantics); the retained-original-CreatedAt
    semantics asserted too (lead minor 9).
  - `Roles/UnassignIdempotent` — unassign of an absent assignment is
    nil; repeat-unassign is nil.
  - `Roles/HasExactRole` (renamed with the port method — lead minor 8) —
    store-level exact matching: a global
    assignment does NOT satisfy a scoped store lookup and vice versa;
    **and scopedA-vs-scopedB isolation** (re-review lead major 4: an
    assignment on resource A never satisfies a lookup on resource B —
    the case that catches an accidentally 4-tuple unique index or
    lookup silently collapsing distinct scopes). The service-level
    fallback is layer (b)'s to prove.
  - `Roles/DistinctAssignmentsCoexist` (NEW — re-review lead major 4) —
    same subject, two roles → two rows, both `HasExactRole`-true; same
    subject + role, two scopes → two rows, both true; the listings
    return all of them.
  - `Roles/ListPagination` — keyset round-trip + tiebreak + empty page,
    both listing methods.
  **Layer (b), engine/service-over-store:** construct
  `authorization.NewService` with a fixture schema over the stores under
  test and assert authorization OUTCOMES — this is what proves the
  memstore and the SQL stores authorize identically (design §2.3). The
  **named adversarial sub-runners** (design §13 Z1, verbatim — these
  names appear literally in `t.Run` and in the per-dialect live
  artifacts):
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
  - `Roles/GlobalFallback` (layer (b), service-level) — pins Q5's
    ratified scope rule: under the recommended answer, a global
    assignment satisfies a scoped `Service.HasRole` while the store-level
    lookup stays exact; a scoped assignment never satisfies a
    different scope; a miss is `(false, nil)`.
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
