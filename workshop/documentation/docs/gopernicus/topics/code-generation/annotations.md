---
sidebar_position: 2
title: Annotations
---

# Annotations — queries.sql Reference

Annotations are comments in `queries.sql` that tell the generator what to produce. Each `@func` starts a new query block; all other annotations modify that block's behavior.

```sql
-- @database: primary

-- @func: ListUsers
-- @filter:conditions *,-record_state
-- @search: ilike(email, display_name)
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;
```

---

## File-level annotations

### `@database`

Specifies which database connection pool to use for all queries in the file.

```sql
-- @database: analytics
```

Must match a database name in `gopernicus.yml`. Defaults to `"primary"` if omitted. Must appear before the first `@func`.

---

## Query-level annotations

### `@func`

Marks the start of a query block and names the generated Go method.

```sql
-- @func: CreateUser
INSERT INTO users ($fields) VALUES ($values) RETURNING *;
```

The name becomes the method on both the `Storer` interface and the `Repository` struct (e.g., `func (r *Repository) CreateUser(...)`). Every query must have exactly one `@func`. The block ends at the `;` terminator.

---

### `@filter`

Defines a named dynamic WHERE clause for list queries.

```sql
-- @filter:<name> <field_spec>
```

The name maps to a `$<name>` placeholder in the SQL:

```sql
-- @func: ListUsers
-- @filter:conditions *,-record_state
-- @filter:status record_state
SELECT * FROM users
WHERE $conditions AND $status
ORDER BY $order
LIMIT $limit;
```

This generates:

- A `FilterListUsers` struct with typed fields for each filter
- Builder functions that construct WHERE clauses at runtime
- A `Search` field on the filter struct (if `@search` is present)

Multiple `@filter` annotations on the same query are supported — they're AND-combined.

**Field spec syntax** is shared with `@fields`, `@order`, and `@returns`:

| Spec | Meaning |
|---|---|
| `*` | All columns from the table |
| `*,-col1,-col2` | All except named columns |
| `col1, col2` | Only these columns |

