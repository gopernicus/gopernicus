# pockets/authentication/stores/pgx

The auth pocket's **PostgreSQL** store adapter — the dialect sibling of
`pockets/authentication/stores/turso`. Its own module so a host that brings a different
datastore never pulls `pgx` into its module graph. It owns the SQL and the
canonical migration files; the host owns its database lifecycle.

**`v0.6.1` credential ownership patch:** use authentication core `v0.11.1` or
later. Credential retirement and identifier-use changes validate owned, active
targets under the owning user's transaction lock. A nominated replacement must
be a distinct active identifier of the same user and kind, replacing a primary.
Invalid input rolls back without credential, revision or revocation changes.
This patch adds no migration. Stop old vulnerable writers during deployment.

It ports the authentication repository bundle to PostgreSQL. The schema covers users,
passwords, sessions, oauth accounts/states, service accounts, api keys, security
events, invitations, user identifiers, challenges, contact changes, and
authentication grants, OAuth client metadata, authorization codes, and spent
refresh hashes. `PasswordResets`/`CredentialMutations` reuse existing tables.
It uses PostgreSQL idiom — `TIMESTAMPTZ`, native `BOOLEAN`, `$n` placeholders,
SQLSTATE-based error mapping — with **the same structure** as the turso tree.
Representation changes; structure does not. Secrets are stored as **digests, never
plaintext**: session refresh tokens (`refresh_token_hash`), challenge OTP codes /
magic-link tokens (`secret_digest`), API keys, and invitation tokens are hashed at
rest and looked up by digest. Child tables carry **no enforced FKs** to `users`
(the conformance suite exercises child ports without a users row; credential and
identifier atomicity lives in focused repository transactions, not cascades).

## Transaction scope

