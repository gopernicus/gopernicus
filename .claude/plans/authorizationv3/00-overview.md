# Authorization v3 hardening packet

Status: **DRAFT — audit complete; owner ratification required before execution.**
Working name: `authorizationv3`; task prefix: `AZ3`.
Depends on: auth v3 and `.claude/plans/authv3-delivery-refactor/` reaching the
combined AV3-9.7/9.8 closeout point, because the proof host and optional
administration/effects surfaces compose authentication identity, step-up, jobs,
and events with this feature without the feature cores importing one another.

## Outcome

Ship an authorization feature whose declared model, stored tuples, decisions,
enumeration, mutations, and emitted effects agree under malformed input,
concurrency, retries, multiple store dialects, and partial infrastructure.

Authorization v3 will provide:

- exact userset semantics: `group#member`, `group#admin`, and concrete subjects
  are distinct in validation, storage, direct checks, batch checks, lookup, and
  through traversal;
- an immutable, fully validated compiled schema with deterministic output and a
  stable digest;
- bounded, cancellation-aware decision evaluation with `Check`, `CheckBatch`,
  `FilterAuthorized`, and `LookupResources` parity;
- repository-atomic, idempotent, revisioned grant/revoke/replace operations,
  including single-winner last-owner protection;
- actor-aware mutation policy, explicit trusted-system mutation, and no
  accidental self-escalation in the optional HTTP surface;
- honest effective-role enumeration, including global fallback;
- optional mutation audit and effect delivery with an event-driven durable mode
  or a simple procedural post-commit mode, without an authorization-specific
  jobs table;
- optional, fail-closed JSON administration routes; and
- dual-dialect conformance, live concurrency proof, an upgrade runbook, and an
  auth-cms proof host.

The existing three postures remain intact: no authorization, a host-authored
closure, or the flagship authorization feature. No consumer feature is forced
to import this module or apply its migrations.

## Current-state audit

The current module is a strong v1 base: independently wireable relationship and
role kinds, schema validation, through traversal, middleware, memory/pgx/turso
stores, boot probes, and one shared adversarial conformance suite. Its hermetic
module suite is green as of 2026-07-13.

The hardening plan is driven by these concrete findings:

| Severity | Finding | Evidence / consequence | v3 disposition |
|---|---|---|---|
| critical | Userset relation is decorative at runtime | `ValidateRelation` checks only subject type; all stores expand hard-coded relation `member`; direct/batch/lookup joins ignore stored `subject_relation`; `Subject.Relation` is ignored. A schema declaring `group#admin` can behave like `group#member`, and a tuple missing its required userset relation is accepted. | Exact relation-aware subject state in every port/query and adversarial tests that prove no cross-relation grant. |
| critical | Validated schema is mutable | `NewService` retains caller maps and `GetSchema` returns the same maps. Policy can change after validation, including concurrently. | Deep-copy and compile once; return snapshots/read-only projections; carry schema digest. |
| critical | Last-owner protection is non-atomic | `RemoveMember` performs exists → count → delete as separate repository calls. Two owners can race to remove each other. | One repository mutation with invariant check, revision CAS, and exactly one winner. |
| high | Decision and enumeration disagree | The documented D1(b) hierarchy gap omits descendants of Through-derived roots. `LookupResources` also leaves a shared visited key set, so a second relation that traverses the same target permission can be suppressed. | Make lookup complete for all supported schema shapes and parity-test every Check allow against lookup. |
| high | Relationship changes have ambiguous outcomes | A conflicting create silently succeeds without changing the existing relation; callers must delete then create, creating a gap and race. | Atomic apply/replace with explicit `applied`, `unchanged`, `conflict`, and `stale` dispositions. |
| high | Mutation authority is entirely implicit | Service mutation methods carry no actor, policy decision, audit record, or idempotency key. The proof host role-assignment endpoint is session-gated only. | Actor-aware commands + required mutation guard for untrusted calls; explicit trusted-system path; hardened proof host. |
| high | Evaluation has no work budget | Check has a depth limit, but store group/descendant walks are unbounded and there is no fan-out, batch, result, or query budget. | Construction-time limits, per-decision budget, context checks, bounded lookup, and explicit `indeterminate/limit` errors that callers fail closed on. |
| medium | Role decision and listing disagree | A global role satisfies scoped `HasRole`, but resource listing returns direct scoped assignments only. A scoped revoke can report success while the global grant keeps access. | Effective listing and mutation receipts that report whether access remains via global fallback. |
| medium | Input and schema shape validation is incomplete | Empty names/IDs, ambiguous checks with both/neither direct/through fields, userset relation mismatch, and permissions missing on only some Through targets are not all rejected. | One structural validator used by every entry point and strict schema compilation. |
| medium | Results are operationally unstable | Map iteration affects schema errors, permission lists, lookup ordering, and some reasons; batch reasons can name a relation that did not grant. | Stable reason codes, sorted outputs, deterministic validation, optional bounded explain trace. |

