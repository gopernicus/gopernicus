---
title: Extending Generated Code
---

# Extending Generated Code

Every entity gets two kinds of files: **regenerated** files (overwritten on each `gopernicus generate`) and **bootstrap** files (created once, then yours). The bootstrap files are where you add custom behavior — the generator won't touch them again.

This page covers the patterns for extending each layer.

## The Storer interface and markers

The `Storer` interface in `repository.go` is a bootstrap file with a special feature: **marker comments** that let the generator update the interface signature without overwriting your custom methods.

```go
type Storer interface {
    // Your custom methods go here — above the markers
    Checkout(ctx context.Context, workerID string, now time.Time) (JobQueue, error)
    Complete(ctx context.Context, jobID string, now time.Time) error

    // gopernicus:start (DO NOT EDIT between markers)
    List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]JobQueue, error)
    Get(ctx context.Context, jobID string) (JobQueue, error)
    Create(ctx context.Context, input CreateJobQueue) (JobQueue, error)
    Update(ctx context.Context, jobID string, input UpdateJobQueue) (JobQueue, error)
    Delete(ctx context.Context, jobID string) error
    // gopernicus:end
}
```

When you add a new `@func` to `queries.sql` and regenerate, the generator replaces everything between `gopernicus:start` and `gopernicus:end` with the updated method signatures. Everything above the start marker is preserved.

**Rule:** Add custom `Storer` methods above the `gopernicus:start` marker. Never edit between the markers — your changes will be lost on the next generation.

## Adding custom store methods

Custom SQL that doesn't fit the annotation model goes in the bootstrap `store.go` file under the pgx package:

```go
// core/repositories/jobs/jobqueue/jobqueuepgx/store.go

func (s *Store) Checkout(ctx context.Context, workerID string, now time.Time) (jobqueue.JobQueue, error) {
    query := `
        UPDATE job_queue
        SET status = 'STAGED', worker_name = @worker_name, staged_at = @now
        WHERE job_id = (
            SELECT job_id FROM job_queue
            WHERE status = 'PENDING' AND scheduled_for <= @now
            ORDER BY priority DESC, created_at
            FOR UPDATE SKIP LOCKED
            LIMIT 1
        )
        RETURNING *`

    args := pgx.NamedArgs{"worker_name": workerID, "now": now}

    rows, err := s.db.Query(ctx, query, args)
    if err != nil {
        return jobqueue.JobQueue{}, pgxdb.HandlePgError(err)
    }
    defer rows.Close()

    record, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[jobqueue.JobQueue])
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return jobqueue.JobQueue{}, workers.ErrNoWork
        }
        return jobqueue.JobQueue{}, pgxdb.HandlePgError(err)
    }
    return record, nil
}
```

The custom method:
1. Lives in `store.go` (bootstrap, never overwritten)
2. Uses the same `*Store` receiver and `s.db` pool as generated methods
3. Returns types from the repository package (`jobqueue.JobQueue`)
4. Uses the same error handling pattern (`pgxdb.HandlePgError`)

Add the method signature to the `Storer` interface (above the markers) to complete the contract.

## Adding custom repository methods

The `Repository` struct in `repository.go` delegates to `Storer`. To add business logic around a custom store method, add a method on `Repository`:

```go
// core/repositories/jobs/jobqueue/repository.go

func (r *Repository) ClaimNext(ctx context.Context, workerID string) (JobQueue, error) {
    job, err := r.store.Checkout(ctx, workerID, time.Now().UTC())
    if err != nil {
        return JobQueue{}, fmt.Errorf("claiming next job: %w", err)
    }
    return job, nil
}
```

Repository methods can also use `r.generateID` for ID generation and `r.bus` for event emission — the same infrastructure available to generated methods.

## Customizing generated query behavior

If a generated query doesn't do what you need, you have two options:

### Option 1: Remove the query and write it yourself

