# Phase 3 — guarded mutation service

Status: DRAFT; ready after phases 1–2.
Depends on: exact read semantics and atomic mutation repositories.

## Goal

Promote one actor-aware, policy-guarded mutation lifecycle while keeping trusted
bootstrap/invitation calls explicit and retry-safe.

## Task AZ3-3.1 — guarded relationship grant/revoke/replace lifecycle

Touch: public Service, authorizersvc mutation orchestration, tests.

Implement:

- Add actor-facing commands for GrantRelationship, RevokeRelationship,
  ReplaceRelationship, and PurgeResourceAuthorization.
- Validate command/schema, then pass MutationGuard into repository
  `ApplyGuarded`; the guard evaluates authorization data through the supplied
  dependency-tracking decision view. The repository validates all observed
  scope revisions before commit. Build no detached Check→Apply sequence.
- Require MutationID and actor. Denial never reaches Apply.
- Preserve one relation per subject/resource: exact grant replay is unchanged;
  different relation without Replace is conflict; Replace is atomic.
- Require a separate guard action for bulk purge and bound its affected rows.
- Return explicit command result plus a receipt for persisted outcomes; replay is
  a separate boolean on the receipt.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Grant|Revoke|Replace|Purge|Guard|Receipt'
make guard
```

Acceptance: no actor-facing relationship mutation delegates to raw create/delete
methods.

## Task AZ3-3.2 — atomic last-owner/guardian invariants

Depends on: AZ3-3.1.
Touch: schema/config invariant vocabulary, service, storetest.

Implement:

- Replace hard-coded service count/delete with schema/config-declared protected
  relations (default may include `owner`; empty means no invariant only when
  explicitly configured).
- Repository Apply enforces the post-state rule “at least N direct anchors
  remain” under its scope lock for every ordinary command on a configured
  protected resource type. Group-expanded/effective counts never mask loss of
  the final direct guardian. A direct guardian has an exact concrete
  `SubjectRef` with empty Relation; a `group#member` owner is not a direct
  anchor. The first successful command must establish the
  minimum (normally an owner grant); a member/role-first command and any mutation
  of a legacy orphan scope are blocked until a trusted repair establishes it.
- Apply the invariant to revoke, replace-away, ordinary resource purge/batch,
  and any role operation configured as guardian-bearing. There is no undefined
  subject-purge command.
- Add an explicit `TeardownAuthorizationScope` command on `SystemMutator` for
  resource deletion. Possession of the separately wired capability plus an
  explicit non-empty teardown reason is the trust boundary; authorization does
  not call a foreign resource repository from inside its transaction. Document
  host ordering and ID-reuse hazards honestly. This is the only operation
  allowed to reduce a protected scope to zero, and it remains atomic,
  idempotent, revisioned, receipted, and audited.
- Test self-removal, two concurrent removals, replacing owner→member, group owner,
  and absent target.

Verify:

```sh
cd features/authorization && go test -race -count=20 ./... -run 'LastOwner|Guardian|Concurrent|Replace'
make guard
```

Acceptance: every mutation family has one atomic invariant path and the old
exists/count/delete helper is removed.

## Task AZ3-3.3 — guarded role assign/unassign and effective-grant result

Depends on: AZ3-1.5, AZ3-2.5.
Touch: roles service/public wrappers/tests.

Implement:

- Require actor, guard, MutationID, structurally valid opaque role/scope, and
  expected revision where supplied. Any known-role/assignment catalog belongs
  to host policy or a future admin adapter, not the core role kind.
- Distinguish exact assignment state from effective state in receipts.
- On scoped unassign, report `same_role_grant_remains` when a global assignment
  still satisfies that exact role. Do not claim generic access remains.
- Preserve exact idempotency and reject userset subjects.
- Add separate guard action for global role mutation because its blast radius is
  larger than one resource.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Role|Guard|Global|Effective|Receipt'
