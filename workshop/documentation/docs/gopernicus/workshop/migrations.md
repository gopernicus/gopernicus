---
sidebar_position: 2
title: Migrations
---

# Workshop — Migrations

SQL migrations live in `workshop/migrations/`. They are the source of truth that the code generator reads from — running `gopernicus db reflect` inspects the live schema and writes a JSON snapshot used to produce repository code.

Migrations are **forward-only**. There are no down migrations.

## Databases & `gopernicus.yml`

Gopernicus supports multiple named databases, each defined under the `databases` key in `gopernicus.yml`. The `primary` database is where Gopernicus-managed features (authentication/authorization via `auth` and `rebac`, `tenancy`, `events`, and `jobs`) store their data. You can define additional named databases for your own domain needs.

```yaml
databases:
  primary:
    driver: postgres/pgx
    url_env_var: PRIMARY_POSTGRES_URL
    domains:
      auth: [principals, users, sessions, ...]
      rebac: [rebac_relationships, groups, invitations, ...]
      tenancy: [tenants]
      events: [event_outbox]
      jobs: [job_queue, job_schedules]
  analytics:
    driver: postgres/pgx
    url_env_var: ANALYTICS_POSTGRES_URL
    domains:
      reporting: [daily_summaries, funnels]
```

Each named database gets its own migrations directory and reflected schema under `workshop/migrations/<name>/`.

**Domains** are logical groupings of tables within a database. Gopernicus uses them to scope code generation — the reflected schema and generated repository code are organized by domain.

When you add a new entity, you need to:

1. Add the table in a migration file
2. Declare it under the appropriate domain (or a new one) in `gopernicus.yml`

Without the `gopernicus.yml` entry, the table is still captured in reflection (`_public.json`), but it won't be scaffolded by `boot repos` or included in code generation.

:::note Dialect support
Gopernicus supports two database drivers: `postgres` (the default; the legacy spelling `postgres/pgx` normalizes to it) and `sqlite`. For `sqlite`, the `url_env_var` holds the database file path rather than a connection URL. Bring-your-own migrator integrations are on the long-term roadmap but not yet planned.
:::

## Structure

```
workshop/migrations/
└── primary/
    ├── _public.sql         # Full schema snapshot (reflected)
    ├── _public.json        # Reflected schema used by the generator — do not edit
    ├── 0001_auth.sql
    ├── 0002_rebac.sql
    ├── 0003_tenants.sql
    └── 0004_events.sql
```

Migration files are numbered sequentially. The `_public.json` file is generated — edit the SQL files and re-run `gopernicus db reflect` to update it.

## Workflow

```bash
# 1. Write a new migration file
gopernicus db create my_entity

# 2. Apply it to your local database
gopernicus db migrate

# 3. Reflect the updated schema
gopernicus db reflect

# 4. Scaffold repository files for any new tables
gopernicus boot repos

# 5. Customize queries.sql and bridge.yml, then generate
gopernicus generate
```

After reflecting, make sure the new table is declared under a domain in `gopernicus.yml`. Without it, `boot repos` and `gopernicus generate` won't include the table.

`boot repos` creates `core/repositories/<domain>/<table>/queries.sql` for every domain-mapped table that lacks a repo (tables not mapped to any domain are ignored, framework spec-shipped feature entities are skipped, and existing files are never overwritten). Edit `queries.sql` to define operations and `bridge.yml` to configure HTTP routes before running `gopernicus generate`.

:::tip Full walkthrough
For a complete end-to-end example see [Adding a New Entity](../../guides/adding-new-entity.md).
:::

See [CLI: db](../../cli/db.md) for the full command reference.
