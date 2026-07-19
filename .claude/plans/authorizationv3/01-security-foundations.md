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

### 2026-07-14 — AZ3-0.1 — exact principal, subject, userset, decision, and error vocabulary — PASS

Outcome: vocabulary frozen. Decision requests structurally cannot carry a
userset (no field exists); stored subject relation is a canonical non-pointer
`string`, removing the nullable-erase hazard.

- New public vocabulary: `relationship.SubjectRef{Type, ID, Relation}`
  (comparable; `IsUserset()`, `String()`, `Validate()`; `RelationTarget` is now
  an alias of it), `relationship.ValidateRefField`/`MaxRefFieldLen`/
  `ErrInvalidRef` (non-empty, ≤256 bytes, UTF-8, no control chars, no
  lowercasing), `authorizersvc.PrincipalRef{Type, ID}` replacing `Subject`
  (`CheckRequest.Subject` → `.Principal`), root re-exports
  `authorization.PrincipalRef`/`SubjectRef` + `PrincipalFrom(identity.Principal)`.
  Removed `authorization.Subject` and `ErrUsersetSubjectOnRole` (role methods
  take concrete `PrincipalRef`; userset rejection is structural).
- New `codes.go`: reasons `invalid_request`, `unknown_model_symbol`, `denied`,
  `evaluation_limit`, `stale_revision`, `invariant_conflict`,
  `mutation_payload_mismatch`, `infrastructure_failure`; sentinels per default
  #9 (`ErrEvaluationLimit` wraps `sdk.ErrUnavailable`; stale/invariant/mismatch
  wrap `sdk.ErrConflict`; invalid/unknown wrap `sdk.ErrInvalidInput`). Feature-
  local mapper seam `RespondError`/`ReasonFor` over
  `web.RespondJSONDomainError`; sdk mapper untouched.
- Files: domain/relationship/relationship.go; internal/logic/authorizersvc/
  {model,service,lookup,middleware}.go; authorization.go; codes.go (new);
  stores/{pgx,turso}/relationships.go, memstore, storetest, examples/auth-cms
  (mechanical adaptation — pointer helpers deleted). New tests:
  domain/relationship/subjectref_test.go, authorizersvc/principalref_test.go,
  codes_test.go.
- Tests (re-run independently by orchestrator, `-count=1`):
  TestSubjectRefUsersetDistinct (group / group#member / group#admin never
  equal), TestSubjectRefValidateInvalid, TestCreateRelationshipInvalid,
  TestPrincipalRefInvalid, TestCheckRequestInvalidRejectedAtBoundary,
  TestVocabularyErrorKinds, TestVocabularyReasonFor — all PASS.
- Verify (agent ran; orchestrator re-ran all): core
  `go build/vet/test ./...` green; stores/pgx and stores/turso
  `go build/vet/test ./...` green (hermetic); examples/auth-cms
  `go build ./... && go test ./...` green; `make guard` green.
- Premise adaptations: (1) `CreateRelationship` keeps flat subject fields with
  `SubjectRelation *string`→`string` plus a `Subject() SubjectRef` accessor,
  rather than embedding `SubjectRef` — kept ~31 tuple literals compiling;
  folding to an embedded field is a mechanical follow-up if wanted. (2)
  Decision-boundary validation wired now (Check/CheckBatch/LookupResources
  fail closed on malformed refs) since acceptance requires rejection today.
  (3) Pre-existing gofmt import-order flag in auth-cms main.go left untouched
  (unrelated).

### 2026-07-14 — AZ3-0.2 — strict schema compiler and immutable snapshot contract — PASS

Outcome: strict compiler + immutable snapshot + deterministic digest landed in
`authorizersvc`; runtime wiring deliberately deferred to AZ3-1.2 (`NewService`
still calls `ValidateSchema`; the compiler is exercised by its own tests).

- New: internal/logic/authorizersvc/compiler.go — `Compile(Schema)
  (*CompiledSchema, error)`, `CompiledSchema{Digest(), EncodingVersion(),
  Snapshot()}`, `SchemaCompileError` (sorted errors, unwraps
  `sdk.ErrInvalidInput`), `SchemaEncodingVersion =
  "gopernicus.authorization.schema/1"`; digest = SHA-256(version prefix ||
  0x00 || canonical length-prefixed bytes), lowercase hex. Rejects empty
  names/rules, duplicate declarations after composition, ambiguous checks
  (both/neither Direct/Through), unknown direct/userset relations, userset
  targets on navigational relations, Through permission absent from any
  target, relations with no subjects, genuine cycles, unsatisfiable graphs;
  accepts the sanctioned self-hierarchy. Direct-vs-navigational
  classification is derived from compiled use and folded into the digest.
