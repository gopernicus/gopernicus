# Phase 0 — security foundations

Status: DRAFT; ready after overview preflight and owner ratification.
Depends on: preflight only.

## Goal

Freeze the security and concurrency vocabulary before changing evaluation or
SQL. This phase may add new domain types, pure validators, reference
specification tests, and compile-safe optional ports; it must not fake atomicity
with existing multi-call services.

## Task AZ3-0.1 — exact principal, subject, userset, decision, and error vocabulary

Touch: public authorization vocabulary, relationship domain, pure tests.

Implement:

- Define `PrincipalRef{Type, ID}` for decision callers and actors. It is always
  concrete and directly convertible from `identity.Principal` at the host.
- Define `SubjectRef{Type, ID, Relation}` for stored relationship subjects.
  Empty Relation means a concrete subject; non-empty Relation means the exact
  userset `Type:ID#Relation`.
- `Check`, `CheckBatch`, `FilterAuthorized`, and `LookupResources` accept only
  `PrincipalRef`. Remove the optional Relation field from the decision request
  vocabulary. V3 does not expose a userset-valued Check API; adding one later
  requires separately defined exact-match/set semantics.
- Replace nullable/pointer ambiguity at the service boundary with one canonical
  zero/nonzero relation representation. Stores may still encode empty as `''`.
- Add stable reason/error codes for invalid request, unknown model symbol,
  denied, evaluation limit, stale revision, invariant conflict, mutation-ID
  payload mismatch, and infrastructure failure. Replay is receipt metadata, not
  an error or domain outcome. Effect errors belong to the follow-up packet.
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

Acceptance: no public decision path can carry a userset relation, and no tuple
or mutation path can erase a non-empty stored userset relation.

## Task AZ3-0.2 — strict schema compiler and immutable snapshot contract

Depends on: AZ3-0.1.
Touch: schema/model vocabulary, compiler package, tests; runtime wiring waits for
phase 1.

Implement:

- Introduce a compile step that deep-copies source maps/slices and returns an
  internal immutable representation plus a deterministic schema digest.
- Define a versioned canonical schema encoding: sorted resource/relation/
  permission symbols; `AllowedSubjects` and `AnyOf` normalized as duplicate-free
  sorted semantic sets; resolved defaults included; source map iteration/debug
  text excluded. Hash the version prefix plus canonical bytes with SHA-256 and
  publish the encoding version beside the digest.
- Reject empty names, empty rules, duplicate declarations after composition,
  ambiguous checks (both Direct and Through or neither), unknown direct
  relations, unknown userset relations, and Through permissions missing from
  any possible resource target.
- Validate every `AllowedSubjects{Type, Relation}` pair: a non-empty Relation
  must exist on the referenced resource type and be meaningful as a userset.
- Classify relations as direct-subject relations or navigational relations from
  their compiled use. Any relation referenced by `Through` must allow concrete
  resource subjects only; reject userset targets on that relation. V3 does not
  interpret a permission against `resource#relation`.
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

- Replace the single depth knob with a resolved semantic `EvaluationLimits`
  contract: max Through depth, max expanded graph states/edges, max relation
  targets/fan-out, max batch size, and max lookup results. Lookup queries fetch
  at most max+1 so overflow is distinguishable from a complete bounded result.
- Query count is observer telemetry and may have an adapter-local emergency
  ceiling, but is not a cross-store semantic budget: optimized SQL and the
  reference memory graph naturally use different query counts.
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

## Task AZ3-0.4 — mutation, scope revision, idempotency, outcome, and replay contract

Depends on: AZ3-0.1.
Touch: new mutation domain, port doc comments, storetest/reference specs only.

Implement:

- Define a required cryptographically strong `MutationID`, actor-independent
  mutation payload, scope key, optional expected revision, operation, and
  requested relationship/role state. Define the canonical, versioned encoding
  used for its payload digest.
- Define scope revisions: resource scope for relationship and scoped-role
  mutations; subject scope for global-role mutations.
- One command mutates exactly one scope. A bounded batch may contain multiple
  changes only within that scope; v3 exposes no cross-resource/cross-subject
  atomic batch whose lock set and revision meaning would be ambiguous.
- Define one atomic repository `Apply` contract that de-duplicates MutationID,
  validates expected revision when present, preserves invariants, applies all
  requested row changes or none, increments revision exactly once on a change,
  and returns the same receipt on replay.
