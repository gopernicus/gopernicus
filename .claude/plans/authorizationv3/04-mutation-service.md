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
  transaction-bound decision view. Build no detached Check→Apply sequence.
- Require MutationID and actor. Denial never reaches Apply.
- Preserve one relation per subject/resource: exact grant replay is unchanged;
  different relation without Replace is conflict; Replace is atomic.
- Require a separate guard action for bulk purge and bound its affected rows.
- Return committed receipt alongside post-effect failure per phase-0 contract.

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
- Repository Apply enforces “at least N direct anchors remain” under its scope
  lock. Group-expanded/effective counts never mask loss of the final direct
  guardian.
- Apply invariant to revoke, replace-away, subject purge, resource batch, and any
  role operation configured as guardian-bearing.
- Test self-removal, two concurrent removals, replacing owner→member, group owner,
  and absent target.

Verify:

```sh
cd features/authorization && go test -race -count=20 ./... -run 'LastOwner|Guardian|Concurrent|Replace'
make guard
```

Acceptance: every mutation family has one atomic invariant path and the old
exists/count/delete helper is removed.

## Task AZ3-3.3 — guarded role assign/unassign and effective disposition

Depends on: AZ3-1.5, AZ3-2.5.
Touch: roles service/public wrappers/tests.

Implement:

- Require actor, guard, MutationID, role validator/catalog, and expected revision
  where supplied.
- Distinguish exact assignment state from effective state in receipts.
- On scoped unassign, report `effective_access_remains` when a global assignment
  still satisfies the role.
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

## Task AZ3-3.4 — trusted-system path and legacy API transition

Depends on: AZ3-3.1 through AZ3-3.3.
Touch: public API, invitation adapter/proof-host compile sites, deprecation docs.

Implement:

- Add explicitly named trusted mutation entry points requiring a constructed
  `SystemActor`/capability. Intended callers: bootstrap, migration, invitation
  acceptance, and test fixtures.
- Trusted calls still validate schema, require MutationID, use atomic Apply,
  enforce invariants, increment revisions, audit, and emit configured effects;
  they bypass only the host MutationGuard.
- Migrate auth-cms invitation Granter to stable MutationIDs derived from the
  invitation operation, so retry does not duplicate events.
- Deprecate or remove raw Create/Delete/Assign/Unassign methods according to the
  pre-tag breaking policy. Do not leave an easy unguarded synonym on Service.
- Keep raw port methods available only to store conformance and migrations, not
  ordinary feature consumers.

Verify:

```sh
cd features/authorization && go test ./... -run 'System|Trusted|Legacy|Invitation'
cd examples/auth-cms && go test ./...
make guard
```

Acceptance: every repository write in ordinary host code is visibly guarded or
visibly trusted-system.

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
- committed mutation plus procedural effect failure result; and
- trusted system still honoring invariant and idempotency.

Verify:

```sh
cd features/authorization && go test -race -count=10 ./... -run 'Mutation|Policy|Stale|Replay|Audit|Concurrent'
make check
make guard
```

Acceptance: policy, mutation, and effect outcomes cannot be conflated by callers.

## Phase acceptance

- Actor-facing writes are guarded; system writes are explicit.
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
