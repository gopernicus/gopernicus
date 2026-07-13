# Auth v3 delivery-runtime follow-up task board

Status only. Phase files contain authority, acceptance criteria, verification,
and execution logs. This track runs after `AV3-9.6` and before `AV3-9.7`.

Suggested implementer prompt:

> Execute task `AV3D-x.y` from
> `.claude/plans/authv3-delivery-refactor/<phase-file>.md`. Read
> `00-overview.md`, the full phase file, `.claude/agents/implementer.md`,
> `ARCHITECTURE.md`, and every cited module contract first. Preserve the current
> dirty worktree, implement only that task, run its exact verification, append
> the execution log, and update this board.

## Phase 0 — decisions and characterization

- [ ] AV3D-0.1 — ratify delivery modes and construction matrix
- [ ] AV3D-0.2 — characterize existing delivery security behavior
- [ ] AV3D-0.3 — freeze generic jobs extension vocabulary

## Phase 1 — generic jobs hardening

- [ ] AV3D-1.1 — lease-fenced kernel and queue transitions
- [ ] AV3D-1.2 — logical-key admission and atomic supersession
- [ ] AV3D-1.3 — claimed-payload checkpoint
- [ ] AV3D-1.4 — retry policy, terminal callback, and bounded purge
- [ ] AV3D-1.5 — memory/pgx/turso conformance and live race proof

## Phase 2 — authentication delivery processor

- [ ] AV3D-2.1 — versioned command and processor contract
- [ ] AV3D-2.2 — move initialization and delivery policy into processor
- [ ] AV3D-2.3 — dispatcher and secret-free status seams
- [ ] AV3D-2.4 — migrate every outbound producer
- [ ] AV3D-2.5 — optional lifecycle observer/events adapter

## Phase 3 — durable generic-jobs mode

- [ ] AV3D-3.1 — composition adapter and host wiring
- [ ] AV3D-3.2 — encrypted admission and checkpointed initialization
- [ ] AV3D-3.3 — duplicate, resend, and stale-worker behavior
- [ ] AV3D-3.4 — retry, terminal cleanup, lifecycle, and retention
- [ ] AV3D-3.5 — production/live-store/restart/real-interaction proof

## Phase 4 — bounded in-process mode

- [ ] AV3D-4.1 — fixed pool and bounded admission
- [ ] AV3D-4.2 — process-local idempotency, replacement, and status retention
- [ ] AV3D-4.3 — checkpoint, retry, timeout, and terminal cleanup
- [ ] AV3D-4.4 — enumeration and saturation proof
- [ ] AV3D-4.5 — construction and real-interaction proof

## Phase 5 — migration and closeout handoff

- [ ] AV3D-5.1 — remove bespoke delivery persistence and worker
- [ ] AV3D-5.2 — canonical migrations and adopter upgrade
- [ ] AV3D-5.3 — proof-host variants and operational health
- [ ] AV3D-5.4 — public docs and compatibility inventory
- [ ] AV3D-5.5 — final implementation-complete gate and AV3-9.7 handoff
