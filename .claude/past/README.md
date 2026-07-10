# .claude/past — closed-milestone plan archive

Relocated here 2026-07-07 (jrazmi directive): plan directories for **closed,
fully-executed milestones** move out of `.claude/plans/` so the plans
directory holds only active/draft work. Nothing in here governs new work;
these are execution records.

Contents (each closed with dated NOTES.md entries and live-verified gates):

| directory | milestone | closed |
|---|---|---|
| `datastore-portability/` | pgx connector, cms storetest + postgres backfill | 2026-07-02 |
| `auth-v1/` | features/auth v1 (password + sessions), bcrypt, both stores | 2026-07-02 |
| `jobs-v1/` | sdk/workers, features/jobs, robfig-cron, both stores | 2026-07-02 |
| `sdk-parity/` | original repo's deferred sdk surface (8 phases, 27 tasks) | 2026-07-06 |
| `kvstore-consolidation/` | R-KV1–R-KV3: goredis multi-port, pgx renames | 2026-07-06 |
| `fast-follows/` | backends for every sdk port (21→26 modules) | 2026-07-06 |
| `telemetry-closeout/` | sdk/web.Tracing middleware + otel observability proof, hygiene flags, demand-gated ledger | 2026-07-07 |
| `auth-v2/` | identity remainder in features/auth: v1 debts (session hashing), OAuth, machine identity, JWT bearer, audit rail, invitations; both stores + A9 proof host | 2026-07-07 |
| `feature-standard/` | FS1–FS10 extension model (charter W1–W4) + convergence: auth driving surface, cms plain-text core + views/templ extraction with Views port, sdk view/route primitives, jobs Register, connector promotions D2–D6 | 2026-07-08 |
| `events-v1/` | features/events (outbox + poller + SSE gateway) + both stores; sdk/identity (A-I1); features/auth → features/authentication (A-R1); Mount.Events; cms content.* emitter; G7 rule-6 guard; live turso + pgx conformance | 2026-07-08 |
| `pgx-crud-v1/` | pgx v5 idiom sweep (NamedArgs, CollectRows/RowToStructByName over row structs, UNNEST bulk writes) + sdk/crud List standards: order allow-lists, bidirectional cursors, offset mode, WithCount totals; connector List[T] toolkits; six-case storetest family everywhere; cms Pager views; legacy ListPage deleted. `v1a-list-strategy.md`: explicit crud.Strategy (offset>0 inference removed), ListParams parse struct with env-configurable default, listCursor/listOffset flow split. `v1b-list-limits.md`: crud.Limits per-aggregate (rim ListLimits convention; constants demoted to fallbacks; NormalizedLimit(Limits)) | 2026-07-08/09 |
| `authorization-v1/` | features/authorization — the IAM domain, two independently-wireable kinds (relationships/ReBAC engine salvage + roles) + policy as a named deferred seam; stores/{turso,pgx} with unbounded UNION-dedup recursive CTEs; memstore + both stores live-green on ONE storetest suite (5 named adversarial sub-runners + Roles/*, DP1 parity); Q6 Config.IDs mint seam + inline DDL DEFAULTs; three postures demonstrated on examples/auth-cms (commit-1 `2e1e5eb` middle posture, commit-2 `65fcb49` flagship, Granter swap = design §6 completed); G8 store-glue guard (modules 32–34) | 2026-07-09 |
| `datastore-hardening/` | connector parity + strictness from the post-authorization audit: order allow-lists → rims; turso List identifier strictness (roles role_key rework); /healthz ×4 hosts + the StatusCheck lazy-ping fix (503 driven live); turso query-logging/RedactDSN parity; the FULL turso struct-scan sweep (strict ScanStruct[T], Scanner types, ~23 row structs, zero hand-scans left); crud.Transactor scaffolded UNCONSUMED + guards G9 no-Underlying / G10 no-Lax (ten guards); opt-in Config.Retry (acquisition-only). All ten store suites live-green at close; auth-pgx collation sensitivity flagged | 2026-07-09 |
| `workshop-v2-scaffolding/` | the scaffolding CLI (module 35, the sixth taxonomy kind + guard G11 → eleven guards): `gopernicus init` (host scaffold), `new feature` (26-template FULL charter skeleton: six-case storetest family, both dialect stores, IDs seam, memstore), `db create/migrate/status` (delegation — the CLI's go.mod keeps zero requires); scaffold-compile tests inside make check as the drift answer (emit → absolute-replace → build → emitted storetest → guard shapes); live proofs: emitted host booted, wired create+list, full db verb chain vs throwaway postgres. Regenerate-forever surfaces → workshop-v2b, demand-gated | 2026-07-09 |

Path note: NOTES.md entries and ratified/historical docs written before
2026-07-07 cite these as `.claude/plans/<milestone>/...`; those citations
now resolve under `.claude/past/<milestone>/...`. Historical documents were
NOT rewritten (append-only discipline); active drafts were updated at move
time.

What deliberately did NOT move:
- `.claude/plans/restructure/` — the constitution (rules 1–8) and the
  capability map: inherited law, cited by every active plan.
- `.claude/plans/roadmap/` — ratified rulings (00-intersections R1–R10),
  designs of record (jobs/events/datastore-portability/auth-v2), and
  loop-handoff.md (still the named resume pattern for events-v1).
- Active/draft milestones: `events-v1/`, `repo-hardening/`,
  `telemetry-closeout/`.

Rule going forward: when a milestone's closing NOTES.md entry lands, its
plan directory moves here in the same session.
