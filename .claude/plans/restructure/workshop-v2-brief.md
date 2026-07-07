# Workshop v2 — scope brief for the future codegen milestone

Status: FINAL — phase 5 deliverable of the `restructure` milestone, 2026-07-02
(mid-tier draft reviewed, corrected, and extended by the phase executor;
changes recorded in `05-workshop-v2-brief.md`'s execution log)
Depends on: `00-overview.md` (constitution), `04-capability-map.md` (62-row
classification, referenced not re-decided here), `auth-feature-design.md`
(the build that must land before this milestone starts), `features/README.md`
(the charter workshop v2 must generate into).

This is a **scope brief, not an implementation plan**. It says *what* a future
milestone must generate and *where each thing lands*; it does not design a
generator, choose a template engine, or write code. Per the standing rule
("codegen follows design"), nothing in this document is built by this phase.

## Why this brief exists now, and why it stops here

The original gopernicus's generator caused the architecture flaw this whole
restructure milestone exists to fix: it emitted driven adapters
(`core/repositories/auth/users/userspgx/`), cache decorators, and even
composition-root wiring (`generated_composite.go`) *into* `core/` — because
generation targets were wherever the generator happened to put files, not a
designed placement (constitution, "Verdict this milestone encodes"). Workshop
v2 must generate into the ratified structure from phases 1–4 instead — and
per capability-map.md's W4 build order, that structure must survive real use
first (workshop v2 is explicitly last: W4 items 1–7 before item 8). A codegen
plan written before that proof exists would be speculative in the same way
the original's generator was.

Concretely, before the codegen milestone can even be *planned*:

- `features/auth` v1 (design: `auth-feature-design.md`, not yet built),
  `integrations/cryptids/bcrypt`, and `integrations/datastores/postgres`
  built, with the auth+cms two-feature proof host passing (W4 items 1–3) —
  the structure's second full proof after cms.
- `features/jobs` and `features/events` at least designed (W4 items 4–5) —
  the third and fourth proofs, and the first features whose store adapters a
  generator would plausibly emit rather than hand-write.
- The capability map's nine pending YOUR CALL rows resolved by jrazmi where
  they gate generation scope — at minimum #9 (integration-test harness as
  workshop-v2 scope) and, from experience building auth's admin surface, the
  bridge-generation question in §4 below.

## 1. Generation targets in the new world

Each target below maps to a ratified home from the constitution and
`04-capability-map.md`; none of these are re-decided here.

- **Host scaffold** (`gopernicus init`) → emits a `cmd/server` composition
  root (explicit wiring per constitution rule 5 — no `init()`, no service
  locator), the phase-1 Makefile guards, a workshop/migrations runner, and
  `.env.example`. Replaces the original's `app/` template emission
  (`workshop/gopernicus/commands/init.go`, `commands/new.go`).
- **App-local domain scaffold** (`gopernicus new domain`) → `internal/logic/
  domains/<domain>` (entity + service + port) plus an `internal/outbound`
  store skeleton, inside a *host app*, never inside a `features/*` module.
  Hexagon name is `internal/logic` per D5 (never `sol`).
