# Phase Z1 — `features/authorization` core: BOTH kinds (rims, engine, roles service, socket, memstore, storetest)

Status: **RATIFIED 2026-07-09 (jrazmi) — Q1-Q7 at recommendations; EXECUTING**
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
roles kind, the deferred policy seam)**. Module 32 after task-1 (31 at cut +1 — google-uuid landed 2026-07-09).

The feature this phase builds is the **IAM/authorization domain with two
independently-wireable kinds**: the relationship kind (the ReBAC engine
salvage — table `iam_relationships`) and the roles kind (NEW, minimal —
table `iam_roles`). ReBAC is one kind, not the feature's identity.

Salvage source for the RELATIONSHIP kind only (reference-only; design
ported, code re-typed fresh — the sdk-parity bar; never copy import
paths):
`../gopernicus-original/core/auth/authorization/{authorizer,model,builder,schema_validator,membership,explain,cache_store,errors}.go`
(sibling of this repo's root — path corrected 2026-07-08, codex fold A7)
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
  4); `make check` green at **32 modules**.
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

- `make check` green on the current tree (31 modules, 7 guards).
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
- **Q6 + Q7 ratified (2026-07-09 fresh-review fold — these are Z1
  execution gates, not just docs concerns): Q6 sets `Config.IDs`, the
  `CreateRelationships` mint seam (task-4/task-6), the memstore
  assign-at-insert + DO-NOTHING mirror (task-7), and the
  `Relationship/DBGeneratedIDOnEmpty` + partial-batch conformance cases
  (task-8); Q7 sets the second-relation-same-subject conflict semantics
  the task-1 port doc and task-8 constraint case assert. Do not start Z1
  until both are ruled — they change entity behavior, not just wording.**

## Tasks

