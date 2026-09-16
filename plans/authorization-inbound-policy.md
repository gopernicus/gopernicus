# Inbound authorization ownership

Status: COMPLETED — implemented and verified; uncommitted and unreleased. Starting main 22c712e2100ff58e0b8a3a53528a9b43ae69c942.

## Goal and scope

Inbound adapters choose and invoke application authorization policies. Application
services accept already-authorized commands and preserve validation, tenant/data
constraints and business invariants. Fix policy callbacks in authentication and
permission orchestration in the auth-cms document example. Keep the reusable
transport-independent authorization engine. No release, dependency/schema changes,
host adoption or edits to owner planning files in this task.

## Design

### I1 — Authentication policy ownership

Move InviteCheck and UserAdminCheck contracts, storage and invocation to inbound/http.
Root configuration wires callbacks only into HTTP. Remove service policy options,
fields and forwarding/Authorized methods; migrate all in-repo callers/tests and
current documentation without compatibility shims. Preserve disabled-route and
fail-closed constructor behavior.

Invitation preparation remains domain behavior: normalize identifier and kind,
validate/copy metadata, resolve the invitee once. Expose an opaque prepared-create
value, with defensive-copy inspection, and execute that exact prepared value after
the inbound callback succeeds. Reject zero/foreign-service prepared values. Keep
Create as a convenient policy-free service operation. Authorization callbacks may
not change the command or cause a second lookup. Preserve live authentication,
CSRF, identity proof, acceptance/issuance semantics and lifecycle invariants.
User administration invokes its existing policy before target lookup or mutation;
actor remains attribution, never a service-owned host-policy dependency.

### I2 — Document listings

Move permission selection/evaluation and bypass callbacks into inbound. Domain
and outbound business reads accept explicit query restrictions, apply tenant/search
and ordering before pagination, and never invoke the decision engine. Preserve
candidate and complete-set strategies, encrypted principal/query-bound cursors,
scan budgets, and fresh evaluation on continuation. Assess the same-statement SQL
optimization explicitly: database enforcement of an inbound-selected restriction
must remain separate from deciding host policy; do not introduce provider SQL in
inbound or silently remove supported semantics. The named architecture reviewer approved a domain-owned Restriction containing
IDs, Unrestricted or ExactMembership{SubjectType, SubjectID, Relation}. Zero
matches no rows and mixed forms reject. Inbound validates the supported model;
PostgreSQL executes the selected predicate and memory rejects membership. The
domain Read API returns persisted Position/Row data and has no principal input.

### I3 — Concurrency semantics

Retain existing guarded authorization mutations while agreeing the contract.
Document current admission-time middleware semantics, bounded-cache freshness,
collection page/pull boundaries, and serialized atomic tuple writes. Never imply
that ordinary middleware holds its snapshot through the protected handler or that
opening an ambient transaction alone makes check-plus-write atomic. Keep guardian
invariants and durable audit in the mutation transaction. Add deterministic behavior
tests for the ordinary middleware boundary and preserve existing mutation race tests.
Settle remaining policy choice explicitly with the owner; no silent weakening.

### I4 — Verification and doctrine

Use named implementer and architecture/backend reviewers according to their role
files. Run goimports, affected module build/test/vet and race tests,
HTTP behavior checks, architecture guards and sanitized 42-module make check.
Add a narrow boundary regression guard where practical. No live database mutation
without confirming an owned scratch fixture. SQL tests may compile/skip absent a
fixture, with the limitation recorded. Preserve verification results and completed
plan under plans/ when finished. Current release notes/AUDIT record breaking API
moves as unreleased, never change prior released module contents/tags.

## Tasks

- [x] I1 Authentication callbacks and exact prepared command, migration/tests.
- [x] I2 Inbound collection orchestration, storage restrictions and tests.
- [x] I3 Concurrency contract and deterministic middleware behavior test.
- [x] I4 Documentation, reviews and full verification.

## Preserved state

Owner changes: plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
plans/segovia-v2-audit-upgrade-handoff.md; pre-existing .github/scripts/__pycache__.
Evidence: /tmp/gopernicus-inbound-authorization. No commit/push/tag authorized by
this request. Prior GitHub Linux SDK RangeEdges failure is outside this scope.

## Review and concurrency record

The architecture review approved the data-restriction design and preservation of
atomic tuple writes. One FilterPage call holds one authorization snapshot across
all pulls; business reads and bypass evaluation remain separate. The middleware
revocation interleaving test passes. An optional owner question asks whether to
retain ordinary admission plus atomic permission changes; the
implementation preserves the existing stronger mutation contract. No mutation
check is converted to a detached precheck.

## Execution record

- I1 moved invitation/user-administration policies and contracts into inbound/http,
  removed domain callback fields/options and authorized wrappers, and migrated all
  repository callers. Opaque PreparedCreate is bound to its originating service;
  inspection copies metadata, execution performs no second normalization/lookup,
  and zero/foreign/canceled values reject. Resource/relation/inviter normalization
  now precedes policy on both pending and direct-add paths, closing their previous
  preparation mismatch. HTTP nil/denied/error/cancellation checks prevent execution.
- I2 moved document policy selection, bypasses and encrypted cursors inbound.
  Domain reads have no principal and validate explicit data restrictions. SQL
  applies selected exact membership in one statement; memory rejects unsupported
  membership. All three strategies, scan budgets and continuation privacy remain.
  A new Unicode expansion regression proves valid names can continue paging when
  their lowercase sort key grows beyond the original name's byte length.
- I3 preserves admission for ordinary operations and atomic guards for tuple
  mutations. The optional owner question produced no different ruling; existing
  stronger write semantics were retained, as stated during execution. This is an
  explicit exception for authorization-data mutation, not a requirement for
  application services to repeat role checks. A deterministic middleware test
  proves admitted work can finish after revocation while the next durable read
  denies. Cache and collection snapshot boundaries are documented accurately.
- I4: 42-module make check passed, including generated drift, build/test/vet,
  tagged compile/vet and architecture guards. Authentication, authorization and
  the full example race suites passed. Focused real HTTP regressions passed.
- A verified disposable PostgreSQL17 container exercised all 14 listing tests with
  race detection and no skips, including all strategies, query plans and changed
  grants/tenants. Atomic mutation conformance and six negative-predicate/raw-writer
  subtests passed with race detection. Only this task's container was removed.
- G28 passed, including nine synthetic rejection/allow/exemption cases. All 48
  changed Go files are goimports-clean; diff checks pass. All 1745 Go/build inputs
  remained identical throughout the final gate. No dependencies, SQL schema,
  cache protocol or generated artifacts changed. Three owner plan files and the
  pre-existing Python cache were preserved.
- Named backend and architecture reviews found no blockers. Live authentication
  datastores, Redis and remote Turso were not rerun. No benchmark/capacity claim is
  made, and no commit or publication occurred.

See [verification record](authorization-inbound-policy-verification.json) for the
changed-file manifest, exact commands, input hashes, review verdicts and limits.
