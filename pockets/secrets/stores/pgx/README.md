# pockets/secrets/stores/pgx

The secrets pocket's **PostgreSQL** store. Its own module so a host on another
datastore never pulls pgx into its graph. It owns the SQL and the canonical
migrations; the host applies them pre-boot through its own runner and the
store never migrates. The migrations require **PostgreSQL 18 or later**
(the pgxdb migration floor; `RunMigrations` refuses an older server).

## Surface

| member | shape |
|---|---|
| `Open(ctx, db *pgxdb.DB, opts ...Option) (vault.Repository, error)` | the boot constructor: `StatusCheck`, then the store |
| `NewRepository(db *pgxdb.DB, opts ...Option) *Repository` | the `vault.Repository` with no probe, for a host that runs `StatusCheck` itself; no migration side effects |
| `WithSchema(s pgxdb.Schema) Option` | places every table in `s` |
| `StatusCheck(ctx, db, opts...) error` | boot gate, **boot only**: every secrets table exists under the configured schema, `secrets_audit.outcome` exists (naming `0002_secrets_audit_integrity.sql` when not), then `db.ProbeAppendOnly(ctx, AuditTables(opts...)...)`. Wired to a readiness probe it repeats the development posture line on every probe |
| `AuditTables(opts ...Option) []pgxdb.AppendOnlyTable` | `secrets_audit` and its canonical trigger function, for `pgxdb.InspectTablePrivileges` when a migrator preflights the runtime role: the same verdict boot reaches |
| `ExportMigrations(dst string) error` | copies `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migrations |

## Schema

Three tables: `secrets_owner_keys` (one wrapped data key per owner),
`secrets_values` (sealed values, versioned, FK to the owner key without
cascade) and `secrets_audit` (append-only). Every write and `GetSecret` run in
one transaction with their audit rows. `ReplaceOwnerKey` is a compare-and-swap
on the stored key id; a mismatch is `sdk.ErrConflict`. `DeleteOwner` locks the
owner key first, under the default READ COMMITTED: a write that commits first
makes the delete a retried conflict; a write that arrives after the lock waits
for the delete and fails because the owner no longer exists
(`sdk.ErrInvalidReference`). The lock is held for the transaction; a
request-path caller carries a deadline — there is no `lock_timeout`.

Ordering columns compare under `COLLATE "C"` so pages match the memory store's
byte order whatever the database's default collation.

## The audit table

`secrets_audit` is append-only: the store only ever inserts into it.

| migration | status | what it does |
|---|---|---|
| `0001_secrets.sql` | required | the three tables |
| `0002_secrets_audit_integrity.sql` | **required** (`Open` refuses a database without it, naming the file) | adds `outcome` (`ok`/`denied`, CHECK added `NOT VALID`; `VALIDATE CONSTRAINT` is a later maintenance step), `request_id`, `trace_id`, and the evidence columns `seq` (from `secrets_audit_seq`) and `recorded_at` (the database clock), which the store never reads or writes. Every new column has a default, so a binary built before it keeps inserting; rows from before it keep `seq` and `recorded_at` NULL. It ends `GRANT USAGE ON SEQUENCE secrets_audit_seq TO PUBLIC` so a role that can insert today still can |
| `0003_secrets_audit_immutable.sql` | **default-on** | `secrets_audit_immutable` (`BEFORE UPDATE OR DELETE`, per row) and `secrets_audit_no_truncate` (`BEFORE TRUNCATE`), both `ENABLE ALWAYS`, refusing with `42501` (`sdk.ErrForbidden`) for every role that is not a member of the table's owner. A host that declines them deletes the file from its ledger before applying |

Both later files begin `SET LOCAL lock_timeout = '5s'`: the runner applies the
pending stream in one transaction, so a file blocked behind a long lock aborts
the stream and the host retries instead of stalling inserts.

Rules for the table:

- Never backfill it with `UPDATE`. A NULL `seq` or `recorded_at` is what
  marks a row from before 0002; only the owner can still update, and doing so
  rewrites evidence.
- Never write it with an upsert (`INSERT … ON CONFLICT DO UPDATE`, or
  SQLite's `INSERT OR REPLACE`, which deletes without firing a DELETE
  trigger).

**Runtime role posture.** `Open` (through `StatusCheck`) runs
`db.ProbeAppendOnly` under the `RuntimeMode` the database was opened with. The
zero mode checks nothing; development logs one `append_only=unenforced` line;
production refuses a role that is superuser or a member of the owner of the
table, its schema or the trigger function, that holds `UPDATE`, `DELETE`,
`TRUNCATE` or `TRIGGER` without the verified trigger pair
(`pgxdb.ErrAppendOnlyTableWritable`, admitted by
`pgxdb.Config.AppendOnlyWritableAcknowledged`), or that cannot append: no
`INSERT`, or no `USAGE` on `secrets_audit_seq`
(`pgxdb.ErrAppendOnlyTableUnwritable`, never admitted). Run migrations as one
role and the service as another; the grants, including `GRANT USAGE ON
SEQUENCE secrets_audit_seq TO <runtime role>` for a host that revokes the
PUBLIC grant, are in
[database-posture.md](../../../../workshop/documentation/docs/guides/database-posture.md).

A host that wants the tables in a dedicated schema builds it once and passes it
to both the migration runner and the store:

```go
s, err := pgxdb.NewSchema("iam")
if err := pgxdb.RunMigrations(ctx, db, secretspgx.MigrationsFS, secretspgx.MigrationsDir, pgxdb.WithSchema(s)); err != nil { ... }
repo, err := secretspgx.Open(ctx, db, secretspgx.WithSchema(s))
```

## Testing

`go test ./...` is hermetic: the export test, the bare-table guard and the
trigger-body pin run, and the live suites skip loudly without
`POSTGRES_TEST_DSN`. The live suites create and drop LOGIN fixture roles, so
the DSN's user needs `CREATEROLE`; `make test-stores` runs them.

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:18
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test -count=1 ./...
```

Set `POSTGRES_TEST_SCHEMA` and the suite reruns inside that schema, the proof
that every executed statement is qualified.
