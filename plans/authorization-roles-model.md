# The roles kind, finished — a passed-in role model and one decision surface across kinds

**Feature:** `features/authorization` (root + `internal/logic/{authorizersvc,rolesvc}` + a new `internal/logic/decisionsvc`; `storetest`; `examples/auth-cms`). No store module changes.
**Issue:** gopernicus/gopernicus#5
**Status:** RATIFIED 2026-08-26 (all six recommendations accepted). **RELEASED 2026-08-26 — `features/authorization/v0.3.0` tagged at 552193e on owner dispatch ("go ahead"); cold-resolution verified from a throwaway module (`go list -m …@v0.3.0` → v0.3.0 + sdk v0.1.0; `NewService` with a `RoleModel` constructs); issue #5 closed. See §Execution log.**
**Traced against:** `main` @ `f546a64` (= `features/authorization/v0.2.0`, tagged 2026-08-26).
**Target tag:** `features/authorization/v0.3.0` (minor; additive decision capability, with the named sentinel-identity and unkeyed-literal caveats in §Compatibility). Store modules NOT retagged (D6).
**Reference implementation:** `/Users/jrazmi/code/gps/three-sixty/gps-360-go/features/auth/{logic/model.go,logic/guard.go,inbound/middleware/authorization.go}` @ `d72a1b9` — its generic role-decision half is lifted here; its application-specific `IsSteward` flag is not (§Adoption).
**Consulted (2026-08-26):** lead-backend-engineer, architecture-steward, product-manager — all ship-with-edits; edits folded in and attributed in §Consultation.

## Problem (as filed, confirmed against the code)

The roles kind stores and answers `HasRole` (exact scope, then the Q5 global fallback —
`internal/logic/rolesvc/service.go:83-89`) and lists assignments, but it has **no model**:
roles are opaque strings and no permission is ever derived from one. The whole decision
surface — `Check`, `CheckBatch`, `CheckExplain`, `LookupResources`, `FilterAuthorized`,
`RequirePermission` / `RequirePermissionOn` / `RequirePermissionFixed` — is
relationship-kind only: it returns `ErrRelationshipsNotConfigured` (or, for the gates,
panics at mount — root `middleware.go:36-38,48-51,57-60`) on a roles-only host. `Config`
carries relationship settings only.

So an RBAC-with-scopes host builds the missing half itself. gps-360-go did; most of that
machinery is generic, while its ultimate `IsSteward` policy is not:

| gps-360-go | is | belongs in gopernicus as |
|---|---|---|
| `logic/model.go` `grants` map | permission → roles, per resource type; `Legal()` | a validated **`RoleModel`** passed at construction |
| `logic/guard.go` `Allowed` | any granting role at scope-or-global, with steward as an application fallback | roles-kind `Check`, with steward listed explicitly on the permissions it should grant |
| `logic/guard.go` `AuthorizedIDs` | walk `ListRoleAssignmentsBySubject`; a global granting role ⇒ unrestricted | roles-kind `LookupResources` (+ an **unrestricted** result) |
| `logic/guard.go` `IsSteward` | the application's `ManageAuthorization` flag and current catch-all bypass | stays application-specific; it is not a role-engine primitive |
| `middleware/authorization.go` `Param`/`Fixed` + `Legal` | coordinate gates, legality at boot | `RequirePermissionOn/Fixed` on roles-only hosts, checked against the role model |

auth-cms shows the other half of the demand from the other side: it wires BOTH kinds over
one resource type — `project/view` is a relationship permission, `project` + the `auditor`
role is a hand-gated `HasRole` (`examples/auth-cms/cmd/server/demo.go:101`) — and hand-writes
`isPlatformAdmin` (`membership.go:241`) for "admin sees everything".

## Proposal in one paragraph

Add a **`RoleModel`** (resource type → roles + permission→roles grants) validated at
`NewService` like the relationship `Schema`; a **role engine** that
answers `Check`/`CheckBatch`/`CheckExplain`/`LookupResources` for the roles kind over the
existing `role.Storer` port; and a **composite decider** that owns the ONE decision surface:
dispatch each `(resourceType, permission)` pair to the ONE model that declares it. The
coordinate gates and their registration-time
legality check work whenever at least one model-bearing kind is configured. `LookupResult`
gains `Unrestricted`. Hosts that configure no role model see today's behaviour unchanged.
There is deliberately no universal-role bypass in the feature: an application lists a
globally held administrative role on every permission it should grant, and keeps any true
"bypass every present and future decision" flag in application composition.

## Decisions

### D1 — `RoleModel`: the shape

```go
// RoleModel is the roles kind's permission model: which roles exist on each
// resource type and which permissions those roles grant. Hand-typed, one
// RoleTypeDef per resource type — the
// same shape hosts already write for the relationship Schema.
type RoleModel struct {
    ResourceTypes map[string]RoleTypeDef
}

// RoleTypeDef is one resource type's roles and grants.
type RoleTypeDef struct {
    Roles       []string            // roles assignable at (type, id)
    Permissions map[string][]string // permission → roles that grant it
}
```

Root aliases `authorization.RoleModel`, `authorization.RoleTypeDef` (beside `Schema` /
`ResourceTypeDef`). Home: `internal/logic/decisionsvc/role_model.go` (D3).

The gps model transcribed by effective behaviour — every role and permission it declares
today, with the current steward fallback made explicit on each permission:

```go
authorization.RoleModel{
    ResourceTypes: map[string]authorization.RoleTypeDef{
        "platform": {Roles: []string{"steward", "developer"},
            Permissions: map[string][]string{
                "steward": {"steward"}, "developer": {"steward", "developer"},
                "delete": {"steward"}, "partnership_financials": {"steward"}, "changelog_viewer": {"steward"},
            }},
        "organization": {Roles: []string{"viewer", "contributor", "report_editor", "report_publisher", "steward"},
            Permissions: map[string][]string{
                "view":           {"viewer", "contributor", "report_editor", "report_publisher", "steward"},
                "contribute":     {"contributor", "steward"},
                "report_edit":    {"report_editor", "report_publisher", "steward"},
                "report_publish": {"report_publisher", "steward"},
            }},
        "section": {Roles: []string{"member", "steward"}, Permissions: map[string][]string{"enter": {"member", "steward"}}},
        "page":    {Roles: []string{"viewer", "steward"}, Permissions: map[string][]string{"view": {"viewer", "steward"}}},
    },
}
```

