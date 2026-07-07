# Phase 9 — docs, guards, and record sync

Status: RATIFIED (cut-time split from design §10's phase 8, mirroring the
auth-v1 docs-phase precedent)
Executor model: fable (doc judgment)
Depends on: all prior phases.

## Work items

1. `features/jobs/README.md` — charter checklist item 7 + 12: trio layout
   tree, claimed namespace `/jobs/*` (documented, none registered in v1 —
   J5), the two ports' sentinel/claim contracts, Config fields with nil
   semantics (Schedules nil = queue-only; Cron conditionally required —
   the loud-error rule; defaults table), the Service/Runtime dual entry +
   §3.4 wake wiring note, Enqueue's stdlib-typed compatibility contract
   (§5.2), dialect set + docker one-liner + zero-store framing (memstore),
   at-least-once/idempotent-preferred handler contract (§6.3), pointer to
   `examples/jobs-minimal`.
2. Charter/ARCHITECTURE/root README/RELEASING: module counts + lists gain
   the four new modules; ARCHITECTURE's taxonomy row moves `jobs` from
   "next" to shipped; `sdk/workers` mentioned in the sdk facility list.
3. Capability map: mark the Jobs & events section's jobs rows executed
   (status note); jobs-feature-design.md status → IMPLEMENTED with
   pointer.
4. NOTES.md: dated milestone entry (what shipped, J-decision outcomes,
   live-run artifacts from phases 5/7, the §8 protocol result).
5. Fresh-eyes cross-check (clean-context subagent over the updated docs
   vs the tree): zero contradictions.

## Acceptance

```sh
make check
test -f features/jobs/README.md
```
Fresh-eyes reports zero contradictions.

## Real-interaction check

Standing check (a) AND the §8 proof-host protocol one final time — the
milestone's closing verification.

## Execution log

### 2026-07-02 — phase 9 executed (final loop leg; fable, direct) — MILESTONE CLOSES

Docs synced: `features/jobs/README.md` written (trio tree with the
no-inbound note, /jobs/* claimed-not-registered, port contracts incl.
lease + at-least-once handler contract, Config nil-semantics table,
wake-wiring + dual-entry, Enqueue compatibility contract, dialect set +
zero-store framing, CronSchedule alias note, proof-host pointer).
ARCHITECTURE (18-module layout + workers in the facility examples + jobs
shipped + the features annotation corrected per fresh-eyes), root README
(eighteen modules), RELEASING (18/4 hosts), sdk/README (STALE ratelimiter
"dormant" row corrected — Memory + auth consumer are reality — and a
workers row added), capability map (jobs rows BUILT; events rows noted
deferred), jobs-feature-design status → IMPLEMENTED with the four executed
amendments listed.

Fresh-eyes pass (clean-context subagent, five docs vs tree): ONE
contradiction found and fixed (ARCHITECTURE's features annotation claimed
internal/{logic,inbound} generically; jobs v1 deliberately has no
inbound); everything else clean, incl. the 18-count in go.work AND
Makefile.

Acceptance: `test -f features/jobs/README.md` OK; `make check` → "all
checks passed" (18 modules, 4 guards). Real-interaction: minimal :8081 →
200/200; jobs-minimal :8083 spot-run — enqueue → job_c336a5e9… →
handler log within the second → SIGTERM clean → ports free.

Milestone-close checks (00-overview acceptance): all 8 phase logs green;
features/jobs go.mod requires EXACTLY 1 (gopernicus/sdk); rule-6 greps 0
both directions (two prose doc-comment refs in the turso store reworded
so the literal acceptance grep is clean — they were never imports; G2
green throughout); live artifacts recorded (phases 5/7 NOTES entries,
turso playground + postgres docker); §8 protocol passed (phase 8 log).
**jobs-v1 is DONE.**
