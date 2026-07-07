# Datastore portability — features support multiple datastores out of the box

Status: **RATIFIED 2026-07-02 (jrazmi)** — DP1–DP8 all ratified (DP6: yes,
build cms/stores/postgres this milestone — R7; DP8: the auth-v1 amendments
are ratified R1 and have been APPLIED to `.claude/plans/auth-v1/`, incl. the
new `07-auth-store-postgres.md`), as amended by `00-intersections.md` R3
(memory-store placement rule: DP2 stands for simple features; a feature MAY
ship an in-core public memstore package when the implementation is
substantial and host-usable — jobs qualifies) and extended by R5 (§11
addendum below).
Date: 2026-07-02
Scope: cross-cutting policy (roadmap) + one execution milestone. This is THE
storage-story reference for every feature plan: auth-v1 amendments (§8), jobs,
events, and future features all cite the numbered sections below (§9 maps who
consumes what).

## Context

jrazmi's directive (2026-07-02): features must "support multiple data models,
e.g. turso OR pgx etc etc, out of the box. For other integrations, I think we
get away easier with interfaces — e.g. a cacher for a feature doesn't care if
it's redis/in-memory." Today exactly one feature store dialect exists
(`features/cms/stores/turso`), one datastore integration
(`integrations/datastores/turso`), and no mechanical way to prove a second
dialect implements a feature's ports identically. The constitution
(`.claude/plans/restructure/00-overview.md` rules 2, 4) already gives the
module shape — a datastore-free feature core plus sibling `stores/<dialect>`
modules — this plan supplies the policy (which dialects, when, enforced how)
and the missing infrastructure (the postgres connector, the per-feature
conformance suite).

Everything here respects the locked decisions: D4 (host-owned, pre-boot
migrations), G2 (feature cores never import integrations/stores), rule 2 (one
third-party lib per integration module), the frozen EAV spine, and the
workshop-v2 boundary (the crud Spec/Dialect engine is salvage for a future
codegen milestone — this plan builds none of it).

## Goal

Any host can mount any gopernicus feature on Turso/libSQL, Postgres, or an
in-memory store, with per-dialect behavioral parity proven by a shared
conformance suite rather than asserted — and every future feature ships that
way from its v1 milestone.

## Decision table

