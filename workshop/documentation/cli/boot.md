# gopernicus boot

Bootstrap project components from the reflected schema and manifest.

## Overview

`gopernicus boot` provides batch scaffolding commands that read the domain-to-table
mappings in `gopernicus.yml` and scaffold files for every table in a domain (or
all domains). It is the batch equivalent of running `gopernicus new repo`
individually for each table.

## Subcommands

| Subcommand | Description |
|---|---|
| `boot repos` | Bootstrap repositories for a domain or all domains. |

---

## gopernicus boot repos

Scaffold repository directories and `queries.sql` files for every table mapped
to a domain in `gopernicus.yml`. Existing repos are skipped -- the command never
overwrites files.

### Usage

```
gopernicus boot repos [domain] [--db <name>]
```

### Arguments

| Argument | Description |
|---|---|
| `[domain]` | Optional. Restrict scaffolding to a single domain (e.g. `auth`). When omitted, all domains across all databases are processed. |

### Flags

| Flag | Description |
|---|---|
| `--db <name>` | Restrict to a specific database from the manifest. When omitted, all databases are processed. |

### How It Works

1. Loads `gopernicus.yml` from the project root.
2. Determines which databases to process: either the one specified by `--db` or
   all databases listed in the manifest.
3. For each database, reads the `domains` map. If a domain filter is provided,
   only that domain is processed; otherwise all domains are iterated in
   alphabetical order.
4. For each table in a domain:
   - **Skips** if `core/repositories/<domain>/<table>/queries.sql` already exists.
   - Looks up the table in the reflected schema JSON (`_public.json`).
   - **Skips** with a warning if the table is not found in the reflected schema.
   - Calls the same `scaffoldRepoForTable` function used by `gopernicus new repo`
     to create the repo directory and `queries.sql`.
5. Prints a summary of how many repos were scaffolded and next steps.

### Relationship to `gopernicus new repo`

`boot repos` and `new repo` share the same underlying scaffolding logic. The
difference is scope:

| Command | Scope |
|---|---|
| `gopernicus new repo auth/users` | Single table. |
| `gopernicus boot repos auth` | All tables in the `auth` domain. |
| `gopernicus boot repos` | All tables in all domains across all databases. |

### Reading the Manifest

The command reads domain-to-table mappings from `gopernicus.yml`. For example:

```yaml
databases:
  primary:
    url_env_var: DATABASE_URL
    schemas:
      - public
    domains:
      auth:
        - api_keys
        - oauth_accounts
        - principals
        - sessions
        - users
      rebac:
        - groups
        - invitations
        - rebac_relationships
      tenancy:
        - tenants
```

Running `gopernicus boot repos auth` scaffolds repos for `api_keys`,
`oauth_accounts`, `principals`, `sessions`, and `users` under
`core/repositories/auth/`.

Running `gopernicus boot repos` scaffolds repos for all three domains.

### Error Handling

- If a domain filter is provided but not found in the specified database, the
  command fails with an error listing the available domains.
- If a table is not found in the reflected schema, that table is skipped with a
  warning (not a fatal error). Run `gopernicus db reflect` to update the schema.
- If all repos already exist, the command reports "No new repos to scaffold."

### Examples

```bash
# Bootstrap all repos for all domains (most common after init)
gopernicus boot repos

# Bootstrap only the auth domain
gopernicus boot repos auth

# Bootstrap repos from a specific database
gopernicus boot repos --db analytics

# Bootstrap a specific domain in a specific database
gopernicus boot repos auth --db primary
```

### After Bootstrapping

The command prints next steps:

```
  1. Edit queries.sql files to customize operations
  2. Run 'gopernicus generate' to generate code from queries
```

## Typical Workflow

```bash
# 1. Set up the project (creates gopernicus.yml with domain mappings)
gopernicus init myapp
cd myapp

# 2. Run initial migrations
gopernicus db migrate

# 3. Reflect the database schema
gopernicus db reflect

# 4. Scaffold all repos in one command
gopernicus boot repos

# 5. Customize queries.sql files as needed
vim core/repositories/auth/users/queries.sql

# 6. Generate Go code
gopernicus generate
```

## Related

- [new](new.md) -- `new repo` scaffolds a single repository
- [generate](generate.md) -- generate Go code after scaffolding
- [db](db.md) -- `db reflect` produces the schema JSON required by boot
- [init](init.md) -- creates `gopernicus.yml` with domain mappings
- [YAML Configuration](../generators/yaml-configuration.md)
