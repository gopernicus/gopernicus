# segovia-lessons — milestone overview

Status: **OPEN (standing intake) — ratified 2026-07-08; phase 01 EXECUTED 2026-07-08**
Milestone: `segovia-lessons` — the **standing intake** for framework gaps
surfaced by Segovia v2, the host app being built in tandem with gopernicus
in a separate repo. Segovia records gaps as **flags** in its own flags doc;
this milestone is where a flag becomes a gopernicus plan. Flags are adopted
**verbatim** (the ratified-in-segovia text is the input — never re-derived
here); what gets decided here is only the gopernicus-side shape: what to
ratify into ARCHITECTURE.md / `features/README.md`, what code to re-slice,
and what to consciously decline.

This is deliberately an open-ended milestone: it closes when the owner says
the intake is drained, not when a fixed phase list completes. Each flag gets
its own phase file when it is planned; queued flags live only in the ledger
below until then. **Health condition (PM review fold, 2026-07-08):** drained
vs paused is judged against the ledger — every row PLANNED, QUEUED with its
raised-on date visible, or DECLINED with rationale; a QUEUED row aging past
usefulness gets scheduled or declined at the next cut, and a pause is
recorded here explicitly, never left to silence.

Executor model policy (jrazmi, standing since jobs-v1): implementation
phases on `model: opus`; design/doc-judgment phases on `model: fable`.
Never sonnet.

## Provenance discipline

- The input text of record for each flag is Segovia's flags doc entry, quoted
  in the phase file that plans it. Segovia v2's live tree is the reference
  implementation for app-side conventions (no host in THIS repo has app-local
  domains — both examples are thin hosts — so app-side adoptions are
  documentation-only here, with Segovia as the living proof).
- **Editing Segovia's flags doc is out of scope in every phase** (other
  repo); the owner flips a flag to ratified there themselves.
- A flag that gopernicus declines (in whole or part) gets a recorded
  rationale in the phase file + NOTES.md — never a silent drop.

## The flag ledger (as of 2026-07-08 — three flags exist)

| # | Flag (short) | Raised | Status here | Disposition |
|---|---|---|---|---|
| 1 | **App-local inbound anatomy is underspecified** — ratify Segovia's `internal/inbound/domains/<domain>/` anatomy (routes/api/html/views-port/templates, growth rule, theming seam, override-via-embedding); features somewhat mirror it | 2026-07-08 | **EXECUTED 2026-07-08** — `01-inbound-anatomy.md` | Ratify + document in ARCHITECTURE.md and `features/README.md`; re-slice the three feature inbound packages; D1 RATIFIED (d) `internal/inbound/<feature>/` 2026-07-08 (phase file §D1) |
| 2 | **`sdk/id` is string-only** (26-char lowercase base32) — Segovia needs int, string, AND uuid identifiers | 2026-07-08 | **QUEUED** — no phase file yet | Owner is reviewing ID strategy; cut a phase (`02-…` or later) when that review lands. Do NOT pre-design here — the kind-set and port shape are the owner's open question |
| 3 | **Old-monolith import-path collision** — `github.com/gopernicus/gopernicus v0.5.4` (the original monolith) and this multi-module repo share import-path prefixes, so one Go workspace can never hold both; needs a note in RELEASING.md / migration docs | 2026-07-08 | **QUEUED** — docs-only micro-phase when scheduled | Deliberately NOT folded into phase 01 (scope discipline: 01 is inbound anatomy; a RELEASING.md compatibility note shares nothing with it). Small enough to cut as a one-task phase, or to ride along the next RELEASING.md-touching plan — owner's call at that moment |

| 4 | **Feature route tables can't use the sdk's method helpers** — `RouteRegistrar` retained deliberately (seam ruling, 2026-07-08); add `feature.Methods` sugar, delete unused FS7 route.go, document the host override tiers | 2026-07-08 (owner-raised in-session, not from Segovia's doc) | **CLOSED 2026-07-08 (as amended)** — `02-route-ergonomics.md` | D2 executed (route.go deleted, zero consumers); **D4: Methods sugar built, live-proven, then DECLINED by owner and reverted same day** (cosmetics-only sdk surface; stringly Handle = deliberate guest signposting; resurrect trigger = real host demand); §4 per-route override story documented |

New flags append rows here (dated) as Segovia raises them; each planned flag
gets the next phase number. Flags may also be owner-raised directly (flag #4
is the precedent) — the intake is for lessons from building WITH Segovia,
whichever side spots them.

## Inherited law

The constitution (`restructure/00-overview.md`), the trio layout as amended
2026-07-08 (`features/*/domain/` public rim — commit 88239a5), the
feature-standard charter FS1–FS10 (ratified 2026-07-07 — FS1 sdk-only core
and FS3 views-port doctrine are load-bearing for phase 01's
"necessarily-partial mirror"), `features/README.md` §§2–3, and the
ARCHITECTURE.md app pattern all apply unchanged. This milestone AMENDS
documentation and re-slices files; it does not relitigate any locked
decision — where a flag's text and a locked decision meet (FS1/FS3 vs
co-located templates), the locked decision wins and the divergence is
documented as the deliberate feature-side delta.

