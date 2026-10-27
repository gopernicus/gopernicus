---
title: Production database posture
description: Roles and grants, encryption at rest and backups, time, timeouts and the current TLS behavior of the connectors.
---

# Production database posture

Host-owned controls around the pocket stores, stated for `main` at 2026-10-23.

## Two roles

The scaffold uses one `DB_URL` for the migration runner and the server. Split them:

- a **migrator** role that owns the schema, every table and sequence, the trigger
  functions and the definer functions, and runs `make migrate` pre-boot. Make it
  `NOSUPERUSER` (`CREATE ROLE migrator LOGIN NOSUPERUSER`): a superuser owner makes a definer
  function inoperative, and a docker image's default `postgres` role is a superuser;
- a **runtime** role the server connects as, with data access only.

A migrator-role `*DB` and a runtime-role `*DB` over one database are two datastore instances.
Their transactions do not join, and they can block each other: an inner transaction on one waits
on locks the outer holds on the other, unbounded without `DB_LOCK_TIMEOUT`. Never run the
migrator inside a runtime transaction; migrations already run pre-boot. See *Persistence*, "Two
databases never share a transaction".

A host that uses row-level security also keeps the runtime role `NOBYPASSRLS` and owning no
table: see [Row-level security with pgxdb](./row-level-security.md), "Roles".

Create the runtime role with SQL, not the provider console: Neon console and API roles carry
`pg_write_all_data` (through `neon_superuser`), which no per-table `REVOKE` removes, and a role
holding `rds_superuser` can set `session_replication_role`.

```sql
-- as the migrator
ALTER SCHEMA public OWNER TO migrator;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM runtime;
GRANT USAGE ON SCHEMA public TO runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO runtime;

-- the audit rails are append-only: the runtime role reads and appends
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON security_events FROM runtime;  -- pockets/authentication
GRANT USAGE ON SEQUENCE security_events_seq TO runtime;
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON iam_audit FROM runtime;        -- pockets/authorization
GRANT USAGE ON SEQUENCE iam_audit_seq TO runtime;
REVOKE UPDATE, TRUNCATE, TRIGGER ON iam_operations FROM runtime;           -- DELETE stays granted
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON secrets_audit FROM runtime;    -- pockets/secrets
GRANT USAGE ON SEQUENCE secrets_audit_seq TO runtime;

-- the rail's only purge path (authentication 0024): the runtime role calls the definer
GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO runtime;

-- the password-hash table can be narrowed further; the store needs SELECT/INSERT/UPDATE/DELETE
```

Adjust the schema name when a store is mounted with `WithSchema`. `DELETE` stays granted on
`iam_operations` (an earlier version of this recipe revoked it — grant it back): its trigger
refuses the delete of a fenced row and admits the expiry of the others. The integrity migrations
grant `USAGE` on each rail's sequence to `PUBLIC`, so a host that already split its roles keeps
inserting; granting it to the runtime role and revoking it from `PUBLIC` is optional hardening.
The pockets issue no `UPDATE` or `DELETE` against the audit rails as the runtime role, so the
revokes cost nothing at runtime. Nor do they take row locks there: every row-lock clause
(`FOR UPDATE`, `FOR SHARE`, …) needs `UPDATE`, which this recipe revokes, so the `iam_operations`
expiry deletes without one. A `SECURITY DEFINER` function runs as its owner and may lock —
`security_events_purge_before` selects its batch `FOR UPDATE SKIP LOCKED`.

`0024_security_events_retention.sql` cannot know the runtime role, so the `GRANT EXECUTE` above is
yours; without it the server boots with one `maintenance_execute=missing` WARN and the
maintenance pass reports the rail step `forbidden` every pass. The migration itself refuses to
apply unless it runs in the rail's schema as a member of the table's owner.