## Lessons transferred from auth v3

1. **Freeze security contracts before adapters.** Atomicity, dispositions,
   revision rules, userset semantics, and effect guarantees land in public rim
   doc comments plus reference-memory conformance before dialect work.
2. **Atomicity belongs in repository operations.** No service-level
   check/count/delete or delete/create sequence may pretend to be atomic.
3. **Use one honest reference implementation and one shared suite.** Memory,
   pgx, and turso must authorize and mutate identically; concurrency promises
   run under `-race` and on both live dialects.
4. **Construction matrices prevent half-enabled systems.** Orphaned guards,
   event mode without atomic outbox support, procedural mode without a handler,
   and HTTP admin without protection fail at boot.
5. **Separate accepted, committed, and delivered.** A grant mutation can commit
   even if a post-commit procedural effect fails. Receipts and errors must make
   that state explicit and retry-safe.
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
- Core authorization does not own authentication recency. The optional inbound
  surface requires a host-supplied mutation protector; the host may adapt auth
  v3 recent-auth/step-up without a feature-to-feature import.
- There is no built-in decision cache in v3. Correctness and bounded evaluation
  land first. Mutation revisions/events make a future cache possible without
  inventing invalidation now.
- The deferred attribute-policy kind remains deferred. `MutationGuard` controls
  who may change authorization data; it is not the postponed ABAC policy kind.
- The administration surface is API-only in this packet. A templ/view module is
  demand-gated; authorization policy management does not need to copy auth's
  public account pages to be hardened.

## Jobs, events, and procedural effects

Authorization v3 must not create `authorization_delivery_jobs` or duplicate the
generic jobs queue. Mutations produce one stable change envelope and choose one
of three explicit effect modes:

| mode | mutation guarantee | effect guarantee | wiring |
|---|---|---|---|
| `off` | atomic mutation only | none | no handler or event appender |
| `procedural` | mutation commits first | best-effort post-commit call; failure is returned as committed-post-effect failure and is safe to retry by mutation ID | host closure; it may call a notifier directly or hand off to its own async implementation |
| `events` | mutation + event-outbox row commit together | at-least-once through the generic events poller; a subscriber may enqueue generic jobs for notification/webhook work | dialect store configured with the events store's matching `AppendTx`; host runs events/jobs at the composition root |

The core imports no feature module. Event envelopes use sdk event vocabulary;
dialect-specific `AppendTx` composition remains in the store tier, matching the
events feature's existing transactional-outbox design.

Denied and failed mutation attempts cannot share a domain commit. They go to an
optional best-effort `AuditSink` with coarse error classes. Successful durable
events use the mutation ID as the de-duplication key. Neither logs nor metrics
carry unbounded resource/subject IDs as labels.

### Auth v3 delivery follow-up (now planned)

`.claude/plans/authv3-delivery-refactor/` now owns the prerequisite decision and
implementation. Its settled direction is: generic jobs execute durable work;
optional events observe lifecycle; otherwise an explicitly selected bounded
in-process pool runs the same processor with documented crash loss. It preserves
opaque off-request-path resolution, encrypted payloads, idempotency, resend
supersession, checkpoint-before-send, retry/status, and enumeration parity.

Authorization v3 must not execute its effects phase against the old bespoke-auth
queue pattern. Preflight waits for the combined auth closeout so authorization's
procedural/events composition consumes the final shared jobs/events vocabulary.

## Recommended defaults requiring ratification

1. Preserve one-relation-per-subject-per-resource, but replace silent conflict
   with an explicit outcome and provide atomic Replace.
