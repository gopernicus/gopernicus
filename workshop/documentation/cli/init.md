# gopernicus init

Bootstrap a new gopernicus project in a new directory.

## Overview

`gopernicus init` creates a fully structured project directory with a Go module,
a `gopernicus.yml` manifest, migration directories, core/bridge directory layout,
and (optionally) pre-configured framework features like authentication,
authorization, tenancy, and events.

The command refuses to overwrite an existing non-empty directory.

## Usage

```
gopernicus init <project-name> [--module <path>] [--no-interactive] [--features <list>]
```

### Arguments

| Argument | Description |
|---|---|
| `<project-name>` | Required. Name of the directory to create. Accepts `org/repo` shorthand (e.g. `jrazmi/myapp`), which extracts the repo name and infers the module path as `github.com/jrazmi/myapp`. |

### Flags

| Flag | Description |
|---|---|
| `--module`, `-m` | Go module path (e.g. `github.com/acme/myapp`). Inferred from org/repo shorthand if omitted. |
| `--no-interactive` | Skip interactive prompts. Uses all-features-enabled and default infrastructure when combined with no `--features` flag. |
| `--features <list>` | Comma-separated feature list. Valid values: `authentication`, `authorization`, `tenancy`, `events-outbox`, `all`, `none`. |

## Interactive Prompts

When run without `--no-interactive` (and stdin is a terminal), init launches a
multi-step TUI wizard:

1. **Project name** -- defaults to the positional argument.
2. **Go module path** -- defaults to `github.com/<org>/<project>` or `github.com/your-org/<project>`.
3. **Framework Features** -- multi-select picker with three items, all selected by default:
   - Authentication (users, sessions, OAuth, API keys)
   - Authorization (ReBAC relationships, permissions)
   - Tenancy (multi-tenant isolation, groups)
4. **Event Infrastructure** -- separate picker screen:
   - Events Outbox (durable event delivery via event_outbox table)
5. **Infrastructure Adapters** -- four separate picker screens for:
   - Cache Backend (Redis Cache)
   - Event Bus Backend (Redis Streams)
   - File Storage (Disk, GCS, S3)
   - Email Delivery (SendGrid)

## What Gets Created

The scaffolding process executes these steps in order:

1. **Project directory** -- creates `<project-name>/` in the current working directory.
2. **Go module** -- runs `go mod init` and pins the minimum supported Go version.
3. **Directory layout**:
   - `workshop/migrations/primary/`
   - `core/repositories/`
   - `core/cases/`
   - `bridge/repositories/`
   - `bridge/cases/`
   - `workshop/dev/`
4. **gopernicus.yml** -- manifest file with feature flags and domain-to-table mappings.
5. **.gitignore** -- standard Go gitignore with env file exclusions.
6. **App server scaffold** -- `cmd/`, `app/`, and server wiring code generated from templates. Infrastructure adapters are included based on picker selections.
7. **Feature assets** (when `GOPERNICUS_DEV_SOURCE` is set):
   - SQL migrations (`0001_auth.sql`, `0002_rebac.sql`, `0003_tenants.sql`, `0004_events.sql`)
   - Core repositories (`core/repositories/auth/`, `core/repositories/rebac/`, etc.)
   - Bridge repositories (`bridge/repositories/authreposbridge/`, etc.)
   - Satisfiers (`core/auth/authentication/satisfiers/`, `core/auth/authorization/satisfiers/`, `core/events/satisfiers/`)
   - Import paths are rewritten from `github.com/gopernicus/gopernicus` to your module path.
8. **Framework dependency** -- `go get github.com/gopernicus/gopernicus@latest`, then `go mod tidy`.

### Domain Mappings

When features are selected, the manifest's database section is populated with
domain-to-table mappings:

| Domain | Tables |
|---|---|
| `auth` | api_keys, oauth_accounts, principals, security_events, service_accounts, sessions, user_passwords, users, verification_codes, verification_tokens |
| `rebac` | groups, invitations, rebac_relationships, rebac_relationship_metadata |
| `tenancy` | tenants |
| `events` | event_outbox |

## Examples

```bash
# Interactive wizard (recommended for first-time setup)
gopernicus init myapp

# Shorthand with org -- infers module path github.com/acme/myapp
gopernicus init acme/myapp

# Explicit module path
gopernicus init myapp --module github.com/acme/myapp

# Non-interactive with all features (CI-friendly)
gopernicus init myapp --no-interactive

# Non-interactive with specific features
gopernicus init myapp --no-interactive --features=authentication,authorization

# Non-interactive with no framework features (bare project)
gopernicus init myapp --no-interactive --features=none
```

## Environment Variables

| Variable | Description |
|---|---|
| `GOPERNICUS_DEV_SOURCE` | Path to local gopernicus framework source. When set, uses a `go mod edit -replace` directive instead of fetching from the registry, and copies feature asset files from the local source. |

## After Init

```bash
cd myapp
gopernicus doctor        # verify project health
gopernicus db migrate    # run initial migrations
gopernicus db reflect    # reflect database schema
gopernicus generate      # generate Go code from queries
```

## Related

- [generate](generate.md) -- generate Go code from queries.sql files
- [db](db.md) -- database migration and schema reflection
- [doctor](doctor.md) -- verify project health
- [boot](boot.md) -- batch-scaffold repos for a domain
- [YAML Configuration](../generators/yaml-configuration.md)
