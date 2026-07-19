# Authorization v3 hardening packet

Status: **RATIFIED — execution in progress (2026-07-14).** Ratified basis:
the ten recommended defaults below at their recommended values, plus owner
rulings R1–R4 (nested userset membership IN / rewrite operators OUT; separate
proof phase with the reopen rule; AZADM blocked indefinitely; `iam_*` table
prefix). Ratified 2026-07-14 by jrazmi via the execution-loop handoff.
Working name: `authorizationv3`; task prefix: `AZ3`.
Depends on: auth v3 and `.claude/plans/authv3-delivery-refactor/` reaching the
combined AV3-9.7/9.8 closeout point — **satisfied 2026-07-14** (AV3-9.8 complete;
completion record in `.claude/past/authv3/10-docs-and-closeout.md`). The shared
`identity.Principal`, live-session gate, and jobs/events vocabulary are stable.
Authentication's recent-auth consume operation and browser-safe mutation gate
are not currently public host seams; a generic authorization admin adapter that
claims auth-v3 step-up composition therefore remains a follow-up prerequisite,
not a satisfied v3 premise.

## Outcome

Ship an authorization feature whose declared model, stored tuples, decisions,
enumeration, and mutations agree under malformed input, concurrency, retries,
multiple store dialects, and partial infrastructure.

Authorization v3 will provide:

- exact userset semantics for stored tuples: `group`, `group#member`, and
  `group#admin` are distinct subject references in validation, storage, direct
  checks, batch checks, and lookup;
- concrete-principal decision requests: `Check`, batch, filter, and lookup take
  `PrincipalRef{Type, ID}` and never accept or erase a userset relation;
- an immutable, fully validated compiled schema with deterministic output and a
  stable digest;
- bounded, cancellation-aware decision evaluation with `Check`, `CheckBatch`,
  `FilterAuthorized`, and `LookupResources` parity;
- repository-atomic, idempotent, revisioned grant/revoke/replace operations,
  including single-winner last-owner protection;
- actor-aware mutation policy with revision-tracked authorization dependencies,
  plus a separate trusted `SystemMutator` capability;
- honest effective-role enumeration, including global fallback;
- optional best-effort mutation audit, with effects delivery deferred until its
  retry/cardinality contract is separately ratified; and
- dual-dialect conformance, live concurrency proof, an upgrade runbook, and a
  host-composition proof without a generic authorization admin API.

The existing three postures remain intact: no authorization, a host-authored
closure, or the flagship authorization feature. No consumer feature is forced
to import this module or apply its migrations.

## Core mental model

1. A caller is a concrete `PrincipalRef{Type, ID}`. A stored relationship
   subject is an exact `SubjectRef{Type, ID, Relation}`; non-empty Relation names
   a userset. These are intentionally different types.
2. A navigational `Through` relation points only to concrete resource subjects.
   Usersets may satisfy direct relations; v3 supports nested userset membership
   traversal but defines no userset rewrite operators (union/intersection/
   exclusion, computed/tuple-to-userset) and no userset-valued decision request.
3. A decision is allow, deny, or indeterminate error. Invalid input,
   cancellation, infrastructure failure, and limit exhaustion never masquerade
   as an ordinary deny or a complete partial list.
4. Every write is one atomic command with a MutationID, one mutation scope, an
   optional expected revision, and an explicit domain outcome. Replay is a
   separate fact from that outcome.
5. A guarded write commits only if every authorization scope used by its guard
   has the same revision when the repository locks and validates dependencies.
6. Durable side effects require a same-transaction outbox. The v3 correctness
   kernel emits no effects; effects and a generic admin API are follow-ups.

## Current-state audit

The current module is a strong v1 base: independently wireable relationship and
role kinds, schema validation, through traversal, middleware, memory/pgx/turso
stores, boot probes, and one shared adversarial conformance suite. Its hermetic
module suite is green as of 2026-07-13, and the module was not modified by the
AV3-9.8 remediation, so every finding below was re-verified against the
post-remediation tree on 2026-07-14 (e.g. the pgx CTE still hard-codes
`relation = 'member'`).