2. Make mutation IDs required and repository-idempotent.
3. Add revision anchors by authorization scope: resource scope for relationships
   and scoped roles; subject scope for global roles.
4. Require a `MutationGuard` for actor-facing mutation methods; keep a visibly
   named trusted-system method for bootstrap/invitation adapters.
5. Keep attribute policies and role implication out of v3. Add a host-supplied
   role-assignment validator/catalog seam, but roles remain host-interpreted.
6. Include the optional JSON admin surface after the core/store gates, with no
   HTML module in v3.
7. Because no module tags exist today, fold the final schema into a clean
   canonical migration set and publish a destructive/pre-tag adopter runbook.
   If any relevant tag exists at preflight, stop and switch to append-only
   migrations before implementation.

## Preflight gate

Before `AZ3-0.1`:

1. Finish auth v3 plus `.claude/plans/authv3-delivery-refactor/` through the
   combined AV3-9.7/9.8 closeout; do not mix auth delivery redesign into
   authorization implementation.
2. Run `git status --short` and preserve the current auth v3 worktree.
3. Run `make check`, `make guard`, and authorization's hermetic conformance.
4. Record pgx/turso live authorization conformance availability and DSNs.
5. Confirm no authorization or authorization-store tags exist. If they do,
   revise the migration strategy and upgrade runbook before code.
6. Ratify the seven defaults above and the route/effect vocabulary.
7. Read `ARCHITECTURE.md`, `features/README.md`, the authorization README, the
   events/jobs READMEs, and every touched module's `go.mod`.

## Phase queue

| Phase | File | Depends on | Gate produced |
|---|---|---|---|
| 0 | `01-security-foundations.md` | preflight | exact model, limit, mutation, revision, and effect contracts frozen |
| 1 | `02-decision-engine.md` | phase 0 | exact usersets + immutable schema + bounded Check/Lookup parity |
| 2 | `03-atomic-stores.md` | phases 0–1 | canonical schema and three atomic mutation repositories conform |
| 3 | `04-mutation-service.md` | phases 1–2 | actor-aware guarded relationship/role lifecycle |
| 4 | `05-effects-and-observability.md` | phases 2–3 | procedural/events modes and safe audit/decision observability |
| 5 | `06-admin-and-proof-host.md` | phases 3–4, stable auth v3 | protected JSON surface and end-to-end proof host |
| 6 | `07-docs-and-closeout.md` | phases 0–5 | upgrade/docs/live evidence/review remediation complete |

## Standing invariants

- `sdk` remains stdlib-only and contains no authorization policy model.
- The authorization core imports no other feature, store, integration, example,
  or concrete view module.
- A schema accepted at construction cannot change afterward.
- A tuple valid for `type#relation` is evaluated only as that exact userset.
- Every allowed `Check` for a supported finite query is discoverable by lookup;
  limit exhaustion is an explicit error, never a partial-success result.
- Every mutation is idempotent by mutation ID, atomic, revisioned, and returns
  an explicit disposition.
- For actor-facing writes, the authorization-data guard evaluates through the
  repository's transaction-bound decision view; guard and mutation serialize as
  one operation. Host authentication/step-up middleware remains an external
  precondition and is not misrepresented as database-atomic.
- Last-owner/guardian invariants have one database arbiter under concurrency.
- An untrusted mutation always has a non-empty actor and passes a host guard.
- Effects never determine whether a committed authorization mutation happened.
- Event mode means same-transaction outbox, not emit-after-commit dressed up as
  durable delivery.
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
- Reviewer-agent work, if used, is a single post-implementation wave in phase 6,
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
- allowing a mutation without actor/guard except the explicit trusted-system API;
- importing authentication, events, or jobs from the authorization core;
- claiming event durability without a same-transaction outbox append;
- adding the deferred ABAC policy kind or role implication;
- adding a decision cache before revision/invalidation semantics are proven;
- rewriting canonical migrations after a relevant module tag; or
- weakening host HTTP identity, step-up, origin/CSRF, or body protections.

## Milestone completion

Authorization v3 is complete only when every task is logged, all guards/tests
are green, live dialect races pass repeatedly, the migration runbook has been
executed, procedural and event-driven effects are both demonstrated, the admin
surface negative matrix passes, the proof-host transcript exists, and accepted
review findings are remediated and reverified.

## Execution log

Append only after owner ratification and task execution begins.
