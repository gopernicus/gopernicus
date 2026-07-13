# Phase 3 — durable generic-jobs mode

Depends on phases 1 and 2.

## Outcome

Run authentication delivery as a registered generic job kind, preserving all
durable auth-v3 semantics while authentication and jobs remain independently
importable feature modules.

### AV3D-3.1 — composition adapter and host wiring

Create the narrow composition adapter that maps auth dispatcher operations to
generic jobs submit-once/replace/latest and maps the generic job handler to the
auth processor. The adapter may import both modules; neither core may import the
other.

Resolve construction order explicitly and prove no handler can run before the
fully built auth service is attached. `Register` starts nothing; the host runs the
already-built `jobs.Runtime`.

### AV3D-3.2 — encrypted admission and checkpointed initialization

In jobs mode, every persisted payload is sealed with `DeliveryEncrypter`, including
the opaque resolution input. The handler checkpoints the rendered sealed payload
under the current worker fence before any provider call.

Prove:

- database inspection finds no destination, normalized identifier, code, token,
  subject, or rendered body in plaintext;
- restart after opaque admission initializes safely;
- restart after checkpoint sends the same secret; and
- restart after provider acceptance may resend only that same secret.

### AV3D-3.3 — duplicate, resend, and stale-worker behavior

Map auth receipt keys to generic logical keys, never execution IDs. Submit-once is
idempotent while active; replace creates a fresh generation and status selects the
latest.

Run adversarial replacement while old work is pending, initializing, checkpointed,
and sending. A stale handler cannot checkpoint or record success/failure after
supersession. Record the unavoidable already-in-flight provider race in docs.

### AV3D-3.4 — retry, terminal cleanup, lifecycle, and retention

Map transient provider errors to capped exponential retry-at, permanent errors to
immediate dead-letter, and parent cancellation to reclaimable work. Provider
timeout must be safely shorter than the claim lease.

After a successful dead-letter transition, invoke the idempotent challenge discard
hook. Map generic status to auth status and emit optional observer events. Configure
bounded generic terminal retention/purge without auth-specific SQL.

### AV3D-3.5 — production, live-store, restart, and real-interaction proof

Add construction negatives for missing encrypter, incomplete jobs capabilities,
ephemeral jobs store in production, missing runtime acknowledgment, invalid
timeout/lease/backoff, and development-only transports.

Required run-and-look proof on pgx and turso:

- known/unknown opaque starts have matching admission behavior;
- provider timeout and retry occur off request path;
- process restart at each checkpoint boundary behaves as documented;
- resend and stale-claim races converge to the latest generation;
- status and events contain no secrets; and
- terminal cleanup and purge occur.

### Phase 3 gate

Run jobs/auth/integration suites under `-race`, both live dialects, restart harness,
real interaction, migration parity, `make check`, and `make guard`.

## Execution log

Append task evidence, including exact crash points and live database results.
