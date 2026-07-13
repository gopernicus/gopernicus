# Auth v3 task board

This is status only; phase files contain the authority and acceptance criteria.
Mark a box complete only after the task's verify commands pass and its dated
phase execution-log entry is written.

Suggested implementer prompt:

> Execute task `AV3-x.y` from `.claude/plans/authv3/<phase-file>.md`. Read
> `.claude/plans/authv3/00-overview.md`, the full phase file, its cited design
> sections, `.claude/agents/implementer.md`, and `ARCHITECTURE.md` first. Verify
> dependencies, preserve existing worktree changes, implement only that task,
> run its exact verification, append the execution log, and update `TASKS.md`.

## Phase 0 — security foundations

- [x] AV3-0.1 — method, assurance, and runtime configuration vocabulary
- [x] AV3-0.2 — HMAC challenge protector and privacy keyer
- [x] AV3-0.3 — atomic challenge and recent-auth repository specifications
- [x] AV3-0.4 — credential-policy and optimistic-mutation specification
- [x] AV3-0.5 — HTTP security primitives
- [x] AV3-0.6 — delivery-job repository specification

## Phase 1 — identifiers

- [x] AV3-1.1 — identifier entity, kinds, uses, and normalization
- [x] AV3-1.2 — reshape user and define atomic repository contracts
- [x] AV3-1.3 — contact-change flow state
- [x] AV3-1.4 — update proof-host memory repositories

## Phase 2 — schema and stores

- [x] AV3-2.1 — transitional canonical migrations and parity test
- [x] AV3-2.2 — pgx store implementations
- [x] AV3-2.3 — turso store implementations
- [x] AV3-2.4 — dual-dialect live conformance and race evidence
- [x] AV3-2.5 — host upgrade runbook draft

## Phase 3 — challenges and recovery

- [x] AV3-3.1 — challenge issue/consume/redeem service
- [x] AV3-3.2 — migrate registration verification
- [x] AV3-3.3 — atomic password-reset composition
- [x] AV3-3.4 — password policy hardening
- [x] AV3-3.5 — retire the legacy verification rail
- [x] AV3-3.6 — live atomic recovery proof

## Phase 4 — delivery

- [x] AV3-4.1 — shared delivery renderer/router
- [x] AV3-4.2 — delivery queue service and worker
- [x] AV3-4.3 — migrate all existing outbound sites
- [x] AV3-4.4 — production transport and worker wiring validation
- [x] AV3-4.5 — worker real-interaction check

## Phase 5 — service re-key

- [x] AV3-5.1 — registration/login/token/recovery/resolver re-key
- [x] AV3-5.2 — OAuth matching and adoption hardening
- [x] AV3-5.3 — invitations and kind-aware identity seam
- [x] AV3-5.4 — PII-free rate limits and trusted client IP
- [x] AV3-5.5 — remove transitional email-on-user surface and finalize schema
- [x] AV3-5.6 — regression and live re-key proof

## Phase 6 — credential suite

- [x] AV3-6.1 — recent-authentication grant service and routes
- [x] AV3-6.2 — masked `/auth/methods`
- [x] AV3-6.3 — set/change/remove password
- [x] AV3-6.4 — provider-bound OAuth unlink
- [x] AV3-6.5 — add/change/remove identifier flows
- [x] AV3-6.6 — route/error/event inventory and live suite proof

## Phase 7 — passwordless

- [x] AV3-7.1 — enablement and construction matrix
- [x] AV3-7.2 — asynchronous passwordless start
- [x] AV3-7.3 — OTP verification and session mint
- [x] AV3-7.4 — magic-link redemption and URL safety
- [x] AV3-7.5 — events, errors, timing, and live proof

## Phase 8 — HTML/templ adapters and proof host

- [x] AV3-8.1 — authentication Views port and exported view models
- [x] AV3-8.2 — bundled authentication `views/templ` module
- [x] AV3-8.3 — dual JSON/form dispatch and public HTML handlers
- [x] AV3-8.4 — account-security HTML handlers
- [x] AV3-8.5 — partial override and presentation-isolation proof
- [x] AV3-8.6 — auth-cms composition root and development secrets
- [x] AV3-8.7 — magic-link landing integration
- [x] AV3-8.8 — account-security demonstration surface
- [x] AV3-8.9 — host override and production safeguards
- [x] AV3-8.10 — complete JSON + HTML run-and-look protocol

## Phase 9 — documentation and closeout

> After AV3-9.6, execute `.claude/plans/authv3-delivery-refactor/` completely
> before AV3-9.7. The reviewer gate covers both bodies of work.

- [x] AV3-9.1 — final canonical migration audit
- [x] AV3-9.2 — publish and execute the host upgrade runbook
- [x] AV3-9.3 — public feature and sdk documentation
- [x] AV3-9.4 — release/change inventory
- [x] AV3-9.5 — final adversarial audit
- [x] AV3-9.6 — implementation-complete hermetic and live gate
- [ ] AV3-9.7 — post-implementation full reviewer gate
- [ ] AV3-9.8 — reviewer remediation, reverification, and PR-ready handoff
