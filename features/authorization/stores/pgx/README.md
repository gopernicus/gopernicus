# features/authorization/stores/pgx

The authorization feature's **PostgreSQL** store adapter — the dialect sibling of
`features/authorization/stores/turso`. Its own module so a host that brings a
different datastore never pulls `pgx` into its module graph. It owns the SQL and
the canonical migration files; the host owns its database lifecycle.

It fills **both kinds'** outbound ports over the `integrations/datastores/pgxdb`
connector:

- `relationship.Storer` over **`iam_relationships`** — the ReBAC tuple store
  (group expansion and descendant lookup as recursive CTEs; land in task-2).
- `role.Storer` over **`iam_roles`** — the roles kind's plain assignment lookups.

Timestamps are `TIMESTAMPTZ` (postgres orders them natively; no lexicographic-`TEXT`
convention needed). `iam_relationships` rows are **immutable** — no `updated_at`;
a relationship is deleted and recreated. Representation changes vs turso; structure
and port semantics do not.

## ⚠️ Prerequisite: apply the `authorization` migration source before wiring

Both tables belong to migration source **`authorization`**, distinct from
`cms`/`auth`/`jobs`/`events`. The shared `(source, version)` migration ledger
expresses **no ordering between sources**, so a host that scaffolds another
feature's migrations but not this store's would fail at *runtime*, not boot.

**`Repositories(db)` guards against exactly that:** it probes for **both** the
`iam_relationships` and `iam_roles` tables at construction
(`SELECT to_regclass($1)`) and returns `errs.ErrNotFound` — naming the specific
missing table — if the `authorization` source has not been applied. The failure
surfaces at wiring time, before the host serves traffic. Scaffold this store's
migrations with `ExportMigrations` and apply them with your host's runner pre-boot,
alongside every other feature source you wire.

**Hosts never renumber** the scaffolded files: the filenames are the shared
`(source, version)` ledger keys and the turso sibling carries the byte-identical
set (same filename == same logical schema step; content is per-dialect).

## Kinds are port-optional; the schema is wholesale

The two kinds (relationships, roles) are independently wireable at the
**port/behavior level** — but the **schema is NOT per-kind**. Both `iam_*` tables
scaffold into every adopting host regardless of which kinds it wires (the §2.1
bounding rule applied intra-feature). A **roles-only** adopter still applies the
FULL `authorization` source, `iam_relationships` included, and both boot probes
expect both tables.

**Kind selection is the host's wiring choice.** `Repositories(db)` always returns
BOTH kinds wired; a host wanting a single kind zeroes the other
`authorization.Repositories` field after construction, or wires its own
single-kind `Repositories`. A nil kind field turns that kind OFF structurally
(deny-by-absence) at `authorization.NewService`.

## Surface

| member | shape |
|---|---|
| `Repositories(db *pgxdb.DB) (authorization.Repositories, error)` | both kinds wired; errors if `iam_relationships` or `iam_roles` is missing (boot-time probe, names the missing table) |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Migrations

`migrations/0001_iam_relationships.sql` + `migrations/0002_iam_roles.sql` (source
`authorization`) carry the **byte-identical filename set** as the turso sibling.
`iam_relationships.relationship_id` carries an inline
`DEFAULT gen_random_uuid()::text` so a `cryptids.Database`-wired host lets the DB
mint the key (the store drops the id column from the UNNEST insert for the whole
batch). The scope columns on `iam_roles` are `NOT NULL DEFAULT ''` so a global
grant (empty resource pair) participates in the unique index. After export, the
host owns the final migration stream in its own dir.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs, and the live
conformance suite **skips loudly** without a DSN (`POSTGRES_TEST_DSN not set —
postgres conformance NOT verified`). Unlike the turso sibling (which is
`-tags=integration`), this store follows the pgx convention of plain env-gating —
no build tag. The live run — the dialect-parity gate covering the named
adversarial sub-runners and the `Roles/*` family — runs against a dockered
postgres:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test -count=1 ./...
```

`make check` stays hermetic (the suite skips); `make test-stores` runs this live
path expecting `POSTGRES_TEST_DSN`.