The hardening plan is driven by these concrete findings:

| Severity | Finding | Evidence / consequence | v3 disposition |
|---|---|---|---|
| critical | Userset relation is decorative at runtime | `ValidateRelation` checks only subject type; all stores expand hard-coded relation `member`; direct/batch/lookup joins ignore stored `subject_relation`; `Subject.Relation` is ignored. A schema declaring `group#admin` can behave like `group#member`, and a tuple missing its required userset relation is accepted. | Exact relation-aware stored-subject state in every port/query; decision requests become concrete-principal-only; adversarial tests prove no cross-relation grant. |
| critical | Validated schema is mutable | `NewService` retains caller maps and `GetSchema` returns the same maps. Policy can change after validation, including concurrently. | Deep-copy and compile once; return snapshots/read-only projections; carry schema digest. |
| critical | Last-owner protection is non-atomic | `RemoveMember` performs exists → count → delete as separate repository calls. Two owners can race to remove each other. | One repository mutation with invariant check, revision CAS, and exactly one winner. |
| high | Decision and enumeration disagree | The documented D1(b) hierarchy gap omits descendants of Through-derived roots. `LookupResources` also leaves a shared visited key set, so a second relation that traverses the same target permission can be suppressed. | Make lookup complete for all supported schema shapes and parity-test every Check allow against lookup. |
| high | Relationship changes have ambiguous outcomes | A conflicting create silently succeeds without changing the existing relation; callers must delete then create, creating a gap and race. | Atomic apply/replace with explicit domain outcomes, separate stale errors, and independent replay metadata. |
| high | Mutation authority is entirely implicit | Service mutation methods carry no actor, policy decision, audit record, or idempotency key. The proof host role-assignment endpoint is session-gated only. | Actor-aware commands + required mutation guard for untrusted calls; a distinct trusted `SystemMutator`; guarded service proof. Generic HTTP administration waits for a public authentication sensitive-operation seam. |
| high | Evaluation has no work budget | Check has a depth limit, but store group/descendant walks are unbounded and there is no fan-out, batch, result, or query budget. | Construction-time limits, per-decision budget, context checks, bounded lookup, and explicit `indeterminate/limit` errors that callers fail closed on. Query count is bounded adapter-local telemetry with an emergency ceiling, not a cross-store semantic budget. |
| medium | Role decision and listing disagree | A global role satisfies scoped `HasRole`, but resource listing returns direct scoped assignments only. A scoped revoke can report success while the global grant keeps access. | Effective listing with provenance and mutation results that report whether the same role grant remains via global fallback, without claiming all host-composed access remains. |
| medium | Input and schema shape validation is incomplete | Empty names/IDs, ambiguous checks with both/neither direct/through fields, userset relation mismatch, and permissions missing on only some Through targets are not all rejected. | One structural validator used by every entry point and strict schema compilation. |
| medium | Results are operationally unstable | Map iteration affects schema errors, permission lists, lookup ordering, and some reasons; batch reasons can name a relation that did not grant. | Stable reason codes, sorted outputs, deterministic validation, optional bounded explain trace. |

## Lessons transferred from auth v3

1. **Freeze security contracts before adapters.** Atomicity, outcomes/replay,
   revision rules, userset semantics, and guard dependencies land in public rim
   doc comments plus reference-memory conformance before dialect work. Effect
   guarantees follow the same rule in their later packet.
2. **Atomicity belongs in repository operations.** No service-level
   check/count/delete or delete/create sequence may pretend to be atomic.
3. **Use one honest reference implementation and one shared suite.** Memory,
   pgx, and turso must authorize and mutate identically; concurrency promises
   run under `-race` and on both live dialects.
4. **Construction matrices prevent half-enabled systems.** Orphaned guards,
   invalid limits, partially modeled relationship kinds, and guarded mutation
   without a guard fail at boot. Effects/admin matrices belong to their
   follow-up packets.
5. **Separate accepted, committed, and delivered.** V3 models accepted versus
   committed only. A later effects packet must keep delivery separate and may
   not infer external exactly-once behavior from mutation idempotency.
