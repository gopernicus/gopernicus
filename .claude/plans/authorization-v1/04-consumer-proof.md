# Phase Z4 — consumer seams + proof host (the three postures + both kinds, demonstrated)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: opus
Depends on: Z1 (hard), Z2a/Z2b (default order — this phase itself runs
zero-infra on `memstore/`, the events phase-4/5 independence precedent),
auth-v2 (shipped: invitations + the toy Granter + `RequireVerifiedEmail`
host config exist in `examples/auth-cms`)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2.1–§2.4,
§6 (the Granter seam + the milestone-stranding resolution this phase
completes), §13 Z4 **including the review-gate amendment mandating the
two demonstrations**. Host shape per **Q2** (overview) — this file is
written to Q2's recommended Option A (extend `examples/auth-cms`;
middle-posture demonstration as a two-commit protocol); if jrazmi picks
Option B, task-1 is re-cut as a new-example task before execution.
**Multi-kind (owner direction 2026-07-08):** the flagship host wires
BOTH kinds; this phase gains the roles-kind leg (task-4, protocol steps
10–11). The middle-posture demonstration is unchanged — it is now also
the story for "helpers for other kinds": a host satisfies any Check seam
with its own closure, no IAM in its graph.

**The consumer seam's REAL shape (drift 4 — adapt to this, not the
design's sketch):**

```go
// features/events/events.go:68 (shipped)
type AuthorizeStream func(ctx context.Context, principal identity.Principal, resourceType, resourceID string) (bool, error)
```

`identity.Principal{Type, ID}` maps onto
`authorization.Subject{Type: p.Type, ID: p.ID}` unadapted — pair-shaped,
machine principals flow (the design's §2.2 user-shaped worry is already
resolved in the shipped code). All wiring in this phase uses the **FS2
method form**: `svc, err := name.NewService(repos, cfg)` then
`svc.Register(mount)`.

## DoD

- **Demonstration (a) — the middle posture, made real:**
  `examples/auth-cms` satisfies `events.Config.Authorize` (a Check seam)
  with a **plain ownership closure over the toy membership map**, with
  **no ReBAC in its module graph** — `GOWORK=off go list -m all` output
  captured clean of `features/authorization` (review-gate fold, major 2:
  the workspace-independent form; under go.work the plain form lists
  every workspace module and would false-fail), the scoped stream driven
  live
  (member allowed, non-member denied). Landed and recorded as its own
  commit (commit 1) before the flagship arrives.
- **The flagship posture:** commit 2 mounts `features/authorization`
  (memstore-backed — the host's zero-infra character is preserved; zero
  drivers in the graph, re-asserted): model declared via
  `NewSchema`/`ResourceSchema` in `main`, A9's toy Granter **swapped**
  for a closure over `authorizer.CreateRelationships`, the same
  `AuthorizeStream` closure now backed by `authorizer.Check`, and the
  gated demo route checked through the engine.
- **Demonstration (b) — `LookupResources` exercised:** a host-local
  demo route ("list what this subject may view") backed by
  `authorizer.LookupResources`, driven live for a member (IDs), a
  non-member (empty), and — if the demo seeds the platform-admin tuple
  (review-gate fold, major 1: platform-admin is DATA, a
  `platform:main#admin@…` tuple over a `platform` resource type in the
  schema, never a Config field) — an admin (`Unrestricted`).
- **The roles-kind leg (owner direction):** the flagship host wires BOTH
  kinds (`Repositories{Relationships: …, Roles: …}`, both
  memstore-backed); a role assignment → a role-gated host check allows;
  without it, denies — driven live (task-4, protocol steps 10–11).
- The full real-interaction protocol below passed and recorded verbatim
  (commands, codes, frames). **Green tests alone do not close this
  phase.**
- Rule-6 greps clean both directions; `make check` green (33 modules);
  `examples/auth-cms/go.mod` gains `features/authorization` (+ sibling
  replace) at commit 2 only.

## Preconditions

- Z1 green (memstore + storetest); auth-v2's A9 protocol artifacts read
  (`.claude/past/auth-v2/09-proof-host.md`) — this phase extends that
  host and re-runs its invitation leg through the new Granter.
