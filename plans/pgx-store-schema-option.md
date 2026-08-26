# stores/pgx: `WithSchema` — host-chosen schema for every feature pgx store

**Status: RATIFIED 2026-08-26 (in-session; all seven YOUR CALLs at their defaults). S1–S7 EXECUTED 2026-08-26; S8 docs written, pin moves + tags are the owner's — see the execution log and tag manifest at the end of this file.** Origin: gopernicus
issue #4 (gps-360-go shares gps-360's Postgres and today carries a
`search_path` DSN wrapper it would delete the day this option exists). v2
folds in the data-integration and lead-backend reviews (both
"ship-with-edits"; the datastore reviewer executed all five migration
streams under a strict `SET LOCAL search_path` on a throwaway PG17 — all
clean, zero objects in `public`, `COLLATE "C"` preserved). v3 folds in the
ratification review: Postgres-specific schema validation, an executable
ledger-relocation preflight, explicit cross-schema non-atomicity, a complete
privilege matrix, and tightened verification/compatibility claims. Owner cuts
the tags.

## Problem (from #4, verified against HEAD)

Every feature pgx store writes SQL against unqualified table names — 215
statements over 29 tables across `features/{authentication,authorization,
cms,events,jobs}/stores/pgx`. All are function-local `const` backtick
literals with static table names EXCEPT three package-level SQL fragments
that also carry table names (`reachableCTE` and `boundedReachableCTE` in
`authorization/stores/pgx/relationships.go:32,62`; `userSummarySelect` in
`authentication/stores/pgx/user_admin.go:50`). No Sprintf, no builder, no
cross-feature FKs, no explicit schema or `search_path` anywhere in Go or
migrations. A host that wants the feature tables in a dedicated schema
(`auth.users`) has only the pool-wide `search_path` pin: global, hidden, and
it silently relocates the host's own unqualified statements too.

## Decision

Additive, default unchanged. Four moves:

1. **pgxdb owns the schema seam.** `integrations/datastores/pgxdb` gains a
   validated value type — `pgxdb.Schema`, `pgxdb.NewSchema(name string)
   (Schema, error)`, `(Schema).Table(name string) string` — one validation
   site (the existing `identifierSegment` rule in `identifier.go` plus the
   Postgres schema restrictions below), one qualifier implementation. The zero
   `Schema` renders bare names, so
   `Schema{}.Table("users") == "users"` byte-for-byte. This is ALSO the seam
   for a host's own app-local pgx repositories (gps-360-go's
   `internal/outbound`), which is the "individual repository layer too" ask
   the five feature stores alone would not cover. It is a pgxdb (connector)
   surface, deliberately NOT an sdk contract: schema qualification has no
   honest SQLite/turso implementation and fails ARCHITECTURE.md's sdk
   five-point test on its first point.
2. **Every pgx store package gains `Option` + `WithSchema(s pgxdb.Schema)`**,
   accepted by `Repositories(db, opts...)` AND by every exported
   per-repository constructor (`NewUserStore(db, opts...)`, events'
   `New(db, opts...)`, authorization's `RelationshipRepository(db,
   opts...)`), so a host composing its own `auth.Repositories` from
   individual stores gets the same seam. No option, or a zero `Schema` →
   today's SQL byte-for-byte. `WithSchema` never panics on a malformed name:
   validation happened in `NewSchema` at the host.
3. **Runtime SQL is qualified through one greppable chokepoint per store** —
   an unexported `s.table(name)` method delegating to `Schema.Table` — never
   through `search_path`. With a schema set, every table reference renders
   `"<schema>".<table>`.
4. **Migrations: the pgxdb runner takes the schema; the exported ledger stays
   byte-for-byte.** `pgxdb.RunMigrations(ctx, db, fs, dir,
   pgxdb.WithSchema(s))` runs `SET LOCAL search_path TO "<schema>"` inside
   the migration transaction (transaction-scoped — the opposite of the DSN
   pin) so unqualified DDL lands in the schema, and keeps an explicitly
   qualified `schema_migrations` ledger there. **This ratifies a change to
   the runner's stream model** (see "Migration stream model" below).
   `ExportMigrations` is untouched.

Non-goals (unchanged from #4): no change to repository ports, feature
services, memstores, or the default SQL. `stores/turso` is out of scope —
SQLite has no schemas (`ATTACH` is a different mechanism nobody has asked
for; no turso store references it). Per-repository DIFFERENT schemas within
one feature are out of scope: the constructors mechanically allow it, but
the one-stream-per-schema migration model gives it no story, and the READMEs
say so.

## Design

### pgxdb: `Schema`

```go
// Schema is a validated Postgres schema name. The zero value means "no
// schema": Table renders bare names, so a store constructed without one
// emits exactly the SQL it always has.
type Schema struct{ name string }

