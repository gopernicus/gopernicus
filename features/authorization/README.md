# features/authorization — the IAM domain: independently wireable kinds

A pluggable, datastore-free authorization feature: an IAM domain offering
multiple KINDS of authorization — v1 ships **relationships** (a ReBAC
engine: schema-driven permission checks, group expansion,
through-traversal, platform-admin data tuples) and **roles** (minimal
opaque-string role assignments, scoped or global) — plus a named,
deferred **policy** seam. ReBAC is one kind, not the feature's identity.
Design of record: `.claude/plans/roadmap/auth-v2-feature-design.md` (as
amended by the 2026-07-08 multi-kind owner direction), executed via
`.claude/plans/authorization-v1/`.

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
Everything on `Service` beyond boolean checks (enumeration, relationship
CRUD, role listings, the model DSL) is **flagship-specific API, never a
cross-feature seam**. Graduation trigger (recorded, not cashed): the day
two features need the identical authorize vocabulary, an `sdk`
port is designed; until then there is deliberately no `sdk/authorization`.

Living/recorded artifacts, one per posture: `examples/minimal` (wires
nothing — posture 1); the authorization-v1 Z4 **commit-1** protocol
(`2e1e5eb` — `examples/auth-cms` satisfying the events seam with an
ownership closure, `GOWORK=off go list -m all` captured clean of this
module) — posture 2; the `examples/auth-cms` HEAD host (Z4 commit 2,
`65fcb49`) — posture 3, both kinds.

## The kinds

| kind | expresses | `Repositories` field | table | Service method family | nil semantics |
|---|---|---|---|---|---|
| **relationships** | ReBAC tuples + a schema: who relates to what, and which permissions those relations grant (group expansion, `Through` traversal, platform-admin data tuples) | `.Relationships` | `iam_relationships` | `Check`, `CheckBatch`, `FilterAuthorized`, `LookupResources`, `CreateRelationships`, `DeleteRelationship`, `DeleteResourceRelationships`, `DeleteByResourceAndSubject`, `RemoveMember`, `ValidateRelation(s)`, `GetSchema`, `GetPermissionsForRelation`, `GetRelationTargets`, `ListRelationshipsBySubject/ByResource` | nil ⇒ kind OFF structurally; every method returns `ErrRelationshipsNotConfigured` |
| **roles** | opaque-string role grants — `(subject, role)` pairs, resource-scoped or global | `.Roles` | `iam_roles` | `AssignRole`, `UnassignRole`, `HasRole`, `ListRoleAssignmentsBySubject/ByResource` | nil ⇒ kind OFF; every method returns `ErrRolesNotConfigured` |
| **policy** (deferred) | attribute/condition-shaped rules | (future) `.Policies` | (future) `iam_policies` | — | a designed, named seam — see "The policy seam" below |

Rules of the kinds:

- **Independently wireable.** A host wires either kind, both, or a
  roles-only bundle with no model. Zero kinds is a loud
  `ErrNoKindConfigured` at construction.
- **Port-optional, schema-wholesale** (the §2.1 bounding rule applied
  intra-feature): an adopting host scaffolds ALL `iam_*` tables
  inert-but-present regardless of which kinds it wires. **A roles-only
  host still applies the FULL `"authorization"` migration source,
  `iam_relationships` included, and both store boot probes expect both
  tables.** Source-level schema optionality is the feature boundary's
  job, never a kind's.
- **No composed Check facade.** A unified check consulting multiple
  kinds is exactly the speculative unification this feature avoids — a
  host composes kinds in its own closure (see the labeled snippet in the
  wiring page).
- **Terminology:** a KIND is a nil-safe port family WITHIN this one
  feature module — never a module, and unrelated to ARCHITECTURE.md's
  R6 "Kinds of module" taxonomy vocabulary.

## The cms boundary (documented, not a gap)

cms admin gating stays **coarse**: `AdminMiddleware` is session-level
(`RequireUser`) and this milestone does not change it. Fine-grained cms
authorization (per-entry, per-type) is future demand-gated work; when it
comes, it arrives as host-wired closures over this feature's kinds —
never as a cms→authorization import (rule 6).

## Anatomy + socket

```
authorization.go         the socket: Repositories, Config, Service,
                         NewService, Register; root aliases for the
                         engine vocabulary + rim types; the errs vars
domain/                  the hexagon's public rim — tuple types + ports
  relationship/          CreateRelationship, projections, the 14-method
                         Storer (port doc comments are the spec)
  role/                  Assignment + the 5-method Storer
internal/logic/
  authorizersvc/         the sealed ReBAC engine (schema DSL, validator,
                         check/lookup/traversal, the Q6 id mint seam)
  rolesvc/               the roles service (the Q5 global-fallback rule)
memstore/                public in-core reference implementation, both
                         kinds (real graph-walk expansion) — hosts may
                         wire it for zero-infra
storetest/               executable spec: Run(t, newRepos) — the named
                         adversarial sub-runners + the Roles/* family
stores/turso/            the outbound tier: per-dialect SQL + migrations
stores/pgx/              (source "authorization"), each its own module
```