`steward` is repeated deliberately. gps-360-go assigns it globally at `("", "")`, so Q5's
global fallback makes it grant every permission that explicitly lists it, and
`LookupResources` returns `Unrestricted` for those pairs. It does NOT silently acquire a
new permission or a relationship-owned permission added later. The application's separate
`ManageAuthorization` capability may still be computed by an exact global `HasRole` probe.

**Deviation from the issue sketch — no `Global{Roles, Permissions, Superuser}` sub-struct.**
Global scope is assignment data, not a second permission namespace:

- *Global assignment of a scoped role* is the existing Q5 rule: a `viewer` assigned at
  `("","")` satisfies `view` on every organization. The model need not declare which roles
  may be global — any may; the assignment's scope is the fact.
- *Resource-type-independent permissions* (`developer`, `changelog_viewer`) are a
  singleton resource type — exactly gps's `platform:global`
  (`Fixed(ResourcePlatform, PermissionSteward, "global")`). `CheckRequest.Validate` requires
  a `(Type, ID)` resource; a `Global.Permissions` map would need a second, resource-less
  request shape that nothing else in the feature has.
- *Universal bypass* is application policy, not a role-model fact. A globally held role may
  be listed explicitly on every role-owned permission it should grant. If an application
  truly wants one flag to bypass relationship-owned or future permissions too, it composes
  that before the feature's decision surface and owns the widening.

**Presence.** The model is SET when `len(ResourceTypes) > 0`. The zero value is "no model"
and is not validated.

**Validation** (`ErrInvalidRoleModel`, wrapping `sdk.ErrInvalidInput`, mirroring
`ErrInvalidSchema`) — at `NewService`, first failure wins, message names the symbol:

1. set (above);
2. every resource-type, role, and permission name passes `relationship.ValidateRefField`
   (non-empty, bounded, UTF-8, no control chars — the rule the check path applies to
   request fields; a same-module helper reuse as in `CheckRequest.Validate`, not a
   roles→relationships kind dependency — do not relocate it);
3. within a type: no duplicate role in `Roles`; every permission's role list is non-empty
   and every role in it is declared in that type's `Roles`; every declared role grants at
   least one permission of that type (an unused role is a typo until proven otherwise);
4. **pair ownership across models** (`ErrModelConflict`, at `NewService`): a
   `(resourceType, permission)` pair declared by BOTH the relationship `Schema` and the
   `RoleModel` is a construction error. A resource TYPE may appear in both (auth-cms's
   `project`: `view` from relationships, `audit` from roles) — only a pair may not. This is
   what makes D4 a dispatch, not a merge.

**Deliberate asymmetry with assignments.** `AssignRole`'s rim validation stays
emptiness-only and opaque; a stored role name the model cannot express is simply never a
grantor. (Assign-time model validation is D8, a separate switch.)

