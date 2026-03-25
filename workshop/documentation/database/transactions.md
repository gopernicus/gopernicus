# Transaction Patterns

> How gopernicus handles database transactions across stores and use cases.

## Design Principles

1. **Explicit** — Transaction boundaries are visible in the code, never hidden in context
2. **Short-lived** — Transactions only span the actual multi-table operation
3. **Repository-scoped** — Bridge and app layers are unaware of transaction boundaries
4. **Pragmatic** — Not everything needs a transaction; cross-backend operations use eventual consistency

## The Querier Interface

`pgxdb.Querier` defines the common interface satisfied by both `*pgxpool.Pool` and `pgx.Tx`:

```go
// infrastructure/database/postgres/pgxdb/querier.go
type Querier interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
```

Generated stores use `*Pool` directly — not `Querier`. This is intentional:
- Stores need `pool.Begin()` for store-level transactions, which `Querier` doesn't expose
- `Querier` exists as a hook for future transaction composition repos

## Transaction Tiers

### Tier 1: No Transaction Needed

Single-store CRUD operations. The vast majority of operations.

```go
// One store call = one query = no tx needed
user, err := usersStore.Get(ctx, userID)
```

### Tier 2: Store-Level Transactions

When a single domain concept spans multiple tables, the store handles the transaction internally. The caller has no idea a transaction is involved.

```go
// store.go (bootstrap file — safe to edit)
func (s *Store) Create(ctx context.Context, input users.CreateUser) (users.User, error) {
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return users.User{}, err
    }
    defer tx.Rollback(ctx)

    // Insert principal first (FK constraint)
    _, err = tx.Exec(ctx, `INSERT INTO principals ...`, ...)
    if err != nil {
        return users.User{}, err
    }

    // Insert user
    rows, err := tx.Query(ctx, `INSERT INTO users ...`, ...)
    if err != nil {
        return users.User{}, err
    }
    defer rows.Close()
    user, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[users.User])
    if err != nil {
        return users.User{}, err
    }

    if err := tx.Commit(ctx); err != nil {
        return users.User{}, err
    }
    return user, nil
}
```

**When to use**: Tables that are tightly coupled and always change together (same domain concept, same database).

### Tier 3: Use Cases Without Transactions

Cases orchestrate operations that span different backends or where eventual consistency is acceptable.

```go
func (c *OrgCase) CreateWithOwner(ctx context.Context, input CreateOrg) (Org, error) {
    // Step 1: Create org in PostgreSQL
    org, err := c.orgsRepo.Create(ctx, input)
    if err != nil {
        return Org{}, err
    }

    // Step 2: Create auth relationship (could be SpiceDB, Permify, etc.)
    // Intentionally NOT transactional — allows swapping auth backends
    err = c.authRepo.Create(ctx, rebacrelationships.CreateRebacRelationship{
        ResourceType: "org",
        ResourceID:   org.OrgID,
        Relation:     "owner",
        SubjectType:  "user",
        SubjectID:    input.CreatorID,
    })
    if err != nil {
        return Org{}, err
    }

    return org, nil
}
```

**When to use**: Operations spanning different databases/backends, or where partial completion is recoverable.

### Tier 4: Transaction Composition Repos (Future)

For operations that span multiple domain concepts and **must** be atomic within PostgreSQL.

```
core/repositories/
├── auth/
├── tenancy/
└── txcompositions/          # Future
    ├── checkoutrepo/        # subscription + org + quotas
    └── orgbootstraprepo/    # org + groups + memberships
```

These repos receive the pool directly and manage the full transaction:

```go
type CheckoutRepo struct {
    pool *pgxdb.Pool
}

func (r *CheckoutRepo) Complete(ctx context.Context, input CheckoutInput) (Subscription, error) {
    tx, err := r.pool.Begin(ctx)
    if err != nil {
        return Subscription{}, err
    }
    defer tx.Rollback(ctx)

    // Multiple domain tables in one transaction
    _, err = tx.Exec(ctx, `INSERT INTO subscriptions ...`, ...)
    _, err = tx.Exec(ctx, `UPDATE orgs SET billing_status = ... `, ...)
    _, err = tx.Exec(ctx, `INSERT INTO usage_quotas ...`, ...)

    if err := tx.Commit(ctx); err != nil {
        return Subscription{}, err
    }
    return sub, nil
}
```

## Decision Guide

| Scenario | Approach | Transaction? |
|----------|----------|-------------|
| Single CRUD operation | Store method | No |
| One concept, multiple tables | Store-level `pool.Begin()` | Yes (internal) |
| Multiple concepts, different backends | Use case, no tx | No |
| Multiple concepts, must be atomic, same DB | txcomposition repo | Yes (explicit) |

## Why Not Context-Embedded Transactions?

Some frameworks embed `pgx.Tx` in `context.Context` so stores silently check for a transaction:

```go
// Anti-pattern for gopernicus
func (s *Store) getDB(ctx context.Context) pgxdb.Querier {
    if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
        return tx
    }
    return s.pool
}
```

We avoid this because:

- **Violates visibility** — You can't tell from a call site whether you're in a transaction
- **Interface dishonesty** — Same function signature, different behavior based on hidden state
- **Cache hazard** — Cache decorators can't distinguish committed from uncommitted data
- **Testing complexity** — Must mock context values rather than standard DI

## pgx Transaction Behavior

- `pool.Begin(ctx)` acquires a connection and starts a transaction
- `pgx.Tx` wraps the borrowed connection, does NOT open new connections
- `tx.Commit()` or `tx.Rollback()` returns the connection to the pool
- Always use `defer tx.Rollback(ctx)` — it's a no-op after commit
- `tx.Begin(ctx)` inside a tx creates a savepoint (nested transaction)
