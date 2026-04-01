---
sidebar_position: 4
title: Repositories
---

# Bridge — Repositories

Generated CRUD bridges live in `bridge/repositories/`. Each database entity gets its own bridge package that translates HTTP requests into [Core Repository](../core/repositories.md) calls.

## Domain Organization

Bridges mirror the Core repository domain structure. Each domain has a **composite** package containing individual entity bridge packages:

```
bridge/repositories/
├── authreposbridge/             # Auth domain composite
│   ├── usersbridge/
│   ├── apikeysbridge/
│   ├── sessionsbridge/
│   └── ...
├── rebacreposbridge/            # ReBAC domain composite
│   ├── groupsbridge/
│   ├── invitationsbridge/
│   └── ...
├── eventsreposbridge/           # Events domain composite
│   └── eventoutboxbridge/
└── tenancyreposbridge/          # Tenancy domain composite
    └── tenantsbridge/
```

## Entity Bridge Package

Each entity bridge package contains a mix of always-regenerated and one-time-scaffolded files:

### Generated Files (Regenerated Every Time)

| File | Purpose |
|---|---|
| `generated.go` | HTTP handlers, request/response models with `Validate()` and `ToRepo()`, query parsing, `addGeneratedRoutes()`, `addGeneratedOpenAPISpec()` |

### Scaffolded Files (Created Once, Never Overwritten)

| File | Purpose |
|---|---|
| `bridge.go` | `Bridge` struct with all fields, `NewBridge()` constructor, options |
| `bridge.yml` | Route definitions, middleware ordering, auth schema |
| `routes.go` | `AddHttpRoutes()` — calls `addGeneratedRoutes()` plus custom routes; `OpenAPISpec()` |
| `http.go` | Custom HTTP handlers (empty by default) |
| `fop.go` | Custom filter/order query parameter parsing (empty by default) |

### Domain-Level Generated Files

| File | Purpose |
|---|---|
| `generated_composite.go` | `Bridges` struct grouping all entity bridges, `NewBridges()`, `AddHttpRoutes()`, `OpenAPISpec()`, `AuthSchema()` |
| `generated_authschema.go` | `GeneratedAuthSchema()` — authorization resource schemas derived from `bridge.yml` |
| `authschema.go` | `AuthSchema()` — scaffolded once, calls `GeneratedAuthSchema()` by default, customizable |

## Bridge Struct

The bridge struct holds the repository, shared infrastructure, and auth dependencies. All fields are listed directly — no embedding:

```go
type Bridge struct {
    userRepository *users.Repository
    log            *slog.Logger
    rateLimiter    *ratelimiter.RateLimiter
    authenticator  *authentication.Authenticator
    authorizer     *authorization.Authorizer
    jsonErrors     httpmid.ErrorRenderer
    htmlErrors     httpmid.ErrorRenderer
}
```

Options allow overriding error renderers:
- `WithJSONErrorRenderer(r)` — override JSON error responses (default: `httpmid.JSONErrors{}`)
- `WithHTMLErrorRenderer(r)` — set HTML error renderer for server-rendered routes

## bridge.yml

Each entity bridge has a `bridge.yml` that defines routes, middleware, and authorization schema. This file drives code generation — the generator reads it to produce `generated.go` and `generated_authschema.go`.

```yaml
entity: User
repo: auth/users
domain: auth

auth_relations:
  - "owner(user, service_account)"

auth_permissions:
  - "list(owner)"
  - "create(authenticated)"
  - "read(owner)"
  - "update(owner)"
  - "delete(owner)"
  - "manage(owner)"

routes:
  - func: List
    path: /users
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read

  - func: Get
    path: /users/{user_id}
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: read
          param: user_id

  - func: Update
    path: /users/{user_id}
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
      - authorize:
          permission: update
          param: user_id
```

### Route Fields

| Field | Purpose |
|---|---|
| `func` | Handler function name — maps to a generated handler (`List`, `Get`, `Create`, `Update`, `Delete`, `SoftDelete`, `Archive`, `Restore`) |
| `path` | URL path with `{param}` placeholders |
| `method` | HTTP method override (default inferred from func: List/Get → GET, Create → POST, Update → PUT, Delete → DELETE) |
| `middleware` | Ordered array of middleware applied to this route |

### Middleware Options

