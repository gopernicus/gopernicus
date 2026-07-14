# Phase 0 — security foundations

Status: DRAFT; ready after overview preflight and owner ratification.
Depends on: preflight only.

## Goal

Freeze the security and concurrency vocabulary before changing evaluation or
SQL. This phase may add new domain types, pure validators, reference
specification tests, and compile-safe optional ports; it must not fake atomicity
with existing multi-call services.

## Task AZ3-0.1 — exact subject, userset, decision, and error vocabulary

Touch: public authorization vocabulary, relationship domain, pure tests.

Implement:

- Define a canonical `SubjectRef{Type, ID, Relation}` contract. Empty Relation
  means a concrete subject; non-empty Relation means the exact userset
  `Type:ID#Relation`.
- Decide and document whether a decision request may itself name a userset.
  Recommended: support it exactly; do not silently drop `Subject.Relation`.
- Replace nullable/pointer ambiguity at the service boundary with one canonical
  zero/nonzero relation representation. Stores may still encode empty as `''`.
- Add stable reason/error codes for invalid request, unknown model symbol,
  denied, evaluation limit, stale revision, invariant conflict, mutation-ID
  replay, committed-effect-failed, and infrastructure failure.
- Align with the post-AV3-9.8 sdk taxonomy: backpressure/shutdown/degraded
  unavailability wraps the existing `sdk.ErrUnavailable` (mapped 503/`unavailable`
  by `web.ErrFromDomain`) — do not invent a new kind or reuse `sdk.ErrConflict`
  for saturation. Where named machine codes are needed, follow auth v3's
  precedent: a feature-local mapper seam over `web.RespondJSONDomainError`, with
  the sdk mapper untouched.
- Validate non-empty type/ID/relation/permission fields, bounded lengths, UTF-8,
  and reject control characters. Do not invent global lowercasing; names and IDs
  are opaque exact strings unless the host's schema says otherwise.
- Add table tests proving `group`, `group#member`, and `group#admin` never compare
  equal and that empty or malformed references are rejected.

Verify:

```sh
cd features/authorization && go test ./domain/... ./... -run 'Subject|Userset|Vocabulary|Invalid'
make guard
```

Acceptance: no public decision or mutation path can erase a non-empty userset
relation.

## Task AZ3-0.2 — strict schema compiler and immutable snapshot contract

Depends on: AZ3-0.1.
Touch: schema/model vocabulary, compiler package, tests; runtime wiring waits for
phase 1.

Implement:

- Introduce a compile step that deep-copies source maps/slices and returns an
  internal immutable representation plus a deterministic schema digest.
- Reject empty names, empty rules, duplicate declarations after composition,
  ambiguous checks (both Direct and Through or neither), unknown direct
  relations, unknown userset relations, and Through permissions missing from
  any possible resource target.
- Validate every `AllowedSubjects{Type, Relation}` pair: a non-empty Relation
  must exist on the referenced resource type and be meaningful as a userset.
- Preserve the sanctioned self-hierarchy shape while rejecting genuine cycles
  and globally unsatisfiable permission graphs.
- Sort aggregate validation errors deterministically.
- Define `SchemaSnapshot` as a deep copy/read-only projection. The internal
  compiled schema is never returned.
- Add mutation-after-compile and concurrent-read tests proving caller map edits
  cannot alter decisions or race the engine.

Verify:

```sh
cd features/authorization && go test -race ./internal/logic/authorizersvc/... -run 'Schema|Compile|Immutable'
make guard
```

Acceptance: an accepted schema is a fixed policy artifact identified by one
stable digest.

## Task AZ3-0.3 — evaluation limits and construction matrix

Depends on: AZ3-0.1, AZ3-0.2.
Touch: Config vocabulary, pure validation, construction tests.

Implement:

- Replace the single depth knob with a resolved `EvaluationLimits` contract:
  max Through depth, max relation targets/fan-out, max datastore queries, max
  batch size, max lookup results, and optional wall-clock budget derived from
  context deadline.
- Keep safe nonzero defaults. Negative values are errors; zero selects defaults,
  not unlimited. If an explicit unlimited mode is ever desired, it requires a
  separately named opt-in and is out of v3 by default.
