# Phase 4 — bounded in-process mode

Depends on phase 2. This is a deliberate simple-deployment mode, not a fake
durable queue.

## Outcome

Run the same authentication delivery processor in a host-owned fixed worker pool
with bounded admission, bounded retries, bounded memory, graceful shutdown, and
honest restart loss.

### AV3D-4.1 — fixed pool and bounded admission

Implement a fixed worker count and finite queue capacity. Never start one
goroutine per request. Enqueue either admits within a configured short admission
deadline or returns a typed capacity/closed error.

`RunDelivery(ctx)` owns the pool lifecycle. Cancellation stops admission, lets
in-flight provider calls observe cancellation, and drains only within a configured
shutdown bound. `Register` starts no goroutines.

### AV3D-4.2 — process-local idempotency, replacement, and status retention

Implement submit-once/replace/latest semantics under one lock/arbiter using logical
keys and generations. Replacement prevents queued old generations from starting
and fences old checkpoints/completions where possible.

Status/history storage has explicit maximum entries and TTL. Eviction returns
unknown status; it never grows with total process lifetime. Document that restart
loses queued work, de-duplication, and status.

### AV3D-4.3 — checkpoint, retry, timeout, and terminal cleanup

Checkpoint the rendered envelope in the in-memory work item before send so retry
within the same process reuses the same secret. Apply the same provider timeout,
error classification, attempt cap, capped context-aware backoff, observer, and
terminal challenge discard as jobs mode.

There is no claim lease across restart. Tests must not imply durability or
cross-instance single execution.

### AV3D-4.4 — enumeration and saturation proof

For forgot-password/passwordless, capacity, closed-runtime, and shutdown outcomes
must be independent of whether the identifier exists because no account lookup has
yet occurred. Apply the same response class and timing policy to known and unknown
inputs; never silently return accepted after dropping work.

For account-resolved operations, surface dispatch failure according to the
operation's committed/notification contract. Record committed-but-not-notified
states honestly where applicable.

### AV3D-4.5 — construction and real-interaction proof

Prove invalid capacities, workers, attempts, timeouts, retention, and shutdown
bounds fail at construction. Production without explicit ephemeral acknowledgment
fails. Multi-instance documentation must warn that each process has independent
de-duplication and queues.

Run real mail/notifier checks for normal, saturation, retry, cancellation, and
shutdown. Use leak/race checks to prove goroutine count stays bounded.

### Phase 4 gate

Run authentication and bounded-runtime suites under `-race`, goroutine/leak proof,
real interaction, `make check`, and `make guard`.

## Execution log

Append task evidence including configured bounds and measured maximum worker/
goroutine counts.
