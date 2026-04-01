---
sidebar_position: 3
title: Repositories
---

# Core — Repositories

Repositories are the data access layer. Each database entity gets its own package under `core/repositories/` with a consistent set of files, types, and conventions. The generator bootstraps everything with sensible defaults — entity structs, filter types, pgx queries, cache wrappers, error sentinels — but repositories are not limited to CRUD. Any data access pattern belongs here: complex joins, CTEs, aggregations, bulk operations, or anything else your domain needs.

The generator handles the common cases. For specialized queries, add custom methods to the `Storer` interface and implement them in the pgx store. If a query doesn't fit the annotation model, skip `queries.sql` for that method and write the SQL directly — you're responsible for the structs and function signatures in that case.

## Registration

For generation to run on an entity, it must be registered in `gopernicus.yml` under the appropriate database and domain:

```yaml
databases:
  primary:
    driver: postgres/pgx
    domains:
      auth: [users, sessions, api_keys, ...]
      rebac: [rebac_relationships, groups, invitations, ...]
      tenancy: [tenants]
      events: [event_outbox]
      jobs: [job_queue]
```

The domain name maps to the directory under `core/repositories/` and the entity name maps to the table name.

## Package Structure

Each entity follows the same layout:

```
core/repositories/auth/users/
├── repository.go        # Bootstrap — Storer interface, Repository struct, constructor
├── generated.go         # Entity types, input/filter structs, errors, OrderByFields
├── fop.go               # Pagination defaults (order, direction, limit)
├── cache.go             # Custom cache methods (bootstrap, extensible)
├── generated_cache.go   # Generated CacheStore wrapper
├── queries.sql          # Annotated SQL — the generation source
└── userspgx/
    ├── store.go          # Custom store methods (bootstrap)
    └── generated.go      # Generated pgx Store implementation
```

| File | Generated? | Purpose |
|---|---|---|
| `repository.go` | Bootstrap | `Storer` interface (with markers), `Repository` struct, `NewRepository` |
| `generated.go` | Always | Entity struct, `CreateX`/`UpdateX`/`FilterList` structs, error sentinels, `OrderByFields` |
| `fop.go` | Bootstrap | `DefaultOrderBy`, `DefaultOrderDirection`, `DefaultLimit` |
| `cache.go` | Bootstrap | Custom cache invalidation methods |
| `generated_cache.go` | Always | `CacheStore` wrapper with generated read-through methods |
| `queries.sql` | Hand-written | Annotated SQL that drives code generation |
| `*pgx/store.go` | Bootstrap | Custom store methods (e.g., transactional creates) |
| `*pgx/generated.go` | Always | pgx `Store` struct implementing `Storer` |

## The Storer Interface

The `Storer` interface is the data access contract. Custom methods go above the markers; generated methods live between them:

```go
// repository.go
type Storer interface {
    // Custom methods — never overwritten
    Create(ctx context.Context, input CreateUser) (User, error)

    // gopernicus:start (DO NOT EDIT between markers)
    List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]User, error)
    Get(ctx context.Context, userID string) (User, error)
    Update(ctx context.Context, userID string, input UpdateUser) (User, error)
    SoftDelete(ctx context.Context, userID string) error
    Archive(ctx context.Context, userID string) error
    Restore(ctx context.Context, userID string) error
    Delete(ctx context.Context, userID string) error
    GetByEmail(ctx context.Context, email string) (User, error)
    SetEmailVerified(ctx context.Context, updatedAt time.Time, userID string) error
    SetLastLogin(ctx context.Context, lastLoginAt time.Time, updatedAt time.Time, userID string) error
    // gopernicus:end
}
```

Running `gopernicus generate` regenerates everything between the markers based on `queries.sql`. Your custom methods above the markers are untouched.

For methods that don't fit the annotation model, add the signature above the markers and implement it directly in `store.go`. You're responsible for the types, SQL, and error mapping for these methods — the generator won't touch them.

## Repository Struct

The `Repository` wraps a `Storer` with optional business logic — ID generation and domain event emission:

```go
type Repository struct {
    store      Storer
    generateID func() (string, error)
    bus        events.Bus
}

func NewRepository(store Storer, opts ...Option) *Repository
```

