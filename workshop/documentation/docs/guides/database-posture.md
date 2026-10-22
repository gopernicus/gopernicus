---
title: Production database posture
description: Roles and grants, encryption at rest and backups, time, timeouts and the current TLS behavior of the connectors.
---

# Production database posture

Host-owned controls around the pocket stores, stated for `main` at 2026-10-22.

## Two roles

The scaffold uses one `DB_URL` for the migration runner and the server. Split them:

- a **migrator** role that owns the schema and runs `make migrate` pre-boot;
- a **runtime** role the server connects as, with data access only.

```sql
-- as the owner / migrator role
REVOKE CREATE ON SCHEMA public FROM runtime;
GRANT USAGE ON SCHEMA public TO runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO runtime;

-- the audit rails are append-only at the Go port only; make the database agree
REVOKE UPDATE, DELETE, TRUNCATE ON security_events FROM runtime;   -- pockets/authentication
REVOKE UPDATE, DELETE, TRUNCATE ON iam_audit, iam_operations FROM runtime;  -- pockets/authorization
REVOKE UPDATE, DELETE, TRUNCATE ON secrets_audit FROM runtime;     -- pockets/secrets

-- the password-hash table can be narrowed further; the store needs SELECT/INSERT/UPDATE/DELETE
```

Adjust the schema name when a store is mounted with `WithSchema`. Nothing probes these grants
at boot today (a privilege probe is planned, S2). The pockets issue no `UPDATE` or `DELETE`
against the audit tables, so the revokes cost nothing at runtime. SQLite (`turso` in `file:`
mode) has no privileges: restrict the file and its backups instead.

## Connection security

- `pgxdb` forwards `sslmode` verbatim and validates nothing; unset falls back to libpq's
  `prefer`, which negotiates TLS when offered and silently continues without it. Use
  `sslmode=verify-full` (`DB_SSLMODE=verify-full`) with the provider's CA. The `init`
  scaffold currently emits `DB_SSLMODE=disable`; change it before the first deploy (a
  `verify-full` default and a production refusal are planned, S1).
- `turso` accepts any URL scheme; use `libsql://`, `https://` or `wss://` only.
- The DSN is a secret (it carries the password); the connectors redact it from errors and
  logs. `DB_LOG_QUERIES=true` logs bind arguments verbatim — never in production.

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
rows have random ids, not a sequence. Run NTP on every instance; without it, cross-instance
ordering of audit rows is contestable (a database sequence is planned, S2).

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