- **Feature skeleton** (`gopernicus new feature`) → the charter anatomy in
  `features/README.md` §2 as a compilable skeleton: `<name>.go`
  (`Repositories`/`Config`/`Register`), a `<domain>/` package, `internal/
  <domain>svc/`, `internal/http/`, a `stores/<dialect>/` module, and a
  minimal-host proof harness (§3's checklist item 6). Must respect C1: the
  skeleton documents its claimed route namespace and works under
  `feature.PrefixRegistrar`; whether generated views inherit cms's known
  absolute-links gap (`features/README.md` §4) or get base-path-aware URL
  building from the start is genuinely undecided — listed in §4 below.
- **Store adapter emission** → from an entity spec, emit a `stores/<dialect>`
  implementation of a domain's ports, landing *inside the store module*, never
  the feature core (constitution rule 4). Candidate engine: the original's
  `infrastructure/database/crud` Spec/Store/Dialect design (verified present,
  §2 below) — already proven against postgres+sqlite, with `specstore.go`
  demonstrating spec-from-`queries.sql` extraction works. **Undecided in this
  brief**: runtime generic engine (a `crud.Spec` value interpreted at
  request time) vs. fully-emitted literal Go code vs. a hybrid — this is
  design work for the future milestone, not scoped further here.
- **Migrations tooling** → `db migrate/status/create` verbs plus schema
  reflection, aligned with the scaffold-and-own ledger model (D4): each
  `stores/<dialect>` owns its canonical migration SQL and registers a
  `MigrationSource.Name` in the host's shared ledger. The scaffold seam
  already exists — `ExportMigrations` (`features/cms/stores/turso/turso.go`)
  is "already scaffold-shaped" per D2's own rationale; extend that seam
  rather than inventing one. Schema *reflection* needs a live database at
  generation time — an infrastructure assumption the milestone must decide
  (§4).

## 2. What carries over from the original (by reference, with verified paths)

Every path below was confirmed to exist via read-only `find`/`ls`/`grep`
against `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original` during
this phase (2026-07-02):

- **`queries.sql` annotation language and its parser**:
  `workshop/codegen/generators/parse.go` + `resolve.go`. The phase file names
  `-- @func`/`@filter`/`@order`/`@search`/`@cache`/`@event`; the verified
  vocabulary is larger (`@fields`, `@max`, `@returns`, `@type`, `@check`,
  `@scan`, `@fixture` also appear in the parser/resolver and the SQL corpus) —
  what carries over is the whole language, not the six-example list. Live
  annotated sources: ten `queries.sql` files under `core/repositories/auth/`
  (e.g. `.../verificationtokens/queries.sql`; `@func` alone has 140 uses).
  Placement note feeding §4: in the original, `queries.sql` sits in the
  *consumer's* package beside `repository.go` and the generated
  `userspgx/`/`usersstore/` output — a single-module luxury the new module
  boundaries take away.
- **The bootstrap-vs-generated split + `// gopernicus:start|end` surgical
  markers**: `workshop/codegen/generators/markers.go` (confirmed; also
  referenced from `repository_tmpl.go` and covered by `markers_test.go`).
- **Determinism + drift-as-CI-failure**: enforced in the original by CI, not
  by template tests — `.github/workflows/ci.yml`'s "Verify no drift" step
  regenerates and fails the build on `git diff --cached --exit-code`.
  (Corrects an earlier draft's claim that every `*_tmpl.go` had a paired
  determinism test: verified false — 19 templates, 25 test files, and the two
  most load-bearing templates, `repository_tmpl.go` and `pgxstore_tmpl.go`,
  have no dedicated test. The *discipline* carries over; the original's test
  coverage of it must not be over-credited.) Where this gate runs in a
  multi-module world is a new open question (§4).
- **The crud Spec/Dialect engine and its sqlite fixtures**:
  `infrastructure/database/crud/{spec,store,dialect,dialect_postgres,
  dialect_sqlite}.go`, plus dialect-specific query builders
  `infrastructure/database/crud/pgxq/pgxq.go` and `infrastructure/database/
  crud/sqliteq/sqliteq.go` (all confirmed present). SQLite-specific test
  coverage: `infrastructure/database/crud/crud_test.go` (confirmed to
  reference sqlite). The "golden reference" showing what a future generator
  would emit: `core/repositories/auth/users/usersstore/store.go` (confirmed;
  its own doc comment states it is "the GOLDEN REFERENCE for generator-v2
  output").
- **The `bridge.yml`-driven CRUD admin-bridge pattern** (HTTP generation
  input, not itself carried over as an approach — see §4): example files
  confirmed present at `bridge/repositories/authreposbridge/usersbridge/
  bridge.yml` and `bridge/repositories/tenancyreposbridge/tenantsbridge/
  bridge.yml`, among others.
- **Conformance suites as generated-adapter acceptance tests**: NOT an
  original-repo capability — this is prior art already established in *this*
  restructure by phase 2's kernel hardening: `sdk/cacher/cachertest/
  cachertest.go`, `sdk/filestorage/filestoragetest/filestoragetest.go`,
  `sdk/ratelimiter/ratelimitertest/ratelimitertest.go`, and `examples/minimal/
  internal/memstore/memstore_test.go`'s per-repository pattern. Workshop v2's
  job is to reuse this shape for generated store adapters, not invent a new
  one.

Two more source directories were confirmed present and are worth naming
alongside the above since they are the direct backing for the generation
targets in §1 (schema reflection and the CLI dispatcher):
`workshop/codegen/schema/` (`schema.go`, `checkenum.go`); the reflectors +
migrators at `workshop/codegen/database/sqlite/{reflector,migrator}.go` and
`workshop/codegen/database/postgres/pgx/{reflector,migrator}.go`; and
`workshop/gopernicus/{main.go,commands/**}.go` (the `doctor`/`db`/`boot`/
`generate`/`init`/`new` command dispatcher).

## 3. What explicitly dies (named replacement for each)

- **Generating driven adapters, cache decorators, or composite wiring into
  the domain core** (the original's `core/repositories/auth/users/userspgx/`
  and `generated_composite.go` pattern) — **replaced by**: store adapters
  emitted into their own `stores/<dialect>` module (constitution rule 4);
  composition wiring stays hand-written in a host's `main` (constitution
  rule 5 — no generated service locator, ever).
- **Ports owned by the implementor's layer** (the original's
  `infrastructure/` owning ports like `oauth.Provider`/`cache.Cacher` that
  `core` consumed) — **replaced by**: ports declared by the consumer
  (constitution rule 3), already exercised by every sdk facility
  (`cacher.Storer`, `email.Sender`) and by `auth-feature-design.md`'s
  `PasswordHasher`/repository ports. A generator targeting the new structure
  must emit ports into the *consuming* feature/domain package, never into an
  adapter package.
- **The single-module dependency tax** (the original was one `go.mod` for the
  whole framework, so any generated adapter's third-party dependency became
  every consumer's transitive dependency) — **replaced by**: per-module
  generation that respects module boundaries (constitution rule 2) — a
  generated store lands in its own module with its own `go.mod`; a generated
  integration adapter lands in `integrations/<category>/<tech>` with its own
  `go.mod`.

## 4. Open questions for the future milestone (listed, not answered)

- **Bridge/handler generation vs. the registry-driven-routes pattern.**
  `features/cms` needed zero generated HTTP routes (`internal/http/router.go`
  is hand-written, registry-driven). Does anything in the new structure
  actually need generated route handlers, or does every feature end up
  looking like cms? If nothing needs it, the original's `bridge/repositories/
  {authreposbridge,rebacreposbridge,tenancyreposbridge}` CRUD-bridge pattern
  (§2) may not carry over as an *approach* even though its `bridge.yml` input
  format might inform something else.
- **TS/OpenAPI client generation placement.** `sdk/web`'s runtime OpenAPI
  builder is already ratified (capability-map.md, sdk-shaped utilities
  section) as `sdk/web`, not workshop v2. TypeScript *client* generation from
  that OpenAPI document is workshop v2 by rule 4, but its exact trigger point
  (per-feature? per-host? checked into the repo or a build step?) is
  undecided.
- **`bridge.yml`-style HTTP config vs. code.** The original drove CRUD-bridge
  generation from a YAML config per entity. Whether a future generator wants
  a declarative config file or reads directly off Go types/`queries.sql`
  annotations is open.
- **How generated feature stores version against their feature core.** A
  `features/<name>/stores/<dialect>` module is independently tagged (C4). If
  a generator regenerates a store adapter, what dictates whether that's a
  patch/minor/major bump relative to the feature core it implements? Open.
- **Whether `doctor` returns.** The original's `doctor` command
  (`workshop/gopernicus/commands/doctor.go`; SQL-injection lint via
  `workshop/codegen/sqlguard/sqlguard.go`, general project-health checks) has
  no assigned home yet — folding into workshop v2's CLI dispatcher is the
  natural default but isn't decided. (Path note: `capability-map.md` places
  `sqlguard` under `generators/`; the verified location is
  `workshop/codegen/sqlguard/` — flagged, not silently edited there.)
- **Whether the runtime-generic vs. fully-emitted-code choice for store
  adapters (§1) also applies to feature skeletons**, or whether feature
  skeletons are always fully emitted (scaffolded once, then hand-edited) while
  only store adapters get a runtime-generic option. Open.
- **C1's absolute-links limitation and generated views.** If a generated
  feature ships server-rendered views (mirroring cms), does the generator
  bake in base-path-aware URL building from the start, or does it inherit
  cms's known gap (`features/README.md` §4) until a separate hardening pass
  fixes it for everyone at once? Open — noted in §1 above, not resolved here.
- **Integration-test harness generation.** `04-capability-map.md`'s YOUR CALL
  #9 recommends treating `workshop/testing/{testpgx,testredis,testsqlite,...}`
  as workshop-v2 scope, built alongside the first feature needing
  testcontainers-backed tests. This brief does not further scope *how* —
  whether it's a generated harness per feature or a shared library workshop
  v2 features import.
- **Where the entity spec (annotated `queries.sql`) lives when one spec
  drives two modules.** In the original, `queries.sql` sat beside its
  consumer in one module (§2's placement note); in the new world a single
  spec plausibly informs the feature core (ports/entities, constitution
  rule 3) *and* the `stores/<dialect>` module (SQL, migrations) — two
  modules with a hard import boundary between them. Does the spec live in
  the store module (it is SQL, dialect-flavored), the feature core (it
  defines port semantics), or somewhere dialect-neutral feeding both? And
  since spec extraction leans on schema *reflection* (`specstore.go`,
  `workshop/codegen/database/*`), which needs a live database at generation
  time: what infrastructure does `gopernicus generate` get to assume? Open.
- **Which generation surfaces keep the `// gopernicus:start|end` markers
  under D2's hybrid model.** The original used markers to regenerate inside
  files a human also owned (`core/repositories/auth/*/repository.go`). D2's
  split — scaffold-once-then-owned surfaces (host/domain/feature scaffolds,
  the `ExportMigrations` shape: no regeneration, arguably no markers) vs.
  regenerate-forever surfaces (store adapters, TS clients: plausibly whole
  generated files, where markers are also unnecessary) — leaves it open
  whether *any* new-world surface still needs mid-file surgical
  regeneration, the only case markers exist for. The marker machinery
  (`markers.go`) carries over as capability either way.
- **Where drift-as-CI-failure runs in a multi-module, multi-repo world.**
  The original's gate was one CI step in one module (§2). The new ecosystem
  has 6+ modules under `go.work` with a root `make check`, generated store
  modules living *here*, and scaffolded host/domain code living in a user's
  *separate* repo gopernicus never sees again. Per surface: does the drift
  gate run in this repo's `make check`, in the emitted host Makefile (which
  already ships the four phase-1 guards — drift would be a fifth), both, or
  not at all for scaffold-once output? And must generated output itself pass
  the four layering guards? Open.

## Self-check against the phase spec

All four required sections present (§1–§4); every carried-over item in §2
cites a verified original-repo path; every dead item in §3 names its
replacement; no generator design and no code anywhere in this document. The
phase's acceptance run, real-interaction check, and execution log live in
the phase file (`05-workshop-v2-brief.md`), not here.