The socket is the FS2 method form: `authorizer, err :=
authorization.NewService(repos, cfg)` then `authorizer.Register(mount)`.
`Register` logs one line and mounts **no routes** — `/authorization/*`
is this feature's claimed-but-unregistered namespace (charter C1),
reserved for a future admin surface, which will mount at
`internal/inbound/authorization` per the feature inbound anatomy.

**The model is registered data, zero migrations** (relationship kind
only): a host declares its schema in `main` with
`NewSchema`/`ResourceSchema` — resource types, relations with
`AllowedSubjects`, permissions built from `Direct`/`Through`/`AnyOf` —
and changing it is a redeploy, not a migration. The roles kind takes no
model: roles are **opaque strings** the host interprets.

## Wiring semantics — nil vs required

| field | semantics |
|---|---|
| `Repositories.Relationships` | nil = relationship kind off. Wired ⇔ `Config.Model` set — either without the other is a loud `ErrModelRequired`. |
| `Repositories.Roles` | nil = roles kind off. Needs no Config at all. |
| both nil | loud `ErrNoKindConfigured` at `NewService` — a feature that does nothing is a misconfiguration. |
| an unwired kind's methods | fail closed with that kind's sentinel (`ErrRelationshipsNotConfigured` / `ErrRolesNotConfigured`) — never a silent false/allow. |
| a userset `Subject` (non-empty `Relation`) on a roles-kind method | rejected loudly (`ErrUsersetSubjectOnRole`) — usersets are a relationship-kind concept; dropping the field silently would treat `group#member` as the group itself. |
| `Config.Model` | REQUIRED with `Relationships`, forbidden without it; schema-validated at `NewService` (unknown relations/targets, permission cycles = loud errors). |
| `Config.MaxTraversalDepth` | optional; `<= 0` ⇒ default 10, never an error. Relationship-kind-scoped; ignored-with-note under roles-only wiring. **ENGINE-ONLY:** it bounds the engine's Go through-traversal recursion and is NEVER threaded into the memstore or the store CTEs — those are unbounded-but-cycle-safe (UNION dedup / visited-set). |
| `Config.IDs` | optional (`cryptids.IDGenerator`); zero value ⇒ the nanoid default. Mints `relationship_id` at `Service.CreateRelationships`; `cryptids.Database` delegates to the store's DDL DEFAULT (the store omits the id column for an all-empty batch). Relationship-kind-scoped; ignored-with-note under roles-only wiring. The roles kind has NO id strategy — `iam_roles` is keyed by its 5-tuple. |

The asymmetry is deliberate: an orphaned `Model` errors loudly because
it is capability-defining (a model with no engine is a misconfiguration
trap), while an orphaned `MaxTraversalDepth`/`IDs` is ignored-with-note
because each is a tuning knob (the auth `MailFrom` precedent).

**Platform admin is DATA, never Config.** The bypass convention is the
tuple `platform:main#admin@<type>:<id>` over a host-declared `platform`
resource type. No bypass exists unless the host declares the type in its
schema AND creates the tuple. A platform admin's `LookupResources`
returns `LookupResult.Unrestricted = true` with no id enumeration —
callers must branch on `Unrestricted` before reading `IDs`.

**The roles scope rule (Q5, the one documented rule):** the store-level
lookup (`HasExactRole`) is exact-scope match; the service-level
`HasRole` treats a GLOBAL assignment (empty resource pair) as satisfying
any resource-scoped check. No graph walk — just the one fallback.
**Known, accepted v1 limitation (enumeration and decision diverge):**
`ListRoleAssignmentsByResource` surfaces direct-scope assignments ONLY —
a globally-granted subject that `HasRole` allows never appears in a
resource's listing. "Effective grants for a resource" enumeration is a
named deferred item. Revoke carefully: a scoped unassign revokes only
the scoped grant; a global grant, if one exists, keeps the gate open.

**Consumer-side nil semantics** (in the CONSUMING features): a nil
Check-shaped seam is deny-by-absence — auth's `Granter` (nil = no grant
on invitation accept) and events' `Authorize` (nil = the resource-scoped
stream route never registers) are the live examples.

## The policy seam — designed, named, DEFERRED

The third kind exists as a **named seam only** (the deferral-ledger
discipline): designed enough to land without re-deciding anything, built
when its trigger fires.

