# pockets/authentication/stores/turso

The auth pocket's **Turso / libSQL** store adapter — the dialect sibling of
`pockets/authentication/stores/pgx`. Its own module so a host that brings a
different datastore never pulls libSQL into its module graph. It owns the SQL and
the canonical migration files; the host owns its database lifecycle and applies
the migrations pre-boot (`ExportMigrations`, then its own runner).

`Repositories(ctx, db)` returns the full bundle after probing every canonical
table and ALTER-added column, refusing with `sdk.ErrNotFound` naming the missing
table or migration file. `AuditTables()` lists the append-only tables this store
owns with the immutability triggers `0022` creates; `Repositories` checks them
with `db.ProbeAuditImmutable` last. The bundle fills `Maintenance`
(`NewMaintenanceStore`, the retention port) and `UserErasure`
(`NewErasureStore`); `ServiceAccountStore` implements
`serviceaccount.CascadeDeleter` — see [Retention](#retention-0023-and-0024).

## Migrations

`migrations/*.sql` carry the same version (filename) set as the pgx tree,
`0001`–`0030` with `0017` and `0028` absent.

### Passkeys (`0025`)

**`0025_passkey_credentials.sql` ships; it is required only when `WithPasskeys`
is wired** — apply it ahead of time to toggle passkeys on later. It creates
`passkey_credentials` — one registered WebAuthn credential per row, keyed by
`id`, with a unique `credential_id` and a `(user_id, created_at, id)` index — and
touches no existing table. `Repositories` never probes it: a host that never
wires passkeys boots without it. The bundle fills `Passkeys`
(`NewPasskeyStore`), which implements `passkey.SchemaProber`; the pocket calls
`ProbeSchema` only under `WithPasskeys`, and a database without the table then
refuses the boot with `ErrPasskeySchemaMissing` wrapping `sdk.ErrNotFound`,
naming `0025_passkey_credentials.sql`. Export it (a host ledger gets a
byte-identical copy) before wiring passkeys. `Create` runs its guard under the
`BEGIN IMMEDIATE` write transaction the credential-mutation rail serializes on
(revision, bootstrap, the credential id, the cap, the user handle) and bumps
nothing; `RecordUse` is one
conditional `UPDATE` on the stored sign count. `CredentialMutationStore.Snapshot`
lists the user's passkeys and `Apply` removes one (`RemovePasskey`) or all
(`RemovePasskeys`), revoking every session; `SnapshotScoped`
(`credential.ScopedSnapshotter`) without passkeys — what the pocket reads while
passkeys are unwired — never names the table. Erasure deletes and counts them
only when `EraseInput.Passkeys` says passkeys are wired, and otherwise never
names the table.

### TOTP (`0026`)

**`0026_totp.sql` ships; it is required only when `WithTOTP` is wired** — apply
it ahead of time to toggle TOTP on later. It creates `totp_credentials` (one
authenticator-app credential per user, keyed by `user_id`: the sealed secret, the
pending-enrollment columns, `last_used_step`, the lockout counters) and
`recovery_codes` (one row per unspent code: its digest under `key_id`, unique per
`(user_id, code_digest)`, with `(user_id)` and `(key_id)` indexes), and touches no
existing table.
`Repositories` never probes it: a host that never wires TOTP boots without it.
The bundle fills `TOTP` (`NewTOTPStore`), which implements `totp.SchemaProber`;
the pocket calls `ProbeSchema` only under `WithTOTP`, and a database without
either table then refuses the boot with `ErrTOTPSchemaMissing` wrapping
`sdk.ErrNotFound`, naming `0026_totp.sql`. Export it (a host ledger gets a
byte-identical copy) before wiring TOTP.
`Confirm`, `Remove`, `ReplaceRecoveryCodes` and `ConsumeRecoveryCode` each run
in one `BEGIN IMMEDIATE` write transaction — the lock the credential-mutation
rail serializes on; `RecordUse` (a
compare-and-swap on the step) and `RecordFailure` (one atomic increment that sets
the lockout and resets the count in the same statement) are single statements on
`totp_credentials`. `Remove` of a confirmed credential deletes both tables' rows,
revokes every session and grant and bumps `auth_revision`; of a pending one, only
the row. Erasure deletes and counts both tables only when `EraseInput.TOTP` says
TOTP is wired, and otherwise never names them.

### Service-account status (`0027`)

**`0027_service_account_status.sql` ships; it is required only when
`AdministrationConfig.ServiceAccountStatus` is set** — apply it ahead of time to
turn the disable/enable routes on later. It creates `service_account_status`
(one row per account ever disabled: `status` in `active`/`disabled`,
`changed_at`, `changed_by`; an account without a row is active; no foreign key)
and touches no existing table. `Repositories` never probes it. The bundle fills
`ServiceAccountStatus` (`NewServiceAccountStatusStore`); the pocket calls its
`ProbeSchema` only when the flag is set, and a database without the table then
refuses the boot with `ErrServiceAccountStatusSchemaMissing`, naming the file.
`Disable` writes the status row and revokes every unrevoked key of the account
in one `BEGIN IMMEDIATE` write transaction; `Enable` writes `active` and
revokes nothing; an unknown account is `sdk.ErrNotFound`. `APIKeyStore`
implements `apikey.FilteredLister` (`ListByServiceAccountFiltered`): the status
and never-expires predicates run in SQL over the fixed-width TEXT timestamps —
revoked first, expired at or before the filter's instant.

### Session activity (`0029`)

**`0029_session_activity.sql` ships; it is required only when
`SessionsConfig.IdleTimeout` is set** — apply it ahead of time to turn idle
expiry on later. It adds one nullable column, `sessions.last_rotated_at TEXT`
(NULL for every existing row), written in exactly `created_at`'s fixed-width
TEXT encoding (`tursodb.FormatTime`), so the two order alike; every idle
comparison runs in Go, never in SQL. SQLite has no `lock_timeout`: the ALTER
briefly holds the database write lock, and a writer that outlasts the
connection's `busy_timeout` fails the file — **on lock timeout, re-run `make
migrate`**. `Repositories` never probes it, and every other statement names its
columns explicitly, so an older binary keeps working on a migrated table.
`SessionStore` implements `session.ActivityRepository`; the pocket calls its
`ProbeSchema` only when `IdleTimeout` is set, and a database without the column
then refuses the boot with `ErrIdleTimeoutSchemaMissing`, naming the file.
`GetByRefreshHashWithActivity` reads the row and its stamp in one `SELECT` over
a wired-only column list; `RotateAt` is `Rotate`'s compare-and-swap plus
`last_rotated_at = now` (the app clock) in one `UPDATE`; `Rotate` and
`ConsumeGrace` never write the stamp; `DeleteIfIdle` is a conditional `DELETE`
on `id`, `last_rotated_at IS` the observed stamp (NULL-safe), and the observed
`rotation_count`. All four run on the pool, never on a transaction the context
carries. The only supported manual write is the
grandfather reset, `UPDATE sessions SET last_rotated_at = NULL …`; never
backfill a value. A value the store did not write breaks that session's
refresh: `datetime('now')` (not the fixed-width encoding) fails every wired
refresh's scan, and `''` reads as unstamped but misses `DeleteIfIdle`'s
compare-and-swap, so an idle judgement fails as an internal error.

### OAuth2 code authentication (`0030`)

**`0030_oauth_code_authentication.sql` ships; it is required only when
`OAuth2Config.DelegatedAssurance` is set** — apply it ahead of time to turn
delegated assurance on later. It adds three nullable columns to
`oauth_authorization_codes` in the `sessions` encodings — `authenticated_at
TEXT` (`created_at`'s fixed-width encoding, `tursodb.FormatTime`),
`authentication_methods TEXT` (the JSON method array) and `assurance_level
TEXT` — NULL for every existing row. No index: the table holds only short-lived
codes and is pruned. The file applies in one transaction; the ALTERs briefly
hold the database write lock — **on lock timeout, re-run `make migrate`**.
`Repositories` never probes it, and every other statement names its columns
explicitly, so an older binary keeps working on a migrated table. `OAuth2Store`
implements `oauth2.CodeAuthenticationRepository`; the pocket calls its
`ProbeSchema` only when the flag is set, and a database without the columns then
refuses the boot with `ErrDelegatedAssuranceSchemaMissing`, naming the file.
`CreateCodeWithAuthentication` is `CreateCode`'s `BEGIN IMMEDIATE` transaction
and copies the approving session row it reads under that write lock into the
code row it inserts: no second lock, so a concurrent refresh rotation of the
approving session never deadlocks against it. A part the session did not record
is written NULL. `GetCodeWithAuthentication` reads the code and the copy in one
`SELECT` over a wired-only column list (NULL reads back as zero). Plain
`CreateCode` and `GetCode` never name the columns. `RedeemCode` is unchanged:
the core carries the copy onto the delegated session it persists.
`storetest.RunOAuth2CodeAuthentication` is its conformance suite; Turso is
proven against local libSQL only (no CI secrets).

**Rollout (in this order).** (1) Every process that verifies delegated tokens
— the issuing binary and any separate resource server or sidecar — runs
`pockets/authentication` ≥ v0.37.0, which admits `aal`/`methods` on delegated
tokens; (2) apply `0030`; (3) set `OAuth2Config.DelegatedAssurance`. Grants
approved before the flag keep NULL columns and stay `AssuranceUnknown` until the
user re-consents. **Rollback:** turn the flag off, wait one `AccessTTL`, then
downgrade. A `--posture` host that bumps to this store version copies `0030`
into its ledger (byte-identical, guard P9) even with the flag off.

### Rehash on login

`PasswordStore` implements `user.PasswordHashUpgrader` (no schema change).
`UpgradeHash` is one compare-and-swap statement, `UPDATE user_passwords SET hash = ? WHERE user_id = ? AND hash = ?`: it reports true
only when it replaced the row, so concurrent upgrades from the same old hash
have exactly one winner, and a stale old hash, a user with no password or an
unknown user report false with a nil error. Unlike `Set` and `Change` it is
not a credential change: `auth_revision` is untouched and no session, grant or
challenge is revoked. `storetest.RunPasswordHashUpgrade` is its conformance
suite.

Rehash-on-login is live with this store tag together with
`pockets/authentication` v0.35.0 and `integrations/cryptids/bcrypt` v0.3.0:
after a successful password verification the service re-hashes a password
stored below the hasher's cost and stores it through `UpgradeHash`.

### Global session revocation

`OAuth2Store` implements `session.GlobalRevoker`, filled as `GlobalSessions`
(no schema change). `RevokeAllBatch` is one `BEGIN IMMEDIATE` transaction per
page — the whole-database write lock, so every other writer (logins included)
waits behind each batch for up to the connection's `busy_timeout`: the next
page of active users after the cursor in byte order, `auth_revision` bumped,
then their sessions, step-up grants and `password_reset` challenges deleted.
`SweepOrphans` deletes, in one transaction, the sessions and grants of every
user that is absent or not active. Each batch and the sweep hold the database
write lock (`BEGIN IMMEDIATE`) for their whole transaction and are bounded only
by the context. A failed batch (a busy timeout or a cancelled context) leaves
the earlier batches committed, and the operation is safe to re-run.

### Audit rail (`0021` and `0022`)

**`0021_security_events_integrity.sql` is REQUIRED.** SQLite refuses `ADD COLUMN`
with an expression default, so it rebuilds `security_events` as
`security_events_next`, copying rows in `(created_at, id)` order and re-creating
the indexes. It adds `request_id` and `trace_id` (`TEXT NOT NULL DEFAULT ''`, read
and written by the store) and `recorded_at`, the database clock — an evidence
column no store names. The copy sets `recorded_at` NULL, so a NULL marks a row
written before the migration. Never backfill it with an `UPDATE`: the trigger
refuses it, and a backfilled value is not evidence. There is no sequence: `VACUUM`
renumbers `rowid`, so it proves nothing. The rebuild holds the write lock while it
copies — apply it in a maintenance window on a large table. `Repositories` probes
`security_events.request_id` and refuses to boot without it, naming the file.

**`0022_security_events_immutable.sql` is the immutability trigger pair**, on by
default: `security_events_immutable_update` and `security_events_immutable_delete`
abort UPDATE and DELETE with `security_events is append-only`, which the connector
maps to `sdk.ErrForbidden`. SQLite has no roles, so nothing is exempt. Each
trigger is dropped and re-created, so re-running the file is safe. **A host that
declines the triggers deletes the file from its ledger before applying it.** Edit
neither trigger: the boot probe compares both with this store's canonical copies
and, under a non-zero `RuntimeMode`, WARNs (`turso: audit table has no
immutability triggers; restore the migration file`) when one is missing or
altered. It never refuses — SQLite has no privileges to probe.

- **Paired re-create.** A future migration that rebuilds `security_events` drops
  the triggers with the old table, silently. Every such rebuild ships a paired
  file, sorted after it, that re-creates the pair.
- **`INSERT OR REPLACE` never.** It deletes the conflicting row without firing
  DELETE triggers. No path on this table may use it, or any upsert: the rail is
  INSERT-only.
- PostgreSQL hosts use the pgx sibling instead: it needs PostgreSQL 18 or later
  and a two-role posture whose runtime role holds USAGE on the
  `security_events_seq` sequence. Turso has neither roles nor a sequence.

### Retention (`0023` and `0024`)

Both files are **REQUIRED**: `Repositories` refuses, in every mode, a database
without `users.erased_at` (naming `0023_retention.sql`) or without row 1 of
`security_events_retention` (naming `0024_security_events_retention.sql`). Apply
them in their own runner invocation; a binary built before them keeps running on
a migrated database (a nullable column, indexes, and a table it never reads).

**`0023_retention.sql`** creates the `expires_at` indexes the maintenance purges
select through (`idx_sessions_expires_at`, `idx_authentication_grants_expires_at`,
`idx_contact_changes_expires_at`, `idx_oauth_states_expires_at`,
`idx_challenges_expires_at`, `idx_oauth_clients_expires_at`; the OAuth2 codes and
refresh history keep their `0019` expiry indexes), then adds `users.erased_at`
(`TEXT NULL`, a fixed-width `tursodb.FormatTime` timestamp) last. An erased user
keeps `status = 'deactivated'` — every credential path already refuses it — and
every read (`Get`, the directory, the redemption reads) projects
`user.StatusErased` from `erased_at IS NOT NULL`.

**`0024_security_events_retention.sql`** creates `security_events_retention`
(`id INTEGER PRIMARY KEY CHECK (id = 1)`, `floor_days INTEGER NOT NULL CHECK
(floor_days >= 1)`) and `INSERT OR IGNORE`s row 1 at 365 days, so re-running it
keeps an existing row. **The floor is a footgun guard, not a control:** SQLite has
no roles, and the connection that can purge can also drop the triggers. The store
refuses a cutoff younger than `now - floor_days` with
`maintenance.ErrBelowRetentionFloor`; the Go minimum retention (366 days) sits
above it. There is no procedure for lowering it.

**The rail purge** (`MaintenanceStore.PurgeSecurityEventsBefore`) is one short
`BEGIN IMMEDIATE` transaction per call, in this order: capture the triggers on
`security_events` from `sqlite_schema` (`tbl_name` matched `COLLATE NOCASE`) and
close the rows before any DDL; `DROP TRIGGER` exactly those; delete up to `limit`
rows with `created_at <= cutoff`, oldest first; re-execute the CAPTURED text
verbatim; then append one `security_events_purged` row by plain INSERT
(`actor_type='system'`, `actor_id='maintenance'`, Details `cutoff`, `count`,
`floor_days`, `max_rows`) — after the restore, so a host `AFTER INSERT` trigger
sees it. Restoring what was captured, never the embedded `0022`, means a host
that declined `0022` gets no trigger back and a host-altered trigger is not
"repaired". After COMMIT the store reads the trigger set again and logs ERROR
`error_kind=rail_triggers_changed` when it differs from the capture. A fault or a
cancelled context rolls the whole transaction back, triggers included.

Each batch holds the database's one write lock for its length: every login waits
behind it. Measured against a local `libsql-server` (the `-tags=integration` leg,
`TestRailPurgeAndEraseTimings_Turso`): one 1000-row purge batch from a
20,000-row rail took ~14 ms and one Erase ~10 ms. A remote Turso database adds
its round trips to both; size `maintenance.Config.BatchSize` (and, if logins
queue, pause between passes) from your own measurement.

**Erasure** (`ErasureStore.Erase`) runs the port's order in one `BEGIN
IMMEDIATE` transaction — the write lock is the row locks the pgx sibling takes —
and appends the `user_erased` row inside it. `ServiceAccountStore.Delete`
cascades to the account's API keys (`DeleteWithKeys` counts them), and `Create`
refuses an owner whose `erased_at` is set with `user.ErrErased`. Known residual:
a key minted right after a cascade of its account commits is still inserted (the
store never refused keys for an unknown account) — it is inert, because API-key
authentication resolves the account and finds none.

## Testing

`go test ./...` is hermetic: it includes a local-file SQLite run of the
conformance suite and of the audit-rail cases (`audit_integrity_test.go`). The
live leg runs with `-tags=integration` against `TURSO_DATABASE_URL` and
`TURSO_AUTH_TOKEN`, skipping loudly without them. It is DESTRUCTIVE — it drops
and re-creates every auth table — so point it only at a disposable database. Its
reset drops the two triggers before `DELETE FROM` and re-executes the embedded
`0022` after; it never clears `security_events_retention`, whose row survives.
The rail-purge file cases (`maintenance_localsqlite_test.go`: the restored
trigger set, a declined `0022`, fault injection after the drop and before
COMMIT, a host `AFTER INSERT` trigger, the post-commit check, the floor row, a
concurrent WAL reader) are untagged and run in `go test ./...`; their
libSQL-server twins and the timings are in `maintenance_integration_test.go`.

```sh
TURSO_DATABASE_URL=http://127.0.0.1:8080 TURSO_AUTH_TOKEN=local \
  go test -tags=integration ./...
```
