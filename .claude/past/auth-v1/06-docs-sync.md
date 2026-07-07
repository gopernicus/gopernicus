# Phase 6 — docs, guards, and record sync

Status: DRAFT — pending ratification
Executor model: fable (doc judgment)
Depends on: phases 1–5 + 7 (this phase executes last; see `00-overview.md`'s
execution-order note).

## Goal

The written record catches up with the second feature in the same leg it ships
— no repeat of the original repo's docs-lag-code debt.

## Work items

1. `features/auth/README.md` — per charter checklist item 7: route surface
   (namespace `/auth/*`, prefixable, JSON-only so C1's absolute-link limitation
   doesn't apply), `Config` fields incl. the required-vs-defaulted asymmetry
   and its security rationale (design doc §2), `Repositories` ports, the
   `NewService`/`Register` dual-entry pattern with the wiring sketch, the
   supported dialect set ({turso, postgres} + the storetest reference impl,
   per the ratified portability policy) with the docker one-liner for local
   postgres conformance runs, and a pointer to `examples/auth-cms`.
2. `features/README.md` — execute the deferred anatomy-table generalization
   (adversarial-review finding #3, recorded in the design doc appendix):
   `<name>.go`'s row becomes "the feature's host-facing exported surface
   (Repositories, Config, Register — plus whatever additional exported
   constructors/types cross-feature needs require, e.g. auth's
   Service/NewService)". Re-verify the charter's §5 worked example now that it
   is real — update wording from illustrative to actual, citing
   `examples/auth-cms`.
3. Guards: confirm A4's generalized G2 covers `features/auth` (prove-can-fail
   again for the generalized pattern); root README's module map + count gains
   the four new modules (incl. `stores/postgres`); ARCHITECTURE.md's layout
   block likewise (keep its one-line-per-module style); Makefile `MODULES`
   verified to include all four.
3b. Charter checklist items 10–11 (storetest exists + all supported dialects
   pass it, per the ratified portability policy — added by portability P4)
   verified true for `features/auth`; if portability P4 hasn't landed the
   checklist items yet, note that in the execution log rather than adding
   them here (P4 owns the charter edit).
4. `RELEASING.md`: add the new modules to the tagging list.
5. NOTES.md: dated milestone entry (what shipped, A1–A4 outcomes, live-Turso
   verification status from phase 5).
6. Capability map: mark the auth-v1 rows executed (status note, not a rewrite);
   `auth-feature-design.md`: top status line → implemented, with a pointer to
   the real code.
7. Fresh-eyes cross-check (same protocol as restructure phase 1 W8): a clean
   reader takes only the updated docs and verifies every checkable claim
   against the tree. Zero contradictions.

## Acceptance

`make check` green; `test -f features/auth/README.md`; fresh-eyes pass reports
zero contradictions.

## Real-interaction check

Standing checks (a) AND (b) — the full five-step auth flow one final time, as
the milestone's closing verification.

## Execution log

### 2026-07-02 — phase 6 executed (loop leg 13; fable, direct) — MILESTONE CLOSES

Docs synced against the POST-TRIO-RELAYOUT tree (the re-layout executed
between phases 7 and 6; this phase documents the new shape once):
1. `features/auth/README.md` written — trio layout tree, route surface
   (/auth/*, strict decoding, rate-limit-first), five ports + sentinel
   contract, Config required-vs-defaulted table with rationale,
   NewService/Register dual-entry + wiring pointer, dialect set + docker
   one-liner + zero-store proof framing + the two schema notes.
2. Charter: §2 anatomy rewritten to the trio (generalized `<name>.go` row
   per the W3 finding); the app↔feature MAPPING TABLE + reading rule
   added; the internal-seam extension-model ruling recorded; §3's
   multi-datastore bullet gained store posture C; §5's worked example
   updated from illustrative to REAL (cites examples/auth-cms + the
   verified greps/flow).
3. ARCHITECTURE.md: layout block → 13 modules incl. auth tree + trio
   note; "Thirteen modules today"; taxonomy feature row `cms, auth`.
   Root README: thirteen-module block + test-stores mention. RELEASING:
   thirteen modules, three example hosts.
4. Capability map: auth-v1 execution note (v1 rows BUILT, v2 rows stay
   deferred); auth-feature-design.md status → IMPLEMENTED with trio-path
   note. jobs design tree → trio; events design gained the layout note.
5. Guards: A4-generalized G2 verified this session (phase 1 +
   relayout prove-can-fail runs); Makefile MODULES carries all 13.
6. Checklist items 10–11 verified for auth: both dialect stores pass the
   suite, live runs recorded (phase 5 + 7 NOTES artifacts).
7. Fresh-eyes pass (clean-context subagent, five docs vs tree): ZERO
   contradictions, first pass.

Acceptance: `test -f features/auth/README.md` OK; `make check` → "all
checks passed" (13 modules, 4 guards). Closing real-interaction: check (a)
minimal :8081 → 200/200; check (b) FULL five-step flow firsthand →
401, 201, 200+cookie, 200, 200, 401; ports free.

Milestone acceptance (00-overview): all 7 phases' logs green; auth go.mod
requires exactly gopernicus/sdk; flow (b) passes; rule-6 greps empty both
directions; live conformance artifacts recorded per dialect (turso
playground + postgres docker, each twice). **auth-v1 is DONE.**
