# pockets/authentication/stores/pgx

The auth pocket's **PostgreSQL** store adapter — the dialect sibling of
`pockets/authentication/stores/turso`. Its own module so a host that brings a different
datastore never pulls `pgx` into its module graph. It owns the SQL and the
canonical migration files; the host owns its database lifecycle.

It ports the v3 repository bundle (15 ports over 13 canonical tables — users,
passwords, sessions, oauth accounts/states, service accounts, api keys, security
events, invitations, user identifiers, challenges, contact changes, and
authentication grants; `PasswordResets`/`CredentialMutations` reuse existing
tables) to postgres idiom — `TIMESTAMPTZ`, native `BOOLEAN`, `$n` placeholders,
SQLSTATE-based error mapping — with **the same structure** as the turso tree.
Representation changes; structure does not. Secrets are stored as **digests, never
plaintext**: session refresh tokens (`refresh_token_hash`), challenge OTP codes /
magic-link tokens (`secret_digest`), API keys, and invitation tokens are hashed at
rest and looked up by digest. Child tables carry **no enforced FKs** to `users`
(the conformance suite exercises child ports without a users row; credential and
identifier atomicity lives in the service transactions, not cascades).

## Surface

Mirrors the turso store's exported surface **plus the Postgres-only `WithSchema`
option — SQLite has no schemas** (a host switches dialect by one import + one
`Open` call):

| member | shape |
|---|---|
| `Repositories(db *pgxdb.DB, opts ...Option) (auth.Repositories, error)` | the 17-port bundle, no migration side effects — **probes all 13 canonical tables first**, then the ALTER-added columns (`users.status`, `users.status_changed_at`, `challenges.subject_key`), and errors (`sdk.ErrNotFound`, naming the table or column + the `authentication` migration source) when one is missing; an infrastructure/query failure is never misreported as a missing table |
| `Option` | construction option, accepted by `Repositories` and by every one of the 18 `NewXStore(db, opts ...Option)` constructors, so a host composing its own bundle gets the same seam |
| `WithSchema(s pgxdb.Schema) Option` | places every table these stores touch in `s`; the zero `Schema` is the default and renders today's unqualified SQL byte-for-byte. It never panics — validation happened in `pgxdb.NewSchema` at the host |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

## Schema

By default every statement names its table unqualified, exactly as it always
has. A host that wants the auth tables in their own Postgres schema builds the
name once and passes it to BOTH the runner and the stores:

```go
s, err := pgxdb.NewSchema(os.Getenv("AUTH_SCHEMA")) // validated here, once
if err != nil { return err }

// one stream per schema, applied pre-boot, with its own ledger inside s
if err := pgxdb.RunMigrations(ctx, db, pgxfs, dir, pgxdb.WithSchema(s)); err != nil { return err }

repos, err := pgx.Repositories(db, pgx.WithSchema(s))
```

Every table reference renders `"<schema>".<table>` through one chokepoint, never
through a pool-wide `search_path` pin: the host's own unqualified statements are
untouched. Quoting preserves case (`Auth` ≠ `auth`). A store constructed for a
schema its migrations never reached fails the boot probe, naming the qualified
table.

Per-repository DIFFERENT schemas within one bundle are out of scope: the
constructors mechanically allow it, but the one-stream-per-schema migration model
gives it no story.

**Known looseness:** without `WithSchema` the ALTER-added column probe
(`information_schema.columns`) is NOT filtered by `table_schema` — that is the
query this store has always run, and tightening the default would be a behaviour
change outside this option. With a schema set the probe IS scoped to it.

## Migrations

`migrations/*.sql` carry the **identical version (filename) set** as the turso
tree — `0001`–`0015` (thirteen tables; auth owns no delivery table). Same filename
= same logical schema step; content is per-dialect. After export, the host owns
the final migration stream and applies it pre-boot through its own ledger.

**`0014_user_status.sql` and `0015_challenge_subject_keys.sql` are APPEND-ONLY and
add no table.** `0015` adds `challenges.subject_key`, backfills it from `user_id`,
and moves the single-active claim from `(user_id, purpose)` to
`(subject_key, purpose)` — every pre-existing purpose is unchanged, while an email
magic link gains a key that exists without a user (CHAU-6.1).

**`0014_user_status.sql` is APPEND-ONLY and adds no table.** `0001_users.sql` is
tagged and immutable, so the account-lifecycle work arrives as ALTERs on `users`:
the closed-CHECK `status` column (default and backfill `active`), the nullable
`status_changed_at`, the `COLLATE "C"` pin described below, and the directory's
`idx_users_created_at_id`.

**Upgrading an existing host.** Re-export (or copy) `0014_user_status.sql` into
your migration directory and apply it **before** deploying a binary built against
this tag:

```sh
# from your host, after go get-ing the new store tag
go run ./cmd/exportmigrations   # or however your host calls pgx.ExportMigrations
# then your normal migrate step
```

`Repositories()` probes `users.status`, `users.status_changed_at`, and
`challenges.subject_key` by name and refuses to construct without them, so a skipped migration fails loudly at wiring
time. **`ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"` rewrites the
primary-key index under an ACCESS EXCLUSIVE lock** — on a large users table,
schedule it in a maintenance window. Skipping only that statement does not
corrupt anything; it means the operator directory's `id` tiebreak follows the
cluster default collation, which can disagree with the libSQL sibling's byte
order for ids that mix cases.

**Contractual collation (pgx only).** The opaque `id` columns that serve as the
`created_at DESC, id DESC` keyset tiebreak carry per-column `COLLATE "C"` —
`service_accounts`, `api_keys`, `security_events`, `invitations`, and — since the
operator directory made it a keyset tiebreak — `users` (pinned by the `0014`
ALTER rather than a CREATE TABLE clause) — so
byte-order pagination parity with the SQLite/libSQL `BINARY` tiebreak holds on any
database's default collation. A `C`-locale database stays a supported
belt-and-suspenders posture, not a requirement; human display/content columns are
deliberately left on the database default. **Greenfield caveat:** `CREATE ... IF
NOT EXISTS` no-ops on a pre-existing table, so a host upgrading from a pre-v3
schema does NOT retroactively gain the per-column collation — per the
greenfield-migrations rule the canonical set ships the final schema only and hosts
own their own schema evolution.

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
`RunMigrations`, and `TRUNCATE ... CASCADE`s the auth tables (up front and via
`t.Cleanup`) so every leaf subtest starts from a clean, isolated `Repositories`.

The optional `POSTGRES_TEST_SCHEMA` runs the whole live suite — conformance,
probe, and collation fixtures — inside a named schema, with migrations applied
through `pgxdb.WithSchema` and every store constructed `WithSchema`. That is the
behavioral proof for the non-default-schema paths; unset, every fixture behaves
exactly as it did before the option existed.

```sh
POSTGRES_TEST_DSN='...' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test ./...
```

`make check` stays hermetic (the suite skips); `make test-stores` runs this
live path expecting `POSTGRES_TEST_DSN`.
