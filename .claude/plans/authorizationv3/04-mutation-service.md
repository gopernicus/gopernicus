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
