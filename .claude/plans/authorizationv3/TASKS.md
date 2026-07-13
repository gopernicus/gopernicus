# Authorization v3 task board

Status only. Phase files contain authority, acceptance criteria, and execution
logs. Do not check a task until its exact verification passes and its phase log
entry is written.

## Phase 0 — security foundations

- [ ] AZ3-0.1 — exact subject, userset, decision, and error vocabulary
- [ ] AZ3-0.2 — strict schema compiler and immutable snapshot contract
- [ ] AZ3-0.3 — evaluation limits and construction matrix
- [ ] AZ3-0.4 — mutation, scope revision, idempotency, and disposition contract
- [ ] AZ3-0.5 — actor, guard, audit, and effect-mode contracts

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
- [ ] AZ3-3.3 — guarded role assign/unassign and effective disposition
- [ ] AZ3-3.4 — trusted-system path and legacy API transition
- [ ] AZ3-3.5 — mutation policy, retry, stale revision, and audit attempt suite

## Phase 4 — effects and observability

- [ ] AZ3-4.1 — stable authorization change event envelope
- [ ] AZ3-4.2 — procedural post-commit effect mode
- [ ] AZ3-4.3 — same-transaction events-outbox mode
- [ ] AZ3-4.4 — decision/mutation observers, safe logs, and metrics guidance
- [ ] AZ3-4.5 — construction negatives and real-interaction effects proof

## Phase 5 — admin surface and proof host

- [ ] AZ3-5.1 — optional protected JSON administration surface
- [ ] AZ3-5.2 — strict HTTP body/origin/identity/error contracts
- [ ] AZ3-5.3 — schema/check/explain read surface with anti-enumeration gates
- [ ] AZ3-5.4 — auth-cms guarded mutation composition and step-up
- [ ] AZ3-5.5 — exact-userset, hierarchy, role, and revoke proof protocol
- [ ] AZ3-5.6 — procedural/events host variants and negative matrix

## Phase 6 — documentation and closeout

- [ ] AZ3-6.1 — final migration parity and execute upgrade runbook
- [ ] AZ3-6.2 — public README/API/migration/effects documentation
- [ ] AZ3-6.3 — release and compatibility inventory
- [ ] AZ3-6.4 — final adversarial and race audit
- [ ] AZ3-6.5 — implementation-complete hermetic/live gate
- [ ] AZ3-6.6 — post-implementation reviewer gate
- [ ] AZ3-6.7 — accepted remediation, reverification, and PR-ready handoff
