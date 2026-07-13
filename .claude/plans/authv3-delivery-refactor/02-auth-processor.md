# Phase 2 — transport-neutral authentication delivery processor

Depends on phase 0 and the frozen phase-1 jobs contract. It may begin while jobs
adapters are being implemented only if it touches no shared jobs files and uses
the frozen ports exactly.

## Outcome

Separate authentication delivery policy from queue ownership. One processor must
open, initialize, checkpoint, deliver, classify failure, discard, and observe a
command whether the executor is generic jobs or the bounded in-process runtime.

### AV3D-2.1 — versioned command and processor contract

Define one versioned encrypted command envelope covering:

- kind, purpose, logical receipt key, and opaque/rendered stage;
- normalized resolution input for opaque work;
- rendered destination/content/secret after initialization; and
- enough stable metadata for retry, status, and safe observation.

Parsing rejects unknown versions, malformed stage combinations, missing purpose,
and unsealed durable payloads. Errors and logs never include payload bytes.

The processor receives narrow checkpoint and delivery collaborators and returns
an explicit result: completed, skipped, retry-at, or permanent failure. It does
not claim jobs or own a polling loop.

### AV3D-2.2 — move initialization and delivery policy into the processor

Move the current opaque initialization, renderer/router selection, bounded
provider call, retry classification, and best-effort challenge discard behind the
processor contract.

The sequence is load-bearing:

1. open envelope;
2. if opaque, resolve/issue/render off request path;
3. seal and successfully checkpoint rendered envelope;
4. call provider under timeout;
5. return completion/retry/permanent result; and
6. discard only after a recorded terminal transition callback.

Use the characterization suite to prove identical-secret retry and no send before
checkpoint.

### AV3D-2.3 — dispatcher and secret-free status seams

Replace the internal repository-shaped queue dependency with a transport-neutral
dispatcher supporting submit-once, replace, and latest status. Keep cross-feature
surfaces stdlib-typed or define them at an integration boundary so authentication
imports no jobs package.

Preserve receipt possession plus live-session authorization. Normalize generic
job states into stable auth states without exposing worker name, failure text,
destination, secret, or raw logical key.

### AV3D-2.4 — migrate every producer to the dispatcher

Inventory and migrate all outbound sites, including:

- registration verification;
- forgot/reset password;
- passwordless magic link and OTP;
- step-up/sensitive codes;
- set/change/remove identifier proof and notices;
- OAuth pending-link/unlink paths; and
- invitations/member-added notifications.

For each site record whether it is rendered or opaque, submit-once or replace,
and whether the caller may observe a dispatch error. No site may call a provider
directly.

### AV3D-2.5 — optional lifecycle observer/events adapter

Retain a narrow secret-free observer. Provide an optional host adapter that emits
generic events for operational/domain observation. Event IDs are stable enough for
subscriber de-duplication; payloads contain bounded enums and opaque execution IDs,
not recipient identifiers.

Prove that no observer, missing subscriber, dropped async event, or observer error
can lose, retry, duplicate, or fail accepted delivery work.

### Phase 2 gate

Run the transport-neutral characterization suite, all authentication tests under
`-race`, producer inventory/guards, `make check`, and `make guard`. Both concrete
execution modes may still be test adapters at this point.

## Execution log

Append dated task evidence and the final producer inventory.
