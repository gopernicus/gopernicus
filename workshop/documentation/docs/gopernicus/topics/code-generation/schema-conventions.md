---
sidebar_position: 3
title: Schema Conventions
---

# Schema Conventions

The generator and scaffolder recognize certain column names and patterns in your database schema. When present, they produce specialized behavior — scoped queries, automatic timestamps, soft-delete filtering, and more.

These are conventions, not requirements. If a column doesn't follow these patterns, it's treated as a regular field.

---

## Scaffolding conventions

These conventions are detected by `gopernicus new repo` and `gopernicus boot repos` when scaffolding `queries.sql` and `bridge.yml`. They affect the initial scaffolded output but not subsequent `gopernicus generate` runs — once the files exist, you own them.

### `tenant_id`

A foreign key column named `tenant_id` referencing the `tenants` table signals multi-tenant scoping.

When detected, the scaffolder:

- Adds `tenant_id = @tenant_id` to all WHERE clauses (list, get, update, delete)
- Excludes `tenant_id` from `@fields` on update (tenants can't be changed)
- Adds `tenant_id` to `params_to_input` in bridge.yml (extracted from URL path, not request body)
- Generates URL paths prefixed with `/tenants/{tenant_id}/`

Example scaffolded output for a `questions` table with `tenant_id`:

```sql
-- @func: List
-- @filter:conditions *,-record_state
SELECT * FROM questions
WHERE tenant_id = @tenant_id AND $conditions AND $search
ORDER BY $order LIMIT $limit;

-- @func: Get
SELECT * FROM questions
WHERE question_id = @question_id AND tenant_id = @tenant_id;
```

> **Note:** `parent_tenant_id` referencing `tenants` is also treated as a tenant column.

### `parent_` prefix

A foreign key column with a `parent_` prefix (e.g., `parent_question_id`) signals a parent-child relationship for create and list scoping.

When detected, the scaffolder:

- Adds the parent column to WHERE clauses alongside any tenant scoping
- Generates nested URL paths (e.g., `/tenants/{tenant_id}/questions/{parent_question_id}/takes`)
- Adds the parent column to `params_to_input` in bridge.yml
- Excludes the parent column from `@fields` on update

An entity can have both tenant and parent scoping:

| Schema | Scaffolded URL path |
|---|---|
| `tenant_id` only | `/tenants/{tenant_id}/questions` |
| `parent_` only | `/service-accounts/{service_account_id}/api-keys` |
| Both | `/tenants/{tenant_id}/questions/{parent_question_id}/takes` |
| Neither | `/widgets` |

### `slug`

A column named `slug` with a **single-column** unique constraint generates `GetBySlug` and `GetIDBySlug` query blocks.

Composite unique slugs (e.g., `UNIQUE(tenant_id, slug)`) are not auto-detected — write the custom query yourself.

### `record_state`

A column named `record_state` generates `SoftDelete`, `Archive`, and `Restore` query blocks in addition to a hard `Delete`.

The scaffolder also:

- Excludes `record_state` from `@filter` specs (using `*,-record_state`)
- Excludes `record_state` from `@fields` on update

---

## Generation conventions

These conventions are detected every time `gopernicus generate` runs, regardless of how the queries.sql was created.

### `created_at`

When `created_at` exists in the schema but is **excluded** from `@fields` on a CREATE query, the generated store method injects `time.Now().UTC()` automatically. If you include it in `@fields`, you're responsible for providing the value.

When `created_at` is present in the orderable fields, it becomes the default sort column with descending direction.

### `updated_at`

Same auto-injection as `created_at` on CREATE. On UPDATE queries, if `updated_at` is excluded from `@fields`, the generated store method auto-sets it to `time.Now().UTC()` — even when no other fields change.

### `record_state` in filters

When a `@filter` includes `record_state`, the generated filter logic defaults to `'active'` when no explicit value is provided. This means list queries automatically exclude soft-deleted and archived records unless the caller explicitly requests them.

The filter supports comma-separated states: passing `"active,archived"` generates `record_state = ANY(@record_states)`.

---

## Type inference conventions

When the generator encounters columns or parameters that aren't in the reflected schema (CTE outputs, computed expressions, query parameters), it falls back to name-based heuristics:

### Column name heuristics

| Pattern | Inferred Go type |
|---|---|
| `is_*`, `has_*`, `can_*`, `was_*` | `bool` |
| `*_at`, `*_date`, `last_*`, contains `timestamp` | `*time.Time` |
| `*_count`, `count_*`, `total_*`, `num_*` | `int64` |

### Parameter name heuristics

| Pattern | Inferred Go type |
|---|---|
| `*_at`, `*_since`, `*_before`, `*_after`, `*_date` | `time.Time` |
| `*_count`, `*_limit`, `*_offset` | `int` |
| `is_*`, `has_*`, `*_flag` | `bool` |
| Everything else | `string` |

These heuristics are the lowest-priority fallback. Schema column types and `@type` annotations always take precedence. See [Annotations — Named parameters](./annotations.md#named-parameters) for the full priority cascade.

---

## Primary key conventions

The generator reads the primary key from the reflected schema. No naming convention is required — any column marked as the primary key works.

For string-typed primary keys that appear in `@fields` on CREATE queries, the generated repository uses `WithGenerateID()` to auto-generate a cryptographic ID (21-char, vowel-free alphabet) when the caller doesn't provide one.

---

## Nullable columns

Nullable columns (from the reflected schema) become pointer types in Go:

- **Entity struct:** `*string`, `*time.Time`, etc.
- **Create input:** Pointer types with `omitempty` JSON tags
- **Update input:** Always pointer types — nil means "don't change this field"
- **Filter fields:** Pointer types — nil means "don't filter on this"

---

## Foreign keys

Foreign keys from the reflected schema are used in two places:

- **Fixture generation:** FK dependencies are resolved to produce `CreateTestEntityWithDefaults` functions that auto-create parent records. The generator topologically sorts entities so parents are created before children.
- **Bridge scaffolding:** FK columns listed in `params_to_input` are extracted from URL path parameters instead of the request body.

Foreign keys don't trigger any special behavior in the generated repository or store code beyond what your SQL specifies.
