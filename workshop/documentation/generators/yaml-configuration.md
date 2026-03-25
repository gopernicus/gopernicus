# gopernicus.yml Configuration Reference

The `gopernicus.yml` file is the project manifest. It lives at the project root
and is read by every `gopernicus` CLI command. It configures databases, feature
toggles, event infrastructure, and domain-to-table mappings.

## Full Schema

```yaml
# Required. Manifest format version.
version: "1"

# Optional. The gopernicus framework version to use.
gopernicus_version: latest

# Optional. Path to the .env file for environment variable loading.
env_file: .env

# Required. Named database connections.
databases:
  <name>:
    # Required. Database driver identifier.
    driver: postgres/pgx

    # Required. Environment variable holding the connection URL.
    url_env_var: DATABASE_URL

    # Optional. Database schemas to reflect. Defaults to ["public"].
    schemas:
      - public

    # Optional. Maps domain names to table lists.
    # Used by `gopernicus boot repos <domain>` to scaffold all repos at once.
    domains:
      <domain_name>:
        - <table_name>
        - <table_name>

# Optional. Feature toggles for framework capabilities.
features:
  # Authentication provider. "gopernicus" for built-in, or a custom provider string.
  authentication: gopernicus

  # Authorization provider. "gopernicus" for built-in ReBAC, or a custom provider.
  authorization: gopernicus

  # Multi-tenancy support.
  tenancy: gopernicus

# Optional. Event infrastructure configuration.
events:
  # Outbox pattern for at-least-once event delivery.
  outbox: gopernicus
```

## Section Details

### version

Always `"1"`. Required for forward compatibility.

### gopernicus_version

Controls which version of the gopernicus framework module is referenced.
Use `"latest"` during development.

### env_file

Relative path to the `.env` file. The CLI loads this file before resolving
database URLs. Defaults to `".env"` when omitted.

### databases

A map of named database connections. Most projects need only one, named
`"primary"`. The generator defaults to `"primary"` when a `queries.sql` file
omits the `@database` annotation.

#### databases.\<name\>.driver

The database driver. Currently only `postgres/pgx` is supported.

#### databases.\<name\>.url_env_var

The environment variable name that holds the connection URL (e.g.,
`"DATABASE_URL"` or `"MYAPP_DB_DATABASE_URL"`). This is the raw env var name --
not the URL itself.

When scaffolding a new project with `gopernicus init`, the env var is
automatically namespaced: `gopernicus init myapp` sets
`MYAPP_DB_DATABASE_URL`.

#### databases.\<name\>.schemas

List of PostgreSQL schemas to reflect. Defaults to `["public"]` when omitted.
The reflected schema JSON files are stored at
`workshop/migrations/<dbname>/_<schema>.json`.

#### databases.\<name\>.domains

Maps logical domain names to lists of table names. This serves two purposes:

1. `gopernicus boot repos <domain>` uses it to scaffold all repos for a domain
   in one command.
2. `DomainForTable` lookups use it to determine which domain a table belongs to.

Tables not listed under any domain can still have repositories -- the domain is
inferred from the directory structure under `core/repositories/`.

### features

Feature toggles control which framework capabilities are enabled. Each accepts
one of three values:

| Value | Meaning |
|-------|---------|
| `true` | Enabled with the `"gopernicus"` built-in provider |
| `false` or omitted | Disabled |
| `"<provider>"` | Enabled with a named provider (e.g., `"auth0"`) |

#### features.authentication

When enabled, the generator produces `authenticate` middleware wiring in
bridge routes (as configured in `bridge.yml`) and passes `authEnabled` to
bridge generation.

#### features.authorization

When enabled (set to `"gopernicus"`), the generator:
- Produces `generated_authschema.go` in each bridge composite directory from
  `auth_relations` and `auth_permissions` defined in `bridge.yml` files.
- Generates `authorize` middleware in bridge routes as configured in `bridge.yml`.
- Generates `auth_create` tuple creation in bridge Create handlers.
- Generates `with_permissions` response enrichment.

#### features.tenancy

When enabled, tenant-scoped scaffolding and middleware are activated.

### events

#### events.outbox

When enabled, the event outbox pattern is configured: domain events are
persisted to the `event_outbox` table within the same database transaction as
the operation, then delivered asynchronously. This guarantees at-least-once
delivery across process restarts.

## Complete Annotated Example

This is the configuration for the `pointtaken` project:

```yaml
version: "1"
gopernicus_version: latest
env_file: .env

databases:
  primary:
    driver: postgres/pgx
    url_env_var: POINTTAKEN_DB_DATABASE_URL
    domains:
      auth:
        - api_keys
        - oauth_accounts
        - principals
        - security_events
        - service_accounts
        - sessions
        - user_passwords
        - users
        - verification_codes
        - verification_tokens
      events:
        - event_outbox
      rebac:
        - rebac_relationships
        - rebac_relationship_metadata
        - invitations
        - groups
      questions:
        - questions
        - answers
      tenancy:
        - tenants

features:
  authentication: gopernicus
  authorization: gopernicus
  tenancy: gopernicus

events:
  outbox: gopernicus
```

## Defaults

When `gopernicus init <project>` creates a new manifest:

- `version` is set to `"1"`.
- `gopernicus_version` is set to `"latest"`.
- `env_file` is set to `".env"`.
- A single `primary` database is configured with `postgres/pgx` driver.
- `url_env_var` is namespaced: `<PROJECT>_DB_DATABASE_URL` (uppercased, hyphens
  replaced with underscores).
- All three features (`authentication`, `authorization`, `tenancy`) default to
  `"gopernicus"`.

## How the Generator Uses the Manifest

1. **Schema loading** -- Iterates `DatabaseNames()`, then each database's
   `SchemasOrDefault()`, loading `workshop/migrations/<db>/_<schema>.json`.
2. **Table lookup** -- For each `queries.sql`, the `@database` annotation (or
   default `"primary"`) selects which schema set to search. The directory name
   is matched against table names via `ToPackageName`.
3. **Feature gates** -- `AuthenticationEnabled()` controls whether bridge routes
   include authentication middleware. `AuthorizationProvider()` controls whether
   auth schema and authorization middleware are generated.
4. **Event wiring** -- `OutboxEnabled()` controls whether the composite wires
   the outbox infrastructure into repositories.

---

**Related:**
- [Code Generation Overview](overview.md)
- [Query Annotations Reference](query-annotations.md)
- [Generated File Map](generated-file-map.md)