make guard
```

Acceptance: callers cannot mistake removal of one row for removal of effective
access.

## Task AZ3-3.4 — SystemMutator capability and legacy API transition

Depends on: AZ3-3.1 through AZ3-3.3.
Touch: public API, invitation adapter/proof-host compile sites, deprecation docs.

Implement:

- Have construction return `Components{Service, SystemMutator}`. Intended
  `SystemMutator` holders: bootstrap, migration, invitation acceptance,
  resource teardown, and test fixtures. `Service` cannot recover the sibling
  capability, and `Actor` has no system kind.
- Trusted calls still validate schema, require MutationID, use atomic Apply,
  enforce invariants, increment revisions, persist receipts, and audit; they
  bypass only the host MutationGuard.
- The resource-teardown method is the one explicit exception to the ordinary
  post-state guardian minimum and requires the separately held capability plus a
  recorded teardown reason.
- Migrate auth-cms invitation Granter to stable MutationIDs derived from the
  invitation operation, so retry does not duplicate the stored mutation or
  revision bump. This task owns that API/compile-site transition; proving the
  composed host behavior belongs to the proof-phase composition task AZ3-4.1.
- Deprecate or remove raw Create/Delete/Assign/Unassign methods according to the
  pre-tag breaking policy. Do not leave an easy unguarded synonym on Service.
- Keep raw port methods available only to store conformance and migrations, not
  ordinary feature consumers.

Verify:

```sh
cd features/authorization && go test ./... -run 'SystemMutator|Trusted|Legacy|Invitation|Teardown'
cd examples/auth-cms && go test ./...
make guard
```

Acceptance: every repository write in ordinary host code is visibly guarded or
performed through a separately held `SystemMutator`; the ordinary Service has no
constructible system-actor bypass.

## Task AZ3-3.5 — mutation policy, retry, stale revision, and audit attempt suite

Depends on: AZ3-3.4.
Touch: service tests and adversarial integration tests.

Cover:

- deny, guard infrastructure error, invalid actor, and invalid proposed tuple;
- guard evaluated before write and no receipt on denial;
- concurrent revocation of the actor's manage grant versus guarded Apply, proving
  no stale detached allow commits after the revoke wins;
- stale revision with safe reload/retry rules; never auto-retry policy denial;
- mutation-ID exact replay and payload mismatch;
- self-grant/self-escalation denied by proof policy;
- concurrent grant/revoke/replace with deterministic final state;
- audit accepted/denied/failed statuses without raw error or unbounded payload;
- `SystemMutator` still honoring invariant and idempotency except for the
  explicit, preconditioned teardown rule.

Verify:

```sh
cd features/authorization && go test -race -count=10 ./... -run 'Mutation|Policy|Stale|Replay|Audit|Concurrent'
make check
make guard
```

Acceptance: policy denial, command failure, domain outcome, and replay metadata
cannot be conflated by callers.

## Phase acceptance

- Actor-facing writes are guarded; system writes use a separate capability.
- All writes use atomic Apply and mutation receipts.
- Last-owner and effective-role semantics survive concurrency.
- Legacy proof-host call sites are migrated.
- `make check` and `make guard` pass.

## Stop conditions

- Guard bypass is needed for an ordinary HTTP/user operation.
- Legacy convenience methods would remain a simpler unsafe path.
- A retry can apply a different payload under one MutationID.
- The service reintroduces read/count/write atomicity.

## Execution log

Append only during execution.

### 2026-07-14 — AZ3-3.1 — guarded relationship grant/revoke/replace lifecycle — PASS (live-proven both dialects)

Outcome: actor-facing typed commands GrantRelationship / RevokeRelationship /
ReplaceRelationship / PurgeResourceAuthorization over the guarded
ApplyMutation seam; no raw create/delete delegation anywhere in the new path.

- New relationship_mutations.go: typed command structs (MutationID,
  ResourceType/ID, Relation, Subject SubjectRef, optional ExpectedRevision;
  Purge omits relation/subject) building one mutation.Command each. Result =
  `*Receipt` (Outcome explicit; Replayed independent); command error =
  (nil, err).
- SemanticValidator WIRED (was nil since AZ3-0.5): ApplyMutation now supplies
  current-schema validation (ValidateRelation full pair) for receipt-absent
  commands INSIDE Apply. Validation order kept per frozen AZ3-0.4 contract:
  pre-Apply is structural only — an unconditional pre-Apply ValidateRelation
  would have broken ReplayAfterSchemaChange (recorded adaptation; guidance
  said "before Apply", contract wins). Proven by
  TestGrantSemanticValidatorRejectsUnknownRelation +
  TestGrantReplaySurvivesSchemaChange.
- Schema-digest RECONCILED (the phase-2 flag): new `Command.SchemaDigest`
  (metadata; excluded from payload digest like ExpectedRevision) stamped by
  ApplyMutation from the compiled schema; all three stores copy it into the
  receipt ("unset" remains only as pgx/turso empty-fallback for the
  NOT-empty column). Receipt digest == svc.SchemaDigest() proven; replay
  returns the ORIGINAL digest under a newer schema.
- Purge design: separate guard action = MutationAttempt.Operation (OpPurge
  distinguishable; TestPurgeGuardSeparateAction proves deny-purge/allow-
  grant). Row bound = new `Command.MaxAffectedRows` (also digest-excluded)
  sourced from resolved EvaluationLimits.MaxBatchSize, enforced ATOMICALLY
  in all three repository purge evaluators under the scope lock (service-
  side count would trip the read/count/write stop condition); over-bound
  ordinary purge → OutcomeInvariantBlocked; 0 (teardown) unbounded.
- Denial-never-reaches-Apply: guard runs first inside ApplyGuarded;
  TestGuardDenialCommitsNothing proves denied grant → sdk.ErrForbidden, no
  row, MutationID NOT consumed (later allowed grant applies fresh).
- New tests (11): TestGrantRelationshipGuardedApplies, …ExactReplay,
  …ConflictThenReplace, TestRevokeRelationshipAppliedAndNotFound,
  TestPurgeResourceAuthorizationBound, TestPurgeGuardSeparateAction,
  TestGuardDenialCommitsNothing, TestGrantReadOnlyPosture,
  TestGrantUnwiredRelationshipKind,
  TestGrantSemanticValidatorRejectsUnknownRelation,
  TestGrantReplaySurvivesSchemaChange.
- Verify — agent ran all; orchestrator re-ran: module `-race -count=1` green
  (7 pkgs); 5 keystone tests fresh PASS; auth-cms green; LIVE pgx full suite
  (fresh C-collation DB, `-race -count=1`) PASS 18.5s; LIVE turso
  (`-tags=integration -race -count=1`) PASS 9.7s; `make check` "all checks
  passed"; guard exit 0.
- Premise adaptations: (1) validation-order (above). (2) storetest commands
  leave both new Command fields zero so live conformance exercises prior
  paths unchanged; new behaviors proven service-level + by identical
  evaluator guards across dialects. (3) Purge bound tracks host MaxBatchSize
  automatically (single source of truth). Legacy raw methods untouched
  (AZ3-3.4 owns their fate); new commands never call them.

### 2026-07-14 — AZ3-3.2 — atomic last-owner/guardian invariants — PASS (live-proven both dialects)

Outcome: the critical non-atomic last-owner path is GONE —
authorizersvc/membership.go (RemoveMember: exists→count→delete) and its test
file deleted; root wrapper + orphaned ErrCannotRemoveLastOwner sentinel +
dead codes.go branches removed; grep confirms zero references; NO call site
needed migration (nothing outside tests consumed it). The atomic replacement
is the AZ3-3.1 guarded path with repository-level OutcomeInvariantBlocked.

- Guardian seam DECISION: store construction is the sanctioned seam (the
  invariant must live where the atomic lock lives); Config does NOT carry
  the policy; the vocabulary (GuardianPolicy/GuardianRule/
  DefaultGuardianPolicy) is root-re-exported so hosts avoid importing
  domain/mutation. Explicitly-empty ≠ default honored in all three
  constructors.
- Role-guardian DECISION: roles stay OUT of guardian vocabulary in v3
  (opaque roles have no direct-anchor notion, default #5; ratified set is
  relationship owner; role families pass vacuously; a future role-minimum is
  a new packet). Recorded in guardian.go.
- TeardownAuthorizationScope: SystemMutator.TeardownAuthorizationScope(ctx,
  {MutationID, ResourceType, ResourceID, Reason, ExpectedRevision}); reason
  required non-empty trimmed ≤1024 (ErrTeardownReasonRequired before any
  write); builds OpTeardown via trusted Apply — the one guardian exception;
  atomic/idempotent/revisioned/receipted. Reason recorded via two seams:
  ALWAYS the SystemMutator structured log line (mutation_id, scope, outcome,
  reason) + AuditEvent with new bounded `Detail` field when a sink is wired
  (SystemMutator now carries audit/log; cfg.Audit wired in). Deliberate
  rejection of a receipt `reason` column (would churn frozen phase-2
  migration parity for an operational annotation) — FLAGGED as a possible
  reviewer follow-up with its full cost stated. Host ordering + ID-reuse
  hazards documented on the method.
- Invariant family coverage verified across all three stores (grant/revoke/
  replace/purge consult relationshipInvariantOK); new shared conformance
  case Mutations/LastOwnerGuardianScenarios {self-removal,
  replace-owner-to-member, group-owner-not-direct-anchor, absent-target};
  two-concurrent-removals = existing ConcurrentTwoOwnerRevokeRounds.
  Service tests: TestTeardownReasonRequired, TestTeardownReasonTooLong,
  TestTeardownAppliesTrustedAndAudits, TestTeardownNotConfigured.
- Verify — agent ran all; orchestrator re-ran: membership.go confirmed
  deleted, zero grep references; `-race -count=20` focused suite green; full
  module `-race -count=1` green (7 pkgs); teardown + guardian-scenario tests
  fresh PASS (4 subtests visible); auth-cms green; LIVE pgx Mutations suite
  (fresh C-collation DB) PASS; LIVE turso PASS; `make check` "all checks
  passed"; guard exit 0.
- Premise adaptations: (1) store .go files unchanged — repo invariant paths
  already complete from phase 2; live legs still run because the shared
  suite gained a case. (2) Teardown reason durability choice as above.

### 2026-07-14 — AZ3-3.3 — guarded role assign/unassign and effective-grant result — PASS (live-proven both dialects)

Outcome: actor-aware guarded role lifecycle landed; exact vs effective state
distinguished via an honest IN-LOCK same_role_grant_remains computation.

- API (role_mutations.go): `AssignRoleGuarded(ctx, Actor,
  AssignRoleCommand) (*Receipt, error)`; `UnassignRoleGuarded(ctx, Actor,
  UnassignRoleCommand) (UnassignRoleResult, error)` with
  `UnassignRoleResult{Receipt, SameRoleGrantRemains bool}`; commands carry
  MutationID, concrete `Subject PrincipalRef`, opaque Role, scope pair,
  optional ExpectedRevision; `ErrHalfScopedRoleScope`; scope anchor per
  default #3 (ScopeSubject global / ScopeResource scoped). NAMING
  adaptation: `*Guarded` suffixes because the plain verbs are still the
  legacy unguarded wrappers AZ3-3.4 removes (names collapse then).
- same_role_grant_remains: computed INSIDE Apply's critical section in all
  three stores (after scoped-row removal, before receipt persistence;
  memstore mutex / pgx FOR UPDATE tx / turso BEGIN IMMEDIATE tx) — a state
  consistent with the removal, which a detached post-commit read could not
  promise. Surfaced as NON-PERSISTED computed `Receipt.SameRoleGrantRemains`
  (the Replayed precedent — no column, frozen receipt schema unchanged),
  repackaged by the service into UnassignRoleResult. False for global
  unassign/non-role ops/replay (first-application annotation; replay returns
  the original receipt verbatim — documented). Claims only this exact role
  via global fallback, never generic access.
- Guard distinguishability: no vocabulary change needed —
  MutationAttempt.Scope.Kind==ScopeSubject identifies global role mutation;
  TestGlobalRoleGuardSeparateAction proves deny-global/allow-scoped.
- Userset rejection: structural — commands/RoleRow/PrincipalRef carry no
  Relation field; TestRoleCommandRejectsUsersetSubjectsStructurally
  reflection-pins it.
- Tests (11 new + extended storetest RoleAssignUnassignScopes with
  cross-dialect SameRoleGrantRemains true/false cases):
  TestAssignRoleGuardedApplies, TestGlobalRoleGuardSeparateAction,
  TestUnassignRoleSameRoleGrantRemains, TestUnassignRoleGuardedExactReplay,
  TestAssignRoleGuardedIdempotentDuplicate,
  TestRoleGuardedDenialCommitsNothing, TestRoleGuardedStaleRevision,
  TestRoleGuardedHalfScopedRejected, TestRoleGuardedReadOnlyPosture,
  TestRoleGuardedUnwiredKind, TestRoleCommandRejectsUsersetSubjectsStructurally.
- Verify — agent ran all; orchestrator re-ran: module `-race -count=1` green
  (7 pkgs); 5 keystone tests fresh PASS; auth-cms green; LIVE pgx
  RoleAssignUnassignScopes PASS (fresh C-collation DB, dropped); LIVE turso
  PASS; `make check` "all checks passed"; guard exit 0.
- Notes: legacy raw AssignRole/UnassignRole + call sites untouched
  (AZ3-3.4); guarded methods never call them.

### 2026-07-14 — AZ3-3.4 — SystemMutator capability and legacy API transition — PASS

Outcome: raw unguarded mutation surface REMOVED from public Service (pre-tag
policy: removed, not deprecated); guarded role verbs collapsed to plain
names; SystemMutator gained a typed trusted surface with full digest/
validator parity; auth-cms invitation Granter migrated to stable derived
MutationIDs. Ordinary host code now has no unguarded write and no
constructible system-actor bypass (TestLegacyRawMutationMethodsRemovedFromService
reflection-pins the removal).

- Removed from Service: CreateRelationships, DeleteRelationship,
  DeleteResourceRelationships, DeleteByResourceAndSubject, raw AssignRole/
  UnassignRole. Ports keep raw methods (stores/storetest/migrations
  sanctioned). Migrated call sites: auth-cms membership.go (invitation
  Granter → SystemMutator.GrantRelationship), demo.go (bootstrap + admin
  role routes → SystemMutator), main.go (single authzmem.New bundle wiring
  all three ports incl. Mutations; systemMutator captured);
  storetest budget/roles/adversarial seed via store ports; module tests
  reseeded via memstore port.
- Naming: AssignRoleGuarded→AssignRole, UnassignRoleGuarded→UnassignRole
  (the AZ3-3.3 anticipated collapse; no unguarded synonym remains).
- SystemMutator surface (minimal honest line): GrantRelationship,
  AssignRole, UnassignRole — each with a real auth-cms holder;
  Revoke/Replace/Purge deliberately NOT added (no trusted holder; teardown
  covers destruction). Shared command builders between guarded and trusted
  paths. PARITY FIX: SystemMutator.Apply predated AZ3-3.1 wiring (nil
  validator) — now stamps the governing schema digest and runs the
  SemanticValidator via shared helpers
  (TestSystemMutatorTrustedStampsSchemaDigest,
  …TrustedRunsSemanticValidator).
- DeriveMutationID(parts...): SHA-256 over length-prefixed parts, lowercase
  unpadded base32, 52 chars (clears MinMutationIDLen=26, passes Validate);
  root re-exported. Invitation Granter derives from the tuple identity
  ("auth-cms/invitation-grant" + resource + relation + subject) — the
  Granter seam carries no invitation ID, so the tuple IS the honest
  operation identity; retried accept replays without duplicate bump
  (TestInvitationStableMutationIDReplaysWithoutDuplicateBump).
- New tests: system_mutator_trusted_test.go (7) + renames/reseeding across
  module tests.
- Verify — agent ran hermetic all-green (live skipped: store .go files
  byte-identical); ORCHESTRATOR ADDITIONALLY RAN both live legs anyway
  (storetest fixtures changed): pgx full suite PASS 19.1s (fresh
  C-collation DB, dropped), turso full suite PASS 15.0s
  (-tags=integration); module `-race -count=1` green (7 pkgs); keystone
  tests fresh PASS; auth-cms full green (7 pkgs incl. invitation flow);
  `make check` "all checks passed"; guard exit 0.
- Premise adaptations / flags: (1) auth-cms wires an EXPLICITLY EMPTY
  GuardianPolicy to preserve the demo's member-before-owner flow — the
  guardian composition proof belongs to AZ3-4.1 (flagged for that task).
  (2) Internal engine raw methods (authorizersvc Create/Delete*, rolesvc
  Assign/Unassign) are now production-orphaned, exercised only by their own
  package tests — flagged as AZ3-5.4-audit/cleanup candidate, deliberately
  not removed here. (3) Pre-existing auth-cms gofmt drift untouched.

### 2026-07-14 — AZ3-3.5 — mutation policy, retry, stale revision, and audit attempt suite — PASS (phase 3 gate green)

Outcome: service-level adversarial suite over the real memstore bundle with a
real DecisionView-reading "proof policy" MutationGuard. 12 new tests in
mutation_policy_test.go; every task bullet mapped (new + existing); NO
production code changed; NO found-defects; the four-way matrix (policy denial
/ command failure / domain outcome / replay metadata) distinguishes cleanly
via a caller-side classifier test.

- New: TestProofPolicyManageGuardAllowsAndDenies,
  TestProofPolicySelfEscalationDenied,
  TestConcurrentGuardManageRevokeRaceServicePath (service path, -race ×10),
  TestGuardInfrastructureErrorAuditedFailed (failed ≠ denied),
  TestMutationInvalidActorRejectedBeforeApply,
  TestMutationInvalidProposedTupleRejectedBeforeApply,
  TestStaleRevisionReloadRetryProtocol,
  TestStaleRevisionRetryLoopConvergesButDenialTerminal (denial terminal,
  never auto-retried), TestAuditFieldHygieneAcceptedDeniedFailed (coarse
  bounded fields only), TestMutationDenialFailureOutcomeReplayDistinguishable
  (the acceptance matrix), TestConcurrentGuardedGrantsDeterministicFinalState,
  TestSystemMutatorHonorsMutationInvariantExceptTeardown. Full bullet→test
  mapping in the agent report; existing repo-level cases referenced.
- Premise adaptations: (1) proof-policy bootstrap chicken/egg solved with
  SystemMutator seed (the intended trusted-holder role) + empty guardian so
  the guard, not last-owner, is exercised. (2) The manage-revoke race uses a
  TRUSTED revoke racing the guarded grant (faithful admin-yanks-access
  shape; mirrors the storetest case). (3) Retry loop mints a fresh
  DeriveMutationID per attempt — never re-issues a payload under one key.
- Verify — agent ran all; orchestrator re-ran: focused suite `-race
  -count=10` green (7 pkgs); full module `-race -count=1` green; 4 keystone
  tests fresh PASS; stores hermetic green; auth-cms green; `make check`
  "all checks passed"; guard exit 0. Live legs not run — test-only change,
  no store code touched (justified).

### Phase 3 acceptance — 2026-07-14 — GREEN

All five tasks logged PASS. Actor-facing writes guarded (typed commands →
ApplyMutation → ApplyGuarded; nil guard fails closed). System writes via the
separately held SystemMutator (unreachable from Service, reflection-pinned).
All writes atomic Apply + receipts; no service-level read/count/write
remains (membership.go deleted). Last-owner and effective-role semantics
survive concurrency (service-path race ×10 + storetest ×10 live from phase
2). Legacy proof-host call sites migrated (AZ3-3.4). Orchestrator re-ran
`make check` ("all checks passed") + `make guard` (exit 0). No stop
condition encountered. Phase 4 (proof host) may begin.
