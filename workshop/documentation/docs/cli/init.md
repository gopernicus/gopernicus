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
gopernicus init <project-name> [--module <path>] [--framework-version <version>] [--no-interactive] [--features <list>]
```

### Arguments

| Argument | Description |
|---|---|
| `<project-name>` | Required. Name of the directory to create. Accepts `org/repo` shorthand (e.g. `jrazmi/myapp`), which extracts the repo name and infers the module path as `github.com/jrazmi/myapp`. Only letters, numbers, hyphens, and underscores are allowed. |

### Flags

| Flag | Description |
|---|---|
| `--module`, `-m` | Go module path (e.g. `github.com/acme/myapp`). Inferred from org/repo shorthand if omitted. When neither shorthand nor flag is provided, defaults to `github.com/your-org/<project-name>`. |
| `--framework-version` | Pin a specific gopernicus framework version (e.g. `v0.1.0`). When omitted, fetches `@latest`. |
| `--no-interactive` | Accepted for compatibility. `init` is always non-interactive; omitted flags fall back to defaults (all features, default infrastructure). |
| `--features <list>` | Comma-separated feature list. Valid values: `authentication`, `authorization`, `tenancy`, `event-outbox`, `job-queue`, `all`, `none`. Default when omitted in non-interactive mode: `all`. |

## Feature & Infrastructure Selection

`gopernicus init` is fully flag-driven -- it does **not** launch a TUI wizard.
Even when `--no-interactive` is omitted, the command runs with defaults (printing
a note that it is using defaults; pass `--features`/`--module` to customize).
Omitted flags resolve to:

- **Features** -- all features enabled: `authentication`, `authorization`,
  `tenancy`, `event-outbox`, `job-queue`. Override with `--features`.
- **Infrastructure** -- the default adapter set (see
  [Non-Interactive Infrastructure Defaults](#non-interactive-infrastructure-defaults)
  below).
- **AI companion** -- Claude, which generates `CLAUDE.md` with project
  conventions and `.claude/skills/` with workflow skills for common tasks.

## What Gets Created

The scaffolding process executes these steps in order:

1. **Project directory** -- creates `<project-name>/` in the current working directory.
2. **Go module** -- runs `go mod init` and pins the minimum supported Go version.
3. **Directory layout**:
   - `workshop/migrations/primary/`
   - `workshop/dev/`
   - `workshop/testing/fixtures/`
   - `workshop/testing/e2e/`
   - `core/repositories/`
   - `core/cases/`
   - `core/auth/`
   - `bridge/repositories/`
   - `bridge/cases/`
   - `bridge/transit/`
   - `infrastructure/`
   - `sdk/`
4. **gopernicus.yml** -- manifest file with feature flags and domain-to-table mappings. Always includes `gopernicus_version`, defaulting to `latest`; when `--framework-version` is provided it is set to that pinned version instead.
5. **.gitignore** -- standard Go gitignore with env file exclusions.
6. **App server scaffold** -- `cmd/`, `app/`, and server wiring code generated from templates. Infrastructure adapters are included based on picker selections.
7. **Feature assets** -- when at least one feature is selected, the CLI copies migrations, core repositories, and bridge repositories from the gopernicus framework source. Source files are resolved from the Go module cache (or from `GOPERNICUS_DEV_SOURCE` in dev mode). Copied Go files have their import paths rewritten from the gopernicus module to your module path for `core/repositories/` and `bridge/repositories/` imports. Framework SDK and infrastructure imports are left pointing at gopernicus.
   - SQL migrations (`0001_auth.sql`, `0002_rebac.sql`, `0003_tenants.sql`, `0004_event_outbox.sql`, `0005_job_queue.sql`, `0006_job_schedules.sql`)
   - Core repositories (`core/repositories/auth/`, `core/repositories/rebac/`, `core/repositories/tenancy/`, etc.)
   - Bridge repositories (`bridge/repositories/authreposbridge/`, `bridge/repositories/rebacreposbridge/`, `bridge/repositories/tenancyreposbridge/`)
   - Authentication and authorization satisfiers (`core/auth/authentication/satisfiers/`, `core/auth/authorization/satisfiers/`) are **generated** by the post-init `gopernicus generate` step, not copied.
   - The authentication and invitations bridges (`bridge/auth/authentication/`, `bridge/auth/invitations/`) are **imported** from the framework rather than copied.
8. **AI companion files** -- when Claude is selected:
   - `CLAUDE.md` -- project instructions with architecture overview, conventions, key paths, and common commands
   - `.claude/skills/new-domain.md` -- interactive workflow: design tables, write migrations, scaffold repos, generate, and wire a new domain
   - `.claude/skills/new-case.md` -- interactive workflow: design a use case with interfaces, operations, errors, events, bridge routes, and server wiring
   - `.claude/skills/add-auth.md` -- interactive workflow: design ReBAC relations and permissions, configure bridge.yml auth, generate authorization schema
9. **Framework dependency** -- `go get github.com/gopernicus/gopernicus@latest` (or `@<version>` if `--framework-version` was provided), then `go mod tidy`.

### Domain Mappings

When features are selected, the manifest's database section is populated with
domain-to-table mappings:

| Domain | Tables |
|---|---|
| `auth` | api_keys, oauth_accounts, principals, security_events, service_accounts, sessions, user_passwords, users, verification_codes, verification_tokens |
| `rebac` | groups, invitations, rebac_relationships, rebac_relationship_metadata |
| `tenancy` | tenants |
| `events` | event_outbox |
| `jobs` | job_queue, job_schedules |

### Non-Interactive Infrastructure Defaults

When running with `--no-interactive`, the following infrastructure adapters are
enabled by default (matching the docker-compose development setup):

| Adapter | Default |
|---|---|
| Redis Cache | enabled |
| Redis Streams | enabled |
| Disk Storage | enabled |
| GCS | enabled |
| S3 | disabled |
| SendGrid | enabled |
| Telemetry | enabled |

## Examples

```bash
# Uses defaults for all features and infrastructure (recommended for first-time setup)
gopernicus init myapp

# Shorthand with org -- infers module path github.com/acme/myapp
gopernicus init acme/myapp

# Explicit module path
gopernicus init myapp --module github.com/acme/myapp

# Pin a specific framework version
gopernicus init myapp --framework-version v0.1.0

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
| `GOPERNICUS_DEV_SOURCE` | Path to local gopernicus framework source. When set, uses a `go mod edit -replace` directive instead of fetching from the registry, and resolves feature asset files from the local source instead of the Go module cache. |

## Known Limitations

- The validation error message for project names says "only letters, numbers, and hyphens allowed" but the command actually accepts underscores too. This is a minor bug in the error text.
- There is no `--dry-run` flag to preview what would be created without writing files.
- Features cannot be added to an existing project after init. To add a feature later, you would need to manually copy migrations, repositories, and bridges from the framework source.

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
