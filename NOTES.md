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

## 2026-07-07 — telemetry-closeout EXECUTED: web.Tracing shipped, real-drive proof on remote playground Turso, hygiene flags dispositioned

Workstream 1 closed with a real drive, never green tests alone. Shipped:
task-1's `sdk/web.Tracing(tracing.Tracer)` middleware (span name from
`r.Pattern`, status attribute, 5xx RecordError, Noop-safe) plus the NAMED
optional interface `tracing.SpanIdentity` in `sdk/tracing` (TC5, steward
default stands); task-2's otel finisher `TraceID()`/`SpanID()` + the
`examples/cms` wiring (`TRACING_ENABLED` gates the tracer choice, never
the middleware; Tracing outer of Logger; `tracer.Shutdown` on a fresh
timeout context). Hygiene fixes landed as code: the stale `sdk/tracing`
package doc (task-1), `sdk/ratelimiter` doc staleness (task-3), and the
`jobs.Config.Logger` runtime-pool knob (task-5, wired in
examples/jobs-minimal). Drive evidence — exact commands, span + log
excerpts — lives in `.claude/plans/telemetry-closeout/plan.md`'s
Execution log: DSN class of the evidence is **remote playground Turso**;
the OTLP shutdown-flush leg PASSED via Jaeger (SIGTERM inside the batch
window, all final spans arrived — the fresh-context Shutdown flush
proved); a main-session browser leg observed pattern-named spans and the
trace_id/span_id log linkage from a real browser client.