| # | Decision | Status | Where |
|---|---|---|---|
| DP1 | "Out of the box" = supported dialect set **{turso, postgres}** + an in-memory reference implementation, all passing the feature's conformance suite; parity is a **feature-v1-milestone-close gate** (sequencing inside the milestone is free) | **Proposed default** | §2 |
| DP2 | The in-memory implementation is a **reference impl co-located in `features/<name>/storetest`** (test-scoped); the host-side memstore (`examples/minimal` pattern) remains the charter's zero-infra proof; a `stores/memory` **module is rejected** (isolates no external dependency — rule 2) | **Proposed default** | §4 |
| DP3 | `integrations/datastores/postgres` mirrors turso's connector surface by **convention, not an sdk interface** (Open/Config/DB/MapError/Registrar/StatusCheck) | **Proposed default** | §3 |
| DP4 | SQL is **duplicated, hand-written per dialect module**; no shared dialect abstraction/DSL/codegen now (that is workshop v2); the conformance suite is the parity net | **Proposed default** | §5 |
| DP5 | Each `stores/<dialect>` owns dialect-specific migration SQL under the **same `MigrationSource.Name`**; invariant: the dialect trees of one feature share an **identical version (filename) set, gaps reproduced** | **Proposed default** | §6 |
| DP6 | `features/cms/stores/postgres` **is built, in this milestone** (EAV spine ported as representation, not redesign) | **RATIFIED 2026-07-02** (R7: yes, now) | §7 |
| DP7 | Postgres test infra: **skip-if-no-DSN** (loud `t.Skip` naming the missing env var), a documented one-line docker helper, and milestone-close live runs **recorded as dated NOTES.md artifacts**; testcontainers stays workshop-v2 scope (ratified YOUR CALL #9) | **Proposed default** | §4.3 |
| DP8 | auth-v1 decision **A2 is overridden** by DP1; auth-v1 gains a storetest work item and a `stores/postgres` phase, consuming (never building) this milestone's connector | **RATIFIED 2026-07-02** (R1) — all seven edits APPLIED to `.claude/plans/auth-v1/` same day | §8 |

## §1 The principle — where the interface line sits

Why datastores get per-dialect store modules while cacher/email/ratelimiter/
filestorage get one sdk port with swappable backends:

**A driven adapter needs a per-dialect store module when its implementation
owns durable schema the host must migrate, and a SQL dialect shapes its
observable contract** (error taxonomy from constraints, keyset ordering from
storage representation, uniqueness/FK behavior from DDL). Schema ownership
leaks through any interface: a host on Postgres must run Postgres DDL, carry
the pgx driver in its module graph, and operate that database — no port
signature hides that. So the unit of swap is a **module + migration set**, not
a config value.

**A facility stays one port with swappable backends when its state is opaque
to the host**: cacher moves bytes by key, email sends messages, ratelimiter
counts, filestorage stores blobs by key. No host-owned schema, no migration,
no dialect — the port plus a conformance suite (`cachertest`,
`filestoragetest`, `ratelimitertest`) fully specifies observable behavior, so
redis-vs-memory is invisible to the feature. This is jrazmi's "we get away
easier with interfaces," confirmed as policy: **feature `Config` fields typed
as sdk facility ports (`cacher.Storer`, `email.Sender`,
`ratelimiter.Limiter`, `filestorage.Storer`) are already multi-backend by
construction and need nothing from this plan.**

The litmus test: *if swapping the adapter changes what the host must migrate,
it's a store module per dialect; if the swap is invisible outside the process
boundary, it's a port.*

## §2 The policy (DP1) — charter amendment

Amend `features/README.md` (charter §3 rules + §8 checklist) with:

1. **Supported dialect set, v1: `{turso, postgres}`.** Every feature ships
   `stores/turso` and `stores/postgres`, each its own module (constitution
   rule 4), plus the in-memory reference implementation (§4/DP2). The set is
   a named, amendable list — a third dialect (e.g. a pure-embedded store)
   joins by amending this table, not ad hoc.
2. **Parity timing: milestone-close, not per-phase.** A feature's v1
   milestone is not done until all dialects in the set pass the feature's
   conformance suite (with the live-run artifact of DP7). Inside the
   milestone, turso-first (or memory-first) sequencing is fine — the gate is
   on declaring the feature done, because "out of the box" is a property of
   the shipped feature, not of any interim commit. Rationale for rejecting
   the softer "turso at v1, postgres before first tag": no tags exist or are
   scheduled (D8 deferred), so "before first tag" is an unbounded deferral —
   exactly the as-needed drift the directive countermands.
3. **The store-adapter surface is uniform across dialects.** Each
   `stores/<dialect>` module exports the same trio the turso precedent set
   (`features/cms/stores/turso/turso.go`): `Repositories(db) <name>.Repositories`,
   `ExportMigrations(dst string) error`, `Register(m feature.Mount, db) (…, error)`,
   plus `MigrationsFS`/`MigrationsDir`. A host switches dialect by switching
   one import and one `Open` call in `cmd`.
4. **Every port set ships a conformance suite** (`features/<name>/storetest`,
   §4) and every implementation — memory reference, host memstore, each
   dialect store — runs it.

Retroactivity: cms predates the policy; DP6 (§7) backfills it. auth-v1 is
in-flight; §8 amends it. Checklist items to add to `features/README.md` §8:
"10. A `storetest` conformance package exists and the reference in-memory
implementation passes it in the feature's own `go test ./...`. 11. Every
`stores/<dialect>` in the supported set exists and passes `storetest` (live
run recorded per DP7)."

## §3 `integrations/datastores/postgres` (DP3)

One new module wrapping exactly one third-party library:
`github.com/jackc/pgx/v5` (pool via `pgxpool`, same module). Scope — mirror
the turso connector's surface member-for-member so feature store modules look
symmetric across dialects:

| member | turso precedent | postgres shape |
|---|---|---|
| `Config` | URL/AuthToken/pool sizes/ConnectTimeout | DSN/pool sizes (MaxConns, MinConns, MaxLifetime, MaxIdleTime)/ConnectTimeout — salvage the original `pgxdb.Options` field set, drop its env tags (hosts use `sdk/config`), drop the functional-options layer and query tracers (D9's ruling: observability returns via `sdk/tracing`, not per-connector fields) |
| `Open(cfg) (*DB, error)` | opens + pings | opens pool + pings |
| `DB` | `Exec/Query/QueryRow/InTx/Close/Ping/Underlying` | same methods over `pgxpool.Pool` (`Underlying() *pgxpool.Pool`) |
| `MapError` | **substring** match on SQLite messages | **code-based** via `pgconn.PgError`: `23505`→`errs.ErrAlreadyExists`, `23503`→`errs.ErrInvalidReference` (both insert- and delete-direction, matching turso's undifferentiated mapping), `23514`/`23502`→`errs.ErrInvalidInput`, `pgx.ErrNoRows`→`errs.ErrNotFound` |
| `StatusCheck(ctx, db)` | 1s-deadline ping | same |
| `Registrar`/`NewRegistrar`/`Register`/`Apply` | implements `feature.MigrationRegistrar`; ledger `(source, version, checksum, raw_sql, applied_at)` | same table shape and apply semantics (one tx, lexical source order, checksum guard, forward-only); **omit the legacy-adoption path** (`_legacy` re-sourcing) — no pre-`(source,version)` Postgres databases exist; table introspection uses `to_regclass`/`information_schema`, not `sqlite_master`/PRAGMA |
| `RunMigrations` | legacy single-source wrapper | **omit** — no legacy callers |

**Stated non-guarantee:** connector symmetry is convention with no `make
guard` row. Nothing mechanically proves the two connectors' surfaces or
sentinel coverage stay aligned; the conformance suite (§4) is the only parity
net and it sees only port-reachable behavior. Named here so nobody over-trusts
it; a guard is a future option, not this milestone.

Not in scope for the connector: query builders, dialect helpers, ORM-ish
anything. It owns "how to talk to Postgres," never any feature's SQL.

## §4 Parity enforcement — the `storetest` conformance suite (DP2)

### 4.1 Shape

Per feature, one public test-support package in the feature core:
`features/<name>/storetest`. Modeled on `sdk/cacher/cachertest` (the
`Run(t, newImpl)` runner pattern), scaled up to a repository set:

```go
// features/cms/storetest (illustrative)
// NewRepositories must return a CLEAN, isolated cms.Repositories per call —
// SQL-backed harnesses truncate/clean via t.Cleanup; memory harnesses return
// a fresh instance.
func Run(t *testing.T, newRepos func(t *testing.T) cms.Repositories)
```

`Run` fans out per-port subtests (`Entries`, `Terms`, `Menus`, `Media`,
`Inquiries`). The contract cases per port, derived from the port doc comments
(the port comment is the spec; the suite is its executable form):

- CRUD round-trips; absent rows → `errs.ErrNotFound` (Get, Update, Delete).
- Uniqueness → `errs.ErrAlreadyExists` (e.g. entries `(type, slug)`, terms
  `(kind, slug)`, menu slugs).
- Referential behavior where ports promise it (entry↔term association,
  cascade on entry delete) → `errs.ErrInvalidReference` / observed cascade.
- Cursor pagination: full traversal across ≥2 page boundaries, no skipped or
  duplicated rows, stable `(created_at, id)` ordering, stale/empty cursor
  handling per `sdk/repository.DecodeCursor` semantics.
- **The timestamp-precision case (mandatory):** rows created with
  sub-microsecond `created_at` spacing, asserting pagination remains correct
  when the store truncates to its native precision (§5's precision trap).
  The suite must never assert nanosecond fidelity survives a round trip —
  assert ordering + identity, not exact stored precision.
- Domain-specific semantics the ports promise (e.g., for auth later:
  expired-at-read session behavior).

The suite constructs a **fully wired** `Repositories` (all ports), so
cross-table behavior (entry_terms FKs, cascades) is testable — not ports in
isolation.

### 4.2 Who runs it (three runners per feature)

1. **The reference in-memory implementation, inside `storetest` itself**
   (DP2). This is what lets the feature module self-verify: G2 forbids the
   core from importing `examples/` or its own `stores/`, so without an
   in-package implementation the feature's own `go test ./...` could compile
   the suite but never execute it. Stdlib-only, so G2/guard-sdk-stdlib
   posture is unchanged. It also absorbs the known drift risk: an in-memory
   store must hand-enforce the uniqueness/FK semantics SQL gives for free —
   the exact bug class already hit once (NOTES.md 2026-07-02: memstore's
   term/menu uniqueness had to be retro-fixed to match turso's constraints).
   One shared reference impl beats N host-local reimplementations of that
   discipline. Wrinkle, stated: `storetest` imports `testing`, so the
   reference impl is test-scoped — it is deliberately NOT a host-facing dev
   store. If jrazmi later wants a first-class "run this feature in memory"
   offering, that's a separate decision (a core `memory` package, not a
   module), out of scope here.
2. **Host memstores.** `examples/minimal/internal/memstore`'s tests add one
   `storetest.Run` call — the host's hand-written store keeps its pedagogical
   role and gains a drift net.
3. **Dialect store modules.** `stores/turso` and `stores/postgres` tests run
   the suite against a live database (gating below).

### 4.3 Test infrastructure (DP7) — the cheapest honest option

- **Postgres:** env-gated live tests — `POSTGRES_TEST_DSN` set → run against
  it (each `newRepos` truncates the feature's tables via `t.Cleanup`); unset
  → `t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")`.
  Loud skip message mandatory; a silent green that tested nothing is the
  false-green failure mode jrazmi's standing rules exist to prevent. Local
  runs use one documented line
  (`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=… postgres:17`),
  written into the store module's README and the Makefile comment.
  Testcontainers/harness automation is workshop-v2 scope (ratified YOUR CALL
  #9) — do not build it here.
- **Turso:** keep the existing `-tags=integration`, skip-without-`TURSO_*`
  pattern (`entries_integration_test.go` precedent), now running the shared
  suite instead of (not in addition to) bespoke per-store flows where they
  overlap.
- **`make check` stays hermetic** (skips both), so CI needs no databases. A
  new `make test-stores` target runs the store modules' tests expecting the
  env vars, failing loudly if absent.
- **The anti-false-green gate:** because `make check` can be green with zero
  dialect verification, milestone-close (any feature milestone under DP1)
  requires one recorded live conformance run per dialect — a dated entry in
  `NOTES.md` in the existing "LIVE-VERIFIED" style, naming the suite, the
  dialect, the DSN class (local docker vs real Turso), and the result. An
  artifact, not a verbal claim.

### 4.4 Guards / make

`storetest` lives in the feature core, so G2 already polices it (it can never
import a driver without failing the guard — this is load-bearing, not
incidental). Makefile `MODULES` grows per new module; no new guard targets
required this milestone (§3's non-guarantee noted).

## §5 SQL sharing vs duplication (DP4)

**Duplicate hand-written SQL per dialect module.** No shared dialect
abstraction, no query builder, no codegen — the crud `Spec`/`Dialect` engine
in the original repo is workshop-v2 salvage (`05-workshop-v2-brief.md`), and
NOTES.md already ruled it "too generation-coupled" to carry at runtime. Two
dialects at cms's scale (~5 stores, ~30 statements each) is comfortably
hand-maintainable, and the conformance suite is the parity net that makes
duplication honest.

The enumerable dialect deltas (this table doubles as workshop v2's seed spec
for its future `Dialect` interface — the salvage this policy sets up):

| delta | turso / SQLite | postgres | parity consequence |
|---|---|---|---|
| placeholders | `?` | `$1..$n` | mechanical rewrite per statement |
| timestamps | fixed-width ns TEXT (`2006-01-02T15:04:05.000000000Z07:00`), **lexical** keyset ordering (NOTES.md's "single most subtle correctness detail") | `TIMESTAMPTZ`, **microsecond-truncated**, chronological ordering | **the precision trap**: memory holds ns, turso stores ns, postgres truncates to µs → equal-timestamp rows appear, the `(created_at, id)` PK tie-break becomes load-bearing, and a store that encodes cursors from the in-memory ns value instead of the stored value skips/duplicates boundary rows. §4.1's mandatory conformance case exists for exactly this |
| cursors | — | — | the codec is **shared** (`sdk/repository.EncodeCursor/DecodeCursor`, type-tagged time), not per-store; the only cross-store hazard is the precision row above |
| error surface | textual messages → substring `MapError` | `pgconn.PgError` codes | both normalize to the same `sdk/errs` sentinels — ports and the suite speak sentinels only |
| upsert / RETURNING | `ON CONFLICT` / `RETURNING` supported | same, minor syntax differences | low risk; keep statements dialect-idiomatic, don't contort for sameness |
| DDL types | TEXT/INTEGER affinity | native `BOOLEAN`, `TIMESTAMPTZ`, `JSONB` available | representation may differ; **structure may not** (§7's EAV rule) |
| ledger introspection | `sqlite_master` + `PRAGMA table_info` | `to_regclass` / `information_schema` | connector-internal (§3) |
| recursive CTEs (future: page trees, ReBAC) | supported | supported | no blocker; syntax kept per-dialect |

**Stated invariant (discipline, not a guarantee):** the dialect stores of one
feature implement identical port semantics — same sentinel for the same
violation, same ordering, same page shape. The suite checks port-reachable
behavior only; a dropped index or an unexercised CHECK constraint passes
silently. Schema-shape parity is review discipline (`data-integration-reviewer`
territory), not something the green proves.

## §6 Migrations across dialects (DP5)

Confirming D4, amended with the cross-dialect invariant:

- Each `stores/<dialect>` owns its dialect's canonical migration SQL
  (`migrations/*.sql`, embedded) and exports `ExportMigrations` — the
  scaffold-and-own path is unchanged; hosts apply pre-boot with their own
  runner; the framework never migrates at startup.
- **Same `MigrationSource.Name` across a feature's dialects** (`"cms"` for
  both cms stores; `"auth"` for both auth stores). No collision is possible:
  the ledger lives in the database, and a database has exactly one dialect.
  The name identifies the feature, not the dialect.
- **The version-set invariant:** the dialect trees of one feature carry an
  **identical version (filename) set — gaps reproduced.** cms/turso runs
  0009–0021 with 0011/0012 absent; a cms/postgres tree mirrors those exact
  filenames (content differs per dialect). New features (auth onward) start
  both trees at 0001. Same filename = same logical schema step, so "database
  at cms version 0018" means the same thing on either dialect, and the ledger
  rows are comparable across a host's environments regardless of dialect.
- A fresh database migrated by dialect D's full set yields the same logical
  schema (in D's idiom) as any other dialect's full set — verified by the
  conformance suite behaviorally, plus §5's stated review-discipline caveat.
- Never edit an applied migration (checksum-guarded); a schema change is a
  new version number added to **every** dialect tree in the same change —
  a store PR touching one tree's `migrations/` but not its siblings' is
  incomplete by definition.

## §7 cms backfill (DP6 — YOUR CALL, recommended: build it, this milestone)

**Recommendation: yes — `features/cms/stores/postgres` is built in this
milestone (phase P3).** Reasons: (a) cms is the only shipped feature, so a
policy that exempts it is a policy that governs nothing yet; (b) the EAV
spine is the hardest SQL this policy will meet for a while (multi-table tx
writes, field replace-on-update, term joins, keyset pagination) — proving the
conformance suite catches dialect drift on the hardest case is worth more
than proving it on auth's five simple tables; (c) it makes this milestone
self-contained: policy + connector + suite + one full worked parity example,
which is exactly what jobs/events/auth cite.

The counter-case, stated fairly: no current host needs cms-on-postgres
(`examples/cms` is turso; `examples/minimal` is memory), so this is ~an L of
work with no immediate consumer — deferring it to "first postgres host"
would be the old as-needed posture. jrazmi's directive is precisely that
out-of-the-box beats as-needed, hence the recommendation. **YOUR CALL.**

Port rules for the backfill:

- **Representation may change; structure may not.** `TIMESTAMPTZ` for
  timestamps: yes. `BOOLEAN` where turso used INTEGER: yes. But
  `entry_fields.value` as `JSONB`, typed value columns, or any reshaping of
  `entries`/`entry_fields` is a **spine redesign the frozen-tables rule
  forbids** — the Registry model's premise is that adding a type/field is
  zero-migration, and the spine changes shape only on framework upgrade.
  Port the schema, don't "improve" it.
- Filenames mirror turso's 0009–0021 (gaps at 0011/0012 reproduced), per §6.
- No new example host. `examples/cms` stays on turso; the live conformance
  run against local dockerized Postgres (§4.3) is P3's real-interaction
  check. A postgres example host is a non-goal until a real host wants one.

## §8 auth-v1 amendments (DP8 — ratified R1; edits APPLIED 2026-07-02, incl. the new `07-auth-store-postgres.md` phase file)

If DP1 is ratified, the charter overrides A2 — not merely contradicts it.
Note for the record: A2 as drafted already sits in tension with NOTES.md's
own ratified 2026-07-02 ruling, which says the as-needed order "stands: auth
v1 first, which forces integrations/cryptids/bcrypt **and
integrations/datastores/postgres**." The precise edits to
`.claude/plans/auth-v1/`, for jrazmi to ratify and someone to apply:

1. **`00-overview.md` A2** — replace with: "`integrations/datastores/postgres`
   is built by the datastore-portability milestone (P1), never by auth-v1;
   `features/auth/stores/postgres` is IN this milestone per the DP1 charter
   rule (dialect parity gates milestone close)." Sequencing statement: auth's
   postgres phase depends on datastore-portability P1 having landed; if the
   milestones run concurrently, auth phases 1–6 proceed regardless and the
   new postgres phase queues on P1.
2. **`00-overview.md` phase table** — add phase 7:
   `07-auth-store-postgres.md` — `features/auth/stores/postgres`: SQL for the
   five v1 ports + migrations mirroring turso's version set (from 0001,
   identical filenames), `Repositories`/`ExportMigrations`/`Register`,
   env-gated live conformance run (`POSTGRES_TEST_DSN`), executor model opus.
   Depends on phase 1 + datastore-portability P1. Phase 6 (docs-sync) moves
   after it or gains a follow-up item.
3. **`01-auth-core.md`** — add a work item: `features/auth/storetest` package
   (suite over the five ports + the reference in-memory implementation, per
   §4; includes the session expired-at-read case and the §4.1 mandatory
   timestamp-precision pagination case). Ports and suite are co-designed —
   the suite is the port doc comments made executable. Note: `testing` is
   stdlib, so the milestone acceptance line "`features/auth/go.mod` requires
   exactly `gopernicus/sdk`" still holds.
4. **`04-proof-host.md`** — the proof host's auth memstore runs
   `storetest.Run` in its tests (replacing the bespoke
   uniqueness/honesty-assertion items with the suite, which subsumes them).
5. **`05-auth-store-turso.md`** — acceptance adds: the store passes
   `storetest.Run` under the existing `-tags=integration`/`TURSO_*` gating;
   work item 5's bespoke integration flow becomes the suite plus any
   turso-specific extras.
6. **`06-docs-sync.md`** — `features/auth/README.md` documents the supported
   dialect set; Makefile `MODULES` gains the new module(s); charter checklist
   items 10–11 (§2) verified for auth.
7. **Milestone acceptance (`00-overview.md`)** — add: recorded live
   conformance run per dialect (turso + postgres) as dated NOTES.md artifacts
   (§4.3), alongside the existing (a)/(b) real-interaction checks.

## §8b Addendum (ratified R5, 2026-07-02) — the `sdk/repository` transaction gap

Finding routed here by `roadmap/events-feature-design.md` §5 (its
consultation verified it in code): **`sdk/repository` has zero transaction
vocabulary**, and no mechanism exists for two store modules to share one
transaction. The events design's v1 answer is the dialect-typed appender
(the emitting store declares `AppendTx(ctx, <dialect>.Tx, ...recs)`;
the events store satisfies it structurally; the integration's Tx type is
the shared vocabulary — no store→store import). Its costs are priced in
that design: unguarded per-feature × per-dialect glue, and cross-source
migration ordering mitigated by a boot-time table probe.

**This plan now owns the follow-up question**: whether a generic seam (a
`Transactor` port in `sdk/repository`, or a context-carried transaction
handle mirroring `sdk/logging`'s request-ID pattern) should replace the
appender boilerplate. **Revisit trigger, ratified: the third emitting
feature that wants the durable outbox path** — not before (two emitters ×
two dialects is tolerable hand-rolled glue), and not open-endedly after
(at three, the unguarded pattern becomes the ecosystem's top drift risk).
Whoever hits the trigger writes the Transactor design as an amendment to
this plan.

## §9 Dependencies & intersections (what other plans consume from here)

| consumer | consumes | section |
|---|---|---|
| **auth-v1** | A2 override, phase additions, storetest work item, sequencing on P1 | §8 |
| **jobs plan** | DP1 (ships `stores/{turso,postgres}` + storetest at v1); its `ClaimDue` compare-and-set semantics become storetest cases — CAS/claim-contention is exactly the port-promised, dialect-sensitive behavior the suite exists for; the cron-parser port is facility-side of §1's line (unaffected by this plan) | §1, §2, §4 |
| **events plan** | DP1 for the outbox store (`stores/{turso,postgres}` + storetest, at-least-once/poll-claim semantics as suite cases); the event **bus** port + memory default is facility-side of §1's line — redis backend is a swappable integration, NOT a store module | §1, §2, §4 |
| **workshop v2** | §5's dialect-delta table as the seed spec for its `Dialect` interface; each feature's two hand-written dialect trees as golden references; each feature's storetest suite as the generator's acceptance tests (generated stores must pass the same suite hand-written ones do) | §5, §4 |
| **future integrations (redis cacher/ratelimiter, gcs/s3, sendgrid)** | §1's line: all facility-side — one sdk port, swappable backends, existing `*test` conformance suites; nothing here changes their story | §1 |
| **RELEASING.md** | two new taggable modules this milestone (`integrations/datastores/postgres`, `features/cms/stores/postgres`), later `features/auth/stores/postgres`; store modules' `replace` directives stay workspace-dev-only per C4 | §3, §7 |

## §10 Phases

Execute in order except where noted; each phase gets its own file under
`.claude/plans/datastore-portability/` when this roadmap is ratified (this
document is the design; phase files are the dispatch units).

| P | What | Size | Depends on | Acceptance |
|---|---|---|---|---|
| P1 | **`integrations/datastores/postgres`** per §3: module scaffold (go.work + MODULES), Config/Open/DB/MapError/StatusCheck/Registrar. Unit tests hermetic (MapError via constructed `pgconn.PgError` values, config validation, registrar dup-source); live ping/migrate test env-gated per §4.3 | M | — | module builds/vets/tests standalone; `make check` green with module included; `MapError` unit-tested for all four codes + ErrNoRows; live test skips loudly without DSN |
| P2 | **`features/cms/storetest`** per §4: suite over all five ports incl. the mandatory precision-collision pagination case; reference in-memory `Repositories` passing it in cms's own `go test`; `examples/minimal` memstore tests call `storetest.Run`; `stores/turso` runs the suite under existing integration gating | M–L | — (parallel with P1) | cms core `go test ./...` executes the suite against the reference impl; memstore passes unmodified or divergences fixed/flagged; guard G2 still green (suite imports no drivers) |
| P3 | **`features/cms/stores/postgres`** per §7: migrations mirroring 0009–0021 (gaps reproduced), five stores (spine tx-writes, fields replace, term joins, keyset pagination on TIMESTAMPTZ with PK tie-break), `Repositories`/`ExportMigrations`/`Register` trio, README with the docker one-liner | L | P1 + P2 | suite passes against local dockerized Postgres (recorded NOTES.md artifact); `make check` green (hermetic skip is loud); `make test-stores` target added and passing with DSNs |
| P4 | **Docs + policy sync**: `features/README.md` charter amendments (§2 rules, checklist items 10–11), `ARCHITECTURE.md` (dialect-set sentence + §1's line where the port table lives), `RELEASING.md` module list, Makefile MODULES/test-stores comments, NOTES.md dated entry recording the milestone's live runs | S | P1–P3 | `make check` green; docs match the shipped tree (no aspirational claims); auth-v1 amendment list (§8) handed to jrazmi for ratification — NOT applied by this milestone's executor |

Real-interaction check, every phase: the standing no-regression check
(`make check`; boot `examples/minimal` on :8081; `GET /` and
`GET /products/widget-3000` → 200s) — plus P3's live-Postgres conformance run
as its own gate.

## Non-goals

- **No workshop-v2 codegen, no dialect DSL, no ORM** — §5; the Spec/Dialect
  engine stays salvage.
- **No sdk "database port"** — the connector symmetry is convention (§3);
  sdk stays free of any SQL vocabulary beyond `sdk/repository`'s
  cursor/page shapes.
- **No MySQL/other dialects** — the set is {turso, postgres} until amended.
- **No postgres example host** — §7; live conformance is the proof.
- **No testcontainers harness** — workshop-v2 (ratified YOUR CALL #9).
- **No host-facing in-memory dev store** — DP2's reference impl is
  test-scoped; promoting it is a separate future decision.
- ~~No editing of `.claude/plans/auth-v1/*`~~ — superseded: ratified R1's
  edits were applied 2026-07-02 (§8's status line).

## Risks

1. **Timestamp-precision parity (highest).** Three precisions (memory ns,
   turso ns-TEXT, postgres µs) make the keyset boundary the one place
   parity genuinely breaks; a store encoding cursors from in-memory values
   instead of stored values skips/duplicates rows. Mitigated by §4.1's
   mandatory collision case — the suite must be written to catch it, or the
   whole parity net has a hole exactly where the drift is likeliest.
2. **False greens between milestone closes.** `make check` is hermetic;
   postgres conformance can silently regress until the next live run.
   Mitigated by loud skips, `make test-stores`, and the NOTES.md artifact
   rule — accepted residual risk until workshop-v2's harness automates it.
3. **Schema-shape drift the suite can't see.** Indexes/CHECKs diverging
   across dialects pass behavioral conformance. Accepted as review
   discipline (§5 invariant caveat; `data-integration-reviewer` on every
   store PR).
4. **Charter scope tax on future features.** DP1 makes every feature v1
   milestone carry two SQL trees + a suite (~+1 phase each for jobs/events).
   That is the deliberate price of "out of the box"; called out so nobody
   mistakes it for accidental scope creep.

## Consultation notes

`lead-backend-engineer` reviewed the sketch (verdict: ship-with-edits); all
material findings are folded in: the timestamp-precision trap promoted to
risk #1 and a mandatory suite case (§4.1, §5); the in-memory placement
resolved on principle (reference impl inside `storetest`, since G2 otherwise
makes the feature module unable to execute its own suite, and a
`stores/memory` module would violate rule 2's earn-your-module test — DP2);
the auth-v1 double-booking resolved with an explicit "P1 builds the
connector, auth consumes it" sequencing (§8.1); loud skips + recorded
NOTES.md artifacts against false greens (§4.3); the cursor codec correctly
described as shared, not store-opaque (§5); the version-set invariant
restated as "identical set, gaps reproduced" — cms's turso tree runs
0009–0021 with 0011/0012 absent, so "start at 0001" was false for the
precedent (§6); connector symmetry's no-guard status stated as an explicit
non-guarantee (§3); the EAV representation-vs-structure boundary called out
for the P3 executor (§7); postgres `23503` maps undifferentiated
(insert/delete) to `ErrInvalidReference`, matching turso (§3).

## Open questions

- ~~DP6 and DP8 are the two YOUR CALLs~~ — both RATIFIED 2026-07-02 (R7,
  R1); no open decisions remain in this plan.
- Minor, for P2's author: whether the turso store's existing bespoke
  integration test is fully subsumed by the suite or keeps turso-specific
  extras (e.g. libsql error-string coverage). Implementer's call, flagged
  in the phase file.

## Recommended reviews

- **product-manager** — scope/sequencing: is the DP1 per-feature tax and the
  cms backfill (DP6) the right spend now, concurrent with auth-v1?
- **lead-backend-engineer** — consulted pre-writing (notes above); re-review
  of the final §4 suite contract welcome.
- **data-integration-reviewer** — §5/§6/§7: dialect deltas, migration
  invariant, EAV port rules.
- **platform-sre** — §4.3 test-infra gating, DP7's artifact rule, RELEASING
  implications of two new modules.
- **architecture-steward** — §1's boundary principle and DP2's placement
  against ARCHITECTURE.md/charter.
