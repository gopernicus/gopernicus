# Generated File Map

This document maps every file produced by `gopernicus generate`, organized by
architectural layer. Each file is classified as either **always-regenerated**
(overwritten on every run) or **bootstrap** (created once, never touched again).

The convention: files named `generated.go` or `generated_*.go` are
always-regenerated. All other files are bootstrap files that you can safely
customize.

## Repository Layer

Location: `core/repositories/<domain>/<entity>/`

| File | Type | Contents |
|------|------|----------|
| `generated.go` | Always-regenerated | Entity struct (`Entity`), input structs (`CreateEntity`, `UpdateEntity`), filter structs (`FilterList`, `FilterListBy*`), error variables, order-by fields (`OrderByFields`), max-limit constants, Repository methods (List, Get, Create, etc. on `*Repository`), cursor encode/decode helpers |
| `repository.go` | Bootstrap | `Storer` interface with `// gopernicus:start` / `// gopernicus:end` markers (generated methods between markers, custom methods above), `Repository` struct with fields (store, generateID, bus), `NewRepository` constructor, `Option` pattern with `WithGenerateID` and `WithEventBus` |
| `fop.go` | Bootstrap | `OrderByFields` defaults, `DefaultOrderBy`, `DefaultOrderDirection`, `DefaultLimit`. Customize ordering and pagination defaults here |
| `generated_cache.go` | Always-regenerated | `CacheConfig` struct, `DefaultCacheConfig`, `CacheStore` struct (wraps `Storer`), `NewCacheStore` constructor. Only generated when `@cache` annotations exist |
| `cache.go` | Bootstrap | Custom cache method stubs. Only generated alongside `generated_cache.go` |

### Example: `core/repositories/rebac/invitations/`

```
invitations/
  queries.sql              -- source of truth (hand-written)
  generated.go             -- always-regenerated
  generated_cache.go       -- always-regenerated
  repository.go            -- bootstrap
  fop.go                   -- bootstrap
  cache.go                 -- bootstrap
```

Note: there is no `model.go`. Entity types are defined directly in `generated.go` with final names (`Invitation`, `CreateInvitation`, `UpdateInvitation`).

## Store Layer (pgxstore)

Location: `core/repositories/<domain>/<entity>/<entity>pgx/`

| File | Type | Contents |
|------|------|----------|
| `generated.go` | Always-regenerated | `Store` struct with `pgxdb.Querier`, `NewStore` constructor, compile-time interface check, `mapError` for PG error translation, all query methods with dynamic SQL building using `pgx.NamedArgs` |
| `store.go` | Bootstrap | Custom store method stubs with example code |
| `generated_test.go` | Always-regenerated | Integration tests (build-tagged `integration`): `setupTestStore` helper, `TestEntityStore_Create`, `TestEntityStore_Get`, `TestEntityStore_Update`, `TestEntityStore_Delete`, `TestEntityStore_List` |
| `store_test.go` | Bootstrap | `migrateTestDB` function used by `setupTestStore` to apply migrations before tests |

### Example: `core/repositories/rebac/invitations/invitationspgx/`

```
invitationspgx/
  generated.go             -- always-regenerated
  generated_test.go        -- always-regenerated
  store.go                 -- bootstrap
  store_test.go            -- bootstrap
```

## Domain Composite Layer

Location: `core/repositories/<domain>/`

| File | Type | Contents |
|------|------|----------|
| `generated_composite.go` | Always-regenerated | `Repositories` struct with all entity repositories as fields, `NewRepositories` constructor that wires `pgxstore -> CacheStore -> Repository` for each entity |

### Example: `core/repositories/rebac/`

```
rebac/
  generated_composite.go       -- always-regenerated
  groups/                      -- entity package
  invitations/                 -- entity package
  rebacrelationships/          -- entity package
  rebacrelationshipmetadata/   -- entity package
```

Note: there is no `composite.go` bootstrap with type aliases. The `Repositories` struct is defined directly in `generated_composite.go`. Auth schema files (`authschema.go`, `generated_authschema.go`) are in the bridge composite directory, not the repo composite.

## Bridge Layer

Location: `bridge/repositories/<domain>reposbridge/<entity>bridge/`

| File | Type | Contents |
|------|------|----------|
| `generated.go` | Always-regenerated | HTTP handler methods on `*Bridge` (`httpList`, `httpGet`, `httpCreate`, etc.), `QueryParams*` structs, `parseQueryParams*` functions, `parseFilter*` functions, `parseOrderBy`, `addGeneratedRoutes()` (private), `OpenAPISpec` |
| `bridge.yml` | Bootstrap | Route definitions, ordered middleware arrays (authenticate, authorize, rate_limit, max_body_size, unique_to_id), auth schema (`auth_relations`, `auth_permissions`) |
| `bridge.go` | Bootstrap | `Bridge` struct with ALL fields directly (repository, log, rateLimiter, authenticator, authorizer, jsonErrors), `NewBridge` constructor |
| `routes.go` | Bootstrap | `AddHttpRoutes()` (public) that calls `addGeneratedRoutes()` + custom routes |
| `http.go` | Bootstrap | Custom HTTP handler methods |
| `fop.go` | Bootstrap | Custom query-param parsing stubs. Override `parseFilterList` or `parseOrderBy` here |

