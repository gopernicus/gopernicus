# pockets/secrets/stores/pgx

The secrets pocket's **PostgreSQL** store. Its own module so a host on another
datastore never pulls pgx into its graph. It owns the SQL and the canonical
migration; the host applies the migration pre-boot through its own runner and
the store never migrates.

## Surface

| member | shape |
|---|---|
| `NewRepository(db *pgxdb.DB, opts ...Option) *Repository` | the `vault.Repository`; no migration side effects |
| `WithSchema(s pgxdb.Schema) Option` | places every table in `s` |
| `StatusCheck(ctx, db, opts...) error` | boot gate: every secrets table exists under the configured schema |
| `ExportMigrations(dst string) error` | copies `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration |

## Schema

Three tables: `secrets_owner_keys` (one wrapped data key per owner),
`secrets_values` (sealed values, versioned, FK to the owner key without
cascade) and `secrets_audit` (append-only). Every write and `GetSecret` run in
one transaction with their audit rows. `ReplaceOwnerKey` is a compare-and-swap
on the stored key id; a mismatch is `sdk.ErrConflict`.

Ordering columns compare under `COLLATE "C"` so pages match the memory store's
byte order whatever the database's default collation.

The pocket never updates or deletes an audit row. Grant the application role
`INSERT` and `SELECT` on `secrets_audit` only.

A host that wants the tables in a dedicated schema builds it once and passes it
to both the migration runner and the store:

```go
s, err := pgxdb.NewSchema("iam")
if err := pgxdb.RunMigrations(ctx, db, secretspgx.MigrationsFS, secretspgx.MigrationsDir, pgxdb.WithSchema(s)); err != nil { ... }
if err := secretspgx.StatusCheck(ctx, db, secretspgx.WithSchema(s)); err != nil { ... }
repo := secretspgx.NewRepository(db, secretspgx.WithSchema(s))
```

## Testing

`go test ./...` is hermetic: the export test and the bare-table guard run,
and the live conformance suite skips loudly without `POSTGRES_TEST_DSN`.

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test -count=1 ./...
```

Set `POSTGRES_TEST_SCHEMA` and the suite reruns inside that schema, the proof
that every executed statement is qualified.