Options:
- `WithGenerateID(fn)` — custom ID generation (default: `cryptids.GenerateID`)
- `WithEventBus(bus)` — enable domain event emission

The Repository delegates most methods directly to the Storer. Custom Repository methods can add behavior — for example, the users Repository has a custom `Create` that generates an ID, calls the store, and emits a `UserCreatedEvent`:

```go
func (r *Repository) Create(ctx context.Context, input CreateUser) (User, error) {
    id, err := r.generateID()
    // ... set input.UserID = id
    user, err := r.store.Create(ctx, input)
    // ... emit UserCreatedEvent via r.bus
    return user, nil
}
```

## Generated Types

For each entity, the generator produces:

**Entity struct** — maps directly to the database row:

```go
type User struct {
    UserID        string     `json:"user_id" db:"user_id"`
    Email         string     `json:"email" db:"email"`
    DisplayName   *string    `json:"display_name" db:"display_name"`
    EmailVerified bool       `json:"email_verified" db:"email_verified"`
    LastLoginAt   *time.Time `json:"last_login_at" db:"last_login_at"`
    RecordState   string     `json:"record_state" db:"record_state"`
    CreatedAt     time.Time  `json:"created_at" db:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}
```

**Create input** — includes fields the caller provides. Use `@fields` to exclude auto-set fields (e.g., `*,-user_id,-created_at`):

```go
type CreateUser struct {
    UserID        string  `json:"user_id,omitempty"`
    Email         string  `json:"email,omitempty"`
    DisplayName   *string `json:"display_name,omitempty"`
    EmailVerified bool    `json:"email_verified,omitempty"`
    RecordState   string  `json:"record_state,omitempty"`
}
```

**Update input** — all fields are pointers (nil = don't change):

```go
type UpdateUser struct {
    Email         *string    `json:"email,omitempty"`
    DisplayName   *string    `json:"display_name,omitempty"`
    EmailVerified *bool      `json:"email_verified,omitempty"`
    LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
}
```

**Filter struct** — all fields optional, supports range queries and search:

```go
type FilterList struct {
    UserID            *string    `json:"user_id,omitempty"`
    Email             *string    `json:"email,omitempty"`
    EmailVerified     *bool      `json:"email_verified,omitempty"`
    LastLoginAtAfter  *time.Time `json:"last_login_at_after,omitempty"`
    LastLoginAtBefore *time.Time `json:"last_login_at_before,omitempty"`
    RecordState       *string    `json:"record_state,omitempty"`
    SearchTerm        *string    `json:"search_term,omitempty"`
    AuthorizedIDs     []string   `json:"-"`  // authorization prefilter
}
```

The `AuthorizedIDs` field supports the prefilter pattern from [Authorization](./auth/authorization.md#resource-lookup): nil means unrestricted (admin bypass), empty means no access, and a populated slice filters by `WHERE id = ANY(@authorized_ids)`.

## Error Sentinels

Each entity package generates domain-specific errors wrapping `sdk/errs` base types:

```go
var (
    ErrUserNotFound         = fmt.Errorf("user: %w", errs.ErrNotFound)
    ErrUserAlreadyExists    = fmt.Errorf("user: %w", errs.ErrAlreadyExists)
    ErrUserInvalidReference = fmt.Errorf("user: %w", errs.ErrInvalidReference)
    ErrUserInvalidInput     = fmt.Errorf("user: %w", errs.ErrInvalidInput)
)
```

The pgx store maps database errors to these sentinels — duplicate key becomes `ErrAlreadyExists`, foreign key violation becomes `ErrInvalidReference`, etc. Callers can check at either level with `errors.Is()`.

## OrderByFields

Generated from `@order` annotations in `queries.sql`:

```go
var OrderByFields = map[string]fop.OrderField{
    "user_id":        {Column: "user_id"},
    "email":          {Column: "email", CastLower: true},
    "display_name":   {Column: "display_name", CastLower: true},
    "created_at":     {Column: "created_at"},
}
```

`CastLower: true` applies `LOWER()` for case-insensitive sorting. The bridge layer validates incoming sort fields against this map.

## Pagination Defaults

Set in `fop.go` (bootstrap file — customize freely):

```go
var (
    DefaultOrderBy        = OrderByCreatedAt
    DefaultOrderDirection = fop.DirectionDesc
    DefaultLimit          = 25
)
```

## queries.sql

The generation source. Annotations in SQL comments drive what gets generated. The `@database` annotation is declared once at the top; `@func` annotations mark the start of each query:

```sql
-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(email, display_name)
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: GetByEmail
SELECT * FROM users WHERE email = @email;

