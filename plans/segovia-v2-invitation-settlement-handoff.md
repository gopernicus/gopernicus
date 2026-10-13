# Segovia v2 — adopt invitation terminal settlement (unblocks task 8)

Paste the prompt below into a session working in
`/Users/jrazmi/code/segovia/segovia/v2`. Prepared 2026-10-13 after the gopernicus
release; the upstream contract that `v2-personal-invitation-recovery-followup.md`
marked NOT IMPLEMENTED is now published.

---

Gopernicus has published the invitation terminal-settlement contract your plan
`.claude/plans/v2-personal-invitation-recovery-followup.md` was blocked on. Update
that plan's status from NOT IMPLEMENTED UPSTREAM to AVAILABLE, then execute task 8
against these published modules (all cold-verified on proxy.golang.org):

| Module | Version | Commit |
|---|---|---|
| `github.com/gopernicus/gopernicus/pockets/authorization` | `v0.24.0` | `b7c8a73a` |
| `github.com/gopernicus/gopernicus/pockets/authentication` | `v0.18.0` | `b7c8a73a` |
| `github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx` | `v0.18.0` | `8c9b56e7` |
| `github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx` | `v0.8.0` | `8c9b56e7` |

Poll each proxy `.info` before `go mod tidy`. `authorization/stores/goredis v0.4.0`
is not retagged and builds against core v0.24.0. Read first, in gopernicus:
`plans/invitation-terminal-settlement.md` (Design D1–D8, "Segovia handoff", "Release
record"), `pockets/authorization/stores/UPGRADE.md` (the 0004 entry), the
authentication README block "Terminal settlement", and the reference adoption in
`examples/auth-cms/cmd/server/membership.go` (PR #73).

What the contract gives you:

1. **Durable fence.** `mutations.Service.Fence(ctx, operationID)` writes a fenced row
   in the operation ledger under the same lock `Apply` uses; a later grant with that
   ID replays as `Outcome == OutcomeFenced` (`Replayed && Superseded`) and never
   writes. `Recorded(ctx, operationID)` reports a committed or fenced record.
2. **Authentication seam.** `invitations.Fencer` (wire via `InvitationsConfig.Fencer`);
   `Cancel` on an `accepting` claim now fences and cancels, or completes acceptance
   and returns `invitations.ErrAlreadyGranted` when the grant already committed.
   `invitations.ErrGrantFenced` from your Granter terminates the claim as cancelled.
3. **Bounded listing.** `ListActiveByResource(ctx, invitations.ActiveListInput{
   ResourceType, ResourceID, Relation}, req)` — pending/accepting only, server-side,
   partial-index proven. No 5000-row history cap applies.

Migration: copy `pockets/authorization/stores/pgx/migrations/0004_iam_operations_fenced.sql`
verbatim as host IAM ledger entry **0023** (never edit 0022). It is a one-way door
once a fenced row exists. Deploy per UPGRADE.md: run 0023 in its **own** migrate run
with `options=-c lock_timeout=5s` on the migration DSN (the ALTER takes ACCESS
EXCLUSIVE; a queued ALTER stalls every old-binary `Apply`), freeze scale-out →
migrate → roll new binaries immediately → never start an old revision afterwards.
Use the **two-phase rollout**: phase 1 = repin + 0023 with NO Fencer wired (still
reversible via the self-guarding reverse SQL in UPGRADE.md); phase 2 = wire the
Fencer. Wherever a Fencer is wired, add the boot self-check
`Mutations.Recorded(ctx, "boot-probe")` expecting `(false, nil)`.

Granter (`v2/pockets/auth/inbound/granter.go`): keep `DetachTransaction` and
kind/relation validation; then call `Recorded(ctx, operationID(in.OperationID))`
**before** `TenantOf` — when recorded, skip the resource probe and go straight to
`Standing` + `Apply` (a replay never writes). Map `result.Replayed &&
result.Outcome == mutations.OutcomeFenced → invitations.ErrGrantFenced` first; keep
`Replayed && Superseded → ErrGrantSuperseded` and `ErrOperationMismatch →
ErrGrantSuperseded`. Put `"invitation:" + id` in one helper the Fencer shares.
Fencer: `DetachTransaction`, then `Fence(ctx, operationID(in.OperationID))`;
`OutcomeFenced → FenceOutcomeFenced`, else `FenceOutcomeCommitted`. Add a
fence-then-grant → `ErrGrantFenced` test. Fence refuses an ambient transaction.

Removal flow: collect with `ListActiveByResource` to exhaustion; per row
`PrepareManagement` → `Cancel`. nil → done (the fence holds). `ErrAlreadyGranted` →
the row is `accepted`; run the ordinary exact revoke for that user and continue.
Retire the 409s for "accepting row present" and "collection overflow"; keep 409
only for a `Fence` infrastructure error or `ErrNotPending` on a genuinely foreign
state. Note the pocket's bundled cancel route returns a generic 409 on
`ErrAlreadyGranted`; your own removal route must handle it and revoke.

Tests: turn `TestLivePersonalRemovalReportsClaimConflictBeforeLaterFirstGrant` into
the desired-contract test (pause before first commit, remove → 200, resume →
`ErrGrantFenced`, no standing); add deleted-resource settlement (recorded op
settles without a new grant; restoring the resource never revives a cancelled
invitation), the >5000-row listing proof, restart-at-each-boundary, and the
two-process contention proof on your real PostgreSQL. Only then consider removing
the personal-invitation gate. Hosted Turso is irrelevant for Segovia (pgx).

Report with a status board, what was verified against the real host, and
anything in the upstream contract that did not fit — those go back to gopernicus
as flags, not local workarounds (no Segovia-side tombstone, mutex or raw SQL).
