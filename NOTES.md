# cms v0.1 — proving-ground decision log

What repurposed cleanly from gopernicus vs. what needed adaptation, captured to
feed the gopernicus restructuring. Plan: `.claude/plans/v0.1-cms.md`.

## Repurposed cleanly (import-rewrite only)

- **`sdk/environment` → `sol/config`** — stdlib-only `.env` loader. Renamed
  package; otherwise verbatim. Tests ported as-is.
- **`sdk/logger` → `sol/logging`** — `slog` setup. Dropped trace/span context
  keys; kept request-id injection (the only key the request-id middleware
  needs). `TracingHandler` now injects `request_id` only.
- **`sdk/errs` → `sol/errs`** — sentinels + `IsExpected`. Verbatim.
- **`sdk/web/handler.go` → `sol/web/handler.go`** — `WebHandler` over
  `ServeMux`. Dropped the CORS/default-header options (unseen middleware, not
  needed for SSR). Added empty-method support so `/{$}` patterns register.
- **`sdk/web/errors.go` → `sol/web/errors.go`** — kept the status map,
  `ErrFromDomain`, and `FieldErrors` (forms reuse them). Dropped the JSON-decode
  `ErrValidation`/`MaxBytesError` path.
- **`sdk/fop` cursor/pagination → `sol/repository/cursor.go`** — pure algorithms.
  Trimmed the pointer-type cursor tags (nullable generated columns don't exist
  here). `TrimPage` now returns a `Page[T]` directly instead of a separate
  `Pagination` struct.

## Needed real adaptation

- **`moderncdb` → `sol/sqldb`** — **driver swap** to the pure-Go libSQL remote
  client (`github.com/tursodatabase/libsql-client-go/libsql`); DSN is
  `url + "?authToken=" + token`. Removed all SQLite pragma/WAL/`file:` DSN
  logic and the OTEL tracer plumbing. Error mapping now targets `sol/errs`
  sentinels directly (was package-local `ErrDuplicateEntry`/etc.).
- **Migrations runner** — dropped the `BEGIN IMMEDIATE`-on-pinned-`*sql.Conn`
  lock. That is SQLite-local single-file concurrency control; against a remote
  libSQL endpoint it's both unavailable and unnecessary. Replaced with a plain
  `InTx`. Dropped `BeginImmediate` from the tx helpers.
- **Timestamp storage (new)** — `created_at` is stored TEXT with a **fixed-width**
  layout (`2006-01-02T15:04:05.000000000Z07:00`) so it sorts lexicographically
  for keyset pagination. `time.RFC3339Nano` trims trailing fractional zeros and
  would break ordering — the single most subtle correctness detail in the data
  layer.
- **Server run loop** — `sdk/web/server.go`'s config/types went to
  `sol/web/server.go` (`ServerConfig`, `HTTPServer`); the ListenAndServe +
  graceful-shutdown loop is hand-written in `delivery/http/server.go`
  (decision B-4).

## Deliberately NOT copied

- **`infrastructure/database/crud/*`** — the codegen runtime (Spec/Dialect/
  render/scan). Too generation-coupled. v0.1 hand-writes SQL in
  `providers/turso`; `sol/repository` is a minimal contract, not an engine.
- **`httpmid/{authenticate,authorize,tenant,rate_limit,client_info}`** — auth/
  tenancy/rate-limit, all out of v0.1 scope. The request logger lost its
  `GetClientIP` auth-context dependency and reads `r.RemoteAddr`.

## Hard rule (overrides ratified B-1): sol imports stdlib only

`sol` is the adapter between the standard library and the app — it imports
**only** the standard library and other `sol` packages, never an external
module. This is stronger than plan decision **B-1** (which let `sol/web` import
`templ`); B-1 is **overridden**. Consequences:

- **`sol/web` render seam** — `sol/web.Render` takes a local `Renderer`
  interface (`Render(context.Context, io.Writer) error`), not `templ.Component`.
  `templ.Component` satisfies it implicitly, so concrete views still plug in with
  no `templ` import in `sol`.
- **`sol/sqldb` is a generic `database/sql` wrapper** — it takes a driver name +
  DSN and an optional `ErrorMapper`; it imports no driver. The libSQL driver
  blank-import, the `?authToken=` DSN, the `"libsql"` driver name, and the
  SQLite constraint-string→sentinel mapping all live in `providers/turso`
  (`turso.Open`, `turso.mapError`). `cmd` calls `turso.Open`, then
  `sqldb.RunMigrations` on the returned generic `*sqldb.DB`.
- Enforced by a guard in `make check`: `sol/` may not import
  `github.com|cloud.google.com|golang.org/x|gopkg.in`.

## New, hand-written

- `sol/id` — `crypto/rand` 128-bit base32 IDs (decision B-2; no UUID/ULID dep).
- `sol/web/render.go` — the `templ.Component` render seam (decision B-1).
- `sol/web/middleware.go` `RequestID` — `crypto/rand` request-id propagation
  (not the OTEL telemetry middleware).
- `logic/domains/content` — entity/behavior, `ArticleRepository` port,
  `ContentService`.