Flag closures (TC2/TC3/TC4 per this file's 2026-07-07 "planning wave
RATIFIED" entry):

- **JobID/JobStatus/Retries backing-field rename → WONT-DO** (origin:
  2026-07-02 jobs-v1 close entry, "open to a pre-v1 rename"). v1 shipped
  and is consumed (memstore, two dialect stores, storetest,
  examples/jobs-minimal) — the rename is now a breaking change with zero
  behavior payoff.
- **`AddAcronym`/`Caser` seam → ALREADY-SHIPPED** (origin: 2026-07-06
  sdk-parity entry, open flag #1). Corrects the "parked" framing:
  fast-follows task-0 already shipped the seam (`sdk/conversion/caser.go`,
  immutable `NewCaser(WithAcronyms(...))`, package funcs delegating to an
  immutable default).
- **gcs deprecated `option.WithCredentialsJSON` → per TC4 (DEFER), closed
  WONT-DO-until-a-live-GCS-run** (origin: 2026-07-06 throttler entry,
  "Also pre-existing"). The verified correct form for the future fix:
  `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`.
  NEVER the `option.WithAuthCredentials(credentials.DetectDefault(...))`
  form — it pre-builds scope-less credentials, the storage client
  short-circuits its OAuth scope injection, and every object op 403s in a
  real host while hermetic tests stay green (the emulator path uses
  `WithoutAuthentication`).
- **`ß` → `s` → KEPT (TC2)** (origin: 2026-07-06 sdk-parity entry, open
  flag #3). Matches the old table, already live; switching would deepen
  the D-5 mixed-slug-corpus caveat. The `ss` branch is not taken.
- **turso naming → KEPT (TC3)** (origin: 2026-07-06 kvstore-consolidation
  entry, R-KV2/R-KV3 open flag). Turso is the provider name and the module
  carries the vendor's live-service assumptions; a rename would touch two
  module paths, host imports, and docs for zero behavior.
- **C1 non-root-prefix links → ASSESS-ONLY DONE** (origin: 2026-07-02
  ROADMAP LOOP FINAL SUMMARY open flags; documented limitation in
  features/README.md §4). Forward-plan shape at
  `.claude/plans/telemetry-closeout/c1-assessment.md`: inventory of 36
  templ link sites + 25 Go-side sites; recommended seam is a named
  optional `BasePath()` interface in `sdk/feature` satisfied by
  `PrefixRegistrar`. The fix stays future-milestone scope, trigger-gated
  (ledger entry below).

Two execution facts of record: (1) `examples/cms` mounts its admin routes
UNGATED — no login exists in that host; flagged for jrazmi's awareness
(auth-gating an example host is auth-v2/examples scope, not telemetry
scope). (2) examples/jobs-minimal's `slog.SetDefault(log)` workaround was
removed when the `Config.Logger` knob landed — the knob is now the only
path from the runtime pools to the host logger.

## 2026-07-07 — demand-gated deferral ledger (telemetry-closeout; every deferral gets a wake-up TRIGGER)

Deferrals without triggers evaporate. Every deliberately-deferred item now
carries the observable condition that reopens it. Nothing below is scheduled;
each waits for its trigger, then gets its own plan.

- **jobs v2 — `Mount.Jobs` + jobs admin surface** (R8/J3 designed-deferred;
  jobs-v1 close). TRIGGER: a real scheduled-publishing consumer (e.g. cms
  scheduled publish) OR an operator need for a jobs admin surface. Note: once
  events-v1 ships its SSE gateway, an admin surface gets live job status
  nearly free — if both triggers fire, build the surface after events-v1.
- **Tenancy** (capability-map ratified call #3: an auth v2+ subdomain, never a
  standalone feature). TRIGGER: a real multi-tenant host exists.
- **otel W3C trace-context propagation helpers** (fast-follows open flag #3;
  ruled wait-until-needed 2026-07-06). TRIGGER: the first host needing
  cross-service propagation — calling a downstream traced service or sitting
  behind a traced edge — reopens it as a small addition to
  integrations/tracing/otel. Until then every request is a fresh root trace,
  accepted consciously at the telemetry closeout.
- **s3 manager-backed streaming multipart** for the plain upload path
  (fast-follows open flag #4). TRIGGER: a host uploads objects large enough
  that whole-object buffering hurts. Needs a network fetch of
  feature/s3/manager.
- **goredis smooth token-bucket Acquire variant** (throttler ruling entry: the
  old NewTokenBucket salvage). TRIGGER: a consumer needs even pacing instead
  of burst-then-wait — one more file in integrations/kvstores/goredis
  (R-KV1).
- **sdk/web transit-middleware residue** — trust-proxy IP resolution,
  client-info extraction, idempotency-key dedupe, max-body-size limiter
  (original `bridge/transit/httpmid/{trust_proxies,client_info,unique_to_id,
  body_limit}.go`; capability-map "Bridge transit middleware" row, sdk/web
  backlog). TRIGGER: first host need — a deployment behind a reverse proxy
  (trust-proxy + client-info) or a public write API (idempotency-key +
  body-limit).
- **Generic HTTP rate-limit middleware** (`RateLimit` over `sdk/ratelimiter`;
  original `bridge/transit/httpmid/rate_limit.go`; capability-map Rate
  limiting section, ratified home sdk/web, backlog). TRIGGER: first host
  exposing an endpoint that needs HTTP-surface rate limiting. Both backends
  already exist (`ratelimiter.Memory`, `goredis.Limiter`) — this is
  middleware-shape work only.
- **C1 — cms non-root-prefix link fix** (documented limitation,
  features/README §4; forward-plan shape produced at the telemetry closeout,
  `.claude/plans/telemetry-closeout/c1-assessment.md`). TRIGGER: a host needs
  cms mounted under a non-root prefix, or a multi-feature mount forces
  non-root prefixes.
- **Span vocabulary — server/client span kinds** (conscious loss at the
  telemetry closeout): the string-attribute-only `sdk/tracing` port carries no
  span kind, so HTTP request spans render as INTERNAL in trace viewers, vs the
  original's `SpanKindServer`. TRIGGER: a host needs server/client span
  differentiation in its trace backend — reopens as a port-vocabulary
  question (capability-map ruling: richer vocabulary belongs to the otel
  integration side), not a silent middleware patch.
- **ReBAC** — pointer, not a deferral: the 2026-07-02 "defer entirely" ruling
  is SUPERSEDED by the 2026-07-06 authorization ruling (auth-v2 ships
  authorization as a port-shaped capability; first-party ReBAC authorizer is
  the flagship implementation, never required). Owned by the auth-v2 design
  doc, not this ledger.

## 2026-07-07 — auth-v2 milestone CLOSED

All ten phases green and live-verified, zero new modules (everything folded
into `features/auth` + `stores/{turso,pgx}` + `examples/auth-cms`; the
26-module set unchanged): A1 v1 debts (service-side session hashing — one
authsvc hash helper, no DDL; `POST /auth/password/change` +
`SessionRepository.DeleteByUser`, delete-ALL+remint per the confirmed §7.2
amendment; `RequireVerifiedEmail` knob, AV8 default false), A2 OAuth
(oauthaccount/oauthstate domains, PKCE/state/nonce flow, the pending-link
anti-takeover gate, `ErrOAuthReposRequired`, AV7 trims held), A3 machine
identity (serviceaccount/apikey domains, dotless key mint, the pinned
GetByHash-returns-any-present-row contract, `auth.Principal` value type per
AV5, `RequireServiceAccount`/`RequirePrincipal`, both-or-neither
`ErrMachineReposRequired`), A4 JWT bearer mode (`TokenSigner`/`TokenTTL`,
`POST /auth/token`, two-dot classing arm, AV6 — no refresh tokens), A5 the
synchronous audit rail (13+5 event vocabulary, never-fail writes, coarse-WARN
+ key-prefix content hygiene, the exported `WithClientInfo` carrier as the
single IP source, AV9 optional), A6 invitations (grant-on-accept `Granter` +
`MemberCheck` seams per AV4, deny-by-absence routes,
resolve-on-registration, `ErrInvitationRepoRequired`), A7a/A7b stores
(migrations 0006–0011, identical filename sets, six repositories per
dialect), A9 the proof host + full protocol, A10 docs sync (this entry). A8
was STRUCK pre-execution (ratified AV10): the durable security-event rail is
deferred, trigger = the first real durable consumer; design doc §5.2 governs
it. AV11 held structurally — zero events imports, enforced at close (grep
below).

**Live-store conformance artifacts (charter item 11, both dialects):**

- **2026-07-07, turso (A7a):** store `features/auth/stores/turso`; suite =
  the full `features/auth/storetest` (v1 leaves + the six new sub-runners:
  OAuthAccounts, OAuthStates, ServiceAccounts, APIKeys, SecurityEvents,
  Invitations); DSN class = remote playground
  (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`,
  env-verified pre-run); `go test -tags=integration -count=1 -p 1 -v ./...`
  → **PASS**, 69 leaf PASS, 0 FAIL, ~205s wall. One wrinkle, resolved: the
  first run tripped the checksum guard on `default:0003_sessions.sql` — A1's
  comment-only header edit to an already-applied v1 file, not a new file;
  the single stale ledger row was cleared and 0003 re-applied idempotently.
  Token never appeared in output.
- **2026-07-07, pgx (A7b):** store `features/auth/stores/pgx`; same full
  suite; DSN class = disposable docker postgres:17
  (`postgres://…@localhost:55432`, container `a7b-pg`, removed after);
  `POSTGRES_TEST_DSN=… go test -count=1 -v ./...` → **PASS**, 69 leaf PASS,
  0 FAIL, ~2s wall — leaf-count parity with the turso run (cross-dialect
  DP1). Fresh container = fresh ledger; the 0003 wrinkle did not recur.

**A9 proof protocol (run-and-look, exact codes; `examples/auth-cms` :8082,
RequireVerifiedEmail=true):** leg 0 five-step: 401 → register 201 →
login-before-verify **403** → verify 200 (code from the console-mailer log)
→ login 200+cookie → gated 200 → logout 200 → 401. Leg 1 OAuth (fake
provider): start **302** (state + PKCE S256 visible in Location), callback
302 + session (new-user path), `/auth/oauth/linked` 200 single entry;
re-run same identity → login path, still one link. Leg 2 API key: SA 201,
mint 201 (plaintext once), no-cookie bearer on the RequirePrincipal route
**200** (service_account), revoke 200 → same call **401**. Leg 3 JWT:
`/auth/token` 200, bearer 200 (user); TTL=1s reboot → expired **401**;
`AUTH_JWT_DISABLED=1` reboot → same JWT **401** (never parsed), `/auth/token`
**404**. Leg 4 invitations (toy Granter): pre-grant 403 → invite 201 →
accept 200 → members-only **200**; non-member C **403**; decline → declined,
no grant. Leg 5 audit: debug route 200 with session+`AUTH_DEBUG=1`, 401
without session, 404 without the flag — 22 rows across the vocabulary
(3 register, 4 login/success + 1 login/failure, 3 email_verified, 1 logout,
1 oauth_register + 1 oauth_login, 2 apikey_auth/success + 1
apikey_auth/blocked, 1 token_issued, 2 invitation_created, 1
invitation_granted, 1 invitation_declined).

**Honest divergences of record (operational; each logged in its phase
file):** (1) Login/ChangePassword return the plaintext token separately —
forced by §7.3's `Session.Token`-is-the-hash (A1). (2) The GetByHash
contract was re-pinned at the plan-cut gate: the store returns ANY present
row and revocation/expiry branch in the SERVICE, restoring the `blocked`
audit event's service-account attribution (supersedes cut refinement 3).
(3) The forgot-password *request* records NO security event — it must not
reveal email existence; `password_reset` is recorded on completed reset
(A5). (4) `invitation.ListBySubject` keys on the invitee `Identifier`
(email), not `ResolvedSubjectID` — the pinned 2-port set must find
register-later invitees; visibility rides a table column, honoring the
no-tuples rule (A6). (5) Invitation mail is plain-text via `Config.Mailer`
(no sdk/email template registry in auth yet — v1 precedent). (6)
`oauth_states.payload` is BYTEA on pgx, not JSONB — the storetest contract
asserts a byte-exact round-trip including non-JSON payloads (the jobs
JSON-not-JSONB reasoning); `security_events.details` stays JSONB (A7b).
(7) The proof host ships TWO demo routes (whoami = RequirePrincipal-only,
members-only = + toy membership) — one route cannot serve both leg 2 and
leg 4 (A9). (8) The leg-5 "blocked login" is recorded as `login/failure`
(the verified-email 403); `login/blocked` is the rate-limit branch, and
`apikey_auth/failure` the expired-key branch — neither exercised by the
protocol.

**A10 docs sync:** `features/auth/README.md` rewritten — the grown `/auth/*`
route surface grouped by gate; charter item-12 nil-semantics tables for
EVERY new Config port (Providers, TokenEncrypter, OAuthCallbackBase,
RedirectAllowlist, TokenSigner, TokenTTL, Granter, MemberCheck,
RequireVerifiedEmail with the mailer-lockout WARNING, Logger) and
Repositories port (OAuthAccounts, OAuthStates, ServiceAccounts, APIKeys,
SecurityEvents, Invitations) including the deny-by-absence couplings and
the three loud partial-wiring errors; the session-hashing note; the §7.3
UPGRADE NOTE (forced logout incl. long-TTL sessions, vacuum note,
single-cutover/drain guidance, rollback = second mass logout) in the README
AND `RELEASING.md` (new upgrade-notes section keyed to the module's next
tag); the wiring page in the README (no events-v1 precedent exists yet) —
one diagram + one complete `main.go` (extracted verbatim from the README
and compiled/vetted green in a scratch module; `examples/auth-cms` is the
executable twin) + the corrected migration-source paragraph (connectors
dedupe by FULL FILENAME under ledger source `"default"`; numeric-prefix
overlap safe; hosts must NOT renumber). Capability map: v2 identity rows
marked BUILT, principals NOT salvaged (AV5), durable rail deferred (AV10),
ReBAC rows left pointing at authorization-v1. Module counts verified
unchanged (26) across ARCHITECTURE/README/RELEASING. Fresh-eyes fixes
logged in the A10 execution log (`.claude/past/auth-v2/10-docs-sync.md`) —
notably the phase file's "both trees start at 0001" premise was false (cms
scaffolds start at 0009); the README states the real overlap
(`0009_api_keys.sql` vs `0009_terms.sql`).

**Milestone-close acceptance (all PASS):** `make check` → all checks passed
(26 modules + templ drift + integration-tag vet + 4 guards). Rule-6 both
directions, import-anchored:
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
→ empty; the cms→auth reverse → empty. Deferred-rail absence:
`grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/\(sdk/events\|features/events\)' features/auth/`
→ empty. `features/auth/go.mod` requires exactly `sdk`. Real-interaction:
`examples/minimal` (:8081) `GET /` 200, `GET /products/widget-3000` 200,
killed, port free; and the A9 leg-0 five-step re-run against the shipped
`examples/auth-cms` README's OWN curls verbatim → 401, 201, 403, 200
(verify), 200 (login), 200 (gated), 200 (logout), 401 — the docs are
executable as written. Ports freed.

Next: authorization-v1 (Z1–Z5) is cut from design §13 when its window
opens; the deferred durable rail waits on its trigger (first real durable
consumer) + events-v1 shipped. Plans moved to `.claude/past/auth-v2/` per
the standing housekeeping rule.

## 2026-07-07 — feature-standard RATIFIED (jrazmi): FS1–FS10 extension model; convergence execution opened

Ratified at Claude's recommendations
(`.claude/plans/feature-standard/{00-charter,01-convergence}.md`;
architecture-steward reviewed, 14 findings folded pre-ratification). The
decisions: **FS1** feature core modules import sdk only (machine-checked;
supersedes D7 via its own revisit clause — the headless host materialized);
**FS2** the public `Service` is the feature's driving surface — use-cases
promoted by thin delegation, shipped transport is an optional adapter, and
anything consuming the built feature takes the BUILT Service (supersedes
the §1 `Register(mount, repos, cfg)` contract; jobs's rebuild-and-discard
`Register` amended too); **FS3** presentation is a `Views` port in the
core returning `web.Renderer`, bundled defaults ship as `views/<pkg>`
sibling modules, uniform nil → HTML surface off (amends the R6 taxonomy +
degraded-mode matrix row); **FS4** sibling modules are per-concern, never
mandatory; **FS5** store adapters stay feature-owned — the
stores-under-integrations alternative was examined and REJECTED (import
inversion: feature-aware code below the features layer; plus release-train
locality and the third-party-author test); connectors absorb shared
helpers instead (rich connector, thin adapter); **FS6** Config structs
remain the carrier, no functional options; **FS7** route tables become
data, the public override hook HELD until demanded; **FS8** behavior hooks
defer to the events rail; **FS9** feature transports use sdk/web — sdk
gaps are fixed in the sdk if they pass sdk/README's admission policy,
else one named local helper citing the failed test; **FS10** cms content
is plain text for now (goldmark/bluemonday leave the core; markdown
returns as its own decision at cms specifics).

Owner rulings at ratification: Register = **method form**
(`svc.Register(mount)`; auth's promoted user-registration use-case is
`RegisterUser`); FS1 guard lands with a **cms carve-out scoped to templ
only** (dated TODO, removed at convergence B2). Cross-gates recorded:
repo-hardening task-9 now depends on feature-standard B1+B2 (or a
conscious waiver) so `features/cms/v0.1.0` isn't tagged with deps it's
about to shed; events-v1 task-11's Register wiring carries a sync note to
ship the FS2 method form. Sequencing gate "after auth-v2 close" satisfied
same day; "before first tags" holds (tags still double-gated on events-v1
close + LICENSE). Convergence execution (Phases A/E/B1/C/D1) opened in
this session.

## 2026-07-07 — events-v1 amendments A-I1 + A-R1 RATIFIED (jrazmi): sdk/identity graduation; features/auth → features/authentication

Origin: fresh-eyes taxonomy session (jrazmi's "should auth/events/jobs be
sdk-with-adapters / foundational features be renamed?" question). Verdict
recorded first: **R6's facility/feature litmus reaffirmed** — events and
jobs already have the asked-for shape (sdk vocabulary + feature-owned
durability); auth's facility-shaped parts already graduated at sdk-parity
(oauth, cryptids, ratelimiter, email); no new module kind for
"foundational" features (role ≠ structure; provider role confers no
import privilege — rule 6 unbent). The one real asymmetry found: auth's
identity-in-context vocabulary was still sealed behind a private context
key, forcing every consumer to hold a matched middleware+port PAIR from
the same provider.

**A-I1 (`.claude/plans/events-v1/plan.md`): `sdk/identity`** — vocabulary
only (`Principal{Type, ID}` per AV5, `User`/`ServiceAccount` constants,
`WithPrincipal`/`FromContext`; the oauth/errs shape — no default, no
middleware, no authorization vocabulary). Cashes the charter §5
corollary's named graduation ("an identity-in-context convention");
admission trace recorded in the amendment. Lands as events-v1 tasks
1b/1c + edits E1–E8: the gateway loses `Config.Identity` and the
`CurrentUser` port; absent identity now **fails closed** (401 per
stream) — a recorded amendment to the degraded-mode matrix row "events
`Config.Identity` nil → hard error". Auth conformance is alias-based;
public API provably unchanged (existing tests pass unmodified + login
run-and-look). Authorization stays unsplit and unbuilt: `Granter` is
write-shaped, `AuthorizeStream` check-shaped — no vocabulary convergence;
revisit trigger unchanged. A future authorization implementation is its
own module, never growth inside the authentication feature.

**A-R1 (same plan): rename `features/auth` → `features/authentication`**
(+ both store modules; root package `authentication`, root file
`authentication.go`; example keeps call sites via an `auth` import
alias). Rationale: unambiguous pairing with a future
`features/authorization`; zero tags cut, so the churn is free now.
Scope verified pre-ratification: examples/auth-cms is the ONLY external
importer; no migration-source string exists in store code; no host
ledger holds auth rows. Lands as events-v1 task-0 (phase 0).
COORDINATION FLAG: feature-standard convergence (opened this session)
touches the same files — land/park convergence first; the rename rebases
trivially over content edits, not vice versa.

## 2026-07-07 — events-v1 RATIFIED (jrazmi, at defaults); overnight implementation loop authorized; backlog committed + pushed

events-v1 (`.claude/plans/events-v1/plan.md`) RATIFIED at defaults: open
questions 1–4 at their recommended/decided answers — wiring page in the
feature README (1), pgx payload JSON (2), G5 guard stands (3), P5
MaxConnAge no-disable confirmed (4). Amendments A-I1 (`sdk/identity`) and
A-R1 (`features/authentication` rename) were ratified earlier the same
day (previous entry). Remaining pre-execution plan edit: the FS2 fold-in
(task-11 sync note / feature-standard W4) — assigned to the loop's
pre-flight leg.

Overnight implementation loop authorized and kicked off per
`.claude/plans/roadmap/overnight-loop-2026-07-07.md` (queue:
feature-standard remainder B2 + D2–D6 → events-v1 phases 0–6 →
authorization-v1 plan CUT as DRAFT only; fences: no tags, no LICENSE,
close-blocked-not-faked live legs). In-session confirmations: git
discipline incl. pre-loop backlog commit+push ("this is a clean main"),
D5's `sdk/crud`-into-connectors ratify-at-execution granted,
authorization-v1 cutting allowed as a planning leg. The feature-standard
convergence backlog (phases A/E/B1/C/D1) and today's planning edits were
committed and pushed in the authorizing session — SHAs in git log, CI
required-check green verified before loop start.

## 2026-07-08 — feature-standard milestone CLOSED

Both plan files fully executed; one new module (26→27:
`features/cms/views/templ`). Charter (W1–W4 + ratification) landed
2026-07-07 (its own entry above). Convergence: phases A/E/B1/C/D1 landed
pre-loop (`54ea545`); the remainder landed across the overnight loop and
the 2026-07-08 morning session — B2 `801d6b4` (views/templ extraction, Views
port in core with public aliases, admin coverage gap closed, theme deleted,
G5 carve-out removed, sub-plan + full log in the milestone dir's
`02-b2-views-extraction.md`), D2 `d35aad8` (ExportMigrations + Scanner →
both connectors), D3 `eb73a81` (turso timestamp/bool bundle incl. the
divergence-1 NullTime/NullTimePtr pair), D4 `e9fa8a9` (pgx nullable-time
pair + FromNullTimePtr read twin, logged in-spirit addition), D5 `8d8eeec`
(keyset `ListPage[T]` both dialects; turso Querier mirror; the pre-granted
`sdk/crud`-into-connectors ratification RECORDED), D6 `015a8b2`
(ExecAffecting normalization; zero→ErrNotFound stayed adapter-side). Every
commit CI-green on the remote before the next leg started.

**Live-verified vs hermetic (honest):** B2 proven by pre/post byte-compare
(11/11 identical pages on examples/cms), triple-host render checks,
auth-gated admin 401→200, and a live nil-Views blackout; two intentional
wire deltas of record — media file-serve errors are JSON, nil `Config.Views`
unregisters the HTML surface. D3/D5/D6 each drove examples/cms against the
authorized playground turso DB (URL verified per leg; D6's drive crossed a
real mutation, 303 + not-found 404). D2/D4's pgx paths are hermetic-only —
live postgres conformance stays DSN-gated; next natural live leg is
events-v1 phase 4's docker run.

**Deferrals + open flags (demand-gated ledger discipline):**
- **task-B3 (cms public Service)** — deferred by design, demand-driven.
  TRIGGER: the first host that needs a cms use-case without (or beside) the
  shipped HTML transport.
- **storetest empty-page keyset case** — cms pins it; auth/jobs do not
  (D5 gate finding; empty path lives in `sdk/crud.TrimPage`, tested there).
  OWNER CALL open: add the case to both storetests to pin the contract.
- **Hygiene, parked for repo-hardening tasks 9/10:** stale
  templ/goldmark/bluemonday `// indirect` entries in the two cms store
  go.mods (pre-date B2); `tableExists` unused test helper in the turso
  connector.

Plans housekeeping: `.claude/plans/feature-standard/` →
`.claude/past/feature-standard/` this session, README table updated.

## 2026-07-08 — events-v1 milestone CLOSED

Three new modules (27→30 with feature-standard's views/templ the same day):
`features/events` + `stores/{turso,pgx}`. Shipped per
`.claude/plans/events-v1/plan.md` (phases 0–6, every leg committed and
CI-green before the next): **task-0** the A-R1 rename
(`features/auth` → `features/authentication`, 43 files, alias-based — the
example's call sites unchanged); **phase 1** `Mount.Events` (emit-only,
best-effort at-most-once, nil = silent no-op), `sdk/identity` (A-I1:
vocabulary only — Principal/constants/WithPrincipal/FromContext), auth
conformance onto the one carrier with its public API provably unchanged
(existing tests unmodified), charter C3 cashed in features/README §6;
**phase 2** the feature core — `logic/outbox` port, the exported
host-driven poller (P1 emit discipline: WithSync, mark only after a
successful emit), the SSE gateway (hub + routes; NewService validates nil
Bus per FS2, absent identity fails closed 401 per A-I1 E1, MaxConnAge
no-disable per P5), and the storetest suite with a memstore-honest
reference; **phase 3** cms emits `content.*` post-write (nil-guarded,
best-effort) and the proof host's cache-invalidation subscriber (edit →
X-Cache MISS in 34ms where a 60s TTL previously served stale); **phase 4**
both store modules — identical migration filename sets, source `"events"`,
boot-time table probes, dialect-typed AppendTx (tested, unconsumed,
unguarded — revisit at the third emitting feature); **phase 5** the proof
host end-to-end; **phase 6** the G7 rule-6 cross-feature guard
(prove-can-fail recorded; the plan's "G5" label was taken — landed as G7,
seven guards now), the feature README with the mandated wiring-tour page
(five stops, verbatim-from-twin listing, swap snippet scratch-compiled),
and this docs sync.

**Live-store conformance artifacts (both dialects, 2026-07-08):**

- **turso:** `features/events/stores/turso` against the authorized
  playground DB (URL asserted pre-run): `TestConformance` 5/5 subtests
  PASS (9.87s), `TestAppendTx` PASS (2.56s — visible after commit, gone on
  rollback), `TestExportMigrations` PASS; 6 top-level PASS / 0 FAIL.
- **pgx:** `features/events/stores/pgx` against postgres:17 in docker
  (:55432): `TestConformance` 5/5 PASS, `TestAppendTx` PASS,
  `TestExportMigrations` PASS — 3/3; container removed, port freed.

**Phase-5 real-interaction protocol (recorded verbatim in the plan log):**
steps 1–4 (default/direct-emit variant) — login flow, `curl -N /events`
with ping heartbeats, admin edit → raw SSE frame
(`event: content.updated`, metadata-only body, `id:` = CorrelationID) on
the open stream, unauthenticated 401; steps 5–6 (`EVENTS_OUTBOX=memory`
durable variant) — `/outbox-demo` 202 → frame in 37ms with `id:` = the
durable outbox EventID (provenance distinct from the correlation id), then
SIGTERM showing the documented order verbatim: HTTP server → poller pool →
bus.Close, clean exit.

**Deltas of record:** the plan's supersessions (S1–S6), eight ratification
gate edits, and P1–P5 post-gate amendments are logged in the plan +
design-doc header — headline items: A-I1 (no `Config.Identity`; fails
closed), P5 (`MaxConnAge` cannot be disabled), gate edit 1 (best-effort
SSE `id:` is CorrelationID, no de-dupe; durable `id:` is EventID),
AuthorizeStream takes `identity.Principal` (faithful post-A-I1 shape, a
logged divergence from the design's `userID string`).

**Open flags (jrazmi):** (1) events `Config` has no `Logger` field — the
hub logs via `slog.Default()`; the enumerated Config set was kept exact,
but jobs/auth carry the Config.Logger precedent — decide at first real
need. (2) Each admin edit emits TWO `content.updated` frames (Edit +
SetTerms in the update handler) — harmless for re-fetch consumers,
observation logged at task-11. (3) The D5-era storetest empty-page gap
(auth/jobs) still stands from feature-standard's close. (4) The appender
seam is unguarded by design (§5 cost 1).

Plans housekeeping: `.claude/plans/events-v1/` → `.claude/past/events-v1/`
this session, README table updated.

## 2026-07-08 — feature rim rename `logic/` → `domain/` (trio-relayout L1 amendment)

jrazmi (2026-07-08): *"the top level logic directory feels named wrong...
its really types (but does have constructor)"* → ratified: the feature
hexagon's public rim directory is now `domain/<domain>`, not
`logic/<domain>`. The rim holds domain entities + repository ports (the
domain model + contracts store adapters and hosts implement); the
services under `internal/logic/` are the logic and were NOT touched. Pure
path churn across the four features (authentication, cms, jobs, events):
`git mv features/<f>/logic features/<f>/domain` (17 domain packages ride
along), then import-path rewrites — zero package-name renames, zero
module-path renames, call sites untouched. Scope: ~129 .go files + 6
`.templ` sources rewritten (`*_templ.go` regenerated, not hand-edited);
trio-relayout L1 amendment-marked, authorization-v1 DRAFT synced
(`domain/relationship`, `domain/group`), live docs swept (ARCHITECTURE,
READMEs, features/README §2 trio contract).

**Verification:** negative greps `features/[a-z]*/logic/` empty over
go+templ and live docs; per-module build/vet/test + all seven guards green
pre-commit, full `make check` green at commit (the drift gate compares
moved `*_templ.go` against HEAD — the B2 same-commit precedent); templ
idempotent (generate twice → identical porcelain); both boots real-driven
(examples/minimal :8081 GET / + /products/widget-3000 → 200;
examples/auth-cms :8082 the full five-step protocol 401→201→403→verify
200→login 200→gated /articles 200, then authenticated GET /events → 200
text/event-stream).

## 2026-07-08 — pgx-crud-v1 RATIFIED; authorization-v1 codex fold (two owner rulings)

**pgx-crud-v1 cut and RATIFIED same day** (`.claude/plans/pgx-crud-v1/`,
P1–P6): the pgx v5 idiom sweep (NamedArgs, CollectRows/RowToStructByName
via store-local db-tagged row structs, UNNEST bulk writes) + the sdk/crud
List standards — order wired end to end, bidirectional cursor pagination,
opt-in `Total` counts, opt-in limit/offset mode. Pre-cut owner rulings:
pgx-first (turso gets semantics-only updates; idiom parity is the
declared follow-up `turso-crud-parity`), store-local row structs (no db
tags on `features/*/domain`), one extended `ListRequest`/`Page` pair.
Ratified at all recommendations: **Q1** order allow-lists in feature-core
domain packages; **Q2** cms prev-link templ task IN; **Q3** SSR
fallback-to-default on bad order params (JSON edges stay strict-400).
**Sequencing ruling: pgx-crud-v1 executes BEFORE authorization-v1** —
Z2a/Z2b store phases land on the new List standards.

**authorization-v1 (still DRAFT): external Codex review folded** (A1–A8,
itemized in the plan's consultation notes). Two owner rulings taken:
**A1 — `MaxTraversalDepth` is engine-only**: the review caught that lead
refinement 8 required store CTEs to honor the engine's bound by
"mirroring how the original threads it" — but the original never threads
it (its CTE is unbounded, UNION-dedup-terminated; the bound only guards
the engine's Go recursion). Ruled: match the original; depth-boundary
storetest pair dropped; refinement 8 supersession-marked. **A2 — the
unique-subject index is ADOPTED**: the original's one-relation-per-
subject-per-resource unique index was silently missing from the DDL
bullets; ruled adopt, with a constraint-level storetest case. Plus:
`subject_relation NOT NULL DEFAULT ''` (the iam_roles NOT-NULL precedent
applied over the original's nullable+COALESCE), explicit
relationship_id/created_at in the DDL bullets, `Adversarial/NestedUserset`
reworded tuple-side (request-side `Subject.Relation` is dead in the
original engine), `iam_roles` secondary index realigned to
`ListByResource`'s (resource_type, resource_id, created_at), salvage
paths corrected to `../gopernicus-original`, Q4-conditional metadata
probe. Q1–Q5 ratification still owed.

## 2026-07-09 — pgx-crud-v1a amendment CLOSED: explicit pagination Strategy + connector flow split

Post-close amendment to pgx-crud-v1, owner-ratified in-conversation
2026-07-08 after reviewing the shipped `List[T]`: (1) the single-function
mode interleaving was split into linear `listCursor`/`listOffset` private
flows behind the unchanged `List` signature (both dialects; the offset
flow no longer strips cursors post-hoc); (2) **mode selection is now an
explicit `crud.Strategy`** ("cursor" default via zero value, "offset")
— the `Offset > 0` inference is REMOVED: a cursor-strategy request
carrying an offset fails loudly (wrapping ErrInvalidInput) instead of
silently flipping modes, and `offset=0` is now expressible offset mode
(proven over live HTTP). `ParseListRequest` folded its five strings into
one `ListParams` options struct (the Pager break-once lesson) carrying
`DefaultStrategy`; strategy resolves at the edge by param PRESENCE
(offset param even "0" → offset; cursor → cursor; both → 400; neither →
the host default). `authentication.Config.ListStrategy`
(`env:"AUTH_LIST_STRATEGY" default:"cursor"`, loud validation) is the
env-configurable-default demonstration — note the tag bites only via
sdk/config.ParseEnvTags; literal Configs resolve empty → cursor.
Rejected at ratification: public per-mode List functions (the dispatch
would copy into every store port). Wait-for-demand (recorded): a
`strategy` query param; per-route strategy restriction; exporting the
reverse-probe/resolveOrder flows for non-BaseSQL custom queries — the
clause builders (QuoteIdentifier/ApplyCursorPagination/AddOrderByClause/
AddLimitClause) are already exported and composable. Live artifacts
(2026-07-09): pgx all five modules ok (docker postgres:17); turso all
four `-count=1` ok (playground gate); storetest families extended
(OffsetZero + per-strategy CursorOffsetExclusive pair). Plan file
archived into `.claude/past/pgx-crud-v1/`. Still UNCOMMITTED with the
parent milestone — jrazmi's commit call.

## 2026-07-09 — pgx-crud-v1b amendment CLOSED: per-aggregate list Limits

Second post-close amendment to pgx-crud-v1, owner-ratified
in-conversation (max page size is a RESOURCE property — "a list of ids
could be more than 100; a list of embeddings for moby dick should be
limited"). `crud.Limits{Default, Max}` is the resource's policy,
declared per-aggregate in the domain rim beside OrderFields (the Q1
pattern) via `var ListLimits = crud.Limits{…}` — the DefaultLimit(25)/
MaxLimit(100) constants survive as zero-value FALLBACKS, so sdk stays
the zero-config starter. `NormalizedLimit(l Limits)` replaces the
zero-arg store-edge clamp; `ListParams.Limits` replaces v1a's MaxLimit
field (one vocabulary both edges; strict-vs-clamp split preserved:
edge errors above max, store clamps); both connectors' `ListQuery[T]`
carry Limits. Deliberately NO aggregate declares custom limits yet —
every call site passes the zero value and behavior is identity
(live-proven both dialects + the HTTP edge: bare list default-25,
limit=100 → 200, limit=101 → 400). Ruled at ratification: limits are
code-declared resource properties, never env-configured (strategy
stays the only env-defaultable list knob); no ListRequest field growth.
Archived as `.claude/past/pgx-crud-v1/v1b-list-limits.md`. Still
UNCOMMITTED with the parent milestone + v1a — jrazmi's commit call.

## 2026-07-08 — segovia-lessons phase 01 EXECUTED: inbound anatomy ratified (flag #1); D1 = `internal/inbound/<feature>/`

The segovia-lessons milestone opened as the standing intake for framework
gaps flagged by Segovia v2 (flags adopted VERBATIM from Segovia's
`04-gopernicus-flags.md`; that doc is never edited from here — owner flips
statuses there). Flag #1 ratified and executed same day: ARCHITECTURE.md
gained the §"Inbound anatomy" subsection (per-domain
`internal/inbound/domains/<domain>/` with routes.go/api.go/html.go/
views.go/templates/; `inbound/http/` plumbing-only; `inbound/views/`
global tree + theming seam; override-via-embedding; the resource-axis
growth rule; never `/api`/`/html`/`/htmx` subdirs). One gopernicus-side
extension of the flag text, marked as such: the **maximal flatten** —
single-resource, single-transport, small handler set may keep handlers in
`routes.go` (`features/events/internal/inbound/events/routes.go` is the
blessed example); candidate to flow back to Segovia's doc.

**D1 ratified (d)** after the owner declined the three post-cut reviews'
keep-`inbound/http` recommendation and withdrew the original
`inbound/domain` lean: the feature inbound package is
`internal/inbound/<feature>/` — Segovia names handler packages for the
DOMAIN, never the transport, so `http/` means plumbing on BOTH sides of
the app/feature line (a feature has none until real plumbing appears).
Root-socket aliases `internalhttp` → `inbound`. The mirror stays
deliberately partial per FS1/FS3: feature templates never co-locate; the
render port lives in the core with the `views/<pkg>` sibling default; the
feature theming seam is embed-the-sibling-default.

Executed as four CI-green commits: task-2 dir renames (32 files, alias +
package + comment lines only); task-3 cms `router.go` → `routes.go` (pure
0-line rename); task-4 authentication `http.go` split → `routes.go`
(package doc, `Mount`, the package-wide `handlers` struct, clientInfo
plumbing) + `sessions.go` (authService port, DTOs, `decode`,
session/account handlers), shared test fixtures re-homed to
`helpers_test.go`, `http_test.go` → `sessions_test.go`; the
`mountOAuth`/`mountMachine`/`mountInvitations` helpers stay in their
resource files — that IS the ratified feature form. `token_test.go` still
pairs with a handler in sessions.go (no token.go) — deliberate,
growth-rule only-split-when-heavy. Live legs, both driven: examples/cms
on Turso (`GET /` 200 rendered, `/terms` 200, `/menus` 200, bogus media
404); auth-cms cookie flow register 201 → verify 200 → login 200 →
logout 200 → stale-cookie 401. `features/README.md` §2 rows rewritten
(anatomy + app↔feature mapping) and the two stale `internal/http`
citations fixed.

**Guard-or-decline (the mechanically-guardable "never `/api`/`/html`/
`/htmx` subdirs" rule): DECLINED for now** — zero such subdirs exist, the
guard family G1–G7 is deliberately import/module-shaped, and the anatomy
is doc- and review-enforced; the named trigger to add a find-over-dirs
guard is the first app-local domain in this repo or the workshop-v2
`gopernicus new domain` scaffold (which should emit this shape and is the
mechanical enforcement point). Flags #2 (sdk/id kind-set — owner
reviewing) and #3 (RELEASING.md old-monolith import-path collision note)
remain QUEUED in the milestone ledger.

## 2026-07-08 — segovia-lessons phase 02 CLOSED as amended: route.go deleted; Methods sugar built-then-DECLINED (D4); per-route override story documented

Flag #4 (owner-raised — feature route tables can't use the sdk's verb
helpers) closed with a shape nobody predicted at cut: **the answer to the
flag was documentation, not surface.** D2 executed — `sdk/feature/route.go`
(`Route`/`RegisterRoutes`, FS7's data form) deleted with zero consumers;
supersession marker in features/README.md; the `capturingRegistrar` test
fixture it hosted re-homed to prefix_test.go. `feature.Methods` (verb sugar
over `RouteRegistrar`, D3 parity with web.WebHandler pinned by reflection
test) was built, tested, all 62 feature registrations converted, both
examples live-driven green — and then **DECLINED by the owner (D4) and
reverted the same day**: one string argument per line becoming a method
name does not buy a permanent exported sdk type, a forever-parity test, a
ceremony line per Mount, and an accept-structs wart in the mountX
signatures. The stringly `r.Handle("POST", …)` form is ruled deliberate
signposting: a feature registers as a GUEST through a one-method seam; an
app-local domain owns the concrete router and gets the verbs free
(segovia's dashboards form). Resurrect trigger: real host-developer demand.
The durable deliverable: features/README.md §4 item 3 — the per-route
override pattern (wrap the registrar: deny/replace/re-path one route in ~8
lines of host code, the `inviteOnly` example), framed as the route-level
face of extension tier 4 and the reason FS7's public override hook stays
unshipped. Session lesson recorded: building the thing was what produced
the evidence to decline it — the accept-structs wart and the per-line
delta were visible only in the diff.

## 2026-07-09 — segovia-lessons phase 03 CLOSED: sdk/id nanoid rework (flag #2); port typing wait-for-demand

Flag #2 closed same-day: cut on the owner's ID-strategy direction (nanoid
shape with custom length/alphabet like the original; no third-party libs
in sdk — UUID needs none, ~15 lines of stdlib). **D5 shipped:** `New()`
(21 chars, ~119.8 bits), `NewCustom(alphabet, size)` (validated: ≥2 chars,
unique bytes, size ≥1 — the workshop-v2 per-aggregate seam the original's
codegen consumed), `UUID()` (canonical lowercase v4), exported
`Alphabet`/`DefaultLength`. Mask rejection sampling + 1.6× buffer ported
from `gopernicus-original/infrastructure/cryptids/id.go`; **the original's
default-alphabet bug fixed** (uppercase Z twice, lowercase z missing —
biased generation toward Z; now 52 unique bytes, guarded by test). Panic
posture split: `New`/`UUID` panic on crypto/rand failure (21 call sites
stay clean); `NewCustom` returns validation errors. Behavior change ruled
acceptable: New() output 26-char base32 → 21-char nanoid (TEXT columns,
zero shape assertions verified, dev data only; segovia inherits via its
replace directive). Live-proven: auth-cms register→verify→login→logout
201/200/200/200 with observed 21-char new-alphabet IDs. **D6:** the
int/uuid PORT-typing half is wait-for-demand — crud stays string-keyed
(uuid flows as canonical strings already; int keys are DB-assigned; zero
consumers embed crud over a non-string key, segovia v2 verified crud-free
and all-string); Getter/Lister split and UUIDv7 likewise deferred with
triggers. **D7 (owner-raised):** pluggable ID generation recorded, not
shipped — app-local code needs no framework support (hosts own their call
sites); the feature-side design is pre-agreed (sdk `id.Generator` func
type + per-feature nil-safe `Config.IDGenerator`, extension tier 2);
trigger = first host needing feature entities keyed by its own generator.
Owner carry-back: flip Segovia flag #2, drop its interim-workaround note.

## 2026-07-09 — segovia-lessons phase 04 EXECUTED (as amended in-session): the cryptids facility; sdk/id retired; ID strategy decided-once, threaded, and store-honored

**D8 (as built):** `sdk/id` folded into `sdk/cryptids` — the original
gopernicus "crypto tidbits" home. One port: zero-arg
`GenerateFunc func() (string, error)`; consumers hold `IDGenerator` (zero
value = the 21-char nanoid default over the fixed 52-byte alphabet;
`Generate`/`MustGenerate`); `NanoID(alphabet, size)` validates ONCE at
wiring (ai/nanoid + matoous/go-nanoid credited — stdlib reimplementation
only because the sdk carries no deps); `Database` = the explicit
delegate-to-the-database strategy. Owner-scaffolded siblings ratified:
`Encrypter`+`AESGCM`, `SHA256Hasher`, `JWTSigner` (port-only).

**D9 AMENDED (owner, in-session, superseding the D9a package-var cut):**
the package-private `var ids` migration "hard-codes the generator into the
feature" — the 21 sites split into two kinds and are treated differently.
ENTITY KEYS (11 sites: auth user/serviceaccount/apikey/securityevent/
invitation; cms entry/asset/menu/menuitem/inquiry/term) take the generator:
each feature `Config` gains `IDs cryptids.IDGenerator` (zero value → nanoid
default), threaded Config → service deps → domain constructor (generator
as first param, `MustGenerate` inside). SECURITY MATERIAL (session tokens,
verification codes/reset tokens, oauth states, API-key prefix+secret,
PKCE/nonce, invitation secrets, sdk/events EventID/Correlation) NEVER
follows the app strategy — package-private `secrets` generators, each with
a doc stating why (a Database-generated session token would be an
empty-string credential). Media's blob `StorageKey` decoupled from the
entity ID for the same reason (its own random component).

**Type ruling (owner):** IDs stay `string`; NO generics. Serial/int keys
parked entirely — when a resource genuinely needs one it becomes an
explicit int field on that resource (no generator func, no text casting;
the store returns the int). Bundled stores stay text-keyed end to end;
native uuid/serial columns are a host-owned adapter+migration concern.

**D10 PULLED FORWARD (owner: "we need to honor that"):** the empty-ID
convention is IMPLEMENTED, not just documented. Every entity-keyed
`Create` in stores/pgx + stores/turso (auth ×5, cms ×6) branches on empty
ID → omit the id column → `RETURNING id` (text columns, no casts).
Schema defaults ship as NEW migrations (checksum guard forbids editing
applied files): pgx `0012_id_defaults.sql` via `ALTER ... SET DEFAULT
gen_random_uuid()::text`; turso `0012` via the SQLite table-rebuild dance
(`DEFAULT (lower(hex(randomblob(16))))`, indexes recreated). Reference/mem
stores (storetest references, examples memstore ×2, authmem) assign at
insert. Conformance suites gained `DBGeneratedIDOnEmpty` per entity family
— the footgun warning retired by proof. Secret-keyed tables get NO branch
and NO default: an empty secret is a bug, never a strategy.

**D11 (recorded):** the port reconciliation already existed — authentication
consumes `cryptids.Encrypter`/`JWTSigner`/`SHA256Hasher`; `PasswordHasher`
stays feature-owned by its recorded rationale; no duplicate vocabulary.

**D12 (owner pulled forward):** `integrations/cryptids/google-uuid` ships
now — `V4()`/`V7()` constructors returning `GenerateFunc` (canonical
lowercase text; V7 recommended for DB keys: time-ordered text form, keyset-
and B-tree-friendly). `go-nanoid` stays DECLINED (attribution comment is
the honest relationship). 31 modules.

Supersession story: phase 03's D5 API lived one day — the build-to-evaluate
work was the evidence that produced the cryptids design; 03 carries the
marker. Segovia carry-over (task-2): `sdk/id` deletion breaks segovia v2's
`id.New()` on next build (live replace directive) — migrate per-domain to
`cryptids.IDGenerator{}`.MustGenerate() or thread its own Config strategy.

## 2026-07-09 — correction: root-doc module counts (phase-04 miss) 30 → 31

Phase 04's NOTES entry claimed "31 modules" but the ROOT docs
(README.md "The thirty modules" heading + `make check` prose,
RELEASING.md count + enumeration, Makefile header, ARCHITECTURE.md
"Thirty modules today") were left saying thirty and omitting
`integrations/cryptids/google-uuid` from the two enumerations — a doc-sync
miss, caught during the authorization-v1 fresh review. Corrected to
thirty-one with google-uuid added to the README and RELEASING enumerations.
The pushed commit `8e426c7` carries the stale counts; these fixes are
uncommitted (fold into the next push). No code affected — go.work and
Makefile MODULES both correctly list 31.

## 2026-07-09 — authorization-v1 EXECUTED: features/authorization, the IAM domain (modules 32–34)

The 2026-07-06 authorization ruling cashed: **supported, never required** —
three postures, all first-class, all demonstrated. Shipped per
`.claude/plans/authorization-v1/` (design of record:
`auth-v2-feature-design.md` §§2/10/13 as amended by the 2026-07-08
multi-kind owner direction): `features/authorization` (module 32) with TWO
independently-wireable KINDS — **relationships** (the ReBAC engine salvage,
re-typed: schema DSL + validator, Check/CheckBatch/FilterAuthorized/
LookupResources, group expansion, through-traversal, platform-admin DATA
tuples, 14-method `relationship.Storer`) and **roles** (`iam_roles`,
5-method `role.Storer`, opaque strings, Q5 global fallback) — plus
`stores/turso` (33) and `stores/pgx` (34), `memstore/` (both kinds, real
graph-walk), `storetest`, and the consumer-seam proof on
`examples/auth-cms`. Tree closed at **34 modules**.

**Ratifications (jrazmi, 2026-07-09, all at recommendations):** Q1 groups
TRIM (return: first named-group UX demand) · Q2 Option A (extend auth-cms;
middle posture = the two-commit protocol) · Q3 store-glue guard ADD · Q4
metadata table TRIM (dead table; returns with its first consumer) · Q5
service-level global fallback (store `HasExactRole` stays exact) · Q6
`Config.IDs cryptids.IDGenerator` minting at the `CreateRelationships`
seam, `cryptids.Database` honored by an omit-column branch + inline DDL
DEFAULTs, NO RETURNING · Q7 second-relation-same-subject = silent no-op
under the original's bare ON CONFLICT DO NOTHING (storetest asserts by
re-read).

**Live-store artifacts (milestone close):**

- **turso** — the authorized playground DB (URL asserted pre-run;
  `TURSO_*` env, `-tags=integration`): **21/21 leaf tests PASS, 40.9s** —
  `Relationship/*` (9 incl. DBGeneratedIDOnEmpty), `Adversarial/
  {MembershipCycle, DeepNesting, DiamondDedup, NestedUserset,
  Unrestricted}` (all five named), `Roles/*` (6 incl. GlobalFallback),
  ExportMigrations. Z2a execution log carries the transcript.
- **pgx** — dockered postgres:17 (`POSTGRES_TEST_DSN`, plain env-gated):
  **21/21 leaf tests PASS, 0.93s conformance** — identical sub-runner set.
  Z2b execution log carries the transcript. **DP1 parity: memstore +
  turso + pgx pass the ONE suite — the flagship provably authorizes
  identically across all three backends.** Both stores' recursive CTEs
  are UNBOUNDED, cycle-safe by UNION dedup (A1: `MaxTraversalDepth` is
  engine-only); `CountByResourceAndRelation` is direct-only (the §2.5
  security pin, asserted in DiamondDedup).

**Z4 protocol (both mandated demonstrations + the roles leg, recorded in
04-consumer-proof.md's log):** commit-1 **`2e1e5eb`** — the middle
posture: a host ownership closure satisfies events' `Authorize` with
`GOWORK=off go list -m all` captured CLEAN (authorization grep-count 0,
no libsql); member stream 200 + heartbeat, non-member 403, unauth 401.
commit-2 **`65fcb49`** — the flagship: schema in main, both kinds
memstore-backed, the toy Granter swapped for `relationshipGranter` →
`CreateRelationships` (design §6 completed), Check-backed stream + gate,
`/demo/my-projects` (LookupResources; platform-admin returned
`unrestricted:true`), `/demo/audit` (HasRole) with scoped assign → 200 →
revoke → 403, and the Q5 global fallback driven live — including the
lead-major-3 divergence OBSERVED: the globally-granted subject passes the
scoped gate but does NOT appear in `ListRoleAssignmentsByResource`
(direct-scope-only enumeration; "effective grants" is a named deferred
item).

**Guards:** `features/authorization` joined the FS1 hardcoded list at Z1
task-1 (prove-can-fail recorded in 01-core's log). Q3 ADD landed at Z5:
**G8 `guard-store-no-foreign-feature`** — every `features/<x>/stores/*`
subtree greps clean of `features/<y>` (y ≠ x) AND `examples/` imports
(the steward-minor-6 alternation; store→examples was previously unguarded
by anything). Prove-can-fail recorded both ways (a features/events import
and an examples/minimal import each failed the target loudly, reverted,
green). `make guard` now runs all EIGHT.

**Kind-boundary enforcement note (deliberate):** kind boundaries are
enforced BEHAVIORALLY — construction validation (`ErrNoKindConfigured`,
`ErrModelRequired`), per-kind sentinels, the userset rejection, and
storetest — never guard-shaped: kinds are intra-module and invisible to
import guards.

**The policy seam (deferral-ledger entry):** the third kind is designed
and NAMED (`domain/policy` rim, nil-safe `Repositories.Policies`,
possibly `iam_policies` at the next migration number) but deferred.
Demand trigger: the first host need neither a relationship model nor a
role lookup expresses cleanly (attribute/condition rules, runtime-editable
rules). The data-driven vs code-registered question is decided at ITS
cut, not now. The feature README documents the seam verbatim in intent.

**Execution notes:** the plan's salvage tree `../gopernicus-original/`
does not exist on this machine — DDL verified against the identical copy
under `/Users/jrazmi/code/menagerie/`; the pgx UNNEST insert + both
dialects' CTEs were DERIVED from the port contract + memstore semantics
(the shared suite is the equivalence proof, which is design risk 3's
mitigation working as designed). Z1 exported no order allow-list from the
domain rims — both stores declare a store-local created_at-only
allow-list matching the memstore contract. The pgx roles keyset tiebreak
is a derived `role_key` chr(1) column (PostgreSQL forbids NUL; cursors
are backend-local).

**Open flags for jrazmi:** (1) the roles listing's direct-scope-only
enumeration (accepted v1 limitation — revisit if a host needs "effective
grants for a resource"); (2) `created_at` carries no DDL default in
either dialect (store-stamped, one timestamp per batch — the id tiebreak
is fully load-bearing for keyset order); (3) the storetest suite leaves
the ALL-non-empty and MIXED CreateRelationships branches hermetically
untested (every conformance case mints via the default engine path);
(4) the `.claude/plans/authorization-v1/` → `.claude/past/` archive move
lands with this close.

## 2026-07-09 — datastore-hardening EXECUTED: connector parity, strictness, scaffolded seams

Same-day cut → gate → execute, autonomously under the owner loop directive
(plan: `.claude/past/datastore-hardening/plan.md`; the post-authorization-v1
connector audit is the origin; owner rulings 1–9 + Q1–Q4 ratified at
recommendations; review gate ran post-ratification — steward
ALIGNED-WITH-EDITS ×9 + lead SHIP-WITH-EDITS ×7 folded, incl. the
convergent retry major and the Transact-not-InTx collision catch).

What shipped (P1–P7, one CI-green commit each): authorization order
allow-lists moved to the domain rims + memstore order validation + the
cross-backend RejectsUnknownOrderField storetest cases · turso List
identifier STRICTNESS (QuoteIdentifier; error-returning append helpers;
raw-expression PK fails page 1) with the roles store reworked to the pgx
derived-`role_key` pattern · `/healthz` on all four hosts (cms DB-probed)
**plus the run-and-look catch: both connectors' StatusCheck was Ping-only
and remote libSQL Ping is LAZY — now Ping + SELECT 1, the 503 path driven
live** · turso query-logging/RedactDSN parity (opt-in, args-verbatim,
tx-path threaded; pgxdb's stale asymmetry records rewritten) · the FULL
turso struct-scan sweep (strict `ScanStruct[T]`, `turso.Time/NullTime/
Bool` Scanner types, ~23 row structs + toDomain across five stores, zero
hand-scan callbacks left; write-side helpers renamed FormatNullTime/Ptr)
· `crud.Transactor` scaffolded UNCONSUMED (`Transact`, pinned semantics,
no sdk ctx stash, nesting unpinned) + guards **G9 no-Underlying** and
**G10 no-Lax** (prove-can-fail recorded; `make guard` runs TEN) · opt-in
`Config.Retry` (connection-acquisition-only, full-jitter, ctx-budgeted;
zero value = today's behavior byte-for-byte).

Live close: all TEN store suites green (`make test-stores` — asserted
playground + dedicated C-locale postgres:17); examples/cms rendered live
through the swept row structs; healthz 200s.

Open flags for jrazmi: (1) **auth-pgx storetest id-tiebreaks are
collation-sensitive** — deterministic FAIL on default-locale postgres,
PASS on `--locale=C`; pre-existing, out of milestone scope; fix = pin
`COLLATE "C"` on tiebreak ORDER BYs or pin the container locale in docs.
(2) examples/minimal still has no README (P3 premise-false). (3)
`tursodb.Open`'s zero-value lazy ping stands (opt-in Retry = eager boot
validation; feature-store probes remain the boot validator). (4) The
retry loop's budget is ConnectTimeout — many attempts × long backoff
needs it raised. (5) A raw DSN inside a wrapped DRIVER error is not
scrubbed (parity with pgxdb; hosts use Config.Redacted()).

## 2026-07-09 — workshop-v2-scaffolding EXECUTED: the scaffolding CLI (module 35)

The scaffold-once slice of the workshop-v2 brief, same-day cut → gate →
execute (plan archived at `.claude/past/workshop-v2-scaffolding/`;
ratified Q1–Q5 at recommendations; gate: steward ALIGNED-WITH-EDITS ×8 +
lead SHIP-WITH-EDITS ×7, 15 findings folded — headline: the convergent
stdlib-vs-drivers contradiction resolved by DELEGATION, `db
migrate/status` exec the host-emitted runner and the CLI's go.mod keeps
ZERO require lines, structurally).

Shipped (W1–W5, one CI-green commit each): `workshop/gopernicus` (module
35, a NEW sixth taxonomy kind: the workshop tool — emits anatomies,
never links them; guard **G11 workshop-boundary**, proven-can-fail both
directions — `make guard` runs ELEVEN) · `gopernicus init` (host
scaffold over 7 .tmpl templates: sdk-only composition root, host
Makefile with the one-rule + G9/G10 shapes, pre-tag replace block,
migrations runner with `-status`) · `gopernicus new feature` (26
templates: the FULL charter skeleton — FS2 socket, order.go, the
checklist-14 IDs seam, six-case storetest family + DBGeneratedIDOnEmpty,
public memstore, BOTH dialect stores with inline id DEFAULTs and pinned
driver versions) · `db create/migrate/status` (pure-FS create; delegated
migrate/status with the file-only fallback) · docs/taxonomy/counts (35
modules everywhere).

**The drift answer (load-bearing):** scaffold-compile tests inside
`make check` — every run emits a host + feature into temp dirs, absolute-
replaces, builds (`GOWORK=off`; hermetic legs offline, driver legs
`GOPROXY=off` warm-cache), runs the emitted feature's storetest against
its emitted memstore (11 cases), and greps the guard shapes over emitted
output. Templates are `.tmpl` (invisible to repo guards) — these tests
are the only gate on emitted content, named as guard infrastructure.

Live proofs recorded in the plan's execution log: emitted none-host
BOOTED (healthz 200, clean shutdown); emitted `notes` feature hand-wired
into an emitted host, boot-time create+list proof logged; the full db
verb chain against a throwaway postgres:17 (create → migrate → status →
DB-down file-view). Deferred to workshop-v2b with named triggers: store
emission, TS clients, new-domain, doctor/sqlguard, the test harness.

Open flags: (1) the pinned driver versions in `feature.go`'s const block
must bump together with the repo stores' (comment says so — a
consistency-check test is a cheap future add); (2) G5's hardcoded
feature list remains the one manually-extended guard (the CLI prints the
checklist; making G5 glob-driven is a small standalone item); (3) emitted
hosts need the pre-tag `replace` block until repo-hardening phase 5 cuts
first tags — LICENSE remains that gate.


## 2026-07-10 — identity-resolution EXECUTED: sdk/identity Resolver + sdk/notify + kind-aware invitations

The first milestone under the ratification contract (recorded the same
session): owner-ratified direction ("generic identity resolver without
the User struct; drop the email-only identifier constraint"), REWRITTEN
mid-flight by owner direction to NOTIFIER-FIRST after the token-bearer
trust-model walkthrough — the wired notifier set now DEFINES a host's
supported identity kinds (deny-by-absence per kind), and invitation
tokens are DELIVERED for every kind; no plaintext hand-back exists
anywhere. Plan archived at `.claude/past/identity-resolution/`; three
gates ran (the initial pair + a delta gate on the rewrite), every one
caught at least one would-have-shipped major (accept is email-match; the
token is hashed at rest; the drafted deny-by-absence predicate would
have denied EMAIL invitations on every existing host; MailerBridge had
no From; sendMemberAdded was a second unforked send path).

Shipped: **sdk/identity** grows `Address{Kind,Value}`, `Info` (a
projection — NO User struct in sdk, ever), fail-closed `Resolver`,
strict `ResolveAll` (P1, `feb68fb`) · **sdk/notify** (new):
`Notifier{Kind,Notify}` + `Console` + From-carrying `MailerBridge`; no
Set helper (one consumer) (P2, `e57114b`) · **authentication**:
`auth.Service` implements `identity.Resolver` (nil-guarded, machine-off
safe); `Config.Notifiers` with loud duplicate-kind rejection; kind-aware
invitations — supported-kind predicate (email always-on via the required
Mailer), service-owned kind-aware normalization, the delivery fork over
BOTH send paths, kind-conditional accept (address-possession binding for
non-email kinds), email-keyed auto-paths filtered to email; migration
**0013** both dialects (column + pending-index rebuild) (P3, `c8e4a7f`).

Live proofs: both dialect conformance suites green with 0013 applying
live (pgx 4.8s C-locale docker; turso 460.9s playground, URL asserted);
the A9 email leg code-exact on an UNCHANGED host; the close drive on
auth-cms — phone invite 201 → token VISIBLY DELIVERED by the console
notifier → accepted by token 200 → grant live 200; slack kind (unwired)
400; email 201 throughout.

Deferred ledger: provider integrations (`integrations/notify/<tech>` —
trigger: the first host wiring real SMS/Slack; Segovia likely) · address
verification (enables non-email account-match binding) · authorization
grant-notifications over this port · unifying verification/reset mail
onto notify (the documented invitations-only asymmetry) · tenancy.

Open flags for jrazmi: (1) **invitation ownership** — raised directly
with the P3 executor; its read (endorsed): keep invitations in
authentication (AV4 + the Granter seam already invert the coupling;
relocation would drag Mailer/Notifiers/identity into IAM) — a move needs
its own plan; (2) **securityevent** straddles both features — candidate
for its own audit facility, separate decision; (3) the invitation JSON
response doesn't surface `identifier_kind` yet (request-side only, noted
in the auth README).


## 2026-07-10 — sdk-layering EXECUTED: kernel / foundation / capabilities (the intra-sdk import law)

Owner-driven from the "sdk packages importing sdk packages is a huge code
smell" conversation: the audit showed a 13-edge DAG with unnamed strata;
the ratified answer NAMED and PHYSICALIZED them. Plan archived at
`.claude/past/sdk-layering/`; gate: steward ×7 + lead ×6, two design
forks decided at the fold (id-context → kernel; workers tracing glue
DELETED — the "local interface" alternative was unimplementable, return
types compare by identity).

The law (ARCHITECTURE carries it): root `package sdk` = the KERNEL
(errors.go + context.go; stdlib-only; cycle-enforced against importers,
G12(a) for the rest) ← `foundation/` (11 packages, root-only, FLAT) ←
`capabilities/` (8 packages, root+foundation, never each other) ←
`feature` (the one sanctioned composer). Cross-capability composition
leaves sdk: `integrations/notify/mailer` (module 36, the first COMPOSING
integration — taxonomy amended, guarded).

Shipped P1–P5, one CI-green commit each: errs → root (`sdk.ErrNotFound`;
132 import files + 38 doc-comment files; the validation local-`errs`
trap avoided) · the evictions (web.CachePages → **cacher.Pages**,
web.Tracing → **tracing.Middleware**, StatusRecorder exported,
web.RequestID kernel-backed; workers tracing glue deleted with the
reintroduction home named) · the physical split (146 renames, 359 .go +
11 .tmpl + 19 .md swept, zero old paths) · the mailer integration
(`mailer.New/Bridge`, stutter-free rename, zero consumers) · guards
**G12 sdk-layering** (three-direction prove-can-fail; tests exempt with
the two env round-trip tests cited) + **G13 integration-no-inward**
(load-bearing once composing integrations exist) — `make guard` runs
THIRTEEN.

End-state audit: web → root only; workers → nothing under sdk; every
edge downward; the close drive re-ran the identity-resolution
email+phone invitation legs live on the re-pathed stack — identical
codes, token delivered, port free.

Open flags: (1) `foundation/logging/context_test.go` pairs with
`handler.go` now — trivial rename skipped as out-of-named-scope; (2) one
P1-era `sdk/errs` prose comment in sendgrid.go swept at P5 — none
remain; (3) traced-workers reintroduction trigger stands (first host
wanting it → a decorator in capabilities/tracing).

## 2026-07-11 — authorizer `Check` is pure schema evaluation (segovia-lessons 05)

Downstream feedback from segovia v2 (first real consumer of
`features/authorization`): the two policy short-circuits that ran before any
schema rule — `checkPlatformAdmin` (probed `platform:main#admin@<subj>` on
EVERY check) and `checkSelf` (hardcoded `user`/`service_account` self-access on
`read`/`update`/`delete`) — are REMOVED from the engine. **The engine evaluates
the schema; policy short-circuits are host composition.** Both bypasses now fail
CLOSED as documented host closure recipes.

- **D1 full removal** (not opt-in Config): `Check`, the `checkBatchOptimized`
  fast path, and `LookupResources` no longer bypass. `Reason` vocabulary shrank
  — `"platform:admin"` and `"self"` are gone (informational only, never a
  contract).
- **D2 CORRECTION** — the feedback proposed dropping `CheckRelationExists` from
  the `relationship.Storer` port "if the engine is its only caller." It is NOT:
  `RemoveMember`'s last-owner probe (§2.5 pin), the public
  `Service.CheckRelationExists` dedup primitive, and 7 storetest conformance
  cases all call it. **The port method stays**; only the engine's internal
  `checkPlatformAdmin` call site went away.
- **D3 `LookupResult.Unrestricted` removed** — its only producer was
  `checkPlatformAdmin`, so it became a permanently-false dead field.
  `LookupResources` is now pure enumeration (IDs always non-nil, empty = no
  access). Ripple: auth-cms `/demo/my-projects` JSON contract changed from
  `{"unrestricted",...}` → `{"admin",...}` (host-composed flag).
- **Host recipes** (canonical, in the feature README): platform-admin = declare
  a `platform` type with an `admin` PERMISSION + run its `Check` first in the
  host closure (`isPlatformAdmin` in auth-cms `membership.go`, used by
  `requireMembership` + `demoMyProjects`); self-access = ~8-line ID-equality
  check in the host closure (auth-cms/segovia carry none). The
  `platform:main#admin` DATA tuple and "admin is DATA, never Config" are
  unchanged.
- Engine tests flipped: `TestCheckPlatformAdminIsNotMagic` /
  `TestCheckNoImplicitSelfAccess` / `TestLookupResourcesPlatformAdminIsNotMagic`
  + storetest `Adversarial/PlatformAdminIsNotMagic` prove a tuple holder is
  DENIED on an unrelated resource and gets no implicit self-access. Green on
  memstore + pgx + turso conformance.

Downstream: segovia v2 adds the platform-admin closure atop its
`access.Checker.Check` + the `admin` permission rule to `BuildSchema()` (flags
#4–#7); it never used checkSelf.
