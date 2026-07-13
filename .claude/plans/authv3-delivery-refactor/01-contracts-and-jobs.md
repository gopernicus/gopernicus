# Phases 0-1 — contracts and generic jobs hardening

Read `00-overview.md`, `sdk/foundation/workers`, `features/jobs`, the current
authentication `domain/deliveryjob`, delivery worker, store conformance, and both
features' migrations before changing code.

## Phase 0 outcome

Freeze the observable authentication guarantees and the reusable jobs contracts
before adapters or deletion. Characterization tests must fail if the new runtime
would reissue a secret on retry, leak enumeration, accept a stale claim, or turn
resend into duplicate active work.

### AV3D-0.1 — ratify modes and construction matrix

Specify public delivery-mode vocabulary, enablement, production acknowledgments,
required ports, host lifecycle ownership, and incompatible combinations.

Acceptance:

- empty/unknown mode fails loudly;
- `off` fails when any enabled flow can deliver;
- `jobs` requires the narrow generic queue capabilities, an encrypter, and a
  runtime acknowledgment in production;
- `in_process` rejects durable-status claims and requires explicit production
  crash-loss acknowledgment;
- transport capability checks remain unchanged; and
- `Register` starts no worker.

Verify with table-driven authentication construction tests and public doc
comments.

### AV3D-0.2 — characterize existing delivery behavior

Extract black-box tests from the current worker/service before refactoring:

- duplicate admission creates one active execution;
- replace creates fresh work and supersedes prior active work;
- opaque initialization is off request path;
- checkpoint precedes provider send and retry reuses byte-identical payload;
- crash-after-send may duplicate the same secret, never mint a new one;
- unknown/ineligible identifier skips without provider call;
- transient/permanent/terminal paths, backoff, timeout, discard, purge, and
  status are observable without secret leakage; and
- queue/provider failure responses preserve known/unknown parity.

The suite must be transport-neutral so phases 3 and 4 run the same cases.

### AV3D-0.3 — freeze generic jobs extension vocabulary

Add domain/port specifications and conformance test skeletons for:

- unique execution ID plus optional logical key;
- atomic enqueue-once and atomic replace/supersede by logical key;
- `canceled`/`superseded` terminal state and latest-by-key status;
- worker-owned claim fencing on checkpoint/complete/fail;
- atomic encrypted-payload checkpoint while a claim is current;
- retry-at scheduling, permanent failure, resulting transition, and dead-letter
  hook;
- bounded terminal purge; and
- deterministic conflict/not-found/already-exists behavior.

Prefer stdlib-typed service methods for cross-feature structural ports. Domain-rich
host APIs may remain alongside them.

### Phase 0 gate

Run authentication delivery characterization, jobs storetest compile, sdk/workers
tests, `make check`, and `make guard`. No queue implementation is deleted yet.

## Phase 1 outcome

Make generic jobs strong enough to carry authentication delivery without an
auth-specific persistence or worker domain.

### AV3D-1.1 — lease-fenced kernel and queue transitions

Change the worker/store contract so completion and failure identify the current
claim owner. A reclaimed or superseded claim returns conflict. The runner must not
report a stale completion as success.

Prove:

- two claimers never own one current lease;
- after lease expiry/reclaim, the old owner cannot checkpoint, complete, fail, or
  reschedule;
- the current owner can complete/fail idempotently; and
- cancellation during shutdown leaves work reclaimable without the old worker
  later clobbering it.

Update every sdk/workers and jobs test double in the same task.

### AV3D-1.2 — logical-key admission and supersession

Add logical-key storage and atomic operations:

- enqueue-once returns the current active job for the same key;
- replace terminally supersedes all active generations and inserts one new job;
- distinct execution IDs retain history;
- latest-by-key is deterministic; and
- replacement fences old claims, even if already running.

Document the irreducible race: replacement cannot retract a provider call already
accepted. Conformance must cover concurrent enqueue/replace and one-winner
behavior.

### AV3D-1.3 — claimed-payload checkpoint

Add a payload update that succeeds only for the running job and current worker.
It preserves job identity, attempts, logical key, schedule, and status. The updated
bytes must round-trip exactly in every store.

Prove checkpoint-before-side-effect and recovery:

- checkpoint failure prevents the side effect;
- after checkpoint + crash, reclaim reads the checkpointed bytes;
- a stale or superseded worker cannot checkpoint; and
- concurrent checkpoint/replacement has one valid outcome with no lost current
  generation.

### AV3D-1.4 — retry policy, terminal callback, and purge

Support context-cancellable retry-at scheduling and permanent failure without
busy-looping. Expose the successful resulting transition so a per-kind terminal
hook runs only after dead-letter is recorded. Hook failure is logged/reported but
does not resurrect the job.

Add bounded purge by terminal time and limit. Default behavior for existing jobs
consumers must be documented and compatibility-tested.

### AV3D-1.5 — memory/pgx/turso stores and live race proof

Implement the phase-1 contract in memory, pgx, and turso with matching canonical
migration filenames. Extend the shared storetest suite rather than adding
dialect-only semantics.

Required proof:

- repeated stale-claim/reclaim races;
- concurrent enqueue-once and replace;
- checkpoint versus replace;
- retry-at ordering and permanent dead-letter;
- latest-by-key and bounded purge;
- byte-exact encrypted payload round-trip; and
- fresh/reset live pgx and turso runs with recorded skips/failures.

### Phase 1 gate

Run jobs and sdk/workers suites under `-race`, both live store suites, migration
parity, `make check`, and `make guard`. Authentication still uses its existing
queue until this gate is green.

## Execution log

Append dated entries with task ID, files, exact commands, pass/skip/failure, live
DSN availability, and any premise adaptation.