Ownership is broader than the table's owner. A runtime role counts as an owner when it is a
member — `NOINHERIT` included — of the role that owns the table, of the role that owns its
schema (it can drop the table; `public` belongs to `pg_database_owner`, which
managed providers' default roles reach — hence `ALTER SCHEMA public OWNER TO migrator`), or of
the role that owns a trigger function (it can replace the body). The immutability triggers exempt
the owning role by design, so the migrator can backfill and a migrator-owned `SECURITY DEFINER`
function passes; that is why an owner — or a superuser — is refused as the runtime role.

The triggers ship `ENABLE ALWAYS`, so `session_replication_role = replica` cannot silence them.
`pg_restore --disable-triggers` leaves them `tgenabled=O`; after a restore, re-run
`ALTER TABLE <t> ENABLE ALWAYS TRIGGER <t>_immutable` and `… <t>_no_truncate` on each rail.

What probes this now: each pgx store with an audit table (from authentication `stores/pgx`
v0.10.0, authorization `stores/pgx` v0.20.0 and secrets `stores/pgx` v0.2.0) runs the
`pgxdb` append-only probe when it is constructed. In production it refuses to boot when its
runtime role is superuser, owns an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without
the verified triggers, or cannot insert into it; development logs one `append_only=unenforced`
line. The scaffold's pgx runner makes the same decision from the migrator's connection before a
deploy: `go run ./workshop/migrations -posture-role <runtime role>` prints one
`protected_by=privilege|trigger|waiver|none` line per table and exits non-zero on `none` or a
role that cannot append; for a table with definer functions it adds
`functions=[security_events_purge_before:execute=true|false unsafe=… inoperative=…] floor=365 days`
and exits non-zero on a missing or unsafe function (a missing `EXECUTE` or an inoperative function
is reported, as the boot WARNs on both); archive its output. SQLite (`turso` in `file:` mode) has
no privileges: the turso stores check the trigger text and WARN in production; restrict the file
and its backups.

### Definer functions

A definer function does for the runtime role what its own grants refuse, so the boot probe
judges it by catalog evidence (never its body) and the rules are:

- **owner = the migrator**, a member of the table's owner — the immutability triggers exempt the
  owning role, so the definer can delete past them; never a superuser;
- **`search_path` pinned** to exactly `pg_catalog, <the table's schema>, pg_temp` — `pg_temp`
  LAST, so a caller's temporary table cannot stand in for the rail — computed from the table's
  namespace, never `current_schema()` or `SET search_path FROM CURRENT`;
- **PUBLIC's `EXECUTE` revoked**, then granted to the runtime role alone.

Production refuses an unsafe function (`pgxdb.ErrDefinerFunctionUnsafe`, not waivable): PUBLIC
can execute it, its `search_path` is not the pinned value, or it resolves outside the table's
schema. It WARNs (`definer_function=inoperative`) on an owner/definer mismatch: not `SECURITY
DEFINER`, an owner that is not a member of the table's owner, a superuser owner, or `CREATE` on
the schema for the runtime role (hence `REVOKE CREATE … FROM PUBLIC`); the function then fails
when called. The floor `security_events_purge_before` reads, `security_events_retention_floor()`
(365 days), is a framework invariant: the role split is what keeps the runtime role from
replacing it, and no procedure lowers it.

**Host-owned rails.** `iam_audit` and `secrets_audit` ship no purge function; their retention is
the host's. The template, applied by the migrator as a host migration (repeat it for
`secrets_audit`, whose time column is also `occurred_at`):

```sql
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION iam_audit_purge_before(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)
RETURNS pg_catalog.int8
LANGUAGE plpgsql SECURITY DEFINER AS $iam_audit_purge_before$
DECLARE
  n pg_catalog.int8;
BEGIN
  IF p_before IS NULL OR p_before OPERATOR(pg_catalog.>) (pg_catalog.now() OPERATOR(pg_catalog.-) '<your floor>'::pg_catalog.interval) THEN
    RAISE EXCEPTION 'iam_audit_purge_before: cutoff % is NULL or younger than the retention floor', p_before USING ERRCODE = '22023';
  END IF;
  DELETE FROM iam_audit WHERE id IN (
    SELECT id FROM iam_audit WHERE occurred_at OPERATOR(pg_catalog.<=) p_before
    ORDER BY occurred_at, id LIMIT LEAST(GREATEST(p_max_rows, 1), 10000) FOR UPDATE SKIP LOCKED);
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$iam_audit_purge_before$;
DO $$ BEGIN
  EXECUTE pg_catalog.format('ALTER FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) SET search_path = pg_catalog, %I, pg_temp',
    (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = 'iam_audit'::pg_catalog.regclass));
END $$;
REVOKE EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO runtime;
```

The host's cron calls it in batches as the runtime role and records each returned count as its
retention evidence. The stores' `AuditTables` list no host function, so the boot probe never
inspects it; to preflight it, add `pgxdb.DefinerFunction{Signature:
"iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4)"}` to that table's `Functions` in
the runner's `postureTables`, and `-posture-role` judges it with the same rules.

## Connection security

- `pgxdb` classifies every connection target (the primary and each fallback) before any
  I/O: *verified* (`verify-full`, `verify-ca`, `require` with `sslrootcert`,
  `sslrootcert=system`), *local* (a unix socket), *unverified* (a bare `require`) or
  *plaintext* (`disable`, or the fallback that `prefer`/`allow` keep). With
  `Config.RuntimeMode` production, an unverified or plaintext target is refused
  (`ErrTLSRequired`) unless the composition root sets `TLSUnverifiedAcknowledged` (bare
  `require`) or `TLSDisabledAcknowledged` (all); development logs a WARN. The zero
  `RuntimeMode` is mode-blind. Use `sslmode=verify-full` (`DB_SSLMODE=verify-full`) with the
  provider's CA; the `init` scaffold emits `DB_SSLMODE=verify-full` with the local-docker
  `disable` line commented out (shipped S1).
