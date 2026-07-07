# datastore-portability — milestone overview

Status: **RATIFIED 2026-07-02 (jrazmi)** — phases cut from the ratified design
at `.claude/plans/roadmap/datastore-portability.md` (the DESIGN DOC — every
phase references it by section number; do not re-decide anything it decides).
Date: 2026-07-02
Milestone: `datastore-portability` — the pgx connector, the per-feature
storetest conformance pattern proven on cms, the cms postgres backfill, and
the charter/ARCHITECTURE policy sync. First milestone of the ratified R10
sequence (may run concurrent with auth-v1; auth's phase 7 queues on P1 here).

## Inherited law

The constitution and decision log in `.claude/plans/restructure/00-overview.md`
apply unchanged (rules 1–8; D1–D9 + C1–C4). Additional standing facts:

- All design decisions DP1–DP8 are RATIFIED (design doc's decision table),
  as amended by `.claude/plans/roadmap/00-intersections.md` R3 (memory-store
  placement: DP2's test-scoped reference stands for simple features; an
  in-core public memstore package is allowed when substantial — jobs is the
  named case, NOT cms) and extended by R5 (design doc §8b: the Transactor
  gap is owned there; revisit trigger = third durable emitter — nothing in
  this milestone builds it).
- The auth-v1 amendments (design doc §8 / R1) are ALREADY APPLIED to
  `.claude/plans/auth-v1/` — P4 verifies, it does not re-apply.
- **Executor model policy (jrazmi): implementation phases run on
  `model: opus`; design/doc-judgment phases on `model: fable`. Never sonnet.**

## Phases (execute in order except where noted)

| Phase | File | What | Executor model |
|---|---|---|---|
| P1 | `01-postgres-connector.md` | `integrations/datastores/postgres` (pgx/v5) per design §3 | opus |
| P2 | `02-cms-storetest.md` | `features/cms/storetest` suite + reference in-memory impl per design §4 | opus |
| P3 | `03-cms-store-postgres.md` | `features/cms/stores/postgres` (EAV spine ported) per design §7 | opus |
| P4 | `04-docs-policy-sync.md` | charter/ARCHITECTURE/RELEASING/Makefile/NOTES sync per design §10 P4 + R6 | fable |

Dependencies: P1 and P2 are independent (may run in either order or
parallel legs). P3 needs P1 + P2. P4 needs P1–P3.

## Loop protocol

Same as `.claude/plans/auth-v1/00-overview.md`'s: one phase per leg, read
overview + phase file + **the design doc** fully, preconditions → work items
in order → acceptance → real-interaction check → dated execution-log entry →
stop. Surgical diffs; goimports-formatted; if a work item's premise is
false, do the closest correct thing and log the divergence; if it would
violate the constitution or a ratified decision, STOP and flag.

**Standing real-interaction check for this milestone** (every phase):
`make check` green (all modules, all guards), then boot `examples/minimal`
(localhost:8081 — read `cmd/server/main.go` for defaults), `GET /` and
`GET /products/widget-3000` → 200s, kill, port free. Report exact codes.
P3 additionally has its own live-Postgres gate (see its file).

**Live-store gating (design §4.3, verbatim rules):** postgres conformance
legs are env-gated on `POSTGRES_TEST_DSN` — set → run (each `newRepos`
truncates via `t.Cleanup`); unset → loud
`t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")`.
`make check` stays hermetic; `make test-stores` (added in P3) expects the
env vars and fails loudly without them. Milestone close requires one
recorded live conformance run per dialect as a dated NOTES.md artifact
(suite, dialect, DSN class, result) — a hermetic green NEVER closes this
milestone.

## Acceptance for the milestone as a whole

- All 4 phases' execution logs green; `make check` covers the (now 8)
  modules (+`integrations/datastores/postgres`, +`features/cms/stores/postgres`)
  and all guards.
- `features/cms`'s own `go test ./...` executes the storetest suite against
  the in-package reference implementation (no drivers in the core's graph —
  G2 still green).
- Recorded live conformance runs: cms×postgres (local docker acceptable)
  and cms×turso (existing `-tags=integration` gating), as dated NOTES.md
  artifacts per §4.3.
- Charter carries checklist items 10–12 and the dialect-set rule;
  ARCHITECTURE.md carries the kinds-of-module taxonomy + the §1 boundary
  line (R6).

## Execution log

(planning-leg and cross-phase entries appended here; per-phase logs live in
each phase file)

### 2026-07-02 — planning leg (loop leg 1): phase files cut

Cut `00-overview.md` + P1–P4 phase files from the ratified design
(`roadmap/datastore-portability.md` §10), carrying the acceptance rows,
§4.3 gating rules, R3/R5/R6 amendments, and the auth-v1 loop protocol.
No code touched. Divergence: none — design §10's four phases map 1:1.
Real-interaction check: `make check` → "all checks passed" (6 modules, 4
guards); booted `examples/minimal`, `GET http://localhost:8081/` → 200,
`GET http://localhost:8081/products/widget-3000` → 200; server killed
(note: `go run`'s child needed a direct kill — the wrapper PID alone
doesn't stop the server), port 8081 free. Unverified: nothing (docs-only
leg). Next leg: P1 (`01-postgres-connector.md`, opus).