## Phases

| Phase | File | Flag | What | Size | Model | Modules after |
|---|---|---|---|---|---|---|
| 01 | `01-inbound-anatomy.md` | #1 | Ratify the app-local inbound anatomy (ARCHITECTURE.md), define the feature-side partial mirror (`features/README.md`), decide D1 (feature inbound package name), re-slice `features/{authentication,cms,events}/internal/inbound/http/` to the ratified file anatomy | M | fable (docs) / opus (re-slice) | **30** (unchanged) |
| 02+ | — not yet cut | #2, #3, future | Cut per flag when the owner schedules it | — | — | — |

## Module / API impact (milestone, as currently scoped)

- **Zero new modules — 30 stands.** Phase 01 touches only `internal/`
  packages (import paths private to each feature module) and docs; no
  exported symbol changes, no go.mod/go.work changes, no RELEASING.md
  tagging implications. Flag #2, when planned, WILL touch `sdk`'s public
  API — that impact is assessed at its own cut.
- Guard posture verified at this cut (2026-07-08): **no Makefile guard
  greps an `internal/inbound/http` path** — G6
  (`guard-feature-transport-sdk-web`) scans `features/*/internal/`
  path-agnostically, and G2/G5/G7 are import/go.mod-shaped — so phase 01's
  file/package re-slice needs no guard edits under either D1 outcome.

## Generated-artifact impact

None as scoped. Phase 01 touches no `.templ` sources and no `*_templ.go`
files (the cms views live in the `features/cms/views/templ` sibling module,
untouched); `make check`'s templ-drift gate runs anyway every phase.

## Goal

Every Segovia flag has a recorded gopernicus disposition — ratified into
docs/code, queued with an owner, or declined with rationale — and flag #1's
inbound anatomy is ratified, documented, and reflected in the three feature
inbound packages.

## Out of scope (milestone)

- Editing Segovia's flags doc or any Segovia code (other repo).
- The `gopernicus new domain` scaffold shaped by the anatomy (future
  workshop-v2 brief — flag #1's text names it; recorded, not planned here).
- Implementing flags #2 and #3 (queued; own cuts).
- Any change to the FS1/FS3 feature-standard rules themselves.

## Risks (ordered)

1. **Vocabulary collision around `domain`** — the public rim was renamed to
   `features/*/domain/` on 2026-07-08 (commit 88239a5); D1 option (a) would
   spend the same word on the HTTP interior days later. Mitigation: D1 is an
   explicit owner-ratification decision in phase 01 with tradeoffs laid out,
   never a silent pick. **Resolved 2026-07-08:** owner withdrew (a) and
   ratified (d) `internal/inbound/<feature>/` — the rim's word stays
   unspent.
2. **Doc drift between the two repos** — the anatomy is ratified in Segovia
   and re-stated here; a paraphrase that drifts becomes two conventions.
   Mitigation: phase 01 quotes the flag verbatim and the ARCHITECTURE.md
   subsection is written against that quote, with Segovia named as the
   living reference.
3. **Re-slice churn on a dirty tree** — the working tree carries mid-flight
   modifications to several inbound files at cut time. Mitigation: phase 01
   preconditions require a clean tree at execution.

## Open questions — FOR RATIFICATION (jrazmi)

1. **D1 — the feature inbound package name** — **RATIFIED (d)
   `internal/inbound/<feature>/` 2026-07-08** (phase 01 §D1 has the full
   record; `http/` is plumbing-only on both sides of the app/feature
   line).
2. **Flag #3 vehicle** — own one-task micro-phase vs riding the next
   RELEASING.md-touching plan (ledger row above; decide when scheduling it).

## Recommended reviews

All run post-cut 2026-07-08 (`lead-backend-engineer` ran in place of the
post-hoc frontend pass — the Go mechanics were the open surface; the
frontend doctrine was consulted pre-cut). Verdicts: ship-with-edits across
the board; every must-fix folded — see phase 01 §Consultation notes.

- `product-manager` — scope discipline of the intake shape (verbatim
  adoption, queue-don't-design) and phase 01's cut lines.
- `architecture-steward` — the ARCHITECTURE.md / `features/README.md`
  amendments and the D1 options framing.
- `lead-frontend-engineer` — consulted pre-cut on phase 01 (see its
  Consultation notes); post-hoc review of the final doc text welcome.

## Notes

Cut 2026-07-08 alongside the authorization-v1 DRAFT; the two milestones are
independent (no shared files beyond ARCHITECTURE.md's general growth — if
both land ARCHITECTURE.md edits, ordinary rebase discipline applies).
