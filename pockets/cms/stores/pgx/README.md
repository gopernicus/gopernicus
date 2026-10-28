# pockets/cms/stores/pgx

The CMS pocket's **PostgreSQL** store adapter — the dialect sibling of
`pockets/cms/stores/turso`. Its own module so a host that brings a different
datastore never pulls `pgx` into its module graph. It owns the SQL and the
canonical migration files; the host owns its database lifecycle.

It ports the frozen EAV spine (`entries` + `entry_fields` + `entry_terms`) and
the four typed domains (terms, menus, media, inquiries) to postgres idiom —
`TIMESTAMPTZ`, `$n` placeholders, SQLSTATE-based error mapping — with **the same
structure** as the turso tree. Representation changes; structure does not
(no JSONB-ification of `entry_fields.value`, no typed value columns, no
reshaping of the spine).

## Surface

Mirrors the turso store's exported surface, plus the Postgres-only `WithSchema`
option — SQLite has no schemas (a host switches dialect by one import + one
`Open` call):

| member | shape |
|---|---|
| `Repositories(db *pgxdb.DB, opts ...Option) cms.Repositories` | the five stores plus `InquiryMaintenance` (the same `InquiryStore`), no migration side effects, no probe |
| `NewEntryStore` / `NewTermStore` / `NewMenuStore` / `NewAssetStore` / `NewInquiryStore` `(db *pgxdb.DB, opts ...Option)` | one store at a time, same options |
| `Option` | construction option, applied by `Repositories` and every constructor |
| `WithSchema(s pgxdb.Schema) Option` | places every table this store touches in `s`; never panics |
| `StatusCheck(ctx, db *pgxdb.DB, opts ...Option) error` | boot gate: every cms table exists under the configured schema; WARNs (never fails) when 0022's retention index is missing |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Schema

Without `WithSchema` nothing changes: every statement renders bare table names,
byte-for-byte what this store has always emitted.

With it, build the schema at the host — `pgxdb.NewSchema` validates the name
there, so `WithSchema` itself never panics — apply the migrations into the same
schema, and construct the stores with the same value:

```go
s, err := pgxdb.NewSchema(os.Getenv("CMS_SCHEMA")) // "cms"
if err != nil { return err }
if err := pgxdb.RunMigrations(ctx, db, cmspgx.MigrationsFS, cmspgx.MigrationsDir,
    pgxdb.WithSchema(s)); err != nil { return err }
if err := cmspgx.StatusCheck(ctx, db, cmspgx.WithSchema(s)); err != nil { return err }
repos := cmspgx.Repositories(db, cmspgx.WithSchema(s))
```

**Call `StatusCheck` at boot whenever a schema is configured.** `Repositories`
runs no probe and returns no error, and this store's table names — `entries`,
`terms`, `assets`, `menus` — are the ones a host is most likely to own itself.
A store constructed for a schema its migrations never reached (migrated into one
schema and constructed with another, or migrated with a schema and constructed
without) would otherwise read and write the host's own tables silently.
`StatusCheck` probes all eight tables under the configured schema and fails
naming the qualified one that is missing.

Quoting preserves case: `"CMS"` and `"cms"` are different schemas. Per-repository
DIFFERENT schemas within one pocket are out of scope — the constructors
mechanically allow it, but one migration stream per schema gives it no story.

## Migrations

`migrations/*.sql` carry the **identical version (filename) set** as the turso
tree — `0009`–`0022` with `0011`/`0012` absent (gaps reproduced). Same filename
= same logical schema step; content is per-dialect. After export, the host owns
the final migration stream in `workshop/migrations/{db}`.