Atomic repository methods own their transaction: provisioning, credential writes,
session admission, grants, invitation claims and challenge replacement each enforce
their documented invariant. Authentication stores do not generally join a host's
ambient connector transaction. Wrapping several service calls in host `Transact`
does not make them one atomic unit. External invitation grants must use the stable
operation ID for idempotence. See [AUDIT-022](../../../../AUDIT.md#audit-022-authentication-proof-lifecycle-and-host-api).

## Surface

Mirrors the turso store's exported surface **plus the Postgres-only `WithSchema`
option — SQLite has no schemas** (a host switches dialect by one import + one
`Open` call):

| member | shape |
|---|---|
| `Repositories(ctx context.Context, db *pgxdb.DB, opts ...Option) (auth.Repositories, error)` | the full bundle including `OAuth2` and `SessionManagement`, no migration side effects — probes canonical tables and ALTER-added columns in migration order, and errors (`sdk.ErrNotFound`, naming the table or column + the `authentication` migration source) when one is missing; an infrastructure/query failure is never misreported as a missing table. Last it runs `db.ProbeAppendOnly(ctx, AuditTables(opts...)...)` under the `RuntimeMode` `db` was opened with (see [Audit rail](#audit-rail-0021-and-0022)) |
| `AuditTables(opts ...Option) []pgxdb.AppendOnlyTable` | the append-only tables this store owns — `security_events`, schema-qualified under `WithSchema`, verified against the trigger function `0022` ships. A migrator preflights the runtime role with `pgxdb.InspectTablePrivileges(ctx, q, role, pgx.AuditTables(opts...)...)`, deciding exactly as the boot probe will |
| `Option` | construction option, accepted by `Repositories` and by every `NewXStore(db, opts ...Option)` constructor, so a host composing its own bundle gets the same seam |
| `WithSchema(s pgxdb.Schema) Option` | places every table these stores touch in `s`; the zero `Schema` is the default and renders today's unqualified SQL byte-for-byte. It never panics — validation happened in `pgxdb.NewSchema` at the host |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

The individual `NewXStore` constructors wrap the supplied database without
probing or taking ownership. Passing a nil database is a programming error and
panics immediately. The probing `Repositories` constructor returns
`sdk.ErrInvalidInput` for a nil database.

## Schema

By default every statement names its table unqualified, exactly as it always
has. A host that wants the auth tables in their own Postgres schema builds the
name once and passes it to BOTH the runner and the stores:

```go
s, err := pgxdb.NewSchema(os.Getenv("AUTH_SCHEMA")) // validated here, once
if err != nil { return err }

// one stream per schema, applied pre-boot, with its own ledger inside s
if err := pgxdb.RunMigrations(ctx, db, pgxfs, dir, pgxdb.WithSchema(s)); err != nil { return err }

repos, err := pgx.Repositories(ctx, db, pgx.WithSchema(s))
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
tree — `0001`–`0022` (seventeen tables; auth owns no delivery table). Same filename
= same logical schema step; content is per-dialect. After export, the host owns
the final migration stream and applies it pre-boot through its own ledger.

### Audit rail (`0021` and `0022`)

Both files require **PostgreSQL 14 or later** (`CREATE OR REPLACE TRIGGER`) and
begin with `SET LOCAL lock_timeout = '5s'`: the pending stream runs in one
transaction, so a file blocked behind a long-running reader aborts the whole
stream — nothing applied — and the host retries, instead of queueing logins
behind it.

**`0021_security_events_integrity.sql` is REQUIRED.** It adds `request_id` and
`trace_id` (`TEXT NOT NULL DEFAULT ''`, read and written by the store) and two
evidence columns no store names: `seq`, from the `security_events_seq` sequence
(insertion order; a rolled-back write leaves a gap, so it is not a deletion
detector), and `recorded_at`, the database clock. Both are added NULL and
defaulted afterwards — no rewrite — so a NULL marks a row written before the
migration. Never backfill them with an `UPDATE`: the trigger refuses it for the
runtime role, and a backfilled value is not evidence. `Repositories` probes
`security_events.request_id` and refuses to boot without it, naming the file. The
file ends with `GRANT USAGE ON SEQUENCE security_events_seq TO PUBLIC`: `nextval`
needs it, so a runtime role without it loses every audit row.

**`0022_security_events_immutable.sql` is the immutability trigger**, on by
default. `security_events_refuse_write()` raises SQLSTATE 42501 (mapped to
`sdk.ErrForbidden`) on UPDATE, DELETE and TRUNCATE for every role that is not a
member of the table's owning role; the owner — the migrator, or a SECURITY
DEFINER function it owns — passes, and an owner's UPDATE persists. Both triggers
are `ENABLE ALWAYS`, so `session_replication_role = replica` does not silence
them. Re-running the file is safe. **A host that declines the trigger deletes
the file from its ledger before applying it**; the boot probe then judges the
role by its privileges alone. Edit neither the function nor the triggers: the
probe compares them with this store's canonical copy. A future migration that
re-creates `security_events` drops the triggers with it and ships a paired file
that re-creates them. No path writes this table except a plain INSERT — never an
upsert, and on the turso sibling never `INSERT OR REPLACE`, which deletes rows
without firing DELETE triggers.

**The posture probe.** In production `Repositories` refuses a runtime role that
is a superuser, owns the table, its schema or the trigger function, or holds
UPDATE, DELETE, TRUNCATE or TRIGGER without the verified trigger pair
(`pgxdb.ErrAppendOnlyTableWritable`; `pgxdb.Config.AppendOnlyWritableAcknowledged`
waives it), and a role that cannot append — no INSERT, or no USAGE on
`security_events_seq` (`pgxdb.ErrAppendOnlyTableUnwritable`, never waivable).
Development logs one `append_only=unenforced` line instead; the zero mode is
silent. **A single-role production host refuses to boot after upgrading**: run
the server as a separate runtime role with INSERT and SELECT on the rail and
`GRANT USAGE ON SEQUENCE security_events_seq` — the
[two-role posture](../../../../workshop/documentation/docs/guides/database-posture.md#two-roles).

**`0020_external_subjects.sql` is append-only and REQUIRED.** It creates
`external_subjects`, the external identity mirror keyed `(provider, subject)`, and
touches no existing table. Export and apply it before deploying this adapter
version, including hosts that wire no external provider: `Repositories` probes the
table at boot. `NewExternalSubjectStore` implements `ExternalSubjects`;
`UserStore.Provision` inserts `InitialCredentials.External` in the user's
transaction, so a taken subject rolls the whole provisioning back.

**`0019_oauth2_sessions.sql` is append-only.** Apply it before deploying this
adapter version, including hosts that have not enabled OAuth endpoints. It adds
session profile/delegation columns and three OAuth tables. Existing sessions
backfill to `first_party`. OAuth grants create separate `delegated` rows; refresh
rotation retains every spent hash through the fixed session expiry. Reuse commits
revocation of that connection only. `NewOAuth2Store` implements both `OAuth2` and
`SessionManagement`; the bundle supplies the same store for both ports. Global
revocation increments `auth_revision` and removes all sessions in one transaction,
fencing outstanding authorization codes. Ordinary browser logout and individual
connection revocation do not advance that fence.

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

The audit-rail cases (`audit_integrity_live_test.go`) build their own
disposable schema owned by a run-unique migrator role and connect as run-unique
runtime roles — table grants with and without USAGE on the sequence — dropping
every role with `DROP OWNED BY` + `DROP ROLE` at cleanup. They need a DSN whose
user can create roles.

`make check` stays hermetic (the suite skips); `make test-stores` runs this
live path expecting `POSTGRES_TEST_DSN`.
