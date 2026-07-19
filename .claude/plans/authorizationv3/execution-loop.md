# Authorization v3 execution loop — handoff protocol

Owner ratification: 2026-07-14, jrazmi — the packet as pushed, at all ten
recommended defaults plus rulings R1–R4 (nested userset membership IN / rewrite
operators OUT; separate proof phase with the reopen rule; AZADM
blocked-indefinitely; `iam_*` table prefix). Launching this loop is the
execution authorization. If any default is to change, amend the packet BEFORE
starting; the loop never renegotiates a ratified default.

## Mission

Execute the authorizationv3 packet task-by-task, in TASKS.md order, from
**AZ3-0.1 through AZ3-5.5** (the implementation-complete hermetic/live gate).
**Stop after AZ3-5.5.** AZ3-5.6 (the one post-implementation reviewer wave) and
AZ3-5.7 (owner-gated remediation + PR handoff) run in owner-controlled
sessions — the auth-v3 precedent. Never run reviewer or consultation agents
before AZ3-5.6, and never open a PR, commit, tag, or push (the owner does all
of those; report when a commit point would be sensible, don't make it).

Deferred tracks are out of scope entirely: no `AZFX-*` or `AZADM-*` task may be
started, partially implemented, or "prepared for."

## Preflight (leg 0, once)

1. Read `.claude/plans/authorizationv3/00-overview.md` end to end, then
   `ARCHITECTURE.md`. Skim the six phase-bearing files and TASKS.md.
2. Flip the packet status line in `00-overview.md` from DRAFT to
   **RATIFIED — execution in progress (2026-07-14)** and note the ten defaults
   + R1–R4 as the ratified basis. This is the loop's only status edit.
3. Run the packet's preflight gate: `git status` (clean tree expected at
   start), `git tag` (must be empty — the pre-tag breaking policy depends on
   it; a `features/authorization` tag appearing flips the packet to
   append-only, which is a STOP), `make check` and `make guard` green
   (15 guards), containers `authv3-pg` and `authv3-libsql` up.
4. Confirm the current-state audit's anchor facts still hold (e.g. both store
   `relationships.go` files still hard-code `relation = 'member'`) — if the
   module changed since ratification, stop and report before building on stale
   findings.

## Per-task loop leg

1. Read the owning phase file's full task section AND every prior
   execution-log entry in that file (adaptations accumulate — later tasks
   depend on them).
2. Dispatch **one implementer agent** per task (use the project `implementer`
   agent as-is; its frontmatter sets the model — never override). The brief
   must be self-contained: task ID + full task text, the ratified
   defaults/rulings it touches, relevant prior-leg adaptations, repo hard
   rules (below), and the task's exact verify commands. For pure
   verification/gate tasks (AZ3-5.5 especially) use the `verifier` agent
   instead.
3. **Independently verify** every report: re-run the task's key verify
   commands yourself before logging PASS. Never trust green claims alone, and
   never trust LSP diagnostics during or right after subagent edits — they
   show stale mid-edit snapshots; `go build ./... && go vet ./...` is the
   truth.
4. Append the dated execution-log entry to the owning phase file (outcome,
   files changed, test names, premise adaptations — follow the auth-v3 entry
   shape) and check the task off in TASKS.md. Every task gets an entry, gate
   tasks included.
5. End the leg with a real-interaction check wherever the task has a runtime
   surface (run the actual test/binary/query — not just compile). End every
   leg with the standard closing block: status board, numbered next steps
   (owner decisions marked YOUR CALL), check manually, run next.
6. Phase boundaries: verify the phase-acceptance list in the phase file before
   starting the next phase.

## Hard rules

- **Stop conditions are binding.** The packet's stop conditions (overview +
  per-phase) stop the loop with a report; never work around one, never
  document acceptance of one.
- **Proof-phase gate (phase 4):** a defect surfaced by AZ3-4.1/4.2 reopens the
  owning phase 0–3 task — fix there, re-log there, re-verify forward. Never
  patch-in-place during the proof phase, and phase 5 does not start until the
  phase-4 gate passes.
- Hexagonal boundaries per ARCHITECTURE.md; accept interfaces, return structs;
  authentication/authorization naming discipline (never authz/authn); no
  feature imports another feature; `make guard` after every boundary-touching
  task.
- Surgical diffs; generated files only via `make generate`; per-module
  `go build ./... && go vet ./... && go test -race -count=1 ./...` for touched
  modules; goimports formatting.
- Memory/pgx/turso must behave identically: reference (memory) conformance
  lands with the contract, both dialect adapters prove against the same
  storetest suite.

## Live-gate recipe (auth-v3 Batch 5 lessons — apply to every live leg)

- pgx: C-collation test DBs are MANDATORY
  (`TEMPLATE template0 LC_COLLATE 'C' LC_CTYPE 'C'`); container `authv3-pg`,
  superuser `postgres://postgres:postgres@localhost:5432`.
- turso/libSQL: container `authv3-libsql`, `http://127.0.0.1:8080`, token
  `local-dev`; integration build tag.
- Fresh/reset databases per conformance leg, and **always `-count=1` after a
  DB reset** — the Go test cache silently replays otherwise.
- Concurrency-harness margins: drive queues/jobs to durable terminal state
  before stopping runtimes; keep lease TTLs and poll deadlines far above a
  `-race`-slowed cycle; never weaken a semantic assertion to make a live leg
  pass — widen margins or isolate state instead.
- Leave both containers running; kill every process you start and confirm
  ports released.

## Endpoint

After AZ3-5.5 passes with zero unexplained skips, write the
implementation-complete handoff entry (mirror auth-v3's AV3-9.6 log entry:
consolidated hermetic + live evidence, the reproducible live-gate recipe, what
AZ3-5.6/5.7 need) and stop with a final closing block. Do not start the
reviewer wave.

## Compact preservation

If context compacts, preserve: this file's path, the current task ID and its
in-flight state, the modified-file list, the verify commands in use, any
unresolved failure, and the stop-after-AZ3-5.5 rule.
