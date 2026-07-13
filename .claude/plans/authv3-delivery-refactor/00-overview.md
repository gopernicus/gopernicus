# Auth v3 delivery-runtime follow-up

Status: **DRAFT — architecture recommended; owner ratification required before execution.**
Working name: `authv3-delivery-refactor`; task prefix: `AV3D`.
Insertion point: after `AV3-9.6` and before the existing `AV3-9.7` reviewer wave.

## Outcome

Remove authentication's private durable job domain and make outbound delivery use
one of two explicit host-owned execution modes:

1. `jobs`: encrypted authentication delivery commands run on the generic jobs
   feature, with durable retry, replacement, status, stale-claim fencing, and
   terminal cleanup; or
2. `in_process`: the same delivery processor runs behind a bounded queue and
   fixed worker pool, with finite retry/status retention and explicitly ephemeral
   crash guarantees.

The refactor must preserve the auth-v3 security properties already implemented:

- forgot-password and passwordless starts perform no account lookup, challenge
  issuance, rendering, or provider call on the request path;
- persisted durable payloads are encrypted and contain no plaintext destination
  or secret;
- a retry resends the exact same rendered secret rather than minting a new one;
- duplicate starts are idempotent and an explicit resend supersedes older active
  work under the same PII-free logical key;
- provider calls, attempts, retry delay, queue size, shutdown, and retained status
  are bounded;
- terminally undeliverable challenges are discarded best-effort;
- status reveals only lifecycle data to the existing session-gated receipt flow;
  and
- production wiring fails closed instead of silently selecting an ephemeral mode
  or accepting a jobs mode whose runtime is never started.

## Decision

### Jobs execute work; events observe it

Authentication submits delivery work directly to generic jobs in durable mode.
The sdk events bus is not a queue: its asynchronous mode may drop events and its
synchronous mode is only as durable as its handlers. Putting an event in front of
the job enqueue adds a second failure boundary without improving durability.

An optional, narrow delivery observer may publish secret-free accepted,
initialized, skipped, delivered, retried, dead-lettered, superseded, and purged
events in either mode. Those events are operational/domain effects only. Their
failure never changes the already-recorded job state and they are never required
to make delivery happen.

A future auth-domain transactional outbox remains valid for state changes that
must commit with an event. It is separate from this transport refactor and must
not be simulated with emit-after-commit.

### No automatic production fallback

`DeliveryMode` is explicit. Construction never selects a mode merely because a
collaborator happens to be non-nil:

| mode | accepted work survives restart | cross-instance coordination | retry/status | required host lifecycle |
|---|---|---|---|---|
| `jobs` | yes, when backed by a durable jobs store | yes | durable and retained by jobs | host runs generic jobs runtime |
| `in_process` | no | no | process-local and retention-bounded | host runs auth delivery runtime |
| `off` | n/a | n/a | none | allowed only when no configured auth capability can send |

Development may deliberately select `in_process`. Production requires an
explicit ephemeral-delivery acknowledgment for that mode. The recommended
production posture is `jobs`.

### Why generic jobs must be hardened first

The current jobs queue is close, but it cannot safely replace `DeliveryJobs` as
written:

1. `Complete` and `Fail` are not fenced by the worker that owns the claim, so a
   stale worker can mutate a job after another worker reclaims it.
2. Job ID is the only idempotency key. Authentication needs a stable logical key
   separate from unique execution IDs, plus atomic enqueue-once and replace.
3. There is no atomic claimed-payload checkpoint. Opaque auth work must persist
   its newly rendered encrypted secret before the provider call so every retry
   sends the same secret.
4. Failure requeues immediately. Auth delivery needs bounded backoff, permanent
   failure, and a post-dead-letter cleanup hook.
5. There is no latest-by-logical-key status, cancellation/supersession state, or
   bounded terminal purge.

These become general jobs capabilities with shared memory/pgx/turso conformance.
Authentication must not recreate them in a renamed feature-local repository.

## Delivery processing model

Every delivery uses one versioned, encrypted envelope and one reusable processor:

```text
request/service
    -> dispatcher.Submit or dispatcher.Replace
        -> jobs queue (durable) OR bounded queue (ephemeral)
            -> processor opens envelope
                -> opaque? resolve + issue + render
                -> checkpoint rendered encrypted envelope
                -> bounded provider send
                -> complete / retry / dead-letter + discard
```

For opaque enumeration-safe starts, admission stores only normalized resolution
input inside the encrypted envelope. Initialization runs after admission. The
rendered checkpoint is claim-fenced and durable before send in jobs mode. A crash
after provider acceptance but before completion may resend the same message; this
is intentionally at-least-once, not exactly-once. Redemption remains single-use.

Replacement prevents future processing of superseded work and fences a stale
worker's checkpoint/completion. It cannot retract a provider call already in
flight. The freshly issued challenge invalidates/replaces the old proof where the
auth flow supports replacement; documentation and tests must state this race
honestly.

