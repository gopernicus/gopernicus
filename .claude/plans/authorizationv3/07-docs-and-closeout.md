# Phases 4 and 5 — proof host, then documentation and closeout

File/phase map: file 07 holds phases 4 and 5; files 05 and 06 are deferred
follow-up packets (effects; generic admin), not phases of this milestone.

Status: DRAFT; phase 4 is ready after phases 0–3; phase 5 is ready only after
the phase-4 proof gate passes.
Depends on: every v3 correctness-kernel implementation phase and its execution
log. Deferred effects/admin packets are not prerequisites.

## Phase 4 — proof host

### Goal

Prove guarded and `SystemMutator` host composition and the exact-semantics/
concurrency behavior of the finished kernel through the auth-cms proof host.

Gate rule: a defect found during proof reopens the owning implementation phase
(0–3), never closeout; phase 5 does not begin until the phase-4 proof gate
passes.

### Task AZ3-4.1 — auth-cms guarded and SystemMutator composition

Touch: auth-cms composition root/demo/tests only, plus its self-contained
`go.mod` when store dependencies change.

Implement/prove:

- adapt `identity.Principal` directly to authorization `Actor`/
  `PrincipalRef` at the host;
- compose a host `MutationGuard` from schema-declared `manage_access` and the
  separately composed platform-admin recipe, using only the supplied
  dependency-tracking decision view;
- prove invitation acceptance runs through the deliberately held `SystemMutator`
  with its stable MutationID derived from the invitation operation. The
  API/compile-site transition itself is owned by AZ3-3.4; this task proves the
  composed host behavior only;
- remove or disable the current session-only role/relationship mutation demo
  endpoints. Do not claim recent-auth protection until authentication exports a
  public sensitive-operation protector. Be honest about the consequence:
  examples/auth-cms intentionally loses its browser-driven role-assignment
  surface until the deferred AZADM packet lands — the proof here is host code
  plus tests, not a browser flow;
- keep the no-authorization and host-authored-closure postures demonstrable; and
- prove ordinary host code has no raw write method or constructible system-actor
  synonym.

Verify:

```sh
cd examples/auth-cms && go test -race ./... -run 'Authorization|Guard|SystemMutator|Invitation|Posture'
make guard
```

Acceptance: untrusted service calls cannot self-grant; invitation acceptance is
visibly trusted and idempotent; no shipped HTTP route mutates authorization with
session presence alone.

### Task AZ3-4.2 — exact-semantics and concurrency proof protocol

Depends on: AZ3-4.1.

Drive and record through service/host composition tests:

1. ordinary member cannot self-grant;
2. authorized manager grants an exact `group#member` subject and a concrete
   member gains access;
3. a `group#admin` grant does not authorize an ordinary member;
4. decision APIs reject userset-valued callers;
5. a navigational Through relation rejects userset targets at schema compile;
6. non-self Through-root hierarchy Check and Lookup return the same descendants;
7. global role appears in effective role enumeration;
8. scoped revoke while global remains reports `same_role_grant_remains`;
9. two concurrent last-owner revokes produce one success/one invariant block;
10. stale revision and MutationID payload mismatch return stable errors;
11. exact retry returns the original outcome with `Replayed=true`; and
12. resource teardown is possible only through the separately held
    `SystemMutator` command with a recorded reason.

Acceptance: the transcript includes command/results, receipts/revisions, stored
rows, and audit observations without secrets. It claims no effects delivery or
generic admin API.

### Phase 4 acceptance

- Guarded and `SystemMutator` composition is proven in auth-cms host code and
  tests, and the exact-semantics/concurrency transcript exists.
- No defect found during proof remains open: each one reopened and closed its
  owning implementation phase (0–3), never closeout.
- `make check` and `make guard` pass.

## Phase 5 — documentation and closeout

### Goal

Turn the proven implementation into an adoptable live-proven milestone and run
one post-implementation review/remediation gate. Phase 5 does not begin until
the phase-4 proof gate passes.

### Task AZ3-5.1 — final migration parity and execute upgrade runbook

Implement:

- compare pgx/turso migration filename sets and schema inventories;
- run the upgrade audit against a populated v1 fixture containing direct groups,
  valid usersets, deliberately ambiguous usersets, relationships, global/scoped
  roles, and last owners;
- prove ambiguous rows stop the upgrade until explicitly repaired;
- apply the adopter path, boot v3, compare expected access, then exercise rollback
  only to the documented boundary; and
- publish the runbook in the canonical release/feature documentation.

Acceptance: no upgrade step relies on resetting a real adopter database or
silently interpreting missing relation as `member`.

### Task AZ3-5.2 — public README/API/migration documentation

Document:

- three adoption postures and independent kinds;
- exact userset semantics with member/admin examples;
- immutable schema compilation/digest and all validation failures;
- evaluation limits, indeterminate errors, Check/Lookup parity, and fail-closed
  caller guidance;
- actor/guard/SystemMutator APIs, dependency revision validation, MutationID,
  receipt retention, outcomes versus replay, last-owner/teardown invariants,
  and legacy migration;
