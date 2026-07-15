# features/authorization — the IAM domain: independently wireable kinds

A pluggable, datastore-free authorization feature: an IAM domain offering
multiple KINDS of authorization — **relationships** (a hardened ReBAC engine:
schema-driven permission checks, exact-userset group expansion,
through-traversal) and **roles** (minimal opaque-string role assignments, scoped
or global) — plus a named, deferred **policy** seam. ReBAC is one kind, not the
feature's identity. This is the **v3 correctness kernel**: exact userset
semantics, an immutable compiled schema, bounded/cancellation-aware evaluation,
and a repository-atomic, guarded, idempotent mutation lifecycle. Design of
record: `.claude/plans/roadmap/auth-v2-feature-design.md` (as amended by the
2026-07-08 multi-kind owner direction), executed via `.claude/plans/authorization-v1/`
then hardened via `.claude/plans/authorizationv3/`.

**A host can wire this feature safely from this README alone** — it does not
need to read `internal/` code or the plan. If a claim here disagrees with the
code, the code wins; report the mismatch.

## The three postures — decide this first (§2.1)

Authorization in gopernicus is **supported, never required**. All three
postures are first-class; nothing else in this README matters until a
host knows its row:

| posture | what the host does | modules pulled | migrations |
|---|---|---|---|
| **1 — none** | No authorization checks. Consumer seams stay nil (deny-by-absence closes the gated surfaces). | none | none |
| **2 — host-authored** (the middle posture) | Satisfies any Check-shaped seam (`events.Config.Authorize`, `auth.Config.Granter`, its own gates) with a **plain closure over its own data**. | none — no IAM module in the graph | none |
| **3 — flagship** | Mounts `features/authorization` with any combination of its kinds wired. | this module (+ one `stores/*` module in production; `memstore` for zero-infra hosts) | source `"authorization"`, wholesale |

**Consumer seams are Check-only (AV2).** The seams other features expose
(`func(ctx, principal, resourceType, resourceID) (bool, error)` and
kin) accept ANY implementation — that is what makes posture 2 real.
Everything on `Service` beyond boolean checks (enumeration, the guarded
mutation lifecycle, role listings, the model DSL) is **flagship-specific API,
never a cross-feature seam**. Graduation trigger (recorded, not cashed): the
day two features need the identical authorize vocabulary, an `sdk` port is
designed; until then there is deliberately no `sdk/authorization` — and the
ARCHITECTURE.md protocol table records authorization's check/decision
vocabulary as *deferred* from sdk graduation (fails criterion 2), even after
v3 settled its semantics.

Living/recorded artifacts, one per posture: `examples/minimal` (wires
nothing — posture 1); the middle-posture ownership-closure artifact — posture
2; the `examples/auth-cms` HEAD host — posture 3, both kinds, guarded, with a
separately held trusted `SystemMutator`.

## The kinds

| kind | expresses | `Repositories` field | table | nil semantics |
|---|---|---|---|---|
| **relationships** | ReBAC tuples + a schema: who relates to what, and which permissions those relations grant (exact-userset group expansion, `Through` traversal; `Check` is pure schema evaluation — platform-admin/self-access are host recipes) | `.Relationships` (+ `.Mutations` for writes) | `iam_relationships` | nil ⇒ kind OFF structurally; every read method returns `ErrRelationshipsNotConfigured` |
| **roles** | opaque-string role grants — `(subject, role)` pairs, resource-scoped or global | `.Roles` (+ `.Mutations` for writes) | `iam_roles` | nil ⇒ kind OFF; every method returns `ErrRolesNotConfigured` |
| **policy** (deferred) | attribute/condition-shaped rules | (future) `.Policies` | (future) `iam_policies` | a designed, named seam — see "The policy seam" below |

**Relationship-kind `Service` methods.** Reads/enumeration: `Check`,
`CheckExplain`, `CheckBatch`, `FilterAuthorized`, `LookupResources`,
`ValidateRelation(s)`, `GetSchema`, `SchemaDigest`, `GetPermissionsForRelation`,
`GetRelationTargets`, `ListRelationshipsBySubject`/`ByResource`. Guarded
actor-facing writes (require `Config.Guard` + `Repositories.Mutations`):
`GrantRelationship`, `RevokeRelationship`, `ReplaceRelationship`,
`PurgeResourceAuthorization`.

**Roles-kind `Service` methods.** Reads: `HasRole`,
`ListRoleAssignmentsBySubject`, `ListRoleAssignmentsByResource` (raw),
`ListEffectiveRoleGrantsByResource` (effective). Guarded actor-facing writes:
`AssignRole`, `UnassignRole`.

**There are no raw create/delete/assign methods on `Service`.** v3 removed the
v1 `CreateRelationships`/`DeleteRelationship`/`DeleteResourceRelationships`/
`DeleteByResourceAndSubject`/`RemoveMember`/raw `AssignRole`/`UnassignRole`
(pre-tag breaking policy, AZ3-3.4). Ordinary host code writes only through the
guarded typed commands above or through the separately held `SystemMutator`
(see "The mutation lifecycle"). The raw port methods survive on the
`relationship.Storer`/`role.Storer` ports for store conformance and migrations
only, never on the feature's driving surface.

Rules of the kinds:

- **Independently wireable.** A host wires either kind, both, or a
  roles-only bundle with no model. Zero kinds is a loud
  `ErrNoKindConfigured` at construction.
- **Port-optional, schema-wholesale** (the §2.1 bounding rule applied
  intra-feature): an adopting host scaffolds ALL `iam_*` tables
  inert-but-present regardless of which kinds it wires. **A roles-only
  host still applies the FULL `"authorization"` migration source** (all four
  files, `iam_relationships` included), and both store boot probes expect all
  four tables. Source-level schema optionality is the feature boundary's job,
  never a kind's.
- **No composed Check facade.** A unified check consulting multiple
  kinds is exactly the speculative unification this feature avoids — a
  host composes kinds in its own closure (see the labeled snippet in the
  wiring page).
- **Terminology:** a KIND is a nil-safe port family WITHIN this one
  feature module — never a module, and unrelated to ARCHITECTURE.md's
  R6 "Kinds of module" taxonomy vocabulary.

## Exact userset semantics — the v3 correctness core (member vs admin)