6. **Negative and live proof are milestone work.** Compile-only and hermetic
   green are not enough for database races, HTTP protection, or worker wiring.
7. **Upgrade work is part of the feature.** Canonical migration parity, an
   adopter-owned upgrade path, and proof-host carry are planned before closeout.
8. **Keep an execution log and premise adaptations.** A dialect limitation that
   contradicts the atomic port stops the task; it is never hidden with a weaker
   implementation. Auth v3's Turso `BEGIN IMMEDIATE` finding is the precedent.

## Intentional differences from auth v3

- Authorization has no passwords, tokens, identifier enumeration response, or
  transport secrets. It does not copy challenge protectors, recovery flows, or
  a delivery-job domain.
- Core authorization does not own authentication recency. A future inbound
  surface requires a public host-supplied sensitive-operation protector; the
  current authentication feature does not yet export that full composition seam.
- There is no built-in decision cache in v3. Correctness and bounded evaluation
  land first. Mutation revisions/events make a future cache possible without
  inventing invalidation now.
- The deferred attribute-policy kind remains deferred. `MutationGuard` controls
  who may change authorization data; it is not the postponed ABAC policy kind.
- The deferred administration follow-up is API-only. A templ/view module remains
  demand-gated.

## Effects and administration follow-up boundary

Authorization v3 does not create an authorization delivery queue, dispatch a
post-commit callback, append an event, or mount generic administration routes.
It lands the mutation identity, revision, receipt, and audit vocabulary needed
by later adapters without claiming delivery behavior.

`.claude/plans/authorizationv3/05-effects-and-observability.md` records the
follow-up effect design. Before execution it must ratify command/event
cardinality and choose an honest procedural guarantee: at-least-once attempts
with a MutationID-idempotent handler, or at-most-once best effort. Domain
mutation idempotency alone cannot prove a procedural side effect was not
duplicated after an ambiguous response. Durable mode continues to require a
same-transaction events outbox and never an authorization-specific jobs table.

`.claude/plans/authorizationv3/06-admin-and-proof-host.md` records the follow-up
generic admin design. It may not execute until authentication exports a
host-facing sensitive-operation protector covering live session,
origin/CSRF, and operation-bound recent-auth consumption. That seam can only
arrive through a separately ratified authentication follow-up packet; no such
packet currently exists or is implied, so AZADM is blocked indefinitely —
unschedulable, not merely sequenced. Authorization must never unblock itself by
importing authentication internals. V3 proves guarded and
trusted composition through host code and tests without shipping that API.

Denied and failed mutation attempts cannot share a domain commit. V3 sends them
only to an optional best-effort `AuditSink` with coarse error classes. Neither
logs nor metrics carry unbounded resource/subject IDs as labels.

### Auth v3 delivery follow-up (now shipped)

`.claude/plans/authv3-delivery-refactor/` owned the prerequisite decision and
implementation, and it is complete. The shipped shape is: generic jobs execute
durable work through the settled work protocol — `sdk/capabilities/work` (a new
first-tag module) with typed `work.Status` and opaque `[]byte` payloads —
implemented by `features/jobs` (tag floor MINOR), whose fenced surface includes
`PurgeTerminal`; optional events observe lifecycle; otherwise an explicitly selected
bounded in-process pool runs the same processor with documented crash loss. It
preserves opaque off-request-path resolution, encrypted payloads, idempotency,
resend supersession, checkpoint-before-send, retry/status, and enumeration
parity. The auth-cms proof host runs the in-memory fenced jobs mode and
documents it honestly as non-durable, with a supervised delivery runtime.

Any authorization effects follow-up must consume this final shared jobs/events
vocabulary and must not revive the old bespoke-auth queue pattern.

## Recommended defaults requiring ratification

1. Preserve one resource relation per exact `SubjectRef` per resource, but
   replace silent conflict with an explicit outcome and provide atomic Replace.
2. Make globally unguessable mutation IDs required and repository-idempotent;
   record domain outcome separately from replay. Default receipt retention is
   permanent; any finite window is an explicit weaker idempotency posture with a
   ratified minimum and operational cleanup contract.
