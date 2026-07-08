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