A stored relationship subject is an exact `SubjectRef{Type, ID, Relation}`. An
empty `Relation` names a **concrete** subject (`user:u1`, or the group object
`group:eng` itself); a non-empty `Relation` names the exact **userset**
`Type:ID#Relation` (`group:eng#member`, `group:eng#admin`). These are distinct
in validation, storage, direct checks, batch checks, and lookup:

- `group:eng`, `group:eng#member`, and `group:eng#admin` are **three different
  subjects** and never compare equal. A grant to `group:eng#admin` authorizes
  only the group's admins — it does **not** authorize an ordinary
  `group:eng#member`.
- A concrete `group:eng` grant (empty relation) names the group **object**; it
  does not reach the group's members. Member reach requires a userset grant
  (`group:eng#member`) or a `Through` navigation the schema declares.
- Nested userset membership traverses where the schema allows it (a userset may
  itself contain another exact userset). v3 defines **no** userset rewrite
  operators (union/intersection/exclusion, computed/tuple-to-userset) and **no**
  userset-valued decision request.

This is the single most consequential v1→v3 change: v1 hard-coded
`relation = 'member'` and ignored the stored `subject_relation`, so
`group:eng#admin` behaved like `#member` and a concrete `group:eng` reached
members. v3 evaluates the exact stored relation across memory, pgx, and turso.
See "UPGRADE NOTE" for the access-fate assessment a live adopter runs.

**Decision callers are concrete principals only.** `Check`, `CheckBatch`,
`FilterAuthorized`, and `LookupResources` take a `PrincipalRef{Type, ID}` — a
concrete caller. There is **no** field on a decision request that can carry a
userset relation: supplying a userset at a decision boundary is structurally
impossible, never silently ignored. `PrincipalRef` and `SubjectRef` are
intentionally different types; `authorization.PrincipalFrom(identity.Principal)`
maps a resolved platform principal onto a decision request in one call.

### Self-referential hierarchies (relationships)

A self-referential `Through` expresses a parent tree in schema:
`space.view = AnyOf(Direct("viewer"), Through("parent","view"))` (relation
`parent` targeting `space` itself) flows `view` access down every descendant
of a granted node. `NewService` accepts this shape; genuinely non-terminating
shapes — mutual cross-type cycles (`a.x -> b.x -> a.x`), cross-permission
self-type chains (`space.view -> space.admin -> space.view`), and unsatisfiable
self-only rules (a permission whose every `AnyOf` check is a self-`Through`) —
stay rejected loudly at construction.

**Check/Lookup parity (D1(c) closed, AZ3-1.4).** `Check` walks the parent chain
hop-by-hop, so it honors a node reachable through ANY grant. `LookupResources`
matches it exactly: its self-referential branch seeds the descendant walk from
EVERY root the permission grants — direct grants AND roots derived through a
non-self `Through` (e.g. `Through("org","view")`) — so a grandchild `Check`
allows is enumerated too. The `storetest` bidirectional oracle proves both
directions (every `Check`-allow appears in `LookupResources`; every looked-up ID
passes `Check`) across every dialect. `LookupResources` output is sorted with
each ID exactly once, and limit exhaustion is `ErrEvaluationLimit`, never a
partial list.

## The immutable compiled schema — digest and validation failures

The model is **registered data, zero migrations** (relationship kind only): a
host declares its schema in `main` with `NewSchema`/`ResourceSchema` — resource
types, relations with `AllowedSubjects`, permissions built from
`Direct`/`Through`/`AnyOf` — and changing it is a redeploy, not a migration.

`NewService` **compiles the schema once** and holds an immutable projection:

- The compiler deep-copies the caller's source maps/slices, so a host mutating
  its schema map after construction cannot alter a live decision or race the
  engine. `GetSchema()` returns a `SchemaSnapshot` (a deep, read-only copy
  sharing no memory with the runtime policy); the internal compiled schema is
  never returned.
- `SchemaDigest()` is a stable SHA-256 over a versioned canonical encoding
  (`MutationEncodingVersion` has the mutation analogue; the schema encoding
  version is `gopernicus.authorization.schema/1`). Equivalent schemas — the same
  policy declared in a different map iteration or contributor order — yield an
  **identical digest**; any policy change yields a different one. Each mutation
  receipt records the digest that governed it.

**Validation failures — all loud at `NewService`** (the schema compiler wraps
`sdk.ErrInvalidInput`; aggregated errors are sorted deterministically):

- empty resource/relation/permission names or empty rules;
- duplicate declarations after composition/merge;
- an ambiguous check (both `Direct` and `Through`, or neither);
- an unknown direct relation or unknown userset relation;
- a `Through` permission missing from **any** possible resource target
  (mixed-target partials are rejected, not treated as "exists on one target");
- an `AllowedSubjects{Type, Relation}` whose non-empty `Relation` does not exist
  on the referenced type or is not meaningful as a userset;
- a relation used by `Through` that allows a userset target (navigational
  relations point at concrete resource subjects only);
- a relation with zero allowed subjects (unsatisfiable declaration);
- genuine cycles and globally unsatisfiable permission graphs (the sanctioned
  self-hierarchy shape is preserved).

An accepted schema is a fixed policy artifact identified by one stable digest.

## Evaluation limits, indeterminate errors, and fail-closed guidance

`Config.Limits` is a resolved semantic `EvaluationLimits` budget, charged per
decision/enumeration and shared across nested checks. Fields and their
zero-value defaults (re-exported as `authorization.DefaultMax*` consts):