### task-1: module skeleton + `domain/relationship` rim + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/go.mod,
  features/authorization/domain/relationship/relationship.go,
  features/authorization/domain/relationship/relationship_test.go,
  go.work, Makefile]
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make check` (32 modules; go.work ↔ MODULES agreement) and `make guard`; FS1 prove-can-fail (review-gate fold, steward minor 4): temporarily add a fake extra require to `features/authorization/go.mod`, observe `guard-feature-core-sdk-only` fail naming it, revert, `make guard` green again
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
  on `CreateRelationships`). **`CreateRelationships` doc also pins the
  one-relation-per-subject-per-resource rule (2026-07-08 owner ruling,
  codex fold A2 — the original's `idx_rebac_relationships_unique_subject`
  ADOPTED): a subject holds at most ONE relation on a resource (owner OR
  member, never both; schema `AnyOf` handles implication); a second
  relation for the same subject on the same resource is — under the
  original's bare `ON CONFLICT DO NOTHING` — a SILENT NO-OP (nil error,
  existing relation unchanged, NOT `ErrAlreadyExists`; pending Q7,
  2026-07-09 data-integration fold), and role change stays
  delete+create.** **ID strategy (Q6, 2026-07-09):
  `CreateRelationships(...) error` is error-only; `relationship_id` is
  minted at the engine delegation (task-4) from `Config.IDs`, never in the
  rim — there is no `NewRelationship(IDs, …)` constructor (minting is a
  service-seam concern here; the item-14 "constructor" letter carries this
  recorded exception, documented on the port). Under `cryptids.Database`
  the store omits the id column and the DDL DEFAULT fills it (task-1 DDL
  is authored in Z2a; the rim doc just states the strategy).** Rim test:
  compile-check stub pinning the signatures. Register in `go.work` + Makefile `MODULES` (alphabetical:
  after `features/authentication/stores/turso`, before `features/cms`);
  bump the Makefile header count 31 → 32; **add `features/authorization`
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
  `<= 0` ⇒ 10; **ENGINE-ONLY (2026-07-08 owner ruling, codex fold A1,
  superseding lead refinement 8): it bounds the engine's Go
  through-traversal recursion exactly as the original does
  (authorizer.go:167) and is never threaded into the stores — group
  expansion in memstore/CTEs is unbounded-but-cycle-safe**), **`checkSelf`
  explicitly in scope** (authorizer.go:~250, lead refinement 9:
  self-grant with reason "self" when subject == resource for `user`/
  `service_account` resource types and permission ∈ {read, update,
  delete} — nothing else), `CheckBatch`,
  `FilterAuthorized`, `LookupResources` returning
  `LookupResult{Unrestricted, IDs}` with the original's contract
  verbatim in intent (**explicit bool, fail-closed zero value; IDs
  always non-nil when restricted; Unrestricted ⇒ caller skips ID
  filtering entirely**), relationship CRUD delegation — **`CreateRelation
  ships` mints each tuple's `relationship_id` here from the injected
  generator (Q6, 2026-07-09): the service holds a `cryptids.IDGenerator`
  (from `Config.IDs`, threaded via the socket in task-6) and calls
  `MustGenerate()` per tuple, exactly as the original's satisfier looped
  `generateID()` into `CreateRebacRelationship.RelationshipID`
  (`satisfiers/authorization_store.go:97-118`); under `cryptids.Database`
  every id is `""` and the store omits the column. The mint is all-or-none
  per batch (one generator), so the store's ALL-empty branch selection is
  guaranteed by construction** —
  `RemoveMember`
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
  MaxTraversalDepth int, IDs cryptids.IDGenerator}` — all three
  relationship-kind-scoped; `MaxTraversalDepth <= 0` ⇒ default 10, never
  an error; **`IDs` zero value ⇒ the nanoid default (Q6, 2026-07-09),
  threaded into `authorizersvc` so `CreateRelationships` mints
  `relationship_id`; ignored-with-documented-note under roles-only wiring,
  exactly like `MaxTraversalDepth` (the auth `MailFrom` precedent — an
  orphaned tuning field is silent, not a loud error)**; **no
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
  (duplicate semantics exactly as task-1 pinned, **including the
  one-relation-per-subject-per-resource conflict — codex fold A2**;
  **the honest DO-NOTHING mirror (Q6, 2026-07-09 data-integration fold):
  on an in-batch conflict skip the row silently, keep the EXISTING row's
  id + created_at, insert the non-conflicting siblings, return nil, and
  never leak a minted id for a skipped row; an empty incoming
  `relationship_id` gets a nanoid assigned at insert — the schema-DEFAULT
  mirror, matching the reference-store pattern in the auth/cms
  memstores**);
  graph-walk group
  expansion with a visited-set cycle guard (the memstore must survive
  A∈B, B∈A data — the suite will prove it) — **unbounded-but-cycle-safe
  (2026-07-08 owner ruling, codex fold A1, superseding lead refinement
  8: the original never threads `MaxTraversalDepth` into its store — its
  CTE at `../gopernicus-original/core/repositories/rebac/rebacrelationships/rebacrelationshipspgx/store.go:22-30`
  terminates by UNION dedup alone, and the engine bounds only its own Go
  recursion; the memstore's visited set is the honest mirror, no depth
  parameter anywhere in the port)**;
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
  the D5-era gap for this feature from day one); **two constraint-level
  cases (codex folds A2+A3): a duplicate direct tuple (same six columns,
  empty subject_relation included) conflicts/no-ops per the task-1 pin —
  proven at the CONSTRAINT level, not application logic (the
  `Roles/AssignIdempotent` precedent) — and a SECOND relation for the
  same subject on the same resource is a SILENT NO-OP (nil error, the
  existing relation UNCHANGED on re-read — NOT `ErrAlreadyExists`; pending
  Q7, asserted by re-read never error-shape; the adopted unique-subject
  index under bare `ON CONFLICT DO NOTHING`, 2026-07-09 data-integration
  fold).** **Plus `Relationship/DBGeneratedIDOnEmpty` (Q6, 2026-07-09):
  a `cryptids.Database`-wired create batch → every `relationship_id`
  comes back non-empty and each row is readable — asserted VIA THE LISTING
  (`ListBySubject`/`ListByResource`), since `CreateRelationships` returns
  no rows; and a partial-batch case [new, duplicate-tuple, new] → both new
  rows present, duplicate skipped, nil error, identical across memstore +
  both dialects (the RETURNING/DO-NOTHING row-count trap). Pagination
  under DB-generated ids asserts per-backend coverage consistency (every
  row exactly once across page boundaries) and NEVER compares id ordering
  across backends — uuid-text (pgx) and hex (turso) sort differently, and
  bulk create stamps one `created_at` for the whole batch so the id
  tiebreak is fully load-bearing.** Roles kind (the
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
    (user→G3→G2→G1→resource). **The depth-boundary pair is DROPPED
    (2026-07-08 owner ruling, codex fold A1, superseding lead refinement
    8): group expansion is unbounded-but-cycle-safe in every backend —
    matching the original, whose CTE carries no depth term — so there is
    no store-level bound to probe; `MaxTraversalDepth` bounds only the
    engine's through-traversal recursion and stays engine-only.**
  - `Adversarial/DiamondDedup` — diamond/multi-path membership
    deduplicates, **with an explicit `CountByResourceAndRelation`
    assertion**: multiple expansion paths never inflate the direct count
    (§2.5 — a count divergence is a security divergence; last-owner
    protection depends on it).
  - `Adversarial/NestedUserset` — **tuple-side** userset subjects
    resolve: tuples STORED with `subject_relation` set
    (`org:acme#member@group:eng#member`) grant access to the group's
    members transitively. **(Reworded 2026-07-08, codex fold A5: the
    check signatures never carry a subject relation and the original
    engine ignores request-side `Subject.Relation` on checks —
    model.go:44 is dead there; the userset resolves via stored tuples +
    expansion, so the fixture seeds it tuple-side, never by setting
    `Subject.Relation` on the CheckRequest.)**
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
make check     # 32 modules, all seven guards
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

Standing check (a): `make check` green (32 modules); boot
`examples/minimal` (:8081), `GET /` and `GET /products/widget-3000` →
200s (the new module is unwired in every host — behavior unchanged);
kill, port free. No user-facing surface exists in this phase; the
run-and-look is the no-regression proof.

## Execution log

(append dated entries here)

### 2026-07-09 — task-1 DONE (relationship rim + module 32 registered)

`features/authorization/go.mod` (sdk-only, sibling replace). `domain/
relationship/relationship.go`: the tuple rim — `CreateRelationship` (carries
`RelationshipID`, the engine-populated mint-seam field per Q6; hosts leave it
zero, empty ⇒ store omits column ⇒ DDL DEFAULT), `RelationTarget`, projection
rows `SubjectRelationship`/`ResourceRelationship` **each now carrying `ID`**
(the surrogate relationship_id — needed as the keyset tiebreak AND as the
vehicle the Q6 `DBGeneratedIDOnEmpty` case reads to prove a minted key
non-empty via the listing; a justified add over the original, which had no id
on projections), the two crud-re-typed listings (`crud.ListRequest` in,
`crud.Page[T]` out — the original's `fop.Order`/`PageStringCursor`/`Pagination`
vocabulary does not survive), and the full **14-method `Storer`** salvaged
from `model.go:246` with the Q6 minting + Q7 silent-no-op + §2.5 direct-count
doc pins. Compile-check stub test pins all 14 sigs. Registered module 32:
go.work, Makefile `MODULES`, FS1 guard hardcoded list (+prove-can-fail: fake
require ⇒ guard fails naming `features/authorization`, reverted, green),
header 31→32. `make check` green (32 modules), `make guard` green.

**Design note carried forward to Z2a/Z2b:** the mint stays at the engine
(the original minted in the SATISFIER; Q6 moves it up to `authorizersvc`), so
the store port's `CreateRelationship` — not a separate store-layer input —
carries `RelationshipID`. Store `CreateRelationships` reads it: ""⇒omit column.

### 2026-07-09 — task-2 + task-3 + task-4 DONE

**task-2** (`domain/role/role.go`): `Assignment` + the 5-method `role.Storer`
(`Assign`/`Unassign` idempotent, `HasExactRole` exact-scope, two crud-typed
keyset listings). Doc pins: global `("","")` = empty-strings-not-NULL, scope
cols `NOT NULL` empty-string default, `ListByResource` direct-scope-only,
retained-original-`CreatedAt`, opaque-string roles, table `iam_roles`. Stub test.

**task-3** (`authorizersvc` model/builder/validator): re-typed the schema DSL
(`Schema`/`ResourceTypeDef`/`RelationDef`/`SubjectTypeRef`/`PermissionRule`/
`PermissionCheck`/`ResourceSchema` + `Direct`/`Through`/`AnyOf`/`Remove`/
`IsRemove`), the check types (`Subject`/`Resource`/`CheckRequest`/`CheckResult`/
`LookupResult`), `NewSchema`/`MergeResourceType` (merge-Remove **KEPT**,
logged), and the validator (unknown-direct, unknown-through, missing-target-
permission, cycle — all four rejection classes tested). `maps.Copy` for the
relations merge (idiom).

**task-4** (`authorizersvc` service/membership/lookup): re-typed the full
engine — `Config{MaxTraversalDepth, IDs}`, `NewService` (validates schema →
`ErrInvalidSchema`, depth default 10), `Check` (admin→self→schema),
`checkThrough`/`checkSelf`/`CheckBatch` (optimized + sequential)/
`FilterAuthorized`, CRUD with **Q6 mint in `CreateRelationships` on a COPIED
slice** (default nanoid stamps ids; `cryptids.Database` leaves ""; caller slice
never mutated — tested), `RemoveMember` last-owner (direct count), the
Validate*/GetSchema/GetPermissionsForRelation surface, crud-typed listing
delegations, and `LookupResources`/`lookupThrough`/`lookupDirectOnly`
(unbounded cycle-safe). **Dropped** (logged): the unused `log` field + `WithLogger`
option, and `ChangeMemberRole` (not in the promoted surface — dead public API).
`MaxTraversalDepth` engine-only (`maxDepth`), never passed to the store. Tests
race-run green against an in-package fake store (Check direct/self/admin/through,
batch+filter, mint both generators, validation, last-owner, lookup direct/empty-
non-nil/unrestricted/through). Note: gofmt's doc-comment formatter rewrites a
literal `''` to a smart quote — reworded to "empty-string default" to avoid it.

### 2026-07-09 — task-5 + task-6 DONE (both services + socket; `make check` green @ 32)

**task-5** (`rolesvc`): sealed roles service over `role.Storer`, plain
(subjectType, subjectID) args, **no `authorizersvc` import** (verified — only a
doc-comment mention). `AssignRole`/`UnassignRole` (validate empty subject/role
→ `ErrInvalidRoleAssignment`; half-scoped → `ErrHalfScopedAssignment`),
`HasRole` (Q5: exact-then-global-fallback; scoped grant never satisfies another
scope), two listing delegations, fail-closed on store errors. Race tests green.

**task-6** (`authorization.go` socket + `authorization_test.go`): `Repositories
{Relationships, Roles}` nil-safe; `Config{Model, MaxTraversalDepth, IDs}`;
`NewService` — zero kinds → `ErrNoKindConfigured`, `Relationships` XOR `Model` →
`ErrModelRequired`, invalid model → validator error, roles-only OK. Per-kind
method families guard nil → `ErrRelationshipsNotConfigured` /
`ErrRolesNotConfigured`; roles methods take a root `Subject`, adapt → pair,
reject non-empty `Relation` → `ErrUsersetSubjectOnRole` (every roles method).
`Register` logs one line, no routes, tolerates zero `Mount`. Root aliases: the
engine model/check vocab + DSL (`NewSchema`/`Direct`/`Through`/`AnyOf`/`Remove`/
`MergeResourceType` as `var` re-exports), the relationship rim types, and
`Assignment`. `GetSchema`/`GetPermissionsForRelation` gained an `error` return
for the nil-kind guard (socket-local signature; engine's stays error-free).
Tests: construction matrix, both sentinels, userset-rejection on all roles
methods, both-kind delegation smoke, Register (logger + zero-Mount). Rule-6
grep empty both directions. `make check` green @ **32 modules**, `make guard` green.

**Remaining Z1:** task-7 (`memstore/` both kinds — real graph-walk expansion),
task-8 (`storetest/` named adversarial + `Roles/*` families). Both kinds are
already through the socket — NO Z1/Z1b split needed; landing Z1 whole.

### 2026-07-09 — task-7 + task-8 DONE — **Z1 CLOSED, `make check` green @ 32 modules**

**task-7** (`memstore/`): the public in-core reference, both kinds.
`Relationships` — mutex-backed; unique (subject, resource) key with the ON
CONFLICT DO NOTHING mirror (second relation same subject/resource skipped
silently, existing row's id+created_at retained); empty incoming id assigned a
nanoid (schema-DEFAULT mirror, `memIDs`); **graph-walk group expansion** over
`member` edges with a visited-set (unbounded, cycle-safe — subject_relation
stored-but-ignored in the walk, exactly like the salvage CTE); direct-only
count; fixpoint descendant walk; keyset listings via a replicated `pageMem`
(the auth storetest-reference paginator, using public `crud.TrimPage`/
`MarkPrevPage`/`EncodeCursor`/`DecodeCursor`). `Roles` — 5-tuple exact,
idempotent Assign retaining CreatedAt, keyset listings tie-broken on the 5-tuple
composite (no surrogate id). Race tests green.

**task-8** (`storetest/` + `memstore/conformance_test.go`): `Run(t, newRepos)`
with nil-kind `t.Skip`. **Layer (a)** relationship port contracts (CRUD,
duplicate no-op, second-relation silent no-op, 3 deletes, CheckBatchDirect,
direct-only count, 3 lookups, listing pagination + empty page, and
`Relationship/DBGeneratedIDOnEmpty` incl. the partial-batch full-coverage
assertion). **Layer (b)** the five NAMED adversarial sub-runners —
`Adversarial/{MembershipCycle,DeepNesting,DiamondDedup,NestedUserset,
Unrestricted}` (Unrestricted covers both user AND service_account admins via
the `platform:main#admin` data tuple; DiamondDedup asserts the direct count
stays 1; NestedUserset seeds the userset tuple-side with subject_relation set,
never on the CheckRequest) over `authorization.NewService` + the fixture schema.
**`Roles/*`** family — AssignIdempotent (global-pair dedup), UnassignIdempotent,
HasExactRole (+ scopedA-vs-scopedB isolation), DistinctAssignmentsCoexist,
ListPagination (both methods + empty page), and the service-level GlobalFallback.
checkSelf fixture discipline honored (no subject type == a resource type with
r/u/d). Verified all 21 named sub-runners RUN + PASS (`-v`); `-race` green;
`make check` @ **32 modules**; `make guard` green.

**Real-interaction check (standing a):** `examples/minimal` booted on :8081 →
`GET /` 200, `GET /products/widget-3000` 200 (module unwired everywhere — no
regression); killed, port freed. **Z1 acceptance met. Next leg: Z2a (turso).**