- New: snapshot.go — `SchemaSnapshot` deep-copy read-only projection; internal
  compiled maps never returned. New tests: compiler_test.go,
  immutable_test.go. Reused existing schema_validator.go cycle/unsatisfiable/
  through-target helpers (no duplication).
- Tests (orchestrator re-ran `-race -count=1`): TestCompileAcceptsValidSchemas,
  TestCompileClassifiesNavigationalRelation, TestCompileRejects (13 cases),
  TestCompileErrorsSortedDeterministic,
  TestSchemaDigestDeterministicAcrossIterationOrder,
  TestSchemaDigestNormalizesDeclarationOrder, TestSchemaDigestChangesWithPolicy,
  TestCompileImmutableAfterSourceMutation,
  TestSchemaSnapshotAccessorsReturnCopies,
  TestCompileConcurrentReadsDoNotRaceEngine — all PASS.
- Verify (agent + orchestrator re-run): module `go build/vet/test -race ./...`
  green; `make guard` exit 0; stores/pgx, stores/turso, auth-cms hermetic
  builds unaffected (no consumed public type changed).
- Premise adaptations: (1) "duplicate declarations after composition" =
  exact-duplicate AllowedSubjects/AnyOf entries post-`NewSchema` merge;
  encoder still sorts+dedupes defensively. (2) "resolved defaults included" =
  the derived relation classification folded into canonical bytes (the DSL has
  no other defaulted fields today). (3) "meaningful as a userset" = Type is a
  declared resource type and Relation exists on it (R1: nested membership in,
  no operators). (4) Added rejection of relations with zero allowed subjects
  (unsatisfiable-declaration case). (5) `Compile`/`SchemaSnapshot` not
  root-re-exported yet — AZ3-1.2 owns the swap into `NewService`/`GetSchema`.

### 2026-07-14 — AZ3-0.3 — evaluation limits and construction matrix — PASS

Outcome: single depth knob replaced by resolved semantic `EvaluationLimits`
with construction-time validation, exhaustion error semantics, and a full
construction matrix. Contract-scoped: engine-wide budget charging is handed to
AZ3-1.3 (obligations pinned in doc comments and tests).

- New: internal/logic/authorizersvc/limits.go —
  `EvaluationLimits{MaxThroughDepth, MaxGraphStates, MaxRelationTargets,
  MaxBatchSize, MaxLookupResults int}`, pure `Resolve()`, defaults
  10/10000/1000/1000/1000; zero→default, negative→`ErrInvalidLimits`
  (wraps `sdk.ErrInvalidInput`, all offending fields named via `errors.Join`);
  zero never means unlimited; no unlimited mode. Query count deliberately
  absent (adapter-local telemetry, not a semantic field).
- Changed: engine `Config.MaxTraversalDepth` → `Config.Limits`; public
  `authorization.Config.Limits` + root aliases for `EvaluationLimits` and the
  five `Default*` consts; depth exhaustion flipped from silent
  deny-with-reason to `ErrEvaluationLimit` (indeterminate); sentinel moved to
  authorizersvc/limits.go with root re-export (import-cycle constraint), same
  `sdk.ErrUnavailable` identity end-to-end. Doc-comment rename
  MaxTraversalDepth→MaxThroughDepth in stores/domain comments (no logic).
- Tests (orchestrator re-ran fresh + `-race -count=1` full module):
  TestConstructionDefaultLimits, TestConstructionExplicitLimits,
  TestConstructionNegativeLimitRejected,
  TestConstructionOrphanedLimitsUnderRolesOnly,
  TestEvaluationLimitsResolve{Defaults,ExplicitPreserved,NegativeRejected},
  TestNewServiceRejectsNegativeLimit, TestBudgetThroughDepthExhaustionIsError,
  TestBudgetDefaultDepthReachesGrant — all PASS. stores/pgx, stores/turso,
  auth-cms hermetic build+test green; `make guard` exit 0.