- `delivery/http` package is named `http`; it imports `net/http` (no clash —
  the package's own name is not an in-scope identifier). cmd imports it as
  `deliveryhttp`.

## Connectors vs providers (post-v0.1 restructuring)

The third-party DB plumbing moved OUT of `sol` entirely:
- **`connectors/datastores/turso`** — the reusable Turso/libSQL connector
  (connection, tx, migrations runner, error mapping). "How to talk to Turso,"
  no app queries. Destined to become its own module.
- **`providers/datastores/turso/articles.go`** — the APP-SPECIFIC `ArticleStore`
  (the article SQL + schema), consuming the connector as `tursodb`.
- Naming: **`connectors/`**, not `packages/` ("package" is redundant/noise in Go
  and brushes `golang.org/x/tools/go/packages`). Both connectors and providers
  are grouped by capability (`datastores/`, …).
- **Module-split fork (still open):** making the connector its own module while
  it imports `cms/sol/errs` creates a `cms ↔ connector` cycle. Resolve by either
  (a) extracting `sol` to its own module first (kernel-first), or (b) making the
  connector `sol`-free (expose constraint predicates; app owns sentinel
  mapping). In-module today, so no cycle yet.

## Verification status (v0.1) — LIVE-VERIFIED

All green, including against live Turso (a `.env` with real creds was supplied):
- All unit/handler tests green (`go test ./...`).
- **Live integration test passes** (`go test -tags=integration
  ./providers/datastores/turso/...`): create→get→get-by-slug→list(paginated
  across a boundary)→edit→re-get on the real DB; **Risk R-2 CLOSED** — UNIQUE→
  `ErrAlreadyExists`, missing→`ErrNotFound` map correctly off the libSQL client's
  error strings.
- **Real binary against live Turso**: `go run ./cmd/server` boots, runs
  migrations (idempotent — re-run is a no-op), serves the full SSR flow
  (create 303 → view reads back → list → edit 303 → re-view reflects edit).
- Graceful shutdown drains an in-flight request and returns cleanly
  (`TestRun_GracefulShutdown`). Note: `go run` doesn't forward SIGINT to its
  child, so live Ctrl-C shutdown is best driven against the built binary, not
  `go run`.

## v0.2 — A Real CMS (built on top of v0.1)

Plan: `.claude/plans/v0.2-cms.md`. Built and live-verified against real Turso,
phase by phase:

- **Content model:** `Article` → `Post` (excerpt, author, publishedAt,
  publish/unpublish) + hierarchical `Page` (tree, template). `Slugify` lifted to
  `sol/slug` (pure algorithm) so domains share it without cross-domain imports.
- **New domains** (independent peers under `logic/domains/`): `taxonomy`
  (categories + tags, per-kind slug uniqueness; post↔term join via `post_terms`),
  `menus` (named menus + nestable items), `media` (`Asset` + storage-key gen over
  a `BlobStore` seam), `messaging` (contact `Inquiry`).
- **New sol facility ports:** `sol/email` (`Sender`/`Message`). Wired the dormant
  `sol/filestorage` and `sol/cacher`.
- **New remotes (reusable connectors):** `filestores/disk` (stdlib `os`),
  `email/{smtp,console}` (stdlib `net/smtp` + a dev logger), `caches/memory`
  (in-process TTL). Each implements a `sol` port; redis/gcs/SaaS are drop-in
  peers (deferred — need infra).
- **Inbound:** split into an admin CRUD surface and a themed **public site**
  (`/`, `/blog`, `/blog/{slug}`, `/page/{slug}`, taxonomy archives, `/contact`).
  Markdown is a view concern — rendered with `goldmark` + sanitized by
  `bluemonday` in `inbound/http/views/markdown.go` (third-party allowed in
  inbound, never in sol/logic). Public pages are render-cached (`sol/web.CachePages`).

**Deferred to v0.3 (need infra or a real cross-domain trigger):** auth/multi-user,
redis/gcs/SaaS backends, `sol/queue`, content revisions, full-text search,
comments, the `publishing` composition (cache-busting is TTL-based for now).

**Smell noted:** `inbound/http.BuildRouter` now takes 8 positional deps — worth a
`Deps` struct when convenient.

## Post-v0.2 restructure — Go-convention layout (sol→sdk, logic→sol, internal/, integrations/)

Renamed/relocated to standard Go project layout. The names flipped: the
*framework kernel* (was `sol`) is now **`sdk`**; the *app hexagon center* (was
`logic`) is now **`sol`**, living under `internal/`.

| was | now |
|---|---|
| `sol/` (kernel) | `sdk/` |
| `logic/` (domains) | `internal/sol/` |
| `inbound/` | `internal/inbound/` |
| `outbound/` | `internal/outbound/` |
| `remotes/<cat>/<tech>` | `integrations/<cat>/<tech>` (dropped the `remotes` segment) |

`cmd/` unchanged. Go's `internal/` enforces app-privacy on the hexagon + its
adapters; `integrations/` holds the reusable connectors; `sdk/` stays the stdlib-only
leaf. Guards updated: inward layers are now `sdk/` + `internal/sol/`; the
adapter layers they must not import are `internal/inbound`, `internal/outbound`,
`integrations`. All tests + 3 guards green; live binary re-verified end-to-end.

## 2026-07 — features extraction (retro-recorded)

**This section is reconstructed after the fact** — the repo advanced two
restructurings past the last dated entry above before its written record
caught up (gopernicus restructure milestone, phase 1). No decision log entry
tracked the extraction as it happened; this entry exists so the history isn't
silently lost. The referenced `v0.1-cms.md` / `v0.2-cms.md` plan files are
**not in this repo** — do not expect to open them.

- **The hexagon moved out of the app.** `examples/cms/internal/sol` (domains +
  compositions, per the "Post-v0.2 restructure" entry above) was extracted
  into a standalone module, `features/cms`: public packages (`content`,
  `taxonomy`, `menus`, `media`, `messaging`, `theme`) carry ports + entities;
  `features/cms/internal/*` carries services and the `templ`-rendered HTTP
  layer. `examples/cms` became a thin host: `cmd/server` (composition root),
  `internal/theme` (view overrides), `workshop/migrations` (scaffolded SQL).
- **The store SQL moved with it, into its own module.** `features/cms/stores/turso`
  is a sibling module supplying the libSQL repositories + migrations for the
  feature — datastore-free at the feature core, so a host bringing a
  different datastore never pulls libsql into its build.
- **`sdk/feature` was introduced** — the host↔feature contract: `Mount`
  (`Router` / `Logger`) and `RouteRegistrar`. No `init()` registration, no
  service locator — a feature is reached only through this narrow surface plus
  its own `Register(mount, repos, cfg)`. Migrations are host-owned.
- **`examples/minimal` was added as the opt-out proof** — a second host that
  mounts the same `features/cms` feature over an in-memory store
  (`internal/memstore`), with zero libsql in its module graph. It demonstrates
  the store-adapter split actually decouples the feature from any one
  datastore.
- **Decision D5: the app-hexagon directory name is now `internal/core`.**
  The `sol` name is retired — "Sol" collided with an OpenAI model name. No app
  in this repo currently instantiates an app-local hexagon (both examples are
  thin hosts around `features/cms`), so `internal/core` exists only in docs
  today; it takes effect in code the next time a host builds domains of its
  own beyond a mounted feature. Historical entries above that say `internal/sol`
  describe the repo as it was at the time and are left as written.

## 2026-07-02 — D5 amended: the app hexagon is `internal/logic`

Same-day amendment to D5 (which had renamed `sol` → `core`): the hexagon
directory is **`internal/logic`**, aligning with the convention jrazmi settled
in `gps/gps-360` (`src/internal/{inbound,logic,outbound}`, hexagon split as
`logic/{domains,compositions}`). One convention across the ecosystem; `logic`
also avoids echoing the original gopernicus's flawed `core/` layer. As with the
previous rename, zero code exists under any of these names in this repo — the
change is documentation-only until an app/scaffold next creates a hexagon.

## 2026-07-02 — post-milestone rulings

- **Capability-map YOUR CALL rows 1–9: all ratified to their recommended
  defaults** (ReBAC deferred past auth v1; tenancy folds into auth v2+; event
  bus not built until a second real consumer; sdk/tracing port + Noop default
  with integrations/tracing/otel; jobs' cron parsing via port +
  integrations/scheduling/robfig-cron; conversion/fop ported piecemeal as
  needed; integration-test harness is workshop v2 scope).
- **Three phase-2 findings fixed**: `email.NewConsole(nil)` now discards via
  `io.Discard` (was a nil-writer panic); memstore `termRepo.Create` enforces
  `(kind,slug)` uniqueness and `menuRepo.CreateMenu` enforces slug uniqueness,
  both returning `errs.ErrAlreadyExists`, matching their port doc comments and
  the turso store's SQL constraints. Tests updated from divergence-documenting
  to contract-asserting. Verified: full `make check` + live boot of
  examples/minimal (200s, no seed collisions).
- **Integration porting strategy**: as-needed, not a dedicated milestone —
  each integration is built when it becomes a real blocker for a real host
  (capability-map W4 order stands: auth v1 first, which forces
  integrations/cryptids/bcrypt and integrations/datastores/postgres).

## 2026-07-02 — datastore-portability P1: postgres connector LIVE-VERIFIED

`integrations/datastores/postgres` (pgx/v5) shipped — the 7th module.
LIVE-VERIFIED same day: env-gated live test against local dockerized
postgres:17 (DSN class: local docker, port 55432) — Open/ping, migration
apply (`0001_init.sql`), checksum-guarded re-apply no-op — all passed.
Hermetic `make check` stays green with a loud skip when `POSTGRES_TEST_DSN`
is unset. Ledger/apply semantics mirror turso's; legacy-adoption path and
`RunMigrations` deliberately omitted (no legacy postgres databases exist).
Phase log: `.claude/plans/datastore-portability/01-postgres-connector.md`.

## 2026-07-02 — datastore-portability P2: storetest caught a live turso bug

`features/cms/storetest` (conformance suite + in-package reference impl)
shipped; run by three runners (reference, examples/minimal memstore, turso
store's `-tags=integration` leg). First session out, it exposed: (1) turso
`TermStore.Delete` deleting from `post_terms` — a table NO migration creates
(stale posts→entries rename); fixed to `entry_terms`, but the fix is
LIVE-UNVERIFIED until the milestone-close turso run (no TURSO_* creds in
env); (2) memstore entry pagination ignored cursors and lacked the id
tie-break — fixed against the shared codec. Phase log:
`.claude/plans/datastore-portability/02-cms-storetest.md`.

## 2026-07-02 — datastore-portability P3: cms×postgres conformance LIVE-VERIFIED

`features/cms/stores/postgres` shipped (9th module; migration filenames
identical to turso's 0009–0021 tree, gaps reproduced; EAV spine structure
unchanged). LIVE-VERIFIED twice same day (implementer run + independent
loop-leg re-run with -count=1): suite `features/cms/storetest`, dialect
postgres, DSN class local docker (postgres:17, :55432), result GREEN —
every subtest ran, including the mandatory timestamp-precision pagination
case (cursors encode from stored µs-truncated values, not in-memory ns).
`make test-stores` added. Outstanding for milestone close: cms×turso live
run (TURSO_* creds absent) + P4 docs sync. Phase log:
`.claude/plans/datastore-portability/03-cms-store-postgres.md`.

## 2026-07-02 — datastore-portability milestone CLOSED (one flag for jrazmi)

All four phases green: P1 postgres connector (LIVE-VERIFIED, local docker),
P2 cms storetest (caught the post_terms/entry_terms turso bug + memstore
cursor bug), P3 cms postgres store (LIVE-VERIFIED, local docker, precision
case passing), P4 docs/policy sync (charter items 10–12, ARCHITECTURE
taxonomy, RELEASING/Makefile; fresh-eyes clean). §4.3 close artifacts:
- cms×postgres: suite features/cms/storetest, dialect postgres, DSN class
  local docker (postgres:17), GREEN — twice (implementer + independent
  -count=1 re-run).
- cms×turso: suite features/cms/storetest against the REAL stores/turso
  store, dialect libsql/SQLite, DSN class **local file (libsql embedded
  driver, modernc sqlite)**, GREEN — full pass incl. the entry_terms fix.
  A real-REMOTE Turso run was deliberately NOT performed: the only creds
  available (.env) point at the examples/cms dev database and the suite
  truncates cms tables. **YOUR CALL (jrazmi): accept local-file as the
  turso DSN class, or provide a disposable Turso database for a remote run.**
Milestone declared closed with that single flag; auth-v1 is unblocked
either way (its phase 7 needed only P1).

## 2026-07-02 — turso close-gate artifact upgraded: REAL Turso, GREEN

jrazmi authorized truncating the playground database
libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io
(authorization is for THAT URL specifically — always verify the env's URL
matches it before a destructive run; the .env may point elsewhere in the
future). Ran `go test -tags=integration -count=1 -run TestConformance_Turso`
in features/cms/stores/turso against it: **PASS (76.12s — remote per-
statement round-trips, the documented turso-remote throughput ceiling)**.
The datastore-portability milestone's turso artifact is now: suite
features/cms/storetest, dialect turso, DSN class **real Turso (playground
db)**, result GREEN. The earlier local-file artifact stands as secondary
evidence. The milestone's single open flag is RESOLVED — closed clean, no
caveats.

## 2026-07-02 — auth-v1 phase 4: two-feature proof LIVE-VERIFIED (the acid test)

`examples/auth-cms` (11th module) mounts features/auth AND features/cms with
in-memory stores, zero libsql in its own module graph (GOWORK=off), auth
gating cms admin via `Config.AdminMiddleware`. The five-step cookie-jar flow
(b) passed live TWICE (implementer run + independent loop-leg re-run):
401 → register 201 → login 200+cookie → admin 200 → logout 200 → 401;
public home 200 sessionless throughout. Constitution rule 6 demonstrated
with two real features — neither imports the other (greps empty both
directions); the host's main is the only composition point. Phase log:
`.claude/plans/auth-v1/04-proof-host.md`.

## 2026-07-02 — auth-v1 phase 5: auth×turso conformance LIVE-VERIFIED

`features/auth/stores/turso` shipped (12th module; migrations 0001–0005,
source "auth", sibling to "cms" in the shared ledger). LIVE-VERIFIED twice
against the authorized playground Turso database (URL verified pre-run):
suite features/auth/storetest, dialect turso, DSN class real Turso
(playground), result GREEN — 16/16 leaf subtests, ~30s per run. Two
deliberate schema calls logged in the phase file: plain session tokens
(the port contract's opaque-token shape; hashing = v2 hardening) and no
enforced FKs on child tables (the suite exercises child ports without a
users row; connector doesn't enable PRAGMA foreign_keys). Phase log:
`.claude/plans/auth-v1/05-auth-store-turso.md`.

## 2026-07-02 — auth-v1 phase 7: auth×postgres conformance LIVE-VERIFIED

`features/auth/stores/postgres` shipped (13th module; migration filenames
identical to the turso tree; turso-parity structure incl. the plain-token
and no-FK decisions). LIVE-VERIFIED twice (implementer + independent
loop-leg -count=1 re-run): suite features/auth/storetest, dialect postgres,
DSN class local docker (postgres:17, :55432), result GREEN — 17/17 leaf
subtests. With phase 5's turso runs, BOTH auth dialects now pass one suite:
the ratified DP1 out-of-the-box guarantee holds for the second feature
built under it. Phase log: `.claude/plans/auth-v1/07-auth-store-postgres.md`.

## 2026-07-02 — feature-layout + store-posture rulings (jrazmi)

Mid-auth-v1 rulings from the layout/extensibility discussion:
1. **Trio re-layout RATIFIED** (`roadmap/feature-trio-relayout.md`):
   features wear the hexagon's names — public port layer at
   `logic/<domain>` (public by necessity: hosts/store modules import it
   across module boundaries), `internal/logic/<domain>svc` +
   `internal/inbound/http` interior, `stores/<dialect>` kept and
   documented as the outbound tier module-ized.
2. **`internal/` kept.** Extension model = deliberate seams (Config
   fields, registered data, structural ports), never interior reach-ins;
   a real need inside internal/ is the signal to add a seam
   (AdminMiddleware precedent).
3. **Store posture C.** Framework maintains dialect store modules as
   reference implementations; workshop v2's brief gains store scaffolding
   as a headline deliverable so hosts choose import-vs-own; migrations +
   storetest suites are the durable assets under any posture; features
   provably work with ZERO stores (the in-memory proof hosts are the
   standing evidence).

## 2026-07-02 — trio re-layout EXECUTED and LIVE-VERIFIED

Both features now wear the hexagon's names: `logic/<domain>` public rims,
`internal/logic/<domain>svc` + `internal/inbound/http` interiors,
`stores/<dialect>` as the outbound tier (names ratified L1/L2). All
intra-module moves; zero module-path changes. Verified post-move: make
check green (13 modules), stale-path greps zero, G2 prove-can-fail, ALL
FOUR live conformance legs green (cms+auth × postgres local docker,
cms+auth × turso playground), five-step auth flow (b) firsthand
401→201→200→200→200→401. Plan + log:
`.claude/plans/roadmap/feature-trio-relayout.md`.

## 2026-07-02 — auth-v1 milestone CLOSED

All seven phases green and live-verified: features/auth core (five ports,
rate-limit-first login, strict JSON decoding), integrations/cryptids/bcrypt,
cms.Config.AdminMiddleware (A3), examples/auth-cms (the rule-6 acid test —
five-step flow passed live repeatedly), stores/turso + stores/postgres
(both passing one storetest suite; live artifacts: turso playground ×2,
postgres docker ×2 per store), docs sync (auth README; charter trio
anatomy + app↔feature mapping table + extension-model + posture C; 13
modules across ARCHITECTURE/README/RELEASING; capability-map v1 rows
marked BUILT; fresh-eyes clean). Decisions: A1 separate proof host, A2 as
amended (postgres IN, connector consumed from portability P1), A3
AdminMiddleware, A4 G2 generalized (prove-can-fail ×2). Mid-milestone the
trio re-layout executed and is documented. Deferred flags for later:
login-not-gated-on-verification (product call), ChangePassword unrouted,
session-token hashing (v2 hardening). Next per R10: jobs-v1.

## 2026-07-02 — scope ruling: finish jobs-v1, defer events-v1 (jrazmi)

Token-budget call mid-jobs-v1: the loop completes jobs-v1 (phases 4, 5, 7,
8, 9 remaining) and STOPS; events-v1 is deferred — its design
(`roadmap/events-feature-design.md`) is ratified with the trio-layout note
applied, its preconditions (auth-v1, sdk/workers) are already satisfied,
and the loop-handoff prompt (`roadmap/loop-handoff.md` pattern) resumes it
in any future session at its planning leg. Telemetry remains after events,
as ratified.

## 2026-07-02 — jobs-v1 phase 5: jobs×turso LIVE-VERIFIED + storetest lease ruling

`features/jobs/stores/turso` shipped (16th module). The live run exposed a
REAL suite bug the memstore could never show: storetest.Lease (250ms) was
below the remote Claim→Complete cycle (~338ms measured), so the §6.3
stale-claim arm legitimately double-claimed in-flight jobs (29/60 doubles,
zero spurious errors — the store itself was correct, proven with a 30s
lease). Ruling: storetest.Lease = 3s (~9x margin; evidence + trade-off in
the const's doc comment). After the fix: LIVE-VERIFIED against the
authorized playground Turso — suite features/jobs/storetest, dialect
turso, DSN class real Turso (playground), result GREEN (16/16 cases incl.
ConcurrentClaim 60/60 distinct, 60.1s). The lesson mirrors P2's: the
conformance suite is only as honest as its slowest real dialect — another
entry for the storetest pattern's design notes. Phase log:
`.claude/plans/jobs-v1/05-store-turso.md`.

## 2026-07-02 — jobs-v1 phase 7: jobs×postgres LIVE-VERIFIED

`features/jobs/stores/postgres` shipped (17th module; migration filenames
identical to turso's). LIVE-VERIFIED twice (implementer + independent
-count=1 re-run): suite features/jobs/storetest, dialect postgres, DSN
class local docker (postgres:17, :55432), GREEN — 16/16 incl.
ConcurrentClaim (FOR UPDATE SKIP LOCKED: 60/60 distinct in 0.33s, no
busy-retry needed) and the 3.1s lease-reclaim sleep. One logged deviation:
payload is JSON not JSONB (byte-exact round-trip beats canonicalization
for an opaque column). With phase 5's turso runs, BOTH jobs dialects pass
one suite. Phase log: `.claude/plans/jobs-v1/07-store-postgres.md`.

## 2026-07-02 — jobs-v1 phase 8: the §8 proof protocol LIVE-VERIFIED

`examples/jobs-minimal` (18th module) drove the full protocol:
enqueue→handler in the SAME MILLISECOND (wake wiring, not polling);
flaky retry→success; doomed→dead_letter; deterministic sched_ slot IDs
with fire-once catch-up across a kill/restart (honest caveat: in-memory
restart proves ID determinism + catch-up, not cross-restart dedup);
SIGTERM mid-handler → full 5s handler completion → clean drain → exit 0.
CronSchedule ruling: type alias (option b) — robfig parser wires directly,
zero adapter. Ergonomics flag: no Config knob for the runtime pools'
logger (slog.Default). Phase log: `.claude/plans/jobs-v1/08-proof-host.md`.

## 2026-07-02 — jobs-v1 milestone CLOSED

All eight phases green and live-verified: sdk/workers (race-clean;
no-in-process-retry runner — durable retry is the store's Fail semantics),
features/jobs core (wake wiring proven by channel identity AND live
sub-second pickup), integrations/scheduling/robfig-cron, in-core memstore
+ storetest (16 cases), stores/turso (LIVE on the playground — and the run
that caught the storetest lease bug: 250ms < the remote Claim→Complete
cycle; lease now 3s), stores/postgres (LIVE on docker; FOR UPDATE SKIP
LOCKED made concurrency trivial; JSON-not-JSONB payload), examples/
jobs-minimal + the full §8 protocol (same-millisecond wake pickup,
retry/dead-letter, deterministic sched_ slots, fire-once catch-up across
restart with the honest in-memory caveat, graceful drain to exit 0), docs
sync (README with nil-semantics table; 18 modules across ARCHITECTURE/
README/RELEASING; sdk/README's stale "dormant ratelimiter" row fixed +
workers row added; capability map jobs rows BUILT).
Executed amendments (all logged in the design's status header):
storetest.Lease 3s · Job fields JobID/JobStatus/Retries · CronSchedule
type alias · JSON payload column. Standing flags for later: runtime-logger
Config knob (ergonomics); Job backing-field names open to a pre-v1 rename
if jrazmi prefers. Sequencing note for events-v1: sdk/workers ships
everything its outbox poller stated as requirements.

## 2026-07-02 — ROADMAP LOOP FINAL SUMMARY (the /loop session ends here)

One session, 2026-07-02, executed via the self-paced /loop protocol
(one phase per leg, opus implementers + fable planning/docs, firsthand
re-verification every leg, real-interaction checks mandatory):

- **datastore-portability CLOSED** — pgx connector, cms storetest (+the
  post_terms/entry_terms live-bug catch), cms postgres store (EAV spine,
  precision case), charter/taxonomy docs. Both cms dialects LIVE-VERIFIED.
- **auth-v1 CLOSED** — features/auth + bcrypt + cms AdminMiddleware +
  examples/auth-cms (rule-6 acid test: five-step cookie flow live, twice)
  + both dialect stores LIVE-VERIFIED + docs.
- **trio re-layout RATIFIED+EXECUTED mid-stream** — features wear
  logic/<domain> + internal/{logic,inbound} + stores/; app↔feature mapping
  table in the charter; internal-seam extension model; store posture C.
- **jobs-v1 CLOSED** — sdk/workers + features/jobs + robfig-cron +
  memstore/storetest + both dialect stores LIVE-VERIFIED (incl. the
  storetest-lease lesson) + examples/jobs-minimal passing the full §8
  protocol + docs. 18 modules, make check green, 4 guards.

**Deferred, with resume paths:** events-v1 (design RATIFIED at
roadmap/events-feature-design.md with the trio note; preconditions all
satisfied — sdk/workers ships its poller's requirements; resume = the
loop-handoff pattern at its planning leg, cutting .claude/plans/events-v1/
from the design's §11). Telemetry (sdk/tracing port + otel integration)
after events, per the ratified order. Workshop v2 last (store scaffolding
is now a headline deliverable per posture C).

**Open jrazmi flags:** Job backing-field names (JobID/JobStatus/Retries —
pre-v1 rename cheap if wanted); runtime-logger Config knob (ergonomics);
login-not-gated-on-verification + unrouted ChangePassword + session-token
hashing (auth product calls); cms non-root-prefix link limitation (C1,
pre-existing).

## 2026-07-06 — sdk-parity milestone EXECUTED (scope + plan ratified by jrazmi same day)

The original repo's deferred general-purpose surface is ported: 8 phases, 27
tasks, 18→21 modules, every phase gated green (`make check` + guards), the
web phase closed with a run-and-look drive of examples/cms on the playground
Turso DB (admin create/edit → themed public render → X-Cache MISS→HIT). Plan:
`.claude/plans/sdk-parity/plan.md`; per-phase deviations:
`.claude/plans/sdk-parity/execution-log.md`.

**Shipped.** New sdk packages: `validation`, `async`, `conversion`, `tracing`
(+`Noop`), `cryptids` (`Encrypter`+`AESGCM`, `SHA256Hasher`, `JWTSigner`
port-only), `oauth` (port + PKCE), `events` (+`eventstest`, `Memory`/`Noop`/
`WakeChannel`). Extended: `config` (`ParseEnvTags` — the dormant `env:` tags
on `web.ServerConfig`/`logging.Options` are live again), `logging`
(trace/span IDs), `slug` (accent folding), `workers` (`WithTracer` +
`WithRetryWithinClaim`), `web` (JSON kit, route groups + verb sugar, SSE with
the per-write deadline extension, static/SPA server, app-driven OpenAPI 3.1
builder, CORS/DefaultHeaders constructors), `email` (template registry +
layouts + branding; SMTP now sends multipart text+HTML — HTML was silently
unsent before). New integrations: `events/redis-streams` (go-redis v9; live
leg passed race-clean vs dockered redis:7), `oauth/google` (go-oidc v3),
`oauth/github` (zero external requires).

**Breaking.** `sdk/repository` → `sdk/crud` (D-6): interfaces generic over a
domain filter (`Reader[T,F]`/`CRUD[T,F,C,U]`), `ListRequest` non-generic
(+`Order`), fop restores (ordering, prev-page, strict `ParseListRequest` at
JSON edges vs `NormalizedLimit` clamping at store edges). 30 importers
migrated mechanically, grep-clean, zero behavior change (all call sites keep
clamping; nothing adopts the new vocabulary yet).

**Supersessions (newer ratification wins, logged not re-litigated).**
jobs-v1 J6/J7: in-process retry + tracer hooks restored — reconciled with the
store-owned durable model; `WithRetryWithinClaim(attempts, initialDelay,
maxElapsed)` requires `maxElapsed` > 0 sitting well below the store's claim
lease (lease-overrun regression test in `sdk/workers`); `WithMaxAttempts`
semantics unchanged. Events design §9: redis integration built early; design
status header amended — events-v1 resumes at phase 3 (`Mount.Events`).

**Taxonomy amendment (D-1/task-13, the one rule change).** An integration
isolates exactly one external dependency — a third-party library OR an
external vendor's live API contract; vendor connectors are never sdk defaults
even when stdlib-implementable. The R3 `stores/memory` refusal explicitly
stands. This is what places `integrations/oauth/github` (zero-require).

**Old-code bugs fixed during salvage** (behavior intentionally diverges from
the original): memorybus had two data races + a no-op `WithWorkerCount`;
`RemoteEvent` never implemented `Unmarshaler` (a `TypedHandler` silently
dropped every rehydrated event); the old redis bus never delivered
async-emitted events to `"*"` subscribers (new streams-path wildcard is
process-local by design and documented; broadcast owns cross-process
fan-out); old CORS sent `Allow-Credentials` on wildcard-matched origins (a
credential leak — credentials now only on explicit allowlist matches); the
OpenAPI generator collapsed non-200/201/204 statuses to `"200"` (a 202
override now emits correctly).

**Caveats.** `slug.Make` accent folding is a behavior change (D-5): persisted
slugs are untouched (write-time), but a mixed corpus now exists (old rows
slugged under the old algorithm) and renames re-slug (confirmed live in the
phase-6 drive); the cms content-type route-segment recompute path shifts only
for non-ASCII `Plural` registrations (shipped seeds are ASCII). `ß` folds to
single `s`, matching the old table exactly.

**Not run:** `make test-stores` (no live creds this session; hermetic
storetest suites green — the rename was import-path-mechanical).

**Open jrazmi flags:** (1) `AddAcronym` trimmed from conversion — the seam
for custom acronyms is a future `Caser` type, restorable in an hour; (2)
fast-follow integrations queue: `tracing/otel` (+stdout exporter), redis
cacher/limiter, gcs/s3 filestorage, sendgrid, golang-jwt (JWTSigner port
awaits the library decision); (3) `ß`→`s` vs `ss` — flag if German-correct
`ss` is preferred; (4) events-v1 resume point is its phase 3 per the amended
design.

## 2026-07-06 — kvstore-consolidation EXECUTED (rulings R-KV1–R-KV3, jrazmi)

Same-day follow-on to sdk-parity; plan + rulings at
`.claude/plans/kvstore-consolidation/plan.md`. Module count stays 21.

- **R-KV1 (multi-port integrations):** one integration module may implement
  several sdk facility ports when one client library serves them.
  `integrations/events/redis-streams` → `integrations/kvstores/goredis`
  (package `goredis`): the existing streams Bus/Broadcaster plus NEW
  `Cacher` (cacher.Storer) and `Limiter` (ratelimiter.Limiter, atomic Lua
  sliding window) as files in one package — the sdk-side ports make the old
  per-adapter packages unnecessary. All three conformance suites
  (eventstest/cachertest/ratelimitertest) run env-gated behind
  `REDIS_TEST_ADDR`; live docker leg passed race-clean. Category naming:
  capability by default, tech-family (`kvstores/`) when multi-port.
- **R-KV2/R-KV3 (named for the package, never the protocol; CORRECTION of
  the first-draft "dialect" framing):** concrete adapters are written
  against one package's custom API — that API is why the package was
  chosen. `integrations/datastores/postgres` → `datastores/pgxdb` (package
  `pgx`; jackc imports aliased internally) AND
  `features/{auth,cms,jobs}/stores/postgres` → `stores/pgx` (module paths,
  package clauses, connector alias `pgxdb`). A future sqlx-based store is a
  NEW `stores/sqlx` module. Implementation-independence lives in the
  feature's ports, not adapter names. `stores/turso`/`datastores/turso`
  keep their names (Turso is the provider; open flag if `libsql` preferred).
- **Migration ownership correction:** feature stores expose canonical SQL and
  `ExportMigrations`; hosts own the merged per-database stream under
  `workshop/migrations/{db}` and apply it pre-boot.
- **goredis gained the missing connection story** (jrazmi flag): `Config`
  (env-taggable) + `Open(ctx, cfg, opts...)` with fail-fast PING, plus
  go-redis `Hook`-based instrumentation — `LoggingHook` (slog) and
  `TracingHook` against `sdk/tracing` (otel exporter remains the deferred
  integration). Facilities keep taking a caller-supplied `*redis.Client`;
  bring-your-own-client stays first-class.
- Charter/architecture vocabulary shifted where it named modules
  ("per-dialect" → per-store-implementation; supported set now
  **{turso, pgx}**); "dialect" retained where it truly means SQL dialect
  (identical migration version sets across a feature's store trees).
- **Not run:** `make test-stores` live legs (no creds this session);
  hermetic storetest suites green through every rename step.

## 2026-07-06 — fast-follows EXECUTED (backends for every sdk port, 21→26 modules, jrazmi)

Third same-day milestone; plan at `.claude/plans/fast-follows/plan.md`. Five
new integration modules close the original-repo port queue, plus the task-0
quality-of-life pair. Final gate green: fresh `go clean -testcache && make
check` across all 26 modules, go.work↔Makefile agreement (26/26), all four
guards clean, every live leg skipping loudly.

- **task-0 pair.** `goredis.StatusCheck(ctx, *redis.Client)` — pgx-parity
  runtime health probe (1s default deadline, caller deadline wins; hermetic
  TEST-NET-1 fail test + `REDIS_TEST_ADDR` live leg; README row added for
  doc parity). And `sdk/conversion` gained the reserved `Caser` seam,
  resolving open flag #1 of the sdk-parity entry: immutable
  `NewCaser(WithAcronyms(...))` with the five case methods; the package
  funcs now delegate to an immutable package default — AddAcronym's
  capability restored with no package-mutable global (D-2). Existing
  conversion tests passed unmodified.
- **`integrations/tracing/otel`** (R-KV1: the otel family is one coherent
  dependency; v1.44.0). Implements `sdk/tracing.Tracer`; exporter by
  `Config.Exporter` — `stdout` (default), `otlpgrpc`, or `provider`
  (caller-supplied `TracerProvider`, never shut down by the module).
  `Open(ctx, cfg) → *Tracer` plus `Shutdown`/`ForceFlush`. Hermetic tests
  via tracetest SpanRecorder. NOT ported: the old global-propagator / W3C
  inject-extract helpers — follow-up only if a host needs cross-service
  propagation.
- **`integrations/filestorage/{gcs,s3}`.** Each implements the split ports:
  core `Storer` + `ResumableUploader` + `SignedURLer` (compile-time
  asserted). gcs wraps cloud.google.com/go/storage v1.61.3 (+
  google.golang.org/api for option/iterator — one vendor client-family
  spanning two module paths, flagged for visibility); `Config.Prefix`
  scopes a bucket subtree (the Disk base-dir analogue). s3 wraps
  aws-sdk-go-v2 service/s3 and keeps the S3-compatible seam:
  `Config.Endpoint` + `UsePathStyle` (MinIO/DO Spaces; proven by an
  offline path-style signed-URL test); multipart initiate uses the raw
  client, not the s3 manager (better fit for the resume-token contract).
  Conformance: sdk `filestoragetest` env-gated on `GCS_TEST_BUCKET`
  (+ endpoint/creds) / `S3_TEST_ENDPOINT` (+ creds), loud copy-pasteable
  skips. s3's `Open` deliberately has no construction-time ping (client
  build is network-free; creds resolve lazily — documented in the godoc).
- **`integrations/email/sendgrid`.** `email.Sender` over sendgrid-go.
  HERMETIC ONLY by design: tests point `Config.Host` at httptest and
  assert the auth header, recipients, subject, ordered text/html parts,
  and the status→sentinel table (400/401/403/404); NO live leg exists,
  even env-gated — a live call sends real email. `Config.FromName`
  carries the display name (`email.Message.From` is a bare address).
- **`integrations/cryptids/golang-jwt`** (package `golangjwt`, hyphenated
  dir per robfig-cron). `cryptids.JWTSigner` over golang-jwt/jwt v5.3.1 —
  jrazmi committed to the library, superseding "deliberately not built";
  the one permitted sdk edit updates that `sdk/cryptids/jwt.go` doc
  sentence to point at the module. HMAC-shaped: `New(secret,
  WithMethod(...))` (HS256 default, ≥32-byte secret) → `Sign`/`Verify`.
  Alg-confusion guard: the keyfunc rejects a method mismatch BEFORE
  returning the secret, plus `WithValidMethods` + `WithStrictDecoding`;
  tested three ways (HS512 forgery under the signer's own secret, the
  reverse mismatch, alg=none). Asymmetric RS/ES keying is a
  sibling-connector concern (documented).
- **Registration/docs (task-6, main session).** go.work + Makefile MODULES
  → 26 (alphabetical within the integrations run); ARCHITECTURE tree +
  "Twenty-six modules today"; README list/counts; RELEASING enumeration;
  sdk/README rows (tracing/cryptids/email/filestorage gained their
  external backends; conversion mentions the Caser); goredis README
  StatusCheck row.
- **Execution shape:** task-0 and tasks 1–5 ran as parallel implementer
  agents under the build-isolation rule (no go.work/Makefile edits;
  standalone `GOWORK=off` verification via the bcrypt-pattern replace).
  The network caveat never triggered — all five library families resolved
  from the local module cache.
- **Not run:** `make test-stores` and the new modules' live legs (no
  docker/creds this session; hermetic legs green, live legs skip loudly).

**Open jrazmi flags:** (1) throttler needs an sdk port decision
(waiting-limiter vs the existing rejecting ratelimiter) before its
integration is built; (2) sqlitelimiter — recommend skip unless a consumer
appears; (3) otel W3C trace-context propagation helpers not ported — flag
if cross-service propagation is needed; (4) s3 manager-backed streaming
multipart for the plain upload path, if wanted (needs a network fetch of
feature/s3/manager).

## 2026-07-06 — throttler resolved as `ratelimiter.Acquire` (no new port, jrazmi)

Closes open flag #1 of the fast-follows entry. Ruling (jrazmi, in-discussion:
"let's just do A now"): the old repo's throttler was not a backend — its
primary implementation was a retry loop over the rejecting port (Allow →
sleep `Result.RetryAfter` → retry). So blocking acquisition is a **helper on
the existing port, not a second port**: `sdk/ratelimiter.Acquire(ctx,
limiter, key, limit)` blocks until allowed, honoring ctx cancellation and
flooring sub-millisecond `RetryAfter` (busy-loop guard). Stdlib-only → sdk
per the sdk-default rule; composes with `Memory` and `goredis.Limiter`
as-is. Tests: immediate grant, real blocking across a window reset,
ctx.DeadlineExceeded on an exhausted window, backend-error wrapping
(`errors.Is`), and a call-counting stub proving the zero-RetryAfter clamp
(race-clean). The old `NewTokenBucket` smooth-metering Redis variant stays
unbuilt — one more file in `kvstores/goredis` (R-KV1) if a consumer ever
needs even pacing over burst-then-wait. Also ruled this session: skip
sqlitelimiter; otel W3C propagation + s3 streaming multipart wait for a
real need.

Stale-doc note (unfixed, pre-existing): `sdk/ratelimiter`'s package comment
still says implementations live in a `memorylimiter/` subpackage — `Memory`
has lived in-package since the consolidation. Also pre-existing:
`integrations/filestorage/gcs` uses the now-deprecated
`option.WithCredentialsJSON` (gopls flag; works today, swap on the next
touch of that module).

## 2026-07-06 — events/jobs three-tier straddle re-examined, AFFIRMED

jrazmi asked whether events and jobs straddling sdk/integrations/features
needs a rethink before events-v1 resumes. Re-walked against the constitution
and the live import graph: both capabilities genuinely have two halves — a
pure-behavior vocabulary half (a bus you emit on; a pool that runs work) and
a durable/routed half (outbox table + SSE routes; queue rows + schedules) —
and the repo's own litmus tests (pure behavior → sdk facility; migrations or
routes → feature) sort each piece exactly where it sits. The capabilities
that don't straddle confirm the pattern: filestorage/email/cacher have no
durable half, cms/auth have no kernel-vocabulary half. Alternatives all lose
concretely: folding the bus port into `features/events` creates the
feature→feature edge rule 6 exists to prevent (cms emitting
`content.published` would import a feature — the whole reason `Mount.Events`
is emit-only via the contract); making the outbox relay a jobs-feature job
was already litigated in the events design (§5: no queue row, no CAS claim,
no schedule entity — a manufactured `features/events`→`features/jobs` edge
for zero mechanical benefit); one merged "background" feature couples two
migration sources, two guarantee models (at-least-once outbox vs claim-based
queue), and two release cadences. Arrows verified inward-only: goredis →
`sdk/events`, `features/jobs` → `sdk/workers`, nothing in sdk knows a
concrete; the guards prove it on every `make check`.

Two real costs named, neither fixed by moving code: (1) the adopter wiring
tour for live-updates-end-to-end spans five stops — a comprehension cost,
paid with docs; (2) a perception artifact — events currently exists as three
tiers of infrastructure with no consumer because phases 1–2 landed early in
sdk-parity; it reads as one capability once `features/events` lands with cms
as first emitter. Folded into `roadmap/events-feature-design.md` §11 as two
additive plan-cut requirements: (1) a tier-review gate — architecture-steward
+ lead-backend-engineer critique the drafted events-v1 plan with "is any
piece in the wrong tier, and is the host wiring tour acceptable?" before
ratification, explicitly confirming the SSE-gateway-in-feature placement
(R9) so any change is conscious reopening, not drift; (2) phase 8 must ship
a per-capability wiring page (one diagram + one complete `main.go`), with
phase 7's proof host as its executable twin. No ratified decision reopened;
the design's status header carries the amendment.

## 2026-07-06 — authorization ruling: ReBAC supported, never required (jrazmi); post-events planning wave opened

jrazmi directive, consciously amending the 2026-07-02 capability-map
ratification (YOUR CALLs #1/#2 deferred ReBAC entirely pending a concrete
need): **auth-v2 WILL ship authorization, but as a port-shaped capability**
— a first-party ReBAC authorizer is the flagship implementation, and hosts
must be able to run gopernicus with no authorization at all, or with other
authorizer types (simple role/ownership checks; future policy engines).
Implications the auth-v2 design must honor: consumer-declared narrow ports
with structural satisfaction (the events design's `Config.Authorize`
deny-by-absence row is the standing precedent); no feature→feature imports
(rule 6); a documented nil-semantics row per optional port (charter item
12); naming rule stands (authorization/authorizer, never authz/authn).
Scope-vs-placement stays open for the design doc: auth subdomain vs sibling
module, and how much of the original's Check/CheckBatch/FilterAuthorized/
LookupResources surface is the generic port vs ReBAC-specific API.

Same session, planning wave opened for the post-events remainder (gap
analysis vs gopernicus-original, codegen/workshop-v2 excluded as a later
stage): (1) auth-v2 design doc (roadmap/), scope = authorization per the
ruling above + OAuth feature wiring + API keys/service accounts/principals
+ security events as first durable outbox emitter + JWT bearer mode +
invitations + the flagged v1 product debts; (2) repo-hardening milestone
(git init/remote, CI, pre-commit secrets hygiene, D8 module-path rename,
first tags per RELEASING.md); (3) telemetry closeout + hygiene sweep +
demand-gated ledger (small; demoting R10's standalone telemetry milestone
to a closeout is PROPOSED, ratify at plan review). events-v1 plan cut
continues separately and is unaffected.

## 2026-07-07 — planning-wave review gates run (8 reviewers, unanimous ratify-with-amendments); plans housekeeping

All three planning-wave drafts passed their review gates: auth-v2 design
(architecture-steward + lead-backend-engineer + product-manager),
repo-hardening (platform-sre + product-manager), telemetry-closeout
(architecture-steward + platform-sre + product-manager). Eight verdicts,
all RATIFY WITH AMENDMENTS, none NEEDS REWORK. Load-bearing catches, logged
so they survive the fold-in: (1) NO guard exists for features/X →
features/Y imports — rule 6's core edge is enforced only by acceptance
greps; a new guard target is amendment-mandated (auth-v2 is where the
first feature-pair adjacency appears); (2) the telemetry plan's specified
gcs credential swap (`WithAuthCredentials`+`DetectDefault`) silently drops
OAuth scopes → production 403s hermetic tests cannot catch — the verified
correct form is `option.WithAuthCredentialsJSON(option.ServiceAccount, …)`;
(3) the repo-hardening secret-gate's stated pass condition did not match
its own grep's real output; (4) no LICENSE file exists — tags without one
are legally un-adoptable; (5) A8 (durable security-event rail) has no
consumer in either milestone and is the sole reason auth-v2 gates on
events-v1 — build-vs-defer promoted to an explicit jrazmi call (AV10/AV11).
Amendments dispatched to the three planners for fold-in; drafts stay DRAFT
pending jrazmi ratification against the consolidated call list.

Housekeeping (jrazmi directive): closed-milestone plan dirs relocated
`.claude/plans/` → `.claude/past/` (datastore-portability, auth-v1,
jobs-v1, sdk-parity, kvstore-consolidation, fast-follows; mapping README
at `.claude/past/README.md`). Pre-2026-07-07 citations in this file and in
ratified/historical docs were NOT rewritten (append-only); they resolve
under `.claude/past/`. Standing rule: a milestone's plan dir moves to
`.claude/past/` in the same session its closing NOTES entry lands.

## 2026-07-07 — planning wave RATIFIED (jrazmi): all defaults; repo = github.com/gopernicus/gopernicus PUBLIC; LICENSE deferred

jrazmi ratified all three reviewed drafts at their recommended defaults,
plus the three owner-only calls:

- **auth-v2 design** — AV3 (two milestones: auth-v2 → authorization-v1),
  AV6 (JWT = short-TTL stateless user tokens; machine clients = API keys),
  AV7 (OAuth trims), AV8 (`RequireVerifiedEmail` defaults off), AV9
  (SecurityEvents repository optional), **AV10 (A8 durable rail DEFERRED;
  trigger = first real durable consumer — webhooks/alerting; the §5.2
  contract, re-check gate, guarantee statement, and appender grep travel
  with it)**, **AV11 (events-v1 gates only the deferred A8 — the identity
  milestone is decoupled and schedules on its own merits)**.
- **repo-hardening** — RH1 = `github.com/gopernicus/gopernicus`, **PUBLIC**.
  Conscious confirmation recorded per the RH1↔RH2 cross-link: with RH2's
  tracked set (NOTES.md, `.claude/plans/`, `.claude/past/`,
  `.claude/agents/`) the planning corpus is world-readable by design.
  Phase 4 (D8) collapses to a verification pass; NO quiet window exists —
  the rename-vs-code-milestone collision risk is erased. RH3 (playground
  Turso URL committed as-is), RH4 (CI bundle; manual-only live legs), RH5
  (vertical-slice first tags; timing waits for events-v1 close). **RH6 =
  LICENSE DEFERRED**: the repo will be public source-visible but
  all-rights-reserved; the hard gate STANDS — first tags remain blocked
  until the tagged commit carries a LICENSE. Public visibility enables
  GitHub secret scanning + push protection (the plan's ongoing-posture
  item) — enable at repo creation.
- **telemetry-closeout** — TC1 (ratification = R10 telemetry-milestone
  demotion confirmed), TC2 (`ß`→`s` kept), TC3 (`turso` naming kept, flag
  closed), TC4 (gcs credential swap DEFERRED — wont-do until a live GCS
  run; corrected form `option.WithAuthCredentialsJSON(option.ServiceAccount,
  …)` recorded in the flag), TC5 (`tracing.SpanIdentity` named optional
  interface in `sdk/tracing` — steward default stands).

Status flips DRAFT → RATIFIED applied to all three plan files same day.
Execution order per the ratification: repo-hardening phases 1–3 first
(everything into git before more code lands; hygiene gates before any
push), events-v1 + telemetry-closeout execute per their plans, auth-v2
phase files cut from the design's §13 when its execution window opens
(tier-review question re-runs at the cut, events precedent).

## 2026-07-07 — auth-v2 milestone CUT + gate + CUT-RATIFIED (jrazmi)

Same day as the design ratification, jrazmi opened the planning window
early (legal under AV11 — no events-v1 dependency). The milestone was cut
to `.claude/plans/auth-v2/` (00-overview + phases A1, A2, A3, A4, A5, A6,
A7a, A7b, A9, A10; struck A8 recorded as its deferred disposition; ten
cut-time refinements logged). The plan-cut gate ran per the design's own
requirement: architecture-steward + lead-backend-engineer (tier-review
re-run) + platform-sre + data-integration-reviewer — **4× ratify-with-
amendments**; 25 consolidated amendments folded same day. Load-bearing
catches, recorded so they outlive the fold-in: (1) the cut's rule-6
acceptance greps false-failed on the live tree (a doc comment matched) —
replaced with import-anchored forms; (2) `GetByHash` revoked→NotFound
had silently deleted the `blocked` audit event's service-account
attribution the salvage source carried — the pinned contract now returns
the record for any present row (NULL expiry = never expires) and the
service branches, `ErrExpired` dropped as a port sentinel; (3) the
session-hashing upgrade note gained the rolling-deploy flap +
rollback-second-logout reality; (4) both conformance harnesses' hardcoded
`authTables` truncate slices gain the six new tables (shared-playground
flake/false-green risk); (5) `/debug/security-events` is env-gated
default-OFF + session-gated (PII dump on a public example host
otherwise); (6) absent JWT secret → ephemeral boot-time key, never a
committed constant; (7) migration-ledger language corrected: connectors
dedup by FULL FILENAME under source "default" — hosts must never renumber
scaffolded files. One conscious design amendment landed in-place and was
**explicitly confirmed by jrazmi**: §7.2 ChangePassword = delete ALL
sessions + remint the caller's (supersedes "revoke other sessions"; a
strict security superset). **CUT-RATIFIED 2026-07-07 (jrazmi).**
Execution queues behind repo-hardening phases 1–3; first leg A1
(`01-debts.md`, implementer on opus). authorization-v1 (Z1–Z5) is cut
separately when its window opens.
