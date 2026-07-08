# Overnight implementation loop — 2026-07-07 kickoff (jrazmi-authorized)

Written 2026-07-07 at jrazmi's request. Sibling of
`execution-loop-handoff.md`: that document's **per-leg protocol, git
discipline, model policy, and verification norms are all inherited
verbatim** — this file is only tonight's queue, the authorizations jrazmi
grants by launching it, and the stop conditions. Where the two conflict,
this file wins (it is newer and jrazmi-launched).

## Bootstrap (first leg only)

Read, in order: (1) this file fully; (2) `execution-loop-handoff.md` —
the per-leg protocol section especially; (3) NOTES.md's 2026-07-07
entries (feature-standard ratification, auth-v2 close, events-v1
amendments A-I1 + A-R1); (4) the front-of-queue plan file. Do not
front-load design docs — each leg reads what its task cites.

## Authorizations — ALL CONFIRMED IN-SESSION by jrazmi, 2026-07-07
("go ahead and commit and push … yep ratified. yep recs good")

1. **Git discipline in full.** The convergence backlog + today's planning
   edits were committed and pushed PRE-LOOP, in the authorizing session
   itself (see NOTES.md entry + the two commit SHAs there). Every
   completed loop leg commits and pushes; a leg is not complete until the
   pushed commit's required CI check is green on the remote (handoff
   rule 6).
2. **events-v1 is RATIFIED** — the plan's status header already says so
   (edited in the authorizing session; open questions 1–4 at defaults:
   README wiring page, pgx JSON payload, G5 stands, P5 confirmed). The
   only pre-execution step left on the plan file is the FS2 fold-in
   (pre-flight leg below).
3. **task-D5's embedded "RATIFY at execution"** (connectors gain a
   `sdk/crud` import — no new external dependency, normal downward
   direction) is granted; record it in the execution log and morning
   report.
4. **Cutting authorization-v1 is authorized as tonight's final planning
   leg** — a jrazmi override of the handoff's stop-and-flag row for this
   one item. DRAFT only; zero implementation; morning ratification gate.

## The queue

**Leg 0 — sanity gate (fast; the backlog commit already happened
pre-loop).** Confirm: working tree clean; `git log` HEAD matches
origin/main; full `make check` green on HEAD; the pre-loop pushes' CI
runs green on the remote (`gh run list`). Any failure: STOP the loop and
report — do not start milestone work on a red base.

**Milestone 1 — feature-standard remainder**
(`.claude/plans/feature-standard/01-convergence.md`; RATIFIED).

- **task-B2** — extract `features/cms/views/templ`, Views port in core
  (FS3). The plan instructs the executor to cut a sub-plan when reached:
  cut it under `.claude/plans/feature-standard/`, log it, execute it —
  the decision is ratified; the sub-plan is execution mechanics, not a
  new ratification gate. Includes the four tooling moves (templ tool
  directive, Makefile generate target, go.work + MODULES, repo-hardening
  task-5 sync note) and removing the FS1 guard's cms carve-out (dated
  TODO). Leg real-interaction check: boot `examples/cms`,
  `examples/minimal`, AND `examples/auth-cms`; public page + an admin
  page render correctly on each applicable host (views just moved —
  render proof, not build proof).
- **tasks D2 → D3 → D4 → D5 → D6**, in order, each gated on per-feature
  storetest green (the plan's own gate). D6 is optional — execute if the
  night has room, otherwise flag as skipped-by-priority. **task-B3 stays
  deferred (demand-driven) — do NOT do it.**

**Milestone 2 — events-v1** (`.claude/plans/events-v1/plan.md`; RATIFIED
per authorization 2, with A-I1 E1–E8 already folded and A-R1 as task-0).

- **Pre-flight leg (fable, plan edit):** the FS2 fold-in the task-11 sync
  note + feature-standard W4 mandate — respell every
  `Register(mount, Repositories{}, Config{…})` site in the plan to
  `svc, err := NewService(repos, cfg)` + `svc.Register(mount)` (the
  status header is already RATIFIED — authorization 2). Commit the plan
  edit.
- **Phases 0→6 as written.** Phase 0 (task-0 rename) is safe now — the
  convergence coordination flag is satisfied by leg 0's commit. Then
  tasks 1/1b/1c/2; phase 2 (3–6); phase 3 (7–8); phase 4 (9–10); phase 5
  (11–12 + the mandatory real-interaction SSE protocol, recorded
  verbatim); phase 6 (13–15).
- **Live-store legs (phase 4):** turso via the playground env in `/.env`
  (verify the URL is the authorized playground DB before running — plan
  norm); pgx via `docker run … postgres:17`. If either is unavailable
  tonight: loud skip, record exactly what was attempted, and mark the
  milestone **CLOSE-BLOCKED** — finish all other phases, but do NOT
  write a closing NOTES entry or relocate the plan. Never substitute
  hermetic green for the required live artifacts.
- On a genuine close: closing NOTES.md entry, relocate the plan dir to
  `.claude/past/events-v1/` (standing housekeeping rule), update
  `.claude/past/README.md`.

**Gate re-check — repo-hardening phase 5 (first tags).** After events-v1
closes (or blocks), re-check its two gates. LICENSE still does not exist
→ phase 5 stays blocked; flag it in the morning report. **Never cut a
tag tonight under any circumstances.**

**Final leg — cut authorization-v1 (planning only; authorization 4).**
From `roadmap/auth-v2-feature-design.md`'s ratified three-posture ruling
and Z1–Z5: cut `.claude/plans/authorization-v1/` following the sibling
milestone-dir convention, status **DRAFT — awaiting jrazmi
ratification**. Run architecture-steward + lead-backend-engineer review
passes on the draft and fold findings (still DRAFT). Naming rule applies
everywhere: authorization/authorizer, never abbreviated. Zero
implementation.

**Then stop the loop** (`stop: true` in dynamic mode) with the morning
report as the final message.

## Failure / blocked protocol (adds to the handoff's)

- Two failed attempts on one leg → park it, record state exactly, and
  continue ONLY with work that does not depend on it. events-v1's phases
  are chained: a parked phase parks the milestone — jump to the final
  planning leg rather than forcing anything green.
- A conflict with any ratified decision → STOP and flag (handoff rule 1).
  Never soften a gate, never fake an artifact, never write a closing
  entry for something not actually closed.
- Fences, absolute: no tags; no LICENSE creation; no repo-settings
  changes; no force-push; no editing generated files (`*_templ.go`); no
  touching `/.env` contents beyond reading env vars; NOTES.md is
  append-only.

## Morning report (the loop's final message)

1. Status board: one line per leg — done / parked / blocked, with
   live-verified vs hermetic-only stated honestly per milestone.
2. Commit list (SHAs + messages) and CI status per push.
3. **YOUR CALLS**: authorization-v1 ratification; LICENSE (still blocking
   tags); anything parked/skipped (B3 stands deferred by design; D6 if
   skipped); any divergence logged mid-loop.
4. Check-manually steps for the two riskiest landings (B2 view rendering;
   events-v1 SSE protocol) with exact commands.