- Premise adaptations: (1) depth exhaustion silent-deny → error is real
  enforcement of the already-enforced dimension, not fake enforcement. (2)
  Orphaned limits under roles-only wiring are ignored, not an error (the
  documented MailFrom precedent) — pinned by test. (3) Construction
  (`ErrInvalidLimits`/invalid-input) vs runtime
  (`ErrEvaluationLimit`/unavailable) kept deliberately distinct.
- Handed to AZ3-1.3: MaxGraphStates state accounting across nested checks;
  MaxRelationTargets per-hop fan-out; MaxBatchSize pre-store rejection;
  MaxLookupResults max+1 fetch/overflow (incl. SQL bounding); no store call
  after observed cancellation/exhaustion. FLAGGED for phase 1: middleware
  currently maps limit exhaustion to 500 via fail-closed `web.ErrInternal`;
  surfacing 503 through the codes.go mapper is a small AZ3-1.3/1.6 refinement.

### 2026-07-14 — AZ3-0.4 — mutation, scope revision, idempotency, outcome, and replay contract — PASS

Outcome: atomic write contract frozen as new public `domain/mutation` package
with normative port doc comments + executable reference specification. No
store implements Apply yet (phase 2 owns that); storetest `Mutations` runner
nil-skips until then (verified skipping in memstore conformance).

- New domain/mutation package: mutation.go (`MutationID` + `NewMutationID`,
  crypto-strong, `MutationIDBytes=32`/`MinMutationIDLen=26`; `Revision`
  uint64; `ScopeKind`/`ScopeKey{Kind,Type,ID}` with length-prefixed
  `Canonical()` lock-order key; `Operation`; `RelationshipRow`/`RoleRow`;
  `Command` — single scope by construction, `Validate()` enforces the
  operation/scope/rows matrix); digest.go (`MutationEncodingVersion =
  "gopernicus.authorization.mutation/1"`, SHA-256 over version prefix +
  length-prefixed canonical bytes of operation+scope+sorted rows; excludes
  MutationID/ExpectedRevision/actor); receipt.go (outcomes applied/no_change/
  semantic_conflict/invariant_blocked/not_found; `Receipt` with payload
  digest+encoding, resulting revision, governing schema digest, independent
  `Replayed`); repository.go (normative `MutationRepository.Apply/
  ApplyGuarded`, `DecisionView`, `Dependency`, `Guard`, `SemanticValidator`).
- Scope kinds per default #3: `ScopeResource` (relationships + scoped roles),
  `ScopeSubject` (global roles). Operations: grant/revoke/replace/
  resource_purge/resource_teardown (trusted-only)/role_assign/role_unassign.
  No subject-purge.
- storetest/mutations.go: six named spec cases wired into `Run` (nil-skip):
  ExactReplayReturnsOriginalReceipt, MutationIDPayloadMismatchChangesNothing,
  StaleRevisionRejected, RollbackLeavesNoTrace, NoPartialBatch,
  ConcurrentSingleWinner. Pure tests (run now, orchestrator re-ran `-race
  -count=1`): TestMutationIDNewIsStrongAndUnique, …ValidateRejectsWeak,
  TestMutationCommandValidate{Accepts,Rejects},
  TestMutationScopeCanonicalOrdering, TestMutationExpectedRevisionOptional,
  TestMutationPayloadDigest{Deterministic,ActorIndependentButStateSensitive,
  RoleRows}, TestIdempotencyOutcomePersistenceContract,
  TestReplayIsIndependentOfOutcome — all PASS.
- Root: `Repositories.Mutations` field + root aliases for the vocabulary.
  Verify (orchestrator re-ran): module `-race -count=1` green; stores/pgx,
  stores/turso, auth-cms hermetic green; `make guard` exit 0.
- Premise adaptations: (1) AZ3-0.5 SEAM: `ApplyGuarded` takes `Guard =
  func(ctx, DecisionView) error`; AZ3-0.5 composes Actor+MutationGuard into
  that closure — no breaking change needed. `DecisionView` uses primitive
  subjectType/subjectID (public rim never imports internal/logic). (2) Domain
  outcomes ride `Receipt.Outcome` with nil error (never `(nil,nil)`);
  command errors return `(nil, err)`; only applied/no_change/not_found persist
  replayable receipts — semantic_conflict/invariant_blocked persist nothing so
  retries re-evaluate. (3) Cross-scope batch structurally impossible (one
  `ScopeKey` per Command, rows carry no scope). (4) No new batch-size
  constant — `EvaluationLimits.MaxBatchSize` remains the ceiling, applied by
  the service. (5) Benign lint notes in tests (deliberate determinism
  self-compare; conceptual Replayed write) reviewed by orchestrator — not
  defects.