- the constructor-shape amendment in `features/README.md`: the
  `Components{Service, SystemMutator}` bundle versus FS2's
  `svc, err := NewService(repos, cfg)`, stating whether the bundle is the new
  sanctioned shape or an authorization-only exception (that determination
  happens at ratification/execution, not here);
- raw versus effective role listing;
- migration source/order and live-store commands; and
- explicit non-goals: ABAC policy kind, role implication, cache, templ UI; and
- deferred follow-ups: effects and generic admin, including the missing public
  authentication sensitive-operation prerequisite.

Acceptance: a host can wire safely without reading internal code or this plan.

### Task AZ3-5.3 — release and compatibility inventory

Record:

- breaking public API/port/schema changes and next tag floor, using the
  per-module tag floors now recorded in `RELEASING.md` as the vocabulary;
- consumer changes in auth invitations, events authorization closure, auth-cms,
  and external host recipes;
- canonical migration rewrite versus append-only decision at actual preflight;
- the settled jobs/work dependency: `sdk/capabilities/work` is a new first-tag
  module and `features/jobs` carries a MINOR floor from the delivery refactor —
  record what authorization adds on top, if anything;
- the sdk graduation decision: re-run the three-gate sdk graduation protocol for
  the authorization check/decision vocabulary — this milestone fires the
  ARCHITECTURE protocol-table trigger ("authorizationv3 settles its semantics")
  — and record graduate/re-defer with reasons;
- every accepted premise adaptation and deferred item; and
- module/go.work/Makefile/guard changes: commit to a sixteenth layering guard,
  `guard-authorization-no-delivery-repo` — the `guard-auth-no-delivery-repo`
  shape pointed at `features/authorization` migrations/repositories, proving no
  authorization-specific jobs/delivery table exists — numbered consistently
  with the existing fifteen.

Acceptance: release notes distinguish semantic access changes from source-only
renames.

### Task AZ3-5.4 — final adversarial and race audit

Audit tests/code for:

- any ignored `Subject.Relation`/`subject_relation` or hard-coded `member`;
- mutable schema references;
- read/count/write and delete/create mutation sequences;
- decision/lookup divergence and partial list success;
- unbounded recursion/fan-out/batches;
- missing actor/guard; `SystemMutator` unreachability from ordinary HTTP code
  must be covered by a named permanent regression test, not a one-time audit
  check;
- MutationID replay/payload mismatch and revision gaps;
- last-owner race and effective global role fallback;
- dependency scopes locked without canonical order or revisions left
  unvalidated;
- effects/admin code accidentally pulled into the v3 kernel;
- unsafe logs/metrics and unbounded IDs as metric labels;
- a shipped session-only authorization mutation route; and
- goroutines started by authorization Register.

Use `go test -race` for core/memory/concurrency and repeated live dialect races.
Every finding is fixed, rejected with evidence, or explicitly safely deferred.

### Task AZ3-5.5 — implementation-complete hermetic/live gate

Run and record:

```sh
make check
make guard
cd features/authorization && go test -race ./...
```

Plus full pgx and Turso live conformance, repeated concurrent mutation tests,
upgrade protocol, and the guarded/SystemMutator proof-host protocol. Live skips
do not close the task. Live legs follow the auth v3 recipe:
`authv3-pg` (C-collation) and `authv3-libsql` containers, fresh/reset databases,
explicit `-count=1`, and harness margins that let concurrent attempts reach
terminal results before database teardown under `-race` slowdown.

### Task AZ3-5.6 — post-implementation reviewer gate

Depends on: AZ3-5.5.

Run one review wave over the completed implementation — the auth v3 shape (one
post-implementation wave, then owner-gated remediation in AZ3-5.7) — focusing on:

- ReBAC/userset semantics and graph correctness;
- database atomicity/isolation and migration safety;
- guarded/system capability and host identity boundaries;
- effects/admin deferral boundaries;
- public API compatibility and documentation; and
- proof-host realism.

Consolidate findings by root cause and severity. No PR is opened yet.

### Task AZ3-5.7 — accepted remediation, reverification, and PR-ready handoff

Depends on: AZ3-5.6.

Apply accepted findings without weakening the packet's invariants. Add regression
tests, rerun AZ3-5.5 gates, update runbook/docs/release inventory, and produce the
final accepted/rejected/deferred finding table plus exact verification evidence.

Acceptance: no required work or accepted finding remains.

### Phase 5 acceptance

- Migration/runbook, docs, release inventory, adversarial audit, and guarded
  proof-host evidence are complete.
- Hermetic, race, dual-store live, upgrade, and host-composition gates pass.
- Review remediation is reverified.
- Authorization v3 is ready for a separately authorized PR/tag workflow.

## Stop conditions (both phases)

- A live database leg is unavailable or failing.
- Upgrade ambiguity remains unresolved.
- A reviewer finds a potential overgrant, double mutation, system-capability
  leak, or shipped session-only mutation path not covered by a regression test.
- Effects or generic admin work is pulled into this milestone without a
  separately ratified contract and prerequisites.

## Execution log

Append only during execution; tag each entry with its phase and task ID.