| Middleware | Syntax | Purpose |
|---|---|---|
| `authenticate` | `authenticate: any\|user_only\|service_account_only` | JWT/API key validation |
| `rate_limit` | `rate_limit` | Per-subject rate limiting |
| `authorize` | `authorize: {permission, param}` | Check permission on specific resource via path param |
| `authorize` (prefilter) | `authorize: {pattern: prefilter, permission}` | Resolve authorized IDs before querying (for list endpoints) |
| `max_body_size` | `max_body_size: 1048576` | Limit request body size in bytes |
| `unique_to_id` | `unique_to_id: {resolver, param}` | Resolve unique fields (e.g., slug) to resource ID |

### auth_create

Routes with `auth_create` automatically write authorization relationships after a successful create:

```yaml
- func: Create
    path: /tenants
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
    auth_create:
      - "tenant:{tenant_id}#owner@{=subject}"
```

The template syntax uses `{field_name}` for entity fields from the created record and `{=subject}` for the authenticated caller's subject string.

For child entities, multiple relationships can be created:

```yaml
auth_create:
  - "api_key:{api_key_id}#owner@{=subject}"
  - "api_key:{api_key_id}#service_account@service_account:{parent_service_account_id}"
```

### Removing a Generated Route

To stop generating a route's handler, remove it from `bridge.yml`. If you need custom logic for that operation, write your own handler in `http.go` and register it in `routes.go`.

The users bridge demonstrates this — `Create` is commented out of `bridge.yml` because user creation is handled by the authentication bridge's register flow instead.

## Generated Handler Flow

A typical generated handler follows this pattern:

**List:**
1. Parse query parameters (limit, cursor, order, filters) via `parseQueryParamsList()`
2. Parse pagination with `fop.ParsePageStringCursor()`
3. Parse filters into the repository's `FilterList` struct
4. Parse order with `fop.ParseOrder()` using the repository's allowed fields
5. **Prefilter** — call `authorizer.LookupResources()` to get authorized IDs, set `filter.AuthorizedIDs`
6. Call `repository.List()` with filter, order, and page
7. Respond with `PageResponse[Entity]`

**Get:**
1. Extract path parameter
2. Call `repository.Get()`
3. Respond with `RecordResponse[Entity]`

**Update:**
1. Extract path parameter
2. Decode and validate JSON body (`Validate()` is called automatically by `web.DecodeJSON`)
3. Convert to repository input via `ToRepo()`
4. Call `repository.Update()`
5. Respond with `RecordResponse[Entity]`

**Delete:**
1. Extract path parameter
2. If auth relationships exist, call `authorizer.DeleteResourceRelationships()` first
3. Call `repository.Delete()`
4. Respond with 204 No Content

## Error Handling

Generated handlers use two error paths:

- **Expected errors** (not found, already exists, validation) — mapped to HTTP status codes via `web.RespondJSONDomainError()`. These unwrap to `sdk/errs` sentinels.
- **Unexpected errors** — logged with `b.log.ErrorContext()` before responding. The `errs.IsExpected()` check prevents double-logging expected errors.

## Composites

Each domain has a generated composite that groups all entity bridges:

```go
bridges := authreposbridge.NewBridges(log, repos, rateLimiter, authenticator, authorizer)
bridges.AddHttpRoutes(apiGroup)
specs := bridges.OpenAPISpec()
schemas := bridges.AuthSchema()
```

The composite constructor takes the domain's `Repositories` struct and shared dependencies, then constructs each entity bridge internally.

`AuthSchema()` delegates to the customizable `authschema.go` file, which by default returns `GeneratedAuthSchema()`. To add custom relations or permissions to an entity's auth schema, modify `authschema.go`:

```go
func AuthSchema() []authorization.ResourceSchema {
    schemas := GeneratedAuthSchema()
    for i, s := range schemas {
        if s.Name == "project" {
            s.Def.Relations["editor"] = authorization.RelationDef{
                AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}},
            }
            s.Def.Permissions["edit"] = authorization.AnyOf(
                authorization.Direct("editor"),
            )
            schemas[i] = s
        }
    }
    return schemas
}
```

## Customization Points

| What | Where | How |
|---|---|---|
| Add custom routes | `routes.go` | Register below `addGeneratedRoutes()` |
| Add custom handlers | `http.go` | Write handler methods on `Bridge` |
| Customize filters/order | `fop.go` | Override generated query parsing |
| Change middleware order | `bridge.yml` | Reorder middleware array, then regenerate |
| Remove a generated route | `bridge.yml` | Delete the route entry, then regenerate |
| Customize auth schema | `authschema.go` | Modify the return of `AuthSchema()` |
| Override error rendering | `bridge.go` | Pass `WithJSONErrorRenderer()` or `WithHTMLErrorRenderer()` |