// NewSchema validates name against the identifier segment rule (letter, then
// letters/digits/underscores; single segment, no dots) and the Postgres schema
// restrictions below, and wraps sdk.ErrInvalidInput on failure. Quoting
// preserves case: "Auth" and "auth" are different schemas.
func NewSchema(name string) (Schema, error)

// Table qualifies table with the schema — "<schema>".table — or returns it
// bare for the zero Schema. table is trusted (a store's own literal), never
// host input.
func (s Schema) Table(table string) string
func (s Schema) IsZero() bool
func (s Schema) String() string   // the bare name; "" for zero
```

Rendering `"auth".users` (schema quoted, table bare) keeps the default
rendering byte-identical and matches `QuoteIdentifier`'s quoting.

`NewSchema` is stricter than the general-purpose `QuoteIdentifier`: it rejects
the empty name, names longer than Postgres' default 63-byte identifier limit,
the reserved `pg_` prefix (case-insensitively, conservatively), and the built-in
`information_schema` namespace. `public` remains valid as an explicit schema.
This prevents server-side truncation/collision and keeps a superuser from
accidentally targeting a system namespace. The existing ASCII allow-list makes
the byte-length check unambiguous. Tests pin 63 bytes valid, 64 invalid, all
reserved-name cases, case preservation, and `errors.Is(err,
sdk.ErrInvalidInput)`.

### Store option shape (per package)

```go
// Option configures the store set at construction.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
	// authorization: + guardian mutation.GuardianPolicy (existing)
	// jobs:          + lease time.Duration (moves here from Queue)
}

