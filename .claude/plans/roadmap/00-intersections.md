# Roadmap synthesis — taxonomy, intersections, and the consolidated YOUR CALL list

Status: **RATIFIED 2026-07-02 (jrazmi)** — R1–R10 all ratified, none
rejected; R8 explicitly ratified as amended by R2/R3. Reconciliation edits
APPLIED same day: the seven R1 amendments to `.claude/plans/auth-v1/`
(incl. the new `07-auth-store-postgres.md`), jobs' pgx phase struck +
`memstore` rename (R2/R3), events' suite renamed `storetest` (R4), the
portability plan's §8b Transactor addendum (R5). R6's ARCHITECTURE.md/
charter additions execute in portability P4 (they are milestone work, not
plan edits).
Date: 2026-07-02
Synthesizes: `datastore-portability.md`, `jobs-feature-design.md`,
`events-feature-design.md` (this directory), against the in-flight
`.claude/plans/auth-v1/` draft, the charter (`features/README.md`), and the
restructure constitution. The three sibling plans were written concurrently
and cite each other one-directionally; this document is the cross-check —
it names the taxonomy, maps the seams, reconciles the conflicts the
concurrent writing produced, and consolidates every open decision into one
ratification list (§6).

Nothing here re-decides what a sibling plan decided; where two plans
disagree, the disagreement is surfaced as a YOUR CALL, not resolved
silently.

## §1 Taxonomy — facilities vs features (jrazmi's "capabilities" question, answered)

jrazmi (2026-07-02): *"do we distinguish between capabilities and features?
jobs or events seem more like capabilities that features would be able to
use… these are sort of integrations but our own implementations."*

The architecture already carries this distinction; this section names it so
plans stop re-deriving it. Four kinds of thing exist (proposed vocabulary —
"facility" rather than "capability," because `capability-map.md` already
uses "capability" to mean inventory rows):

| kind | definition | examples | swap unit |
|---|---|---|---|
| **sdk facility** | a capability **port** + a first-party stdlib default + a conformance suite; state is opaque to the host (no host-owned schema, no migrations, no routes) | `cacher`+`Memory`, `email`+`Console`/`SMTP`, `ratelimiter`+`Memory`, `filestorage`+`Disk`; **new**: `workers` (jobs plan §2), `events` bus+`Memory`/`Noop` (events plan §2); future: `tracing`+`Noop` | a config value — the swap is invisible outside the process |
| **integration** | a third-party backend for a port; one library, one module | `datastores/turso`, `cryptids/bcrypt`; **new**: `datastores/postgres`, `scheduling/robfig-cron`; future: `caches/redis`, `events/redis` | a module import in the host's `main` |
| **feature** | a mountable domain module: own entities, **own durable schema + migrations**, and/or **own route surface**; hosts `Register` it | `cms`; planned: `auth`, `jobs`, `events` | a `Register` call |
| **store module** | a feature's per-dialect SQL + migrations (`stores/<dialect>`) | `cms/stores/turso`; planned: `*/stores/postgres` | a module import + one `Open` call |

[AMENDED 2026-07-07, feature-standard FS3 (`.claude/plans/feature-standard/00-charter.md`): a fifth kind, the **views module** (`views/<pkg>`) — a feature's bundled presentation default implementing the core's `Views` port; swap unit = a module import + one `Config` field. The feature row's `Register` shape is superseded by FS2: `NewService` + `svc.Register(mount)`. ARCHITECTURE.md's table is the live form.]

**The litmus tests** (both already stated in the sibling plans, unified here):

1. *Facility vs store module* (portability §1): if swapping the adapter
   changes what the host must **migrate**, it's a store module per dialect;
   if the swap is invisible outside the process boundary, it's one port
   with swappable backends. This is why "a cacher doesn't care if it's
   redis/in-memory" is true and "a repository doesn't care if it's
   turso/postgres" is only true *at the port* — the host still carries the
   dialect's driver, DDL, and operations.
2. *Facility vs feature*: needs its own migrations or routes → feature;
   pure behavior a consumer calls → facility. **Jobs and events are each
   both**, split accordingly: the worker pool and the bus port are
   facilities (`sdk/workers`, `sdk/events`); the durable queue, cron
   schedules, outbox, and SSE gateway are features (`features/jobs`,
   `features/events`) because they own tables and (for SSE) routes.

