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
with `db.ProbeAuditImmutable` last.

## Migrations

`migrations/*.sql` carry the same version (filename) set as the pgx tree,
`0001`–`0022` with `0017` absent.

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
- PostgreSQL hosts use the pgx sibling instead: it needs PostgreSQL 14 or later
  and a two-role posture whose runtime role holds USAGE on the
  `security_events_seq` sequence. Turso has neither roles nor a sequence.

## Testing

`go test ./...` is hermetic: it includes a local-file SQLite run of the
conformance suite and of the audit-rail cases (`audit_integrity_test.go`). The
live leg runs with `-tags=integration` against `TURSO_DATABASE_URL` and
`TURSO_AUTH_TOKEN`, skipping loudly without them. It is DESTRUCTIVE — it drops
and re-creates every auth table — so point it only at a disposable database. Its
reset drops the two triggers before `DELETE FROM` and re-executes the embedded
`0022` after.

```sh
TURSO_DATABASE_URL=http://127.0.0.1:8080 TURSO_AUTH_TOKEN=local \
  go test -tags=integration ./...
```