// WithSchema places every table this store touches in s. The zero Schema is
// the default (unqualified). Build s with pgxdb.NewSchema at the host so a
// malformed name fails there, before any store is constructed.
func WithSchema(s pgxdb.Schema) Option
```

- Each store struct gains `schema pgxdb.Schema`, set by its constructor, and
  a one-line `func (s *userStore) table(name string) string { return
  s.schema.Table(name) }` (or one shared unexported helper type the stores
  embed — implementer's choice). Contract: every table reference in the
  package renders through `.table("`/`.Table("`; the bare-reference test below
  guards the current statement forms and the schema conformance leg proves the
  paths it executes.
- **Statement mechanics:** function-local `const q = \`... FROM users ...\``
  becomes `q := \`... FROM \` + s.table("users") + \` ...\``. The SQL stays
  next to its method; Go folds the constant halves; pgx's statement cache is
  keyed on rendered text, stable per store instance. Per-call concatenation
  is a few hundred bytes against a network round trip and is NOT a review
  point; an implementer may render a hot-path statement (jobs `Claim`) once
  in the constructor if they prefer, nothing requires it. The three
  package-level fragments named in §Problem become `func reachableCTE(s
  pgxdb.Schema) string` etc. Column-list constants are unaffected.
- **YOUR CALL 1 (alternative):** `WithSchema(name string)` panicking through
  `pgxdb.NewSchema` at wiring, keeping #4's literal `WithSchema("auth")`
  ergonomics. Repo precedent exists for wiring-time panics
  (`sdk/foundation/workers/runner.go:138`, `authorization/middleware.go:33`),
  but schema names arrive from host env/config in practice, and the
  value-type form costs the host two lines and zero panics. Default: value
  type.

### Package specifics

| Package | Today | After |
|---|---|---|
| authentication | `Repositories(db)`; 18 exported `NewXStore(db)`; no Option | `Repositories(db, opts ...Option)`; every `NewXStore(db, opts ...Option)`; `Option`/`WithSchema`; `userSummarySelect` → func |
| authorization | `Option`/`config{guardian}`; `Repositories(db, opts...)`; `RelationshipRepository(db)`; unexported `newXStore` | `config` gains `schema`; `RelationshipRepository(db, opts ...Option)`; unexported constructors take `config`; the two CTE consts → funcs |
| cms | `Repositories(db) cms.Repositories`; 5 exported `NewXStore(db)`; no probe | `Repositories(db, opts ...Option)`; `NewXStore(db, opts ...Option)`; **new additive `StatusCheck(ctx, db, opts ...Option) error`** (see Probes) |
| events | `New(db *pgxdb.DB) (*Store, error)` | `New(db *pgxdb.DB, opts ...Option) (*Store, error)` |
| jobs | `QueueOption func(*Queue)`; `WithLease`; `Repositories(db, opts ...QueueOption)`; `NewQueueStore(db, opts ...QueueOption)`; `NewScheduleStore(db)`; `NewFencedQueueStore(db)` (not wired by `Repositories`, by design) | `type Option func(*config)`, `config{lease, schema}`; **`type QueueOption = Option` (alias, kept)**; `WithLease` returns `Option` and keeps its non-positive-ignored rule (pinned by a test); all three constructors take `opts ...Option` — `NewScheduleStore`/`NewFencedQueueStore` document that lease is accepted and ignored; `Repositories` signature text unchanged; **new additive `StatusCheck(ctx, db, opts ...Option) error`**. Compatibility: values returned by `WithLease(d)` still compile, but any caller that constructs, converts, invokes, returns, or wraps its own `QueueOption func(*Queue)` breaks because the function type changes to the package's unexported config. Accepted at v0.x and recorded precisely in the upgrade note; it is not described as only a function-literal break. **YOUR CALL 2.** Naming stays three-way (memstore `Option`, pgx `Option`+alias, turso `QueueOption`) — folding a turso rename into this train would cost the turso retags the plan avoids; documented, not fixed. |

### Probes

- The three private `probeTable` copies (authentication, authorization,
  events) are replaced by `pgxdb.ProbeTable(ctx, db, s.table(name))`
  (v0.4.1, tagged; accepts bare or qualified — the datastore reviewer
  confirmed `to_regclass('"auth".users')` resolves and an absent schema
  yields NULL, i.e. `sdk.ErrNotFound`, never a `MapError`). Wrapping
  messages naming the migration source stay.
- **cms and jobs gain an additive `StatusCheck(ctx context.Context, db
  *pgxdb.DB, opts ...Option) error`** that `ProbeTable`s the package's
  tables under the configured schema — the `pgxdb.StatusCheck` /
  `Limiter.StatusCheck` idiom. Rationale: today those packages cannot be
  wired wrong; with this option a well-formed but mismatched schema
  (migrated into `authn`, constructed with `auth`, or migrated with a
  schema and constructed without) would silently read/write the host's own
  `public.entries`/`public.terms`/`public.assets`/`public.menus` — the most
  collision-prone names in the repo and the exact hazard #4 removes.
  `Repositories` itself stays probe-less and error-less (non-breaking); the
  READMEs and the #4 adoption snippet call `StatusCheck` at boot. **YOUR
  CALL 3:** additive `StatusCheck` (default) vs. probing inside
  `Repositories` and panicking on absence (rejected: infra failures would
  panic) vs. widening cms/jobs `Repositories` to return `error` (breaking).
- authentication `probeColumn` (`information_schema.columns`): with a schema
  set, add `AND table_schema = $3`; with none, the query is unchanged
  (byte-for-byte rule — today it is unfiltered by schema; tightening the
  default is a behaviour change outside this plan; README notes the
  looseness).
- The `_test.go` catalog reads (`pg_database` collation,
  `information_schema.columns` collation) gain the same `table_schema`
  filter when the test schema is set.
- Advisory locks (`pg_advisory_xact_lock` in authorization + jobs fenced)
  are database-scoped, not schema-scoped: two hosts sharing one database
  AND the same lock key text would contend across schemas. Not a
  regression (true under the `search_path` pin today); documented in both
  READMEs, no code change.

### pgxdb runner

```go
// MigrateOption configures RunMigrations.
type MigrateOption func(*migrateConfig)

// WithSchema applies the stream inside s: the migration transaction runs
// SET LOCAL search_path TO "<schema>" (transaction-scoped; the pool is never
// touched), creates the schema if absent, and keeps this stream's
// schema_migrations ledger there.
func WithSchema(s Schema) MigrateOption
```

- **Migration stream model (RULING REQUIRED — YOUR CALL 4).** The runner's
  godoc (`migrate.go:28-38`) ratified "one database, one stream: … calls
  RunMigrations once per database … splitting the stream into multiple
  calls would make applied migrations look new." `WithSchema` is per-call,
  so the #4 use case (feature tables in `auth`, host tables in `public`)
  is only reachable by **two calls over two directories** — and that is
  what this plan ratifies: **one stream per schema, each with its own
  `schema_migrations` in its own schema.** The ledgers are disjoint, so
  filename uniqueness becomes per-(schema, source) and cross-schema
  ordering is unexpressed. Host tree layout: one exported
  migrations directory per schema (`migrations/auth/`, `migrations/`). S1
  amends the `RunMigrations` godoc and the pgxdb README to say exactly
  this. The alternative — `WithSchema` applies to the host's WHOLE stream,
  mixed use forbidden — does not serve gps-360-go and is rejected.
- **Transaction boundary (part of YOUR CALL 4).** One call is still one
  transaction, but two schema streams are two transactions: if the first call
  commits and the second fails, the database is partially upgraded. This is
  stronger than the existing cross-source ordering warning; boot probes and
  `StatusCheck` detect an incomplete boot but cannot roll back the committed
  stream. The godoc/README therefore require a deterministic call order, state
  that cross-schema atomicity is not provided, and give the recovery rule
  (correct the failing stream and rerun; each committed stream is idempotent by
  its own ledger). Cross-schema FKs, views, functions, and other dependencies
  must be explicitly schema-qualified and applied after their dependencies;
  the per-schema ledgers do not encode that order. A future multi-stream
  transaction API may add atomic orchestration, but is outside this train.
- Inside the existing `db.InTx`, first probe the target namespace. If it exists,
  skip `CREATE SCHEMA` entirely; this makes DBA pre-creation a real workaround
  for a migrator without `CREATE ON DATABASE`. If absent, run `CREATE SCHEMA IF
  NOT EXISTS "<schema>"`
  (**YOUR CALL 5**: create-if-absent, default, vs. host pre-creates). Schema
  creation needs `CREATE ON DATABASE`, a different grant from the `CREATE ON
  SCHEMA` that table creation needs, and a least-privilege role sharing
  another app's database may lack it — so S1 maps `insufficient_privilege`
  to an explicit error ("schema %q does not exist and could not be created;
  grant CREATE ON DATABASE or pre-create it") and the README documents the
  grant. Before applying DDL, verify the current role has `USAGE` and `CREATE`
  on an existing schema and return named errors for each missing privilege.
  Then `SET LOCAL search_path TO "<schema>"` and assert `current_schema()` is
  the requested schema — strict, NO `public`
  fallback: with `public` on the path, an `ALTER TABLE users` whose
  `"auth".users` is missing would silently alter the host's `public.users`.
  `pg_catalog` is implicitly searched, so `gen_random_uuid()`,
  `hashtext`/`hashtextextended`, and `pg_advisory_xact_lock` (the only
  functions our SQL calls) resolve — verified live. Host migrations calling
  extension functions installed in `public` must qualify them (README).
- pgx uses the simple protocol for argument-less statements, so `SET LOCAL`
  and the multi-statement DDL are never prepared/cached; `InTx` has no
  retry loop. Safe.
- The four ledger statements (`to_regclass`, `CREATE TABLE`, `SELECT
  checksum`, `INSERT`) are qualified explicitly through `Schema.Table`, NOT
  left to `search_path`. This makes ledger routing explicit and deterministic
  on every connection and does not depend on prepared-statement reparse rules
  when `search_path` changes. PostgreSQL normally reparses a prepared statement
  when `search_path` changes, so this is an isolation guarantee, not a claim
  that a cached statement permanently binds to the first schema it saw. Ledger
  identity stays `(source="default", version=filename)`.
- **Existing-ledger relocation (gps-360-go's actual path).** Under today's
  DSN pin (`search_path=auth,public`) the ledger probe `to_regclass
  ('schema_migrations')` searches the whole path while the unqualified
  `CREATE TABLE` writes to the first entry — so a host's rows may live in
  `public.schema_migrations`. Adopting `WithSchema("auth")` then finds an
  empty `"auth".schema_migrations` and re-runs the stream. Most files are
  `IF NOT EXISTS`-safe, but authentication `0014_user_status.sql:37` is an
  `ALTER COLUMN … TYPE TEXT COLLATE "C"` (full table rewrite under an
  exclusive lock) and `0015_challenge_subject_keys.sql:22` re-runs an
  `UPDATE`. The pgxdb README, the store upgrade notes, and the #4 adoption
  comment carry an executable one-time preflight that runs BEFORE the first
  schema-scoped `RunMigrations` call:

  1. In one explicit transaction, verify the target schema and already-migrated
     feature tables exist.
  2. Create `"auth".schema_migrations` with the runner's exact canonical column
     and primary-key shape if absent. Do not invoke the runner merely to create
     it, because that invocation would immediately start applying files.
  3. Copy with an explicit column list from `public.schema_migrations`, filtered
     by `source = 'default'` and the exact feature-file manifest; use `ON
     CONFLICT (source, version) DO NOTHING` so the preflight is resumable.
  4. Before commit, assert that every manifest filename has exactly one target
     row and that `(source, version, checksum)` matches the public ledger. Abort
     on any missing row, duplicate, or checksum disagreement.
  5. Only after that transaction commits, call schema-scoped `RunMigrations`;
     it must report every copied file already applied.

  The canonical core of that transaction is pinned here (the adoption note
  expands `<feature files>` to the exact shipped manifest):

  ```sql
  BEGIN;
  CREATE TABLE IF NOT EXISTS "auth".schema_migrations (
      source TEXT NOT NULL,
      version TEXT NOT NULL,
      checksum TEXT NOT NULL,
      raw_sql TEXT,
      applied_at TIMESTAMPTZ NOT NULL,
      PRIMARY KEY (source, version)
  );
  INSERT INTO "auth".schema_migrations
      (source, version, checksum, raw_sql, applied_at)
  SELECT source, version, checksum, raw_sql, applied_at
  FROM public.schema_migrations
  WHERE source = 'default' AND version IN (<feature files>)
  ON CONFLICT (source, version) DO NOTHING;
  -- The published snippet follows with an EXCEPT/count assertion over the
  -- same manifest and raises before COMMIT on a missing/checksum-mismatched row.
  COMMIT;
  ```

  The docs include copy/paste SQL with explicit
  `(source, version, checksum, raw_sql, applied_at)` columns rather than
  `SELECT *`. If the copy is deliberately skipped, the current five feature
  streams are safe to re-run but authentication 0014 may take an exclusive
  lock/full rewrite and 0015 repeats its backfill; that is an explicit
  safe-but-potentially-expensive fallback, not the recommended adoption path.
  Live tests pin both the preflight/skip path and the full re-run fallback.
- `ExportMigrations` unchanged. README contract for hosts with their own
  runner: apply the exported stream inside a transaction that has `SET
  LOCAL search_path TO "<schema>"`, or qualify the DDL yourself.
- `ratelimit_windows` (pgxdb's own host-schema table, v0.3.0): the limiter's
  SQL is unqualified and its DDL is README reference SQL, so a host that
  merges that DDL into a schema-scoped stream relocates the table away from
  the limiter (it fails loudly at `limiterProbeSQL`, not silently). README
  says explicitly: do NOT include the limiter DDL in a schema-scoped
  stream. A limiter schema option is a separate demand, not built here.
- README nits: quoting preserves case (`Auth` ≠ `auth`); pgx's default
  statement cache is 512 entries per connection, so a schema-per-tenant
  host running three-plus store sets on one pool should size
  `statement_cache_capacity` in the DSN or use one pool per schema.

### Verification

Hermetic (in `make check`):
- pgxdb: `TestSchema` (valid/invalid names → `sdk.ErrInvalidInput`, zero
  renders bare, qualified rendering, case preserved, 63-byte boundary,
  reserved/system namespaces rejected); `TestMigrateConfig_WithSchema` (valid
  schema carried into the config; zero schema preserves the default). Invalid
  names are tested only through `NewSchema`: external code cannot construct an
  invalid `Schema`, so the migration option has no second validation seam.
- Per store: `TestNoBareTableReferences` — parses the package's non-test
  `.go` files with `go/parser` and inspects ONLY `*ast.BasicLit` string
  values (comments and identifiers excluded — the doc comments and
  `probeTables` inventory would otherwise false-positive), failing on
  `(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+<table>\b` for each table name
  **derived from the package's embedded `MigrationsFS` `CREATE TABLE`
  statements** (so a future migration is covered without a hand list; the
  `producer_seam_test.go` "enumerate by directory, not by hand" precedent).
  This is a Go test beside the seam, not a Makefile guard — the repo's
  stated convention for a boundary a grep would over-match. It is described as
  a regression guard for the current statement forms, not proof that arbitrary
  SQL composition is qualified: inspecting individual literals cannot detect
  constructions such as `"FROM " + "users"`, comma joins, `TRUNCATE`, or a
  future SQL form outside the regex. The non-default-schema live conformance
  leg is the behavioral proof for executed paths. Residual gap, stated: five
  in-package tests, no repo-wide guard; `make guard`'s set is unchanged.
- jobs: `WithLease` non-positive-ignored rule after the move to `config`.
- The "default unchanged" promise is proven by the default conformance leg
  (every statement executes unqualified against the migrated schema), plus
  reviewer diff of the 215 edits; no golden-string test (it would be a copy
  of the literal).

Live (`POSTGRES_TEST_DSN`):
- pgxdb (`go test ./...` in the module with the DSN set — env-gated like
  its `live_test.go` siblings; S7 adds the module to `make test-stores`,
  which does not enter it today):
  `TestLive_RunMigrations_WithSchema` (tables + ledger in the schema,
  `public` untouched, second run idempotent, checksum guard fires);
  `TestLive_RunMigrations_SchemaThenDefault_SamePool` (schema call then
  default call on the same `*DB` — rows land in `x.schema_migrations` and
  `public.schema_migrations` respectively: explicit ledger isolation on a
  reused pooled connection); `TestLive_RunMigrations_PerSchemaTransactions`
  (schema A commits, schema B intentionally fails, A remains committed, and a
  corrected B rerun succeeds — pins the documented non-atomic boundary);
  `TestLive_RunMigrations_AdoptPublicLedger` (pre-seeded public ledger + tables
  → execute the documented target-ledger preflight → schema-scoped runner skips
  every copied file); `TestLive_RunMigrations_WithSchema_LedgerInPublic`
  (deliberately omit the copy → the full feature re-run succeeds).
- pgxdb live privilege matrix: absent schema + no `CREATE ON DATABASE` → named
  creation error; DBA-precreated schema + no database `CREATE` + schema
  `USAGE, CREATE` → succeeds and proves the creation statement was skipped;
  existing schema without `USAGE` → named usage error; existing schema without
  schema `CREATE` → named creation-in-schema error. Tests use disposable roles
  and schemas owned by the throwaway PG17 container.
- Every pgx conformance suite reads optional `POSTGRES_TEST_SCHEMA`; when
  set, setup does `DROP SCHEMA IF EXISTS … CASCADE`, runs migrations with
  `pgxdb.WithSchema`, constructs repos with `WithSchema`, and TRUNCATEs
  qualified names (cms: assert the `CASCADE` to `entry_terms` stays inside
  the schema). The destructive non-conformance fixtures honour it too:
  authentication `schema_probe_test.go:39`, authorization
  `collation_test.go:69` and `upgrade_runbook_test.go:159` currently
  DROP/migrate unqualified against `public` and would otherwise churn
  `public` mid-run. `make test-stores` runs each pgx leg twice: default,
  then `POSTGRES_TEST_SCHEMA=gopernicus_schema_test`. That is #4's
  "conformance run once with a non-default schema", as the full suite.
- Decoy tests (events, then authentication): the session first `SET
  search_path TO public` (a free-form `POSTGRES_TEST_DSN` may itself pin
  `search_path` — exactly gps-360-go's wrapper — and would false-green);
  create a bare `public.event_outbox`/`public.users` decoy; construct with
  `WithSchema(x)` before migrating into `x` → probe FAILS naming the
  qualified table; migrate into `x` → succeeds; a write lands in
  `x.event_outbox`, the `public` decoy stays empty; AND the negative
  direction — constructed with no option, the write lands in the `public`
  decoy. Assertions count rows per schema; none compares
  `to_regclass(...)::text` output (it renders unqualified when the relation
  is path-visible).
- authentication `probeColumn` with schema: decoy `public.users` WITH the
  0014 columns and `x.users` WITHOUT them → the probe must fail (the only
  direction that proves the `table_schema` filter).
- cms/jobs `StatusCheck`: well-formed-but-absent schema → `sdk.ErrNotFound`
  naming the qualified table.

### Release

Six modules, one train (order: pgxdb first, cold-resolve, then stores):
- `integrations/datastores/pgxdb/v0.5.0` — minor (`Schema`, `MigrateOption`
  / `WithSchema`, the stream-model godoc change).
- Stores, **minor** each: `features/authentication/stores/pgx v0.3.0 →
  v0.4.0`; `features/{authorization,cms,events}/stores/pgx v0.1.0 →
  v0.2.0`; `features/jobs/stores/pgx v0.2.0 → v0.3.0`.
- **Pin moves are two per module, not one:** every store pins `pgxdb
  v0.5.0` (authentication from v0.4.0; the other four from v0.1.0), and
  because pgxdb requires `sdk v0.4.0`, MVS drags `sdk` along —
  authorization/cms/events move from `sdk v0.1.0`, jobs from `v0.2.0`
  (`go mod tidy` rewrites the requires). Cold-verified compile-safe by the
  backend reviewer (`GOWORK=off` builds of the four feature cores against
  sdk v0.4.2). The four store upgrade notes must say: "adopting this tag
  raises your effective `sdk` floor to v0.4.x — read the sdk v0.3.0 note
  (global middleware genuinely global; `HandleRaw` no longer bypasses it)
  before adopting." **YOUR CALL 6:** all five on pgxdb v0.5.0 (default, one
  train) vs. the v0.4.1 minimum `ProbeTable` needs (`Schema` is v0.5.0
  anyway, so the minimum is moot unless YOUR CALL 1 flips to strings).
- Doc parity: `events/stores/pgx/postgres.go:4-7`,
  `jobs/stores/pgx/postgres.go:4-6`, `authorization/stores/pgx/postgres.go:4-7`,
  `cms/stores/pgx/postgres.go:3-5` claim "same exported surface" as the
  turso sibling; amend to "same surface plus the Postgres-only `WithSchema`
  option — SQLite has no schemas". authentication's README row likewise.
- No feature-core, sdk, or turso bump. RELEASING.md summary line + six
  upgrade notes (jobs names every custom `QueueOption` construction/invocation
  break, not only function literals); README
  surface rows in all six modules.

## Tasks

- **S1** — pgxdb: `Schema`/`NewSchema`/`Table` with the Postgres-specific
  reserved-name and 63-byte checks; `MigrateOption`/`WithSchema`; namespace
  existence probe, create-if-absent, and the database/schema privilege matrix
  with named errors; `SET LOCAL search_path` + `current_schema()` assertion;
  explicitly qualified ledger statements; the `RunMigrations` godoc rewrite
  (one stream per schema, deterministic ordering, separate transactions and
  recovery); README sections (stream model + host tree layout, host-runner
  contract, executable ledger-relocation transaction with manifest/checksum
  assertion, grants, limiter DDL exclusion, case + statement-cache nits);
  hermetic + live tests listed in Verification.
- **S2** — events (5 statements, 1 table): `Option`/`WithSchema`, `table`
  method, `New(db, opts...) (*Store, error)`, `ProbeTable` adoption,
  bare-reference test, conformance `POSTGRES_TEST_SCHEMA` leg, decoy test
  (both directions, search_path pinned), doc-parity amendment. Establishes
  the pattern the other four copy.
- **S3** — jobs (30 statements, 3 tables): `Option` + `QueueOption` alias,
  `WithLease` → `config` with its rule test, three constructors (+ ignored-
  lease godoc), `StatusCheck`, tests, conformance leg, doc-parity amendment.
- **S4** — cms (35 statements, 8 tables): `Option`, five constructors,
  `StatusCheck`, tests, conformance leg (cascade-stays-in-schema), doc-parity
  amendment.
- **S5** — authorization (41 statements, 4 tables): `config` gains `schema`,
  `RelationshipRepository` option, the two CTE consts → funcs, `ProbeTable`
  adoption, `_test.go` catalog filters, `collation_test.go:69` +
  `upgrade_runbook_test.go:159` fixtures honour the schema env, conformance
  + mutations live legs, doc-parity amendment.
- **S6** — authentication (104 statements, 13 tables, 18 constructors):
  `Option`, all constructors, `userSummarySelect` → func, `ProbeTable`
  adoption, `probeColumn` schema filter + its decoy test,
  `schema_probe_test.go:39` fixture honours the schema env, decoy test,
  conformance leg, README parity row.
- **S7** — `Makefile test-stores`: add the pgxdb module and double every pgx
  leg (default + `POSTGRES_TEST_SCHEMA`); both legs green against a
  throwaway `postgres:17` container (not coordination-hub's port); `make
  check` green.
- **S8** — RELEASING.md (summary + six notes incl. the sdk-floor line and
  the full custom-`QueueOption` break), six READMEs, pin moves + `go mod tidy` per module
  → owner tags → cold-resolution check → comment on #4 with the gps-360-go
  adoption snippet: delete the `search_path` wrapper; `s, _ :=
  pgxdb.NewSchema("auth")`; `RunMigrations(ctx, db, featureFS, dir,
  pgxdb.WithSchema(s))` as its own call beside the host's default-schema
  call; `WithSchema(s)` on each store; `StatusCheck` for cms/jobs at boot;
  the verified target-ledger preflight if its rows live in `public`; and the
  required order/retry rule for the two non-atomic schema streams.

S2–S6 are independent after S1 and run as parallel implementer legs (Opus
per the subagent policy), each verified by `go build/test/vet` in its own
module before the S7 live run.

## YOUR CALLs

1. **`WithSchema(pgxdb.Schema)` value type, validated at the host, no
   panics** (default) vs. `WithSchema(string)` panicking through
   `pgxdb.NewSchema` at wiring (#4's literal ergonomics).
2. **jobs `QueueOption` → alias of package `Option`** (default; documented
   compile break for callers that construct, convert, invoke, return, or wrap
   their own `QueueOption func(*Queue)`; `WithLease` callers are unchanged) vs. a
   separate `WithSchema` that is also a `QueueOption` and threads through
   `Queue` to the other two stores (no alias, uglier).
3. **cms/jobs boot gate = additive `StatusCheck(ctx, db, opts...)`**
   (default) vs. probe-and-panic inside `Repositories` vs. widening
   `Repositories` to return `error` (breaking).
4. **Migration stream model: one stream per schema, per-schema ledgers,
   one transaction per call, no cross-schema atomicity, `RunMigrations` godoc
   amended with order/recovery rules** (default; the only model that serves
   gps-360-go) vs. whole-stream-in-schema with mixed use forbidden.
5. **Runner creates the schema if absent** (default; skips creation when
   already DBA-created and names database `CREATE` / schema `USAGE, CREATE`
   failures) vs. requiring the host to pre-create it.
6. **All five stores pin `pgxdb v0.5.0` + the sdk drag to v0.4.x**
   (default; one train) — see Release for the adopter-facing consequence.
7. **Plan location:** root `plans/` (the tracked convention since
   2026-08-25; `.claude/` is gitignored here) — say if you want it under
   `.claude/plans/` instead.

## Open questions only the owner can answer

These do not block API/design ratification. The ledger-location answer does
block publishing and executing gps-360-go's final adoption runbook, because it
selects either the verified relocation preflight or the no-copy path.

- Does gps-360-go's database role have `CREATE ON DATABASE`, or is the
  schema DBA-created? (Decides whether YOUR CALL 5's default ever fires
  there.)
- Where do gps-360-go's ledger rows live today — `public.schema_migrations`
  or `auth.schema_migrations`? (Decides whether the verified relocation
  preflight is required on adoption.)
- Does the schema name come from an env var in gps-360-go? (If yes, YOUR
  CALL 1's default is the safer one.)

## Execution log — 2026-08-26

Ratified in-session ("let's get to work"; every YOUR CALL at its default).
S1 ran first as one implementer leg; S2–S6 ran as five parallel implementer
legs, each against its own throwaway database (`leg_<feature>`) and its own
test schema so the live runs could not collide; S7/S8 by the orchestrating
session. Throwaway container: `docker run --rm -d --name gopernicus-schema-pg
-p 55432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` (left running for
the owner's re-run; `docker stop gopernicus-schema-pg` removes it).

| task | result |
|---|---|
| S1 pgxdb | `schema.go` (`Schema`/`NewSchema`/`Table`/`IsZero`/`String`), `migrate.go` (`MigrateOption`/`WithSchema`, `prepareSchema`: namespace probe → create-if-absent → `has_schema_privilege` USAGE/CREATE → strict `SET LOCAL search_path` → `current_schema()` assertion; ledger explicitly qualified; grant failures wrap `sdk.ErrForbidden`), README. Hermetic 59 pass; live 83 pass incl. the five schema legs + 4-case privilege matrix. |
| S2 events | 5 statements; `New(db, opts…)`; `ProbeTable` adoption; decoy test both directions. 6/6 + 5/5 conformance in both legs. |
| S3 jobs | 29 statements (plan said 30 — counting convention, guard proves zero bare refs); `QueueOption = Option` alias; `WithLease` → config; `StatusCheck`. 68/68 in both legs. |
| S4 cms | 41 statements (plan said 35); `StatusCheck`; cascade-stays-in-schema test; extra `TestProbeTablesMatchMigrations`. 54+1skip / 55 in default / schema legs. |
| S5 authorization | 47 render sites incl. the two CTE consts → funcs; `RelationshipRepository(db, opts…)`; all five live fixtures schema-aware via `qualify`/`qualifySQL`. 17/17 both legs; schema leg re-run from an EMPTY `public`. |
| S6 authentication | 118 render sites across 16 store files via one embedded `qualified` helper; 18 constructors; `probeColumn` two-const split; both decoy tests. 239 pass / 1 skip (NonC DSN absent) in both legs; mutation checks on all three new tests. |
| S7 | `Makefile test-stores`: pgxdb added, every pgx leg doubled via the `pgx-leg` macro (16 `go test` lines). `make check`: all modules + 18 guards green. `make test-stores` against the container: every leg `ok`, exit 0 (turso legs skip loudly without creds, as before). |
| S8 | RELEASING.md summary line + one combined upgrade note (adoption snippet included). Six READMEs written by their legs. **Pin moves NOT done** — they require the pgxdb tag to exist first (see manifest). No commit, tag, or push made; no comment on #4 yet. |

Deviations from plan text, all recorded here rather than silently: the
`pg_database.datcollate` test read was NOT given a `table_schema` filter (it
is database-scoped; nothing to filter). authorization's schema leg drops the
schema once per process, not per subtest (per-subtest isolation still comes
from the qualified TRUNCATE). `user_admin.go` picked up a gofmt-only hunk
(it was not gofmt-clean at HEAD). The `_test.go` catalog filters and every
fixture behave exactly as before when `POSTGRES_TEST_SCHEMA` is unset.

## Tag manifest — owner cuts

Working tree is DIRTY and uncommitted: 57 modified + 12 untracked files
across `integrations/datastores/pgxdb`, the five `features/*/stores/pgx`,
`Makefile`, `RELEASING.md`, and this plan. Pins move **one dependency level
at a time**, each level tagged and pushed before the next level's `go mod
tidy`, so every tag's `go.sum` carries real proxy checksums (the 2026-08-16
precedent, `.claude/plans/coordination-hub-auth-upstream/tag-manifest.md` §8).

| level | module | current | tag | pins after tidy |
|---|---|---|---|---|
| 1 | `integrations/datastores/pgxdb` | v0.4.1 | **v0.5.0** | unchanged (`sdk v0.4.0`) |
| 2 | `features/authentication/stores/pgx` | v0.3.0 | **v0.4.0** | `pgxdb v0.4.0 → v0.5.0`; `sdk v0.4.0` stays |
| 2 | `features/authorization/stores/pgx` | v0.1.0 | **v0.2.0** | `pgxdb v0.1.0 → v0.5.0`; `sdk v0.1.0 → v0.4.x` (MVS) |
| 2 | `features/cms/stores/pgx` | v0.1.0 | **v0.2.0** | same shape as authorization |
| 2 | `features/events/stores/pgx` | v0.1.0 | **v0.2.0** | same shape |
| 2 | `features/jobs/stores/pgx` | v0.2.0 | **v0.3.0** | `pgxdb v0.1.0 → v0.5.0`; `sdk v0.2.0 → v0.4.x` |

Steps: (1) commit the batch; (2) tag + push `integrations/datastores/pgxdb/v0.5.0`;
(3) in each store module `go get github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.5.0 && go mod tidy`,
`GOWORK=off go build ./... && go vet ./...`, commit the five go.mod/go.sum;
(4) tag + push the five store tags; (5) cold resolution from a throwaway
module outside the workspace (`GOWORK=off`, no `replace`) importing all six —
`go list -m all` must show the six versions above and a `go run` must
construct `pgxdb.NewSchema("auth")` + each store's `WithSchema`; (6) `make
check` re-run after the pin edits; (7) comment on gopernicus #4 with the
RELEASING.md adoption snippet and the ledger-relocation preflight pointer,
then close it. Open owner facts still unanswered (do not block tagging):
gps-360-go's role privileges, where its ledger rows live today, and whether
its schema name comes from env.