- Pin validation order across schema upgrades: parse and structurally validate
  the command, authorize the current actor, then let Apply check MutationID and
  payload digest. An exact stored replay returns its original receipt even if
  the current schema no longer accepts that old relation. Only a receipt-absent
  command runs current-schema semantic validation. Supply that validation as a
  pure callback/compiled command step inside Apply; it performs no I/O. Receipts
  record the schema digest that governed the original application.
- For actor-facing mutations, `ApplyGuarded` supplies a dependency-tracking
  decision view. Every authorization read records its scope key and revision.
  Before committing, the repository locks the mutation scope plus dependency
  anchors in canonical order and validates every observed revision; a mismatch
  returns stale and writes nothing. The callback is synchronous,
  cancellation-bound, and may make authorization reads only through the view;
  it must not call the outer Service or perform network/unrelated store I/O.
- Actor-facing replays run the guard against current authorization state before
  returning a stored receipt; possession or guessing of a MutationID is not
  mutation authority. Trusted `SystemMutator` replay bypasses only that guard.
- Stable domain outcomes: applied, no_change, semantic_conflict,
  invariant_blocked, and not_found. `Receipt.Replayed` is independent metadata.
  Stale expected/dependency revision and MutationID payload mismatch are command
  errors, not successful outcomes. Do not encode conflict as `(nil, nil)`.
- Specify receipt persistence: committed applied/no_change/not_found receipts
  are replayable; validation, denial, stale, payload mismatch, cancellation, and
  infrastructure failures do not create a receipt. Permanent retention is the
  default. A finite retention window is an explicit weaker posture with a
  ratified minimum and cleanup API/runbook; idempotency is guaranteed only
  inside that window and MutationID reuse remains forbidden.
- Model grant, revoke, replace, resource purge, and role assign/unassign. Preserve
  one resource relation per exact `SubjectRef` per resource. Resource teardown
  is a distinct command, not ordinary purge, because it may reduce protected
  guardian counts to zero. No subject-purge command is part of v3.
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

## Task AZ3-0.5 — actor, guard, SystemMutator, and audit contracts

Depends on: AZ3-0.4.
Touch: public Config/service dependency types and pure construction tests.

Implement:

- Define `Actor` from the concrete platform principal pair. It represents an
  untrusted principal only; empty actors are invalid and there is no `system`
  enum value callers can construct.
- Define `MutationGuard.AuthorizeMutation` over actor, operation, target scope,
  proposed change, and a dependency-tracking decision/check view supplied by the
  repository atomic operation. It returns allow or a stable denial/error; the
  feature supplies no default allow policy. A guard that depends on
  authorization data must use this view, never call the outer Service and create
  a check-then-write race. The view records every scope revision it observes for
  canonical-order validation before commit.
- A nil `MutationGuard` is a read-only posture: decision/list APIs and the
  separately held `SystemMutator` remain available, while every actor-facing
  mutation returns a stable mutations-not-configured error. There is no default
  allow guard. Orphaned actor-mutation settings with no guard fail construction.
- Change construction, under the pre-tag breaking policy, to return a
  `Components{Service, SystemMutator}` bundle. `SystemMutator` is a distinct
  capability for bootstrap, invitation acceptance, controlled migrations, and
  explicit resource teardown. It exposes only trusted command methods, is not
  reachable from `Service`, and is passed deliberately by the composition root.
  HTTP handlers receive only `Components.Service`.
- Define optional best-effort `AuditSink` for accepted/denied/failed attempts.
  Its failures are warned with coarse fields and never change a committed
  mutation.
- Freeze no effect mode in v3. Preserve mutation IDs, receipts, and revision
  vocabulary so a separately ratified effects adapter can compose later. Do not
  add an authorization delivery-job repository or import events/jobs.

Verify:

```sh
cd features/authorization && go test ./... -run 'Actor|Guard|System|Audit|Construction'
make guard
```

Acceptance: untrusted mutation cannot execute without an actor and guard;
guard dependencies are revision-validated inside the atomic mutation boundary;
trusted mutation is a separately held capability.

## Phase acceptance

- Exact userset, immutable schema, bounded evaluation, atomic mutation, actor,
  dependency-validation, system capability, and audit contracts are executable
  in tests.
- No current store adapter has been claimed atomic before phase 2.
- `make check` and `make guard` pass.

## Stop conditions

- Userset relation behavior remains ambiguous.
- A mutation contract needs two unlocked repository calls.
- A limit can silently return a partial allow/list.
- A host guard requires network or unrelated-store I/O inside ApplyGuarded.

## Execution log

Append only during execution.