### 2026-07-14 — AZ3-0.5 — actor, guard, SystemMutator, and audit contracts — PASS (phase 0 gate green)

Outcome: actor/guard/SystemMutator/audit contracts and the
`Components{Service, SystemMutator}` construction shape frozen with a full
construction matrix. Guarded path exercised via test stubs only — no store
claimed atomic.

- New mutation_service.go: `Actor{PrincipalRef}` (untrusted only; empty
  invalid; NO system/kind/trust field — pinned by reflection test);
  `MutationGuard.AuthorizeMutation(ctx, MutationAttempt, DecisionView) error`
  (`MutationAttempt{Actor, Operation, Scope, Change ProposedChange}`; nil =
  allow; no default-allow policy); `composeGuard` completes the AZ3-0.4 seam
  (closure captures Actor + command state, passes the repository's
  DecisionView straight through — repository port unchanged);
  `AuditSink.RecordMutation` + `AuditEvent`/`AuditDecision`
  (accepted/denied/failed; best-effort; failure warned via slog with coarse
  bounded fields, never raw IDs, never changes committed result);
  `SystemMutator` (trusted Apply, structurally unreachable from Service);
  generic guarded seam `Service.ApplyMutation` (phase 3 layers typed methods
  over it; passes nil SemanticValidator until AZ3-3.x wires the compiled-
  schema validator).
- Construction: `NewService` now returns `(Components, error)`; `Config` gains
  `Guard`/`Audit`. Matrix: nil guard = read-only posture
  (`ErrMutationsNotConfigured` wraps `sdk.ErrInvalidInput` — deterministic
  precondition refusal, deliberately not ErrUnavailable (saturation, default
  #9) nor ErrForbidden (falsely implies another principal could succeed));
  `Audit!=nil && Guard==nil` fails boot (`ErrAuditWithoutGuard`);
  `Guard!=nil && Mutations==nil` fails boot (`ErrGuardWithoutMutations`).
  Call sites updated: module tests, storetest adversarial helper, auth-cms
  main.go (documents its read-only actor-mutation posture).
- Tests (orchestrator re-ran fresh `-count=1` + full `-race`):
  TestConstructionReturnsComponents, …NilGuardIsReadOnlyPosture,
  …ReadOnlyWithoutMutations, …GuardWithoutMutationsFails,
  …AuditWithoutGuardFails, …MutationsNotConfiguredKind,
  …FullActorMutationPostureApplies, TestActorValidateRejectsEmpty,
  TestActorHasNoConstructibleSystemKind, TestGuardComposesIntoMutationGuard,
  TestGuardDependenciesRecordedThroughView, TestAuditRecordsDeniedAttempt,
  TestAuditSinkFailureDoesNotChangeMutation,
  TestSystemMutatorUnreachableFromService — all PASS.
- Premise adaptations: (1) the "orphaned actor-mutation setting" is
  AuditSink-without-Guard (Mutations-without-guard is the valid read-only +
  SystemMutator posture, so it cannot be the orphan). (2)
  ErrGuardWithoutMutations added (guard with no atomic boundary = half-enabled
  system, lesson #4). (3) Generic ApplyMutation seam landed now so the
  not-configured and audit contracts are executable; legacy raw methods
  untouched until AZ3-3.4; no new unguarded path added. (4) Pre-existing
  auth-cms main.go gofmt drift left untouched.

### Phase 0 acceptance — 2026-07-14 — GREEN

All five tasks logged PASS. Contracts (exact userset, immutable schema,
bounded evaluation, atomic mutation, actor, dependency-validation, system
capability, audit) are executable in tests. No store adapter claimed atomic
(storetest Mutations runner verified nil-skipping). Orchestrator re-ran:
`make check` → "all checks passed"; `make guard` → exit 0; authorization
module `go build/vet/test -race -count=1 ./...` green; stores/pgx,
stores/turso, examples/auth-cms hermetic green. No stop condition
encountered. Phase 1 may begin.