- **Shape when it lands:** a `policy.Evaluator` port in its own public
  rim (`domain/policy`), one nil-safe `Repositories.Policies` field
  (kind OFF when nil, exactly like the other two), per-kind Service
  methods, and — if data-driven — an `iam_policies` table as the next
  migration number in source `"authorization"` (scaffolding wholesale
  like every `iam_*` table). Nothing in v1 blocks it: `Repositories` and
  `Config` grow one nil-safe field each, and the kind-sentinel pattern
  extends unchanged.
- **The named open question (decided at ITS cut, not now):** data-driven
  policies (rows in `iam_policies`, host-editable at runtime) vs
  code-registered policies (host registers Go predicates at
  construction — the cms `Types` / jobs `Handlers` precedent).
- **Demand trigger:** the first host need neither a relationship model
  nor a role lookup expresses cleanly — attribute/condition rules
  (time-boxed access, ownership-with-status, environment conditions) or
  runtime-editable rules. The deferral is a documented seam, not a gap.

## Store parity — one suite, three backends

Supported stores: **{turso, pgx}** as sibling modules, plus the in-core
`memstore/` reference — and all three pass the ONE `storetest` suite
(`storetest.Run(t, newRepos)`; a nil kind in a host's own store skips
that kind's families with a loud named `t.Skip`). The suite's named
adversarial sub-runners are acceptance criteria, not nice-to-haves:

- `Adversarial/MembershipCycle` — cyclic group data terminates (the
  stores' recursive CTEs are cycle-safe by UNION dedup; memstore by
  visited-set — all UNBOUNDED, no depth term).
- `Adversarial/DeepNesting` — ≥3-level group nesting resolves.
- `Adversarial/DiamondDedup` — diamond-shaped membership deduplicates,
  **with the direct-count assertion**: `CountByResourceAndRelation`
  counts direct tuples ONLY. That count assertion is a **security
  assertion** — the count feeds last-owner protection, and an expansion
  join would silently overcount owners and let the last real owner be
  removed.
- `Adversarial/NestedUserset` — tuple-side userset subjects
  (`…@group:eng#member`) expand correctly.
- `Adversarial/Unrestricted` — the platform-admin data tuple yields
  `Unrestricted` for user AND service_account admins.
- The `Roles/*` family — assign/unassign idempotence, exact-scope
  isolation, distinct-assignments coexistence, keyset listings, the Q5
  global fallback.
- `Relationship/DBGeneratedIDOnEmpty` — the `cryptids.Database`
  omit-column branch: the DDL DEFAULT fills the PK (asserted via
  listing; `CreateRelationships` is error-only, no RETURNING).

Group expansion and descendant lookup are **recursive CTEs** in both SQL
stores and a Go graph walk in `memstore` — the one place the flagship
could authorize differently per backend, which is why the same suite
runs against all three (live per dialect at milestone close).

**Migrations:** source `"authorization"` —
`0001_iam_relationships.sql` + `0002_iam_roles.sql`, identical filename
sets in both store modules (dialect-specific DDL inside; the
`relationship_id` DEFAULT is `lower(hex(randomblob(16)))` on turso,
`gen_random_uuid()::text` on pgx). **Scaffold-and-own:** hosts export
with `ExportMigrations(dst)` into their own ledger and NEVER renumber
scaffolded files. Both store constructors
(`Repositories(db) (authorization.Repositories, error)`) probe
`iam_relationships` AND `iam_roles` at boot and error before the host
serves traffic, naming the specific missing table. The
`iam_relationship_metadata` table was trimmed (Q4): its engine consumer
was deleted upstream; it returns as the next migration number with the
first real metadata consumer.

## Wiring page — the stops, then the code

```
model (NewSchema) ─┐
                   ├─> NewService(Repositories{Relationships, Roles}, Config{Model})
memstore/store  ───┘        │
                            ├─> Register(mount)                    (logs; no routes)
                            ├─> Granter closure  ──> auth.Config.Granter
                            ├─> Check closure    ──> events.Config.Authorize
                            ├─> LookupResources  ──> "what may I see?" surfaces
                            └─> HasRole          ──> role-gated host routes
```

One complete `main.go` wiring, the executable twin of
`examples/auth-cms/cmd/server/` (Z4 commit 2 — read that host for the
full running program):

```go
// The model is registered data — no migration. Relationship kind only.
model := authorization.NewSchema([]authorization.ResourceSchema{
    {Name: "project", Def: authorization.ResourceTypeDef{
        Relations: map[string]authorization.RelationDef{
            "owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
            "member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
        },
        Permissions: map[string]authorization.PermissionRule{
            "view": authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("member")),
        },
    }},
    // Declaring a `platform` type + creating a platform:main#admin@user:<id>
    // tuple is the (data, not Config) platform-admin convention.
    {Name: "platform", Def: authorization.ResourceTypeDef{
        Relations: map[string]authorization.RelationDef{
            "admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
        },
    }},
})

// BOTH kinds, memstore-backed (zero-infra host). Production: swap the
// memstore for a store module — see the labeled snippet below.
authorizer, err := authorization.NewService(authorization.Repositories{
    Relationships: authzmem.NewRelationships(),
    Roles:         authzmem.NewRoles(),
}, authorization.Config{Model: model})
if err != nil {
    return err
}
if err := authorizer.Register(mount); err != nil { // logs only; no routes
    return err
}

// Stop 1 — the Granter seam (auth): invitation-accept writes a real tuple.
// auth.Granter is a one-method interface; the host adapter is ~10 lines:
//
//   type relationshipGranter struct{ authorizer *authorization.Service }
//   func (g relationshipGranter) Grant(ctx context.Context, resourceType,
//       resourceID, relation, subjectType, subjectID string) error {
//       return g.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{{
//           ResourceType: resourceType, ResourceID: resourceID, Relation: relation,
//           SubjectType: subjectType, SubjectID: subjectID,
//       }})
//   }
authCfg.Granter = relationshipGranter{authorizer: authorizer}

// Stop 2 — the Check seam (events): the scoped SSE stream authorizes
// through the engine. This closure shape satisfies ANY Check-only seam.
eventsCfg.Authorize = func(ctx context.Context, p identity.Principal, resourceType, resourceID string) (bool, error) {
    res, err := authorizer.Check(ctx, authorization.CheckRequest{
        Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
        Permission: "view",
        Resource:   authorization.Resource{Type: resourceType, ID: resourceID},
    })
    if err != nil {
        return false, err // fail CLOSED — never `allowed, _ :=`
    }
    return res.Allowed, nil
}

// Stop 3 — enumeration (flagship-only API, never a seam):
result, err := authorizer.LookupResources(ctx, subject, "view", "project")
// result.Unrestricted == true → platform admin; don't read result.IDs.

// Stop 4 — a role-gated host route (the roles kind; opaque role string):
ok, err := authorizer.HasRole(ctx, subject, "auditor", "project", "demo")
```

**The store-module swap** (memstore → production turso; pgx is
symmetric):

```go
import authzstore "github.com/gopernicus/gopernicus/features/authorization/stores/turso"

repos, err := authzstore.Repositories(db) // boot-probes iam_relationships AND iam_roles
if err != nil {
    return err // errors BEFORE serving traffic, naming the missing table
}
authorizer, err := authorization.NewService(repos, authorization.Config{Model: model})
```

plus the migration step: `authzstore.ExportMigrations(dst)` scaffolds
`0001_iam_relationships.sql` + `0002_iam_roles.sql` (source
`"authorization"`) into the host's ledger — apply before boot, never
renumber.

**Roles-only wiring** (the kind independence made visible — no model,
no engine):

```go
authorizer, err := authorization.NewService(authorization.Repositories{
    Roles: authzmem.NewRoles(), // Relationships nil = that kind OFF
}, authorization.Config{}) // no Model — a roles-only host never constructs one
```

**The composed-kinds closure** (refinement 13's reference pattern —
there is no built-in facade; the host owns the composition):

```go
authorize := func(ctx context.Context, p identity.Principal, rt, rid string) (bool, error) {
    res, err := authorizer.Check(ctx, authorization.CheckRequest{
        Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
        Permission: "view",
        Resource:   authorization.Resource{Type: rt, ID: rid},
    })
    if err != nil {
        return false, err // fail CLOSED on error
    }
    if res.Allowed {
        return true, nil
    }
    return authorizer.HasRole(ctx, authorization.Subject{Type: p.Type, ID: p.ID}, "auditor", rt, rid)
}
```

Anti-pattern, named: `allowed, _ := authorizer.Check(...)` is a silent
fail-OPEN — an engine error (store down, unwired kind) reads as a
decision. Always propagate the error and fail closed.

## Non-goals (cut lines)

- **No `sdk/authorization` port** and no `Config.Identity`-style
  consumer pairing (identity rides `sdk/foundation/identity`). Graduation trigger
  recorded above.
- **No PostfilterLoop** (§2.6 demand gate) — and the recorded
  constraint: a future enumeration-shaped consumer seam must ship paired
  with it.
- **No groups aggregate** (Q1 TRIM): the engine needs no groups table —
  expansion is pure tuples (`group:{id}#member@user:{x}`). Return
  trigger: the first named-group UX demand (an admin surface listing
  "who's in Engineering"); it lands as a follow-on with migration 0003+.
- **No composed Check facade** — hosts compose kinds in their own
  closures (the labeled snippet above).
- **No role registry/vocabulary** — roles are opaque strings (a role
  model is policy-seam-adjacent).
- **No policy kind in v1** — the policy-seam section is its ledger.
- **No routes, no HTML** in v1 — `Register` logs only; `/authorization/*`
  stays claimed-unregistered.
