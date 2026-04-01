---
sidebar_position: 6
title: FOP (Filter, Order, Pagination)
---

# Bridge — FOP

The bridge FOP package (`bridge/transit/fop/`) provides HTTP response envelope types and a post-filter authorization helper. It bridges the gap between [SDK FOP](../sdk/fop.md) primitives (used in Core) and the JSON shapes returned to API clients.

## Response Envelopes

### PageResponse

Wraps a paginated list of records with cursor-based pagination metadata:

```go
type PageResponse[T any] struct {
    Data       []T            `json:"data"`
    Pagination fop.Pagination `json:"pagination"`
}
```

Produces JSON like:

```json
{
  "data": [{ "user_id": "abc", "email": "..." }, ...],
  "pagination": {
    "limit": 25,
    "next_cursor": "eyJ...",
    "page_total": 25,
    "has_prev": true,
    "previous_cursor": "eyJ..."
  }
}
```

Used by all generated list handlers:

```go
web.RespondJSONOK(w, fopb.PageResponse[users.User]{
    Data:       records,
    Pagination: pagination,
})
```

### RecordResponse

Wraps a single record with optional relationship and permission metadata:

```go
type RecordResponse[T any] struct {
    Record       T        `json:"record"`
    Relationship string   `json:"relationship,omitempty"`
    Permissions  []string `json:"permissions,omitempty"`
}
```

Without permissions: `{"record": {...}}`

With permissions (when the route uses `with_permissions`):

```json
{
  "record": { "user_id": "abc", "email": "..." },
  "relationship": "owner",
  "permissions": ["read", "update", "delete", "manage"]
}
```

Both fields use `omitempty` so regular get handlers produce a clean envelope and permission-aware handlers include the extra fields — no special casing on the frontend.

## Post-Filter Authorization

When prefiltering via `LookupResources` is impractical (e.g., the authorization backend doesn't support it for a resource type, or the relationship graph is too complex), `PostfilterLoop` provides an alternative: fetch pages, batch-check authorization, and accumulate authorized results.

```go
records, pagination, err := fop.PostfilterLoop(
    r.Context(),
    b.authorizer,
    subject,
    "read",
    "user",
    func(rec users.User) string { return rec.UserID },
    func(ctx context.Context, p sdkfop.PageStringCursor) ([]users.User, sdkfop.Pagination, error) {
        return b.userRepository.List(ctx, filter, orderBy, p)
    },
    page,
)
```

### Parameters

| Parameter | Purpose |
|---|---|
| `authorizer` | The authorization service for batch permission checks |
| `subject` | The authenticated caller (`authorization.Subject`) |
| `permission` | Permission to check (e.g., `"read"`) |
| `resourceType` | Resource type for authorization (e.g., `"user"`) |
| `getID` | Extracts the resource ID from a record |
| `list` | Calls the repository's list function with pagination |
| `page` | The requested page (limit + cursor) |

### Algorithm

1. Fetch a batch from the repository with 2x overfetch (to minimize round trips when most records are authorized)
2. Batch-check authorization via `FilterAuthorized` on the fetched IDs
3. Accumulate authorized records
4. Repeat until one of:
   - Accumulated results reach the requested limit
   - Data source is exhausted (returned fewer rows than requested)
   - No next cursor to follow

### Prefilter vs Post-filter

| | Prefilter | Post-filter |
|---|---|---|
| **How** | `LookupResources` returns authorized IDs, passed as `filter.AuthorizedIDs` to the repository | Fetch pages, then `FilterAuthorized` to keep only allowed records |
| **When** | Default for generated list handlers | When prefilter isn't practical |
| **Pros** | Single DB query, exact pagination | Works with any authorization topology |
| **Cons** | Requires `LookupResources` support | Multiple DB round trips, approximate pagination cursor |

Generated list handlers use prefilter by default. Switch to post-filter by replacing the prefilter block in a custom handler.

## Files

| File | Purpose |
|---|---|
| `response.go` | `PageResponse[T]`, `RecordResponse[T]` envelope types |
| `post_filter.go` | `PostfilterLoop` — post-filter authorization for list endpoints |

See also: [SDK FOP](../sdk/fop.md) for the underlying `Pagination`, `PageStringCursor`, `Order`, and `ParseOrder` primitives.
