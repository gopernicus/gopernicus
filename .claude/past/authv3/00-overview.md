# Auth v3 implementation packet

Status: **CUT — owner-directed scope, executable after preflight.**
Source of truth: `.claude/plans/roadmap/auth-v3-identity-design.md`.
Executor: `.claude/agents/implementer.md`, one task at a time, no parallel
implementers.

## Outcome

Ship auth v3 with:

- `user_identifiers` for multiple email/phone identifiers and explicit login,
  recovery, notification, and primary uses;
- repository-atomic challenge replacement/redemption, HMAC-protected short
  codes, and key rotation;
- operation-bound recent-authentication grants and revision-serialized
  credential mutations;
- durable enumeration-safe delivery jobs;
- identifier management, hardened recovery, passwordless login, and a masked
  method inventory;
- fail-closed production wiring and HTTP protections;
- unchanged JSON API routes plus optional normal HTML handlers rendered through
  an overridable default templ view module; and
- stable assurance/method seams for the auth-v4 MFA add-on without implementing
  MFA in v3.

## Required reading before every task

1. This overview.
2. The assigned phase file and task.
3. The design sections cited by that task.
4. `.claude/agents/implementer.md` and `ARCHITECTURE.md`.
5. `features/README.md`, the root `Makefile`, and every touched module's
   `go.mod`.
6. Two or three sibling files in each touched layer.

The design decides product and architecture questions. An implementer must not
invent a different repository shape, weaken an atomicity requirement, collapse
typed credential tables, add MFA early, or replace the durable delivery job
with a synchronous send.

## Preflight gate

Before AV3-0.1:

1. Confirm auth-v2 and JWT refresh work in the current tree is complete enough
   that `make check` is green. If not, stop: auth v3 must not absorb unrelated
   baseline failures.
2. Run `git status --short`; preserve every pre-existing change. Never reset or
   overwrite another workstream.
3. Confirm both auth migration trees have identical filenames.
4. Run the current auth store conformance suites. Live DSN legs may skip, but
   record the skip.
5. Read the full design once and record any contradiction between it and the
   current tree. A premise that is merely stale may be adapted and logged; a
   product/architecture conflict stops execution.

## Iteration protocol

- Assign exactly one `AV3-x.y` task to the implementer per iteration.
- **No reviewer, consultation, fresh-agent, or specialist-review agents run
  during AV3-0.1 through AV3-9.6.** Execute the cut plan sequentially and use
  its tests/acceptance criteria as the in-flight gate. Do not pause after phases
  for reviewer ratification.
- The implementer checks that all listed dependencies are complete.
- Tests named “write first” are committed in the same task as the contract they
  specify; adapters do not precede conformance tests.
- Run the task verification, then the phase gate when the final task in a phase
  closes.
- Add a dated `Execution log` entry to that phase file: task IDs, files changed,
  exact commands, observed pass/skip/failure, and any premise adaptation.
- After verification and logging, mark only that task complete in `TASKS.md`.
- Do not mark a task complete from hermetic green if the task names a live or
  run-and-look check.
- After two failed repair iterations on the same root cause, stop and report the
  blocker instead of broadening scope.

The owner-directed delivery-runtime follow-up in
`.claude/plans/authv3-delivery-refactor/` is inserted after AV3-9.6 and before
the sole reviewer-agent wave. AV3-9.7 therefore reviews the original auth-v3
implementation plus the completed delivery refactor as one final system. AV3-9.8
then applies accepted findings and re-runs final gates before any PR is opened.

## Phase queue

| Phase | File | Depends on | Gate produced |
|---|---|---|---|
| 0 | `01-security-foundations.md` | preflight | atomic/security contracts frozen in code |
| 1 | `02-identifiers.md` | phase 0 | identifier domain + memory/reference contract |
| 2 | `03-schema-and-stores.md` | phases 0–1 | both SQL dialects conform + upgrade draft |
| 3 | `04-challenges-and-recovery.md` | phases 0–2 | one atomic secret rail + hardened recovery |
| 4 | `05-delivery.md` | phases 0, 2 | durable worker + unified outbound |
| 5 | `06-service-rekey.md` | phases 1–4 | existing auth/OAuth/invitations use identifiers |
| 6 | `07-credential-suite.md` | phases 3–5 | step-up protected credential/identifier lifecycle |
| 7 | `08-passwordless.md` | phases 3–5 | passwordless login on atomic challenges/outbox |
| 8 | `09-proof-host.md` | phases 6–7 | JSON + HTML adapters, default templ, overrides, observable proof host |
| 9 | `10-docs-and-closeout.md` | phases 0–8 | upgrade/docs/live evidence/milestone close |