| field | bounds | default |
|---|---|---|
| `MaxThroughDepth` | navigational `Through` recursion (Through hops from 0; `depth > MaxThroughDepth` exhausts) | 10 |
| `MaxGraphStates` | distinct `(resource, permission)` states expanded (diamond-deduped) | 10000 |
| `MaxRelationTargets` | per-hop relation fan-out / expanded targets | 1000 |
| `MaxBatchSize` | checks accepted in one `CheckBatch`/`FilterAuthorized` (also bounds a purge's affected rows) | 1000 |
| `MaxLookupResults` | resource IDs one `LookupResources` returns (the store fetches max+1 so overflow is distinguishable) | 1000 |

Rules:

- A **zero** field selects the default; a **negative** field fails `NewService`
  with `ErrInvalidLimits` (a construction error wrapping `sdk.ErrInvalidInput`).
  Zero never means unlimited; there is no unlimited mode.
- **Exhaustion is indeterminate, never a deny or a truncated list.** Any budget
  dimension hitting its ceiling returns `ErrEvaluationLimit` (wrapping
  `sdk.ErrUnavailable`, HTTP 503 — never a new error kind, never `ErrConflict`).
  `LookupResources` never returns a partial slice as complete.
- A decision is **allow, deny, or indeterminate error**. Invalid input,
  cancellation, infrastructure failure, and limit exhaustion never masquerade
  as an ordinary deny or a complete partial list. The engine checks context
  cancellation before recursion and before every store call.

**Fail-closed caller guidance (load-bearing).** `allowed, _ := authorizer.Check(...)`
is a silent fail-OPEN — an engine error (store down, unwired kind, budget
exhausted) reads as a decision. **Always propagate the error and fail closed:**

```go
res, err := authorizer.Check(ctx, req)
if err != nil {
    return false, err // fail CLOSED — never `allowed, _ :=`
}
return res.Allowed, nil
```

The shipped `RequirePermission` middleware and all consumer closures follow
this: engine or resolver error → 500 (or 503 for `ErrEvaluationLimit`), no
principal → 401, `!Allowed` → 403. Stable reason codes
(`ReasonGranted`/`ReasonDenied`/`ReasonEvaluationLimit`/`ReasonStaleRevision`/…)
are frozen wire codes a host, audit sink, or explain trace can switch on;
`CheckExplain` returns an opt-in bounded `Explanation` that rides the same
evaluation path and budget (it cannot create a separate, more permissive
evaluator, cannot change the decision, and is never auto-logged).

## The mutation lifecycle — actors, guard, SystemMutator, receipts

Every write is one atomic command with a `MutationID`, one mutation scope, an
optional expected revision, and an explicit domain outcome. There are two write
capabilities, held apart by construction.

### Construction — the `Components` bundle

`NewService` returns a **`Components{Service, SystemMutator}`** bundle (not a
bare `*Service`):

```go
comps, err := authorization.NewService(repos, cfg)
// comps.Service       — the host-facing decision/list/guarded-mutation surface
// comps.SystemMutator  — the separately held, trusted, actor-free capability
```

`Service` cannot recover `SystemMutator`; the composition root hands the trusted
capability only to code that legitimately needs it (bootstrap, migration,
invitation acceptance, resource teardown, test fixtures). HTTP handlers receive
`Components.Service` only.

Construction matrix (all loud at `NewService`):

| condition | result |
|---|---|
| both `Repositories` kind fields nil | `ErrNoKindConfigured` |
| `Relationships` set XOR `Config.Model` set | `ErrModelRequired` |
| `Config.Guard` set, `Repositories.Mutations` nil | `ErrGuardWithoutMutations` (a guard has no atomic write path) |
| `Config.Audit` set, `Config.Guard` nil | `ErrAuditWithoutGuard` (nothing to observe) |
| nil `Config.Guard` (the READ-ONLY posture) | construction succeeds; every actor-facing mutation fails closed with `ErrMutationsNotConfigured`; decision/list APIs and `SystemMutator` remain available |
| negative `Config.Limits` field (relationship kind wired) | `ErrInvalidLimits` |

There is **no default allow guard**: the absence of a guard closes the
actor-facing write path, it never opens it.

### Actor-facing (guarded) writes

An untrusted write always carries a non-empty concrete `Actor` (an
`Actor{PrincipalRef}` — there is no `system` kind a caller can construct) and
passes the host `MutationGuard`:

```go
type MutationGuard interface {
    AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error
}
```

The guard returns nil to ALLOW or a stable denial/error (typically wrapping
`sdk.ErrForbidden`) to reject. **A guard that depends on authorization DATA must
read it only through `view`** — the dependency-tracking `DecisionView` the
repository supplies inside the atomic boundary (`view.CheckRelation` /
`view.HasRole`) — and must **never** call the outer `Service`, which would open a
detached check-then-write race. Every scope the guard reads through `view` is
recorded with the revision it observed; the repository locks those anchors plus
the mutation scope in canonical order and **re-validates every observed revision
before commit** — a mismatch returns `ErrStaleRevision` and writes nothing. The
guard must be synchronous, honor `ctx` cancellation, and do no network or
unrelated-store I/O.

Typed guarded commands (each takes `actor Actor` + a command struct carrying a
`MutationID`, the target, and an optional `ExpectedRevision *Revision`):

- `Service.GrantRelationship` / `RevokeRelationship` / `ReplaceRelationship` /
  `PurgeResourceAuthorization` → `(*Receipt, error)`;
- `Service.AssignRole` → `(*Receipt, error)`;
- `Service.UnassignRole` → `(UnassignRoleResult, error)` (see raw vs effective
  role listing).

A denial never reaches `Apply`; on denial the `MutationID` is **not** consumed
(a later allowed command with a fresh ID applies cleanly). The one-relation rule
means a different relation for a subject already related to the resource is a
`semantic_conflict` — use `ReplaceRelationship` (atomic, no delete/create gap).

### The trusted `SystemMutator`

`SystemMutator` bypasses only the host `MutationGuard`; it still validates
schema, requires a `MutationID`, uses atomic `Apply`, enforces guardian
invariants, increments revisions, persists receipts, and audits. Its surface:

- `Apply(ctx, Command)` — the generic trusted write;
- `GrantRelationship(ctx, GrantRelationshipCommand)`;
- `AssignRole(ctx, AssignRoleCommand)` / `UnassignRole(ctx, UnassignRoleCommand)`;
- `TeardownAuthorizationScope(ctx, TeardownAuthorizationScopeCommand)` — the one
  operation allowed to reduce a protected scope to zero (see invariants).

### MutationID, dependency revision, outcomes vs replay, receipt retention

- **`MutationID`.** `NewMutationID` mints a cryptographically strong,
  globally-unguessable 256-bit key (base32) — the actor-facing default.
  `DeriveMutationID(parts...)` produces a *deterministic* stable ID from a fixed
  operation identity (SHA-256 over length-prefixed parts) for **trusted**
  idempotency — a `SystemMutator` holder derives it so a retried bootstrap or
  invitation-accept dedups against its stored receipt rather than duplicating.
  Possession of a `MutationID` is **never** authority: an actor-facing replay
  re-runs the guard against current state before returning a stored receipt.
- **Dependency-revision validation.** Scope revisions are per-scope anchors —
  resource scope (`ScopeResource`) for relationships and scoped roles, subject
  scope (`ScopeSubject`) for global roles. A guarded write commits only if every
  authorization scope the guard used has the same revision when the repository
  locks and validates dependencies (canonical lock order; an absent anchor reads
  as revision 0, so a concurrent first writer is a detectable 0→1 change).
- **Outcome vs replay are independent facts** (default #8). A `Receipt` carries a
  stable `Outcome` — `OutcomeApplied`, `OutcomeNoChange`, `OutcomeSemanticConflict`,
  `OutcomeInvariantBlocked`, `OutcomeNotFound` — with **`Receipt.Replayed`** as
  separate metadata. Conflict is never encoded as `(nil, nil)`. Stale expected/
  dependency revision (`ErrStaleRevision`) and a `MutationID` replayed with a
  different payload (`ErrMutationMismatch`) are **command errors**, not outcomes.
  Only committed `applied`/`no_change`/`not_found` receipts are persisted and
  replayable; denial, stale, payload mismatch, cancellation, and infrastructure
  failure create no receipt (so a retry re-evaluates). An exact retry returns the
  original receipt with `Replayed=true` and no revision bump — even under a newer
  schema that would now reject the original relation.
- **Receipt retention.** Permanent by default (`iam_mutations.expires_at` is
  nullable; a NULL means permanent). A finite window is an explicit weaker
  idempotency posture with a ratified minimum and a cleanup contract — not the
  default.

### Last-owner/guardian and teardown invariants

The last-owner/guardian invariant is **one repository-atomic post-state rule**,
never a service-level exists→count→delete (the v1 non-atomic path is deleted).
Under the mutation scope lock, every ordinary command on a configured protected
resource type must leave **at least N direct anchors** for the protected
relation. A *direct anchor* is an exact concrete `SubjectRef` with an empty
relation — a `group:eng#member` owner is **not** a direct anchor, and
group-expanded/effective counts never mask loss of the final direct guardian.
The first successful command must establish the minimum (normally an owner
grant); a member/role-first command and any mutation of a legacy orphan scope
are blocked until a trusted repair establishes it. Two concurrent last-owner
revokes produce exactly one success and one `OutcomeInvariantBlocked` (one
database arbiter under concurrency).

The policy is `GuardianPolicy{Rules []GuardianRule{ResourceType, Relation,
MinAnchors}}`. **Its configuration seam is STORE CONSTRUCTION, not `Config`** —
the invariant must live where the atomic lock lives, so it is a store option:
`memstore.WithGuardianPolicy` / `stores/pgx.WithGuardianPolicy` /
`stores/turso.WithGuardianPolicy`. `DefaultGuardianPolicy` is the ratified
default (owner, min-1 direct anchor, every resource type); an explicitly empty
`GuardianPolicy` declares no invariant. The `authorization.GuardianPolicy` /
`GuardianRule` / `DefaultGuardianPolicy` aliases let a host name the vocabulary
without importing `domain/mutation`. Roles carry no guardian notion in v3
(opaque roles have no direct-anchor concept, default #5).

**Resource teardown** is a distinct `SystemMutator.TeardownAuthorizationScope`
command — the one operation permitted to reduce a protected scope to zero. It
requires the separately held capability **plus** a recorded non-empty teardown
reason (`ErrTeardownReasonRequired` otherwise). Ordinary
`PurgeResourceAuthorization` cannot silently bypass the guardian minimum (a purge
that would orphan a protected resource is `OutcomeInvariantBlocked`).
Authorization does **not** call a foreign resource repository from inside its
transaction; a host deleting a resource orders its own teardown, and the
ordering + ID-reuse hazard is documented on the method rather than misrepresented
as cross-feature database atomicity.

### Legacy migration and best-effort audit

The v1→v3 legacy API transition is complete: the raw unguarded mutation surface
is removed from `Service`; the `examples/auth-cms` invitation `Granter` was
migrated to `SystemMutator.GrantRelationship` with a stable `DeriveMutationID`,
so a retried accept replays without a duplicate revision bump. `Config.Audit`
(optional `AuditSink`) observes actor-facing and teardown attempts as
accepted/denied/failed with coarse bounded fields — never raw resource/subject
IDs as labels, never changing a committed mutation; a sink failure is warned and
swallowed.

## Raw versus effective role listing

Two listings, deliberately distinct:

- `ListRoleAssignmentsByResource` surfaces the **RAW** direct-scope assignments
  stored at a resource. It never surfaces a globally-granted subject.
- `ListEffectiveRoleGrantsByResource` (`EffectiveGrant` with explicit
  provenance — `Direct`, `Global`, or both) unions the direct scoped assignments
  with the **global** assignments a scoped `HasRole` satisfies, de-duplicated by
  `(subject, role)`. Its grant set **agrees with `HasRole`** (the Q5 global
  fallback), closing the v1 enumeration-vs-decision divergence — a global grant
  is reported with `Global` provenance, never rewritten as a scoped row.

**The roles scope rule (Q5).** The store-level lookup (`HasExactRole`) is
exact-scope match; `HasRole` treats a GLOBAL assignment (empty resource pair) as
satisfying any resource-scoped check. No graph walk — just the one fallback.
**Revoke carefully:** a scoped `UnassignRole` returns an `UnassignRoleResult`
whose `SameRoleGrantRemains` is true iff a GLOBAL assignment for the same exact
role still satisfies the scoped fallback — so a caller cannot mistake removal of
one scoped row for removal of effective access. It is a statement about THIS
exact role grant via the global fallback only; it does not claim generic access
remains (a host may compose access from other role/ReBAC rules).

## The cms boundary (documented, not a gap)

cms admin gating stays **coarse**: `AdminMiddleware` is session-level
(`RequireUser`) and this milestone does not change it. Fine-grained cms
authorization (per-entry, per-type) is future demand-gated work; when it
comes, it arrives as host-wired closures over this feature's kinds —
never as a cms→authorization import (rule 6).

## Anatomy + socket

```
authorization.go         the socket: Repositories, Config, Service,
                         NewService (→ Components{Service, SystemMutator}),
                         Register; root aliases for the engine + mutation
                         vocabulary; the errs vars
codes.go                 stable Reason codes + feature error sentinels + the
                         web.Error mapper seam
mutation_service.go      Actor, MutationGuard, AuditSink, SystemMutator,
                         Components, the generic guarded ApplyMutation seam
relationship_mutations.go  typed guarded relationship commands
role_mutations.go        typed guarded role commands + UnassignRoleResult
middleware.go            RequirePermission (root re-export; bodies internal)
domain/                  the hexagon's public rim — tuple types + ports
  relationship/          SubjectRef, CreateRelationship, projections, the Storer
  role/                  Assignment, EffectiveGrant, the Storer
  mutation/              MutationID, Command, Receipt, Outcome, Revision,
                         ScopeKey, GuardianPolicy, MutationRepository (the
                         frozen atomic write contract; port doc comments = spec)
internal/logic/
  authorizersvc/         the sealed ReBAC engine (schema DSL, compiler,
                         immutable snapshot, bounded check/lookup, budget,
                         reasons/explain, EvaluationLimits)
  rolesvc/               the roles service (the Q5 global-fallback rule)
memstore/                public in-core reference implementation, all three
                         ports over ONE shared-state bundle (real graph-walk
                         expansion + atomic Apply) — hosts may wire it
storetest/               executable spec: Run(t, newRepos) — Adversarial,
                         Roles/*, Mutations (22 cases), the Parity oracle, Budget
stores/turso/            the outbound tier: per-dialect SQL + migrations
stores/pgx/              (source "authorization"), each its own module
```

The socket is the FS2-shaped build plus the ratified `Components` bundle:
`comps, err := authorization.NewService(repos, cfg)` then
`comps.Service.Register(mount)`. `Register` logs one line, captures the logger
for best-effort audit warnings, and mounts **no routes** — `/authorization/*`
is this feature's claimed-but-unregistered namespace (charter C1), reserved for
a future admin surface (the deferred AZADM packet).

## Wiring semantics — nil vs required

| field | semantics |
|---|---|
| `Repositories.Relationships` | nil = relationship kind off. Wired ⇔ `Config.Model` set — either without the other is a loud `ErrModelRequired`. |
| `Repositories.Roles` | nil = roles kind off. Needs no Config at all. |
| `Repositories.Mutations` | the atomic write path (`mutation.MutationRepository`). Independent of the read ports (a store implements all three). Required whenever `Config.Guard` is set (`ErrGuardWithoutMutations`); the `SystemMutator` also needs it. |
| both kind fields nil | loud `ErrNoKindConfigured` at `NewService`. |
| an unwired kind's methods | fail closed with that kind's sentinel (`ErrRelationshipsNotConfigured` / `ErrRolesNotConfigured`). |
| `Config.Model` | REQUIRED with `Relationships`, forbidden without it; compiled + schema-validated at `NewService` (see validation-failures list). |
| `Config.Limits` | optional `EvaluationLimits`; zero fields → safe defaults, negative → `ErrInvalidLimits`. Relationship-kind-scoped; ignored-with-note under roles-only wiring. |
| `Config.IDs` | optional (`cryptids.IDGenerator`); zero value ⇒ the nanoid default; `cryptids.Database` defers to the store's DDL DEFAULT. Relationship-kind-scoped. |
| `Config.Guard` | nil = READ-ONLY posture (actor-facing mutations fail closed with `ErrMutationsNotConfigured`; decisions/lists and `SystemMutator` still work). Non-nil requires `Repositories.Mutations`. No default-allow policy. |
| `Config.Audit` | optional best-effort `AuditSink`; requires `Config.Guard` (`ErrAuditWithoutGuard`). |

The nil-Guard/nil-Model asymmetry is deliberate: an orphaned `Model` errors
loudly (capability-defining), while an orphaned `Limits`/`IDs` is
ignored-with-note (a tuning knob, the auth `MailFrom` precedent).

**`Check` evaluates the schema; policy short-circuits are host composition.**
The engine is a pure schema evaluator — it grants no platform-admin or
self-access bypass. Both are HOST recipes a host runs first in its own Check
closure, and both fail **closed**:

- **Platform admin (host recipe).** Declare a `platform` resource type with an
  `admin` *permission* (not just a relation), create a
  `platform:main#admin@user:<id>` data tuple, and check that permission first in
  the host's Check closure. Platform-admin stays DATA, never Config — the bypass
  is host composition, not engine magic. For `LookupResources`, the host runs the
  admin check first and skips ID filtering if it holds.
- **Self-access (host recipe).** A host that models users as ReBAC resources adds
  an ID-equality check in its own closure; a host that doesn't simply never
  carries the rule.

### The `RequirePermission` middleware gate (middleware-consolidation, 2026-07-11)

The feature exports an HTTP middleware builder that gates a route on the context
`Principal` holding a permission on a resolved resource — the
`RequireUser`-shaped sibling of the recipes above.

**Requires the relationship kind wired; a roles-only host must not mount it.**
`RequirePermission` panics if `Repositories.Relationships` is nil, at
REGISTRATION/BOOT time (build the gate once when wiring routes, never inside the
per-request path), naming the missing kind. The shape (root re-export of the
internal engine implementation — the root package writes NO HTTP; the
401/403/500/503 responses live in `authorizersvc`):

```go
type ResourceResolver func(r *http.Request) (Resource, error)
func FixedResource(resourceType, resourceID string) ResourceResolver
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware
```

**PURE Check, no bypass hook.** A host wanting platform-admin/self-access
composes those recipes as its OWN closure AROUND this middleware, run first
(auth-cms's `requireMembership` composes `isPlatformAdmin` before the
builder-gated handler). D-D: it fails CLOSED (`Check`/resolver error → 500;
`ErrEvaluationLimit` → 503; no principal → 401; `!Allowed` → 403), the
deliberate opposite of `ratelimiter.Middleware`'s fail-open. The 401/403/500
responses use `web.RespondJSONError` (the FS9 `web.Error` shape) — an adopter
replacing a hand-rolled gate with this builder changes its response *body*
contract to the FS9 shape (status codes unchanged).

**Consumer-side nil semantics** (in the CONSUMING features): a nil Check-shaped
seam is deny-by-absence — auth's `Granter` (nil = no grant on invitation accept)
and events' `Authorize` (nil = the resource-scoped stream route never registers)
are the live examples.

## The policy seam — designed, named, DEFERRED

The third kind exists as a **named seam only**: designed enough to land without
re-deciding anything, built when its trigger fires.

- **Shape when it lands:** a `policy.Evaluator` port in its own public rim
  (`domain/policy`), one nil-safe `Repositories.Policies` field (kind OFF when
  nil), per-kind Service methods, and — if data-driven — an `iam_policies` table
  as the next migration number in source `"authorization"`.
- **The named open question (decided at ITS cut):** data-driven policies (rows,
  host-editable at runtime) vs code-registered policies (the cms `Types` / jobs
  `Handlers` precedent).
- **Demand trigger:** the first host need neither a relationship model nor a role
  lookup expresses cleanly (attribute/condition rules) or runtime-editable rules.

## Store parity — one suite, three backends

Supported stores: **{turso, pgx}** as sibling modules, plus the in-core
`memstore/` reference — and all three pass the ONE `storetest` suite
(`storetest.Run(t, newRepos func(t) authorization.Repositories)`; a nil kind
skips that kind's families with a loud named `t.Skip`). Group expansion,
descendant lookup, and atomic `Apply` are the places the flagship could
authorize/mutate differently per backend — recursive CTEs (both SQL stores) vs a
Go graph walk + one mutex (`memstore`) — which is why the same suite runs against
all three, live per dialect at milestone close.

The suite's named families are acceptance criteria, not nice-to-haves:

- `Adversarial/*` — `MembershipCycle` (cyclic group data terminates; CTEs
  cycle-safe by relation-aware UNION dedup, memstore by a `[3]string` visited
  set — all unbounded-but-cycle-safe), `DeepNesting`, `DiamondDedup` (with the
  **direct-count security assertion**: `CountByResourceAndRelation` counts direct
  tuples ONLY — that count feeds last-owner protection, and an expansion join
  would silently overcount owners), `NestedUserset`, `PlatformAdminIsNotMagic`,
  `MemberAdminUsersetSeparation`, `CyclePerRelationIsRelationAware`,
  `RelationAwareConcreteGroupGrant`, `MissingUsersetRelationRejected`.
- The `Mutations/*` family — **22 cases**: the six frozen specs
  (`ExactReplayReturnsOriginalReceipt`, `MutationIDPayloadMismatchChangesNothing`,
  `StaleRevisionRejected`, `RollbackLeavesNoTrace`, `NoPartialBatch`,
  `ConcurrentSingleWinner`) plus grant/revoke/replace revisions, purge/teardown,
  role assign/unassign scopes, expected-revision/no-op,
  `ReplayAfterSchemaChange`, `GuardianEstablishesMinimum`,
  `LastOwnerGuardianScenarios`, `ConcurrentGuardRevokeRacesGuardedMutation`,
  `ConcurrentTwoOwnerRevokeRounds`, `ConcurrentReplaceNoAbsentState`,
  `ConcurrentReceiptRevisionForensics`, `CrossScopeBatchRejectedNoStateChange`,
  `ContextCancellationNoStateChange`, replay/stale-writer/mixed-kind storms.
- The `Parity/*` oracle — bidirectional Check/Lookup completeness+soundness over
  a finite fixture universe + `LimitExhaustionIsError`.
- The `Budget/*` family — depth-boundary, fan-out, lookup-result-cap, and
  sibling-Through parity across dialects.
- The `Roles/*` family — assign/unassign idempotence, exact-scope isolation, the
  Q5 global fallback, `EffectiveEnumerationAgreesWithHasRole`,
  `ScopedRevokeGlobalRoleRemains`, `EffectivePagination`.

**Migrations:** source `"authorization"` — the identical four-file set in both
store modules (dialect-specific DDL inside):

- `0001_iam_relationships.sql` — the ReBAC tuple store. `relationship_id`
  DEFAULT is `lower(hex(randomblob(16)))` (turso) / `gen_random_uuid()::text`
  (pgx); `idx_iam_relationships_unique_subject` (WITHOUT `relation`) enforces the
  one-relation-per-exact-`SubjectRef` rule.
- `0002_iam_roles.sql` — the roles assignment store; `ck_iam_roles_scope_pair`
  keeps the global/scoped pair consistent.
- `0003_iam_scopes.sql` — the scope **revision anchors** (`(scope_kind,
  scope_type, scope_id)` PK, `revision ≥ 0 DEFAULT 0`; an absent anchor reads as
  revision 0 by contract).
- `0004_iam_mutations.sql` — the mutation **receipts** keyed by `MutationID`
  (payload digest, resulting revision, domain outcome, governing schema digest —
  never the payload; nullable `expires_at`, permanent retention default).

**Scaffold-and-own:** hosts export with `ExportMigrations(dst)` into their own
ledger and NEVER renumber scaffolded files. Both store constructors
(`Repositories(db, ...Option) (authorization.Repositories, error)`) probe all
four `iam_*` tables at boot and error before the host serves traffic, naming the
specific missing table. Migration source ordering: the four files are a
contiguous intra-source group in filename order; the `authorization` source is
self-contained and can sit anywhere in a host's ordered stream relative to
`cms`/`auth`/`jobs`/`events`.

**Live-store commands** (env-gated; loud skips keep `make check` hermetic):

```sh
# pgx — plain env-gate; pgx test DBs must be C-collation
cd features/authorization/stores/pgx && \
  POSTGRES_TEST_DSN='postgres://…?sslmode=disable' go test -race -count=1 ./...

# turso — build-tag gated
cd features/authorization/stores/turso && \
  TURSO_DATABASE_URL='libsql://…' TURSO_AUTH_TOKEN='…' \
  go test -tags=integration -race -count=1 ./...

# the memory reference + shared suite (hermetic, race + high-contention)
cd features/authorization && go test -race ./...
```

## Wiring page — the code

One complete `main.go` wiring, the executable twin of
`examples/auth-cms/cmd/server/` (read that host for the full running program):

```go
// The model is registered data — no migration. Relationship kind only.
model := authorization.NewSchema([]authorization.ResourceSchema{
    {Name: "project", Def: authorization.ResourceTypeDef{
        Relations: map[string]authorization.RelationDef{
            "owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
            "member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
        },
        Permissions: map[string]authorization.PermissionRule{
            "view":          authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("member")),
            "manage_access": authorization.AnyOf(authorization.Direct("owner")), // what the guard reads
        },
    }},
    // Declaring a `platform` type with an `admin` permission + a
    // platform:main#admin@user:<id> tuple is the (data, not Config) platform-admin
    // recipe. The host runs the `admin` Check first in its own closure.
    {Name: "platform", Def: authorization.ResourceTypeDef{
        Relations:   map[string]authorization.RelationDef{"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}}},
        Permissions: map[string]authorization.PermissionRule{"admin": authorization.AnyOf(authorization.Direct("admin"))},
    }},
})

// BOTH kinds over ONE shared-state memstore bundle (zero-infra host) so the
// trusted SystemMutator's writes and the read side observe the same state. The
// guardian minimum (owner, min-1) is a STORE-construction option. Production:
// swap memstore.New for a store module — see the swap snippet below.
store := authzmem.New(authzmem.WithGuardianPolicy(authorization.GuardianPolicy{
    Rules: []authorization.GuardianRule{{ResourceType: "project", Relation: "owner", MinAnchors: 1}},
}))

comps, err := authorization.NewService(authorization.Repositories{
    Relationships: store.Relationships(),
    Roles:         store.Roles(),
    Mutations:     store.Mutations(),
}, authorization.Config{
    Model: model,
    Guard: hostGuard{}, // the host MutationGuard reading manage_access via the DecisionView
})
if err != nil {
    return err
}
authorizer := comps.Service
system := comps.SystemMutator // held apart — hand only to trusted bootstrap/invitation code
if err := authorizer.Register(mount); err != nil { // logs only; no routes
    return err
}

// Boot-seed the ownable scope through the TRUSTED SystemMutator (establish the
// owner FIRST so a later member invitation is not member-first-blocked). Each
// MutationID is DERIVED from its tuple, so a restart re-seed dedups.
seed := authorization.GrantRelationshipCommand{
    ResourceType: "project", ResourceID: "demo", Relation: "owner",
    Subject: authorization.SubjectRef{Type: "user", ID: "demo-owner"},
}
seed.MutationID = authorization.DeriveMutationID("host/bootstrap-owner",
    seed.ResourceType, seed.ResourceID, seed.Relation, seed.Subject.Type, seed.Subject.ID)
if _, err := system.GrantRelationship(ctx, seed); err != nil {
    return err
}

// Stop 1 — the Granter seam (auth): invitation-accept writes a real tuple through
// the TRUSTED SystemMutator with a stable derived MutationID (idempotent retry):
//
//   type invitationGranter struct{ system *authorization.SystemMutator }
//   func (g invitationGranter) Grant(ctx context.Context, resourceType,
//       resourceID, relation, subjectType, subjectID string) error {
//       cmd := authorization.GrantRelationshipCommand{
//           ResourceType: resourceType, ResourceID: resourceID, Relation: relation,
//           Subject: authorization.SubjectRef{Type: subjectType, ID: subjectID},
//       }
//       cmd.MutationID = authorization.DeriveMutationID("host/invitation-grant",
//           resourceType, resourceID, relation, subjectType, subjectID)
//       _, err := g.system.GrantRelationship(ctx, cmd)
//       return err
//   }
authCfg.Granter = invitationGranter{system: system}

// Stop 2 — the Check seam (events): the scoped SSE stream authorizes through the
// engine. This closure shape satisfies ANY Check-only seam. PrincipalRef is
// concrete; a decision request cannot carry a userset.
eventsCfg.Authorize = func(ctx context.Context, p identity.Principal, resourceType, resourceID string) (bool, error) {
    res, err := authorizer.Check(ctx, authorization.CheckRequest{
        Principal:  authorization.PrincipalFrom(p),
        Permission: "view",
        Resource:   authorization.Resource{Type: resourceType, ID: resourceID},
    })
    if err != nil {
        return false, err // fail CLOSED — never `allowed, _ :=`
    }
    return res.Allowed, nil
}

// Stop 3 — enumeration (flagship-only API, never a seam):
result, err := authorizer.LookupResources(ctx, authorization.PrincipalFrom(p), "view", "project")
// pure enumeration: result.IDs sorted, each once (empty = no access). For
// admin-sees-everything, run the platform-admin Check first and skip filtering.

// Stop 4 — a role-gated host route (the roles kind; opaque role string):
ok, err := authorizer.HasRole(ctx, authorization.PrincipalFrom(p), "auditor", "project", "demo")
```

**The store-module swap** (memstore → production turso; pgx is symmetric):

```go
import authzstore "github.com/gopernicus/gopernicus/features/authorization/stores/turso"

repos, err := authzstore.Repositories(db,
    authzstore.WithGuardianPolicy(authorization.DefaultGuardianPolicy())) // boot-probes all four iam_* tables
if err != nil {
    return err // errors BEFORE serving traffic, naming the missing table
}
comps, err := authorization.NewService(repos, authorization.Config{Model: model, Guard: hostGuard{}})
```

plus the migration step: `authzstore.ExportMigrations(dst)` scaffolds the four
`authorization`-source files into the host's ledger — apply before boot, never
renumber.

**Roles-only wiring** (kind independence — no model, no engine; still applies the
full four-file source):

```go
store := authzmem.New()
comps, err := authorization.NewService(authorization.Repositories{
    Roles:     store.Roles(),     // Relationships nil = that kind OFF
    Mutations: store.Mutations(), // needed for guarded AssignRole/UnassignRole
}, authorization.Config{Guard: hostGuard{}}) // no Model — a roles-only host never constructs one
```

**The composed-kinds closure** (there is no built-in facade; the host owns the
composition):

```go
authorize := func(ctx context.Context, p identity.Principal, rt, rid string) (bool, error) {
    principal := authorization.PrincipalFrom(p)
    res, err := authorizer.Check(ctx, authorization.CheckRequest{
        Principal: principal, Permission: "view", Resource: authorization.Resource{Type: rt, ID: rid},
    })
    if err != nil {
        return false, err // fail CLOSED on error
    }
    if res.Allowed {
        return true, nil
    }
    return authorizer.HasRole(ctx, principal, "auditor", rt, rid)
}
```

## The proof host — examples/auth-cms

`examples/auth-cms` is the living posture-3 composition (all in-memory, rule 6
demonstrated). It proves the guarded and trusted paths in host code and tests:

- `cmd/server/authorization.go` — `authzSchema` (adds `manage_access =
  AnyOf(Direct(owner))`), `authzGuardianPolicy` (owner/min-1 narrowed to the
  ownable `project` type — the sanctioned host-narrowing path, since `platform`
  is a flat admin-list type with no owner relation), `newAuthorization` (wires
  `Config.Guard`), `seedAuthorization` (boot-seeds `project:demo#owner` then
  `platform:main#admin` via `SystemMutator` + `DeriveMutationID`).
- `cmd/server/guard.go` — `hostMutationGuard`: a platform-admin short-circuit
  (`view.CheckRelation(platform:main#admin)`), global (subject-scoped) mutations
  refused (trusted-only blast radius), else the `manage_access`-backing relation
  (`owner`) on the mutated resource scope — reading ONLY the dependency-tracking
  `DecisionView`, so both tuples become revision-tracked dependencies. `hostActor`
  adapts `identity.Principal` → `Actor` at the boundary.
- `cmd/server/testdata/az3-proof-transcript.md` — the checked-in
  exact-semantics/concurrency transcript (12 sections, real observed values;
  claims NO effects delivery and NO generic admin API; secret-scanned clean).

**Honest consequence (deferred surface).** auth-cms intentionally lost its
browser-driven role-assignment endpoints: all session-only authorization-mutation
HTTP routes were removed (a shipped session-only mutation route is forbidden).
Recent-auth/step-up protection is NOT claimed — authentication does not yet
export a public sensitive-operation protector. The proof here is host code plus
tests, not a browser flow; the browser role-assignment surface returns with the
deferred AZADM packet.

## Non-goals (cut lines)

- **No ABAC policy kind in v3** — the attribute/condition policy kind stays a
  named, deferred seam (the policy-seam section is its ledger).
- **No role implication / role catalog / role vocabulary** — roles are opaque
  strings; any known-role or allowed-assignment catalog is host/admin policy, not
  the core role kind.
- **No decision cache** — correctness and bounded evaluation land first. Mutation
  revisions/events make a future cache possible without inventing invalidation
  now.
- **No templ/HTML UI, no routes** — `Register` logs only; `/authorization/*`
  stays claimed-unregistered. A view module is demand-gated.
- **No composed Check facade** — hosts compose kinds in their own closures.
- **No `sdk/authorization` port** — authorization's check/decision vocabulary
  stays consumer-declared (fails the sdk graduation criteria today); identity
  rides `sdk/foundation/identity`.
- **No groups aggregate** (Q1 TRIM): expansion is pure tuples
  (`group:{id}#member@user:{x}`); a groups table returns with the first
  named-group UX demand as migration 0005+.
- **No PostfilterLoop** (§2.6 demand gate) — a future enumeration-shaped consumer
  seam must ship paired with it.

## Deferred follow-ups (non-blocking, not part of the v3 gate)

These are recorded, ratified-as-deferred packets — not gaps, and not part of the
v3 completion gate:

- **Effects and observability**
  (`.claude/plans/authorizationv3/05-effects-and-observability.md`). The v3
  correctness kernel emits **no** durable side effects: it lands the mutation
  identity, revision, receipt, and audit vocabulary a later adapter needs without
  creating a delivery queue, dispatching a post-commit callback, or appending an
  event. Before it executes it must ratify command/event cardinality and choose
  an honest procedural guarantee (at-least-once with a MutationID-idempotent
  handler, or at-most-once best effort) — domain mutation idempotency alone
  cannot prove a procedural side effect was not duplicated. Durable mode requires
  a same-transaction events outbox, never an authorization-specific jobs table,
  and must consume the shared `sdk/capabilities/work` + `features/jobs`
  vocabulary.
- **Generic admin surface**
  (`.claude/plans/authorizationv3/06-admin-and-proof-host.md`). API-only, and
  **blocked indefinitely** — it may not execute until authentication exports a
  host-facing sensitive-operation protector covering live session, origin/CSRF,
  and operation-bound recent-auth consumption. **That seam does not exist**: the
  current authentication feature does not export a public recent-auth consume /
  browser-safe mutation gate, and no ratified authentication follow-up creates
  one, so a generic authorization admin adapter that claims auth-v3 step-up
  composition is a **missing prerequisite**, not a satisfied v3 premise.
  Authorization must never unblock itself by importing authentication internals.

## UPGRADE NOTE — v1 → v3 (host-owned, data-preserving)

A host with a live pre-v3 authorization database follows the **executed,
data-preserving upgrade runbook**, [`stores/UPGRADE.md`](stores/UPGRADE.md), which
wraps the detection-and-repair queries in [`stores/CONVERSION.md`](stores/CONVERSION.md).
The one semantic change that moves access is that **the userset relation became
load-bearing**: v1 hard-coded `relation = 'member'` and ignored the stored
`subject_relation`, so a `group:g#admin` grant behaved like `#member` and a
concrete `group:g` grant reached the group's members; v3 evaluates the exact
stored relation. Before deploying, the runbook's **gain/lose/retain assessment**
tells an adopter each stored shape's access fate — concrete principals and valid
`#member` usersets **retain**; concrete-group grants and non-`member` usersets
**lose** the over-broad access v1's `member` collapse granted; structurally
malformed rows **block** until repaired.

The runbook is **executed and validated** (AZ3-5.1, 2026-07-14): it ran end to end
against a populated v1 fixture on live PostgreSQL and libSQL/SQLite, and booted a
v3 `Service` over the converted PostgreSQL store to confirm the verdicts. Two rules
are load-bearing and non-negotiable: **no step resets a real adopter database**
(the destructive reset path is dev/example only), and **no ambiguous or missing
userset relation is ever silently defaulted to `member`** — that guess is the v1
defect v3 removes. The constraints are added with an explicit
`ALTER TABLE … ADD CONSTRAINT` (PostgreSQL) or table-rebuild (libSQL/SQLite), which
**fails while any malformed row remains** — the enforced repair gate.