3. Add revision anchors by authorization scope: resource scope for relationships
   and scoped roles; subject scope for global roles.
4. Require a `MutationGuard` for actor-facing mutation methods; expose bootstrap
   and invitation mutation through a separate `SystemMutator` capability, never
   an actor-kind flag on the ordinary service.
5. Keep attribute policies, role implication, and a role catalog out of v3.
   Roles remain opaque; host/admin policy owns any catalog. Core role methods
   validate only structural subject, role, and scope shape.
6. Defer effects and the generic JSON admin surface to separately ratified
   follow-ups. V3 still proves guarded composition in auth-cms.
7. Because no module tags exist today, fold the final schema into a clean
   canonical migration set and publish a destructive/pre-tag adopter runbook.
   If any relevant tag exists at preflight, stop and switch to append-only
   migrations before implementation.
8. Separate outcomes from replay: `Receipt.Replayed` is independent metadata,
   never a domain outcome; conflict is never encoded as `(nil, nil)`; stale
   revision and MutationID payload mismatch are command errors, not outcomes
   (per AZ3-0.4).
9. Limit-exhaustion taxonomy: evaluation budget exhaustion is an
   indeterminate/error wrapping `sdk.ErrUnavailable` — never a new error kind
   and never `sdk.ErrConflict` (per AZ3-0.1).
10. Guardian-minimum defaults: the default protected relation set may include
    `owner`; legacy-orphan scopes and member/role-first commands are blocked
    until a trusted repair establishes the minimum (per AZ3-3.2 and the
    mutation-service phase).

## Preflight gate

Before `AZ3-0.1`:

1. **Satisfied 2026-07-14:** auth v3 plus `.claude/plans/authv3-delivery-refactor/`
   reached the combined AV3-9.7/9.8 closeout; auth delivery redesign is done and
   stays out of authorization implementation.
2. Run `git status --short` and preserve the current auth v3 worktree.
3. Run `make check`, `make guard`, and authorization's hermetic conformance.
4. Record pgx/turso live authorization conformance availability and DSNs. The
   auth v3 live legs ran against the standing `authv3-pg` (C-collation
   PostgreSQL — pgx test databases must be C-collation) and `authv3-libsql`
   containers; reuse that environment, reset/fresh databases per leg, and always
   pass `-count=1` on live legs so the Go test cache cannot replay results
   across database resets.
5. Confirm no authorization or authorization-store tags exist. If they do,
   revise the migration strategy and upgrade runbook before code. Note the
   consistency dependency: the packet's pre-tag breaking policy (the
   `Components` bundle, removing raw Create/Delete) holds only while no
   `features/authorization` tag exists; cutting the pending `RELEASING.md`
   minor tag first would flip this packet to append-only/non-breaking, so
   preflight must re-verify zero tags.
6. Ratify the ten defaults above, concrete-principal decision boundary,
   navigational-Through restriction, dependency-revision protocol, receipt
   retention, and resource-teardown invariant.
7. Read `ARCHITECTURE.md`, `features/README.md`, the authorization README, the
   events/jobs READMEs, and every touched module's `go.mod`.

## Phase queue

| Phase | File | Depends on | Gate produced |
|---|---|---|---|
| 0 | `01-security-foundations.md` | preflight | exact model, limit, mutation, revision, guard, and audit contracts frozen |
| 1 | `02-decision-engine.md` | phase 0 | exact usersets + immutable schema + bounded Check/Lookup parity |
| 2 | `03-atomic-stores.md` | phases 0–1 | canonical schema and three atomic mutation repositories conform |
| 3 | `04-mutation-service.md` | phases 1–2 | actor-aware guarded relationship/role lifecycle |
| 4 | `07-docs-and-closeout.md` (phase-4 section) | phases 0–3 | guarded/SystemMutator proof host and exact-semantics/concurrency transcript |
| 5 | `07-docs-and-closeout.md` (phase-5 section) | phase 4 gate | upgrade/docs/release/live evidence/review remediation complete |

