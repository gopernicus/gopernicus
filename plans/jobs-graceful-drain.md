# jobs: graceful drain for the unfenced runtime — the terminal write survives shutdown

Status: BUILT + TAGGED 2026-08-31 — commit `3107dce`, tag `pockets/jobs/v0.4.0`
(originating host gps-360-go, plans/40 Batch 0; found in that plan's pre-build
review). Store modules NOT retagged — no repository contract changed.

## Context

The README sells the drain contract: "Cancel the ctx to drain gracefully —
in-flight handlers finish and persist." The implementation did not deliver it
for the unfenced runtime: `Runtime.Run` passed the canceled pool context
through an already-claimed handler and then into `Complete`/`Fail`. A
context-aware handler stopped mid-work, and — worse — the terminal write
itself failed on the dead context, leaving the row `running` until lease
recovery re-ran it elsewhere. The documented contract was stronger than the
code; the host adopting the runtime (gps-360-go) made the fix a blocking
prerequisite of its adoption.

The fenced runtime is DELIBERATELY not in scope: its
cancellation-to-lease-recovery contract is different and stays different
(cancellation there intentionally abandons processing and leaves the lease
reclaimable — the checkpointed payload makes the re-run byte-identical).

## The fix

In `pockets/jobs/internal/logic/runtime`, `drainInFlight` wraps the unfenced
**queue** and **scheduler** pool work funcs in `context.WithoutCancel`: context
VALUES (including the worker id) survive, cancellation does not.
`sdk/foundation/workers.Pool` still checks cancellation before each iteration,
so shutdown still stops NEW claims; an iteration already begun finishes its
handler and persists its terminal `Complete`/`Fail` on a live context.
`Run`'s doc comment corrected to match.

Host-side consequence (recorded in the upgrade note): a detached iteration no
longer dies with the process context, so a host MUST bound handler work itself
— wrap registered handlers in a timeout strictly below the queue lease
(gps-360-go: 10m default under the 15m lease) so drain can never silently
outlive the lease.

## The proof

`runtime_test.go` gains the cancellation-during-handler regression at the
jobs-runtime seam: start a handler, cancel `Run`, prove it has not returned,
release the handler, require `Complete` to receive a live context and be
durably recorded, then require `Run` to return cleanly. The fake queue's
`Complete`/`Fail` refuse a canceled context, so the old behavior cannot pass.
Verified failing with the fix reverted ("Run returned before the in-flight
handler drained", the log showing `failed to mark job failed … context
canceled`), then green under `-race -count=3`. Fenced cancellation tests
untouched and green; full `pockets/jobs` and `sdk` suites green.

## Release record

- Commit `3107dce` (two files: `runtime.go`, `runtime_test.go`), tag
  `pockets/jobs/v0.4.0` (annotated), 2026-08-31.
- MINOR: behavioral fix toward the documented contract; no API change, no
  schema, no store retags, no pin moves.
- First adopter: gps-360-go pins v0.4.0 in its plans/40 Batch 1 and proves the
  drain host-side (its `cmd/worker` test cancels mid-handler on the real pgx
  store and asserts the completion persisted before `Run` returned).