## Public-boundary direction

- Remove `Repositories.DeliveryJobs` and the public
  `domain/deliveryjob.Repository`.
- Add explicit delivery configuration and narrow stdlib-typed queue/processor
  seams; authentication still imports no sibling feature.
- The generic jobs service exposes the primitive methods necessary for a
  consuming feature to match structurally without importing jobs domain types.
- A composition adapter may import both features, but neither feature core
  imports the other.
- Replace `RunDeliveryWorker` with a host-owned runtime only for `in_process`.
  In `jobs` mode the host runs `jobs.Runtime`; auth exposes the registered job
  kind/handler seam.
- Preserve `DeliveryStatus` at the auth boundary, mapping generic job lifecycle
  to the existing secret-free projection.

Exact names are frozen in phase 0 after checking repository compatibility; these
points are architectural constraints, not a mandate for one premature Go shape.

## Phase queue

| Phase | File | Depends on | Gate produced |
|---|---|---|---|
| 0 | `01-contracts-and-jobs.md` | auth v3 through AV3-9.6 | mode, delivery, and generic-jobs contracts frozen |
| 1 | `01-contracts-and-jobs.md` | phase 0 | generic jobs has fenced/keyed/checkpointed retry primitives |
| 2 | `02-auth-processor.md` | phases 0-1 contracts | one transport-neutral auth delivery processor |
| 3 | `03-durable-jobs-mode.md` | phases 1-2 | durable generic-jobs mode preserves all auth semantics |
| 4 | `04-bounded-mode.md` | phase 2 | bounded in-process mode with honest weaker guarantees |
| 5 | `05-migration-and-closeout.md` | phases 3-4 | bespoke queue removed, hosts/docs/proof migrated |

Phases 3 and 4 may be implemented in either order after phase 2, but use one
implementer at a time to avoid colliding on the shared processor and construction
matrix.

## Execution protocol

- Execute one `AV3D-x.y` task at a time and append its evidence to the owning
  phase file.
- Preserve the current dirty auth-v3 worktree. Do not reset, squash, or rewrite
  unrelated changes.
- Behavioral characterization lands before deleting the bespoke implementation.
- Generic jobs contracts and conformance land before the auth adapter consumes
  them.
- Both jobs and authentication migration trees maintain pgx/turso filename
  parity.
- Run narrow module gates plus `make guard` per code task; run `make check` and
  `make guard` at phase gates.
- Live pgx/turso, restart, stale-claim, resend, and real-provider checks cannot be
  closed by hermetic tests alone.
- After two failed repairs for the same root cause, stop instead of weakening a
  guarantee.

## Standing invariants

- No request-path provider call or account lookup for unauthenticated starts.
- No goroutine per request, unbounded channel, unbounded retry loop, or unbounded
  status map.
- No plaintext secret/destination in durable generic job columns, logs, events,
  errors, metrics labels, or status responses.
- A checkpoint, completion, failure, or retry from a stale/superseded claim fails
  with conflict and cannot clobber the current execution.
- Backoff is context-cancellable and provider timeout sits safely inside the
  claim lease.
- Events are never the only record that accepted delivery work.
- `Register` starts no goroutines. Hosts explicitly run the selected runtime.
- `jobs` production mode validates durable store and runtime acknowledgment;
  `in_process` production mode validates explicit crash-loss acknowledgment.
- The authentication core, jobs core, and sdk preserve their module boundaries.

## Preflight and insertion into auth-v3 closeout

Before `AV3D-0.1`:

1. Confirm `AV3-9.1` through `AV3-9.6` remain green and `AV3-9.7/9.8` are open.
2. Record `git status --short` and preserve all current changes.
3. Run jobs, authentication, sdk/workers, `make check`, and `make guard` baselines.
4. Record live pgx/turso availability for both jobs and authentication.
5. Ratify the two-mode decision and the production acknowledgment posture.

The existing `AV3-9.7` reviewer wave is intentionally delayed until phase 5 is
complete. That reviewer audits auth v3 plus this refactor as one final system;
`AV3-9.8` applies accepted findings and performs the PR-ready reverification.
Authorization v3 execution remains paused until that combined closeout, so its
effects work can consume the settled jobs/events pattern.

## Global stop conditions

Stop for owner direction if implementation would require:

- synchronous lookup or provider delivery for forgot-password/passwordless;
- treating best-effort event emission as accepted durable work;
- plaintext durable auth payloads;
- weakening single-use challenge redemption;
- silently allowing ephemeral production delivery;
- maintaining a second auth-specific durable queue after cutover; or
- a jobs API change that forces unrelated consumer features to import auth.

## Execution log

Append only. Phase files own task-level entries; this overview receives the final
handoff entry before `AV3-9.7` begins.