`05-effects-and-observability.md` and `06-admin-and-proof-host.md` are retained
as explicitly non-blocking follow-up packets. Their tasks are not part of the v3
completion gate.

## Standing invariants

- `sdk` remains stdlib-only and contains no authorization policy model.
- The authorization core imports no other feature, store, integration, example,
  or concrete view module.
- A schema accepted at construction cannot change afterward.
- A tuple valid for `type#relation` is evaluated only as that exact userset.
- Decision requests contain a concrete principal only. A non-empty relation at
  a decision boundary is invalid, never ignored.
- A relation used for `Through` contains concrete resource targets only.
- Every allowed `Check` for a supported finite query is discoverable by lookup;
  limit exhaustion is an explicit error, never a partial-success result.
- Every mutation is idempotent by mutation ID for at least the configured
  receipt-retention window (permanent by default), atomic, revisioned, and
  returns an explicit domain outcome plus an independent replay flag.
- For actor-facing writes, the authorization-data guard evaluates through the
  repository's dependency-tracking decision view. Every read scope and revision
  is recorded; the repository locks dependency anchors in canonical order and
  validates them before commit. Host authentication/step-up remains an external
  precondition and is not misrepresented as database-atomic.
- Last-owner/guardian invariants have one database arbiter under concurrency.
- Resource teardown is a distinct `SystemMutator` operation with an explicit
  reason; ordinary purge cannot silently bypass guardian invariants. Host
  resource-delete ordering is documented rather than misrepresented as
  cross-feature database atomicity.
- An untrusted mutation always has a non-empty actor and passes a host guard.
- Trusted mutation is available only through the separately held
  `SystemMutator`, not `Actor{Kind: system}`.
- Middleware and consumers propagate decision errors and fail closed.
- pgx, turso, and the reference memory store implement the same public contract.
- Both dialect migration trees have identical filename sets.
- Production/example wiring never relies on an authorization-specific jobs
  queue or a goroutine silently started by `Register`.

## Iteration protocol

- Execute one `AZ3-x.y` task at a time in dependency order.
- Read this overview, the full phase file, and referenced current code first.
- Contract tests land in the same task as the contract; adapters never weaken a
  test to match a dialect.
- Run the task verification, then the phase gate on the last task.
- Append a dated execution-log entry to the phase file with files, exact
  commands, pass/skip/failure, live environment, and premise adaptations.
- Mark only verified tasks in `TASKS.md`.
- After two failed repairs on the same root cause, stop and report it.
- Reviewer-agent work, if used, is a single post-implementation wave in phase 5,
  not repeated design churn between implementation tasks.

## Standing verification

Each code task runs the narrowest package tests plus `make guard`. Each phase
closes with:

```sh
make check
make guard
```

Decision/memory/concurrency work also uses `go test -race`. Store phases require
live pgx and turso legs; a skip is recorded but cannot close the milestone.

## Global stop conditions

Stop for an owner decision if implementation would require:

- treating all usersets as `member` or ignoring `Subject.Relation`;
- retaining a mutable or partially validated schema;
- implementing last-owner protection outside one repository atomic operation;
- authorizing an actor-facing data mutation with a detached Check followed by
  Apply when the guard depends on authorization data;
- returning partial lookup results as complete after a work limit;
- allowing a mutation without actor/guard except through the separately held
  `SystemMutator` capability;
- importing authentication, events, or jobs from the authorization core;
- adding effects/admin work to the v3 gate without separately ratifying its
  prerequisites and guarantees;
- adding the deferred ABAC policy kind or role implication;
- adding a decision cache before revision/invalidation semantics are proven;
- rewriting canonical migrations after a relevant module tag; or
- shipping a generic admin route before its identity, sensitive-operation,
  origin/CSRF, body, and anti-enumeration contracts are separately proven.

## Milestone completion

Authorization v3 is complete only when every non-deferred task is logged, all
guards/tests are green, live dialect races pass repeatedly, the migration
runbook has been executed, the guarded/system proof-host transcript exists, and
accepted review findings are remediated and reverified. Effects and generic
administration are not completion conditions for this milestone.

## Execution log

Append only after owner ratification and task execution begins.