- Define limit exhaustion as indeterminate/error. Middleware and helpers fail
  closed; Lookup never returns a truncated slice as complete.
- Define context cancellation behavior and ensure no store call begins after the
  budget/cancellation is observed.
- Add construction tests for zero/defaults, every invalid value, roles-only
  wiring, relationships-only wiring, and orphaned relationship-only settings.

Verify:

```sh
cd features/authorization && go test ./... -run 'Construction|Config|Limit|Budget'
make guard
```

Acceptance: no supported production query has an implicit unbounded work path.

## Task AZ3-0.4 — mutation, scope revision, idempotency, and disposition contract

Depends on: AZ3-0.1.
Touch: new mutation domain, port doc comments, storetest/reference specs only.

Implement:

- Define a required `MutationID`, actor-independent mutation payload, scope key,
  optional expected revision, operation, and requested relationship/role state.
- Define scope revisions: resource scope for relationship and scoped-role
  mutations; subject scope for global-role mutations.
- Define one atomic repository `Apply` contract that de-duplicates MutationID,
  validates expected revision when present, preserves invariants, applies all
  requested row changes or none, increments revision exactly once on a change,
  and returns the same receipt on replay.
- For actor-facing mutations, `ApplyGuarded` opens the atomic scope and supplies
  a transaction-bound decision view to a guard callback before any write. Guard
  denial/error rolls back; every authorization scope read by the guard is locked
  or revision-validated so a concurrent revoke cannot race a detached Check.
  Trusted-system Apply is the only no-guard variant.
- Stable dispositions: applied, unchanged replay/idempotent, stale revision,
  semantic conflict, invariant blocked, and not found. Do not encode conflict as
  `(nil, nil)`.
- Model grant, revoke, replace, resource purge, and role assign/unassign. Preserve
  one-relation-per-subject-per-resource pending owner ratification.
- Write reference specification cases for rollback, exact replay, mutation-ID
  payload mismatch, stale revision, concurrent single winner, and no partial
  batch.

Verify:

```sh
cd features/authorization && go test -race ./storetest ./domain/... -run 'Mutation|Revision|Idempot|Atomic'
make guard
```

Acceptance: the public contract cannot be implemented as read/check/write calls
without violating its tests or doc comments.

## Task AZ3-0.5 — actor, guard, audit, and effect-mode contracts

Depends on: AZ3-0.4.
Touch: public Config/service dependency types and pure construction tests.

Implement:

- Define `Actor` from the platform principal pair plus an explicit actor kind
  (`principal` or `system`). Empty principal actors are invalid.
- Define `MutationGuard.AuthorizeMutation` over actor, operation, target scope,
  proposed change, and a transaction-bound decision/check view supplied by the
  repository atomic operation. It returns allow or a stable denial/error; the
  feature supplies no default allow policy. A guard that depends on
  authorization data must use this view, never call the outer Service and create
  a check-then-write race.
- Define a visibly named trusted-system mutation capability for bootstrap,
  invitation acceptance, and controlled migrations. It must not be reachable
  from the HTTP handlers.
- Define optional best-effort `AuditSink` for accepted/denied/failed attempts.
  Its failures are warned with coarse fields and never change a committed
  mutation.
- Define `EffectMode` enum: off, procedural, events. Empty resolves to off.
  Orphaned handler/appender settings error; procedural requires a handler;
  events requires repository-declared atomic-outbox capability.
- Define committed-post-effect failure semantics and retry by MutationID.
- Do not add an authorization delivery-job repository or import events/jobs.

Verify:

```sh
cd features/authorization && go test ./... -run 'Actor|Guard|Audit|Effect|Construction'
make guard
```

Acceptance: untrusted mutation cannot be constructed without an actor and guard,
guarded data checks share the atomic mutation boundary, and effect guarantees
are explicit at boot.

## Phase acceptance

- Exact userset, immutable schema, bounded evaluation, atomic mutation, actor,
  and effect contracts are executable in tests.
- No current store adapter has been claimed atomic before phase 2.
- `make check` and `make guard` pass.

## Stop conditions

- Userset relation behavior remains ambiguous.
- A mutation contract needs two unlocked repository calls.
- A limit can silently return a partial allow/list.
- Effect mode would require a core feature import.

## Execution log

Append only during execution.
