# Query Annotations Reference

Annotations are SQL comments in `queries.sql` files that control code generation.
They follow the syntax `-- @key: value` and appear as **query-level**
annotations (between a `@func` and the SQL statement).

In v2, `queries.sql` contains only **data annotations**. Protocol annotations
(`@http:json`, `@authenticated`, `@authorize`, `@with:permissions`) and auth
schema annotations (`@auth.relation`, `@auth.permission`) have moved to
`bridge.yml`. See the [YAML Configuration Reference](yaml-configuration.md)
for those.

## Query-Level Annotations

### @func

**Required.** Starts a new query block and names the generated Go function.
Must be PascalCase.

```sql
-- @func: ListUsers
-- @func: Get
-- @func: Create
-- @func: GetByEmail
```

### @database

Specifies which database configuration from `gopernicus.yml` this file targets.
Defaults to `"primary"` when omitted. This is a file-level annotation that
appears before any `@func`.

```sql
-- @database: primary
```

### @filter:conditions

Declares which columns are available as query-parameter filters on list
endpoints. Uses the field spec syntax (see below). When present, the SQL must
contain a `$conditions` placeholder.

```sql
-- @filter:conditions *
-- @filter:conditions invitation_status,relation,auto_accept
-- @filter:conditions *,-password_hash,-token_hash
```

The generated filter struct includes pointer fields for each column. Time-typed
columns automatically get `_after` and `_before` range filter variants. A
`SearchTerm` field and `AuthorizedIDs` slice are always included.

### @search

Configures full-text or ILIKE search on the entity. Three search types are
supported:

```sql
-- @search: ilike(email, display_name)
-- @search: web_search(title, body)
-- @search: tsvector(search_vector)
```

When `@search` is omitted but `$filters` is present, search defaults to `ilike`
on all non-enum string columns.

The SQL must include a `$search` placeholder where the search condition belongs:

```sql
WHERE $conditions AND $search
```

### @order

Declares which columns are available for `?order=` sorting on list endpoints.
Uses the field spec syntax. The SQL must contain `$order`.

```sql
-- @order: *
-- @order: *,-password_hash
-- @order: created_at,updated_at,email
```

### @max

Sets the maximum page size for a list endpoint. Requests exceeding this limit
are clamped.

```sql
-- @max: 100
-- @max: 500
```

### @fields

Specifies which columns are included in INSERT or UPDATE operations. On INSERT
queries, this controls the `$fields`/`$values` expansion. On UPDATE queries,
this controls the `SET $fields` expansion. Uses the field spec syntax.

```sql
-- @fields: *,-created_at,-updated_at
-- @fields: *,-user_id,-record_state,-created_at
-- @fields: email,display_name,avatar_url
```

### @returns

Overrides the return type of a query. Instead of returning the full entity, the
generator creates a custom result struct with only the named columns. Useful for
lightweight lookup queries.

```sql
-- @func: GetIDBySlug
-- @returns: user_id
SELECT user_id FROM users WHERE slug = @slug;
```

Note: explicit column lists in `SELECT` or `RETURNING` clauses also trigger
custom result types without needing `@returns`.

### @cache

Enables caching for a read query. The generated `CacheStore` decorator wraps
the method with cache-aside logic and automatic invalidation on writes.

```sql
-- @cache: ttl=5m
```

### @event

Declares a domain event to emit after this operation completes.

```sql
-- @event: UserCreated
```

### @scan

Controls how rows are scanned from the database result set.

### @type

Provides type hints for the generator when automatic type inference is
insufficient.

## Field Spec Syntax

Several annotations use a shared column-spec syntax:

| Pattern | Meaning |
|---------|---------|
| `*` | All columns from the reflected schema |
| `-col` | Exclude a column |
| `col1,col2` | Include only these columns |
| `*,-col1,-col2` | All columns except the excluded ones |

Examples:
```
*                          -- all columns
*,-password_hash           -- all except password_hash
*,-created_at,-updated_at  -- all except timestamps
email,display_name         -- only these two
```

## SQL Placeholders

Placeholders in the SQL body drive runtime query building:

| Placeholder | Purpose | Required annotation |
|-------------|---------|-------------------|
| `@param_name` | Named parameter (maps to column type) | none |
| `$conditions` | Dynamic WHERE conditions from filter struct | `@filter:conditions` |
| `$search` | Search clause (ILIKE or tsvector) | `@search` (or defaults from `$filters`) |
| `$order` | Dynamic ORDER BY from parsed `?order=` param | `@order` |
| `$limit` | Page size (clamped by `@max`) | `@max` |
| `$fields` | Column list for INSERT/UPDATE | `@fields` |
| `$values` | Value placeholders for INSERT | `@fields` (on INSERT) |

## Complete Example

In v2, `queries.sql` contains only data annotations. Protocol and auth
annotations are in `bridge.yml`:

**queries.sql:**
```sql
-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(title, body)
-- @order: *
-- @max: 100
SELECT *
FROM questions
WHERE tenant_id = @tenant_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM questions
WHERE question_id = @question_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
-- @event: QuestionCreated
INSERT INTO questions
($fields)
VALUES ($values)
RETURNING *;
```

**bridge.yml** (in the bridge package directory):
```yaml
routes:
  - func: List
    path: /tenants/{tenant_id}/questions
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read

  - func: Get
    path: /tenants/{tenant_id}/questions/{question_id}
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: read
          param: question_id

  - func: Create
    path: /tenants/{tenant_id}/questions
    params_to_input:
      - tenant_id
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
      - authorize:
          permission: create
          param: tenant_id

auth_relations:
  - "tenant(tenant)"
  - "owner(user, service_account)"

auth_permissions:
  - "list(tenant->list)"
  - "create(tenant->manage)"
  - "read(owner|tenant->read)"
  - "update(owner|tenant->manage)"
  - "delete(owner|tenant->manage)"
  - "manage(owner|tenant->manage)"
```

---

**Related:**
- [Code Generation Overview](overview.md)
- [YAML Configuration Reference](yaml-configuration.md)
- [Generated File Map](generated-file-map.md)