See [Field Specs](#field-specs) for details.

---

### `@search`

Declares which columns are searchable and the search method.

```sql
-- @search: ilike(email, display_name)
```

**Search types:**

| Type | SQL generated | Use when |
|---|---|---|
| `ilike(col1, col2)` | `col ILIKE '%' \|\| @search \|\| '%'` | Simple substring matching |
| `web_search(col1, col2)` | `col @@ websearch_to_tsquery(@search)` | Natural language search (PostgreSQL 11+) |
| `tsvector(search_vector)` | `col @@ plainto_tsquery(@search)` | Pre-computed full-text search column |

If you omit the type prefix, `ilike` is assumed: `-- @search: email, name` is the same as `-- @search: ilike(email, name)`.

If `@search` is omitted but `@filter` is present, the generator defaults to `ilike` on all text columns in the table.

The search clause maps to the `$search` placeholder in SQL.

---

### `@order`

Specifies which columns can be used for dynamic ORDER BY.

```sql
-- @order: *,-created_at
```

Generates `OrderByFields` constants for each allowed column (ascending and descending). The fop.go bootstrap file controls the default order, direction, and limit.

Maps to the `$order` placeholder in SQL. Lines containing `$order` or `$limit` are stripped from the base SQL and rebuilt dynamically at runtime.

---

### `@max`

Sets the maximum page size for a list query.

```sql
-- @max: 100
```

The generated code clamps the requested limit to this value. Maps to the `$limit` placeholder.

---

### `@fields`

Specifies which columns are included in INSERT or UPDATE operations.

```sql
-- @func: CreateUser
-- @fields: *,-created_at,-updated_at,-user_id
INSERT INTO users ($fields) VALUES ($values) RETURNING *;

-- @func: UpdateUser
-- @fields: *,-user_id,-created_at,-record_state
UPDATE users SET $fields WHERE user_id = @user_id RETURNING *;
```

For INSERT: generates a `CreateUser` input struct with one field per included column. For UPDATE: generates an `UpdateUser` input struct with pointer fields (nil = don't update).

`$fields` expands to the comma-separated column list. `$values` expands to the corresponding `@column_name` parameter placeholders.

**Timestamp handling:** If `created_at` or `updated_at` are excluded from `@fields`, the generated store method injects `time.Now().UTC()` automatically. If you include them, you're responsible for providing the value.

---

### `@returns`

Explicitly specifies the return columns, overriding AST-based inference.

```sql
-- @func: GetIDBySlug
-- @returns: user_id
SELECT user_id FROM users WHERE slug = @slug;

-- @func: IncrementAttempts
-- @returns: attempt_count
UPDATE verification_codes
SET attempt_count = attempt_count + 1
WHERE identifier = @identifier
RETURNING attempt_count;
```

When the returned columns differ from the full entity struct, the generator creates a custom result type (e.g., `GetIDBySlugResult`).

Most queries don't need `@returns` — the generator parses your SELECT/RETURNING clause automatically. Use it when AST inference can't resolve the columns (complex CTEs, computed expressions, ambiguous aliases).

---

### `@cache`

Declares that query results should be cached.

```sql
-- @func: GetUser
-- @cache: 5m
SELECT * FROM users WHERE user_id = @user_id;
```

Supported units: `s` (seconds), `m` (minutes), `h` (hours). The generated `CacheStore` wrapper handles cache reads and write-through; invalidation strategy is up to your application.

---

### `@event`

Declares that a mutation should emit a domain event.

```sql
-- @func: SetEmailVerified
-- @event: user.email_verified
UPDATE users SET email_verified = true WHERE user_id = @user_id;
```

The repository method constructs the event and calls `bus.Emit()` after the query completes.

**Transactional outbox:** Add `outbox` to write the event atomically with the mutation:

```sql
-- @func: CreatePrincipal
-- @event: principal.created outbox
INSERT INTO principals ($fields) VALUES ($values) RETURNING *;
```

With `outbox`, the store method wraps the query in a transaction and writes to the `event_outbox` table alongside the mutation. A separate poller publishes committed events to the bus.

Without `outbox`, the event is emitted after the query succeeds — if the process crashes between the query and the emit, the event is lost.

> **Note:** `@event` does not generate the event struct. You must define it yourself (e.g., `type PrincipalCreatedEvent struct { ... }`).

---

### `@type`

Explicitly sets the Go type for a named parameter, overriding inference.

```sql
-- @func: GetUpcomingEvents
-- @type:now time.Time
SELECT * FROM events WHERE starts_at > @now;
```

The syntax is `@type:<param_name> <go_type>` — no space after the colon. The type string is used verbatim in generated code, so it must be a valid Go type.

Useful when the generator can't infer the right type — for example, a parameter that doesn't correspond to any table column, or when you need `uuid.UUID` instead of the inferred `string`.

---

### `@scan`

Overrides the inferred store method category.

```sql
-- @func: CheckoutJob
-- @scan: one
SELECT * FROM job_queue
WHERE status = 'pending'
ORDER BY created_at
LIMIT 1
FOR UPDATE SKIP LOCKED;
```

| Value | Generated return type |
|---|---|
| `one` | `(Entity, error)` |
| `many` | `([]Entity, error)` |
| `exec` | `error` |

The generator normally infers the category from the query shape (filters → list, `@fields` → create/update, etc.). Use `@scan` when inference gets it wrong — typically for queries that use `FOR UPDATE SKIP LOCKED` or other patterns the inferrer doesn't recognize.

---

### `@check_rows`

Controls whether the generated store method checks `RowsAffected` after a DML statement.

```sql
-- @func: CleanupExpired
-- @check_rows: false
DELETE FROM sessions WHERE expires_at < @now;
```

By default, `exec`-category methods check `RowsAffected() == 0` and return `ErrEntityNotFound` if no rows were affected. Set `@check_rows: false` for statements where zero affected rows is a valid outcome — cleanup queries, conditional updates, idempotent deletes.

Only meaningful for `exec` category queries (UPDATE/DELETE without RETURNING).

---

## Field specs

Field specs control which columns are included in `@filter`, `@fields`, `@order`, and `@returns` annotations. The syntax is the same everywhere:

| Spec | Meaning | Example |
|---|---|---|
| `*` | All table columns (schema order) | `@filter:conditions *` |
| `*,-col1,-col2` | All except named | `@fields: *,-created_at,-user_id` |
| `col1, col2` | Only these columns | `@returns: user_id, email` |
| `alias.col` | Qualified name (JOINs) | `@order: u.last_login_at` |

Columns must match the reflected schema. Exclusions use a `-` prefix. Whitespace around commas is ignored.

## SQL placeholders

Placeholders in your SQL are expanded by the generated store code:

| Placeholder | Annotation | Expands to |
|---|---|---|
| `$fields` | `@fields` | Column list for INSERT/UPDATE |
| `$values` | `@fields` | Parameter placeholders for INSERT |
| `$conditions` | `@filter:conditions` | Dynamic WHERE clause |
| `$<name>` | `@filter:<name>` | Named filter clause |
| `$search` | `@search` | Search predicate |
| `$order` | `@order` | ORDER BY clause |
| `$limit` | `@max` | LIMIT value |

## Named parameters

Parameters in SQL use `@` prefix and become Go function arguments:

```sql
WHERE user_id = @user_id AND expires_at > @now
```

Type inference for each parameter follows this priority:

1. `@type:param` annotation (explicit override)
2. Column match in the primary table
3. Column match in any table (for JOINs)
4. SQL comparison context (`expires_at > @now` → type of `expires_at`)
5. Name heuristics (`_at` → `time.Time`, `_count` → `int`, `is_` → `bool`)
6. Fallback: `string`

## Continuation lines

Long annotation values can span multiple lines with `-- |`:

```sql
-- @filter:conditions *,-record_state,-internal_flag
-- | -admin_only,-debug_field
```

The continuation is appended with a space, resulting in `*,-record_state,-internal_flag -admin_only,-debug_field`.

## Category inference

The generator infers the store method template from the query shape:

| Query has... | Category | Return type |
|---|---|---|
| `@filter`, `@order`, or `@max` | list | `([]Entity, Pagination, error)` |
| `@fields` + INSERT + RETURNING | create | `(Entity, error)` |
| `@fields` + UPDATE + RETURNING | update | `(Entity, error)` |
| `@fields` + UPDATE (no RETURNING) | update | `error` |
| SELECT returning rows | scan_one | `(Entity, error)` |
| Custom return columns | scan_one_custom | `(CustomResult, error)` |
| DML without RETURNING | exec | `error` |

Override with `@scan` when inference gets it wrong.
