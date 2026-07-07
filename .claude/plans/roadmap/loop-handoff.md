# Loop handoff — execute the ratified 2026-07-02 roadmap

Paste the block below as the `/loop` prompt (self-paced; omit an interval).
Written 2026-07-02, immediately after R1–R10 ratification.

---

You are executing the gopernicus roadmap ratified 2026-07-02 (jrazmi):
R1–R10 in `.claude/plans/roadmap/00-intersections.md` §6. One loop leg =
one phase. Do NOT re-decide anything a ratified plan decides.

## Read first, every leg (skim what you already know; read the phase file fully)

1. `.claude/plans/restructure/00-overview.md` — constitution + decision log (law).
2. `.claude/plans/roadmap/00-intersections.md` — taxonomy, seam map, R1–R10.
3. The current milestone's overview + the current phase file + its design doc.

## Milestone order (fixed, ratified R10)

1. **datastore-portability** — design: `.claude/plans/roadmap/datastore-portability.md`
   (RATIFIED). Phase files live at `.claude/plans/datastore-portability/`;
   if that directory doesn't exist yet, THIS milestone's first leg is a
   planning leg: cut `00-overview.md` + one file per phase P1–P4 from the
   design's §10 (carry its acceptance rows, §4.3's gating rules, and the
   standing real-interaction check verbatim; loop protocol copied from
   `.claude/plans/auth-v1/00-overview.md`'s).
2. **auth-v1** — `.claude/plans/auth-v1/` (phase files exist; A2 as amended).
   Execution order: 1, 2, 3, 4, 5, 7, 6 — phase 6 (docs) runs LAST despite
   file numbering. Phase 7 depends on portability P1; the design doc is
   `.claude/plans/restructure/auth-feature-design.md`.
3. **jobs-v1** — design: `.claude/plans/roadmap/jobs-feature-design.md`
   (RATIFIED, as amended: pgx phase struck per R2; memory store is the
   in-core `features/jobs/memstore` package per R3). First leg: cut
   `.claude/plans/jobs-v1/` phase files from its §10 (phases 1–5, 7, 8 —
   6 is struck).
4. **events-v1** — design: `.claude/plans/roadmap/events-feature-design.md`
   (RATIFIED; suite is `features/events/storetest` per R4). First leg: cut
   `.claude/plans/events-v1/` phase files from its §11. Preconditions per
   that section: auth-v1 executed, `sdk/workers` landed.

Telemetry (sdk/tracing) is AFTER all of the above and NOT part of this loop.

## Finding your place

The current leg is the first phase file (in the milestone execution order
above) whose `## Execution log` has no dated green entry. If a milestone's
phase directory doesn't exist, the planning leg above is current. When all
four milestones' logs are green, the loop is DONE — write a final summary
into NOTES.md and stop scheduling.

## Leg protocol (per phase — the restructure/auth-v1 protocol, unchanged)

1. Verify preconditions/dependencies (including cross-milestone ones, e.g.
   auth phase 7 needs portability P1 merged). If unmet, do the nearest
   ready phase instead; if nothing is ready, stop and flag.
2. Execute work items IN ORDER. Executor model per the phase's
   "Executor model" line — dispatch implementation phases to the
   `implementer` agent with `model: opus`, design/doc-judgment and
   phase-cutting legs run on `model: fable` (planner/fable). NEVER sonnet
   for executors (standing jrazmi policy).
3. Surgical diffs; goimports-formatted; match repo conventions; never edit
   generated files directly (fix the source and regenerate — templ views:
   `cd features/<name> && go tool templ generate`).
4. If a work item's premise is false, do the closest correct thing and log
   the divergence. If it would violate the constitution or a ratified
   decision, STOP the loop and flag for jrazmi.
5. Acceptance: run the phase's exact acceptance commands. `make check` must
   be green across all modules + guards.
6. **Real-interaction check — mandatory, every leg; green tests NEVER
   close a leg alone.** Standing check (a): boot `examples/minimal`
   (:8081), `GET /` and `GET /products/widget-3000` → 200s, kill, port
   free. Plus the phase's own check: auth flow (b) once `examples/auth-cms`
   exists (five-step cookie-jar curl per `auth-v1/00-overview.md`); jobs
   proof-host steps per jobs design §8; events `curl -N /events` per its
   phase 7. Record exact commands + status codes.
7. Live-store gates: postgres conformance legs need `POSTGRES_TEST_DSN`
   (docker one-liner in the store README); turso legs need `TURSO_*` env.
   Check the environment first — do not ask. If infra is absent: loud
   skips are acceptable mid-milestone (state plainly what is unverified),
   but a milestone whose close-gate requires a recorded live run
   (portability §4.3, auth acceptance, jobs/events store phases) must NOT
   be declared done — stop and ask jrazmi for the env instead of closing
   on a hermetic green.
8. Append a dated entry to the phase file's `## Execution log`: what
   shipped, divergences, acceptance output summary, real-interaction
   results (exact codes), what remains unverified.
9. Update NOTES.md with any dated LIVE-VERIFIED artifacts the phase
   produced. Then end the leg.

## Honesty rules (standing)

Report status specifically: what passed, what's unverified, what's blocked.
Never claim feature success from green tests alone. If a leg fails
acceptance twice on honest attempts, stop the loop and report rather than
thrash. Confirm external preconditions (env vars, ports free, modules in
go.work) before acting on them.
