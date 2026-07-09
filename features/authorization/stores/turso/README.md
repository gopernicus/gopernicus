# features/authorization/stores/turso

The authorization feature's **Turso / libSQL** store adapter — its own module so a
host that brings a different datastore never pulls `libsql` into its module graph.
It owns the SQL and the canonical migration files; the host owns its database
lifecycle.

It fills **both kinds'** outbound ports over the `integrations/datastores/turso`
connector:

- `relationship.Storer` over **`iam_relationships`** — the ReBAC tuple store
  (group expansion and descendant lookup as recursive CTEs; land in task-2).
- `role.Storer` over **`iam_roles`** — the roles kind's plain assignment lookups.

Timestamps are fixed-width ISO-8601 `TEXT` (lexicographic == chronological, which
the keyset listings' `created_at` order relies on). `iam_relationships` rows are
**immutable** — no `updated_at`; a relationship is deleted and recreated.

## ⚠️ Prerequisite: apply the `authorization` migration source before wiring

Both tables belong to migration source **`authorization`**, distinct from
`cms`/`auth`/`jobs`/`events`. The shared `(source, version)` migration ledger
expresses **no ordering between sources**, so a host that scaffolds another
feature's migrations but not this store's would fail at *runtime*, not boot.

**`Repositories(db)` guards against exactly that:** it probes for **both** the
`iam_relationships` and `iam_roles` tables at construction and returns
`errs.ErrNotFound` — naming the specific missing table — if the `authorization`
source has not been applied. The failure surfaces at wiring time, before the host
serves traffic. Scaffold this store's migrations with `ExportMigrations` and apply
them with your host's runner pre-boot, alongside every other feature source you
wire.

**Hosts never renumber** the scaffolded files: the filenames are the shared
`(source, version)` ledger keys and the pgx sibling carries the identical set.

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
| `Repositories(db *turso.DB) (authorization.Repositories, error)` | both kinds wired; errors if `iam_relationships` or `iam_roles` is missing (boot-time probe, names the missing table) |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Migrations

`migrations/0001_iam_relationships.sql` + `migrations/0002_iam_roles.sql` (source
`authorization`) are the canonical schema — the version filename set authored here
and mirrored exactly by the pgx sibling (same filename == same logical schema
step; content is per-dialect). `iam_relationships.relationship_id` carries an
inline `DEFAULT (lower(hex(randomblob(16))))` so a `cryptids.Database`-wired host
lets the DB mint the key (the store omits the id column for the whole batch). The
scope columns on `iam_roles` are `NOT NULL DEFAULT ''` so a global grant (empty
resource pair) participates in the unique index. After export, the host owns the
final migration stream in its own dir.

## Testing

`go test ./...` is hermetic: the live conformance suite is behind
`-tags=integration` and skips loudly without the env
(`TURSO_DATABASE_URL/TURSO_AUTH_TOKEN`). The live run — the dialect-parity gate
covering the named adversarial sub-runners and the `Roles/*` family — runs against
the authorized playground database:

```sh
TURSO_DATABASE_URL='libsql://…' TURSO_AUTH_TOKEN='…' \
  go test -tags=integration -count=1 ./...
```

`make check` stays hermetic (the live suite is tag-gated); `make test-stores`
runs this live path expecting `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`.