Phases 3 and 4 may be developed in either order after phase 2, but still use one
implementer at a time. Phases 6 and 7 may be developed in either order only
after phase 5. Every other dependency is strict.

## Standing invariants

- `sdk` remains stdlib-only.
- The authentication feature core imports no integrations, examples, or store
  modules.
- pgx, turso, `storetest` reference memory, and `examples/auth-cms/authmem`
  implement the same public contracts.
- Both dialect migration trees have byte-for-byte identical filename sets.
- Passwords, OAuth accounts, and future MFA authenticators remain typed stores.
- Short codes use HMAC-SHA-256 with a host key ring; 256-bit tokens use SHA-256.
- Challenge success is single-use under concurrency because the repository
  consumes atomically.
- Sensitive method/identifier mutations require a live session, a consumed
  recent-auth grant, policy approval, and `auth_revision` serialization.
- Unauthenticated start endpoints never synchronously resolve an account or
  call a provider.
- Secrets never enter audit details, ordinary logs, limiter keys, or plaintext
  delivery-job columns.
- Production mode rejects development transports and incomplete security
  wiring.
- `Config.Views == nil` is API-only; non-nil Views adds HTML without changing
  JSON contracts. Concrete templ code lives only in the sibling view module.
- V3 exposes method/assurance seams but does not implement TOTP, passkeys, or
  recovery-code MFA.

## Standing verification

For each code task, run the narrowest relevant module gate plus:

```sh
make guard
```

Every phase closes with:

```sh
make check
make guard
```

Phases 2, 3, 4, and 9 also require the live-store or worker checks named in
their files. `make check` skipping live stores is never the final milestone
proof.

## Global stop conditions

Stop and ask for a decision if implementation would require:

- a third v3 identifier kind;
- a uniform credential/accounts table;
- an external secret/HSM service as a hard dependency;
- synchronous unauthenticated delivery;
- weakening CSRF/origin or production validation defaults;
- accepting a concurrent double-redemption or stale-method mutation race;
- adding MFA behavior before auth-v4; or
- destructive changes to an existing host without the phase-2 upgrade runbook.

## Milestone completion

Auth v3 is complete only when every task is logged, `make check` and guards are
green, both SQL dialects have recorded live conformance runs on fresh/reset
databases, the proof-host transcripts exist, the upgrade runbook is validated,
the production-negative wiring tests pass, the post-implementation reviewer
wave is closed, and review remediation has been reverified. No auth-v3 PR should
be opened before AV3-9.8.

## Execution log

Append only. Phase files own task-level entries; this overview receives the
final milestone-close entry.

### 2026-07-14 — MILESTONE CLOSE

Auth v3 is COMPLETE per the completion criteria above. AV3-0.1 through AV3-9.8
are all checked off with dated execution-log entries in their phase files.
Final state: one post-implementation reviewer wave (AV3-9.7, eight specialist
reviews over the auth-v3 + AV3D + SWP cut, 41 findings) dispositioned via the
canonical IX-01..IX-23 integrated table (owner-adopted 2026-07-13); all seven
owner gates resolved; remediation executed in five bounded batches (files,
regressions, and adaptations logged per batch in 10-docs-and-closeout.md);
reverification green with zero skips — hermetic 36/36 modules + 15/15 guards,
fresh dual-store conformance 10/10 legs, the lease/generation fence's
first-ever live pgx/turso concurrency run 41/41 subtests `-race` both dialects,
the eight-proof livedelivery harness green ×4 runs, and the IX-11 reset-retry
browser drive 9/9. Two verification-discovered defects were root-caused as
test/harness bugs (no product defect; no assertion weakened). The PR-ready
completion record and final disposition table live in 10-docs-and-closeout.md.
No PR, tag, push, or commit was made — committing the tree, opening the PR,
tagging per the RELEASING.md floors, LICENSE, and turso CI secrets remain the
owner's release workflow.