Delete the entire query block (annotations + SQL) from `queries.sql`. On the next `gopernicus generate`, the method is removed from the generated code. Write your own version in `store.go` and add the signature to `Storer` above the markers.

This is the right choice when the query shape fundamentally differs from what the generator produces.

### Option 2: Keep the `@func` and override at the repository level

Keep the generated store method but wrap it with custom logic in a repository method. For example, if the generated `Create` works but you need to validate or transform input:

```go
func (r *Repository) CreateWithDefaults(ctx context.Context, input CreateJobQueue) (JobQueue, error) {
    if input.Priority == 0 {
        input.Priority = 5
    }
    input.ScheduledFor = time.Now().UTC()
    return r.store.Create(ctx, input)
}
```

## Custom cache methods

The `cache.go` bootstrap file is where custom cache invalidation goes:

```go
// core/repositories/tenancy/tenants/cache.go

func (s *CacheStore) InvalidateForTenant(ctx context.Context, tenantID string) {
    _ = s.cache.DeletePattern(ctx, s.config.KeyPrefix+":*")
}
```

The generated `CacheStore` in `generated_cache.go` handles read-through caching and write-through invalidation for generated methods. Custom cache methods extend that with domain-specific patterns.

## Custom bridge handlers

The `bridge.go`, `routes.go`, and `http.go` files in each bridge package are all bootstrap files. To add custom HTTP endpoints:

1. Add a handler method in `http.go`:

```go
func (b *Bridge) httpArchive(w http.ResponseWriter, r *http.Request) {
    id := web.Param(r, "question_id")
    if err := b.repo.Archive(r.Context(), id); err != nil {
        web.RespondJSONDomainError(w, err)
        return
    }
    web.RespondNoContent(w)
}
```

2. Register it in `routes.go`:

```go
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
    // Custom route
    group.PUT("/{question_id}/archive", b.httpArchive,
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.AuthorizeParam(b.authorizer, b.log, b.jsonErrors, "Question", "manage", "question_id"),
    )

    // Generated routes
    b.addGeneratedRoutes(group)
}
```

Custom routes are registered alongside generated ones — `addGeneratedRoutes` is a separate method that the generator manages.

## Custom FOP defaults

The `fop.go` bootstrap file controls default ordering, direction, and page size:

```go
// core/repositories/tenancy/tenants/fop.go

var (
    DefaultOrderBy  = OrderByCreatedAt
    DefaultDirection = fop.DESC
    DefaultLimit     = 25
)
```

Change these to match your domain's natural ordering. The generated list handlers use these defaults when the caller doesn't specify.

## File quick reference

| File | Bootstrap? | What to customize |
|---|---|---|
| `repository.go` | Yes | `Storer` interface (above markers), `Repository` methods, options |
| `generated.go` | No | Entity types, input structs, errors — **don't edit** |
| `fop.go` | Yes | Default order, direction, limit |
| `cache.go` | Yes | Custom cache invalidation |
| `generated_cache.go` | No | CacheStore wrapper — **don't edit** |
| `*pgx/store.go` | Yes | Custom SQL methods |
| `*pgx/generated.go` | No | Generated pgx implementations — **don't edit** |
| `*bridge/bridge.go` | Yes | Bridge struct, constructor, options |
| `*bridge/routes.go` | Yes | Custom route registration |
| `*bridge/http.go` | Yes | Custom HTTP handlers |
| `*bridge/generated.go` | No | Generated handlers — **don't edit** |
| `bridge.yml` | Hand-written | Route config for generated handlers |
| `queries.sql` | Hand-written | Annotated SQL for generated store methods |

## When to use cases instead

If your custom logic orchestrates **multiple repositories**, emits events, or enforces cross-entity business rules, it belongs in a [case](../core/cases.md) — not in a repository. Repositories handle single-entity data access; cases handle workflows.

```
Repository: "get user by ID"
Case:       "register user, hash password, create verification code, emit event"
```

Cases get their own bridge packages under `bridge/cases/` with hand-written HTTP handlers.
