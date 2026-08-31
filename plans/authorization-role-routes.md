# authorization: bundled role-administration routes (issue #20) — host gate + assignment-policy hook, receipts as today

**Status: PLANNED 2026-08-31.** Plan C of the three-plan batch; releases together
with Plan B (#19 RequireAnyPermission + #22 LookupResources limit/paging) as ONE
`pockets/authorization/v0.7.0` train (current tag: `pockets/authorization/v0.6.0`).
Additive only — no removals, no store retags.

## Context

The authorization pocket ships the guarded role-mutation lifecycle
(`Service.AssignRole`/`UnassignRole`, receipts, idempotent mutation ids,
`MutationGuard` inside the atomic boundary) but no HTTP for it, so every
flagship-posture host rebuilds the same thin wire — gps-360-go carries ~200
lines at `pockets/auth/inbound/roles.go` (that wire is absent from the local
checkout; this plan works from the issue). Issue #20 applies the #6 precedent
(authentication's `MachineRoutesGate`): the routes come standard, mounted by
`Service.Register` only when the host supplies a gate; absent gate = not
mounted. `pockets/authorization/authorization.go:31-36` already reserves the
`/authorization/*` namespace for exactly this surface, and
`examples/auth-cms/cmd/server/authorization.go:117-120` explicitly defers the
role-assignment surface — this plan closes both.

## Goal

A host that sets `Config.RoleRoutesGate` gets a complete, receipted
role-administration HTTP surface under `/authorization/*` from `Register`, with
its optional `Config.AssignmentPolicy` consulted before every bundled assign —
and a host that sets nothing sees zero behavior change.

## Out of scope

- **No relationship-mutation routes** — role administration only, per the issue.
- **No symmetric UnassignmentPolicy hook** — decided below (D4), documented non-goal.
- **No bundled enumeration of the GLOBAL scope** (`by-resource` with empty
  values) — decided below (D3), documented non-goal; a host writes its own wire.
- **No new sdk symbols** (no `web.Chain`) — sdk is not on this train.
- **No `Mount.Events` emission** — `AuditSink` remains the only observation seam
  for mutation attempts.
- **No views/HTML** — JSON only; the pocket stays view-free (FS1).
- **gps-360-go deleting its hand wire and passing `ValidateAssignment` as the
  policy hook** — downstream/owner, noted only.
- **Plan B's work** (#19, #22) — separate plan; only the shared v0.7.0 tag is
  joint.

## Decisions

The two review findings already flagged to the owner, plus the calls this plan
settles. Locked framing honored: `RoleRoutesGate web.Middleware` (nil = not
mounted) and `AssignmentPolicy func(ctx, AssignRoleCommand) error`, mirroring
`MachineRoutesGate` (#6) including its misconfiguration-error pattern.

### D1 — Namespace: `/authorization/*`, never `/auth/*` (review finding 1)

The issue sketches `POST /auth/roles`, but `/auth/*` belongs to the
authentication pocket and `Service.Register`'s own doc reserves
`/authorization/*` for this surface. Route table (issue calls names
bikesheddable; settled here):

| Method | Path | Service call | Response |
|---|---|---|---|
| POST | `/authorization/roles` | `AssignRole(ctx, actor, cmd)` | 200 `{"receipt":{…}}` |
| POST | `/authorization/roles/unassign` | `UnassignRole(ctx, actor, cmd)` | 200 `{"receipt":{…},"same_role_grant_remains":bool}` |
| GET | `/authorization/roles/by-subject?subject_type=…&subject_id=…` | `ListRoleAssignmentsBySubject` | 200 `crud.Page` of assignments |
| GET | `/authorization/roles/by-resource?resource_type=…&resource_id=…` | `ListRoleAssignmentsByResource` | 200 `crud.Page` of assignments |
| GET | `/authorization/roles/effective?resource_type=…&resource_id=…` | `ListEffectiveRoleGrantsByResource` | 200 `crud.Page` of effective grants |

- **`unassign`, not `revoke`** (lead finding, adopted): the domain verb is
  `OpRoleUnassign`/`UnassignRole`; the API-key precedent says "revoke" because
  its domain verb IS revoke. One vocabulary, no second wire verb.
- **Named list paths, not query-XOR dispatch** (lead finding, adopted): one
  `GET /authorization/roles` dispatching on parameter presence would force the
  host gate to re-parse the query (two parsers, one confused deputy) and an
  empty-string fallthrough (`?resource_type=&resource_id=`) would enumerate
  every GLOBAL assignment. Each list path requires BOTH of its query values
  non-empty → 400 otherwise. All three listings exist on `Service` today
  (`authorization.go:660-690`); `effective` is included because it is the
  enumeration that agrees with `HasRole` — exactly what an administration UI
  needs.
- Every route carries exactly the host gate; the pocket adds no middleware of
  its own (it has no authenticator to add — see D2/D6).

### D2 — Actor derivation: `identity.FromContext`, no new hook (review finding 2)

Bundled writes must supply an `Actor` to `AssignRole`/`UnassignRole`
(`role_mutations.go:76,91`); `Actor` is exactly a concrete `PrincipalRef`
(`mutation_service.go:68`) with no privilege flag. The seam is
`sdk/foundation/identity.FromContext` (`identity.go:108`):

- It is the AV5 platform-wide convention: `WithPrincipal`/`FromContext` live in
  **sdk**, the write site is the credential owner's middleware —
  `pockets/authentication`'s v0.9.0 `RequirePrincipal` posture stashes the
  principal there (`internal/logic/authsvc/context.go`) — but ANY host
  middleware can call `identity.WithPrincipal`, so the seam is host-agnostic
  without the authorization pocket importing authentication. The module graph
  confirms: `pockets/authorization/go.mod` requires **sdk only** (FS1), pockets
  never import each other, and the root package already imports `identity` for
  `PrincipalFrom` (`authorization.go:271`). A `Config.ActorResolver` hook would
  duplicate AV5 with a second, pocket-local convention — rejected (lead
  concurs).
- No principal on the context → 401 (`web.ErrUnauthorized`), never a zero-value
  `Actor` (which `Actor.Validate` would reject later and less legibly). The
  gate is therefore REQUIRED to include an authenticating layer (D6).
- Mechanically: the inbound handler checks `identity.FromContext` for the 401
  and forwards the `(Type, ID)` strings; the root adapter (D5) constructs
  `Actor{PrincipalRef{…}}`.

### D3 — Listing hygiene

- Both query values non-empty on every list path; the global scope
  (`ListRoleAssignmentsByResource("","")` is a legal Service call) is NOT
  reachable through the bundled routes — documented non-goal. A subject's
  global rows still appear in `by-subject` (their `resource_type`/`resource_id`
  are empty in the DTO).
- Pagination via `crud.ParseListQuery` + `crud.ParseOrder` against
  `role.OrderFields`/`DefaultOrder` (both raw listings) and
  `role.EffectiveOrderFields`/`DefaultEffectiveOrder` (effective), copying the
  authentication `parseListRequest` helper shape
  (`internal/inbound/authentication/machine.go:259-273`).
- A non-empty `q` is rejected with a named 400 at the transport ("role listings
  declare no search fields") — never forwarded to 400 confusingly at the store
  edge, never silently dropped.
- New `Config.ListStrategy crud.Strategy` (zero = `StrategyCursor`) feeds the
  parser's `DefaultStrategy`, mirroring `authentication.Config.ListStrategy`.
  Validated at `NewService` whenever non-zero (`ErrInvalidListStrategy`) — an
  invalid value is invalid even when orphaned; a VALID value with no gate is a
  cosmetic orphan, silently ignored (see D7's orphan rule).

### D4 — `AssignmentPolicy` applies to ASSIGN only

Kept per the ratified framing (the lead argued to cut it; not adopted — see
Consultation notes), with its contract sharpened:

- **Scope: assignment only.** This mirrors the pocket's own D8 posture
  verbatim: `Config.RoleModel` governs assignment legality while "unassignment
  and every read path stay opaque" (`authorization.go:326-332`). Revocation
  AUTHORIZATION already flows through `Config.Guard`, which sees
  `OpRoleUnassign` and the scope kind inside the atomic boundary — a host that
  wants revocation policy (gps-360-go's `PolicyMutator` decorated every pass)
  expresses it there, where it is atomic and audited. A symmetric
  `UnassignmentPolicy` is a documented non-goal, revisitable on demand.
- **Contract (documented on the field):** the policy is a bundled-route
  LEGALITY pre-check over the command shape (unknown role names, global-only
  rules, closed scope registries, machine subjects barred) — it is NOT the
  authorization seam and must not read authorization state (a state-dependent
  policy belongs in the `MutationGuard`, which gets a revision-tracked
  `DecisionView`; a route-level state read would be the detached
  check-then-write AZ3-0.5 eliminated). It is consulted only on the bundled
  assign path, before `Service.AssignRole`; hosts driving the Service directly
  are unaffected; its refusals are NOT observed by `AuditSink` (the sink
  records guard outcomes; a policy refusal never reaches the guard) — all three
  properties stated in the field doc.
- **Error mapping:** a refusal should wrap an sdk sentinel (`sdk.ErrForbidden`
  or `sdk.ErrInvalidInput`) or be a `web.NewSafeDomainError` for a custom safe
  sentence; the handler responds through `web.RespondJSONDomainError`
  (FS9 shape), so an unwrapped bare error lands 500 by design — the same
  contract `MutationGuard` documents (`mutation_service.go:102-107`).
- Declared as a named root type so it is documentable:
  `type AssignmentPolicy func(ctx context.Context, cmd AssignRoleCommand) error`;
  `Config.AssignmentPolicy AssignmentPolicy`.

### D5 — The import-cycle seam (lead's blocking finding, adopted)

`Actor`, `AssignRoleCommand`, `UnassignRoleCommand`, `UnassignRoleResult` are
ROOT-package types; the root must import the inbound package to mount, so
inbound can never import the root. Resolution (lead option (a)):

- `internal/inbound/authorization` declares a narrow port over types it CAN
  import (`domain/mutation`, `domain/role`, `sdk/foundation/crud`):

  ```go
  type RoleAdminService interface {
      AssignRole(ctx context.Context, req AssignRoleRequest) (*mutation.Receipt, error)
      UnassignRole(ctx context.Context, req UnassignRoleRequest) (*mutation.Receipt, bool, error) // bool = same_role_grant_remains
      ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error)
      ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error)
      ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error)
  }
  ```

  with inbound-local `AssignRoleRequest`/`UnassignRoleRequest` structs carrying
  actor `(Type, ID)` strings, the command fields, and the optional
  `MutationID`/`ExpectedRevision`.
- The ROOT ships the one unexported adapter (`role_routes.go`,
  `roleRouteAdapter`) — the SINGLE conversion site: builds
  `Actor{PrincipalRef{…}}`, mints `NewMutationID` when the request carries
  none, validates a supplied one, constructs `AssignRoleCommand`/
  `UnassignRoleCommand`, runs `AssignmentPolicy` (assign only), then calls the
  Service's guarded methods. The adapter is unit-tested field-for-field so the
  duplicate command shape cannot drift silently.
- The command vocabulary does NOT move out of the root (lead option (b)
  rejected: churn to `role_mutations.go`/`mutation_service.go` public docs for
  no consumer benefit).

### D6 — The gate is the ENTIRE stack; stays singular `web.Middleware`

Unlike #6, this pocket supplies no authenticator underneath the gate
(credential-free by FS1) and no browser-CSRF layer. The `RoleRoutesGate` field
doc therefore MANDATES: the gate must be the host's complete chain —
authenticator (something that stashes `identity.WithPrincipal`), any
browser-origin/CSRF defense for cookie-credential hosts, then the authorization
decision — and shows the three-line compose closure (there is no `web.Chain`
in sdk, and sdk is not on this train). The field stays a single
`web.Middleware` per the ratified framing and the `MachineRoutesGate`
precedent (#6 recorded the same singular-vs-slice deviation); the lead's push
for `[]web.Middleware` is recorded in Open questions for the owner. Per-path
granularity (read-wide, write-narrow) needs no second field: paths are
distinct, so a host gate may switch on method/path.

### D7 — Misconfiguration matrix and the orphan rule

Analogous to `ErrMachineRoutesGateWithoutRepos`
(`pockets/authentication/authentication.go:868-876`), decided for the roles
subsystem, all at `NewService`:

- `RoleRoutesGate` set, `Repositories.Roles` nil → **`ErrRoleRoutesGateWithoutRoles`**.
- `RoleRoutesGate` set, `Config.Guard` nil → **`ErrRoleRoutesGateWithoutGuard`**
  (every bundled write would deterministically 400 with
  `ErrMutationsNotConfigured` — a deployment the operator must fix, so fail
  construction; `Guard` already implies `Repositories.Mutations` via
  `ErrGuardWithoutMutations`).
- `AssignmentPolicy` set, `RoleRoutesGate` nil → **`ErrAssignmentPolicyWithoutRoutes`**
  (a legality policy that would never run is a silent security no-op — the
  `ErrAuditWithoutGuard` reasoning).
- `ListStrategy` non-zero and not cursor/offset → **`ErrInvalidListStrategy`**.
- `Register` with gate set and `Mount.Router` nil → **`ErrRoleRoutesWithoutRouter`**
  (Register today tolerates a zero Mount; with routes promised, a nil router
  must be loud).
- Roles + Guard wired, gate nil → one **WARN at `Register`** ("bundled
  role-administration routes are NOT mounted — set Config.RoleRoutesGate or
  serve your own routes over the Service methods"), mirroring
  `authentication.go:2065`.

**Orphan rule, stated once in the Config doc:** a security-affecting orphaned
setting fails construction (`AssignmentPolicy` without gate; `Audit` without
`Guard`); a cosmetic orphaned setting is silently ignored (`ListStrategy`
without gate — the MailFrom precedent, AZ3-0.3).

### D8 — DTOs, idempotency, and status mapping

- **Request body** (both writes), mirroring the commands:
  `{"mutation_id"?, "subject_type", "subject_id", "role", "resource_type"?, "resource_id"?, "expected_revision"?}`.
  `mutation_id` optional: absent → the adapter mints `NewMutationID` (each
  request distinct); present → `MutationID.Validate` and the documented retry
  contract (a client that retries with ITS OWN id dedups against the stored
  receipt). Client-supplied ids are kept — retry idempotency is the point of
  the receipts rail — with the squat surface documented: the population behind
  the gate is the host's administrators, ids must be unguessably random, and a
  squatted id yields a 409 payload-mismatch, never a silent overwrite.
  `expected_revision` is exposed (`*uint64` → `*Revision`): compare-and-set is
  part of the lifecycle the routes surface; omitting it would be a silent
  capability cut.
- **Receipt DTO** — explicit wire struct, never `json.Marshal` of
  `mutation.Receipt`: `{"mutation_id","scope_kind","scope_type","scope_id","operation","outcome","revision","replayed","created_at"}`.
  `payload_encoding`/`payload_digest`/`schema_digest` are deliberately OFF the
  v1 wire (lead's leak concern; additive later if a host needs them).
  `same_role_grant_remains` appears top-level in the unassign envelope only.
- **Assignment DTO**: `{"subject_type","subject_id","role","resource_type","resource_id","created_at"}`
  (`role.Assignment`, `domain/role/role.go:63-70`); **effective-grant DTO**:
  `{"subject_type","subject_id","role","direct","global"}` (`role.EffectiveGrant`).
  Pages via the `crud.Page` wire shape (`newPageResponse` pattern).
- **Status mapping:** all five domain outcomes (including `semantic_conflict`,
  `invariant_blocked`, `not_found`) ride **200 + the receipt envelope's
  `outcome` field** — a conflict is an outcome, NEVER an error (mutation
  default #8). ERRORS map through `web.RespondJSONDomainError`/`ErrFromDomain`
  (FS9): guard denial → 403, missing principal → 401, malformed body /
  half-scoped pair (`ErrHalfScopedRoleScope`) / `ErrMutationsNotConfigured` /
  D8-model-illegal role → 400, stale revision & payload-mismatch → their
  existing sdk-wrapped kinds (409 class), unwrapped policy errors → 500.

## Schema / datastore impact

None. No DDL, no migrations, no store-port change; `stores/pgx`,
`stores/turso`, `memstore`, and `storetest` are untouched and the store modules
do **not** retag on this train.

## Module / API impact

- `pockets/authorization/go.mod`: **sdk pin bump `v0.5.0` → `v0.6.0`**
  (required: `crud.ParseListQuery`/`ListQueryOptions` do not exist at v0.5.0 —
  `go.work` hides this locally, so it must be an explicit task with a
  cold-resolution check at tag time). Store modules keep their sdk pins.
- New exported root symbols (all additive): `Config.RoleRoutesGate`,
  `Config.AssignmentPolicy` + `type AssignmentPolicy`, `Config.ListStrategy`,
  `ErrRoleRoutesGateWithoutRoles`, `ErrRoleRoutesGateWithoutGuard`,
  `ErrAssignmentPolicyWithoutRoutes`, `ErrInvalidListStrategy`,
  `ErrRoleRoutesWithoutRouter`.
- `Service.Register` gains behavior (mounts routes when gated); signature
  unchanged. Package doc + Register doc updated: `/authorization/*` is no
  longer "reserved, mounts NO routes" — it mounts the role-administration
  surface when gated and stays otherwise reserved.
- **Release train:** ONE tag `pockets/authorization/v0.7.0` covering this plan
  AND Plan B (#19+#22); minor per RELEASING.md (additive symbols + minimum-sdk
  bump). Tag/cold-verify happen once, at the train cut, not per-plan.

## Generated-artifact impact

None — no `.templ` sources touched.

## Risks

1. **Duplicate command shape at the D5 seam** — the inbound request structs
   re-state the root commands; drift would ship silently. Mitigation: the root
   adapter is the single conversion site with field-for-field unit tests, and
   the root integration tests drive the full path through `Register`.
2. **Cookie-credential hosts with a policy-only gate get CSRF-free
   state-changing POSTs** — the pocket cannot supply a browserSafe layer
   (FS1). Mitigation: D6's mandatory field doc + README section; recorded as
   an owner-visible open question on the gate's shape.
3. **The sdk pin bump ripples** to every host consuming
   `pockets/authorization@v0.7.0` (minimum sdk becomes v0.6.0). Mitigation:
   called out in the train's upgrade notes; authentication already pins v0.6.0
   so flagship hosts are there.

## Tasks

### task-1: Root seams — sdk pin, Config fields, misconfiguration matrix

- **depends_on:** []
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/go.mod`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/go.sum`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/authorization.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/role_routes.go` (new — `AssignmentPolicy` type; error vars may live here or with the siblings in `authorization.go`, follow the file's vars-at-top convention)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/authorization_test.go` (or a new `role_routes_test.go` for the matrix cases)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Bump the sdk require to v0.6.0. Add `Config.RoleRoutesGate
  web.Middleware`, `Config.AssignmentPolicy AssignmentPolicy`,
  `Config.ListStrategy crud.Strategy` with the D6 field doc (gate = the entire
  chain: authenticator + CSRF + decision), the D4 policy contract doc, and the
  D7 orphan rule in the Config doc. Add the five error vars and the `NewService`
  validation per D7; capture the new fields on `Service`. G12: table-driven
  `NewService` tests for every matrix row (gate w/o roles, gate w/o guard,
  policy w/o gate, bad ListStrategy, and the still-valid postures).

### task-2: Inbound package — port, handlers, DTOs, listings

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/internal/inbound/authorization/routes.go` (new — `RoleAdminService` port, `Deps{Service, Gate, ListStrategy}`, `Mount`)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/internal/inbound/authorization/roles.go` (new — handlers, request/response DTOs, strict-JSON-body + list-parse helpers per the authentication `machine.go`/`helpers` pattern)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/internal/inbound/authorization/roles_test.go` (new)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization && go build ./... && go test ./... && go vet ./...` then `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard` (confirm `guard-pocket-transport-sdk-web` and `guard-pocket-isolation` actually match the new `internal/inbound/` path — check the Makefile expressions, don't assume)
- **description:** Build the D1 route surface over the D5 port with a stub
  `RoleAdminService`. Handlers: derive the principal via `identity.FromContext`
  (absent → 401), strict bounded JSON bodies, both-values-non-empty on every
  list path, named 400 for `q`, D8 DTOs and outcome-on-200 mapping, all errors
  through `web.RespondJSONDomainError`. G12 tests in-package: 401 without
  principal, 400 shapes (half query pairs, `q`, bad body, bad `mutation_id`),
  DTO wire shapes (receipt envelope, unassign `same_role_grant_remains`
  top-level only), order/limit parsing against `role.OrderFields` and
  `EffectiveOrderFields`, and that the gate middleware wraps every route
  (counting-gate pattern from authentication's `machine_gate_test.go`).

### task-3: Root adapter + Register mounting + end-to-end proof

- **depends_on:** [task-2]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/role_routes.go` (adapter `roleRouteAdapter` implementing the inbound port; MutationID mint/validate; AssignmentPolicy invocation on assign)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/authorization.go` (`Register`: mount when gated via aliased inbound import, `ErrRoleRoutesWithoutRouter`, the not-mounted WARN, `role_routes` field on the registered log line, package/Register doc updates)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/role_routes_test.go` (new — root integration tests over `memstore`, distinct names from the existing `roles_gate_test.go`)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization && go build ./... && go test ./... && go vet ./...` then `make guard` at the repo root
- **description:** Implement the single conversion site (D5) and wire `Register`
  per D7. G12 integration tests through a real `Register` + memstore host:
  routes 404 when the gate is nil (and the WARN fires); mounted routes refuse
  through a denying gate (403 with the FS9 body); a passing gate + stashed
  principal assigns and returns a receipt, replays the same `mutation_id` with
  `replayed:true`, unassigns with an honest `same_role_grant_remains`;
  `AssignmentPolicy` refusal (wrapping `sdk.ErrForbidden`) → 403 and never
  reaches the store; policy NOT consulted on unassign; missing principal → 401;
  adapter unit tests prove field-for-field command construction, server-minted
  vs client-supplied `mutation_id`, and `expected_revision` passthrough.

### task-4: README + pocket docs

- **depends_on:** [task-3]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization/README.md`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/pockets/authorization && go build ./... && go test ./...` (docs task; keep the module green)
- **description:** New section "Bundled role-administration routes" between the
  mutation-lifecycle and raw-vs-effective sections: the D1 route table, the D6
  gate-chain mandate with the compose example and the cookie/CSRF warning, the
  D4 AssignmentPolicy contract (legality-only, assign-only, not audited, Guard
  is the authorization seam), the D7 matrix + orphan rule, the D8 wire shapes
  and outcome-vs-error mapping, and the non-goals (no global-scope enumeration,
  no unassignment policy hook, no events emission). Update the "Wiring
  semantics — nil vs required" table and the anatomy/socket section for the new
  Config fields.

### task-5: Proof host — examples/auth-cms mounts the gated surface

- **depends_on:** [task-3]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/authorization.go` (set the gate; close the AZADM deferral comment at :117-120)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/main.go` (ordering note below)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/role_routes_proof_test.go` (new, or extend an existing boot fixture test)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/README.md` (curl walkthrough)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms && go build ./... && go test ./... && go vet ./...` — **plus run-and-look:** `go run ./cmd/server` per the auth-cms README, sign in as the seeded admin, `POST /authorization/roles` a demo-role grant (200 + receipt), replay the same `mutation_id` (`replayed:true`), `GET /authorization/roles/by-subject` shows it, `POST /authorization/roles/unassign` clears it; as a non-admin every route 403s with the FS9 `permission_denied` body; with the gate line commented out all five 404 and the boot WARN appears
- **description:** Wire `Config.RoleRoutesGate` as the D6 chain: the host's
  authentication middleware composed with
  `authorizer.RequirePermissionFixed(platformResourceType, "admin", platformResourceID)`
  (the `MachineRoutesGate` line at `main.go:269` is the model). **Known
  circularity:** the authorizer is built and registered (`main.go:217-220`)
  before `authSvc` exists (`main.go:304`), and the gate needs `authSvc`
  middleware — resolve with a request-time indirection (a `web.Middleware`
  variable the closure dereferences per request, assigned right after `authSvc`
  is built, before serving) or by reordering construction; either is
  acceptable, comment whichever is chosen. This is the only place the real FS9
  error bodies and the full chain are provable (the pocket's own tests use stub
  gates — the #6 precedent).

### task-6: Full-repo verify + train note

- **depends_on:** [task-4, task-5]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.claude/plans/authorization-role-routes.md` (status flip + execution notes)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make check` (all 21 guards + every module + generation-drift check)
- **description:** Run the full check matrix; record execution notes in this
  plan. Do NOT tag: `pockets/authorization/v0.7.0` is cut once, jointly with
  Plan B (#19+#22), by the owner — at that cut, cold-resolve the tag from a
  throwaway module (`GOFLAGS=-mod=mod go list -m
  github.com/gopernicus/gopernicus/pockets/authorization@v0.7.0`, then build a
  file setting `Config.RoleRoutesGate`) and confirm the sdk v0.6.0 minimum
  resolves. Note in the train's upgrade notes: gps-360-go deletes
  `pockets/auth/inbound/roles.go` and passes `ValidateAssignment` as
  `AssignmentPolicy` (downstream/owner, not planned here).

## Sequencing

1 → 2 → 3 strictly (pin+seams, then the transport against a stub, then the
adapter closes the loop). 4 and 5 both depend on 3 and may run in either order;
6 last. Tagging is outside this plan (joint train with Plan B).

## Consultation notes

`lead-backend-engineer` reviewed the sketch (2026-08-31). Adopted: the D5
import-cycle seam (blocking — root types unreachable from inbound), named list
paths over query-XOR with non-empty-values enforcement and the global-scope
enumeration cut (D1/D3), `/roles/unassign` over `/roles/revoke` (D1), the
explicit sdk v0.6.0 pin bump task with cold-resolution (Module impact, task-1),
explicit receipt DTO with digests off the v1 wire (D8), `expected_revision`
exposed (D8), the stated orphan rule + `ListStrategy` validation (D7), the
`q`-rejection and guard-path-coverage checks (D3, task-2), and the auth-cms
proof host closing the AZADM deferral (task-5). **Not adopted:** cutting
`AssignmentPolicy` (owner-ratified; kept with the D4 legality-only contract
that answers the lead's atomicity/audit objections), moving the command
vocabulary into `internal/logic` (churn; the adapter is cheaper),
server-mint-only `mutation_id` (client retry idempotency is the rail's point;
griefing surface documented instead), and `[]web.Middleware` for the gate
(ratified singular; escalated below instead of silently changed).

## Open questions

1. **Gate shape — singular `web.Middleware` vs `[]web.Middleware` chain.** The
   ratified framing and the `MachineRoutesGate` precedent say singular; the
   backend lead pushed for a slice because this pocket, unlike authentication,
   supplies no authenticator or CSRF layer beneath the gate, so a host that
   sets a policy-only gate over cookie credentials ships unprotected POSTs.
   Plan proceeds singular with the D6 doc mandate; flagging for the owner —
   a slice would be additive later without breaking v0.7.0 adopters.
2. **Bundled global-scope enumeration** ("list every global admin") is cut as a
   non-goal (D3). If the owner wants it standard, it should be its own gated
   route (e.g. `GET /authorization/roles/global`) in a follow-up, not an
   empty-string fallthrough.

## Recommended reviews

- **product-manager** — route names, DTO field names, and the
  assignment-only policy scope are host-facing API.
- **lead-backend-engineer** — post-hoc on task-3's adapter (they flagged the
  seam; they should see its resolution).
- **platform-sre** — the D7 fail-at-construction matrix and the not-mounted
  WARN are operator surface.
- **architecture-steward** — D2 (identity-context over a resolver hook) and D6
  (gate carries the whole identity stack) touch the cross-pocket seam rules.

## Notes

- The existing `roles_gate_test.go` at the pocket root tests the
  `RequirePermission` gates, not these routes — new root tests use distinct
  fixture names.
- Auth naming discipline throughout: authentication/authorization,
  authenticator/authorizer — never the abbreviated forms.
- gps-360-go's hand wire (`pockets/auth/inbound/roles.go`) was not readable
  from this checkout; the plan derives the surface from the issue, the #6
  precedent, and the pocket's own Service methods.

## Execution record

**EXECUTED 2026-08-31**, branch `authorization-role-routes`, stacked on Plan B's
`authorization-gates-and-lookup` (b7d13d7..e8a2c9b). Not tagged, not pushed —
`pockets/authorization/v0.7.0` is the joint cut with Plan B, by the owner.

| task | commit | note |
|---|---|---|
| task-1 | `8408f31` | Config fields, AssignmentPolicy, five sentinels, sdk pin → v0.6.0 |
| task-2 | `e8fe8c5` | `internal/inbound/authorization`: port, handlers, DTOs, listings |
| task-3 | `bd3cbfe` | `roleRouteAdapter` + gated `Register` + memstore e2e |
| task-4 | `528a65e` | pocket README |
| task-5 | `1afc5f4` | auth-cms gate + `role_routes_proof_test.go` + README |
| task-6 | — | `make check` green; plan file appended by the session |
| extra | `6647ae8` | `go.sum` populated; cold resolution verified early |
| review | `4f3b3d4` | lead-review edits: `Deps.RespondError` stable wire codes; `MaxBatchSize` third-meaning doc; auth-cms boot assertion; honest CSRF docs; `LookupResourcesIn` doc drift; `slices.Clip` |

**Verification.** `go build/vet/test` green in `pockets/authorization` after every
task; `make guard` (21) green after every task; `examples/auth-cms`
`build/vet/test` green (the known `TestAdminResendReportsRealTargetState` flake
did not fire); `make check` exit 0, "all checks passed".

**Cold resolution done early.** `pockets/authorization/go.sum` was EMPTY — `go.work`
supplied the local sdk, so the hashes were never written, and a consumer of the
tag would have failed verification. Populated and proven: `GOWORK=off go
build/vet/test ./...` passes against the released sdk v0.6.0. The v0.7.0 cut
still re-verifies from a throwaway module.

**Run-and-look.** Real server, real curl: anonymous → 401 `unauthenticated` on all
five; registered/verified/logged-in non-admin → 403 `permission_denied` on all
five. The authorized 200 path is NOT hand-drivable on this host — `platform:main
#admin` is boot-seeded for the synthetic `user:demo-owner`, so a fresh browser
account is a 403 by design. `TestRoleRoutesPlatformAdminDrivesTheLifecycle`
drives assign / replay / list / effective / unassign over real HTTP with a real
session instead, and the README says so plainly.

**Decisions honored as ratified.** D6 singular `web.Middleware` (owner ruling
2026-08-31); D4 assign-only, no unassign hook; sdk pinned to the RELEASED v0.6.0,
with v0.7.0 left to the train.

**Deviations.**

1. **auth-cms's gate composes two layers, not three.** D6's cookie/CSRF layer is
   omitted because `pockets/authentication` exports no browser-safe middleware and
   the gate's shape is open question 1 — resolving it in the host would pre-empt
   the ruling. Flagged at the gate closure, in the pocket README, and in the
   auth-cms README. **This makes open question 1 load-bearing in the flagship
   example**: auth-cms ships cookie-authenticated `/authorization/*` POSTs with no
   CSRF defense today.
2. **`go.sum` was an unplanned but necessary commit** (see above).
3. **`newAuthorization()` gained a parameter** (`newAuthorization(gate
   web.Middleware)`); the four existing test call sites pass `nil`. The known
   circularity uses the plan's request-time-indirection option:
   `deferredMiddleware` over an `atomic.Pointer`, which fails CLOSED with 500 when
   unassigned (pinned by `TestDeferredMiddlewareFailsClosed`).
4. **New boot WARN for existing hosts.** Any host with the roles kind + a `Guard`
   and no gate now logs the not-mounted WARN at `Register`. Intended by D7, but a
   visible upgrade-time change worth a line in the train's upgrade notes.

**Pre-existing, untouched:** `examples/auth-cms/internal/authpages/authpages_test.go`
fails `gofmt -l` (last modified in `aaaf5c8`).

**Downstream, unchanged:** gps-360-go deletes `pockets/auth/inbound/roles.go` and
passes `ValidateAssignment` as `Config.AssignmentPolicy` — owner, at adoption.
