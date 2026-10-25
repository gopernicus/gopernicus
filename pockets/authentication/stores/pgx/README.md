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
| `Repositories(ctx context.Context, db *pgxdb.DB, opts ...Option) (auth.Repositories, error)` | the full bundle including `OAuth2` and `SessionManagement`, no migration side effects — probes canonical tables and ALTER-added columns in migration order, and errors (`sdk.ErrNotFound`, naming the table or column + the `authentication` migration source) when one is missing; an infrastructure/query failure is never misreported as a missing table; it refuses a database without the `0024` functions or the `0025` `passkey_credentials` table the same way, naming the file; it never probes `0026` (see [TOTP](#totp-0026)). Last it runs `db.ProbeAppendOnly(ctx, AuditTables(opts...)...)` under the `RuntimeMode` `db` was opened with (see [Audit rail](#audit-rail-0021-and-0022) and [Retention](#retention-0023-and-0024)). The bundle fills `Maintenance` (`NewMaintenanceStore`) and `UserErasure` (`NewErasureStore`); `ServiceAccountStore` implements `serviceaccount.CascadeDeleter` |
| `AuditTables(opts ...Option) []pgxdb.AppendOnlyTable` | the append-only tables this store owns — `security_events`, schema-qualified under `WithSchema`, verified against the trigger function `0022` ships, with the one `DefinerFunction` `0024` ships (`security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)`). A migrator preflights the runtime role with `pgxdb.InspectTablePrivileges(ctx, q, role, pgx.AuditTables(opts...)...)`, deciding exactly as the boot probe will |
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
tree — `0001`–`0026` (twenty tables; auth owns no delivery table). Same filename
= same logical schema step; content is per-dialect. After export, the host owns
the final migration stream and applies it pre-boot through its own ledger.

### Passkeys (`0025`)

**`0025_passkey_credentials.sql` is REQUIRED** — without it the boot fails. It creates
`passkey_credentials` — one registered WebAuthn credential per row, keyed by
`id`, with a unique `credential_id` and a `(user_id, created_at, id)` index — and
touches no existing table, so its indexes build on an empty table (no
`CONCURRENTLY` step). `Repositories` refuses, in every mode, a database without
it with `sdk.ErrNotFound` naming `0025_passkey_credentials.sql` — including hosts
that wire no passkeys: export it (a host ledger gets a byte-identical copy) and
apply it before deploying this adapter version.
The bundle fills `Passkeys` (`NewPasskeyStore`). `Create` runs its guard under the
`users` row lock the credential-mutation rail takes (revision, bootstrap, the
credential id, the cap, the user handle) and bumps nothing; `RecordUse` is one
conditional `UPDATE` on the stored sign count. `CredentialMutationStore.Snapshot`
lists the user's passkeys and `Apply` removes one (`RemovePasskey`) or all
(`RemovePasskeys`), revoking every session; erasure deletes and counts them.

### TOTP (`0026`)

**`0026_totp.sql` ships; it is required only when `WithTOTP` is wired** — apply
it ahead of time to toggle TOTP on later. It creates `totp_credentials` (one
authenticator-app credential per user, keyed by `user_id`: the sealed secret, the
pending-enrollment columns, `last_used_step`, the lockout counters) and
`recovery_codes` (one row per unspent code: its digest under `key_id`, unique per
`(user_id, code_digest)`, with `(user_id)` and `(key_id)` indexes), and touches no
existing table, so its indexes build on empty tables (no `CONCURRENTLY` step).
`Repositories` never probes it: a host that never wires TOTP boots without it.
The bundle fills `TOTP` (`NewTOTPStore`), which implements `totp.SchemaProber`;
the pocket calls `ProbeSchema` only under `WithTOTP`, and a database without
either table then refuses the boot with `ErrTOTPSchemaMissing` wrapping
`sdk.ErrNotFound`, naming `0026_totp.sql`. Export it (a host ledger gets a
byte-identical copy) before wiring TOTP.
`Confirm` and `Remove` take the `users` row lock the credential-mutation rail
takes FIRST, then the credential row; `ReplaceRecoveryCodes` and
`ConsumeRecoveryCode` lock the credential row before the codes; `RecordUse` (a
compare-and-swap on the step) and `RecordFailure` (one atomic increment that sets
the lockout and resets the count in the same statement) are single statements on
`totp_credentials`. `Remove` of a confirmed credential deletes both tables' rows,
revokes every session and grant and bumps `auth_revision`; of a pending one, only
the row. Erasure deletes and counts both tables only when `EraseInput.TOTP` says
TOTP is wired, and otherwise never names them.

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

### Retention (`0023` and `0024`)

Both files are **REQUIRED** and begin with `SET LOCAL lock_timeout = '5s'`.
`Repositories` refuses, in every mode, a database without `users.erased_at`
(naming `0023_retention.sql`) or without either `0024` function (naming
`0024_security_events_retention.sql`). **Apply them in their own runner
invocation**, before deploying a binary built against this store: a binary
built before them keeps running on a migrated database — a nullable column,
indexes and functions it never calls. Rolling the binary back is safe (an old
binary reads an erased user as `deactivated`); rolling the schema back is
forward-fix only — there are no down files, and dropping `erased_at` after an
erase loses the marker.

**`0023_retention.sql`** builds the `expires_at` indexes the maintenance purges
select through — `idx_sessions_expires_at`, `idx_authentication_grants_expires_at`,
`idx_contact_changes_expires_at`, `idx_oauth_states_expires_at`,
`idx_challenges_expires_at`, `idx_oauth_authorization_codes_expires_at`,
`idx_oauth_clients_expires_at`, `idx_oauth_refresh_history_expires_at`
(invitations get none: one row per invite) — each `CREATE INDEX IF NOT EXISTS`,
and only then adds `users.erased_at TIMESTAMPTZ NULL`. The `ADD COLUMN` is
metadata-only but takes ACCESS EXCLUSIVE on `users`, so it comes last: the
stream waits on `users` only for that one statement, never behind an index
build. A plain `CREATE INDEX` blocks writes to its table while it builds; **on a
large host, pre-build them first**, outside the runner, then apply the file —
`IF NOT EXISTS` makes each in-stream build a no-op:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
-- … the same for the other seven, then confirm none is left invalid:
SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
 WHERE c.relname LIKE 'idx_%_expires_at' AND NOT i.indisvalid;  -- drop and rebuild any row
```

An erased user keeps `status = 'deactivated'` — every credential path and the
fenced mint already refuse it — and every read (`Get`, the directory's
`GetSummary`/`List`, the redemption reads) projects `user.StatusErased` from
`erased_at IS NOT NULL`. `SetStatus` refuses `StatusErased` as a target and
refuses reactivating an erased user (`user.ErrErased`); deactivating one is a
no-op replay.

**`0024_security_events_retention.sql`** gives the rail its only purge path.
The runtime role holds no DELETE on `security_events`, so the store calls a
SECURITY DEFINER function owned by the table's owner:

- `security_events_retention_floor()` — `STABLE`, returns `365 days`. **The floor
  is a framework invariant**: no procedure lowers it, and the Go minimum
  retention (366 days) sits above it so the bundled service can never configure
  below it.
- `security_events_purge_before(p_before pg_catalog.timestamptz, p_max_rows
  pg_catalog.int4)` — SECURITY DEFINER. It refuses a NULL cutoff and one younger
  than `now() - floor` with SQLSTATE 22023 (`maintenance.ErrBelowRetentionFloor`
  — refused, never clamped), clamps `p_max_rows` to `[1, 10000]`
  (`maintenance.MaxBatchSize`), deletes the oldest rows `created_at <= p_before`
  through a `FOR UPDATE SKIP LOCKED` subselect (two concurrent purges never wait
  on each other) and appends ONE `security_events_purged` row per call — a zero
  count included — with Details `cutoff`, `count`, `floor_days`, `session_user`
  (the connecting role; behind a pooler, the pooler's login role) and
  `max_rows`. Every function and operator in both bodies is
  `pg_catalog`-qualified.

The file first asserts that it runs in the schema holding `security_events`
and that the migrator is a member of the table's owner (`make migrate` fails
before boot otherwise, naming the fix), then pins the definer's `search_path` to
`pg_catalog, <the table's schema>, pg_temp` — computed from the table, quoted
when the name needs quotes, `pg_temp` last — and ends with `REVOKE EXECUTE …
FROM PUBLIC`. After a restore into a renamed schema, re-run its pin `DO` block.
The migration cannot know your runtime role, so **grant it yourself**:

```sql
GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO <runtime role>;
```

The store calls it with explicit casts, schema-qualified under `WithSchema`.
`Maintenance` must be backed by the database that holds the rail: the function
probe at construction runs on the same connection the purge does.

**What the probe checks.** `AuditTables` lists the purge function, so
`ProbeAppendOnly` resolves it on the runtime role's `search_path` (before it pins
its own) and judges it by catalog evidence, never by comparing its body:
production **refuses** (`pgxdb.ErrDefinerFunctionUnsafe`, not waivable) when
PUBLIC can execute it, its `proconfig` is not exactly the pinned `search_path`,
or it resolves outside the table's schema; it **WARNs** (`definer_function=
inoperative`) when it is not SECURITY DEFINER, its owner is not a member of the
table's owner, its owner is a superuser, or the runtime role holds CREATE on the
schema. A runtime role without EXECUTE boots in production with one WARN,
`maintenance_execute=missing … fix=GRANT EXECUTE …`; the maintenance pass then
reports `step=security_events error_kind=forbidden` while every other step runs.
Probe hygiene: `REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC`, and run
migrations as a `NOSUPERUSER` owner role. A migrator preflights the runtime role
with the same `AuditTables`; the reference host's `go run ./workshop/migrations
-posture-role <role>` prints `functions=[security_events_purge_before:execute=…
unsafe=… inoperative=…] floor=365 days` and decides as the boot does.

**Erasure** (`ErasureStore.Erase`) is one transaction in the port's order:
`SELECT … FOR UPDATE` on the user (the mint's and the service-account insert's
`FOR SHARE` wait on it); the invitations addressed to one of the user's
identifier pairs (less any pair another user holds active) or resolved to the
user, locked `FOR UPDATE` so a concurrent `ClaimAcceptance` waits and then finds
the row gone or unchanged; a refusal (`user.ErrEraseBlockedByClaim`) when one is
accepting; then the writes and the `user_erased` row by plain INSERT, inside the
transaction. `ServiceAccountStore.Delete` cascades to the account's API keys
under `FOR UPDATE` on the account (`APIKeyStore.Create` takes `FOR SHARE` on it),
and `Create` takes `FOR SHARE` on the owner and refuses an erased one with
`user.ErrErased`. Known residual: a key minted while a cascade of its account
commits FIRST is still inserted (the store never refused keys for an unknown
account) — it is inert, because API-key authentication resolves the account and
finds none. Erase is not a ban: the next external login provisions a fresh
user — deactivate instead to keep someone out.

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

Session time fields come back at microsecond precision from every insert path
(`Create`, `CreateForActiveUser`, `RedeemCode`, the passwordless redemption) —
exactly what `Get` reads — and the suite's sub-microsecond fixture clock proves it.

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
user can create roles. The retention cases (`maintenance_live_test.go`,
`erasure_live_test.go`, `upgrade_live_test.go`) do the same with a runtime role
in the two-role posture (no UPDATE, DELETE or TRUNCATE on the rail); under
`POSTGRES_TEST_SCHEMA` their schema's name is mixed case, so the pinned
`search_path` is quoted.

`make check` stays hermetic (the suite skips); `make test-stores` runs this
live path expecting `POSTGRES_TEST_DSN`.
