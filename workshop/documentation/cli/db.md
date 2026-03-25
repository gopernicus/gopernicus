# gopernicus db

Database utilities for managing migrations and schema reflection.

## Overview

`gopernicus db` provides subcommands for running migrations, reflecting live
database schema into JSON, checking migration status, and creating new migration
files. All subcommands connect to PostgreSQL using the URL configured in
`gopernicus.yml`.

## Subcommands

| Subcommand | Description |
|---|---|
| `db migrate` | Run pending migrations. |
| `db reflect` | Reflect live database schema into `_schema.json` and `_schema.sql`. |
| `db status` | Show migration status (applied, pending, tampered). |
| `db create` | Create a new timestamped migration file. |

---

## gopernicus db reflect

Connect to the live database, query schema metadata, and write machine-readable
and human-readable schema files into the migrations directory.

### Usage

```
gopernicus db reflect [--db-url <url>] [--db <name>]
```

### What It Produces

For each schema (default: `public`), two files are written to
`workshop/migrations/{db}/`:

| File | Description |
|---|---|
| `_public.json` (or `_{schema}.json`) | Machine-readable schema consumed by `gopernicus generate` and `gopernicus new repo`. Contains tables, columns (with Go types), primary keys, foreign keys, indexes, and enum types. |
| `_public.sql` (or `_{schema}.sql`) | Human-readable SQL summary of the reflected schema. Useful for review and documentation. |

The schema names to reflect are read from `gopernicus.yml` under
`databases.{name}.schemas`. If not configured, defaults to `["public"]`.

### Examples

```bash
# Reflect the primary database (default)
gopernicus db reflect

# Reflect a specific database
gopernicus db reflect --db analytics

# Override the database URL directly
gopernicus db reflect --db-url "postgres://user:pass@localhost:5432/mydb"
```

---

## gopernicus db migrate

Run all pending SQL migration files against the database.

### Usage

```
gopernicus db migrate [--db-url <url>] [--db <name>]
```

Migration files are read from `workshop/migrations/{db}/` (e.g.
`workshop/migrations/primary/`). Files prefixed with `_` are ignored (they are
schema reflection output, not migrations). Migrations are applied in filename
order.

### Examples

```bash
# Run pending migrations on the primary database
gopernicus db migrate

# Run migrations on a specific database
gopernicus db migrate --db analytics
```

---

## gopernicus db status

Show the status of all migration files: applied, pending, or tampered (checksum
mismatch between the file on disk and what was recorded when applied).

### Usage

```
gopernicus db status [--db-url <url>] [--db <name>]
```

If the database is unreachable, status falls back to listing migration files
from disk without connection data.

### Output Symbols

| Symbol | Meaning |
|---|---|
| `+` | Applied successfully. Shows the recorded checksum. |
| `.` | Pending -- not yet applied. |
| `!` | Tampered -- the file has changed since it was applied (checksum mismatch). |

### Examples

```bash
# Check migration status
gopernicus db status

# Check a specific database
gopernicus db status --db analytics
```

---

## gopernicus db create

Create a new empty migration file with a timestamp prefix.

### Usage

```
gopernicus db create <name> [--db <name>]
```

The migration name is sanitized to lowercase alphanumeric characters and
underscores. Spaces and hyphens are converted to underscores. The resulting
file is named `{timestamp}_{name}.sql` and placed in the migrations directory.

### Examples

```bash
# Create a new migration
gopernicus db create add_widgets_table
# Creates: workshop/migrations/primary/20260318143022_add_widgets_table.sql

# Create for a specific database
gopernicus db create add_metrics_index --db analytics
```

---

## Database Configuration in gopernicus.yml

The `db` subcommands resolve the database connection URL using this priority:

1. **`--db-url` flag** -- explicit override, highest priority.
2. **Manifest config** -- `databases.{name}.url_env_var` specifies an environment variable name. The CLI loads `.env` from the project root (or the file specified by `env_file` in the manifest) and reads the variable.
3. **Legacy fallback** -- if no databases section exists in the manifest, the CLI looks for a bare `DATABASE_URL` environment variable.

### Example manifest

```yaml
env_file: .env

databases:
  primary:
    url_env_var: DATABASE_URL
    schemas:
      - public
    domains:
      auth:
        - users
        - sessions
        - api_keys
      tenancy:
        - tenants

  analytics:
    url_env_var: ANALYTICS_DATABASE_URL
    schemas:
      - public
```

The `--db` flag selects which database to operate on. It defaults to `primary`.

## Typical Workflow

```bash
# 1. Create a migration
gopernicus db create add_products_table

# 2. Write the SQL
vim workshop/migrations/primary/20260318143022_add_products_table.sql

# 3. Apply it
gopernicus db migrate

# 4. Reflect the updated schema
gopernicus db reflect

# 5. Scaffold repos and generate code
gopernicus new repo catalog/products
gopernicus generate
```

## Related

- [generate](generate.md) -- consumes `_schema.json` to generate Go code
- [new](new.md) -- `new repo` looks up tables in the reflected schema
- [boot](boot.md) -- `boot repos` also requires reflected schema
- [init](init.md) -- initial project setup creates the migrations directory
- [Database Infrastructure](../infrastructure/database.md)