**Generated ids and their backstops.** The pocket mints every id in Go (UUIDv7
by default — see the pocket's `Config.IDs`); the column defaults are only the
backstop for a raw SQL insert or a host wired with `sdk.DatabaseID`. Every
generated `id` column (`terms`, `menus`, `menu_items`, `assets`, `inquiries`,
`entries`) is `TEXT COLLATE "C" … DEFAULT uuidv7()::text`, so the canonical set
needs PostgreSQL 18. The columns that hold one of those ids
(`terms.parent_id`, `menu_items.{menu_id,parent_id}`, `entries.parent_id`,
`entry_fields.entry_id`, `entry_terms.{entry_id,term_id}`) carry `COLLATE "C"`
too: an index serves a join only under the join key's collation, and the
`created_at DESC, id DESC` tiebreak and `ORDER BY term_id` / `parent_id` then
sort in byte order on any cluster locale, matching the turso sibling. No store
query calls `uuidv7()` at runtime. A host that already applied the previous
files re-exports them against a fresh database: the edited files' checksums
changed.

## Retention and erasure

Inquiries hold a third party's name, email and message, and nothing in the pocket
ever deletes them. Both bundled stores implement the optional
`messaging.InquiryEraser` and `Repositories` fills `cms.Repositories.InquiryMaintenance`
with it; `cms.Register` logs `inquiry_maintenance=wired|absent` and, in production,
WARNs when a host store leaves the slot nil. The pocket mounts no route for it — the
host calls the eraser it wired:

| method | semantics |
|---|---|
| `PurgeBefore(ctx, before, limit)` | removes at most `limit` inquiries with `created_at <= before` (a row exactly at `before` goes), oldest first; `limit <= 0` is `sdk.ErrInvalidInput`. The host's retention schedule calls it; a backlog drains over successive calls |
| `DeleteByEmail(ctx, email)` | removes every inquiry whose email matches under `lower()` on both sides — the port promises the ASCII case fold; PostgreSQL also folds non-ASCII letters per the database's ctype, SQLite does not. The "erase a person" step |
| `Delete(ctx, id)` | removes one inquiry; an absent id is `sdk.ErrNotFound` |

No evidence row records an inquiry deletion; an `inquiry.deleted` event and an admin
delete surface are deferred to a follow-up cms plan.

`0022_inquiries_created_at.sql` adds `idx_inquiries_created_at` on `(created_at)`, the
purge's access path. It is **admitted, not required**: without it the store still
works, every batch sorts the whole table, and `StatusCheck` logs
`index_missing=idx_inquiries_created_at file=0022_inquiries_created_at.sql`. The file
sets `lock_timeout = '5s'` and uses `IF NOT EXISTS`, so on a large table pre-build the
index with `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_inquiries_created_at ON
inquiries (created_at)` (check `pg_index.indisvalid`; drop an invalid one first) and the
in-stream statement becomes a no-op.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test, the bare-table
reference guard (`TestNoBareTableReferences`), and the rendering test
(`TestWithSchema`) run, and the live conformance suite (`storetest.Run`)
**skips loudly** without a DSN
(`POSTGRES_TEST_DSN not set — postgres conformance NOT verified`). A silent
green that tested nothing is the false-green failure mode this gating exists
to prevent.

The live conformance run is this store's dialect-parity gate. Spin a local
database and run it:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:18
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test ./...
```

Each `newRepos` opens a connection, applies the migrations via the connector's
`RunMigrations`, and `TRUNCATE ... CASCADE`s the cms tables (up front and via
`t.Cleanup`) so every leaf subtest starts from a clean, isolated `Repositories`.

Setting `POSTGRES_TEST_SCHEMA` runs the same suite inside that schema — dropped
first, migrated with `pgxdb.WithSchema`, constructed with `WithSchema`, and
truncated by qualified name — which is the behavioural proof that the option
reaches every executed path. That leg also asserts the `TRUNCATE ... CASCADE`
stays inside the schema: a decoy row in a bare `public.entries` survives it.

The id inventories in `collation_test.go` are checked three ways: the canonical
DDL (hermetic), the applied catalog (`POSTGRES_TEST_DSN`), and — with
`POSTGRES_NON_C_TEST_DSN` pointing at a non-C database such as the
`postgres:18` image's default `en_US.utf8` — the entry tiebreak and term-id
order through the store. `id_default_live_test.go` proves each `uuidv7()`
backstop with a raw insert that omits the id.

```sh
POSTGRES_TEST_SCHEMA=gopernicus_schema_test \
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test ./...
```

`make check` stays hermetic (the suite skips); `make test-stores` runs this
live path expecting `POSTGRES_TEST_DSN`. Milestone close records a dated
NOTES.md live-conformance artifact.
