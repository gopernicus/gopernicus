# Authorization v3 task board

Status only. Phase files contain authority, acceptance criteria, and execution
logs. Do not check a task until its exact verification passes and its phase log
entry is written.

## Phase 0 — security foundations

- [ ] AZ3-0.1 — exact principal, subject, userset, decision, and error vocabulary
- [ ] AZ3-0.2 — strict schema compiler and immutable snapshot contract
- [ ] AZ3-0.3 — evaluation limits and construction matrix
- [ ] AZ3-0.4 — mutation, scope revision, idempotency, outcome, and replay contract
- [ ] AZ3-0.5 — actor, guard, SystemMutator, and audit contracts

## Phase 1 — decision engine correctness

- [ ] AZ3-1.1 — exact relation-aware userset expansion across all readers
- [ ] AZ3-1.2 — immutable compiled schema wiring and deterministic validation
- [ ] AZ3-1.3 — bounded traversal, cancellation, and path-correct cycle handling
- [ ] AZ3-1.4 — complete LookupResources and Check/Lookup parity
- [ ] AZ3-1.5 — effective role enumeration and validation symmetry
- [ ] AZ3-1.6 — stable decision reasons and bounded explain surface

## Phase 2 — atomic stores and migrations

- [ ] AZ3-2.1 — canonical migrations, scope revisions, and mutation receipts
- [ ] AZ3-2.2 — reference-memory atomic mutation repositories
- [ ] AZ3-2.3 — pgx atomic relationship and role mutation repositories
- [ ] AZ3-2.4 — turso atomic relationship and role mutation repositories
- [ ] AZ3-2.5 — shared conformance and repeated dual-dialect race proof
- [ ] AZ3-2.6 — host upgrade runbook draft

## Phase 3 — guarded mutation service

- [ ] AZ3-3.1 — guarded relationship grant/revoke/replace lifecycle
- [ ] AZ3-3.2 — atomic last-owner/guardian invariants
- [ ] AZ3-3.3 — guarded role assign/unassign and effective-grant result
- [ ] AZ3-3.4 — SystemMutator capability and legacy API transition
- [ ] AZ3-3.5 — mutation policy, retry, stale revision, and audit attempt suite

## Phase 4 — proof host (file 07)

A defect found during proof reopens the owning implementation phase, never
closeout; phase 5 does not begin until this phase's gate passes.

- [ ] AZ3-4.1 — auth-cms guarded and SystemMutator composition
- [ ] AZ3-4.2 — exact-semantics and concurrency proof protocol

## Phase 5 — documentation and closeout (file 07)

- [ ] AZ3-5.1 — final migration parity and execute upgrade runbook
- [ ] AZ3-5.2 — public README/API/migration documentation
- [ ] AZ3-5.3 — release and compatibility inventory
- [ ] AZ3-5.4 — final adversarial and race audit
- [ ] AZ3-5.5 — implementation-complete hermetic/live gate
- [ ] AZ3-5.6 — post-implementation reviewer gate
- [ ] AZ3-5.7 — accepted remediation, reverification, and PR-ready handoff

## Deferred follow-ups — not part of authorization v3 completion

Effects tasks `AZFX-1` through `AZFX-5` live in
`05-effects-and-observability.md`. Generic admin tasks `AZADM-1` through
`AZADM-6` live in `06-admin-and-proof-host.md`. They remain intentionally off
this milestone checklist until separately ratified. AZADM is blocked
indefinitely: it is unschedulable until a separately ratified authentication
follow-up packet ships the public `SensitiveMutationProtector` seam; no such
packet currently exists or is implied, and authorization must never unblock
itself by importing authentication internals.
