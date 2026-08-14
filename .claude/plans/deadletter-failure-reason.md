# DeadLetterFunc failure-reason threading

**Status: EXECUTED 2026-08-14** (ratified in-session same day). Fixes Coordination-Hub issue #5:
`jobs.DeadLetterFunc` receives a job with an empty `FailureReason` because the
kernel fires the hook with the job value as claimed, while the reason goes only
to `store.Fail`.

## Decision (Option A, ratified)

Thread the reason through the kernel seam explicitly; keep the host-facing
frozen `jobs.DeadLetterFunc` signature (AV3D-0.3) intact and have the
features/jobs dispatch closure stamp `FailureReason` on the job value.

Scope ruling: stamp **only** `FailureReason` — not `JobStatus`/`FailedAt` —
surgical, matches the issue's ask.

## Changes

1. `sdk/foundation/workers/fenced.go`
   - `FencedDeadLetterFunc[T Job]` becomes
     `func(ctx context.Context, job T, reason string) error`; doc notes the
     reason is exactly what `Fail` durably recorded.
   - `deadLetterFail` passes `reason` to `fireDeadLetter`; `fireDeadLetter`
     passes it to the hook.
2. `sdk/foundation/workers/fenced_test.go` — update the four hook closures;
   assert the received reason matches the process error.
3. `features/jobs/fenced.go`
   - Remove the AV3D-1.4 compile seam (`var _ = func(f DeadLetterFunc) ...`);
     the shapes now intentionally differ, the dispatch closure is the adapter.
   - Update `DeadLetterFunc` docs: signature unchanged, `j.FailureReason` is
     now populated with the recorded reason.
   - Dispatch closure sets `j.FailureReason = reason` before per-kind dispatch.
4. `features/jobs/fenced_test.go` — extend the dead-letter tests to assert the
   hook sees the populated `FailureReason`.
5. `features/jobs/README.md` — one-line note that the hook's job carries the
   recorded failure reason.

Kernel-hook consumers repo-wide: features/jobs closure + kernel tests only, so
the signature break is contained. Pre-1.0; acceptable now, expensive later.

## Verify

`go build ./... && go test ./... && go vet ./...` in `sdk` and `features/jobs`
modules (plus the repo's standard guard sweep if touched modules require it).

## Closeout

Comment on gpsimpact/Coordination-Hub#5 confirming upstream oversight + fix, so
the host can drop its `Runner.lastFailure` workaround.