-- @func: SetEmailVerified
UPDATE users SET email_verified = true, updated_at = @updated_at WHERE user_id = @user_id;
```

The annotation system supports field exclusion (`@fields: *,-user_id,-created_at`), multiple named filter groups, various search strategies (`ilike`, `ts_vector`), CTEs, joins, and other advanced SQL patterns. See [Annotations](../../topics/code-generation/annotations.md) for the full reference.

Not every method needs to go through `queries.sql`. For queries that don't fit the annotation model — or when you want full control — add the method signature to the `Storer` interface above the markers and implement it directly in `store.go`. You're responsible for the structs, SQL, error mapping, and tests for these methods.

## CacheStore

The generated `CacheStore` wraps a `Storer` with transparent cache-aside reads and write-through invalidation:

```go
store := userspgx.NewStore(log, db)
cachedStore := users.NewCacheStore(store, cacheInstance)
repo := users.NewRepository(cachedStore, users.WithEventBus(bus))
```

If the cache is nil, the CacheStore passes through to the inner store with no caching. Custom cache methods (e.g., pattern-based invalidation) go in the `cache.go` bootstrap file.

## pgx Store

The generated pgx Store implements the `Storer` interface using `pgxdb.Querier`, which accepts both a connection pool and a transaction:

```go
type Store struct {
    log *slog.Logger
    db  pgxdb.Querier
}

func NewStore(log *slog.Logger, db pgxdb.Querier) *Store
```

The List method implements cursor-based pagination: over-fetches by 1 to detect the next page, supports bidirectional traversal via `forPrevious`, and handles case-insensitive sorting via `LOWER()`. Filter conditions and search terms are built at runtime from the `FilterList` struct.

Custom store methods go in `store.go` — for example, the users entity has a custom `Create` that wraps user and principal insertion in a transaction.

## Schema Conventions

The generator recognizes certain column naming patterns and produces specialized behavior. A brief overview of the key conventions — see [Schema Conventions](../../topics/code-generation/schema-conventions.md) for the full reference.

**Timestamps:**
- **`created_at`** — automatically set to `NOW()` on insert. Excluded from update input types.
- **`updated_at`** — automatically set to `NOW()` on insert and update.

**Record state:**
- **`record_state`** — enables `SoftDelete`, `Archive`, `Restore`, and `Delete` methods. The generated List filter defaults to `record_state = 'active'` when no explicit filter is provided. CSV values are supported (e.g., `"active,archived"` to include both).

**Foreign keys:**
- **`parent_` prefix** — columns like `parent_user_id` signal a parent-child relationship. The generator produces scoped queries, composite delete methods (e.g., `DeleteAllForUser`), and parent-aware list operations.
- **`tenant_id`** — when present, the generator adds automatic tenant scoping to queries.

These conventions keep the generated code predictable. If a column doesn't follow these patterns, it's treated as a regular field with no special behavior.

## Domain Organization

Repositories are grouped by domain:

```
core/repositories/
├── auth/                # Users, sessions, passwords, API keys, OAuth accounts, etc.
├── rebac/               # Groups, invitations, relationships, relationship metadata
├── events/              # Transactional event outbox
├── jobs/                # Job queue (durable deferred processing)
└── tenancy/             # Tenants
```

Each domain has a `generated_composite.go` that wires all its entities together:

```go
type Repositories struct {
    User              *users.Repository
    Session           *sessions.Repository
    UserPassword      *userpasswords.Repository
    // ... all entities in this domain
}

func NewRepositories(log *slog.Logger, db pgxdb.Querier, c *cache.Cache, bus events.Bus) *Repositories
```

The composite constructor builds the full chain for each entity: `pgxStore → CacheStore → Repository`, with event bus injection. The app layer calls `NewRepositories` once and passes the composite to cases and bridges.

See also: [Bridge Repositories](../bridge/repositories.md) for the HTTP handlers that expose these.
