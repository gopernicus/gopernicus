# features/cms/stores/pgx

The CMS feature's **PostgreSQL** store adapter — the dialect sibling of
`features/cms/stores/turso`. Its own module so a host that brings a different
datastore never pulls `pgx` into its module graph. It owns the SQL and the
canonical migration files; the host owns its database lifecycle.

It ports the frozen EAV spine (`entries` + `entry_fields` + `entry_terms`) and
the four typed domains (terms, menus, media, inquiries) to postgres idiom —
`TIMESTAMPTZ`, `$n` placeholders, SQLSTATE-based error mapping — with **the same
structure** as the turso tree. Representation changes; structure does not
(no JSONB-ification of `entry_fields.value`, no typed value columns, no
reshaping of the spine).

## Surface

Mirrors the turso store's exported surface (a host switches dialect by one import
+ one `Open` call):

| member | shape |
|---|---|
| `Repositories(db *postgres.DB) cms.Repositories` | the five stores, no migration side effects |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Migrations

`migrations/*.sql` carry the **identical version (filename) set** as the turso
tree — `0009`–`0021` with `0011`/`0012` absent (gaps reproduced). Same filename
= same logical schema step; content is per-dialect. After export, the host owns
the final migration stream in `workshop/migrations/{db}`.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs, and the
live conformance suite (`storetest.Run`) **skips loudly** without a DSN
(`POSTGRES_TEST_DSN not set — postgres conformance NOT verified`). A silent
green that tested nothing is the false-green failure mode this gating exists
to prevent.

The live conformance run is this store's dialect-parity gate. Spin a local
database and run it:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test ./...
```

Each `newRepos` opens a connection, applies the migrations via the connector's
`RunMigrations`, and `TRUNCATE ... CASCADE`s the cms tables (up front and via
`t.Cleanup`) so every leaf subtest starts from a clean, isolated `Repositories`.

`make check` stays hermetic (the suite skips); `make test-stores` runs this
live path expecting `POSTGRES_TEST_DSN`. Milestone close records a dated
NOTES.md live-conformance artifact.
