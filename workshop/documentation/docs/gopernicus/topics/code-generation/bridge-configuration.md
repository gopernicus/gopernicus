---
sidebar_position: 4
title: Bridge Configuration
---

# Bridge Configuration — bridge.yml Reference

Each entity bridge has a `bridge.yml` file that defines HTTP routes, middleware, and authorization schema. The generator reads this file alongside `queries.sql` to produce HTTP handlers, route registration, and OpenAPI specs.

```yaml
entity: Question
repo: questions/questions
domain: questions

auth_relations:
  - "question(question)"
  - "owner(user, service_account)"

auth_permissions:
  - "read(owner|question->read)"
  - "manage(owner|question->manage)"
  - "list(question->list)"

routes:
  - func: List
    path: /questions
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read
          subject: "question:tenant_id"
```

---

## Root-level fields

### `entity`

PascalCase entity name. Used as the base for generated request/response types (`CreateQuestionRequest`, `UpdateQuestionRequest`, etc.).

### `repo`

Repository package path relative to `core/repositories/`. The generator uses this to import the correct repository and resolve query functions.

```yaml
repo: auth/users        # → core/repositories/auth/users
repo: tenancy/tenants   # → core/repositories/tenancy/tenants
```

### `domain`

Domain name for the entity. Used to construct import paths and scope operations.

---

## Authorization schema

### `auth_relations`

Defines what relationships can exist between subjects and this resource type:

```yaml
auth_relations:
  - "question(question)"              # question-to-question (parent)
  - "owner(user, service_account)"    # users and SAs can be owners
  - "viewer(user)"                    # only users can be viewers
```

Each entry follows `relation_name(subject_type, subject_type, ...)`.

### `auth_permissions`

Maps permission names to the relations that grant them:

```yaml
auth_permissions:
  - "read(owner|viewer|question->read)"
  - "update(owner|question->manage)"
  - "delete(owner|question->manage)"
  - "list(question->list)"
```

Each entry follows `permission_name(rule|rule|...)` where rules can be:

| Rule | Meaning |
|---|---|
| `owner` | Direct — subject has this relation to the resource |
| `question->read` | Through — traverse the `question` relation, then check `read` permission on the target |

Multiple rules are OR-combined: the subject needs any one of them.

---

## Routes

Each route maps a repository function to an HTTP endpoint.

### `func`

The repository function name from `queries.sql` (e.g., `List`, `Get`, `Create`, `Update`, `Delete`, `SoftDelete`).

### `path`

URL path with `{param}` placeholders for path parameters:

```yaml
path: /questions/{question_id}
path: /tenants/{tenant_id}/questions
path: /tenants/by/slug/{slug}
```

### `method`

HTTP method. If omitted, inferred from the query category:

| Query category | Inferred method |
|---|---|
| list, scan_one, scan_many | `GET` |
| create | `POST` |
| update, update_returning | `PUT` |
| exec (delete) | `DELETE` |

Set `method` explicitly when the default doesn't fit — for example, soft-delete uses an UPDATE query but you might want `PUT /questions/{id}/delete`.

### `params_to_input`

Extracts path parameters and sets them on the repository input struct. Used for parent-scoped creates where the FK comes from the URL, not the request body:

```yaml
- func: Create
  path: /tenants/{tenant_id}/questions
  params_to_input:
    - tenant_id
  middleware:
    - max_body_size: 1048576
    - authenticate: any
    - authorize:
        permission: create
        param: tenant_id
```

The generated handler sets `input.TenantID = tenantID` before calling the repository.

### `with_permissions`

For single-record GET endpoints, includes the authenticated subject's relationship and permissions in the response:

```yaml
- func: Get
  path: /questions/{question_id}
  with_permissions: true
  middleware:
    - authenticate: any
    - authorize:
        permission: read
        param: question_id
```

Response wraps the record in a `RecordResponse` with `relationship` and `permissions` fields.

### `auth_create`

Creates authorization relationships after a successful create. Uses a tuple format:

```yaml
auth_create:
  - "question:{question_id}#owner@{=subject}"
  - "question:{question_id}#question@question:{tenant_id}"
```

The syntax is `resource_type:{resource_id}#relation@subject_type:{subject_id}` where:

| Placeholder | Resolves to |
|---|---|
| `{field_name}` | Field from the created record (e.g., `record.QuestionID`) |
| `{=subject}` | Authenticated subject from context |
| Literal | Used as-is |

