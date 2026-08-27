---
title: Persistence & migrations
description: Choose a connector, wire pocket stores, own migrations, and prove datastore parity.
---

# Persistence & migrations

Gopernicus separates three responsibilities:

1. a datastore **connector** owns database mechanics;
2. a pocket **store module** owns pocket SQL and repository implementations;
3. the **host** owns the final migration ledger and when it is applied.

## Choose a connector

```go
db, err := pgxdb.Open(pgxdb.Config{
    DSN: os.Getenv("DATABASE_URL"),
})
if err != nil {
    return err
}
defer db.Close()
```

Or use the symmetric Turso connector. Connector config may carry environment tags, but `Open` reads no variables implicitly.

## Choose or implement stores

```go
authRepos, err := authpgx.Repositories(db)
if err != nil {
    return err
}

cmsRepos := cmspgx.Repositories(db)
```

Some constructors perform boot schema probes and therefore return errors; others currently return bundles directly. Always use the module's actual signature.

A host may implement the public repositories instead. That is the escape hatch for another database, an existing schema, or host-specific transaction strategy.

## Export canonical migrations once

Pocket store modules expose:

- `MigrationsFS`;
- `MigrationsDir`;
- `ExportMigrations(dst string)`.

Scaffold chosen pocket sources into one host-owned ordered directory:

```go
if err := authpgx.ExportMigrations("workshop/migrations/primary"); err != nil {
    return err
}
if err := cmspgx.ExportMigrations("workshop/migrations/primary"); err != nil {
    return err
}
```

Resolve filename conflicts in the host ledger. After export, the files are the host's: review and commit them. Do not re-export over evolved production migrations or blindly copy a newer greenfield `0001` into an older deployed schema.

## Apply before boot

The host's migration binary embeds its merged ledger and uses the selected connector:

```go
//go:embed primary/[0-9]*.sql
var migrations embed.FS

if err := pgxdb.RunMigrations(
    ctx,
    db,
    migrations,
    "primary",
); err != nil {
    return err
}
```

Run it as a deployment/pre-boot step:

```bash
go run ./workshop/migrations
# or
gopernicus db migrate
```

The application server does not migrate automatically. This prevents replicas from racing schema changes and keeps operational authority with deployment.

## Ledger rules

- migration identity is the full filename;
- apply in filename order;
- never renumber an applied file;
- merge all sources targeting one database into one globally ordered stream;
- use checksums and forward-only history;
- keep dialect siblings on identical version/filename sets, with dialect-specific SQL inside;
- write explicit host upgrade migrations for already-deployed schemas.

`gopernicus db create <slug>` allocates the next four-digit filename in a host ledger.

## Transactions stay behind ports

Use SDK `crud.Transactor` or pocket-declared atomic repository methods when a use case spans several writes. Do not pull a connector's raw underlying handle into logic as a service-locator shortcut.

An atomic domain operation should be one port method when all implementations must guarantee the same invariant—for example authentication's user + primary identifier creation or authorization's guarded mutation apply.

## Prove parity

Hermetic `make check` lets live datastore suites skip loudly. Milestone/release proof runs them against real PostgreSQL and Turso with race detection where applicable.

```bash
POSTGRES_TEST_DSN='postgres://…?sslmode=disable' make test-stores
```

Turso suites use the `integration` build tag and `TURSO_DATABASE_URL` / `TURSO_AUTH_TOKEN`. The common `storetest` suite is the behavioral oracle; driver tests alone are not enough.

## Development logging warning

Both connectors support query logging for local diagnosis. Arguments may contain secrets or PII. Leave it disabled in production and never use a raw DSN in logs—use connector redaction helpers.