### Example: `bridge/repositories/rebacreposbridge/invitationsbridge/`

```
invitationsbridge/
  generated.go             -- always-regenerated
  bridge.yml               -- bootstrap
  bridge.go                -- bootstrap
  routes.go                -- bootstrap
  http.go                  -- bootstrap
  fop.go                   -- bootstrap
```

## Bridge Composite Layer

Location: `bridge/repositories/<domain>reposbridge/`

| File | Type | Contents |
|------|------|----------|
| `generated_composite.go` | Always-regenerated | `Bridges` struct with all entity bridges as fields, `NewBridges` constructor, `AddHttpRoutes` (aggregates all entity routes), `OpenAPISpec` (aggregates all entity specs) |
| `generated_authschema.go` | Always-regenerated | Auth schema (`AuthSchema()`) returning `[]authorization.ResourceSchema` built from `auth_relations` and `auth_permissions` in bridge.yml files. Only generated when authorization is enabled |
| `authschema.go` | Bootstrap | `AuthSchema()` that delegates to generated schema. Override to add custom relations or permissions |

### Example: `bridge/repositories/rebacreposbridge/`

```
rebacreposbridge/
  generated_composite.go   -- always-regenerated
  generated_authschema.go  -- always-regenerated
  authschema.go            -- bootstrap
  groupsbridge/            -- entity bridge package
  invitationsbridge/       -- entity bridge package
```

## Test Fixtures

Location: `core/testing/fixtures/`

A single cross-domain package containing fixture helpers for all entities.

| File | Type | Contents |
|------|------|----------|
| `generated.go` | Always-regenerated | `CreateTest<Entity>` functions for every entity across all domains. Each function generates a unique ID, inserts a record via raw SQL (bypassing the repository), and returns the created entity. Respects FK ordering so parent fixtures are valid dependencies |
| `fixtures.go` | Bootstrap | Custom fixture helpers (e.g., bulk creation, scenario-specific data) |

## E2E Tests

Location: `testing/e2e/`

Generated for every entity that has routes defined in `bridge.yml`.

| File | Type | Contents |
|------|------|----------|
| `setup_test.go` | Bootstrap | `setupTestServer` helper that boots the full application stack with a test database and HTTP server. Created once on first generation |
| `<entity>_generated_test.go` | Always-regenerated | `TestGenerated<Entity>_Get`, `TestGenerated<Entity>_List`, and other HTTP endpoint tests using `testhttp.Client`. Build-tagged `e2e` |
| `<entity>_test.go` | Bootstrap | Custom E2E test stubs for entity-specific scenarios |

### Example: `testing/e2e/`

```
e2e/
  setup_test.go                          -- bootstrap (once)
  invitation_generated_test.go           -- always-regenerated
  invitation_test.go                     -- bootstrap
  group_generated_test.go                -- always-regenerated
  group_test.go                          -- bootstrap
  user_generated_test.go                 -- always-regenerated
  user_test.go                           -- bootstrap
  ...
```

## Summary Table

| Location pattern | Always-regenerated files | Bootstrap files |
|-----------------|------------------------|-----------------|
| `core/repositories/<domain>/<entity>/` | `generated.go`, `generated_cache.go` | `repository.go`, `fop.go`, `cache.go` |
| `core/repositories/<domain>/<entity>/<entity>pgx/` | `generated.go`, `generated_test.go` | `store.go`, `store_test.go` |
| `core/repositories/<domain>/` | `generated_composite.go` | -- |
| `bridge/repositories/<domain>reposbridge/<entity>bridge/` | `generated.go` | `bridge.yml`, `bridge.go`, `routes.go`, `http.go`, `fop.go` |
| `bridge/repositories/<domain>reposbridge/` | `generated_composite.go`, `generated_authschema.go` | `authschema.go` |
| `core/testing/fixtures/` | `generated.go` | `fixtures.go` |
| `testing/e2e/` | `<entity>_generated_test.go` | `setup_test.go`, `<entity>_test.go` |

## Key Rule

Never edit files marked "always-regenerated" -- your changes will be lost on
the next `gopernicus generate` run. Instead, use the bootstrap files to customize
behavior. The generated code is designed so that bootstrap files can override
any default by defining methods with the same signature or by configuring
routes and middleware in `bridge.yml`.

---

**Related:**
- [Code Generation Overview](overview.md)
- [Query Annotations Reference](query-annotations.md)
- [YAML Configuration Reference](yaml-configuration.md)
