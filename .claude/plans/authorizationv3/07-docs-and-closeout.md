# Phase 6 — documentation and closeout

Status: DRAFT; ready after phases 0–5.
Depends on: every implementation phase and its execution log.

## Goal

Turn the implementation into an adoptable, live-proven milestone and run one
post-implementation review/remediation gate.

## Task AZ3-6.1 — final migration parity and execute upgrade runbook

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

## Task AZ3-6.2 — public README/API/migration/effects documentation

Document:

- three adoption postures and independent kinds;
- exact userset semantics with member/admin examples;
- immutable schema compilation/digest and all validation failures;
- evaluation limits, indeterminate errors, Check/Lookup parity, and fail-closed
  caller guidance;
- actor/guard/trusted-system mutation APIs, MutationID, revisions, dispositions,
  last-owner invariants, and legacy migration;
- raw versus effective role listing;
- off/procedural/events effects guarantee table and generic jobs/events wiring;
- optional admin route table and HTTP protection requirements;
- migration source/order and live-store commands; and
- explicit non-goals: ABAC policy kind, role implication, cache, templ UI.

Acceptance: a host can wire safely without reading internal code or this plan.

## Task AZ3-6.3 — release and compatibility inventory

Record:

- breaking public API/port/schema changes and next tag floor, using the
  per-module tag floors now recorded in `RELEASING.md` as the vocabulary;
- consumer changes in auth invitations, events authorization closure, auth-cms,
  and external host recipes;
- canonical migration rewrite versus append-only decision at actual preflight;
- the settled jobs/work dependency: `sdk/capabilities/work` is a new first-tag
  module and `features/jobs` carries a MINOR floor from the delivery refactor —
  record what authorization adds on top, if anything;
- every accepted premise adaptation and deferred item; and
- module/go.work/Makefile/guard changes (fifteen layering guards exist today;
  number any new ones consistently).

Acceptance: release notes distinguish semantic access changes from source-only
renames.

## Task AZ3-6.4 — final adversarial and race audit

Audit tests/code for:

- any ignored `Subject.Relation`/`subject_relation` or hard-coded `member`;
- mutable schema references;
- read/count/write and delete/create mutation sequences;
- decision/lookup divergence and partial list success;
- unbounded recursion/fan-out/batches;
- missing actor/guard or SystemActor reachable from HTTP;
- MutationID replay/payload mismatch and revision gaps;
- last-owner race and effective global role fallback;
- emit-after-commit described as durable;
- unsafe logs/events/metrics and unbounded IDs as metric labels;
- admin identity/step-up/origin/CSRF/body/error failures; and
- goroutines started by authorization Register/effect code.

Use `go test -race` for core/memory/concurrency and repeated live dialect races.
Every finding is fixed, rejected with evidence, or explicitly safely deferred.

## Task AZ3-6.5 — implementation-complete hermetic/live gate

Run and record:

```sh
make check
make guard
cd features/authorization && go test -race ./...
```

Plus full pgx and Turso live conformance, repeated concurrent mutation tests,
upgrade protocol, admin negative matrix, proof-host protocol, and both effect
modes. Live skips do not close the task. Live legs follow the auth v3 recipe:
`authv3-pg` (C-collation) and `authv3-libsql` containers, fresh/reset databases,
explicit `-count=1`, and harness margins that reach durable terminal state
before stopping runtimes (lease TTLs versus `-race` slowdown).

## Task AZ3-6.6 — post-implementation reviewer gate

Depends on: AZ3-6.5.

Run one review wave over the completed implementation — the auth v3 shape (one
post-implementation wave, then owner-gated remediation in AZ3-6.7) — focusing on:

- ReBAC/userset semantics and graph correctness;
- database atomicity/isolation and migration safety;
- API/authentication/step-up/CSRF boundaries;
- events/jobs composition and delivery guarantees;
- public API compatibility and documentation; and
- proof-host realism.

Consolidate findings by root cause and severity. No PR is opened yet.

## Task AZ3-6.7 — accepted remediation, reverification, and PR-ready handoff

Depends on: AZ3-6.6.

Apply accepted findings without weakening the packet's invariants. Add regression
tests, rerun AZ3-6.5 gates, update runbook/docs/release inventory, and produce the
final accepted/rejected/deferred finding table plus exact verification evidence.

Acceptance: no required work or accepted finding remains.

## Phase acceptance

- Migration/runbook, docs, release inventory, adversarial audit, and proof-host
  evidence are complete.
- Hermetic, race, dual-store live, upgrade, HTTP negative, and effects gates pass.
- Review remediation is reverified.
- Authorization v3 is ready for a separately authorized PR/tag workflow.

## Stop conditions

- A live database leg is unavailable or failing.
- Upgrade ambiguity remains unresolved.
- A reviewer finds a potential overgrant, double mutation, or admin bypass not
  covered by a regression test.
- Auth delivery redesign is pulled into this milestone without a separate
  ratified contract.

## Execution log

Append only during execution.
