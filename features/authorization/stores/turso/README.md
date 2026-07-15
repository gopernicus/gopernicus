# features/authorization/stores/turso

The authorization feature's **Turso / libSQL** store adapter — its own module so a
host that brings a different datastore never pulls `libsql` into its module graph.
It owns the SQL and the canonical migration files; the host owns its database
lifecycle.

It fills **all three** outbound ports over the `integrations/datastores/turso`
connector:

- `relationship.Storer` over **`iam_relationships`** — the ReBAC tuple store
  (group expansion and descendant lookup as recursive CTEs; land in task-2).
- `role.Storer` over **`iam_roles`** — the roles kind's plain assignment lookups.
- `mutation.MutationRepository` — the atomic v3 write path over the shared `iam_*`
  tables plus the **`iam_scopes`** revision anchors and **`iam_mutations`**
  receipts. libSQL/SQLite has no `FOR UPDATE`, so one `Apply`/`ApplyGuarded` is a
  single `BEGIN IMMEDIATE` write-serializing transaction (the auth v3 precedent):
  it takes the write intent up front so `sqld` serializes contending writers, then
  re-reads the mutation scope plus every guard-observed dependency anchor in
  canonical order, re-validates the observed revisions, de-duplicates by receipt,
  evaluates the guardian invariant, applies all rows or none, bumps the scope
  revision exactly once, and mints the receipt. Full write serialization is
  strictly stronger than the pgx sibling's per-anchor `FOR UPDATE` — it precludes a
  mid-transaction dependency change by construction, so the canonical-order
  dependency re-validation is a defense-in-depth mirror of the pgx contract, not
  the primary mechanism (two last-owner revokes cannot both commit; a replay storm
  has exactly one first application). A residual `SQLITE_BUSY` (serialization that
  timed out under load rather than blocking) is absorbed by a bounded busy-retry of
  the idempotent transaction — never surfaced where the contract promises an
  application outcome.

Timestamps are fixed-width ISO-8601 `TEXT` (lexicographic == chronological, which
the keyset listings' `created_at` order relies on). `iam_relationships` rows are
**immutable** — no `updated_at`; a relationship is deleted and recreated.

## ⚠️ Prerequisite: apply the `authorization` migration source before wiring

Both tables belong to migration source **`authorization`**, distinct from
`cms`/`auth`/`jobs`/`events`. The shared `(source, version)` migration ledger
expresses **no ordering between sources**, so a host that scaffolds another
feature's migrations but not this store's would fail at *runtime*, not boot.

**`Repositories(db)` guards against exactly that:** it probes for **all four**
`iam_relationships`, `iam_roles`, `iam_scopes`, and `iam_mutations` tables at
construction and returns `errs.ErrNotFound` — naming the specific missing table —
if the `authorization` source has not been applied. The failure surfaces at wiring
time, before the host serves traffic. Scaffold this store's migrations with
`ExportMigrations` and apply them with your host's runner pre-boot, alongside every
other feature source you wire.

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
both kinds AND the atomic mutation repository wired; a host wanting a single kind
zeroes the other `authorization.Repositories` field after construction, or wires
its own single-kind `Repositories`. A nil kind field turns that kind OFF
structurally (deny-by-absence) at `authorization.NewService`.

## Surface

| member | shape |
|---|---|
| `Repositories(db *turso.DB, opts ...Option) (authorization.Repositories, error)` | all three ports wired (relationships, roles, atomic mutations); errors if any `iam_*` table is missing (boot-time probe, names the missing table) |
| `WithGuardianPolicy(p mutation.GuardianPolicy) Option` | overrides the mutation repository's guardian invariant (default: owner protected on every type, min one direct anchor) — mirrors the memstore and pgx options |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Migrations

The canonical schema (source `authorization`) is the version filename set authored
here and mirrored exactly by the pgx sibling (same filename == same logical schema
step; content is per-dialect):

- `migrations/0001_iam_relationships.sql` — the ReBAC tuple store.
  `relationship_id` carries an inline `DEFAULT (lower(hex(randomblob(16))))` so a
  `cryptids.Database`-wired host lets the DB mint the key. The
  `idx_iam_relationships_unique_subject` index (WITHOUT `relation`) enforces the
  ratified one-relation-per-exact-`SubjectRef` rule.
- `migrations/0002_iam_roles.sql` — the roles assignment store. Scope columns are
  `NOT NULL DEFAULT ''` so a global grant (empty resource pair) participates in
  the unique index; the `ck_iam_roles_scope_pair` constraint keeps that pair
  consistent (both empty or both non-empty).
- `migrations/0003_iam_scopes.sql` — the scope **revision anchors** (v3 write
  path). One row per resource/subject scope; `revision` is the monotonic anchor
  the atomic mutation repositories bump and validate under lock.
- `migrations/0004_iam_mutations.sql` — the mutation **receipts** (idempotency
  ledger, keyed by MutationID). Stores the payload digest, resulting revision,
  domain outcome, and governing schema digest — never the payload itself.
  `expires_at` is nullable; **permanent retention is the default posture**.

After export, the host owns the final migration stream in its own dir.

**Upgrading an existing v1 database?** See
[`../CONVERSION.md`](../CONVERSION.md) for the v1 → v3 detection-and-repair draft
(invalid/missing userset relations, silent-conflict awareness, scope-revision
seeding — and the standing rule that an ambiguous userset relation is an operator
decision, never an automatic `member` grant). The full host upgrade runbook —
backup, window, binary stop, audit/repair, apply/seed, boot, rollback boundary,
and the gain/lose/retain access assessment — is [`../UPGRADE.md`](../UPGRADE.md).

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
