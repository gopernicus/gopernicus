# Execution-loop handoff — the post-planning-wave milestone queue

Written 2026-07-07 at jrazmi's request. This is the kickoff document for a
fresh session running the execution loop over the ratified plans. It is a
sibling of `loop-handoff.md` (the planning-loop resume pattern); this one
governs IMPLEMENTATION legs.

## Bootstrap (first leg only — do this before any work)

Read, in order:
1. This file, fully.
2. `NOTES.md` — at minimum every 2026-07-06 and 2026-07-07 entry (the
   planning wave, its ratifications, the auth-v2 cut ratification, and the
   plans-housekeeping rule).
3. `ARCHITECTURE.md` and the Makefile (know what `make check` covers).
4. The `00-overview.md` / `plan.md` of whichever milestone is at the front
   of the queue (below).

Do NOT re-read every design doc up front — each phase file cites the
design sections it operationalizes; read those per leg.

## The queue (ratified order — NOTES.md 2026-07-07 entries are the record)

| # | Milestone | Plan | Status at handoff | Gate |
|---|---|---|---|---|
| 1 | repo-hardening phases 1–4 | `.claude/plans/repo-hardening/plan.md` | RATIFIED | none — front of queue |
| 2 | events-v1 | `.claude/plans/events-v1/plan.md` | **DRAFT — awaiting jrazmi ratification** | ratification (jrazmi) |
| 3 | telemetry-closeout | `.claude/plans/telemetry-closeout/plan.md` | RATIFIED | repo-hardening 1–3 landed |
| 4 | auth-v2 (A1–A7b, A9, A10) | `.claude/plans/auth-v2/00-overview.md` | CUT-RATIFIED | repo-hardening 1–3 landed |
| 5 | repo-hardening phase 5 (first tags) | same plan, tasks 8–12 | RATIFIED, double-gated | events-v1 CLOSED **and** LICENSE exists |
| — | authorization-v1 (Z1–Z5) | not cut | design ratified, no phase files | STOP and flag — cutting it is a planning leg with its own review gate, not loop work |

Queue rules:
- **repo-hardening phases 1–3 run first, to completion** — everything
  enters git before any milestone code lands. Phase 4 (D8 verification
  pass, read-only) follows as soon as its dependency (task-5) is done.
- **events-v1 slot**: check its status header each time the queue would
  reach it. Still DRAFT → emit a flagged **YOUR CALL (jrazmi): ratify
  events-v1** in the leg report, skip it, continue to the next unblocked
  milestone. If jrazmi ratifies mid-loop, events-v1 takes the next free
  slot — finish the in-flight milestone first, never abandon one mid-way.
- **telemetry-closeout before auth-v2** (small before large; both are
  unblocked once repo-hardening 1–3 land; if jrazmi says otherwise in the
  session, their word wins).
- **repo-hardening phase 5**: re-check its two gates at the end of every
  milestone. LICENSE is jrazmi's deferred call (RH6) — flag it whenever
  phase 5 is skipped for it. Never soften the gate.
- When every queue item is done or gated, report the board and stop the
  loop (`stop: true` if running /loop dynamic mode).

## Per-leg protocol (one phase/task per leg — the jobs-v1/auth-v1 loop discipline)

1. Read the milestone overview + THE PHASE FILE fully + the design-doc
   sections it cites. Do not re-decide anything a ratified doc decides;
   conflict with a ratified decision → STOP and flag, never work around.
2. Verify preconditions (including external ones: env files present,
   ports free, `gh auth status` before any GitHub-touching task).
3. Execute via the **implementer** agent (the project-defined agent; its
   frontmatter sets the model — never override, never sonnet). One
   implementer at a time; no parallel implementers (the fast-follows
   parallel pattern required special build-isolation rules that are NOT in
   effect here). Docs/judgment phases (A10, task-6/task-7-style work,
   plan-file edits) run on `model: fable` per the standing executor
   policy.
4. Work items in order → acceptance criteria → **real-interaction check**.
   Green tests NEVER close a leg by themselves. Standing check every leg:
   `make check` green (then-current MODULES set + all guards), boot
   `examples/minimal` (:8081), `GET /` and `GET /products/widget-3000` →
   200s, kill, port free. Per-plan protocols (auth-v2 A9 six-leg protocol,
   telemetry task-2 span drive, repo-hardening's CI-green-on-remote and
   go-get probes) come from the phase files verbatim.
5. Dated execution-log entry in the phase file (what ran, exact commands,
   observed output, divergences). Premise-false → do the closest correct
   thing and log the divergence.
6. **Git discipline** (from the moment repo-hardening phase 2 lands): each
   completed leg commits with a descriptive message and pushes. Once
   phase 3's CI gate exists, a leg is not complete until the pushed
   commit's required check run is green on the remote.
7. Milestone close: closing NOTES.md entry (dated, honest: what passed,
   what was live-verified vs hermetic, what's flagged), then move the plan
   dir to `.claude/past/` in the same session (standing rule, NOTES
   2026-07-07) and add it to `.claude/past/README.md`'s table.

## Live-store gates (auth-v2 A7a/A7b, telemetry task-2, repo-hardening task-6)

- Turso legs run ONLY against the authorized playground DB
  (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`)
  — verify the env URL matches before ANY run. Single executor against the
  playground at a time. Creds via local `.env`, never into CI logs (live
  CI legs are manual-dispatch only, per RH4).
- pgx legs env-gated on `POSTGRES_TEST_DSN` (docker, 55432→5432).
- Loud skips are fine mid-milestone; milestone close requires recorded
  live conformance runs as dated NOTES.md artifacts — never hermetic
  green.

## Stop-and-flag conditions (end the leg, report, wait for jrazmi)

- events-v1 ratification (queue slot 2), the LICENSE call (phase-5 gate),
  authorization-v1's cut, or any decision a plan marks YOUR CALL.
- Anything that would contradict a ratified decision or the constitution.
- A leg failing verification twice — stop and report; do not thrash.
- Missing external preconditions the loop cannot create (gh org access
  for creating `github.com/gopernicus/gopernicus`, absent creds where a
  phase requires a live leg to CLOSE a milestone).
- Note: pushing the repo public in repo-hardening phase 2 is ALREADY
  AUTHORIZED (RH1 ratified public, world-readable `.claude/` consciously
  confirmed — NOTES 2026-07-07). Do not stall on it; do state it loudly in
  that leg's report.

## Leg report format (every leg, small ones included)

Status board (queue position, one line per milestone) → what this leg did
and how it was verified (exact commands + observed results) → flagged
**YOUR CALL** items → next leg. Honesty rules apply: state what is
unverified or skipped; never claim success from green tests alone.

## Compact-survival note

If context compacts mid-loop, preserve: this file's path, the current
milestone + phase file path, the modified-file list, `make check` as the
verify command, unresolved failures, and the loop protocol above. Resume
by re-reading this file and the current phase file.