- `turso` under production refuses a hosted URL without TLS — `http://`, `ws://`, or
  `libsql://…?tls=0` — with `ErrTLSRequired` unless `TLSDisabledAcknowledged`; `file:` is
  exempt. Use `libsql://`, `https://` or `wss://`.
- The DSN is a secret (it carries the password): `pgxdb` `DSN`/`Password` and `turso`
  `URL`/`AuthToken` are `sdk.Secret`, which prints `[redacted]`; read one with `Reveal()`. `Open`
  refuses a DSN or URL composed from a printed Secret (`sdk.ErrRedactedSecret`), and the
  connectors redact the target from errors and logs (shipped S6).
- `DB_LOG_QUERIES=true` (`LogQueries`) logs each query's SQL with its argument count and types —
  never values — plus the SQLSTATE and constraint (pgx) of a failed query, never its error text.
  `DB_LOG_QUERY_ARGS=true` (`LogQueryArgs`) adds bind values (and, on pgx, the error text),
  development only, and is inert without `DB_LOG_QUERIES`. Production refuses any query logging
  on both connectors (`ErrQueryLoggingRefused` naming `Config.LogQueries (DB_LOG_QUERIES)`, no
  acknowledgement), as pgxdb refuses a `Tracer` that is or wraps `*LoggingQueryTracer` (shipped
  S6).

## Encryption at rest and backups

The framework encrypts specific columns (provider OAuth tokens when `TokenEncrypter` is wired,
queued delivery envelopes, vault secrets); everything else relies on the database. Host
requirements: encrypted storage, encrypted backups, and a backup retention aligned with the
retention periods you set for the tables in *Operations runbooks*. Point-in-time recovery and
replica retention follow the same clock. A backup of the authentication database contains
password hashes, token digests, identifiers and the audit rail; treat it as the production
database for access purposes.

## Time

Audit timestamps (`security_events.created_at`, `iam_audit.occurred_at`,
`iam_operations.committed_at`, `secrets_audit`) come from the application clock in UTC, and
rows have random ids; the stores page by these. Since S2 each rail also carries evidence the
database writes and no store reads: `recorded_at`, the database clock at insert, and on pgx
`seq` (not on `iam_operations`), a sequence value that proves the order of insertion. Both are
NULL on rows written before the migration. A sequence is non-transactional, so a rolled-back
write leaves a gap: a gap is not evidence of deletion — the triggers and the role split are.
Run NTP on every instance; the application timestamps are still what cross-instance ordering of
the API reads rests on.

## Timeouts

`pgxdb.Config` sets pool sizes and lifetimes, not statement timeouts. Set them on the DSN:

```
postgres://runtime:…@host:5432/app?sslmode=verify-full&options=-c%20statement_timeout%3D30000%20-c%20lock_timeout%3D5000
```

## Migrations

Migrations are host-owned and applied pre-boot by the host runner, never by the framework.
The runner applies files in name order inside one transaction and records
`(source, version, checksum, raw_sql, applied_at)`; a modified already-applied file fails with
a checksum mismatch. The runner takes no advisory lock, so run exactly one instance of it per
deploy.

Every S2 pgx store migration begins with `SET LOCAL lock_timeout = '5s'`. The pending files run
in one transaction, and `ADD COLUMN` and `CREATE TRIGGER` queue behind a running `pg_dump`; with
the timeout a blocked file aborts the whole stream — nothing is applied — and you re-run the
runner, instead of holding locks that stall login inserts for the runner's whole budget. The S2
pgx files require PostgreSQL 18 or later, the pgxdb migration floor.

**Apply the S3 files in their own runner invocation**, before deploying the binaries built
against them: authentication `0023_retention.sql` (required; its `users.erased_at` `ADD COLUMN`
comes last) and `0024_security_events_retention.sql` (required), and the admitted index files —
authorization `0007_iam_operations_expiry.sql`, jobs `0005_job_queue_terminal_purge.sql`, cms
`0022_inquiries_created_at.sql`. Their indexes are plain `CREATE INDEX IF NOT EXISTS` builds,
which block writes to the table while they run; on a large table pre-build each one first,
outside the runner, under the same name, so the in-stream build is a no-op:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
-- … each index the files create, then:
SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE NOT i.indisvalid;
```

A failed concurrent build leaves an INVALID index that `IF NOT EXISTS` keeps — `DROP INDEX` it
first, then build it again.