- Read `examples/auth-cms/cmd/server/{main,membership,demo}.go` fully:
  the toy Granter, `requireMembership`, the events mount
  (`Authorize` currently nil ⇒ the scoped route is unregistered), the
  `EVENTS_OUTBOX` variant, the shutdown order. Every edit here is
  host wiring (rule 8) — no feature code changes anywhere in Z4.
- Q2 ratified (this file assumes Option A).

## Tasks

### task-1: commit 1 — the middle posture (ownership closure, clean graph)

- **depends_on:** []
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/main.go,
  examples/auth-cms/cmd/server/membership.go,
  examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check`; the clean-graph assertion, captured into the execution log (workspace-independent form — review-gate fold, major 2): `cd examples/auth-cms && GOWORK=off go list -m all | grep -c authorization` → 0 (and `GOWORK=off go list -m all | grep -i libsql` → empty, the standing zero-driver claim); run-and-look: protocol steps 1–4 below
- **description:** Wire `events.Config.Authorize` with a host-authored
  closure reading the toy membership map (a `hasRelation`-style read
  added to `membership.go` beside `requireMembership`):

  ```go
  Authorize: func(ctx context.Context, p identity.Principal, resourceType, resourceID string) (bool, error) {
      return members.Has(resourceType, resourceID, demoRelation, p.Type, p.ID), nil
  },
  ```

  Non-nil `Authorize` registers the resource-scoped
  `GET /events/{resource_type}/{resource_id}` route (deny-by-absence
  ends here, deliberately). This commit IS design §2.1's middle posture
  row demonstrated: a Check seam satisfied by a plain host closure, no
  authorization module anywhere in `GOWORK=off go list -m all` — the
  ruling's point, demonstrated not asserted (the review-gate amendment's
  words). README gains a short "postures" paragraph naming this commit
  as the middle-posture reference. Commit message names the
  demonstration; the captured `GOWORK=off go list -m all` goes in the
  execution log.

### task-2: commit 2 — the flagship: model + engine + Granter swap + Check closure

- **depends_on:** [task-1]
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/main.go,
  examples/auth-cms/cmd/server/membership.go,
  examples/auth-cms/go.mod,
  examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 greps: `! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/` and the reverse for `features/authorization` over the other feature cores; run-and-look: protocol steps 5–9
- **description:** Wiring only (rule 8), FS2 method form throughout.
  (1) `go.mod` gains `features/authorization` + sibling replace —
  memstore-backed, so the graph stays driver-free (`GOWORK=off go list
  -m all | grep -i libsql` still empty). (2) Declare the model in
  `main`: `authorization.NewSchema(...)` with a `project` resource type,
  `owner`/`member` relations, a `view` permission =
  `AnyOf(owner, member)` (+ a `Through` example if it keeps the demo
  legible — the wiring page will reprint this; add a `platform` resource
  type + seed the `platform:main#admin@user:<seed-admin>` tuple if the
  step-8 Unrestricted leg is wanted — platform-admin is data, never
  Config). The model governs the relationship kind only — the roles kind
  (task-4) needs none. Note on `checkSelf` (review-gate fold, lead
  refinement 9):
  the demo's `view` permission on `project` sits outside checkSelf's
  scope (self-grants fire only for subject == resource on
  `user`/`service_account` types with read/update/delete), so no demo
  assertion can be silently satisfied by a self-grant — stated here so
  the executor doesn't trip on it. (3) Build the service, BOTH kinds
  wired (owner direction):
  `authorizer, err := authorization.NewService(
  authorization.Repositories{Relationships: mem, Roles: mem},
  authorization.Config{Model: model})` (adjust to memstore's landed
  constructor shape) and
  `authorizer.Register(mount)` (logs only — no routes; the FS2 shape on
  display). (4) **Swap the toy Granter** (design §6's promised
  completion): `auth.Granter` is a one-method INTERFACE
  (`features/authentication/authentication.go:103`), not a func type
  (review-gate fold, lead refinement 10) — add a small host-local
  adapter type in `membership.go` whose `Grant` method calls
  `authorizer.CreateRelationships`, and wire it into
  `auth.Config.Granter` (keeps the C2 zero-import proof exact; the toy
  `membership` type retires from the Granter seam — delete it only if
  nothing else reads it, otherwise leave and log). (5) The
  `AuthorizeStream` closure now
  delegates to the engine, exactly design §2.2's snippet adapted to the
  shipped signature:

  ```go
  Authorize: func(ctx context.Context, p identity.Principal, rt, rid string) (bool, error) {
      res, err := authorizer.Check(ctx, authorization.CheckRequest{
          Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
          Permission: "view",
          Resource:   authorization.Resource{Type: rt, ID: rid},
      })
      return res.Allowed, err
  },
  ```

  (adjust field names to Z1's landed API). (6) `requireMembership`'s
  gate re-reads through `authorizer.Check` (same closure shape). (7)
  Seed one owner tuple at boot via `CreateRelationships`
  (`project:demo#owner@user:<seed-admin>` — or grant on first verified
  registration; pick whichever the existing demo seeding supports, log
  the choice). No import edge between features anywhere — the host is
  the only place that knows both exist (C2).

### task-3: demonstration (b) — the `LookupResources` demo route

- **depends_on:** [task-2]
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/demo.go,
  examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check`; run-and-look: protocol step 8
- **description:** A small host-local session-gated route (the
  `/outbox-demo` precedent), e.g. `GET /demo/my-projects`: reads
  `identity.FromContext`, calls `authorizer.LookupResources(ctx,
  authorization.Subject{Type: p.Type, ID: p.ID}, "view", "project")`,
  responds via `sdk/web` responders with `{unrestricted, ids}` —
  exercising the enumeration API so it doesn't ship unexercised (the
  review-gate amendment's mandate) and displaying the
  `LookupResult.Unrestricted` contract in real JSON. README documents
  the route as demo-only host surface (not feature API), and repeats the
  §2.4 line: enumeration is flagship-specific API, never a consumer
  seam.

### task-4: the roles-kind leg (owner direction)

- **depends_on:** [task-2]
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/demo.go,
  examples/auth-cms/cmd/server/main.go,
  examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check`; run-and-look: protocol steps 10–11
- **description:** Prove the roles kind end to end, host wiring only —
  deliberately DISTINCT from the relationship demo (two kinds, two
  checks, no entanglement): a host-local role-gated route (e.g.
  `GET /demo/audit`) whose gate reads `identity.FromContext` and calls
  `authorizer.HasRole(ctx, authorization.Subject{Type: p.Type, ID:
  p.ID}, "auditor", demoResourceType, demoResourceID)` (adjust to Z1's
  landed signature) — 403 on false, 200 on true; plus a minimal
  assignment path — either a seed-admin-gated host route
  (`POST /demo/roles/assign`, the `/outbox-demo` precedent) or boot-time
  seeding via `authorizer.AssignRole`; pick whichever keeps `main`
  legible, log the choice — **plus an unassign path** (the revoke leg,
  re-review lead minor 11) and **one driven
  `authorizer.ListRoleAssignmentsByResource` HTTP call** (a demo
  read-back route or an extension of `/demo/audit`'s response —
  symmetry with demonstration (b); the ListByResource direct-scope-only
  pin, re-review lead major 3, makes the listing worth exercising and
  its blind spot worth SEEING live). If Q5 ratified the global fallback,
  the protocol also drives it once (a GLOBAL "auditor" assignment
  satisfies
  the scoped gate). README: one paragraph — the roles kind is
  independently wireable; a roles-only host would wire
  `Repositories{Roles: …}` alone and never construct a model.

## The real-interaction protocol (recorded verbatim in the execution log — commands, ports, exact codes, observed frames)

Boot: `cd examples/auth-cms && go run ./cmd/server` (:8082 per README
env; `RequireVerifiedEmail=true` per the A9 host config; cookie jars per
user).

**Commit-1 legs (middle posture — run BEFORE task-2 lands):**

1. `GOWORK=off go list -m all` captured: no `features/authorization`,
   no libsql (the workspace-independent form — review-gate fold,
   major 2).
2. Register + verify + login user B; invite B to `project/demo`
   (relation `member`) from the seed user via
   `POST /auth/invitations/project/demo`; accept → toy Granter records
   membership (the auth-v2 A9 flow, unchanged).
3. `curl -N -b jarB /events/project/demo` → stream opens (ownership
   closure allows); heartbeats arrive. The scoped route EXISTS now
   (Authorize non-nil). Connection authorization is what's under test —
   scoped delivery filtering was proven at events-v1 (P4). Optional
   frame proof: point the `/outbox-demo` record's aggregate at
   (`project`, `demo`) in the `EVENTS_OUTBOX=memory` variant and watch
   the frame arrive on the scoped stream (no cms emitter carries
   project-shaped metadata, so don't assert one).
4. Login user C (no membership): `curl -N -b jarC /events/project/demo`
   → 403. Unauthenticated → 401. Record all codes.

**Commit-2 legs (flagship):**

5. Fresh boot. Invite → accept as user B: the accept now grants through
   `authorizer.CreateRelationships` — assert the tuple exists (log line
   or a debug read via the demo route), then `curl -N -b jarB
   /events/project/demo` → stream opens (**Check allows**), and the
   gated demo surface (`requireMembership`-successor) → 200.
6. User C (no tuple): scoped stream → 403; gated surface → 403/401.
   **Denies without the tuple** — the design's run-and-look clause.
7. Decline/cancel paths still behave (spot-check one — invitations'
   own semantics must be untouched by the Granter swap).
8. `GET /demo/my-projects` as B → `{"unrestricted": false, "ids":
   ["demo"]}`; as C → empty ids; if the demo declared the `platform`
   resource type and seeded the `platform:main#admin@user:<seed-admin>`
   tuple (task-2 point 2 — data, not Config): as that admin subject →
   `{"unrestricted": true}`.
9. Ctrl-C → the documented shutdown order (HTTP → poller pool if the
   outbox variant is on → bus.Close), clean exit, port free.

**Roles-kind legs (task-4 — owner direction):**

10. As user B (no role): `GET /demo/audit` → 403. Assign the `auditor`
    role to B on `project/demo` (the task-4 assignment path); repeat →
    200. **Assign → allows; without → denies.** Then the driven listing:
    `ListRoleAssignmentsByResource(project/demo)` shows B's scoped
    assignment. **Revoke leg (re-review lead minor 11):** scoped
    `UnassignRole` for B → `GET /demo/audit` → 403 again. Note while
    driving it: a scoped unassign revokes only the scoped grant — a
    GLOBAL grant, if one existed, would keep the gate open (the
    lingering-global-grant footgun; record the observation).
11. As user C (never assigned): `GET /demo/audit` → 403 throughout. If
    Q5 ratified the global fallback: assign C the GLOBAL `auditor` role
    → the scoped gate now 200s for C (the fallback driven live) — and
    the `ListRoleAssignmentsByResource(project/demo)` read-back does
    NOT show C (the lead-major-3 enumeration-vs-decision divergence,
    observed live and recorded); unassign
    → 403 again. Record exact codes.

## Acceptance

```sh
cd examples/auth-cms && go build ./... && go vet ./... && go test ./...
make check     # 33 modules
make guard     # G7 continuously proves rule 6 across the new edges
```

Rule-6 greps both directions (import-anchored) — empty; the commit-1
`GOWORK=off go list -m all` capture present in the execution log; the protocol
transcript complete (steps 1–11, roles leg included). The two mandated
demonstrations each traceable to a
commit hash in the log.

## Real-interaction check

The protocol above IS this phase's check — plus standing check (a)
(`examples/minimal` :8081 → 200s) at each commit boundary.

## Execution log

(append dated entries here; commit hashes for commit 1 / commit 2 are
load-bearing — Q2 Option A makes the middle-posture demonstration a
recorded-protocol-plus-commit artifact)
