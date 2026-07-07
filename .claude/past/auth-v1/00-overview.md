# auth v1 — milestone overview

Status: **DRAFT — pending ratification by jrazmi**
Date: 2026-07-02
Milestone: `auth-v1` — build `features/auth` v1 per the ratified design at
`.claude/plans/restructure/auth-feature-design.md` (the DESIGN DOC — every phase
references it; do not re-decide anything it decides).

## Inherited law

The constitution and decision log in `.claude/plans/restructure/00-overview.md`
apply unchanged (8 rules; D1–D9 + C1–C4 + the 2026-07-02 post-milestone rulings
in NOTES.md). Additional standing facts:

- The app hexagon is `internal/{inbound, logic, outbound}` (D5 as amended —
  never `sol`, never `core`).
- Capability-map YOUR CALLs 1–9 are ratified to their defaults.
- The three phase-2 findings are FIXED (Console nil→io.Discard; memstore term/menu
  uniqueness enforced) — the design doc's §4 gotchas are marked resolved.
- **Executor model policy (jrazmi, 2026-07-02): implementation phases run on
  `model: opus`; design/doc-judgment phases on `model: fable`. Never sonnet.**

## Milestone decisions (new, this milestone)

| # | Decision | Status | Rationale |
|---|---|---|---|
| A1 | Two-feature proof host is a NEW module `examples/auth-cms` (examples/minimal stays untouched as the single-feature zero-infra proof) | **Proposed default** | The design doc left this "implementer's call" (§4). A separate host keeps examples/minimal pedagogically pristine and keeps the standing real-interaction check stable; the new host becomes the auth+cms composition proof |
| A2 | **(AMENDED 2026-07-02, ratified R1)** `integrations/datastores/postgres` is built by the datastore-portability milestone (P1), never by auth-v1; `features/auth/stores/postgres` IS in this milestone per the ratified DP1 charter rule (dialect parity gates milestone close) | **Ratified** | jrazmi's multi-datastore directive; `.claude/plans/roadmap/datastore-portability.md` §8. Sequencing: phases 1–6 proceed regardless of the portability milestone; phase 7 queues on portability P1 having landed |
| A3 | cms's middleware hook is `cms.Config.AdminMiddleware []web.Middleware`, applied to admin routes only (public routes untouched) | **Proposed default** | Matches the design doc §3's sketch; narrow, additive, no Mount change |
| A4 | Guard G2 generalizes from `features/cms` to ALL `features/*` cores (never import integrations/examples/own stores) | **Proposed default** | Second feature makes the per-feature grep a pattern; generalize once |

## Phases (execute in order; dependencies noted)

| Phase | File | What | Executor model |
|---|---|---|---|
| 1 | `01-auth-core.md` | `features/auth` module: entities, ports, services, JSON HTTP, Register/Service | opus |
| 2 | `02-bcrypt.md` | `integrations/cryptids/bcrypt` module | opus |
| 3 | `03-cms-admin-middleware.md` | `cms.Config.AdminMiddleware` hook (A3) | opus |
| 4 | `04-proof-host.md` | `examples/auth-cms`: two-feature zero-infra host (A1) — the milestone's acid test | opus |
| 5 | `05-auth-store-turso.md` | `features/auth/stores/turso`: SQL + `"auth"` migrations | opus |
| 7 | `07-auth-store-postgres.md` | `features/auth/stores/postgres`: SQL + `"auth"` migrations (identical version set), env-gated live conformance run (ratified R1) | opus |
| 6 | `06-docs-sync.md` | READMEs, charter anatomy-table generalization (the flagged nit), guards, records | fable |

Dependencies: 4 needs 1+2+3. 5 needs 1. 7 needs 1 + datastore-portability P1
(the pgx connector — built there, never here). 6 needs everything incl. 7 —
phase 6 executes LAST despite the file numbering (the table above is the
execution order). (2, 3 are independent of 1 but sequenced after it so the
ports they satisfy/gate exist first.)

## Loop protocol

Same as `.claude/plans/restructure/00-overview.md`'s: one phase per leg, read
overview + phase file + **the design doc** fully, preconditions → work items in
order → acceptance → real-interaction check → dated execution-log entry → stop.
Surgical diffs; goimports-formatted; if a work item's premise is false, do the
closest correct thing and log the divergence; if it would violate the
constitution, STOP and flag.

**Standing real-interaction check for this milestone** (phases 1–3 use (a);
phases 4–6 use (a)+(b)):

(a) No-regression: `make check` green (all modules, all guards), then boot
`examples/minimal` (localhost:8081), `GET /` and `GET /products/widget-3000`
→ 200s, kill, port free.

(b) Auth flow (once `examples/auth-cms` exists; read its main.go for port —
plans assume localhost:8082): with a cookie jar (`curl -c jar -b jar`):
1. `GET /articles` (cms admin list) with no session → expect 401 (JSON error).
2. `POST /auth/register` (JSON body per the design doc's route surface) → 2xx.
3. `POST /auth/login` → 2xx + Set-Cookie (session).
4. `GET /articles` with the cookie → 200.
5. `POST /auth/logout` → 2xx; repeat step 4 → 401.
Report exact codes for all five steps in the execution log. The milestone is
NOT done while any step diverges.

## Acceptance for the milestone as a whole

- All 7 phases' execution logs green; final `make check` covers every module
  in the Makefile `MODULES` list (this milestone adds five:
  `features/auth`, `integrations/cryptids/bcrypt`, `examples/auth-cms`,
  `features/auth/stores/turso`, `features/auth/stores/postgres`) and the
  generalized guards.
- Recorded live conformance run per dialect (turso + postgres) as dated
  NOTES.md artifacts, per `roadmap/datastore-portability.md` §4.3 — a green
  hermetic `make check` alone does not close the store phases.
- `features/auth/go.mod` requires **exactly** `gopernicus/sdk` (nothing else) —
  leaner than cms, per design doc §5 item 2.
- The (b) flow above passes end-to-end.
- Constitution rule 6 demonstrated: `grep -rn "features/auth" features/cms/` and
  `grep -rn "features/cms" features/auth/` both empty.