---

## Middleware

Middleware is applied in declaration order. Each entry is one of:

### `authenticate`

Validates the request's authentication credentials and sets the subject in context.

```yaml
- authenticate: any              # User or service account
- authenticate: user             # User only
- authenticate: service_account  # Service account only
- authenticate: user_session     # User with full session validation
```

### `authorize`

Checks whether the authenticated subject has permission on the resource.

**Check pattern** (default) — for single-resource endpoints:

```yaml
- authorize:
    permission: read
    param: question_id    # Path param identifying the resource
```

Calls `authorizer.Check()` with the resource ID from the path parameter. Use for Get, Update, Delete.

**Prefilter pattern** — for list endpoints:

```yaml
- authorize:
    pattern: prefilter
    permission: read
    subject: "question:tenant_id"  # Optional: explicit subject
```

Calls `authorizer.LookupResources()` before the query to get authorized IDs, then injects them into the filter. The query only returns records the subject can access.

**Postfilter pattern** — for list endpoints where prefiltering isn't practical:

```yaml
- authorize:
    pattern: postfilter
    permission: read
```

Fetches records in a loop, batch-checking authorization for each page, accumulating authorized records until the requested page size is reached. Uses `PostfilterLoop` with 2x overfetch.

The `entity` field overrides the resource type if it differs from the bridge entity:

```yaml
- authorize:
    permission: manage
    param: parent_service_account_id
    entity: ServiceAccount           # Check against SA, not APIKey
```

### `rate_limit`

Applies rate limiting with auth-aware keying (authenticated user ID or client IP):

```yaml
- rate_limit
```

### `max_body_size`

Limits request body size in bytes:

```yaml
- max_body_size: 1048576    # 1 MB
- max_body_size: 65536      # 64 KB
```

### `unique_to_id`

Resolves a unique field (like a slug) to a resource ID before subsequent middleware runs. Useful when the URL uses a slug but authorization needs the ID:

```yaml
- unique_to_id:
    resolver: GetIDBySlug       # Repository function to call
    param: slug                 # Path param with the lookup value
    target_param: tenant_id     # Inject resolved ID as this param
    id_field: TenantID          # Go field on the resolver result
```

After resolution, downstream middleware and handlers see `tenant_id` as if it were in the original URL.

### Raw Go expressions

Any string that doesn't match a known middleware type is inserted directly as a Go expression:

```yaml
- myCustomMiddleware(b.log)
```

---

## Generated output

For each route, the generator produces:

- **Handler method** on `*Bridge` — parses request, calls repository, handles errors, responds
- **Request/response types** — `Create{Entity}Request` and `Update{Entity}Request` with `Validate()` and `ToRepo()` methods
- **Route registration** in `addGeneratedRoutes()` — wires handler + middleware to the router
- **OpenAPI spec** entries — documents the endpoint with method, path, request/response schemas

---

## Complete example

A tenant-scoped entity with full CRUD, slug resolution, and authorization:

```yaml
entity: Tenant
repo: tenancy/tenants
domain: tenancy

auth_relations:
  - "owner(user, service_account)"
  - "admin(user)"
  - "member(user)"

auth_permissions:
  - "read(owner|admin|member)"
  - "update(owner|admin)"
  - "delete(owner)"
  - "manage(owner|admin)"

routes:
  - func: List
    path: /tenants
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read

  - func: Get
    path: /tenants/{tenant_id}
    with_permissions: true
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: read
          param: tenant_id

  - func: GetBySlug
    path: /tenants/by/slug/{slug}
    middleware:
      - authenticate: any
      - unique_to_id:
          resolver: GetIDBySlug
          param: slug
          target_param: tenant_id
          id_field: TenantID
      - rate_limit
      - authorize:
          permission: read
          param: tenant_id

  - func: Create
    path: /tenants
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
    auth_create:
      - "tenant:{tenant_id}#owner@{=subject}"

  - func: Update
    path: /tenants/{tenant_id}
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
      - authorize:
          permission: update
          param: tenant_id

  - func: SoftDelete
    method: PUT
    path: /tenants/{tenant_id}/delete
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: delete
          param: tenant_id

  - func: Delete
    path: /tenants/{tenant_id}
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: delete
          param: tenant_id
```