**No feature variants, ever.** "cms event-driven vs cms non-event-driven"
does not exist as two artifacts. A feature acquires an optional capability
via one nil-safe port field (`Config` or `Mount`), and the **host's `main`
is where the app's shape is decided** — wire the port or don't. The cost is
linear (one field + one documented nil-meaning per capability), not
combinatorial. §2 is that documentation, as a matrix.

Where this lands in the repo: ARCHITECTURE.md gets a short "kinds of
module" table (portability P4 / docs-sync is the natural carrier — flagged
there, decided here).

## §2 Degraded-mode matrix — what nil means, per feature × optional port

The executable form of "no feature variants." Verified against
`features/cms/cms.go` for cms; design-doc values for the rest.

| feature | port (where) | nil / absent means | required instead? |
|---|---|---|---|
| cms | `Config.Views` | bundled site chrome [SUPERSEDED 2026-07-07, feature-standard FS3: nil → the HTML surface is not registered, uniformly across features; the bundled default becomes one import + one Config field from `views/templ` at convergence B2] | — |
| cms | `Config.Cache` | no public-page caching | — |
| cms | `Config.Blobs` | media upload unusable — host infrastructure the feature cannot default | effectively required for media |
| cms | `Config.Mailer` | contact-form delivery has no transport | effectively required for contact |
| cms | `Mount.Events` (future, events plan §4) | cms emits nothing; no SSE/cache-invalidation wake-ups | — |
| cms | `TaskEnqueuer` port (future, jobs plan §7.3) | no scheduled publishing | — |
| cms | `Config.AdminMiddleware` (auth-v1 A3) | admin routes unauthenticated (today's status quo) | — |
| auth | `Config.Hasher` | **hard `Register`/`NewService` error** | required — security foot-gun otherwise |
| auth | `Config.Mailer` | **hard error** | required — silent email drop is unsafe |
| auth | `Config.RateLimiter` | `ratelimiter.NewMemory()` default | — |
| auth | jobs dependency | none — session/token expiry is enforced on read (jobs plan §7.2) | — |
| jobs | `Repositories.Schedules` | queue-only host; Runtime skips the scheduler | — |
| jobs | `Config.Cron` | error **only when** a `Spec.Cron` schedule appears; `Spec.Every` is the stdlib path | conditionally required |
| jobs | `Mount.Jobs` (designed, deferred — jobs plan §5) | features can't self-register background work; host-authored handler closures cover it | — |
| events | `Config.Bus` | **hard error** — a gateway with no bus is misconfiguration | required |
| events | `Config.Identity` | **hard error** for streams | required |
| events | `Config.Authorize` | resource-scoped stream routes not registered (deny by absence) | — |
| events | `Repositories.Outbox` | direct-emit mode: no poller, no durable rail | — |
| any feature | `Mount.Migrations` | no migration collection (examples/minimal's shape) | — |

Pattern confirmed across all four features: **safe degradation gets a
silent default; unsafe degradation gets a loud constructor error** (auth's
Hasher/Mailer precedent, adopted by events for Bus/Identity and by jobs for
conditional Cron). New features must declare each optional port's row in
their README — proposed as charter checklist item 12 (§6, R6).

## §3 Seam map — who provides, who consumes

| seam | provider | consumers | contract | status |
|---|---|---|---|---|
| identity middleware + current-user | `auth.Service` (`RequireUser`, `CurrentUser`) | cms admin gating (via `Config.AdminMiddleware`, A3); events SSE connect-time (`CurrentUser` port + `StreamMiddleware`) | structural satisfaction of consumer-declared ports; host wires; no feature→feature import | auth-v1 phases 1+3; events consumes later |
| worker execution | `sdk/workers` (Pool, Runner, `ErrNoWork`, `WithWakeChannel`, Middleware) | `features/jobs` runtime (first); `features/events` outbox poller (second — needs Pool/WorkFunc/wake only, stated as requirements in events §5); any host loop | jobs plan §2 owns the surface; events plan adds no demands beyond it (verified: requirement lists match) | jobs-v1 phase 1 |
| event emission | `sdk/events` `Emitter` via `Mount.Events` | cms `entrysvc` (first emitter); auth v2 security events (first **durable** emitter, via outbox not Mount) | best-effort at-most-once on Mount; durable at-least-once rides Repositories (events §3 guarantee table) | events-v1 phases 2–3 |
| event consumption | `sdk/events` `Bus` (full port, via events feature `Config.Bus`) | SSE gateway; host cache-invalidation subscriber | one bus instance flows to both `Mount.Events` and gateway `Config.Bus` | events-v1 |
| cross-feature enqueue | `jobs.Service.Enqueue` — **stdlib-typed signature as a hard compatibility contract** (jobs §3.2) | any feature's own narrow `TaskEnqueuer`-shaped port (cms scheduled publishing is the named first) | structural match, zero imports | jobs-v1; consumers later |
| pgx connector | `integrations/datastores/postgres` | `features/{cms,auth,jobs,events}/stores/postgres` | connector surface mirrors turso member-for-member by convention (portability §3) | **portability P1 — single owner, see §4 conflict 1** |
| transactional appender | emitting store declares `OutboxAppender` (dialect-typed Tx); events store satisfies | cms/auth stores in durable mode | no store→store import; integration's Tx is the shared vocabulary (events §5) | designed; wired only when a durable consumer exists |
| storetest conformance | each feature's suite package | memory reference, host memstores, each dialect store | portability §4 owns the pattern | per feature-v1 milestone |
| migrations ledger | host-owned, `(source, version)` | sources `"cms"`, `"auth"`, `"jobs"`, `"events"` | same source name across a feature's dialects; identical version-filename set per feature (portability §6); **no cross-source ordering** — events' boot-time probe mitigates (events §5, risk 2) | standing |

One-directional by construction: portability → (jobs, events, auth
amendments); jobs → events (`sdk/workers`); auth → (cms gating, events
identity). No cycles. Verified consistent: `ErrNoWork` defined once in
`sdk/workers`, used by queue `Claim` and outbox `Poll`; wake-channel
protocol identical in both plans; `Record.EventID` the single de-dupe key.

## §4 Cross-plan conflicts (found by this synthesis; each carried into §6)

1. **Who builds `integrations/datastores/postgres`.** Portability P1 claims
   it; jobs phase 6 provisionally also budgets it ("coordinate — one of the
   two plans must own it," jobs open question). **Proposed resolution:
   portability P1 owns it, unconditionally**; jobs' phase 6 is struck and
   its phase 7 (`stores/postgres`) gains a dependency on P1. Rationale:
   portability lands first or concurrent with auth-v1 either way, and its
   §3 connector-surface spec is the design of record. → R2.
2. **Memory-store placement — three plans, three answers.** Portability DP2:
   reference impl lives *inside* `features/<name>/storetest` (test-scoped;
   a `stores/memory` module is rejected). Jobs J9: `features/jobs/stores/
   memory` as a real module, because it is load-bearing twice (conformance
   reference AND the proof host's backing — a lease-respecting concurrent
   queue is too much code to duplicate example-locally). Events §8:
   example-local in-memory outbox (small enough to hand-write, cms
   precedent). These are genuinely in tension: DP2's rejection of a memory
   *module* collides with J9's need for an *importable, non-test-scoped*
   memory store. **Proposed resolution (new option, for ratification): a
   feature MAY ship its reference in-memory implementation as a public
   package inside the feature core module** (e.g. `features/jobs/memstore`,
   stdlib-only, G2-clean since it is not a `stores/*` module and carries no
   driver) **when the implementation is too substantial to duplicate
   example-locally; `storetest` then wraps it.** Simple features (cms,
   auth, events-outbox) keep DP2's test-scoped reference + example-local
   memstores; jobs qualifies for the in-core package. No `stores/memory`
   modules either way. → R3 (YOUR CALL).
3. **Conformance-suite naming.** Portability: `features/<name>/storetest`.
   Events: `features/events/outbox/outboxtest`. **Proposed resolution:
   standardize on `features/<name>/storetest`** (one suite package per
   feature, sub-runners per port set — jobs' `RunQueue`/`RunSchedules`
   shape generalizes; events renames `outboxtest` → `storetest`). → R4.
4. **The `sdk/repository` transaction gap has a finder but no owner-section
   yet.** Events §5 flags it to the portability plan ("Transactor question,
   urgent at third emitting feature"), but the portability plan — written
   concurrently — has no section receiving it. **Proposed resolution: on
   ratification, the portability plan gains an addendum recording the
   finding and the revisit trigger** (third durable emitter), so the
   flag has a home and doesn't evaporate. → R5.
5. **auth-v1 A2 vs everything.** A2 ("postgres OUT") is contradicted by the
   portability plan (DP8), by NOTES.md's own 2026-07-02 ruling (which
   already said auth v1 "forces … integrations/datastores/postgres"), and
   by jrazmi's directive. Not really a conflict — a stale draft decision.
   The seven-edit amendment list in portability §8 is the fix. → R1.

## §5 Proposed milestone sequence (amends capability-map W4 only by inserting portability)

1. **datastore-portability** (P1–P4: pgx connector, cms storetest, cms
   postgres backfill if DP6 ratified, docs/charter sync). May run
   concurrent with auth-v1; auth's new postgres phase queues on P1.
2. **auth-v1, as amended** (7 phases: core+storetest, bcrypt, cms
   AdminMiddleware, proof host, stores/turso, stores/postgres, docs).
3. **jobs-v1** (sdk/workers, feature core, robfig-cron, memory+storetest,
   stores/turso, stores/postgres, proof host — pgx phase struck per §4.1).
4. **events-v1** (sdk/web SSE port, sdk/events, Mount.Events, feature core,
   cms emitter, stores×2, proof host, docs). Preconditions: auth-v1,
   sdk/workers, P1.
5. **telemetry** (sdk/tracing port + Noop; integrations/tracing/otel) —
   unchanged from W4; after the domain features exist.

## §6 Consolidated ratification list — ALL RATIFIED 2026-07-02 (R8 as amended by R2/R3)

**R1 — auth-v1 amendments (portability §8, DP8).** Override A2; add phase
07-auth-store-postgres; storetest into phase 1; proof-host memstore runs the
suite; turso phase runs the suite; docs + milestone acceptance updates.
*Recommended: ratify all seven edits; apply them to `.claude/plans/auth-v1/`
as a small follow-up task before the milestone loop starts.*

**R2 — pgx connector single owner (§4.1).** Portability P1 builds
`integrations/datastores/postgres`; jobs phase 6 struck. *Recommended: yes.*

**R3 — memory-store placement rule (§4.2).** Adopt the "in-core public
memstore package when substantial, test-scoped reference otherwise, never a
stores/memory module" rule; jobs qualifies. *Recommended: yes — reconciles
DP2 and J9 without exceptions-by-fiat. YOUR CALL because it amends DP2 as
written.*

**R4 — storetest naming standard (§4.3).** One `features/<name>/storetest`
package per feature, port-set sub-runners. *Recommended: yes.*

**R5 — transaction-gap ownership (§4.4).** Portability plan gains the
Transactor addendum; revisit trigger = third durable emitter. *Recommended:
yes.*

**R6 — taxonomy + degraded-mode charter additions (§1, §2).** ARCHITECTURE.md
"kinds of module" table; charter checklist item 12 (document each optional
port's nil semantics); both carried by portability P4. *Recommended: yes.*

**R7 — DP6: cms postgres backfill in the portability milestone.**
*Recommended: yes (portability §7's reasoning — the EAV spine is the
hardest parity proof; a policy that exempts the only shipped feature
governs nothing).*

**R8 — jobs plan defaults J1–J9.** J1 (postgres at v1, pgx via P1 per R2),
J2 (no naive cron parser; `Spec.Every` is the stdlib path), J3 (Mount.Jobs
designed, deferred), J9 (superseded by R3); J4–J8 are proposed defaults.
*Recommended: ratify as a block with R2/R3 applied.*

**R9 — events plan defaults O1–O8.** O1 (Mount.Events), O2 (outbox ports +
stores ship in events-v1, nothing wires durable mode), O3 (async memory bus
+ WithSync), O4 (keep tenant metadata vocabulary), O5 (keep package name,
alias), O6 (exact + `"*"` topics), O7 (MaxConnAge 15m), O8 (appender now,
Transactor per R5). *Recommended: ratify as a block.*

**R10 — milestone sequence (§5).** *Recommended: yes; the only change to
the ratified W4 order is inserting the portability milestone at the front.*

## Non-goals of this synthesis

- No code, no edits to auth-v1 files, no edits to the three sibling plans
  (their reconciliations land only after R1–R5 are ratified).
- No re-litigation of the sibling plans' internal proposed defaults beyond
  the conflicts named in §4.
- Review-agent passes (product-manager, architecture-steward, leads, sre,
  data-integration) are recommended per each sibling plan's own list —
  running them is a separate, post-ratification-scoping decision.