The engine deep-copies the model at construction and never retains the caller's maps (the
`Compile` precedent). Grantor lists are stored **sorted**: evaluation order — and so the
debug `Reason` and the explain trace — is deterministic. Known tradeoff: hosts cannot express
probe priority (gps's `viewer, contributor, …` becomes alphabetical); the deny path pays
all `|grantors|` probes regardless. A priority-ordered model is a follow-up if a measured
host needs it.

### D2 — Roles-kind semantics: `Check`, `LookupResources`, `Unrestricted`

**`rolesvc` gains ONE method**, plain-string arguments like every other `rolesvc` method
(a `PrincipalRef` parameter would be an import cycle):

```go
// HasRoleWhere is HasRole with PROVENANCE: the same Q5 scope rule (exact scope,
// then the global fallback for a scoped query), additionally reporting WHERE
// the grant was found — role.ProvenanceDirect when the exact-scope row matched,
// role.ProvenanceGlobal when the ("","") fallback did, "" when not held. A role
// held at BOTH scopes reports direct (the more specific), so callers are
// deterministic regardless of row order.
func (s *Service) HasRoleWhere(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (held bool, provenance string, err error)
```

**Check** for `(principal, permission, resource{type,id})` — the role engine's own answer;
the composite (D4) decides when it is asked:

1. `req.Validate()` as today (`ReasonInvalidRequest`).
2. `grantors := model.grantors(type, permission)` — nil when the pair is undeclared ⇒
   `{Allowed:false, ReasonDenied, Reason:"no rules defined"}` (the relationship engine's
   wording).
3. For each grantor in sorted order: `HasRoleWhere(principal.Type, principal.ID, role,
   type, id)`. First hit ⇒ `{Allowed:true, ReasonGranted, Reason:"role:<role>@<provenance>"}`
   (`role:viewer@direct`, `role:viewer@global`).
4. Otherwise `{Allowed:false, ReasonDenied, Reason:"no matching role"}`.

Any store error ⇒ `(CheckResult{}, err)` — never an allow. Cost is bounded by the model:
≤ 2·|grantors| probes per check, short-circuiting.

**CheckExplain** rides the same path with a trace collector (the `explain.go` pattern): one
`ExplainStep` per probe. Additive vocabulary — reusing the provenance words the feature
already ships (`role.EffectiveGrant.Provenance()` returns `direct`/`global`/`both`), not a
second vocabulary:

```go
const (
    ExplainKindRole    = "role"   // a grantor probe at the request's scope
    ExplainScopeDirect = "direct" // found at the exact resource scope
    ExplainScopeGlobal = "global" // found as the ("","") global assignment
)
type ExplainStep struct {
    …existing fields…
    // Role and Scope are set for ExplainKindRole steps:
    // the role probed and where it was found (ExplainScopeDirect/Global; "" when
    // not held). Empty for relationship steps.
    Role  string
    Scope string
}
```

Role steps: `Depth: 0`, `Relation: ""`, `ResourceType`/`ResourceID`/`Permission` = the
request's coordinates. `Explanation.Decision` is the FINAL `CheckResult.ReasonCode`.

**CheckBatch** for the roles kind is sequential `Check`s (the relationship engine's
non-optimisable path); the `MaxBatchSize` gate is the composite's (D4). No batch store
query (D6).

**LookupResources** for `(principal, permission, type)` — the role engine's answer:

1. validate as the relationship engine does (`lookup.go:39-47`).
2. `grantors` nil ⇒ `{IDs: []string{}}`.
3. Walk `ListRoleAssignmentsBySubject` to exhaustion (page limit `crud.MaxLimit`,
   cursor-following; cancellation checked before every page). **Every scanned assignment
   row is charged against `MaxGraphStates`** — the existing "work units expanded"
   dimension — so the walk is budgeted, not merely finite: a subject with an adversarial
   assignment count is `ErrEvaluationLimit` (indeterminate), never an unbounded store
   walk. (That exhaustion in production is D6's trigger.) For each assignment whose
   `Role ∈ grantors`: global scope ⇒ return `Unrestricted` immediately; `ResourceType ==
   type` ⇒ add `ResourceID` to the distinct set, charging the running distinct count
   against `MaxLookupResults` (overflow ⇒ `ErrEvaluationLimit`, never a truncated list —
   AZ3-1.3). The walk is not a snapshot: an assignment created mid-walk may or may not
   appear; duplicates fold into the distinct set; `Check` is unaffected.
4. Return `{IDs: sorted distinct}`.

```go
// LookupResult is the enumeration result of LookupResources.
//
// Contract: IDs is ALWAYS a non-nil slice. Unrestricted reports that the
// principal may access EVERY resource of the type because a role that grants
// the permission is held GLOBALLY — in which case IDs is empty and the host
// must skip ID filtering entirely rather than treat the empty slice as "none".
// Only the roles kind produces Unrestricted; the relationship kind is pure
// tuple enumeration and never does.
type LookupResult struct {
    IDs          []string
    Unrestricted bool
}
```

The field is additive because it is **fail-closed under ignorance**: a caller compiled
against v0.2.0 semantics that reads only `IDs` returns an empty page for an unrestricted
principal — restrictive, never permissive. gps's `(nil ids, unrestricted bool)` shape is
rejected: nil-vs-empty as a semantic switch breaks the standing "IDs is ALWAYS non-nil"
contract.

### D3 — Placement: a compositions-tier `decisionsvc`; `authorizersvc` and `rolesvc` stay what they are

- `internal/logic/authorizersvc` — the relationship engine and the decision VOCABULARY
  (`PrincipalRef`, `Resource`, `CheckRequest`, `CheckResult`, `Reason`, `LookupResult`,
  `Explanation`, `ExplainStep`, `EvaluationLimits`, the budget). Changes: `LookupResult.
  Unrestricted`, the `ExplainStep`/`ExplainKindRole`/`ExplainScope` additions, an exported
  `DeclaresPermission(type, permission) bool` on `Service`, and the gate body extracted to a
  package-level builder over two narrow interfaces — `Checker{Check}` and
  `Declarer{DeclaresPermission}` — which `Service` keeps satisfying, so
  `Service.RequirePermission*` STAY and `authorizersvc/middleware_test.go` (calls at
  `:98,151,161,167`) compiles unchanged. One HTTP ladder (401/403/500/503), no drift.
  Package doc stays true; `model.go:1-10`'s "the roles kind has no schema" sentence is
  updated to point at `decisionsvc`.
- `internal/logic/rolesvc` — gains `HasRoleWhere`, nothing else. Its rule ("NEVER imports
  the relationship engine") stays doc-only under this layout, so **T7 adds a one-line grep
  guard** (`guard-authorization-rolesvc-no-engine`: `rolesvc` imports neither
  `authorizersvc` nor `decisionsvc`) to `make guard`.
- `internal/logic/decisionsvc` — NEW, the compositions tier (ARCHITECTURE: "Compositions
  depend downward on multiple domains and own the cross-domain workflow"); imports
  `authorizersvc` (vocabulary + relationship engine) and `rolesvc`; no cycle. Files:
  `role_model.go` (D1: types, validation, compiled sorted copy, `DeclaresPermission`),
  `roles.go` (the role engine over a narrow interface `roleProbe{HasRoleWhere,
  ListRoleAssignmentsBySubject}` satisfied by `*rolesvc.Service` — accept interfaces),
  `composite.go` (D4), `middleware.go` (D5: `Composite.RequirePermission*` over the
  `authorizersvc` builder). Root aliases: `RoleModel`/`RoleTypeDef` = `decisionsvc.*`.
- Root `authorization.Service` gains `decider *decisionsvc.Composite` (nil ⇔ no
  model-bearing kind) and its HTTP-writing stays zero (ARCHITECTURE row 152 "root writes NO
  HTTP; bodies live in `internal/logic/…svc`" holds — `decisionsvc` is a `…svc`).

*Alternative considered and rejected:* the role engine + composite inside `authorizersvc`
(one engine package, two kinds). Cheaper by one package; makes `rolesvc`'s rule
cycle-enforced; but turns the "sealed RELATIONSHIP engine" doc false and puts a composition
inside a domain engine. (An earlier draft claimed the sibling layout had an import cycle;
it does not — `authorizersvc` imports neither.)

### D4 — The composite: dispatch by pair ownership (no union)

The issue sketched "any configured kind allowing ⇒ allowed; `LookupResources` = union
across kinds". No host needs a pair answered by two kinds (gps is roles-only; auth-cms
splits `project` by PERMISSION), and D1 rule 4 forbids it at boot — so the composite is a
**dispatch**, which needs no kind ordering, no batch-subset merge, no union charging. The
union is a strict superset that can be added the day a host declares a pair in both models
(then D1 rule 4 relaxes and this section grows); it is not built now.

Rules:

- **Dispatch.** `Check`: validate the request, then let the pair's owning model answer
  (`DeclaresPermission` on each);
  declared by neither ⇒ `{Allowed:false, ReasonDenied, Reason:"no rules defined"}` from the
  relationship engine when it is wired, else from the role engine. The only kind that runs
  is the owner, so there is no cross-kind availability coupling: a roles-kind store failure
  never touches a relationship-owned pair, and vice versa.
- **`CheckBatch`:** `MaxBatchSize` gate once; zero-length identities preserved literally
  (`CheckBatch(nil)`/`([]…{})` ⇒ `(nil, nil)` without touching a kind;
  `FilterAuthorized` with no IDs ⇒ `(nil, nil)`, as `service.go:109-111,152-154` do today).
  Validate every request before touching either store, then group requests by owning kind,
  index-preserving: the relationship subset goes through the relationship
  `CheckBatch` (its optimised same-shape path survives — never called with an empty
  subset); the roles subset runs sequentially; results merged by index.
- **`LookupResources`:** validate principal/permission/resource type, then return the owning
  kind's lookup verbatim (its own `MaxLookupResults` charging, unchanged). A globally held
  granting role may return `Unrestricted`; there is no cross-kind bypass, union, or halved
  headroom.
- **`CheckExplain`:** the owning kind's steps; `Decision` = the final `ReasonCode`.
- **Single-kind identity.** A relationship-only host's composite is a pass-through: same
  decisions, reasons, traces, zero-length values (T3a pins this with an equivalence test
  over a shared fixture). The composite's resolved `EvaluationLimits` is the SAME
  resolution the relationship engine holds, so the `MaxBatchSize` gate is not doubled in
  effect.
- **Sentinel.** Decision methods on a host with NO model-bearing kind (roles-only without
  a `RoleModel` — gps today) return
  `ErrNoDecisionKind = fmt.Errorf("authorization: no decision-capable kind is configured (set Config.Model for the relationship kind or Config.RoleModel for the roles kind): %w", sdk.ErrInvalidInput)`
  — the `ErrMutationsNotConfigured` precedent (`codes.go:118`: a stable precondition
  refusal, retrying unchanged cannot help, never `ErrForbidden`), so it maps through the
  `web.Error` seam as a wiring fault (500), never a deny. Today those calls return
  `ErrRelationshipsNotConfigured` — the wrong diagnosis once "wire a role model" is the fix.
  This is the ONE error-identity change: `Check`, `CheckBatch`, `CheckExplain`,
  `FilterAuthorized`, `LookupResources` on that wiring. No host in `examples/` or gps-360-go
  references either not-configured sentinel (grep, 2026-08-26); it is named in the upgrade
  note. (Owner option: multi-`%w` wrap `ErrRelationshipsNotConfigured` too for one release
  — Open question 4.)

**`EvaluationLimits` stays on `Config` (top level).** It is a decision-surface budget:
`MaxBatchSize`, `MaxLookupResults`, and (now) `MaxGraphStates` for the assignment walk are
charged on roles-only hosts too; the Through/fan-out dimensions are inert without the
relationship kind. `Limits.Resolve()` runs whenever ANY model-bearing kind is wired (a
negative limit under roles-with-model wiring is now `ErrInvalidLimits`); under
roles-only-without-model it stays ignored-with-note (`TestConstructionOrphanedLimitsUnderRolesOnly`
unchanged). `Service.maxBatchSize` is captured whenever the decider exists (its comment
loses "0 = relationship kind off"); the purge blast-radius consumer is unaffected —
`PurgeResourceAuthorization` is still gated on `s.relationships == nil`
(`relationship_mutations.go:104-106`).

### D5 — Gates in coordinates on every model-bearing host

`Composite.RequirePermission`, `RequirePermissionOn`, `RequirePermissionFixed` are built
over the shared `authorizersvc` builder (D3). The root wrappers' public method set on
`*Service` is UNCHANGED; they delegate to the composite and panic at mount with a new
message when `decider == nil`:
`"authorization: RequirePermission… requires a decision-capable kind (Config.Model or Config.RoleModel); a roles-only host without a role model must not mount it"`.
`mustDeclare(type, permission)` = declared by the relationship schema OR the role model
(exactly one, by D1 rule 4); by neither ⇒ panic at mount (wording unchanged). Request
semantics (401/403/500/503, fail closed, FS9 body) unchanged — they now run the composite
`Check`.

### D6 — No store port change; no store retag (owner may flip)

Everything above runs on the EXISTING `role.Storer` (`HasExactRole` via `rolesvc`,
`ListBySubject`). The optimisation — one `Storer.RolesHeld(subject, type, id)` (rows at
the exact scope ∪ the global scope) and one `Storer.ListBySubjectForResourceType` — is a
port addition: memstore + pgx + turso, a `storetest` contract, and retagging both store
modules (`stores/pgx` was tagged v0.2.0 this morning), plus a SECOND repin for gps-360-go
while its WithSchema adoption is still open. The decisive reason to defer is adoption cost,
not performance: on the existing port a host gets the role model by bumping **one** module.
With the D2 row-scan charge the walk is budgeted, so the port addition is a performance
follow-up, not a correctness gap. **Trigger:** `ErrEvaluationLimit` from the assignment
walk in a real host, or measured gate latency.

### D7 — `Config` shape: one new field, no restructure (owner may flip)

The issue sketches `Config{Relationships: *RelationshipConfig, Roles: *RoleConfig, Policy:
*PolicyConfig}` with a deprecation release. Recommendation: **don't** — add one field:

```go
type Config struct {
    Model  Schema               // relationship kind — unchanged
    Limits EvaluationLimits     // unchanged; charged on any model-bearing kind (D4)
    IDs    cryptids.IDGenerator // unchanged
    // RoleModel is the roles kind's permission model. Unset (no ResourceTypes)
    // = no model: the roles kind answers HasRole and the listings only
    // (today's behaviour) and takes no part in the decision
    // surface. Set requires Repositories.Roles (ErrRoleModelWithoutRoles); it is
    // validated at NewService (ErrInvalidRoleModel, ErrModelConflict).
    RoleModel RoleModel
    Guard  MutationGuard // unchanged
    Audit  AuditSink     // unchanged
}
```

Kind presence already rides `Repositories` (nil = off) and model presence rides the model
value; the restructure buys nothing behavioural and costs a deprecation release plus a
`Config` edit in three hosts. An empty `PolicyConfig` today is a speculative type with no
fields. If per-kind config is ever right, it is a **one-time v1.0 cut** made with the policy
kind's real shape in hand — recorded as such, not as "additive later". Wiring-semantics
asymmetry stays: `Model` ⇔ `Relationships` (both or neither, `ErrModelRequired`);
`RoleModel` ⇒ `Repositories.Roles` (one direction — a roles repo with no model is the
opaque posture and stays valid). If the owner prefers the issue's shape it is a superset
(`RelationshipConfig{Model, IDs}` + `RoleConfig{Model}` + top-level `Limits`, deprecated
pass-throughs, `ErrConfigConflict`); the engine work is identical.

**Naming note (owner question, 2026-08-26):** `Config.Model` is the relationship
model — `RoleModel`'s exact counterpart (same shape, validated at `NewService`, D1 rule 4
checks the pair between them). Only the NAME is asymmetric, because `Model` predates a
second model. A `Model → RelationshipModel` rename is source-breaking (46 in-repo literals
+ auth-cms + coordination-hub) and belongs to the one-time v1.0 `Config` cut recorded
above, not this additive train. Owner may flip: add `RelationshipModel Schema` now with
`Model` as a one-release deprecated pass-through + `ErrConfigConflict`.

### D8 — Assign-time model validation (in scope; owner may cut)

When a `RoleModel` is set, role assignments on every high-integrity path reject a
`(resourceType, role)` pair the model does not declare with `ErrInvalidRoleModel` (wrapping
`sdk.ErrInvalidInput`): a scoped assignment needs `role ∈ ResourceTypes[type].Roles`; a
global assignment needs the role declared in ANY type's `Roles`. This runs as part of the
receipt-absent `SemanticValidator` inside `MutationRepository.Apply`/`ApplyGuarded`, composed
with relationship validation, rather than as a preflight in the typed methods. Therefore
`Service.AssignRole`, `SystemMutator.AssignRole`, and generic
`SystemMutator.Apply(OpRoleAssign)` cannot disagree or bypass it, while an exact replay still
returns its stored receipt after a later model change. `UnassignRole` and every read path
stay opaque, so existing rows need no migration and remain removable. Rationale: gps's
`Legal()`/`RolesGranting` are the only things catching a typo'd role today and this plan
deletes them; without an assign-side
check, `AssignRole(subject, "vewer", "organization", id)` is a permanently silent no-grant
— fail-loud is this feature's posture. Hosts with no model are untouched. Read-side/strict
enforcement over historical rows stays deferred. If cut, the trap is documented verbatim in
the README and named as the #1 follow-up.

## Execution notes (2026-08-26, recorded during T3a/T4)

- **`ErrNoDecisionKind` HTTP status is 400, not 500.** The plan text (§D4, T4) said the
  sentinel "maps through the `web.Error` seam as a wiring fault (500)"; that was a factual
  slip about its own precedent — `ErrMutationsNotConfigured` wraps `sdk.ErrInvalidInput`,
  which `web.ErrFromDomain` maps to 400 (`sdk/foundation/web/errors.go:267`). The ratified
  sentinel shape (Q4: wraps `sdk.ErrInvalidInput` only) is kept, so it lands at 400 exactly
  like its precedent. `codes_test.go:TestNoDecisionKindIsAWiringFaultNotADeny` pins the
  load-bearing properties: never a deny/403, never `ErrUnavailable`/`ErrForbidden`/
  `ErrConflict`, not a wrap of `ErrRelationshipsNotConfigured`, same status as the precedent.
  A literal 500 would need a bespoke mapper case + reason code — owner may flip.
  - **2026-08-26 (v0.4.0, owner ruling — the flip):** `ErrNoDecisionKind` now wraps NO sdk
    kind, so `web.ErrFromDomain`'s default lands it at **500** — a server-side wiring fault,
    consistent with the gates panicking at mount, and deliberately unlike
    `ErrMutationsNotConfigured` (400, an actor-observable precondition). No bespoke mapper
    case was needed. See `.claude/plans/authorization-roles-model-followups.md`.
- `Composite.FilterAuthorized` exists (mirrors `authorizersvc.FilterAuthorized`) so the root
  method stays a thin delegation; `NewComposite` returns nil when no model-bearing kind is
  wired, which is what makes `decider == nil` fall out of construction.

## Compatibility

Minor, predominantly additive; `features/authorization` v0.2.0 → **v0.3.0**. The
`ErrNoDecisionKind` identity change and exported-struct unkeyed-literal caveat are explicit
below.

- New exported symbols: `RoleModel`, `RoleTypeDef`, `Config.RoleModel`,
  `LookupResult.Unrestricted`, `ExplainStep.Role`/`.Scope`, `ExplainKindRole`,
  `ExplainScopeDirect`, `ExplainScopeGlobal`, `ErrInvalidRoleModel`, `ErrModelConflict`,
  `ErrRoleModelWithoutRoles`, `ErrNoDecisionKind`; `Service.DeclaresPermission` is
  internal-only.
- **Relationship-only hosts** (coordination-hub): identical decisions, reasons, traces,
  zero-length values (T3a pins it). `examples/minimal`, `examples/cms` untouched.
- **Roles-only without a model** (gps today): `HasRole` and listings identical; decision
  methods return `ErrNoDecisionKind` instead of `ErrRelationshipsNotConfigured`; gates still
  panic at mount (new message).
- **Both kinds wired, host later sets `Config.RoleModel`** (auth-cms's shape): every
  relationship-owned pair decides exactly as before and role-owned pairs become decidable.
  There is no cross-kind administrative widening. A global role assignment is unrestricted
  only for role-owned pairs whose grantor list explicitly names that role; a new permission
  grants nothing until its model entry says so.
- `Config`, `ExplainStep`, and `LookupResult` gain fields. Keyed literals are unaffected;
  no unkeyed literal of any of the three exists in-repo or in gps-360-go (grep 2026-08-26),
  and `go vet` checks composites in the verify set. As with any exported Go struct field
  addition, an unknown downstream unkeyed literal would need to become keyed; name that
  source-compatibility caveat in the release note rather than claiming otherwise.
- Store modules: no port, DDL, migration, or tag change (D6).
- `Register`'s log line gains `role_model=<bool>` — a bool only, never type/role names
  (policy vocabulary stays out of logs).

## Tasks

Executor model per `[[subagent-model-policy]]`: implementation → Opus; design reviews →
Fable. Each task ends green on `go build ./... && go test ./... && go vet ./...` in
`features/authorization`, plus `make guard` at the root from T3 on.

- **T1 — `RoleModel` + validation** (`decisionsvc/role_model.go`, `_test.go`): types, the
  D1 validation matrix (one test per rule, each asserting `errors.Is(err,
  ErrInvalidRoleModel)` / `ErrModelConflict` and the symbol in the message), presence rule,
  compiled deep copy with sorted grantors, `DeclaresPermission`, immutability test (the
  `immutable_test.go` precedent). Errors: `ErrInvalidRoleModel`, `ErrModelConflict` are
  engine-defined (wrap `sdk.ErrInvalidInput`) and re-exported in `codes.go` beside
  `ErrInvalidLimits` (`ErrInvalidSchema` itself is not root-exported; `ErrInvalidLimits` is
  the precedent); `ErrRoleModelWithoutRoles` and `ErrNoDecisionKind` are construction/
  precondition sentinels in `authorization.go`'s var block beside `ErrModelRequired`;
  the `ExplainKind*`/`ExplainScope*` consts join the existing const block
  (`authorization.go:109-112`).
- **T2 — role engine** (`rolesvc.HasRoleWhere` + test; `decisionsvc/roles.go`, `_test.go`
  over `memstore`, engine over the narrow `roleProbe` interface): `Check` (direct hit,
  global hit, held-at-both ⇒ direct, several roles per subject, undeclared pair, store
  error ⇒ error), deterministic `Reason` across map-order shuffles, `CheckExplain` steps
  (`Kind`, `Role`, `Scope`, `Depth`, `Outcome`), `LookupResources` (sorted+distinct,
  multi-page walk with a small page size, global granting role ⇒ `Unrestricted`, `MaxLookupResults`
  overflow ⇒ `ErrEvaluationLimit` with no partial list, **`MaxGraphStates` row-scan
  exhaustion ⇒ `ErrEvaluationLimit`**, cancellation before a page ⇒ ctx error).
- **T2b — `storetest` roles-decision family** (`storetest/`; the suite lives in the core
  module — no port change, no retag): a `Roles/Decision` family run by all three backends
  when `Repositories.Roles` is wired (the `oracle.go:275` construction pattern, now with
  `Config.RoleModel`): direct grant allows / global grant satisfies the scoped check /
  undeclared pair denies / a global granting role returns `Unrestricted` only for its
  declared pair; and a roles arm of the `Parity/*` bidirectional oracle — every
  `Check`-allow appears in `LookupResources`, every looked-up ID passes `Check`, across a
  multi-page `ListBySubject` walk with a small page size so cursor behaviour is pinned per
  dialect; plus a composed leg on a both-kinds fixture (pair ownership split over one
  type). Run live per dialect at close (README "Store parity" commands; the pgx leg needs
  the C-collation DB, the turso leg `-tags=integration`).
- **T2c — assign-time validation (D8)** (`mutation_service.go` semantic-validator
  composition, root wiring; tests for scoped/global/undeclared/no-model, generic
  `SystemMutator.Apply` parity, and exact replay after the configured model drops the role).
- **T3a — the composite** (`decisionsvc/composite.go`, `_test.go`): dispatch by pair,
  "declared by neither" reason, validate-before-store for `Check`/batch/lookup, batch grouping
  + index merge + zero-length identities, lookup dispatch + `Unrestricted`, explain
  delegation + `Decision`, owner-only error rules (a roles store error cannot affect a
  relationship-owned pair), and the single-kind
  equivalence test (relationship-only fixture: composite vs engine — decisions, reasons,
  traces, nil-vs-empty — byte-equal).
- **T3b — gates** (`authorizersvc` builder extraction over `Checker`/`Declarer`, `Service`
  keeps its three methods and `authorizersvc/middleware_test.go` compiles unchanged;
  `decisionsvc/middleware.go`): legality across both models, the nil-decider panic message,
  401/403/500/503 on a roles-only-with-model host; root `middleware_test.go` asserts the
  public method set on `*Service` is unchanged.
- **T4 — root wiring** (`authorization.go`, `middleware.go`, `codes.go`, root tests):
  `Config.RoleModel`, the construction matrix (`RoleModel` without `Roles` repo ⇒
  `ErrRoleModelWithoutRoles`; invalid ⇒ `ErrInvalidRoleModel`; pair in both ⇒
  `ErrModelConflict`; roles-only + model ⇒ decision surface live; roles-only without model
  ⇒ `ErrNoDecisionKind` on every decision method and mount-time panic on every gate;
  negative `Limits` under roles+model ⇒ `ErrInvalidLimits`), `maxBatchSize` capture,
  `ErrNoDecisionKind` through the `web.Error` mapper as 500 (asserted), `Register` log
  bool.
- **T5 — the real-engine proof** (`features/authorization/roles_gate_test.go`): gps-360-go's
  `TestGuardWithRealEngine` + `TestAuthorizerRefusesNonsenseAtBoot` lifted in shape —
  memstore repositories, assignments through `SystemMutator.AssignRole`
  (`DeriveMutationID`), the D1 gps model, routes mounted with
  `RequirePermissionOn("organization","view","id")` and
  `RequirePermissionFixed("platform","steward","global")` on `web.NewWebHandler`, driven
  with `httptest`: member 204 on org-1 / 403 on org-2, service account likewise, steward 204
  on every modeled GPS gate, no principal 401, nonsense pair panics at mount; `LookupResources` =
  `["org-1"]` for the member and `Unrestricted` for the steward; `CheckExplain` for the
  steward names `role:steward@global`. The steward result comes only from its explicit entry
  in every permission's grantor list; deleting it from one permission makes that permission
  deny and its lookup return no IDs.
- **T5b — the composed proof host** (`examples/auth-cms/cmd/server/{authorization.go,
  demo.go,membership.go}`, `examples/auth-cms/README.md`; in the v0.3.0 gate — owner may
  pull it out, Open question 3): add `Config.RoleModel{ResourceTypes: {"project":
  {Roles: ["auditor"], Permissions: {"audit": ["auditor"]}}}}`
  (`project/view` stays relationship-owned — the pair split D1 rule 4 permits); seed the
  demo auditor role through `SystemMutator.AssignRole`; replace `/demo/audit`'s hand-rolled
  `authorizer.HasRole` gate (`demo.go:101`) with
  `RequirePermissionOn("project","audit",…)` — a real deletion of host code; **keep
  `isPlatformAdmin` and `requireMembership` unchanged** (ARCHITECTURE.md's middleware table
  and the README name them as the relationship-kind recipe; both recipes stay demonstrable
  side by side) with a comment at `membership.go:235` saying universal bypass remains host
  policy while a global role can grant explicitly listed role permissions; add one composed
  assertion in `authorization_test.go` (an
  `auditor` passes `/demo/audit` with no relationship tuple; a `view`-holder without the
  role does not; the existing platform-admin recipe continues to govern relationship-owned
  routes and does not arise from `RoleModel`).
- **T6 — docs**: `features/authorization/README.md` — kinds table row for roles (has a
  model, answers the decision surface); REWORD (not delete) the "No composed Check facade"
  rule at BOTH sites (§Rules of the kinds ~:90, §Non-goals ~:903): "one decision surface
  dispatches each pair to the model that declares it; cross-kind union and universal-role
  bypass are not built (2026-08-26: gps-360-go hand-wrote the whole roles decision
  half and auth-cms splits one type across kinds by permission — demand for a roles engine
  and one surface, not for merging kinds)"; narrow the "No role implication / role catalog /
  role vocabulary" non-goal (~:895) to what stays true (no implication hierarchy; the rim
  stays opaque; D8 is assign-time only); wiring-semantics rows for `RoleModel`; the gate
  section's "requires the relationship kind" → "requires a model-bearing kind"; rewrite
  wiring-page "Stop 4 — a role-gated host route" (~:811, `HasRole` → a role-model gate) and
  "the composed-kinds closure" (~:846, kept only as before/after); the §"`Check` evaluates
  the schema" bypass paragraph gains the precise split — **a globally held role is data and
  grants only the role-owned permissions whose model entries explicitly name it; a universal
  platform-admin/steward bypass remains application composition; D-D fail-closed unchanged**;
  `LookupResult.Unrestricted`; `MaxGraphStates` now also bounds
  the roles assignment walk (limits table). Package docs: `authorization.go` posture-2
  sentence (":19-21"), the `Service` doc (":292-293"), the `Config` doc (":253-256", already
  stale at five fields), `authorizersvc/model.go:1-10`, `domain/role/role.go` ("a role
  model is … deferred" → it exists, lives in `decisionsvc`, the rim stays opaque).
  `ARCHITECTURE.md`: the "host recipe" row (153) gains the model/data split sentence; the
  graduation row (122) is explicitly UNCHANGED (still one producer; consumer seams stay
  closure-shaped; criterion 2 still fails — say so in the plan's Non-goals, done below).
  `RELEASING.md`: "features/authorization — v0.3.0 (next tag): the roles kind's model + one
  decision surface (minor, predominantly additive)" naming the `ErrNoDecisionKind` identity change and its
  exact call set, the absence of any cross-kind universal bypass, D8, and the D6 follow-up.
- **T7 — guard + verify + tag**: add `guard-authorization-rolesvc-no-engine` to the
  Makefile guard list; `make guard`; per-module build/test/vet for `features/authorization`,
  both store modules (pins unchanged, but run — including the live conformance legs of T2b),
  `examples/auth-cms`; tag; cold-resolution check (`GOFLAGS=-mod=mod go list -m …@v0.3.0`
  from a temp module — the 2026-08-26 protocol); close #5 with the tag.

## Adoption (downstream, after the tag)

- **gps-360-go** — delivered as a handoff prompt (the coordination-hub precedent) that
  states the ORDER against gps's still-open `stores/pgx` WithSchema adoption: bump
  `features/authorization` to v0.3.0 and adopt the model in ONE change, independent of the
  store repin (they do not interact). Content: pass the D1 model as `Config.RoleModel`;
  `NewAuthorizer(guard).Param/Fixed` → `components.Service.RequirePermissionOn/Fixed`;
  `guard.AuthorizedIDs` → `Service.LookupResources` (+ `Unrestricted`); `guard.IsSteward`
  is NOT promoted upstream — the `ManageAuthorization` flag at `bootstrap.go:93` remains an
  exact application-owned `HasRole(…, "steward", "", "")` probe, while the other capability
  booleans use `Check`; delete the generic `Allowed`/`AuthorizedIDs` Guard machinery (and the
  Guard entirely if the one exact steward probe is kept as a small host helper), the `grants`
  map and `Legal` in
  `logic/model.go` (keep the name constants), `inbound/middleware/authorization.go` + tests
  (assertions live upstream in T5). `TestModelIsExactlyTheVocabulary` becomes "the passed
  `RoleModel` mentions every constant and lists steward explicitly on every permission the
  product says it holds". **One behaviour change:** an empty path parameter
  answers 500 (resolver error, fail closed) instead of the Guard's 403; if the hub's
  non-enumerating posture matters, pass a host `ResourceResolver` instead of the
  coordinate form.
- **coordination-hub / Segovia**: no change required (relationship-only; identical).

## Non-goals

- The policy kind (README "The policy seam") — unchanged, still deferred.
- Cross-kind UNION of a pair declared by two models — D1 rule 4 forbids it; trigger is the
  first host that needs one.
- A universal `Superuser`/`IsSuperuser` role-model primitive. A globally assigned role grants
  only the declared role-owned permission pairs that name it. Application flags such as
  gps-360-go's `ManageAuthorization`, and any deliberate bypass of relationship-owned or
  future permissions, stay in host composition. Trigger for a generic override seam: a
  second host needs the same policy shape and cannot express it as explicit grants.
- ARCHITECTURE's "authorization check/decision vocabulary — deferred" graduation row does
  NOT move: still one producer; consumer seams (`events.Config.Authorize`,
  `auth.Config.Granter`) stay closure-shaped; criterion 2 still fails.
  `LookupResult.Unrestricted` is flagship API and creates no enumeration-shaped consumer
  seam (the README "No PostfilterLoop" non-goal is unaffected).
- Role-aware guarded mutations. `MutationGuard`/`DecisionView` reads relationship rows
  inside the repository's atomic boundary (`mutation_service.go:109-118`); a role probe
  there would be a detached, non-atomic read. A host wanting a role-gated write composes it
  in its own `AuthorizeMutation`.
- Role-model snapshot/digest APIs (`GetRoleModel`, a digest on receipts, a digest in the
  `Register` line). Trigger: the deferred admin packet, or the first host UI that lists
  "what can role X do".
- Read-side/strict validation of EXISTING assignment rows against the model (D8 is
  assign-time only).
- Store-side role probes (D6) — recorded, not built.

## Consultation (2026-08-26) — what changed from the first draft

- **owner review (after the three consultations):** the feature must not define
  `Superuser`/`IsSuperuser`; universal authority is application policy. gps-360-go's globally
  assigned `steward` is instead an explicit grantor on every current permission it should
  hold, so it remains unrestricted for those pairs but acquires neither relationship-owned
  nor future permissions automatically. Its `ManageAuthorization` steward flag remains a
  host-owned exact role probe. This supersedes the earlier consultation suggestions for a
  cross-kind superuser probe and removes their availability/validation/trace consequences.
- **architecture-steward:** sibling `decisionsvc` is cycle-free and the compositions-tier
  shape (adopted, D3; the first draft's cycle claim was wrong); union semantics exceed
  demand → dispatch by ownership (adopted, D4 — ownership is per PAIR because auth-cms
  splits `project` by permission); README rule reworded not deleted; model-vs-data split
  for global assignments; graduation-row non-goal; error/const placement; citations; the
  `rolesvc` grep guard.
- **product-manager:** the issue's "conformance per kind + composed" had been dropped →
  T2b `storetest` family; the assignment walk must be budgeted → `MaxGraphStates` row
  charge; auth-cms in-train as the composed proof host, `isPlatformAdmin` KEPT
  (ARCHITECTURE cites it); D8 assign-time validation; T3 split; T6 sites enumerated; gps
  handoff ordering. The proposed both-kinds superuser widening and trap documentation were
  superseded by owner review above.
- **lead-backend-engineer:** `HasRoleWhere` takes plain strings (cycle); `HasExactRole` is a
  store method — exact global = `HasRole(…,"","")`; presence = `ResourceTypes`; gate builder over
  `Checker`/`Declarer` so `Service.RequirePermission*` and its tests stay; provenance
  vocabulary `direct`/`global` with named consts; zero-length identities; `Unrestricted`
  fail-closed rationale; `ErrNoDecisionKind` wraps `sdk.ErrInvalidInput`; guarded-mutation
  non-goal; `maxBatchSize` note; gps 403→500 note; sorted-grantor tradeoff; explain field
  values; walk-not-a-snapshot. The proposed superuser-first rule was superseded by owner
  review above. Not adopted: a `LookupResult.Allows(id)` helper (YAGNI until a second host
  asks).

## Open questions for the owner — RULED 2026-08-26 (all recommendations accepted; see Status)

1. **D4 — dispatch by pair ownership (recommended) vs the issue's cross-kind union.**
   Dispatch needs no ordering, no merge, no union charging, and pins a loud
   `ErrModelConflict` at boot; union is a superset that can be added when a host declares a
   pair in both models. Your issue text says union — say if you want it now.
2. **D6 — existing `role.Storer` (recommended)** or add the two probe methods now with a
   store train (`stores/pgx v0.3.0`, `stores/turso v0.2.0`)?
3. **T5b — auth-cms in the v0.3.0 gate (recommended by product; backend would trail it).**
   It is the only both-kinds host and the only exercise of pair-split dispatch outside unit
   tests; its existing `isPlatformAdmin` host recipe remains deliberately independent.
4. **`ErrNoDecisionKind`** — clean identity change wrapping `sdk.ErrInvalidInput`
   (recommended), or additionally multi-`%w` wrap `ErrRelationshipsNotConfigured` for one
   release so an unknown host's `errors.Is` branch cannot flip?
5. **D8 — assign-time model validation in (recommended) or cut** to a documented trap +
   follow-up?
6. **D7 — one `Config.RoleModel` field (recommended)** or the issue's per-kind restructure?

## Execution log (2026-08-26, as built)

- **Rulings:** Q1 dispatch by pair ownership · Q2 existing `role.Storer` (no store train) · Q3 auth-cms in gate · Q4 clean `ErrNoDecisionKind` · Q5 D8 IN · Q6 one `Config.RoleModel` field. Owner naming question answered in §D7 note (`Model` is the relationship model; rename deferred to the v1.0 config cut).
- **Legs:** A (T1+T2) ∥ A′ (T3b builder) → B (T3a, T3b gates, T4, T2c) → C1 (T2b+T5) ∥ C2 (T5b) ∥ T6 → G19 guard (T7) → reviews (lead-backend: ship-with-edits, 8 edits; architecture-steward: aligned-with-edits, 4 edits) → fix leg (all 12 applied).
- **Deviations from plan text, all recorded above:** `ErrNoDecisionKind` is HTTP 400 (§Execution notes); `Composite.FilterAuthorized` exists; role engine unexported inside `decisionsvc`; `decisionsvc` reaches `rolesvc` through its own `roleProbe` port (no import); `Composed` is a top-level storetest family; auth-cms uses `RequirePermissionFixed` (fixed `project:demo`); `/demo/audit` denial body is now the FS9 shape; `Parity` arm naming asymmetry documented in `storetest.go`.
- **Verification:** `features/authorization` 8/8 pkgs (incl. `-race`), `stores/pgx` + `stores/turso` hermetic, `examples/auth-cms` 7/7; `make guard` 19/19 (G19 negative-tested for `"` and backtick imports); gofmt clean. Live: pgx conformance on a throwaway `postgres:17` container — `Parity/RolesCheckLookupOracle`, `Parity/RolesMultiPageWalk` (205 rows, two cursor follows), `Roles/Decision` ×4, `Composed/PairOwnershipDispatch` PASS (run twice: after C1 and after the fix leg); turso `-tags=integration` on the authorized playground — all new families PASS (734 s total); **pre-existing** `TestSchemaProbe` failure on the playground (stale `iam_relationships`/`iam_roles` lack CHECK constraints — DB drift, remedy = drop those two tables + ledger rows and re-migrate; NOT done, owner call). Real behaviour: auth-cms live app — `role_model=true` in the mount log, `/demo/audit` 401 unauthenticated / 403 non-auditor; the 200-as-auditor leg is end-to-end in `TestDemoAuditRouteIsGatedByTheRoleModel` (real cookie login; the app ships no HTTP role-assignment surface).
- **Diff:** 27 modified + 3 new paths, +2147/−274. No `stores/` code change, no port change, go.mod unchanged.
- **Dispatched 2026-08-26 ("go ahead"):** commit 552193e; annotated tag `features/authorization/v0.3.0` pushed with main; cold-resolve OK; #5 closed. **Remaining downstream:** the gps-360-go handoff prompt (§Adoption); turso playground `TestSchemaProbe` drift (owner call).
